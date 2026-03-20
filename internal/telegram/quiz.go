package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/vstakhov/rspamd-telegram-bot/internal/quiz"
)

// resolveChannelID resolves a channel name or ID string to a numeric ID.
// Accepts: numeric ID (-1001234), or channel name/title (matched against known channels).
func (tb *Bot) resolveChannelID(ctx context.Context, input string) (int64, error) {
	input = strings.TrimSpace(input)

	// Try numeric ID first
	if id, err := strconv.ParseInt(input, 10, 64); err == nil {
		return id, nil
	}

	// Search by channel title in Redis
	keys, _ := tb.redis.Keys(ctx, "tg_channel:*:info").Result()
	inputLower := strings.ToLower(input)

	for _, key := range keys {
		title, _ := tb.redis.HGet(ctx, key, "title").Result()
		if strings.ToLower(title) == inputLower {
			// Extract channel ID from key: tg_channel:-1001234:info
			parts := strings.Split(key, ":")
			if len(parts) >= 2 {
				if id, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					return id, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("channel '%s' not found (use numeric ID or exact channel name)", input)
}

// handleQuizCommand handles /quiz subcommands in moderator channel.
func (tb *Bot) handleQuizCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	if tb.quiz == nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Quiz system not configured (missing OPENROUTER_API_KEY).",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	arg := extractCommandArg(msg.Text, "/quiz")
	parts := strings.SplitN(arg, " ", 2)
	if len(parts) < 1 || parts[0] == "" {
		tb.sendQuizHelp(ctx, b, msg)
		return
	}

	subcmd := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	switch subcmd {
	case "prompt":
		tb.handleQuizPrompt(ctx, b, msg, rest)
	case "message":
		tb.handleQuizMessage(ctx, b, msg, rest)
	case "mode":
		tb.handleQuizMode(ctx, b, msg, rest)
	case "show":
		tb.handleQuizShow(ctx, b, msg, rest)
	case "test":
		tb.handleQuizTest(ctx, b, msg, rest)
	default:
		tb.sendQuizHelp(ctx, b, msg)
	}
}

func (tb *Bot) sendQuizHelp(ctx context.Context, b *bot.Bot, msg *models.Message) {
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: msg.Chat.ID,
		Text: "Quiz commands:\n" +
			"/quiz prompt <channel> <prompt text> — set LLM prompt for questions\n" +
			"/quiz message <channel> <message text> — set notification ({link}, {user})\n" +
			"/quiz mode <channel> <suspicious|all> — when to quiz\n" +
			"/quiz show <channel> — show current config\n" +
			"/quiz test <channel> — test quiz flow here\n\n" +
			"Modes:\n" +
			"  suspicious — quiz only when spam signals detected (default)\n" +
			"  all — quiz every new user on join",
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleQuizMode(ctx context.Context, b *bot.Bot, msg *models.Message, rest string) {
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) < 2 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /quiz mode <channel> <suspicious|all>",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	channelID, err := tb.resolveChannelID(ctx, parts[0])
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Channel not found: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	mode := strings.TrimSpace(parts[1])
	if err := tb.quiz.SetMode(ctx, channelID, mode); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("Quiz mode set to '%s' for channel %d.", mode, channelID),
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleQuizPrompt(ctx context.Context, b *bot.Bot, msg *models.Message, rest string) {
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) < 2 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /quiz prompt <channel_id> <prompt text>\nExample: /quiz prompt -1001234 Generate a simple question about FreeBSD administration",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	channelID, err := tb.resolveChannelID(ctx, parts[0])
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Channel not found: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	prompt := parts[1]
	if err := tb.quiz.SetPrompt(ctx, channelID, prompt); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("Quiz prompt set for channel %d.", channelID),
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleQuizMessage(ctx context.Context, b *bot.Bot, msg *models.Message, rest string) {
	parts := strings.SplitN(rest, " ", 2)
	if len(parts) < 2 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text: "Usage: /quiz message <channel_id> <message text>\n" +
				"Use {link} for the quiz deep link.\n" +
				"Example: /quiz message -1001234 Please verify yourself: {link}",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	channelID, err := tb.resolveChannelID(ctx, parts[0])
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Channel not found: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	message := parts[1]
	if err := tb.quiz.SetMessage(ctx, channelID, message); err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            fmt.Sprintf("Quiz message set for channel %d.", channelID),
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleQuizShow(ctx context.Context, b *bot.Bot, msg *models.Message, rest string) {
	if strings.TrimSpace(rest) == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /quiz show <channel>",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}
	channelID, err := tb.resolveChannelID(ctx, rest)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Channel not found: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	cfg, err := tb.quiz.GetConfig(ctx, channelID)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("No quiz config for channel %d: %v", channelID, err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Quiz config for channel %d:\n\n", channelID))
	sb.WriteString(fmt.Sprintf("Mode: %s\n\n", cfg.Mode))
	sb.WriteString(fmt.Sprintf("Prompt:\n%s\n\n", cfg.Prompt))
	sb.WriteString(fmt.Sprintf("Message:\n%s\n", cfg.Message))

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            sb.String(),
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleQuizTest(ctx context.Context, b *bot.Bot, msg *models.Message, rest string) {
	if strings.TrimSpace(rest) == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /quiz test <channel>",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}
	channelID, err := tb.resolveChannelID(ctx, rest)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Channel not found: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	cfg, err := tb.quiz.GetConfig(ctx, channelID)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Quiz not configured for channel %d: %v", channelID, err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Generate question
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: msg.Chat.ID,
		Text:   "Generating quiz question...",
	})

	recentQs := tb.quiz.RecentQuestions(ctx, channelID)
	question, err := tb.quizLLM.GenerateQuestion(ctx, cfg.Prompt, recentQs)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed to generate question: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	tb.quiz.TrackQuestion(ctx, channelID, question)

	// Create test session (user = sender for testing)
	token, err := tb.quiz.CreateSession(ctx, msg.From.ID, channelID, question)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("Failed to create session: %v", err),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Build deep link
	botUser, _ := b.GetMe(ctx)
	link := fmt.Sprintf("https://t.me/%s?start=quiz_%s", botUser.Username, token)

	// Show what would be posted in channel
	channelMsg := strings.ReplaceAll(cfg.Message, "{link}", link)

	var sb strings.Builder
	sb.WriteString("Quiz test:\n\n")
	sb.WriteString(fmt.Sprintf("Generated question: %s\n\n", question))
	sb.WriteString(fmt.Sprintf("Channel message would be:\n%s\n\n", channelMsg))
	sb.WriteString(fmt.Sprintf("Click to test: %s", link))

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            sb.String(),
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

// handleStartCommand handles /start in private chat (deep links for quizzes).
func (tb *Bot) handleStartCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.Type != "private" {
		return
	}

	arg := extractCommandArg(msg.Text, "/start")
	if !strings.HasPrefix(arg, "quiz_") {
		return
	}

	if tb.quiz == nil || tb.quizLLM == nil {
		return
	}

	token := strings.TrimPrefix(arg, "quiz_")
	session, err := tb.quiz.GetSession(ctx, token)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   "This quiz link has expired or is invalid.",
		})
		return
	}

	if session.Status != "pending" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   fmt.Sprintf("This quiz has already been completed. Status: %s", session.Status),
		})
		return
	}

	// Verify user matches session
	if session.UserID != msg.From.ID {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   "This quiz was created for a different user.",
		})
		return
	}

	// Lazy question generation — only when user clicks the link
	if session.Question == "" {
		cfg, err := tb.quiz.GetConfig(ctx, session.ChannelID)
		if err != nil {
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: msg.Chat.ID,
				Text:   "Quiz configuration error.",
			})
			return
		}

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   "Generating your question...",
		})

		recentQs := tb.quiz.RecentQuestions(ctx, session.ChannelID)
		question, err := tb.quizLLM.GenerateQuestion(ctx, cfg.Prompt, recentQs)
		if err != nil {
			tb.logger.Error("quiz question generation failed", "error", err)
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: msg.Chat.ID,
				Text:   "Failed to generate question. Please try again.",
			})
			return
		}

		session.Question = question
		tb.quiz.UpdateSession(ctx, session)
		tb.quiz.TrackQuestion(ctx, session.ChannelID, question)
	}

	// Send the question
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: msg.Chat.ID,
		Text:   fmt.Sprintf("Please answer the following question to verify yourself:\n\n%s\n\nYou have 1 minute to answer.", session.Question),
	})

	// Mark session as waiting for answer
	userStateKey := fmt.Sprintf("tg_quiz_answer_pending:%d", msg.From.ID)
	tb.redis.Set(ctx, userStateKey, token, 1*time.Minute)

	// Start timeout — auto-fail after 1 minute
	go func() {
		time.Sleep(1 * time.Minute)

		// Check if still pending
		s, err := tb.quiz.GetSession(context.Background(), token)
		if err != nil || s.Status != "pending" {
			return // already answered
		}

		s.Status = "failed"
		s.Result = "timeout — no answer within 1 minute"
		tb.quiz.UpdateSession(context.Background(), s)

		// Clean up pending state
		tb.redis.Del(context.Background(), userStateKey)

		tb.quizFail(context.Background(), b, s, msg.Chat.ID, "timeout — no answer within 1 minute")
		tb.logger.Info("quiz timed out", "user_id", s.UserID, "token", token)
	}()
}

