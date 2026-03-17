package telegram

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// sendHTML sends an HTML message, falling back to plain text on error.
// Logs errors instead of silently failing.
func (tb *Bot) sendHTML(ctx context.Context, b *bot.Bot, chatID int64, text string, replyTo int) {
	// Sanitize: ensure valid UTF-8
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "?")
	}

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          chatID,
		Text:            text,
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: replyTo},
	})
	if err != nil {
		tb.logger.Error("sendHTML failed, retrying without HTML",
			"chat_id", chatID,
			"error", err,
			"text_len", len(text),
		)
		// Retry without HTML parsing
		_, err2 := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          chatID,
			Text:            text,
			ReplyParameters: &models.ReplyParameters{MessageID: replyTo},
		})
		if err2 != nil {
			tb.logger.Error("send plain text also failed",
				"chat_id", chatID,
				"error", err2,
			)
		}
	}
}
