package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"

	"github.com/vstakhov/rspamd-telegram-bot/internal/config"
	"github.com/vstakhov/rspamd-telegram-bot/internal/maps"
	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
	"github.com/vstakhov/rspamd-telegram-bot/internal/storage"
	"github.com/vstakhov/rspamd-telegram-bot/internal/telegram"
	"github.com/vstakhov/rspamd-telegram-bot/internal/userpic"
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

	// Initialize ClickHouse
	store, err := storage.NewClient(ctx, cfg.ClickHouse.DSN, logger)
	if err != nil {
		logger.Error("failed to connect to ClickHouse", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Initialize Rspamd client
	rspamdClient := rspamd.NewClient(cfg.Rspamd.URL, cfg.Rspamd.Password, cfg.Rspamd.Timeout, logger)

	// Initialize maps manager for admin-managed Rspamd rules
	mapsDir := cfg.Maps.Dir
	if mapsDir == "" {
		mapsDir = "/maps"
	}
	mapsManager, err := maps.NewManager(mapsDir, logger)
	if err != nil {
		logger.Error("failed to initialize maps manager", "error", err)
		os.Exit(1)
	}

	// Initialize Redis client (shared with Rspamd for profile data)
	redisClient := redis.NewClient(&redis.Options{
		Addr: cfg.Redis.Addr,
		DB:   cfg.Redis.DB,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Error("failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()
	logger.Info("connected to Redis", "addr", cfg.Redis.Addr)

	// Initialize userpic analyzer (uses OpenRouter vision API)
	var userpicAnalyzer *userpic.Analyzer
	if apiKey := os.Getenv("OPENROUTER_API_KEY"); apiKey != "" {
		userpicAnalyzer = userpic.NewAnalyzer(
			apiKey,
			os.Getenv("OPENROUTER_VISION_MODEL"),
			"",
			logger,
		)
		logger.Info("userpic analyzer enabled")
	}

	// Initialize Telegram bot
	bot, err := telegram.New(ctx, cfg, rspamdClient, store, mapsManager, userpicAnalyzer, redisClient, logger)
	if err != nil {
		logger.Error("failed to create telegram bot", "error", err)
		os.Exit(1)
	}

	logger.Info("starting rspamd-telegram-bot")
	bot.Start(ctx)
}
