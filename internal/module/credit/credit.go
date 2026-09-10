// Package credit 信用分模块：互评 → 信用分更新。
package credit

import (
	"context"
	"database/sql"
)

// Service 信用服务。
type Service struct{ db *sql.DB }

// New 构造。
func New(db *sql.DB) *Service { return &Service{db: db} }

// Update 信用分更新（-5~+5，夹在 [300, 950]）。
func (s *Service) Update(ctx context.Context, uid string, delta int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET credit_score = MIN(950, MAX(300, credit_score + ?)) WHERE id = ?`,
		delta, uid)
	return err
}

// Review 提交互评（UNIQUE(squad_id, reviewer) 幂等）。
func (s *Service) Review(ctx context.Context, squadID, reviewer, reviewee string, rating int, tags, comment string) error {
	if rating < 1 || rating > 5 {
		return sql.ErrNoRows
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO reviews (id, squad_id, reviewer, reviewee, rating, tags, comment)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(squad_id, reviewer) DO NOTHING`,
		"rv_"+squadID[3:]+"_"+reviewer[2:], squadID, reviewer, reviewee, rating, tags, comment); err != nil {
		return err
	}
	// 评分映射：5 星 +5 … 1 星 -5
	return s.Update(ctx, reviewee, (rating-3)*5/2)
}

// ByUser 用户信用概览（「我的」页数据源）。
func (s *Service) ByUser(ctx context.Context, uid string) (int, int, error) {
	var score int
	var squads int
	err := s.db.QueryRowContext(ctx,
		`SELECT credit_score FROM users WHERE id = ?`, uid).Scan(&score)
	if err != nil {
		return 0, 0, err
	}
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM squads WHERE status IN ('SETTLED','REVIEWED')
		 AND (initiator_id = ? OR id IN (SELECT squad_id FROM negotiations WHERE candidate_id = ?))`,
		uid, uid).Scan(&squads)
	return score, squads, nil
}
