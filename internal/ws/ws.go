// Package ws WebSocket 网关：连接管理 / seq 补拉 / 心跳（详细设计 §3.4）。
package ws

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// Envelope WS 信封：{type, seq, payload, ts}。
type Envelope struct {
	Type    string          `json:"type"`
	Seq     int64           `json:"seq,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Ack     int64           `json:"ack,omitempty"`
	LastSeq int64           `json:"last_seq,omitempty"`
	TS      int64           `json:"ts"`
}

// Gateway 网关：单实例 map 管连接。
type Gateway struct {
	db    *sql.DB
	mu    sync.Mutex
	conns map[string]*websocket.Conn // userID → conn
	seqMu sync.Mutex
}

// New 构造。
func New(db *sql.DB) *Gateway {
	return &Gateway{db: db, conns: map[string]*websocket.Conn{}}
}

// NextSeq 分配用户递增 seq（ws_outbox 持久化保证断线补拉）。
func (g *Gateway) NextSeq(userID string) (int64, error) {
	g.seqMu.Lock()
	defer g.seqMu.Unlock()
	var seq int64
	if err := g.db.QueryRow(
		`SELECT COALESCE(MAX(seq), 0) FROM ws_outbox WHERE user_id = ?`, userID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq + 1, nil
}

// Push 持久化并即时推送（若在线）。msgType 即客户端契约 5 类之一。
func (g *Gateway) Push(userID, msgType string, payload any) error {
	seq, err := g.NextSeq(userID)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := g.db.Exec(
		`INSERT INTO ws_outbox (id, user_id, seq, type, payload) VALUES (?,?,?,?,?)`,
		"wo_"+userID[2:]+"_"+time.Now().Format("150405.000000000"), userID, seq, msgType, string(buf)); err != nil {
		return err
	}
	g.mu.Lock()
	c := g.conns[userID]
	g.mu.Unlock()
	if c != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = wsjson.Write(ctx, c, Envelope{Type: msgType, Seq: seq, Payload: buf, TS: time.Now().UnixMilli()})
	}
	return nil
}

// Handler 升级入口：/ws?token=...
func (g *Gateway) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := r.URL.Query().Get("uid") // 演示鉴权（生产替换 JWT 校验）
		if uid == "" {
			http.Error(w, "uid required", 401)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")

		g.mu.Lock()
		g.conns[uid] = c
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			delete(g.conns, uid)
			g.mu.Unlock()
		}()

		ctx := r.Context()
		for {
			var e Envelope
			if err := wsjson.Read(ctx, c, &e); err != nil {
				return
			}
			switch e.Type {
			case "resume":
				// 断线补拉：按 last_seq 从 ws_outbox 补发
				g.resume(ctx, c, uid, e.LastSeq)
			case "ack":
				// 客户端 ACK：seq 已落盘，无需处理（保留审计语义）
			}
		}
	}
}

func (g *Gateway) resume(ctx context.Context, c *websocket.Conn, uid string, lastSeq int64) {
	rows, err := g.db.QueryContext(ctx, `
		SELECT seq, type, payload FROM ws_outbox
		WHERE user_id = ? AND seq > ? ORDER BY seq LIMIT 500`, uid, lastSeq)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var t, p string
		if rows.Scan(&seq, &t, &p) == nil {
			_ = wsjson.Write(ctx, c, Envelope{Type: t, Seq: seq, Payload: json.RawMessage(p), TS: time.Now().UnixMilli()})
		}
	}
	// 缺口过大由客户端降级 REST /v1/ws/messages（详细设计客户端 §2.2）
}

// PullAfter REST 断线补拉端点数据源。
func (g *Gateway) PullAfter(ctx context.Context, uid string, afterSeq int64, limit int) ([]map[string]any, error) {
	rows, err := g.db.QueryContext(ctx, `
		SELECT seq, type, payload, created_at FROM ws_outbox
		WHERE user_id = ? AND seq > ? ORDER BY seq LIMIT ?`, uid, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var seq int64
		var t, p, ts string
		if rows.Scan(&seq, &t, &p, &ts) == nil {
			out = append(out, map[string]any{
				"seq": seq, "type": t, "payload": json.RawMessage(p), "ts": ts,
			})
		}
	}
	return out, nil
}
