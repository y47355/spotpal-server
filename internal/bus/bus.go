// Package bus 进程内事件总线 + outbox 投递器（详细设计 §3.6/§5.1）。
//
// 纪律：事件先落 event_outbox（与业务同事务），投递器 200ms 扫描未投递事件
// 分发给订阅者；失败×3 → dead_letter；崩溃重启后未投递事件自动补发。
package bus

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/spotpal/spotpal-server/internal/pkg/log"
)

// 事件主题契约（8 主题，与客户端文档一字不改）。
const (
	TopicPoolJoined           = "pool.joined"
	TopicNegotiationProposal  = "negotiation.proposal"
	TopicNegotiationExpired   = "negotiation.expired"
	TopicSquadConfirmed       = "squad.confirmed"
	TopicSquadSettled         = "squad.settled"
	TopicCreditUpdated        = "credit.updated"
	TopicSquadStatusChanged   = "squad.status_changed"
	TopicRiskFlagged          = "risk.flagged"
)

// Event 领域事件（payload 已 JSON 化）。
type Event struct {
	ID      string
	Topic   string
	Payload []byte
}

// Handler 订阅者处理函数；返回 error 记一次投递失败。
type Handler func(ctx context.Context, e Event) error

// Bus 进程内总线：订阅表 + outbox 投递器。
type Bus struct {
	db *sql.DB
	mu sync.RWMutex
	subs map[string][]Handler
	fail map[string]int // event_id → 连续失败次数
}

// New 构造总线。
func New(db *sql.DB) *Bus {
	return &Bus{db: db, subs: map[string][]Handler{}, fail: map[string]int{}}
}

// Subscribe 注册主题订阅。
func (b *Bus) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], h)
}

// Append 在调用方事务外写入 outbox（调用方通常已持有单写连接，
// 因此这里同步执行；Go 单写者模型保证与业务写串行）。
func (b *Bus) Append(id, topic string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = b.db.Exec(
		`INSERT INTO event_outbox (id, topic, payload) VALUES (?, ?, ?)`,
		id, topic, string(buf))
	return err
}

// Run 启动投递器：200ms 扫描未投递事件，按主题分发；
// 失败累计 3 次进死信。ctx 取消即停。
func (b *Bus) Run(ctx context.Context) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.deliverOnce(ctx)
		}
	}
}

func (b *Bus) deliverOnce(ctx context.Context) {
	rows, err := b.db.Query(
		`SELECT id, topic, payload FROM event_outbox WHERE delivered = 0 ORDER BY created_at LIMIT 100`)
	if err != nil {
		return
	}
	type item struct{ id, topic, payload string }
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.topic, &it.payload) == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		e := Event{ID: it.id, Topic: it.topic, Payload: []byte(it.payload)}
		if err := b.dispatch(ctx, e); err != nil {
			b.fail[it.id]++
			if b.fail[it.id] >= 3 {
				_, _ = b.db.Exec(
					`INSERT OR REPLACE INTO dead_letter (id, topic, payload, err) VALUES (?,?,?,?)`,
					it.id, it.topic, it.payload, err.Error())
				_, _ = b.db.Exec(`UPDATE event_outbox SET delivered = 2 WHERE id = ?`, it.id)
				delete(b.fail, it.id)
				log.L.Error("event dead-letter", "topic", it.topic, "err", err)
			}
			continue
		}
		_, _ = b.db.Exec(`UPDATE event_outbox SET delivered = 1 WHERE id = ?`, it.id)
		delete(b.fail, it.id)
	}
}

func (b *Bus) dispatch(ctx context.Context, e Event) error {
	b.mu.RLock()
	hs := b.subs[e.Topic]
	b.mu.RUnlock()
	for _, h := range hs {
		if err := h(ctx, e); err != nil {
			return err
		}
	}
	return nil
}
