// Package risk 风控模块：拉黑/举报 → 事件 risk.flagged → match/squad 消费。
package risk

import (
	"context"
	"database/sql"
)

// Service 风控服务。
type Service struct{ db *sql.DB }

// New 构造。
func New(db *sql.DB) *Service { return &Service{db: db} }

// Block 拉黑：出池 + 风控标记（事件由 bus 分发，match/squad 消费过滤）。
func (s *Service) Block(ctx context.Context, uid, target string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM candidate_pool WHERE user_id = ? AND candidate_id = ?`, uid, target); err != nil {
		return err
	}
	// risk.flagged 事件经 outbox（此处直接写表，投递器统一分发）
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO event_outbox (id, topic, payload) VALUES (?,?,?)`,
		"ev_risk_"+uid[2:]+"_"+target[2:], "risk.flagged",
		`{"by":"`+uid+`","target":"`+target+`","reason":"blocked"}`)
	return err
}
