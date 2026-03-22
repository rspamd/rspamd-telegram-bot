package storage

import (
	"context"
	"sync"
	"time"
)

const (
	bufferFlushInterval = 5 * time.Second
	bufferMaxSize       = 100
)

type bufferedEvent struct {
	EventType string
	ChatID    int64
	UserID    int64
	Username  string
	FirstName string
	Detail    string
	Timestamp time.Time
}

type bufferedMessage struct {
	Msg *Message
}

// Buffer accumulates inserts and flushes them in batches.
type Buffer struct {
	client   *Client
	events   []bufferedEvent
	messages []bufferedMessage
	mu       sync.Mutex
	stopCh   chan struct{}
}

// NewBuffer creates a new insert buffer and starts the flush timer.
func NewBuffer(client *Client) *Buffer {
	b := &Buffer{
		client: client,
		stopCh: make(chan struct{}),
	}
	go b.flushLoop()
	return b
}

// AddEvent queues an event for batch insert.
func (b *Buffer) AddEvent(eventType string, chatID, userID int64, username, firstName, detail string) {
	b.mu.Lock()
	b.events = append(b.events, bufferedEvent{
		EventType: eventType,
		ChatID:    chatID,
		UserID:    userID,
		Username:  username,
		FirstName: firstName,
		Detail:    detail,
		Timestamp: time.Now(),
	})
	shouldFlush := len(b.events) >= bufferMaxSize
	b.mu.Unlock()

	if shouldFlush {
		b.Flush()
	}
}

// AddMessage queues a message for batch insert.
func (b *Buffer) AddMessage(msg *Message) {
	b.mu.Lock()
	b.messages = append(b.messages, bufferedMessage{Msg: msg})
	shouldFlush := len(b.messages) >= bufferMaxSize
	b.mu.Unlock()

	if shouldFlush {
		b.Flush()
	}
}

// Flush writes all pending data to ClickHouse.
func (b *Buffer) Flush() {
	b.mu.Lock()
	events := b.events
	messages := b.messages
	b.events = nil
	b.messages = nil
	b.mu.Unlock()

	ctx := context.Background()

	if len(events) > 0 {
		batch, err := b.client.conn.PrepareBatch(ctx, `
			INSERT INTO telegram_bot.bot_events
			(event_type, chat_id, user_id, username, first_name, detail, timestamp)`)
		if err != nil {
			b.client.logger.Error("prepare event batch failed", "error", err)
		} else {
			for _, e := range events {
				batch.Append(e.EventType, e.ChatID, e.UserID, e.Username, e.FirstName, e.Detail, e.Timestamp)
			}
			if err := batch.Send(); err != nil {
				b.client.logger.Error("send event batch failed", "error", err, "count", len(events))
			} else {
				b.client.logger.Debug("flushed events", "count", len(events))
			}
		}
	}

	if len(messages) > 0 {
		batch, err := b.client.conn.PrepareBatch(ctx, `
			INSERT INTO telegram_bot.messages
			(message_id, chat_id, user_id, username, first_name, last_name,
			 text, message_type, has_media, reply_to_message_id, forward_from_id,
			 timestamp, rspamd_score, rspamd_action, is_spam)`)
		if err != nil {
			b.client.logger.Error("prepare message batch failed", "error", err)
		} else {
			for _, m := range messages {
				msg := m.Msg
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
				batch.Append(
					uint64(msg.MessageID), msg.ChatID, msg.UserID,
					msg.Username, msg.FirstName, msg.LastName,
					msg.Text, msg.MessageType, hasMedia,
					replyTo, forwardFrom,
					msg.Timestamp, msg.RspamdScore, msg.RspamdAction, isSpam,
				)
			}
			if err := batch.Send(); err != nil {
				b.client.logger.Error("send message batch failed", "error", err, "count", len(messages))
			} else {
				b.client.logger.Debug("flushed messages", "count", len(messages))
			}
		}
	}
}

// Stop flushes remaining data and stops the buffer.
func (b *Buffer) Stop() {
	close(b.stopCh)
	b.Flush()
}

func (b *Buffer) flushLoop() {
	ticker := time.NewTicker(bufferFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.Flush()
		case <-b.stopCh:
			return
		}
	}
}
