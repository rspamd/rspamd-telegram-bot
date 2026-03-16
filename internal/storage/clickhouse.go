package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Message represents a message record stored in ClickHouse.
type Message struct {
	MessageID        int64
	ChatID           int64
	UserID           int64
	Username         string
	FirstName        string
	LastName         string
	Text             string
	MessageType      string
	HasMedia         bool
	ReplyToMessageID int64
	ForwardFromID    int64
	Timestamp        time.Time
	RspamdScore      float32
	RspamdAction     string
	IsSpam           bool
}

// Client is a ClickHouse storage client.
type Client struct {
	conn   driver.Conn
	logger *slog.Logger
}

// NewClient creates a new ClickHouse client and verifies connectivity.
func NewClient(ctx context.Context, dsn string, logger *slog.Logger) (*Client, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse DSN: %w", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open clickhouse connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}

	logger.Info("connected to ClickHouse")

	return &Client{
		conn:   conn,
		logger: logger,
	}, nil
}

// Store inserts a message record into ClickHouse.
func (c *Client) Store(ctx context.Context, msg *Message) error {
	var replyTo *uint64
	if msg.ReplyToMessageID != 0 {
		v := uint64(msg.ReplyToMessageID)
		replyTo = &v
	}

	var forwardFrom *int64
	if msg.ForwardFromID != 0 {
		forwardFrom = &msg.ForwardFromID
	}

	var hasMedia uint8
	if msg.HasMedia {
		hasMedia = 1
	}

	var isSpam uint8
	if msg.IsSpam {
		isSpam = 1
	}

	err := c.conn.Exec(ctx, `
		INSERT INTO telegram_bot.messages (
			message_id, chat_id, user_id, username, first_name, last_name,
			text, message_type, has_media, reply_to_message_id, forward_from_id,
			timestamp, rspamd_score, rspamd_action, is_spam
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uint64(msg.MessageID),
		msg.ChatID,
		msg.UserID,
		msg.Username,
		msg.FirstName,
		msg.LastName,
		msg.Text,
		msg.MessageType,
		hasMedia,
		replyTo,
		forwardFrom,
		msg.Timestamp,
		msg.RspamdScore,
		msg.RspamdAction,
		isSpam,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}

	c.logger.Debug("stored message",
		"message_id", msg.MessageID,
		"chat_id", msg.ChatID,
	)

	return nil
}

// Close closes the ClickHouse connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
