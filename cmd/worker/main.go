// Package main 是 `worker` 子命令：后台可靠性任务进程。
//
// P0 仅提供生命周期骨架与心跳探活；真实任务（释放过期预占、支付/退款对账、
// outbox 分发、云打印、回收站窗口）在 P5/P6 实现（PRD §15）。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dishflow/zshop/internal/config"
	"github.com/dishflow/zshop/internal/members"
	"github.com/dishflow/zshop/internal/orders"
	"github.com/dishflow/zshop/internal/security"
	"github.com/dishflow/zshop/internal/server"
	"github.com/dishflow/zshop/internal/storage"
	"github.com/dishflow/zshop/internal/worker"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := server.NewLogger(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := storage.OpenMySQL(ctx, cfg.DatabaseDSN)
	if err != nil {
		cancel()
		return err
	}
	defer db.Close()

	rdb, err := storage.OpenRedis(ctx, cfg.RedisAddr)
	cancel()
	if err != nil {
		log.Warn("redis unavailable at startup; worker heartbeat disabled", "error", err)
	} else {
		defer rdb.Close()
	}

	// 优雅关停。
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 心跳：每 15s 写 Redis worker:heartbeat，TTL 45s（PRD §15）。
	if rdb != nil {
		go heartbeatLoop(rootCtx, log, rdb)
	}

	// 启动 Worker 任务循环（outbox/释放/对账/回收站，PRD §15）。
	ordStore := orders.NewStore(db.DB)
	enc, _ := security.NewEncryptor(cfg.CredentialKey32())
	memStore := members.NewStore(db.DB, enc)
	w := worker.New(db.DB, log).WithStores(ordStore, memStore)
	go w.Run(rootCtx)

	log.Info("worker started", "dev_mode", cfg.DevMode)
	<-rootCtx.Done()
	log.Info("worker stopped")
	return nil
}

// heartbeatLoop 周期性写 Redis 心跳键。
func heartbeatLoop(ctx context.Context, log *slog.Logger, rdb *redis.Client) {
	set := func() {
		hctx, hcancel := context.WithTimeout(ctx, 3*time.Second)
		defer hcancel()
		if err := rdb.Set(hctx, "worker:heartbeat", time.Now().UTC().Unix(), 45*time.Second).Err(); err != nil {
			log.Warn("worker heartbeat failed", "error", err)
		}
	}
	set()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			set()
		}
	}
}
