// Package im 搭局群聊模块（轻实现：群消息走 ws_outbox 持久化）。
package im

import (
	"context"
	"database/sql"
)

// Service 群聊服务。
type Service struct {
	db  *sql.DB
	seq func(userID string) (int64, error)
}

// New 构造（seq 分配器由 ws 网关注入）。
func New(db *sql.DB, nextSeq func(userID string) (int64, error)) *Service {
	return &Service{db: db, seq: nextSeq}
}

// Send 群消息入 outbox（ws 网关扫描投递）。
func (s *Service) Send(ctx context.Context, squadID, from, to, text string) error {
	seq, err := s.seq(to)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO ws_outbox (id, user_id, seq, type, payload) VALUES (?,?,?,?,?)`,
		"wo_"+squadID[3:]+"_"+from[2:]+"_"+itoa64(seq), to, seq, "im.message",
		`{"squad_id":"`+squadID+`","from":"`+from+`","text":"`+text+`"}`)
	return err
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [21]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
