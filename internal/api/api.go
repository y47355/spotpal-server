// Package api HTTP 路由 + 中间件（鉴权/限流/trace）+ 11 端点（详细设计 §6）。
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"

	"github.com/spotpal/spotpal-server/internal/module/match"
	"github.com/spotpal/spotpal-server/internal/module/negotiate"
	"github.com/spotpal/spotpal-server/internal/module/pay"
	"github.com/spotpal/spotpal-server/internal/module/squad"
	"github.com/spotpal/spotpal-server/internal/module/user"
	"github.com/spotpal/spotpal-server/internal/pkg/log"
	"github.com/spotpal/spotpal-server/internal/platform/llm"
	"github.com/spotpal/spotpal-server/internal/ws"
)

// Envelope 响应信封 {code, msg, data, req_id}。
type Envelope struct {
	Code  int         `json:"code"`
	Msg   string      `json:"msg"`
	Data  interface{} `json:"data,omitempty"`
	ReqID string      `json:"req_id"`
}

// API 服务聚合。
type API struct {
	User *user.Service
	Match *match.Service
	Squad *squad.Service
	Negotiate *negotiate.Service
	Pay  pay.Ledger
	OrderBook *pay.PayLaterLedger // 结算单查询（接口外能力）
	WS   *ws.Gateway
	TokenKey string
	lim  *limiter
}

// New 构造。
func New(u *user.Service, m *match.Service, sq *squad.Service, ng *negotiate.Service,
	p pay.Ledger, ob *pay.PayLaterLedger, w *ws.Gateway, tokenKey string, rps int) *API {
	return &API{
		User: u, Match: m, Squad: sq, Negotiate: ng, Pay: p, OrderBook: ob, WS: w,
		TokenKey: tokenKey, lim: newLimiter(rps),
	}
}

// ---- 限流（进程内令牌桶，按 IP+UID）----

type limiter struct {
	mu sync.Mutex
	m  map[string]*rate.Limiter
	r  rate.Limit
	b  int
}

func newLimiter(rps int) *limiter {
	return &limiter{m: map[string]*rate.Limiter{}, r: rate.Limit(rps), b: rps}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.m[key]
	if !ok {
		lim = rate.NewLimiter(l.r, l.b)
		l.m[key] = lim
	}
	return lim.Allow()
}

// ---- 中间件 ----

func (a *API) wrap(h func(w http.ResponseWriter, r *http.Request, uid string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
		w.Header().Set("X-Req-Id", reqID)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		// 鉴权（/login 豁免在外层路由）
		uid := a.uid(r)
		if uid == "" {
			write(w, 1001, "token invalid", nil, reqID)
			return
		}
		// 限流
		if !a.lim.allow(r.RemoteAddr + "|" + uid) {
			write(w, 1002, "rate limited", nil, reqID)
			return
		}
		h(w, r, uid)
	}
}

func (a *API) uid(r *http.Request) string {
	tok := r.Header.Get("Authorization")
	if len(tok) > 7 && tok[:7] == "Bearer " {
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(tok[7:], claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(a.TokenKey), nil
		})
		if err == nil {
			if s, ok := claims["uid"].(string); ok {
				return s
			}
		}
	}
	// 演示通道：?uid=（生产环境删除）
	if v := r.URL.Query().Get("uid"); v != "" {
		return v
	}
	return ""
}

func write(w http.ResponseWriter, code int, msg string, data interface{}, reqID string) {
	_ = json.NewEncoder(w).Encode(Envelope{Code: code, Msg: msg, Data: data, ReqID: reqID})
}

// ---- 路由装配（11 端点）----

