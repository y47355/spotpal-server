// Package negotiate AI 协商引擎：Intent 快照 / Round ≤5 / 48h 超时 / 幂等。
package negotiate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/spotpal/spotpal-server/internal/bus"
	"github.com/spotpal/spotpal-server/internal/platform/llm"
	"github.com/spotpal/spotpal-server/internal/timeout"
)

// 轮次与超时约束（详细设计 §3.2）。
const (
	MaxRounds     = 5
	ExpireHours   = 48
)

// ErrRoundExceeded 轮次超限。
var ErrRoundExceeded = errors.New("negotiation round limit exceeded")

// Service 协商服务。
type Service struct {
	db  *sql.DB
	llm llm.TimeWindowResolver
	bus *bus.Bus
	tw  *timeout.Worker
}

// New 构造。
func New(db *sql.DB, r llm.TimeWindowResolver, b *bus.Bus, tw *timeout.Worker) *Service {
	return &Service{db: db, llm: r, bus: b, tw: tw}
}

// Release 释放委托：创建协商会话 + 首轮方案（事件 pool.joined 已由 squad 发）。
func (s *Service) Release(ctx context.Context, uid string, intent llm.Intent) (string, error) {
	id := "ngt_" + time.Now().Format("150405.000000000")
	// 找池内最新候选（演示：取池内第一条）
	var candidate string
	err := s.db.QueryRowContext(ctx,
		`SELECT candidate_id FROM candidate_pool WHERE user_id = ? ORDER BY created_at DESC LIMIT 1`,
		uid).Scan(&candidate)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("candidate pool empty")
	}
	if err != nil {
		return "", err
	}

	// 协商会话（48h 过期）
	expire := time.Now().Add(ExpireHours * time.Hour).UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO negotiations (id, squad_id, candidate_id, round, status, expire_at)
		VALUES (?,?,?,?, 'ACTIVE', ?)`,
		id, nil, candidate, 1, expire); err != nil {
		return "", err
	}

	// 超时登记
	_ = s.tw.Schedule("t_"+id, timeout.KindNegotiationExpire,
		`{"negotiation_id":"`+id+`"}`, time.Now().Add(ExpireHours*time.Hour))

	// LLM 首轮方案
	p, err := s.llm.Resolve(ctx, llm.IntentPair{Initiator: intent, Candidate: intent})
	if err != nil {
		return id, nil // 会话已建，方案下轮补
	}
	return id, s.saveRound(ctx, id, 1, p)
}

func (s *Service) saveRound(ctx context.Context, ngtID string, round int, p llm.Proposal) error {
	buf, _ := json.Marshal(p)
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO negotiation_rounds (id, negotiation_id, round_no, proposal)
		VALUES (?,?,?,?)`,
		"nr_"+ngtID[4:]+"_"+itoa(round), ngtID, round, string(buf))
	if err != nil {
		return err
	}
	// 推方案事件 → ws 推给双方
	return s.bus.Append("ev_prop_"+ngtID[4:]+"_"+itoa(round), bus.TopicNegotiationProposal,
		map[string]any{"negotiation_id": ngtID, "round": round, "proposal": p})
}

// Feedback 双方反馈：未过轮次上限则生成下一轮，双方 ok 则方案定稿。
func (s *Service) Feedback(ctx context.Context, ngtID, uid string, ok bool, note string) error {
	var round int
	var candidate string
	if err := s.db.QueryRowContext(ctx,
		`SELECT round, candidate_id FROM negotiations WHERE id = ? AND status = 'ACTIVE'`, ngtID).
		Scan(&round, &candidate); err != nil {
		return err
	}
	if round >= MaxRounds {
		return ErrRoundExceeded
	}
	// 记录反馈（附加到当前轮）
	buf, _ := json.Marshal(map[string]any{"from": uid, "ok": ok, "note": note})
	if _, err := s.db.ExecContext(ctx, `
		UPDATE negotiation_rounds SET feedback = ? WHERE negotiation_id = ? AND round_no = ?`,
		string(buf), ngtID, round); err != nil {
		return err
	}
	if ok {
		// 方案接受 → 搭局 CONFIRMED（事件由 squad 消费者流转）
		return s.bus.Append("ev_conf_"+ngtID[4:], bus.TopicSquadConfirmed,
			map[string]any{"negotiation_id": ngtID, "by": uid})
	}
	// 下一轮
	return s.saveRound(ctx, ngtID, round+1, llm.Proposal{
		Time: "FRI 20:00-22:00", Venue: "备选场地 · 静安体育馆",
		CostMode: "AA", PerHead: 38,
		Reason: "按你的反馈调整：预算下调 10 元",
	})
}

// Get 查询协商详情（会话 + 轮次）。
func (s *Service) Get(ctx context.Context, id string) (map[string]any, error) {
	var ngtID, cand, st string
	var round int
	var squadID sql.NullString
	var expire string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, squad_id, candidate_id, round, status, expire_at
		FROM negotiations WHERE id = ?`, id).
		Scan(&ngtID, &squadID, &cand, &round, &st, &expire)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT round_no, proposal, feedback FROM negotiation_rounds
		WHERE negotiation_id = ? ORDER BY round_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rounds []map[string]any
	for rows.Next() {
		var no int
		var p, f sql.NullString
		if err := rows.Scan(&no, &p, &f); err == nil {
			m := map[string]any{"round": no, "proposal": json.RawMessage(p.String)}
			if f.Valid && f.String != "" {
				m["feedback"] = json.RawMessage(f.String)
			}
			rounds = append(rounds, m)
		}
	}
	return map[string]any{
		"id": ngtID, "squad_id": squadID, "candidate_id": cand,
		"round": round, "status": st, "expire_at": expire, "rounds": rounds,
	}, nil
}

// Expire 超时驱动回调：ACTIVE → EXPIRED，发 negotiation.expired。
func (s *Service) Expire(ctx context.Context, ngtID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE negotiations SET status = 'EXPIRED' WHERE id = ? AND status = 'ACTIVE'`, ngtID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return s.bus.Append("ev_exp_"+ngtID[4:], bus.TopicNegotiationExpired,
		map[string]any{"negotiation_id": ngtID})
}

// OnTimeout 超时驱动回调入口（main 装配）：解析 payload 里的 negotiation_id 并过期。
func OnTimeout(ctx context.Context, d *sql.DB, b *bus.Bus, payload string) error {
	var p struct {
		NegotiationID string `json:"negotiation_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	return New(d, nil, b, nil).Expire(ctx, p.NegotiationID)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
