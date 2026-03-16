package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (tb *Bot) handleUserCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	arg := extractCommandArg(msg.Text, "/userinfo")
	if arg == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /userinfo <@username or user_id>",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Resolve user ID
	var userID string
	if strings.HasPrefix(arg, "@") {
		// Lookup by username
		username := strings.ToLower(strings.TrimPrefix(arg, "@"))
		key := fmt.Sprintf("tg_username:%s", username)
		val, err := tb.redis.Get(ctx, key).Result()
		if err != nil {
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:          msg.Chat.ID,
				Text:            fmt.Sprintf("User @%s not found in profiles.", username),
				ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
			})
			return
		}
		userID = val
	} else {
		userID = arg
	}

	// Fetch profile
	profileKey := fmt.Sprintf("tg_profile:%s", userID)
	profile, err := tb.redis.HGetAll(ctx, profileKey).Result()
	if err != nil || len(profile) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("No profile data for user %s.", userID),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Fetch last messages
	msgListKey := fmt.Sprintf("tg_profile:%s:messages", userID)
	messages, _ := tb.redis.LRange(ctx, msgListKey, 0, 9).Result()

	// Fetch contact count
	contactsKey := fmt.Sprintf("tg_profile:%s:contacts", userID)
	contactCount, _ := tb.redis.SCard(ctx, contactsKey).Result()

	// Fetch channels
	channelsKey := fmt.Sprintf("tg_profile:%s:channels", userID)
	channelIDs, _ := tb.redis.SMembers(ctx, channelsKey).Result()

	// Resolve channel titles
	var channels []channelInfo
	for _, chID := range channelIDs {
		infoKey := fmt.Sprintf("tg_channel:%s:info", chID)
		info, _ := tb.redis.HGetAll(ctx, infoKey).Result()
		channels = append(channels, channelInfo{ID: chID, Title: info["title"]})
	}

	// Format output
	text := formatProfileReport(userID, profile, messages, contactCount, channels)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            text,
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleChannelsCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	// Get all channels sorted by last activity (most recent first)
	channels, err := tb.redis.ZRevRangeWithScores(ctx, "tg_channels", 0, 49).Result()
	if err != nil || len(channels) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "No channels tracked yet.",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	var sb strings.Builder
	sb.WriteString("<b>Tracked channels:</b>\n\n")

	for i, ch := range channels {
		chatID := ch.Member.(string)
		lastSeen := time.Unix(int64(ch.Score), 0)

		// Fetch channel info
		infoKey := fmt.Sprintf("tg_channel:%s:info", chatID)
		info, _ := tb.redis.HGetAll(ctx, infoKey).Result()

		title := info["title"]
		if title == "" {
			title = chatID
		}
		msgCount := info["msg_count"]
		if msgCount == "" {
			msgCount = "0"
		}

		// Count users
		usersKey := fmt.Sprintf("tg_channel:%s:users", chatID)
		userCount, _ := tb.redis.ZCard(ctx, usersKey).Result()

		sb.WriteString(fmt.Sprintf("%d. <b>%s</b>\n", i+1, adminEscapeHTML(title)))
		sb.WriteString(fmt.Sprintf("   ID: <code>%s</code>\n", chatID))
		sb.WriteString(fmt.Sprintf("   Messages: %s | Users: %d\n", msgCount, userCount))
		sb.WriteString(fmt.Sprintf("   Last activity: %s\n", lastSeen.UTC().Format("2006-01-02 15:04")))
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            sb.String(),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

func (tb *Bot) handleUsersCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Chat.ID != tb.cfg.Telegram.ModeratorChannel {
		return
	}

	chatID := extractCommandArg(msg.Text, "/users")
	if chatID == "" {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Usage: /users <channel_id>",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Get top users in channel sorted by message count (descending)
	usersKey := fmt.Sprintf("tg_channel:%s:users", chatID)
	users, err := tb.redis.ZRevRangeWithScores(ctx, usersKey, 0, 29).Result()
	if err != nil || len(users) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            fmt.Sprintf("No users found for channel %s.", chatID),
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		return
	}

	// Get channel title
	infoKey := fmt.Sprintf("tg_channel:%s:info", chatID)
	info, _ := tb.redis.HGetAll(ctx, infoKey).Result()
	title := info["title"]
	if title == "" {
		title = chatID
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>Users in %s</b>\n\n", adminEscapeHTML(title)))

	for i, u := range users {
		uid := u.Member.(string)
		msgCount := int(u.Score)

		// Fetch user profile
		profileKey := fmt.Sprintf("tg_profile:%s", uid)
		profile, _ := tb.redis.HGetAll(ctx, profileKey).Result()

		name := profile["first_name"]
		if profile["last_name"] != "" {
			name += " " + profile["last_name"]
		}
		if name == "" {
			name = uid
		}

		username := ""
		if profile["username"] != "" {
			username = fmt.Sprintf(" (@%s)", profile["username"])
		}

		sb.WriteString(fmt.Sprintf("%d. %s%s — %d msgs\n",
			i+1, adminEscapeHTML(name), adminEscapeHTML(username), msgCount))
	}

	totalUsers, _ := tb.redis.ZCard(ctx, usersKey).Result()
	if totalUsers > 30 {
		sb.WriteString(fmt.Sprintf("\n... and %d more users\n", totalUsers-30))
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            sb.String(),
		ParseMode:       models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
}

type channelInfo struct {
	ID    string
	Title string
}

func formatProfileReport(userID string, profile map[string]string, messages []string, contacts int64, channels []channelInfo) string {
	var sb strings.Builder

	sb.WriteString("<b>User Profile</b>\n\n")

	// Basic info
	name := adminEscapeHTML(profile["first_name"])
	if profile["last_name"] != "" {
		name += " " + adminEscapeHTML(profile["last_name"])
	}
	sb.WriteString(fmt.Sprintf("<b>Name:</b> %s\n", name))

	if profile["username"] != "" {
		sb.WriteString(fmt.Sprintf("<b>Username:</b> @%s\n", adminEscapeHTML(profile["username"])))
	}
	sb.WriteString(fmt.Sprintf("<b>User ID:</b> <code>%s</code>\n", userID))

	// Activity stats
	msgCount := profile["msg_count"]
	if msgCount == "" {
		msgCount = "0"
	}
	sb.WriteString(fmt.Sprintf("<b>Messages:</b> %s\n", msgCount))
	sb.WriteString(fmt.Sprintf("<b>Contacts:</b> %d\n", contacts))

	adminReplies := profile["admin_replies"]
	if adminReplies == "" {
		adminReplies = "0"
	}
	sb.WriteString(fmt.Sprintf("<b>Admin endorsements:</b> %s\n", adminReplies))

	// Timestamps
	if firstSeen, err := strconv.ParseInt(profile["first_seen"], 10, 64); err == nil {
		t := time.Unix(firstSeen, 0)
		age := time.Since(t)
		sb.WriteString(fmt.Sprintf("<b>First seen:</b> %s (%.0f days ago)\n",
			t.UTC().Format("2006-01-02 15:04"), age.Hours()/24))
	}
	if lastSeen, err := strconv.ParseInt(profile["last_seen"], 10, 64); err == nil {
		t := time.Unix(lastSeen, 0)
		sb.WriteString(fmt.Sprintf("<b>Last seen:</b> %s\n", t.UTC().Format("2006-01-02 15:04")))
	}

	// Reputation estimate
	sb.WriteString("\n<b>Reputation:</b> ")
	mc, _ := strconv.Atoi(msgCount)
	ar, _ := strconv.Atoi(adminReplies)
	firstSeen, _ := strconv.ParseInt(profile["first_seen"], 10, 64)
	ageDays := float64(time.Since(time.Unix(firstSeen, 0))) / float64(24*time.Hour)

	switch {
	case ageDays >= 30 && mc >= 100:
		sb.WriteString("established (-5.0)")
	case mc >= 50:
		sb.WriteString("high volume (-4.0)")
	case ageDays >= 7 && mc >= 20:
		sb.WriteString("active (-3.0)")
	case mc >= 10:
		sb.WriteString("regular (-2.0)")
	case ageDays >= 1 && mc >= 5:
		sb.WriteString("known (-1.0)")
	default:
		sb.WriteString("new (0)")
	}
	if ar > 0 {
		bonus := ar
		if bonus > 3 {
			bonus = 3
		}
		sb.WriteString(fmt.Sprintf(" + admin bonus (-%d.0)", bonus))
	}
	sb.WriteString("\n")

	// GPT verdict
	if gptVerdict := profile["gpt_verdict"]; gptVerdict != "" {
		sb.WriteString(fmt.Sprintf("\n<b>GPT verdict:</b>\n<code>%s</code>\n", adminEscapeHTML(gptVerdict)))
	}

	// Channels
	if len(channels) > 0 {
		sb.WriteString(fmt.Sprintf("\n<b>Channels (%d):</b>\n", len(channels)))
		for _, ch := range channels {
			title := ch.Title
			if title == "" {
				title = ch.ID
			}
			sb.WriteString(fmt.Sprintf("  %s (<code>%s</code>)\n", adminEscapeHTML(title), ch.ID))
		}
	}

	// Last messages
	if len(messages) > 0 {
		sb.WriteString("\n<b>Last messages:</b>\n")
		for i, m := range messages {
			truncated := m
			if len(truncated) > 100 {
				truncated = truncated[:100] + "..."
			}
			sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", i+1, adminEscapeHTML(truncated)))
		}
	}

	return sb.String()
}
