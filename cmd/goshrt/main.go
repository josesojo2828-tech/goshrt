package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/jsojo/goshrt/internal/config"
	"github.com/jsojo/goshrt/internal/handler"
	"github.com/jsojo/goshrt/internal/metrics"
	"github.com/jsojo/goshrt/internal/service"
	"github.com/jsojo/goshrt/internal/store/postgres"
	"github.com/jsojo/goshrt/internal/store/redis"
	"github.com/jsojo/goshrt/internal/worker"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	metrics.BuildInfo.WithLabelValues(version, commit, runtime.Version()).Set(1)

	cfg := config.Load()

	pgStore, err := postgres.NewStore(cfg.PostgresDSN)
	if err != nil {
		slog.Error("connecting to postgres", "error", err)
		os.Exit(1)
	}
	defer pgStore.Close()

	redisCache, err := redis.NewCache(cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		slog.Error("connecting to redis", "error", err)
		os.Exit(1)
	}
	defer redisCache.Close()

	svc := service.New(pgStore, redisCache)

	ctx := context.Background()
	count, err := svc.SeedCache(ctx)
	if err != nil {
		slog.Warn("cache seed failed", "error", err)
	} else {
		slog.Info("cache seeded", "urls", count)
	}

	clickWorker := worker.New(svc, cfg.ClickSyncInterval)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go clickWorker.Start(ctx)

	h := handler.New(svc)
	addr := cfg.Port
	if addr[0] != ':' {
		addr = ":" + addr
	}
	srv := &http.Server{
		Addr:    addr,
		Handler: h.Routes(),
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down")
		clickWorker.Stop()
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("goshrt listening", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