// Router 装配全部路由。
func (a *API) Router() *http.ServeMux {
	mux := http.NewServeMux()

	// 1. 登录（验证码演示通道）
	mux.HandleFunc("POST /v1/auth/login", a.login)

	// 2~11：鉴权端点
	mux.HandleFunc("POST /v1/pool/push", a.wrap(a.pushPool))
	mux.HandleFunc("GET /v1/pool", a.wrap(a.getPool))
	mux.HandleFunc("DELETE /v1/pool/{candidateId}", a.wrap(a.delPool))
	mux.HandleFunc("POST /v1/negotiation/release", a.wrap(a.release))
	mux.HandleFunc("GET /v1/negotiation/{id}", a.wrap(a.getNegotiation))
	mux.HandleFunc("POST /v1/negotiation/{id}/feedback", a.wrap(a.feedback))
	mux.HandleFunc("POST /v1/squad/{id}/confirm", a.wrap(a.confirmSquad))
	mux.HandleFunc("POST /v1/squad/{id}/checkin", a.wrap(a.checkin))
	mux.HandleFunc("GET /v1/feed/recommend", a.wrap(a.recommend))
	mux.HandleFunc("GET /v1/ws/messages", a.wrap(a.pullMessages))
	mux.HandleFunc("GET /v1/me/squads", a.wrap(a.mySquads))

	// 健康检查
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// login POST /v1/auth/login {phone, code, nickname?}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	var body struct {
		Phone    string `json:"phone"`
		Code     string `json:"code"`
		Nickname string `json:"nickname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Phone == "" {
		write(w, 2001, "bad request", nil, reqID)
		return
	}
	// 演示模式：任意 6 位验证码通过（生产：sms.Sender 下发 + 校验）
	u, err := a.User.LoginOrRegister(r.Context(), body.Phone, body.Nickname)
	if err != nil {
		write(w, 2002, "login failed", nil, reqID)
		return
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"uid": u.ID, "exp": time.Now().Add(7 * 24 * time.Hour).Unix(),
	}).SignedString([]byte(a.TokenKey))
	write(w, 0, "ok", map[string]any{
		"token": tok, "user_id": u.ID, "nickname": u.Nickname, "credit_score": u.CreditScore,
	}, reqID)
}

// pushPool POST /v1/pool/push（幂等：Idempotency-Key + UNIQUE 约束兜底）
func (a *API) pushPool(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	var body struct {
		CandidateID string `json:"candidate_id"`
		Intent      string `json:"intent"` // JSON 快照
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CandidateID == "" {
		write(w, 3001, "bad request", nil, reqID)
		return
	}
	if err := a.Squad.Push(r.Context(), uid, body.CandidateID, body.Intent); err != nil {
		write(w, 3002, "pool push failed", nil, reqID)
		return
	}
	write(w, 0, "ok", map[string]any{"candidate_id": body.CandidateID, "status": "WAITING"}, reqID)
}

// getPool GET /v1/pool
func (a *API) getPool(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	items, err := a.Squad.Pool(r.Context(), uid)
	if err != nil {
		write(w, 3003, "pool query failed", nil, reqID)
		return
	}
	write(w, 0, "ok", map[string]any{"items": items}, reqID)
}

// delPool DELETE /v1/pool/{candidateId}
func (a *API) delPool(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	id := r.PathValue("candidateId")
	if err := a.Squad.Remove(r.Context(), uid, id); err != nil {
		write(w, 3004, "remove failed", nil, reqID)
		return
	}
	write(w, 0, "ok", nil, reqID)
}

// release POST /v1/negotiation/release → 事件 pool.joined
func (a *API) release(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	var body struct {
		Activity string   `json:"activity"`
		Hours    []int    `json:"hours"`
		Weekdays []string `json:"weekdays"`
		CostMode string   `json:"cost_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Activity == "" {
		write(w, 4001, "bad request", nil, reqID)
		return
	}
	id, err := a.Negotiate.Release(r.Context(), uid, llm.Intent{
		UserID: uid, Activity: body.Activity, Hours: body.Hours,
		Weekdays: body.Weekdays, CostMode: body.CostMode,
	})
	if err != nil {
		write(w, 4002, "release failed: "+err.Error(), nil, reqID)
		return
	}
	write(w, 0, "ok", map[string]any{"negotiation_id": id}, reqID)
}

// getNegotiation GET /v1/negotiation/{id}
func (a *API) getNegotiation(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	n, err := a.Negotiate.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		write(w, 4003, "not found", nil, reqID)
		return
	}
	write(w, 0, "ok", n, reqID)
}

