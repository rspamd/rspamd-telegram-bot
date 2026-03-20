package telegram

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
	"github.com/vstakhov/rspamd-telegram-bot/internal/storage"
)

// formatSymbolList returns a compact string of non-zero symbols for notifications.
func formatSymbolList(result *rspamd.CheckResult) string {
	type sym struct {
		name  string
		score float64
	}
	var syms []sym
	for name, s := range result.Symbols {
		if s.Score != 0 {
			syms = append(syms, sym{name, s.Score})
		}
	}
	sort.Slice(syms, func(i, j int) bool {
		return syms[i].score > syms[j].score
	})
	var parts []string
	for _, s := range syms {
		parts = append(parts, fmt.Sprintf("%s(%.1f)", s.name, s.score))
	}
	return strings.Join(parts, ", ")
}

// handleUpdate is the default handler for all updates.
func (tb *Bot) handleUpdate(ctx context.Context, b *bot.Bot, update *models.Update) {
	// Handle ChatMember updates (joins via invite links, shared folders, etc.)
	if update.ChatMember != nil {
		tb.handleChatMemberUpdate(ctx, b, update.ChatMember)
		return
	}

	if update.Message == nil {
		return
	}

	msg := update.Message

	// Handle private messages (quiz answers)
	if msg.Chat.Type == "private" {
		tb.handlePrivateMessage(ctx, b, update)
		return
	}

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
	tgMsg.UserpicRisk = -1 // not analyzed by default

	// Check userpic for new users (no profile in Redis yet)
	if tb.userpic != nil && tb.userpic.Enabled() && msg.From != nil {
		tb.analyzeUserpicIfNew(ctx, msg.From, tgMsg)
	}

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

		// Auto-ban if score >= reject threshold
		if result.Score >= tb.cfg.Thresholds.RejectScore {
			tb.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
				ChatID:    msg.Chat.ID,
				MessageID: msg.ID,
			})
			tb.bot.BanChatMember(ctx, &bot.BanChatMemberParams{
				ChatID: msg.Chat.ID,
				UserID: msg.From.ID,
			})
			tb.logger.Info("auto-banned for spam message",
				"user_id", msg.From.ID,
				"score", result.Score,
			)
		} else if tb.quiz != nil && tb.quiz.IsConfigured(ctx, msg.Chat.ID) {
			// Score above log but below reject — restrict + quiz
			// Delete the spam message
			tb.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
				ChatID:    msg.Chat.ID,
				MessageID: msg.ID,
			})
			// Restrict user to read-only
			tb.bot.RestrictChatMember(ctx, &bot.RestrictChatMemberParams{
				ChatID: msg.Chat.ID,
				UserID: msg.From.ID,
				Permissions: &models.ChatPermissions{
					CanSendMessages: false,
				},
			})
			// Trigger quiz
			tb.TriggerQuiz(ctx, tb.bot, msg.Chat.ID, msg.From)
			tb.logger.Info("restricted + quiz triggered for spam",
				"user_id", msg.From.ID,
				"score", result.Score,
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

		// Trigger quiz on join if configured for this channel
		if tb.quiz != nil && !member.IsBot {
			cfg, err := tb.quiz.GetConfig(ctx, msg.Chat.ID)
			if err == nil && cfg.Mode == "all" {
				tb.TriggerQuiz(ctx, tb.bot, msg.Chat.ID, &member)
			}
		}
	}
}

// handleChatMemberUpdate handles ChatMemberUpdated events.
// This catches joins via invite links, shared folders, join requests — methods that
// don't generate NewChatMembers messages.
func (tb *Bot) handleChatMemberUpdate(ctx context.Context, b *bot.Bot, cmu *models.ChatMemberUpdated) {
	if !tb.isMonitored(cmu.Chat.ID) {
		return
	}

	// Only interested in new member joins (was not member, now is member)
	oldStatus := cmu.OldChatMember.Type
	newStatus := cmu.NewChatMember.Type

	isJoin := (oldStatus == models.ChatMemberTypeLeft || oldStatus == models.ChatMemberTypeBanned || oldStatus == "") &&
		(newStatus == models.ChatMemberTypeMember || newStatus == models.ChatMemberTypeAdministrator)

	if !isJoin {
		return
	}

	user := cmu.From
	if user.IsBot {
		return
	}

	tb.logger.Info("chat member join detected",
		"user_id", user.ID,
		"username", user.Username,
		"first_name", user.FirstName,
		"chat_id", cmu.Chat.ID,
		"via_folder", cmu.ViaChatFolderInviteLink,
		"via_join_request", cmu.ViaJoinRequest,
	)

	// ViaChatFolderInviteLink is a strong spam signal — store in profile
	if cmu.ViaChatFolderInviteLink {
		profileKey := fmt.Sprintf("tg_profile:%d", user.ID)
		tb.redis.HSet(ctx, profileKey, "joined_via_folder", "true")
		tb.logger.Warn("user joined via shared folder link (spam indicator)",
			"user_id", user.ID,
			"username", user.Username,
			"chat_id", cmu.Chat.ID,
		)
	}

	// Scan join event through rspamd
	tgMsg := &rspamd.TelegramMessage{
		ChatID:          cmu.Chat.ID,
		ChatTitle:       cmu.Chat.Title,
		UserID:          user.ID,
		Username:        user.Username,
		FirstName:       user.FirstName,
		LastName:        user.LastName,
		IsBot:           user.IsBot,
		IsPremium:       user.IsPremium,
		JoinedViaFolder: cmu.ViaChatFolderInviteLink,
		Text:            buildJoinText(&user),
		MessageType:     "join",
		UserpicRisk:     -1,
	}

	// Analyze userpic for new joiners
	if tb.userpic != nil && tb.userpic.Enabled() {
		tb.analyzeUserpicIfNew(ctx, &user, tgMsg)
	}

	result, err := tb.rspamd.Check(ctx, tgMsg)
	if err != nil {
		tb.logger.Error("rspamd check failed for chat member join",
			"user_id", user.ID,
			"error", err,
		)
		return
	}

	// Auto-ban if score >= reject threshold (folder spammers, etc.)
	if result.Score >= tb.cfg.Thresholds.RejectScore {
		_, banErr := b.BanChatMember(ctx, &bot.BanChatMemberParams{
			ChatID: cmu.Chat.ID,
			UserID: user.ID,
		})
		if banErr != nil {
			tb.logger.Error("auto-ban failed", "user_id", user.ID, "error", banErr)
		} else {
			tb.logger.Info("auto-banned on join", "user_id", user.ID, "score", result.Score)
		}

		tb.sendHTML(ctx, b, tb.cfg.Telegram.ModeratorChannel,
			fmt.Sprintf("<b>Auto-banned on join:</b> %s (@%s)\n<b>Score:</b> %.1f\n<b>Reason:</b> %s",
				adminEscapeHTML(user.FirstName),
				adminEscapeHTML(user.Username),
				result.Score,
				formatSymbolList(result),
			), 0)
		return
	}

	if result.Score >= tb.cfg.Thresholds.LogScore {
		if err := tb.reporter.Report(ctx, tgMsg, result); err != nil {
			tb.logger.Error("moderator report failed", "user_id", user.ID, "error", err)
		}
	}

	// Trigger quiz on join if configured
	if tb.quiz != nil {
		cfg, err := tb.quiz.GetConfig(ctx, cmu.Chat.ID)
		if err == nil && cfg.Mode == "all" {
			tb.TriggerQuiz(ctx, b, cmu.Chat.ID, &user)
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
		IsPremium:   msg.From.IsPremium,
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
