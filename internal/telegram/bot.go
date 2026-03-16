package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/redis/go-redis/v9"

	"github.com/vstakhov/rspamd-telegram-bot/internal/config"
	"github.com/vstakhov/rspamd-telegram-bot/internal/maps"
	"github.com/vstakhov/rspamd-telegram-bot/internal/moderator"
	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
	"github.com/vstakhov/rspamd-telegram-bot/internal/storage"
)

// Bot wraps the Telegram bot with spam-checking capabilities.
type Bot struct {
	bot      *bot.Bot
	cfg      *config.Config
	rspamd   *rspamd.Client
	storage  *storage.Client
	reporter *moderator.Reporter
	maps     *maps.Manager
	redis    *redis.Client
	logger   *slog.Logger
	chatSet  map[int64]bool

	// Admin status cache
	adminMu    sync.RWMutex
	adminCache map[adminCacheKey]adminCacheEntry
}

type adminCacheKey struct {
	ChatID int64
	UserID int64
}

type adminCacheEntry struct {
	IsAdmin   bool
	ExpiresAt time.Time
}

const adminCacheTTL = 5 * time.Minute

// New creates and configures a new Telegram bot.
func New(ctx context.Context, cfg *config.Config, rspamdClient *rspamd.Client, storageClient *storage.Client, mapsManager *maps.Manager, redisClient *redis.Client, logger *slog.Logger) (*Bot, error) {
	tb := &Bot{
		cfg:        cfg,
		rspamd:     rspamdClient,
		storage:    storageClient,
		maps:       mapsManager,
		redis:      redisClient,
		logger:     logger,
		chatSet:    make(map[int64]bool),
		adminCache: make(map[adminCacheKey]adminCacheEntry),
	}

	for _, chatID := range cfg.Telegram.MonitoredChats {
		tb.chatSet[chatID] = true
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(tb.handleUpdate),
	}

	b, err := bot.New(os.Getenv("BOT_TOKEN"), opts...)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	tb.bot = b
	tb.reporter = moderator.NewReporter(b, cfg.Telegram.ModeratorChannel, logger)

	// Utility commands (work in any chat)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/chatid", bot.MatchTypePrefix, tb.handleChatIDCommand)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/help", bot.MatchTypePrefix, tb.handleHelpCommand)

	// Register command handlers for monitored chats
	b.RegisterHandler(bot.HandlerTypeMessageText, "/spam", bot.MatchTypePrefix, tb.handleSpamCommand)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/ham", bot.MatchTypePrefix, tb.handleHamCommand)

	// Register admin commands (only work in moderator channel)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/addregexp", bot.MatchTypePrefix, tb.handleAddRegexp)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/delregexp", bot.MatchTypePrefix, tb.handleDelRegexp)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/listregexp", bot.MatchTypePrefix, tb.handleListRegexp)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/addurl", bot.MatchTypePrefix, tb.handleAddURL)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/delurl", bot.MatchTypePrefix, tb.handleDelURL)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/listurls", bot.MatchTypePrefix, tb.handleListURLs)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/userinfo", bot.MatchTypePrefix, tb.handleUserCommand)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/channels", bot.MatchTypePrefix, tb.handleChannelsCommand)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/context", bot.MatchTypePrefix, tb.handleContextCommand)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/users", bot.MatchTypePrefix, tb.handleUsersCommand)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/trainspam", bot.MatchTypePrefix, tb.handleTrainSpam)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/trainham", bot.MatchTypePrefix, tb.handleTrainHam)

	return tb, nil
}

// Start begins long polling. Blocks until context is cancelled.
func (tb *Bot) Start(ctx context.Context) {
	tb.logger.Info("starting telegram bot")
	tb.bot.Start(ctx)
}

// isMonitored returns true if the chat is in the monitored list.
func (tb *Bot) isMonitored(chatID int64) bool {
	return tb.chatSet[chatID]
}

// isAdminCached checks admin status with in-memory cache.
func (tb *Bot) isAdminCached(ctx context.Context, b *bot.Bot, chatID int64, userID int64) bool {
	key := adminCacheKey{ChatID: chatID, UserID: userID}

	tb.adminMu.RLock()
	entry, ok := tb.adminCache[key]
	tb.adminMu.RUnlock()

	if ok && time.Now().Before(entry.ExpiresAt) {
		return entry.IsAdmin
	}

	// Cache miss or expired — query Telegram API
	result, err := tb.isAdmin(ctx, b, chatID, userID)
	if err != nil {
		tb.logger.Debug("admin check failed, assuming not admin", "error", err)
		result = false
	}

	tb.adminMu.Lock()
	tb.adminCache[key] = adminCacheEntry{
		IsAdmin:   result,
		ExpiresAt: time.Now().Add(adminCacheTTL),
	}
	tb.adminMu.Unlock()

	return result
}