// handlePrivateMessage handles answers to quiz questions in private chat.
func (tb *Bot) handlePrivateMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.Type != "private" || tb.quiz == nil {
		return
	}

	// Check if user has a pending quiz answer
	userStateKey := fmt.Sprintf("tg_quiz_answer_pending:%d", msg.From.ID)
	token, err := tb.redis.Get(ctx, userStateKey).Result()
	if err != nil {
		return // no pending quiz
	}

	session, err := tb.quiz.GetSession(ctx, token)
	if err != nil || session.Status != "pending" {
		return
	}

	// Get channel prompt for evaluation context
	cfg, err := tb.quiz.GetConfig(ctx, session.ChannelID)
	if err != nil {
		return
	}

	// Evaluate answer
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: msg.Chat.ID,
		Text:   "Evaluating your answer...",
	})

	verdict, reason, err := tb.quizLLM.EvaluateAnswer(ctx, session.Question, msg.Text, cfg.Prompt)
	if err != nil {
		tb.logger.Error("quiz answer evaluation failed", "error", err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   "Failed to evaluate your answer. Please try again.",
		})
		return
	}

	// Update session
	session.Answer = msg.Text
	session.Status = verdict + "ed"
	session.Result = reason
	tb.quiz.UpdateSession(ctx, session)

	// Clean up pending state
	tb.redis.Del(ctx, userStateKey)

	if verdict == "pass" {
		// Store pass in profile
		profileKey := fmt.Sprintf("tg_profile:%d", session.UserID)
		tb.redis.HSet(ctx, profileKey, "quiz_result", "pass")
		tb.redis.HSet(ctx, profileKey, "quiz_reason", reason)

		// Unrestrict user in channel
		b.RestrictChatMember(ctx, &bot.RestrictChatMemberParams{
			ChatID: session.ChannelID,
			UserID: session.UserID,
			Permissions: &models.ChatPermissions{
				CanSendMessages:       true,
				CanSendAudios:         true,
				CanSendDocuments:      true,
				CanSendPhotos:         true,
				CanSendVideos:         true,
				CanSendVideoNotes:     true,
				CanSendVoiceNotes:     true,
				CanSendPolls:          true,
				CanSendOtherMessages:  true,
				CanAddWebPagePreviews: true,
				CanInviteUsers:        true,
			},
		})

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   fmt.Sprintf("You passed the verification!\n\nReason: %s", reason),
		})

		// Notify moderator channel
		tb.sendHTML(ctx, b, tb.cfg.Telegram.ModeratorChannel,
			fmt.Sprintf("<b>Quiz passed:</b> %s (@%s)\n<b>Q:</b> %s\n<b>A:</b> %s\n<b>Reason:</b> %s",
				adminEscapeHTML(msg.From.FirstName),
				adminEscapeHTML(msg.From.Username),
				adminEscapeHTML(session.Question),
				adminEscapeHTML(msg.Text),
				adminEscapeHTML(reason),
			), 0)

		// Delete channel quiz message
		if session.MessageID != 0 {
			b.DeleteMessage(ctx, &bot.DeleteMessageParams{
				ChatID:    session.ChannelID,
				MessageID: session.MessageID,
			})
		}

		tb.logger.Info("quiz passed", "user_id", session.UserID, "channel_id", session.ChannelID)
	} else {
		tb.quizFail(ctx, b, session, msg.Chat.ID,
			fmt.Sprintf("wrong answer: %s", reason))
	}
}

