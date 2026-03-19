package rspamd

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
	"time"
)

// TelegramMessage holds the Telegram message data needed to build an RFC 2822 message.
type TelegramMessage struct {
	MessageID   int64
	ChatID      int64
	ChatTitle   string
	UserID      int64
	Username    string
	FirstName   string
	LastName    string
	IsBot       bool
	IsPremium   bool
	Text        string
	MessageType string // text, photo, document, sticker, join, forward
	HasMedia    bool
	MediaName   string
	MediaData   []byte
	ReplyToID      int64
	ReplyToUserID  int64
	ForwardFrom    int64
	IsForward      bool
	IsAdmin        bool
	Readonly       bool
	UserpicRisk    float64 // -1 = not analyzed, 0.0-1.0 = risk score
	Timestamp      time.Time
}

// BuildMessage constructs an RFC 2822 MIME message from a Telegram message.
func BuildMessage(msg *TelegramMessage) ([]byte, error) {
	var buf bytes.Buffer

	fromName := msg.FirstName
	if msg.LastName != "" {
		fromName += " " + msg.LastName
	}
	fromAddr := fmt.Sprintf("user_%d@telegram.local", msg.UserID)
	chatAddr := fmt.Sprintf("chat_%d@telegram.local", msg.ChatID)

	// Write standard headers
	writeHeader(&buf, "From", fmt.Sprintf("%q <%s>", fromName, fromAddr))
	writeHeader(&buf, "To", fmt.Sprintf("%q <%s>", msg.ChatTitle, chatAddr))

	subject := buildSubject(msg)
	writeHeader(&buf, "Subject", subject)

	writeHeader(&buf, "Date", msg.Timestamp.UTC().Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", fmt.Sprintf("<msg_%d.chat_%d@telegram>", msg.MessageID, msg.ChatID))

	if msg.ReplyToID != 0 {
		writeHeader(&buf, "In-Reply-To", fmt.Sprintf("<msg_%d.chat_%d@telegram>", msg.ReplyToID, msg.ChatID))
	}

	// Telegram-specific headers
	writeHeader(&buf, "X-Telegram-User-Id", fmt.Sprintf("%d", msg.UserID))
	if msg.Username != "" {
		writeHeader(&buf, "X-Telegram-Username", msg.Username)
	}
	writeHeader(&buf, "X-Telegram-First-Name", msg.FirstName)
	if msg.LastName != "" {
		writeHeader(&buf, "X-Telegram-Last-Name", msg.LastName)
	}
	writeHeader(&buf, "X-Telegram-Is-Bot", fmt.Sprintf("%t", msg.IsBot))
	writeHeader(&buf, "X-Telegram-Is-Premium", fmt.Sprintf("%t", msg.IsPremium))
	writeHeader(&buf, "X-Telegram-Chat-Id", fmt.Sprintf("%d", msg.ChatID))
	writeHeader(&buf, "X-Telegram-Chat-Title", msg.ChatTitle)
	writeHeader(&buf, "X-Telegram-Message-Type", msg.MessageType)
	writeHeader(&buf, "X-Telegram-Has-Media", fmt.Sprintf("%t", msg.HasMedia))

	if msg.ForwardFrom != 0 {
		writeHeader(&buf, "X-Telegram-Forward-From", fmt.Sprintf("%d", msg.ForwardFrom))
	}
	writeHeader(&buf, "X-Telegram-Is-Forward", fmt.Sprintf("%t", msg.IsForward))
	writeHeader(&buf, "X-Telegram-Is-Admin", fmt.Sprintf("%t", msg.IsAdmin))
	if msg.UserpicRisk >= 0 {
		writeHeader(&buf, "X-Telegram-Userpic-Risk", fmt.Sprintf("%.2f", msg.UserpicRisk))
	}
	if msg.ReplyToUserID != 0 {
		writeHeader(&buf, "X-Telegram-Reply-To-User-Id", fmt.Sprintf("%d", msg.ReplyToUserID))
	}
	writeHeader(&buf, "MIME-Version", "1.0")

	// Build body
	if msg.HasMedia && len(msg.MediaData) > 0 {
		if err := buildMultipartBody(&buf, msg); err != nil {
			return nil, fmt.Errorf("build multipart body: %w", err)
		}
	} else {
		writeHeader(&buf, "Content-Type", "text/html; charset=utf-8")
		buf.WriteString("\r\n")
		buf.WriteString(msg.Text)
		buf.WriteString("\r\n")
	}

	return buf.Bytes(), nil
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

func buildSubject(msg *TelegramMessage) string {
	switch msg.MessageType {
	case "join":
		return "[New Member]"
	case "photo", "document", "sticker":
		if msg.Text != "" {
			return truncate(msg.Text, 78)
		}
		return "[Media]"
	default:
		if msg.Text != "" {
			return truncate(msg.Text, 78)
		}
		return "[Empty]"
	}
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func buildMultipartBody(buf *bytes.Buffer, msg *TelegramMessage) error {
	mpw := multipart.NewWriter(buf)
	writeHeader(buf, "Content-Type", fmt.Sprintf("multipart/mixed; boundary=%s", mpw.Boundary()))
	buf.WriteString("\r\n")

	// Text part
	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", "text/html; charset=utf-8")
	textPart, err := mpw.CreatePart(textHeader)
	if err != nil {
		return err
	}
	textPart.Write([]byte(msg.Text))

	// Media part
	if len(msg.MediaData) > 0 {
		mediaHeader := make(textproto.MIMEHeader)
		mediaName := msg.MediaName
		if mediaName == "" {
			mediaName = "attachment"
		}
		mediaHeader.Set("Content-Type", "application/octet-stream")
		mediaHeader.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", mediaName))
		mediaPart, err := mpw.CreatePart(mediaHeader)
		if err != nil {
			return err
		}
		mediaPart.Write(msg.MediaData)
	}

	return mpw.Close()
}
