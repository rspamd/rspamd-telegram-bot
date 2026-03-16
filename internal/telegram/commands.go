package telegram

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// handleSpamCommand handles the /spam command (reply to a message to train it as spam).
func (tb *Bot) handleSpamCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	// Only allow in monitored chats
	if !tb.isMonitored(msg.Chat.ID) {
		return
	}

	// Must be a reply to another message
	if msg.ReplyToMessage == nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Reply to a message with /spam to mark it as spam.",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Check if user is admin
	isAdmin, err := tb.isAdmin(ctx, b, msg.Chat.ID, msg.From.ID)
	if err != nil {
		tb.logger.Error("failed to check admin status", "error", err)
		return
	}
	if !isAdmin {
		return
	}

	target := msg.ReplyToMessage
	tgMsg := buildTelegramMessage(target)

	// Learn as spam
	if err := tb.rspamd.LearnSpam(ctx, tgMsg); err != nil {
		tb.logger.Error("learn spam failed",
			"message_id", target.ID,
			"error", err,
		)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed to learn spam: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Add fuzzy hash
	if err := tb.rspamd.FuzzyAdd(ctx, tgMsg); err != nil {
		tb.logger.Error("fuzzy add failed",
			"message_id", target.ID,
			"error", err,
		)
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            "Message learned as spam (Bayes + fuzzy hash added).",
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})

	tb.logger.Info("spam training completed",
		"message_id", target.ID,
		"trained_by", msg.From.ID,
	)
}

// handleHamCommand handles the /ham command (reply to a message to train it as ham).
func (tb *Bot) handleHamCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	if !tb.isMonitored(msg.Chat.ID) {
		return
	}

	if msg.ReplyToMessage == nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Reply to a message with /ham to mark it as ham.",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	isAdmin, err := tb.isAdmin(ctx, b, msg.Chat.ID, msg.From.ID)
	if err != nil {
		tb.logger.Error("failed to check admin status", "error", err)
		return
	}
	if !isAdmin {
		return
	}

	target := msg.ReplyToMessage
	tgMsg := buildTelegramMessage(target)

	// Learn as ham
	if err := tb.rspamd.LearnHam(ctx, tgMsg); err != nil {
		tb.logger.Error("learn ham failed",
			"message_id", target.ID,
			"error", err,
		)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed to learn ham: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Remove fuzzy hash if exists
	if err := tb.rspamd.FuzzyDel(ctx, tgMsg); err != nil {
		tb.logger.Warn("fuzzy del failed (may not exist)",
			"message_id", target.ID,
			"error", err,
		)
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            "Message learned as ham (Bayes trained + fuzzy hash removed).",
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})

	tb.logger.Info("ham training completed",
		"message_id", target.ID,
		"trained_by", msg.From.ID,
	)
}

// handleChatIDCommand replies with the current chat's ID. Works in any chat,
// no configuration required — use this to discover IDs for config.yml.
func (tb *Bot) handleChatIDCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	text := fmt.Sprintf("Chat ID: <code>%d</code>\nChat title: %s\nChat type: %s",
		msg.Chat.ID,
		msg.Chat.Title,
		msg.Chat.Type,
	)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            text,
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

// isAdmin checks if a user is an admin/creator in the given chat.
func (tb *Bot) isAdmin(ctx context.Context, b *bot.Bot, chatID int64, userID int64) (bool, error) {
	member, err := b.GetChatMember(ctx, &bot.GetChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
	if err != nil {
		return false, fmt.Errorf("get chat member: %w", err)
	}

	// member is a *models.ChatMember which has a Type field
	switch member.Type {
	case models.ChatMemberTypeAdministrator, models.ChatMemberTypeOwner:
		return true, nil
	default:
		return false, nil
	}
}
