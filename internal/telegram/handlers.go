package telegram

import (
	"context"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
	"github.com/vstakhov/rspamd-telegram-bot/internal/storage"
)

// handleUpdate is the default handler for all updates.
func (tb *Bot) handleUpdate(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	msg := update.Message

	// Handle forwarded messages in moderator channel (auto-scan)
	if msg.Chat.ID == tb.cfg.Telegram.ModeratorChannel && msg.ForwardOrigin != nil {
		tb.handleModeratorMessage(ctx, b, update)
		return
	}

	// Only process messages from monitored chats
	if !tb.isMonitored(msg.Chat.ID) {
		return
	}

	// Handle new members
	if len(msg.NewChatMembers) > 0 {
		tb.handleNewMembers(ctx, msg)
		return
	}

	// Skip messages without processable content
	if msg.Text == "" && msg.Caption == "" && msg.Photo == nil && msg.Document == nil && msg.Sticker != nil {
		return
	}

	tb.processMessage(ctx, msg)
}

// processMessage scans a message through Rspamd and handles the result.
func (tb *Bot) processMessage(ctx context.Context, msg *models.Message) {
	tgMsg := buildTelegramMessage(msg)
	tgMsg.IsAdmin = tb.isAdminCached(ctx, tb.bot, msg.Chat.ID, msg.From.ID)

	// Scan with Rspamd
	result, err := tb.rspamd.Check(ctx, tgMsg)
	if err != nil {
		tb.logger.Error("rspamd check failed",
			"message_id", msg.ID,
			"error", err,
		)
		return
	}

	isSpam := result.Score >= tb.cfg.Thresholds.LogScore

	// Store in ClickHouse (symbols are stored by rspamd clickhouse plugin)
	storeMsg := &storage.Message{
		MessageID:        int64(msg.ID),
		ChatID:           msg.Chat.ID,
		UserID:           msg.From.ID,
		Username:         msg.From.Username,
		FirstName:        msg.From.FirstName,
		LastName:         msg.From.LastName,
		Text:             getMessageText(msg),
		MessageType:      getMessageType(msg),
		HasMedia:         msg.Photo != nil || msg.Document != nil || msg.Sticker != nil,
		ReplyToMessageID: getReplyToID(msg),
		ForwardFromID:    getForwardFromID(msg),
		Timestamp:        time.Unix(int64(msg.Date), 0),
		RspamdScore:      float32(result.Score),
		RspamdAction:     result.Action,
		IsSpam:           isSpam,
	}

	if err := tb.storage.Store(ctx, storeMsg); err != nil {
		tb.logger.Error("clickhouse store failed",
			"message_id", msg.ID,
			"error", err,
		)
	}

	// Update channel context for legitimate messages (for GPT module)
	if !isSpam {
		tb.updateChannelContext(ctx, msg)
	}

	// Report to moderator channel if spam
	if isSpam {
		tb.logger.Warn("spam detected",
			"message_id", msg.ID,
			"chat_id", msg.Chat.ID,
			"user_id", msg.From.ID,
			"score", result.Score,
			"action", result.Action,
		)

		if err := tb.reporter.Report(ctx, tgMsg, result); err != nil {
			tb.logger.Error("moderator report failed",
				"message_id", msg.ID,
				"error", err,
			)
		}
	}
}

// handleNewMembers processes new chat member events.
func (tb *Bot) handleNewMembers(ctx context.Context, msg *models.Message) {
	for _, member := range msg.NewChatMembers {
		tgMsg := &rspamd.TelegramMessage{
			MessageID:   int64(msg.ID),
			ChatID:      msg.Chat.ID,
			ChatTitle:   msg.Chat.Title,
			UserID:      member.ID,
			Username:    member.Username,
			FirstName:   member.FirstName,
			LastName:    member.LastName,
			IsBot:       member.IsBot,
			Text:        buildJoinText(&member),
			MessageType: "join",
			Timestamp:   time.Unix(int64(msg.Date), 0),
		}

		result, err := tb.rspamd.Check(ctx, tgMsg)
		if err != nil {
			tb.logger.Error("rspamd check failed for new member",
				"user_id", member.ID,
				"error", err,
			)
			continue
		}

		if result.Score >= tb.cfg.Thresholds.LogScore {
			if err := tb.reporter.Report(ctx, tgMsg, result); err != nil {
				tb.logger.Error("moderator report failed",
					"user_id", member.ID,
					"error", err,
				)
			}
		}
	}
}

// buildTelegramMessage converts a Telegram API message to our internal format.
func buildTelegramMessage(msg *models.Message) *rspamd.TelegramMessage {
	tgMsg := &rspamd.TelegramMessage{
		MessageID:   int64(msg.ID),
		ChatID:      msg.Chat.ID,
		ChatTitle:   msg.Chat.Title,
		UserID:      msg.From.ID,
		Username:    msg.From.Username,
		FirstName:   msg.From.FirstName,
		LastName:    msg.From.LastName,
		IsBot:       msg.From.IsBot,
		Text:        getMessageHTML(msg),
		MessageType: getMessageType(msg),
		HasMedia:    msg.Photo != nil || msg.Document != nil || msg.Sticker != nil,
		ReplyToID:     getReplyToID(msg),
		ReplyToUserID: getReplyToUserID(msg),
		ForwardFrom:   getForwardFromID(msg),
		IsForward:     msg.ForwardOrigin != nil,
		Timestamp:     time.Unix(int64(msg.Date), 0),
	}

	if msg.Document != nil {
		tgMsg.MediaName = msg.Document.FileName
	}

	return tgMsg
}

func getMessageText(msg *models.Message) string {
	if msg.Text != "" {
		return msg.Text
	}
	return msg.Caption
}

func getMessageHTML(msg *models.Message) string {
	if msg.Text != "" {
		return entitiesToHTML(msg.Text, msg.Entities)
	}
	if msg.Caption != "" {
		return entitiesToHTML(msg.Caption, msg.CaptionEntities)
	}
	return ""
}

func getMessageType(msg *models.Message) string {
	switch {
	case len(msg.NewChatMembers) > 0:
		return "join"
	case msg.ForwardOrigin != nil:
		return "forward"
	case msg.Photo != nil:
		return "photo"
	case msg.Document != nil:
		return "document"
	case msg.Sticker != nil:
		return "sticker"
	default:
		return "text"
	}
}

func getReplyToID(msg *models.Message) int64 {
	if msg.ReplyToMessage != nil {
		return int64(msg.ReplyToMessage.ID)
	}
	return 0
}

func getReplyToUserID(msg *models.Message) int64 {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		return msg.ReplyToMessage.From.ID
	}
	return 0
}

func getForwardFromID(msg *models.Message) int64 {
	if msg.ForwardOrigin == nil {
		return 0
	}
	// ForwardOrigin is a union type; try to extract user ID if available
	if msg.ForwardOrigin.MessageOriginUser != nil {
		return msg.ForwardOrigin.MessageOriginUser.SenderUser.ID
	}
	return 0
}

func buildJoinText(user *models.User) string {
	name := user.FirstName
	if user.LastName != "" {
		name += " " + user.LastName
	}
	if user.Username != "" {
		return name + " (@" + user.Username + ") joined the chat"
	}
	return name + " joined the chat"
}
