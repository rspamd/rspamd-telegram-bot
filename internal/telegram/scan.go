package telegram

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
)

// handleModeratorMessage handles messages in the moderator channel.
// Forwarded messages are automatically scanned and results displayed.
func (tb *Bot) handleModeratorMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	// Only handle forwarded messages (not commands)
	if msg.ForwardOrigin == nil {
		return
	}
	if strings.HasPrefix(msg.Text, "/") {
		return
	}

	tgMsg := buildForwardedMessage(msg)

	// Scan with rspamd
	result, err := tb.rspamd.Check(ctx, tgMsg)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Scan failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Format detailed report
	text := formatScanReport(tgMsg, result)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            text,
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

// handleTrainSpam trains a forwarded message as spam (reply with /trainspam).
func (tb *Bot) handleTrainSpam(ctx context.Context, b *bot.Bot, update *models.Update) {
	tb.handleTrain(ctx, b, update, "spam")
}

// handleTrainHam trains a forwarded message as ham (reply with /trainham).
func (tb *Bot) handleTrainHam(ctx context.Context, b *bot.Bot, update *models.Update) {
	tb.handleTrain(ctx, b, update, "ham")
}

func (tb *Bot) handleTrain(ctx context.Context, b *bot.Bot, update *models.Update, class string) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	if msg.ReplyToMessage == nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Reply to a message with /train%s to train it.", class),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	target := msg.ReplyToMessage
	tgMsg := buildForwardedMessage(target)

	// Neural learn
	if err := tb.rspamd.NeuralLearn(ctx, tgMsg, class); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Neural learn failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Also fuzzy for spam
	if class == "spam" {
		if err := tb.rspamd.FuzzyAdd(ctx, tgMsg); err != nil {
			tb.logger.Warn("fuzzy add failed on train", "error", err)
		}
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("Trained as <b>%s</b> (neural + fuzzy).", class),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

// buildForwardedMessage creates a TelegramMessage from a forwarded/moderator message.
func buildForwardedMessage(msg *models.Message) *rspamd.TelegramMessage {
	text := ""
	if msg.Text != "" {
		text = entitiesToHTML(msg.Text, msg.Entities)
	} else if msg.Caption != "" {
		text = entitiesToHTML(msg.Caption, msg.CaptionEntities)
	}

	tgMsg := &rspamd.TelegramMessage{
		MessageID:   int64(msg.ID),
		Text:        text,
		MessageType: getMessageType(msg),
		HasMedia:    msg.Photo != nil || msg.Document != nil || msg.Sticker != nil,
		IsForward:   msg.ForwardOrigin != nil,
	}

	// Extract original sender info from forward origin
	if msg.ForwardOrigin != nil {
		switch {
		case msg.ForwardOrigin.MessageOriginUser != nil:
			user := msg.ForwardOrigin.MessageOriginUser.SenderUser
			tgMsg.UserID = user.ID
			tgMsg.Username = user.Username
			tgMsg.FirstName = user.FirstName
			tgMsg.LastName = user.LastName
			tgMsg.IsBot = user.IsBot
		case msg.ForwardOrigin.MessageOriginHiddenUser != nil:
			tgMsg.FirstName = msg.ForwardOrigin.MessageOriginHiddenUser.SenderUserName
		case msg.ForwardOrigin.MessageOriginChat != nil:
			chat := msg.ForwardOrigin.MessageOriginChat.SenderChat
			tgMsg.ChatID = chat.ID
			tgMsg.ChatTitle = chat.Title
			tgMsg.FirstName = chat.Title
		}
	}
	// Fallback: no sender info available (hidden user with no name)
	if tgMsg.FirstName == "" && tgMsg.UserID == 0 {
		tgMsg.FirstName = "Unknown (forwarded)"
	}

	if msg.Chat.ID != 0 {
		tgMsg.ChatID = msg.Chat.ID
		tgMsg.ChatTitle = msg.Chat.Title
	}

	return tgMsg
}

func formatScanReport(msg *rspamd.TelegramMessage, result *rspamd.CheckResult) string {
	var sb strings.Builder

	sb.WriteString("<b>Scan Result</b>\n\n")

	// Sender info
	if msg.FirstName != "" {
		sb.WriteString(fmt.Sprintf("<b>From:</b> %s", adminEscapeHTML(msg.FirstName)))
		if msg.LastName != "" {
			sb.WriteString(" " + adminEscapeHTML(msg.LastName))
		}
		if msg.Username != "" {
			sb.WriteString(fmt.Sprintf(" (@%s)", adminEscapeHTML(msg.Username)))
		}
		sb.WriteString("\n")
	}

	// Score and action
	sb.WriteString(fmt.Sprintf("<b>Score:</b> %.2f / %.2f\n", result.Score, result.RequiredScore))
	sb.WriteString(fmt.Sprintf("<b>Action:</b> %s\n", adminEscapeHTML(result.Action)))

	// Symbols sorted by absolute score
	type symEntry struct {
		name  string
		score float64
		opts  string
	}
	var syms []symEntry
	for name, s := range result.Symbols {
		opts := ""
		if len(s.Options) > 0 {
			opts = strings.Join(s.Options, ", ")
		}
		syms = append(syms, symEntry{name, s.Score, opts})
	}
	sort.Slice(syms, func(i, j int) bool {
		ai, aj := syms[i].score, syms[j].score
		if ai < 0 {
			ai = -ai
		}
		if aj < 0 {
			aj = -aj
		}
		return ai > aj
	})

	if len(syms) > 0 {
		sb.WriteString("\n<b>Symbols:</b>\n")
		for _, s := range syms {
			if s.score == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("  <code>%s</code> (%.2f)", s.name, s.score))
			if s.opts != "" {
				truncated := s.opts
				if len(truncated) > 80 {
					truncated = truncated[:80] + "..."
				}
				sb.WriteString(fmt.Sprintf(" — %s", adminEscapeHTML(truncated)))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\nReply with /trainspam or /trainham to train neural.")

	return sb.String()
}
