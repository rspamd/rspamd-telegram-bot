package moderator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
)

// Reporter sends spam reports to the moderator channel.
type Reporter struct {
	bot       *bot.Bot
	channelID int64
	logger    *slog.Logger
}

// NewReporter creates a new moderator Reporter.
func NewReporter(b *bot.Bot, channelID int64, logger *slog.Logger) *Reporter {
	return &Reporter{
		bot:       b,
		channelID: channelID,
		logger:    logger,
	}
}

// Report sends a spam report to the moderator channel.
func (r *Reporter) Report(ctx context.Context, msg *rspamd.TelegramMessage, result *rspamd.CheckResult) error {
	text := formatReport(msg, result)

	_, err := r.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    r.channelID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		return fmt.Errorf("send moderator report: %w", err)
	}

	r.logger.Info("sent moderator report",
		"message_id", msg.MessageID,
		"chat_id", msg.ChatID,
		"score", result.Score,
	)

	return nil
}

func formatReport(msg *rspamd.TelegramMessage, result *rspamd.CheckResult) string {
	var sb strings.Builder

	sb.WriteString("\xf0\x9f\x9a\xa8 <b>Spam Detected</b>\n\n")

	// User info
	sb.WriteString(fmt.Sprintf("<b>User:</b> %s", escapeHTML(msg.FirstName)))
	if msg.LastName != "" {
		sb.WriteString(" " + escapeHTML(msg.LastName))
	}
	if msg.Username != "" {
		sb.WriteString(fmt.Sprintf(" (@%s)", escapeHTML(msg.Username)))
	}
	sb.WriteString(fmt.Sprintf("\n<b>User ID:</b> <code>%d</code>\n", msg.UserID))
	sb.WriteString(fmt.Sprintf("<b>Chat:</b> %s\n", escapeHTML(msg.ChatTitle)))

	// Message text (truncated)
	if msg.Text != "" {
		truncated := msg.Text
		if len(truncated) > 300 {
			truncated = truncated[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("\n<b>Message:</b>\n<blockquote>%s</blockquote>\n", escapeHTML(truncated)))
	}

	// Rspamd results
	sb.WriteString(fmt.Sprintf("\n<b>Score:</b> %.2f / %.2f\n", result.Score, result.RequiredScore))
	sb.WriteString(fmt.Sprintf("<b>Action:</b> %s\n", escapeHTML(result.Action)))

	// Top symbols by score
	sb.WriteString("\n<b>Symbols:</b>\n")
	symbols := topSymbols(result.Symbols, 10)
	for _, sym := range symbols {
		sb.WriteString(fmt.Sprintf("  \xe2\x80\xa2 <code>%s</code> (%.2f)", escapeHTML(sym.Name), sym.Score))
		if sym.Description != "" {
			sb.WriteString(fmt.Sprintf(" \xe2\x80\x94 %s", escapeHTML(sym.Description)))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func topSymbols(symbols map[string]rspamd.SymbolResult, limit int) []rspamd.SymbolResult {
	syms := make([]rspamd.SymbolResult, 0, len(symbols))
	for _, s := range symbols {
		if s.Score != 0 {
			syms = append(syms, s)
		}
	}

	sort.Slice(syms, func(i, j int) bool {
		// Sort by absolute score descending
		ai, aj := syms[i].Score, syms[j].Score
		if ai < 0 {
			ai = -ai
		}
		if aj < 0 {
			aj = -aj
		}
		return ai > aj
	})

	if len(syms) > limit {
		syms = syms[:limit]
	}

	return syms
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
