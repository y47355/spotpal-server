// Package timeout 超时驱动：timeouts 表 + 30s 轮询认领（详细设计 §3.5）。
//
// 单写者模型下无并发竞争，先 SELECT 后 UPDATE 即串行安全（无 SKIP LOCKED）。
package timeout

import (
	"context"
	"database/sql"
	"time"

	"github.com/spotpal/spotpal-server/internal/pkg/log"
)

// 超时种类（与状态机/协商引擎对齐）。
const (
	KindNegotiationExpire = "NEGOTIATION_EXPIRE"
	KindCheckinGrace      = "CHECKIN_GRACE"
	KindReviewDefault     = "REVIEW_DEFAULT"
)

// Handler 认领后的处理器。
type Handler func(ctx context.Context, kind, payload string) error

// Worker 轮询器。
type Worker struct {
	db  *sql.DB
	h   Handler
}

// New 构造。
func New(db *sql.DB, h Handler) *Worker { return &Worker{db: db, h: h} }

// Schedule 注册一条未来超时（due_at 为 ISO 8601 UTC）。
func (w *Worker) Schedule(id, kind, payload string, dueAt time.Time) error {
	_, err := w.db.Exec(
		`INSERT OR REPLACE INTO timeouts (id, due_at, kind, payload) VALUES (?,?,?,?)`,
		id, dueAt.UTC().Format(time.RFC3339), kind, payload)
	return err
}

// Run 每 30s 扫描到期未认领条目：认领→处理→删除；失败重投（claimed_at 置空）。
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	rows, err := w.db.Query(
		`SELECT id, kind, payload FROM timeouts
		 WHERE due_at <= datetime('now') AND claimed_at IS NULL LIMIT 100`)
	if err != nil {
		return
	}
	type item struct{ id, kind, payload string }
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.kind, &it.payload) == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		res, err := w.db.Exec(
			`UPDATE timeouts SET claimed_at = datetime('now') WHERE id = ? AND claimed_at IS NULL`, it.id)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // 已被认领（理论上单写者不会发生，防御性保留）
		}
		if err := w.h(ctx, it.kind, it.payload); err != nil {
			// 失败重投：清空认领，下一轮再来
			_, _ = w.db.Exec(`UPDATE timeouts SET claimed_at = NULL WHERE id = ?`, it.id)
			log.L.Warn("timeout retry", "kind", it.kind, "err", err)
			continue
		}
		_, _ = w.db.Exec(`DELETE FROM timeouts WHERE id = ?`, it.id)
	}
}
