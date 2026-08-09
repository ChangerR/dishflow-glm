// Package main 是 `serve` 子命令：启动 API 进程。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dishflow/zshop/internal/config"
	"github.com/dishflow/zshop/internal/reliability"
	"github.com/dishflow/zshop/internal/server"
	"github.com/dishflow/zshop/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error("serve failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := server.NewLogger(cfg)

	// 连接 MySQL + Redis（ready 检查依赖；启动时允许 Redis 暂时不可达是后续考虑）。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := storage.OpenMySQL(ctx, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	rdb, err := storage.OpenRedis(ctx, cfg.RedisAddr)
	if err != nil {
		log.Warn("redis unavailable at startup; serve will start but /health/ready will fail", "error", err)
	} else {
		defer rdb.Close()
	}

	srv := server.New(cfg, log, storage.SQLDB(db), rdb)
	handler := srv.Router()

	// 在 /api/v1 挂载幂等中间件示例（后续阶段细化）。
	_ = reliability.Middleware

	addr := cfg.ServeAddr
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// 优雅关停。
	go func() {
		log.Info("http server listening", "addr", addr, "dev_mode", cfg.DevMode)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen error", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	return httpSrv.Shutdown(shutdownCtx)
}