// quizFail handles quiz failure: ban user, delete channel message, notify.
func (tb *Bot) quizFail(ctx context.Context, b *bot.Bot, session *quiz.Session, privateChatID int64, reason string) {
	// Store fail in profile
	profileKey := fmt.Sprintf("tg_profile:%d", session.UserID)
	tb.redis.HSet(ctx, profileKey, "quiz_result", "fail")
	tb.redis.HSet(ctx, profileKey, "quiz_reason", reason)

	// Notify user in private chat
	if privateChatID != 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: privateChatID,
			Text:   fmt.Sprintf("Verification failed: %s\nYou have been banned from the channel.", reason),
		})
	}

	// Ban user from channel
	_, err := b.BanChatMember(ctx, &bot.BanChatMemberParams{
		ChatID: session.ChannelID,
		UserID: session.UserID,
	})
	if err != nil {
		tb.logger.Warn("quiz ban failed", "user_id", session.UserID, "channel_id", session.ChannelID, "error", err)
	} else {
		tb.logger.Info("quiz ban applied", "user_id", session.UserID, "channel_id", session.ChannelID)
	}

	// Delete channel quiz message
	if session.MessageID != 0 {
		b.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    session.ChannelID,
			MessageID: session.MessageID,
		})
	}

	// Notify moderators
	tb.sendHTML(ctx, b, tb.cfg.Telegram.ModeratorChannel,
		fmt.Sprintf("<b>Quiz failed + banned:</b> user %d\n<b>Reason:</b> %s",
			session.UserID, adminEscapeHTML(reason)), 0)
}

