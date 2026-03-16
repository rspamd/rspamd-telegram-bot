package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func adminEscapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// extractCommandArg extracts the argument from a command message,
// handling the optional @botname suffix.
func extractCommandArg(text, command string) string {
	rest := strings.TrimPrefix(text, command)
	// Strip optional @botname
	if strings.HasPrefix(rest, "@") {
		if idx := strings.IndexByte(rest, ' '); idx >= 0 {
			rest = rest[idx:]
		} else {
			rest = ""
		}
	}
	return strings.TrimSpace(rest)
}

func (tb *Bot) handleAddRegexp(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	pattern := extractCommandArg(msg.Text, "/addregexp")
	if pattern == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /addregexp <pattern>\nExample: /addregexp /crypto.*invest.*profit/i",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	if err := tb.maps.AddPattern(pattern); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("Regexp pattern added: <code>%s</code>\nRspamd will reload the map automatically.", adminEscapeHTML(pattern)),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleDelRegexp(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	pattern := extractCommandArg(msg.Text, "/delregexp")
	if pattern == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /delregexp <pattern>",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	if err := tb.maps.RemovePattern(pattern); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("Regexp pattern removed: <code>%s</code>", adminEscapeHTML(pattern)),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleListRegexp(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	patterns, err := tb.maps.ListPatterns()
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	if len(patterns) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "No regexp patterns configured.",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	var sb strings.Builder
	sb.WriteString("<b>Regexp patterns:</b>\n")
	for i, p := range patterns {
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", i+1, adminEscapeHTML(p)))
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            sb.String(),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleAddURL(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	url := extractCommandArg(msg.Text, "/addurl")
	if url == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /addurl <domain_or_url>\nExample: /addurl spamsite.com",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	if err := tb.maps.AddURL(url); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("URL added: <code>%s</code>\nRspamd will reload the map automatically.", adminEscapeHTML(url)),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleDelURL(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	url := extractCommandArg(msg.Text, "/delurl")
	if url == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /delurl <domain_or_url>",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	if err := tb.maps.RemoveURL(url); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("URL removed: <code>%s</code>", adminEscapeHTML(url)),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleListURLs(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	urls, err := tb.maps.ListURLs()
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	if len(urls) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "No URL rules configured.",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	var sb strings.Builder
	sb.WriteString("<b>URL rules:</b>\n")
	for i, u := range urls {
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", i+1, adminEscapeHTML(u)))
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            sb.String(),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}