// feedback POST /v1/negotiation/{id}/feedback
func (a *API) feedback(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	var body struct {
		OK   bool   `json:"ok"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		write(w, 4004, "bad request", nil, reqID)
		return
	}
	if err := a.Negotiate.Feedback(r.Context(), r.PathValue("id"), uid, body.OK, body.Note); err != nil {
		write(w, 4005, "feedback failed: "+err.Error(), nil, reqID)
		return
	}
	write(w, 0, "ok", nil, reqID)
}

// confirmSquad POST /v1/squad/{id}/confirm（乐观锁兜底竞态）
func (a *API) confirmSquad(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	id := r.PathValue("id")
	sq, err := a.Squad.Get(r.Context(), id)
	if err != nil {
		write(w, 3101, "squad not found", nil, reqID)
		return
	}
	if err := a.Squad.Transition(r.Context(), id, sq.Status, squad.StCONFIRMED); err != nil {
		write(w, 3102, "transition failed: "+err.Error(), nil, reqID)
		return
	}
	// 结算单（纯记账）随成局生成
	if sq != nil {
		_ = a.Pay.CreateOrder(r.Context(), pay.SettleOrder{
			SquadID: id, Mode: orDefault(sq.CostMode, "AA"), Amount: 12900,
		})
	}
	_ = a.WS.Push(uid, "squad.status", map[string]any{"squad_id": id, "status": "CONFIRMED"})
	write(w, 0, "ok", map[string]any{"squad_id": id, "status": "CONFIRMED"}, reqID)
}

// checkin POST /v1/squad/{id}/checkin → 结算单 PENDING → SUCCESS（纯记账）
func (a *API) checkin(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	id := r.PathValue("id")
	o, err := a.OrderBook.BySquad(r.Context(), id)
	if err != nil {
		// 成局未生成过结算单（防御性补开）
		o = &pay.SettleOrder{ID: "so_" + id[3:], SquadID: id, Mode: "AA", Amount: 0}
		_ = a.Pay.CreateOrder(r.Context(), *o)
	}
	if err := a.Pay.MarkSettled(r.Context(), o.ID); err != nil {
		write(w, 5001, "settle failed", nil, reqID)
		return
	}
	if err := a.Squad.Transition(r.Context(), id, squad.StCONFIRMED, squad.StSETTLED); err != nil {
		// 容忍状态已流转（幂等）
		log.L.Warn("checkin transition", "err", err)
	}
	write(w, 0, "ok", map[string]any{
		"order_id": o.ID, "status": "SUCCESS",
		"note": "纯记账结算 · 费用线下自理",
	}, reqID)
}

// recommend GET /v1/feed/recommend?cursor&lat&lng
func (a *API) recommend(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	cursor := r.URL.Query().Get("cursor")
	var lat, lng *float64
	if s := r.URL.Query().Get("lat"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			lat = &v
		}
	}
	if s := r.URL.Query().Get("lng"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			lng = &v
		}
	}
	cards, next, err := a.Match.Recommend(r.Context(), uid, cursor, 20, lat, lng)
	if err != nil {
		write(w, 1003, "recommend failed", nil, reqID)
		return
	}
	write(w, 0, "ok", map[string]any{"cards": cards, "next_cursor": next}, reqID)
}

// pullMessages GET /v1/ws/messages?after_seq=
func (a *API) pullMessages(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	msgs, err := a.WS.PullAfter(r.Context(), uid, after, 500)
	if err != nil {
		write(w, 1004, "pull failed", nil, reqID)
		return
	}
	write(w, 0, "ok", map[string]any{"messages": msgs}, reqID)
}

// mySquads GET /v1/me/squads
func (a *API) mySquads(w http.ResponseWriter, r *http.Request, uid string) {
	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	items, err := a.Squad.MySquads(r.Context(), uid)
	if err != nil {
		write(w, 3103, "query failed", nil, reqID)
		return
	}
	write(w, 0, "ok", map[string]any{"squads": items}, reqID)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

var _ = context.Background // 保持 import（当前实现以 r.Context() 传递）
