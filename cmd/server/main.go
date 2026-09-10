// 搭趣 SpotPal 服务端 · 唯一入口。
//
// 启动：make dev / ./spotpal-server / go run ./cmd/server
// 零基础设施：单二进制 + 单数据文件（SQLite WAL）。
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spotpal/spotpal-server/internal/api"
	"github.com/spotpal/spotpal-server/internal/bus"
	"github.com/spotpal/spotpal-server/internal/db"
	"github.com/spotpal/spotpal-server/internal/module/credit"
	"github.com/spotpal/spotpal-server/internal/module/match"
	"github.com/spotpal/spotpal-server/internal/module/negotiate"
	"github.com/spotpal/spotpal-server/internal/module/pay"
	"github.com/spotpal/spotpal-server/internal/module/squad"
	"github.com/spotpal/spotpal-server/internal/module/user"
	"github.com/spotpal/spotpal-server/internal/pkg/config"
	"github.com/spotpal/spotpal-server/internal/pkg/log"
	"github.com/spotpal/spotpal-server/internal/platform/llm"
	"github.com/spotpal/spotpal-server/internal/timeout"
	"github.com/spotpal/spotpal-server/internal/ws"
)

func main() {
	cfg := config.Load()
	log.Init(os.Getenv("SPOTPAL_DEBUG") != "")

	// 数据层
	d, err := db.Open(cfg.DataDir)
	if err != nil {
		log.L.Error("open db", "err", err)
		os.Exit(1)
	}
	defer d.Close()
	if err := db.Migrate(d, "migrations"); err != nil {
		log.L.Error("migrate", "err", err)
		os.Exit(1)
	}
	if cfg.Seed {
		if err := db.Seed(d, "migrations"); err != nil {
			log.L.Error("seed", "err", err)
			os.Exit(1)
		}
		log.L.Info("demo data seeded")
	}

	// 平台依赖（LLM mock / SMS 日志）
	var resolver llm.TimeWindowResolver = llm.NewMock()
	switch cfg.LLM {
	case "mock":
		resolver = llm.NewMock()
	default:
		log.L.Warn("unknown llm impl, fallback to mock", "want", cfg.LLM)
	}

	// 进程内设施
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b := bus.New(d)
	tw := timeout.New(d, func(ctx context.Context, kind, payload string) error {
		// 超时统一回调：当前仅协商过期一种
		if kind == timeout.KindNegotiationExpire {
			return negotiate.OnTimeout(ctx, d, b, payload)
		}
		return nil
	})
	gw := ws.New(d)

	// 模块装配（接口注入，禁止模块互 import）
	userSvc := user.New(d)
	matchSvc := match.New(d)
	squadSvc := squad.New(d)
	negSvc := negotiate.New(d, resolver, b, tw)
	_ = credit.New(d)
	_ = pay.NewPayLater(d)

	// 事件订阅（8 主题契约）
	b.Subscribe(bus.TopicSquadConfirmed, func(ctx context.Context, e bus.Event) error {
		return gw.Push("u_demo_ye", "negotiation.confirmed", map[string]any{"raw": string(e.Payload)})
	})
	b.Subscribe(bus.TopicNegotiationProposal, func(ctx context.Context, e bus.Event) error {
		return gw.Push("u_demo_zhe", "negotiation.proposal", map[string]any{"raw": string(e.Payload)})
	})

	// HTTP
	a := api.New(userSvc, matchSvc, squadSvc, negSvc, pay.NewPayLater(d), pay.NewPayLater(d), gw, cfg.TokenKey, cfg.RateLimit)
	mux := a.Router()
	mux.Handle("/ws", gw.Handler())

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}
	go func() {
		log.L.Info("spotpal-server listening", "addr", cfg.HTTPAddr, "data", cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.L.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	// 后台循环：outbox 投递器 + 超时轮询
	go b.Run(ctx)
	go tw.Run(ctx)

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	log.L.Info("bye")
}