// TriggerQuiz initiates a quiz for a suspicious user in a channel.
// Called from processMessage when spam indicators are detected.
// Question is NOT generated here — it's generated lazily when user clicks the link.
func (tb *Bot) TriggerQuiz(ctx context.Context, b *bot.Bot, channelID int64, user *models.User) {
	if tb.quiz == nil || tb.quizLLM == nil {
		return
	}

	// Check if quiz is configured for this channel
	cfg, err := tb.quiz.GetConfig(ctx, channelID)
	if err != nil {
		return
	}

	// Check if user already has active quiz
	if tb.quiz.HasActiveQuiz(ctx, channelID, user.ID) {
		return
	}

	// Create session without question (lazy generation on click)
	token, err := tb.quiz.CreateSession(ctx, user.ID, channelID, "")
	if err != nil {
		tb.logger.Error("quiz session creation failed", "error", err)
		return
	}

	// Build deep link
	botUser, _ := b.GetMe(ctx)
	link := fmt.Sprintf("https://t.me/%s?start=quiz_%s", botUser.Username, token)

	// Post in channel
	channelMsg := strings.ReplaceAll(cfg.Message, "{link}", link)

	// Mention user
	userMention := user.FirstName
	if user.Username != "" {
		userMention = "@" + user.Username
	}
	channelMsg = strings.ReplaceAll(channelMsg, "{user}", userMention)

	sent, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: channelID,
		Text:   channelMsg,
	})
	if err != nil {
		tb.logger.Error("quiz channel message failed", "error", err)
		return
	}

	// Store message ID for later deletion
	tb.quiz.SetChannelMessageID(ctx, token, sent.ID)

	// Schedule message deletion after 2 minutes (quiz timeout is 1 min + buffer)
	go func() {
		time.Sleep(2 * time.Minute)
		b.DeleteMessage(context.Background(), &bot.DeleteMessageParams{
			ChatID:    channelID,
			MessageID: sent.ID,
		})
	}()

	tb.logger.Info("quiz triggered",
		"user_id", user.ID,
		"channel_id", channelID,
		"token", token,
	)
}
