package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (tb *Bot) handleHelpCommand(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	// Get rspamd version
	rspamdVersion := "unknown"
	if ver, err := tb.rspamd.Version(ctx); err == nil {
		rspamdVersion = ver
	}

	var sb strings.Builder
	sb.WriteString("<b>Rspamd Telegram Bot</b>\n")
	sb.WriteString(fmt.Sprintf("Rspamd: %s\n\n", adminEscapeHTML(rspamdVersion)))

	sb.WriteString("<b>Anywhere:</b>\n")
	sb.WriteString("/chatid — show chat ID\n")
	sb.WriteString("/help — this message\n")

	sb.WriteString("\n<b>Monitored chats (admin only):</b>\n")
	sb.WriteString("/spam — reply to train as spam\n")
	sb.WriteString("/ham — reply to train as ham\n")

	sb.WriteString("\n<b>Moderator channel:</b>\n")
	sb.WriteString("Forward a message — auto-scan and show results\n")
	sb.WriteString("/trainspam — reply to train as spam (neural + fuzzy)\n")
	sb.WriteString("/trainham — reply to train as ham (neural)\n")
	sb.WriteString("/userinfo @user or ID — show user profile\n")
	sb.WriteString("/channels — list tracked channels\n")
	sb.WriteString("/users &lt;channel_id&gt; — top users in channel\n")
	sb.WriteString("/context [channel_id] — show channel context for GPT\n")
	sb.WriteString("/checkprofile @user or ID — run profile analysis through rspamd\n")

	sb.WriteString("\n<b>Rule management:</b>\n")
	sb.WriteString("/addregexp &lt;pattern&gt; — add regexp spam rule\n")
	sb.WriteString("/delregexp &lt;pattern&gt; — remove regexp rule\n")
	sb.WriteString("/listregexp — list regexp rules\n")
	sb.WriteString("/addurl &lt;url&gt; — add spam URL\n")
	sb.WriteString("/delurl &lt;url&gt; — remove URL\n")
	sb.WriteString("/listurls — list URL rules\n")

	tb.sendHTML(ctx, b, msg.Chat.ID, sb.String(), msg.ID)
}
