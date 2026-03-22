package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/vstakhov/rspamd-telegram-bot/internal/config"
	"github.com/vstakhov/rspamd-telegram-bot/internal/maps"
	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
	"github.com/vstakhov/rspamd-telegram-bot/internal/storage"
	"github.com/vstakhov/rspamd-telegram-bot/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	configPath := "config.yml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		configPath = p
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ClickHouse
	store, err := storage.NewClient(ctx, cfg.ClickHouse.DSN, logger)
	if err != nil {
		logger.Error("failed to connect to ClickHouse", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr: cfg.Redis.Addr,
		DB:   cfg.Redis.DB,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Error("failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	// Rspamd client (for version/stats)
	rspamdClient := rspamd.NewClient(cfg.Rspamd.URL, cfg.Rspamd.Password, cfg.Rspamd.Timeout, logger)

	// Maps manager
	mapsDir := cfg.Maps.Dir
	if mapsDir == "" {
		mapsDir = "/maps"
	}
	mapsManager, err := maps.NewManager(mapsDir, logger)
	if err != nil {
		logger.Error("failed to initialize maps manager", "error", err)
		os.Exit(1)
	}

	// Web server
	listen := cfg.Web.Listen
	if listen == "" {
		listen = ":8080"
	}

	srv := web.New(cfg, store, redisClient, rspamdClient, mapsManager, logger)

	httpServer := &http.Server{
		Addr:    listen,
		Handler: srv.Handler(),
	}

	go func() {
		logger.Info("starting web server", "listen", listen)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("web server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down web server")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)
}
