// Package pay 结算模块（初版纯记账，详细设计 §3.7）。
//
// PayLaterLedger 不调用任何外部支付 API：结算单只是账面记录，
// 费用线下自理。wx_order_id 恒空。接入真实支付时换 Ledger 实现类。
package pay

import (
	"context"
	"database/sql"
	"time"
)

// SettleOrder 结算单。
type SettleOrder struct {
	ID      string
	SquadID string
	Mode    string // AA | ROTATE | TREAT
	Amount  int64  // 分
}

// Ledger 结算接口：初版唯一实现 PayLaterLedger；未来接微信分账换实现类。
type Ledger interface {
	CreateOrder(ctx context.Context, o SettleOrder) error
	MarkSettled(ctx context.Context, orderID string) error
}

// PayLaterLedger 纯记账实现。
type PayLaterLedger struct{ db *sql.DB }

// NewPayLater 构造。
func NewPayLater(db *sql.DB) *PayLaterLedger { return &PayLaterLedger{db: db} }

// CreateOrder 落 settle_orders 表（CREATED）。
func (l *PayLaterLedger) CreateOrder(ctx context.Context, o SettleOrder) error {
	if o.ID == "" {
		o.ID = "so_" + time.Now().Format("150405.000000000")
	}
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO settle_orders (id, squad_id, mode, amount, status)
		VALUES (?,?,?,?,'CREATED') ON CONFLICT(id) DO NOTHING`,
		o.ID, o.SquadID, o.Mode, o.Amount)
	return err
}

// MarkSettled 状态机照常流转：CREATED → PENDING → SUCCESS（无资金划转）。
func (l *PayLaterLedger) MarkSettled(ctx context.Context, orderID string) error {
	res, err := l.db.ExecContext(ctx, `
		UPDATE settle_orders SET status = 'SUCCESS'
		WHERE id = ? AND status IN ('CREATED','PENDING')`, orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MarkPending 打卡后进入待结算。
func (l *PayLaterLedger) MarkPending(ctx context.Context, orderID string) error {
	_, err := l.db.ExecContext(ctx,
		`UPDATE settle_orders SET status = 'PENDING' WHERE id = ? AND status = 'CREATED'`, orderID)
	return err
}

// BySquad 按搭局查结算单（客户端「费用明细」数据源）。
func (l *PayLaterLedger) BySquad(ctx context.Context, squadID string) (*SettleOrder, error) {
	var o SettleOrder
	err := l.db.QueryRowContext(ctx, `
		SELECT id, squad_id, mode, amount FROM settle_orders
		WHERE squad_id = ? ORDER BY created_at DESC LIMIT 1`, squadID).
		Scan(&o.ID, &o.SquadID, &o.Mode, &o.Amount)
	if err != nil {
		return nil, err
	}
	return &o, nil
}
