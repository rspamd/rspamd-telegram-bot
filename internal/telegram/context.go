package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	maxRecentMessages = 20
	contextTTL        = 24 * time.Hour
)

type channelContext struct {
	ChannelTitle       string           `json:"channel_title"`
	ChannelDescription string           `json:"channel_description,omitempty"`
	RecentMessages     []contextMessage `json:"recent_messages"`
	KnownSpamPatterns  []string         `json:"known_spam_patterns,omitempty"`
}

type contextMessage struct {
	From string `json:"from"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

// updateChannelContext updates the rolling channel context in Redis
// after a legitimate (non-spam) message.
func (tb *Bot) updateChannelContext(ctx context.Context, msg *models.Message) {
	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	key := fmt.Sprintf("tg_channel_ctx:%s", chatID)

	// Get sender name
	fromName := msg.From.FirstName
	if msg.From.Username != "" {
		fromName = "@" + msg.From.Username
	}

	// Get message text
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		return
	}
	runes := []rune(text)
	if len(runes) > 200 {
		text = string(runes[:200])
	}

	newMsg := contextMessage{
		From: fromName,
		Text: text,
		TS:   time.Now().Unix(),
	}

	// Read existing context
	var chanCtx channelContext
	data, err := tb.redis.Get(ctx, key).Bytes()
	if err == nil {
		_ = json.Unmarshal(data, &chanCtx)
	}

	// Update title
	chanCtx.ChannelTitle = msg.Chat.Title

	// Append message, keep last N
	chanCtx.RecentMessages = append(chanCtx.RecentMessages, newMsg)
	if len(chanCtx.RecentMessages) > maxRecentMessages {
		chanCtx.RecentMessages = chanCtx.RecentMessages[len(chanCtx.RecentMessages)-maxRecentMessages:]
	}

	// Write back
	encoded, err := json.Marshal(chanCtx)
	if err != nil {
		tb.logger.Error("failed to marshal channel context", "error", err)
		return
	}

	tb.redis.Set(ctx, key, encoded, contextTTL)
}

// handleContextCommand dumps channel context from Redis for debugging.
func (tb *Bot) handleContextCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	if msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	chatID := extractCommandArg(msg.Text, "/context")

	// No argument — list all contexts
	if chatID == "" {
		keys, _ := tb.redis.Keys(ctx, "tg_channel_ctx:*").Result()
		if len(keys) == 0 {
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:          msg.Chat.ID,
				Text:            "No channel contexts found.",
				ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
			})
			return
		}
		var sb strings.Builder
		sb.WriteString("<b>Channel contexts:</b>\n\n")
		for _, k := range keys {
			id := strings.TrimPrefix(k, "tg_channel_ctx:")
			data, _ := tb.redis.Get(ctx, k).Bytes()
			var c channelContext
			_ = json.Unmarshal(data, &c)
			title := c.ChannelTitle
			if title == "" {
				title = id
			}
			sb.WriteString(fmt.Sprintf("%s — %d msgs, %d bytes\n  /context %s\n\n",
				adminEscapeHTML(title), len(c.RecentMessages), len(data), id))
		}
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            sb.String(),
			ParseMode:       models.ParseModeHTML,
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	key := fmt.Sprintf("tg_channel_ctx:%s", chatID)
	data, err := tb.redis.Get(ctx, key).Bytes()
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("No context for channel %s", chatID),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	var chanCtx channelContext
	if err := json.Unmarshal(data, &chanCtx); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed to parse context: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>Channel context for %s</b>\n", adminEscapeHTML(chatID)))
	sb.WriteString(fmt.Sprintf("<b>Title:</b> %s\n", adminEscapeHTML(chanCtx.ChannelTitle)))
	if chanCtx.ChannelDescription != "" {
		sb.WriteString(fmt.Sprintf("<b>Description:</b> %s\n", adminEscapeHTML(chanCtx.ChannelDescription)))
	}
	sb.WriteString(fmt.Sprintf("<b>Messages:</b> %d\n", len(chanCtx.RecentMessages)))
	sb.WriteString(fmt.Sprintf("<b>Redis key:</b> <code>%s</code>\n", key))
	sb.WriteString(fmt.Sprintf("<b>Raw size:</b> %d bytes\n", len(data)))

	if len(chanCtx.RecentMessages) > 0 {
		sb.WriteString("\n<b>Recent messages:</b>\n")
		for i, m := range chanCtx.RecentMessages {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("... and %d more\n", len(chanCtx.RecentMessages)-10))
				break
			}
			text := m.Text
			runes := []rune(text)
			if len(runes) > 80 {
				text = string(runes[:80]) + "..."
			}
			sb.WriteString(fmt.Sprintf("%d. <b>%s</b>: %s\n", i+1,
				adminEscapeHTML(m.From), adminEscapeHTML(text)))
		}
	}

	if len(chanCtx.KnownSpamPatterns) > 0 {
		sb.WriteString(fmt.Sprintf("\n<b>Known spam patterns:</b> %s\n",
			adminEscapeHTML(strings.Join(chanCtx.KnownSpamPatterns, ", "))))
	}

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            sb.String(),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
	if err != nil {
		tb.logger.Error("context send failed", "error", err)
		// Retry without HTML parsing
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            sb.String(),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
	}
}
