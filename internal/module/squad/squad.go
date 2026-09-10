// Package squad 搭局模块：候选池 + 7 状态 8 迁移状态机（契约不变）。
package squad

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// 状态机（与《技术设计文档》4.1 一致）。
const (
	StIDLE        = "IDLE"
	StPOOLED      = "POOLED"
	StNEGOTIATING = "NEGOTIATING"
	StCONFIRMED   = "CONFIRMED"
	StSETTLED     = "SETTLED"
	StREVIEWED    = "REVIEWED"
	StEXPIRED     = "EXPIRED"
)

// transitions 合法迁移表（乐观锁 WHERE status=? 语义兜底）。
var transitions = map[string][]string{
	StIDLE:        {StPOOLED, StEXPIRED},
	StPOOLED:      {StNEGOTIATING, StEXPIRED},
	StNEGOTIATING: {StCONFIRMED, StEXPIRED},
	StCONFIRMED:   {StSETTLED},
	StSETTLED:     {StREVIEWED},
	StREVIEWED:    {},
	StEXPIRED:     {},
}

// ErrIllegalTransition 非法状态迁移。
var ErrIllegalTransition = errors.New("illegal squad status transition")

// Squad 搭局聚合。
type Squad struct {
	ID         string
	Initiator  string
	Status     string
	Activity   string
	TimeWindow string // JSON
	CostMode   string
}

// Service 搭局服务。
type Service struct{ db *sql.DB }

// New 构造。
func New(db *sql.DB) *Service { return &Service{db: db} }

// CanTransition 校验迁移合法性（单测全排列覆盖）。
func CanTransition(from, to string) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Push 上推入池（幂等：UNIQUE(user_id, candidate_id) + REPLACE 语义）。
func (s *Service) Push(ctx context.Context, uid, candidateID, snapshot string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO candidate_pool (id, user_id, candidate_id, intent_snapshot)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, candidate_id) DO UPDATE
		  SET intent_snapshot = excluded.intent_snapshot, status = 'WAITING'`,
		"cp_"+randID(), uid, candidateID, snapshot)
	return err
}

// Pool 池列表。
func (s *Service) Pool(ctx context.Context, uid string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT cp.id, cp.candidate_id, u.nickname, cp.intent_snapshot, cp.status
		FROM candidate_pool cp JOIN users u ON u.id = cp.candidate_id
		WHERE cp.user_id = ? ORDER BY cp.created_at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, cid, nick, snap, st string
		if err := rows.Scan(&id, &cid, &nick, &snap, &st); err == nil {
			out = append(out, map[string]any{
				"id": id, "candidate_id": cid, "nickname": nick,
				"intent": json.RawMessage(snap), "status": st,
			})
		}
	}
	return out, nil
}

// Remove 移除候选（拉黑时也调用）。
func (s *Service) Remove(ctx context.Context, uid, candidateID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM candidate_pool WHERE user_id = ? AND candidate_id = ?`, uid, candidateID)
	return err
}

// Transition 乐观锁状态迁移：UPDATE ... WHERE status=? 返回 0 行即已变（详细设计 §3.1）。
func (s *Service) Transition(ctx context.Context, id, from, to string) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE squads SET status = ?, updated_at = datetime('now') WHERE id = ? AND status = ?`,
		to, id, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrIllegalTransition
	}
	return nil
}

// Get 查询搭局。
func (s *Service) Get(ctx context.Context, id string) (*Squad, error) {
	var sq Squad
	err := s.db.QueryRowContext(ctx, `
		SELECT id, initiator_id, status, activity, time_window, COALESCE(cost_mode,'')
		FROM squads WHERE id = ?`, id).
		Scan(&sq.ID, &sq.Initiator, &sq.Status, &sq.Activity, &sq.TimeWindow, &sq.CostMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	return &sq, err
}

// MySquads 我的搭局列表。
func (s *Service) MySquads(ctx context.Context, uid string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, status, activity, time_window, COALESCE(cost_mode,''), COALESCE(expire_at,'')
		FROM squads WHERE initiator_id = ? OR id IN (
		  SELECT squad_id FROM negotiations WHERE candidate_id = ?)
		ORDER BY created_at DESC LIMIT 50`, uid, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, st, act, tw, cm, ex string
		if err := rows.Scan(&id, &st, &act, &tw, &cm, &ex); err == nil {
			out = append(out, map[string]any{
				"id": id, "status": st, "activity": act,
				"time_window": json.RawMessage(tw), "cost_mode": cm, "expire_at": ex,
			})
		}
	}
	return out, nil
}

// Create 创建搭局（IDLE 起步，默认 48h 过期）。
func (s *Service) Create(ctx context.Context, initiator, activity, timeWindow, costMode string) (string, error) {
	id := "sq_" + randID()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO squads (id, initiator_id, status, activity, time_window, cost_mode, expire_at)
		VALUES (?,?,?,?,?,?,?)`,
		id, initiator, StIDLE, activity, timeWindow, costMode,
		time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return id, nil
}

func randID() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 12)
	for i := range b {
		b[i] = hex[int(time.Now().UnixNano())%16]
		b[i] = hex[b[i]%16]
	}
	return string(b)
}
