package storage

import (
	"context"
	"fmt"
	"time"
)

// OverviewStats holds aggregate stats for a channel/period.
type OverviewStats struct {
	TotalMessages uint64 `json:"total_messages"`
	SpamCount     uint64 `json:"spam_count"`
	MediaCount    uint64 `json:"media_count"`
	UsersJoined   uint64 `json:"users_joined"`
	UsersLeft     uint64 `json:"users_left"`
	UniqueUsers   uint64 `json:"unique_users"`
}

// TalkerStats holds per-user message stats.
type TalkerStats struct {
	UserID        int64  `json:"user_id"`
	Username      string `json:"username"`
	FirstName     string `json:"first_name"`
	MsgCount      uint64 `json:"msg_count"`
	SampleMessage string `json:"sample_message"`
}

// SearchResult holds a message matching a search query.
type SearchResult struct {
	MessageID uint64  `json:"message_id"`
	ChatID    int64   `json:"chat_id"`
	UserID    int64   `json:"user_id"`
	Username  string  `json:"username"`
	FirstName string  `json:"first_name"`
	Text      string  `json:"text"`
	Timestamp uint32  `json:"timestamp"`
	Score     float32 `json:"rspamd_score"`
	IsSpam    bool    `json:"is_spam"`
	Distance  float32 `json:"distance,omitempty"`
}

// TableStats holds ClickHouse table statistics.
type TableStats struct {
	TotalRows     uint64 `json:"total_rows"`
	DiskBytes     uint64 `json:"disk_bytes"`
	OldestMessage uint32 `json:"oldest_message"`
	NewestMessage uint32 `json:"newest_message"`
}

// TimelineBucket holds message count for a time bucket.
type TimelineBucket struct {
	Bucket   string `json:"bucket"`
	Total    uint64 `json:"total"`
	Spam     uint64 `json:"spam"`
}

func periodToInterval(period string) string {
	switch period {
	case "day":
		return "1 DAY"
	case "week":
		return "7 DAY"
	case "month":
		return "30 DAY"
	default:
		return "1 DAY"
	}
}

// StatsOverview returns aggregate stats for a channel and time period.
func (c *Client) StatsOverview(ctx context.Context, chatID int64, period string) (*OverviewStats, error) {
	interval := periodToInterval(period)
	chatFilter := ""
	args := []interface{}{}
	if chatID != 0 {
		chatFilter = "AND chat_id = ?"
		args = append(args, chatID)
	}

	query := fmt.Sprintf(`
		SELECT
			count() AS total,
			countIf(is_spam = 1) AS spam,
			countIf(has_media = 1) AS media,
			countIf(message_type = 'join') AS joined,
			uniqExact(user_id) AS unique_users
		FROM telegram_bot.messages
		WHERE timestamp >= now() - INTERVAL %s %s
	`, interval, chatFilter)

	var stats OverviewStats
	row := c.conn.QueryRow(ctx, query, args...)
	if err := row.Scan(&stats.TotalMessages, &stats.SpamCount, &stats.MediaCount,
		&stats.UsersJoined, &stats.UniqueUsers); err != nil {
		return nil, fmt.Errorf("stats overview: %w", err)
	}

	return &stats, nil
}

// TopTalkers returns the most active users in a channel/period.
func (c *Client) TopTalkers(ctx context.Context, chatID int64, period string, limit int) ([]TalkerStats, error) {
	interval := periodToInterval(period)
	chatFilter := ""
	args := []interface{}{}
	if chatID != 0 {
		chatFilter = "AND chat_id = ?"
		args = append(args, chatID)
	}

	query := fmt.Sprintf(`
		SELECT
			user_id,
			any(username) AS username,
			any(first_name) AS first_name,
			count() AS msg_count,
			any(text) AS sample_message
		FROM telegram_bot.messages
		WHERE timestamp >= now() - INTERVAL %s
			AND message_type != 'join'
			%s
		GROUP BY user_id
		ORDER BY msg_count DESC
		LIMIT ?
	`, interval, chatFilter)

	args = append(args, limit)
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("top talkers: %w", err)
	}
	defer rows.Close()

	var result []TalkerStats
	for rows.Next() {
		var t TalkerStats
		if err := rows.Scan(&t.UserID, &t.Username, &t.FirstName, &t.MsgCount, &t.SampleMessage); err != nil {
			return nil, err
		}
		result = append(result, t)
	}

	return result, nil
}

// FuzzySearch finds messages similar to the query using ClickHouse ngramDistance.
func (c *Client) FuzzySearch(ctx context.Context, query string, chatID int64, limit int) ([]SearchResult, error) {
	chatFilter := ""
	args := []interface{}{query}
	if chatID != 0 {
		chatFilter = "AND chat_id = ?"
		args = append(args, chatID)
	}

	sql := fmt.Sprintf(`
		SELECT
			message_id, chat_id, user_id, username, first_name,
			text, toUnixTimestamp(timestamp), rspamd_score, is_spam,
			ngramDistance(text, ?) AS dist
		FROM telegram_bot.messages
		WHERE length(text) > 0
			%s
		ORDER BY dist ASC
		LIMIT ?
	`, chatFilter)

	args = append(args, limit)
	rows, err := c.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("fuzzy search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var isSpam uint8
		if err := rows.Scan(&r.MessageID, &r.ChatID, &r.UserID, &r.Username, &r.FirstName,
			&r.Text, &r.Timestamp, &r.Score, &isSpam, &r.Distance); err != nil {
			return nil, err
		}
		r.IsSpam = isSpam == 1
		results = append(results, r)
	}

	return results, nil
}

// MessageContext returns messages around a given timestamp in a chat.
func (c *Client) MessageContext(ctx context.Context, chatID int64, ts time.Time, windowMinutes int) ([]SearchResult, error) {
	rows, err := c.conn.Query(ctx, `
		SELECT
			message_id, chat_id, user_id, username, first_name,
			text, toUnixTimestamp(timestamp), rspamd_score, is_spam
		FROM telegram_bot.messages
		WHERE chat_id = ?
			AND timestamp BETWEEN ? - INTERVAL ? MINUTE AND ? + INTERVAL ? MINUTE
		ORDER BY timestamp ASC
		LIMIT 50
	`, chatID, ts, windowMinutes, ts, windowMinutes)
	if err != nil {
		return nil, fmt.Errorf("message context: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var isSpam uint8
		if err := rows.Scan(&r.MessageID, &r.ChatID, &r.UserID, &r.Username, &r.FirstName,
			&r.Text, &r.Timestamp, &r.Score, &isSpam); err != nil {
			return nil, err
		}
		r.IsSpam = isSpam == 1
		results = append(results, r)
	}

	return results, nil
}

// MessageTimeline returns message counts bucketed by time.
func (c *Client) MessageTimeline(ctx context.Context, chatID int64, period string) ([]TimelineBucket, error) {
	interval := periodToInterval(period)
	// Use hourly buckets for day, daily for week/month
	bucketExpr := "formatDateTime(timestamp, '%Y-%m-%d %H:00')"
	if period == "week" || period == "month" {
		bucketExpr = "formatDateTime(timestamp, '%Y-%m-%d')"
	}

	chatFilter := ""
	args := []interface{}{}
	if chatID != 0 {
		chatFilter = "AND chat_id = ?"
		args = append(args, chatID)
	}

	query := fmt.Sprintf(`
		SELECT
			%s AS bucket,
			count() AS total,
			countIf(is_spam = 1) AS spam
		FROM telegram_bot.messages
		WHERE timestamp >= now() - INTERVAL %s %s
		GROUP BY bucket
		ORDER BY bucket ASC
	`, bucketExpr, interval, chatFilter)

	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("message timeline: %w", err)
	}
	defer rows.Close()

	var result []TimelineBucket
	for rows.Next() {
		var b TimelineBucket
		if err := rows.Scan(&b.Bucket, &b.Total, &b.Spam); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}

// LengthBucket holds message count for a length range.
type LengthBucket struct {
	Range string `json:"range"`
	Count uint64 `json:"count"`
}

// MessageLengthDistribution returns message counts grouped by text length ranges.
func (c *Client) MessageLengthDistribution(ctx context.Context, chatID int64, period string) ([]LengthBucket, error) {
	interval := periodToInterval(period)
	chatFilter := ""
	args := []interface{}{}
	if chatID != 0 {
		chatFilter = "AND chat_id = ?"
		args = append(args, chatID)
	}

	query := fmt.Sprintf(`
		SELECT
			multiIf(
				length(text) = 0, 'empty',
				length(text) <= 10, '1-10',
				length(text) <= 50, '11-50',
				length(text) <= 100, '51-100',
				length(text) <= 200, '101-200',
				length(text) <= 500, '201-500',
				'500+'
			) AS len_range,
			count() AS cnt
		FROM telegram_bot.messages
		WHERE timestamp >= now() - INTERVAL %s
			AND message_type != 'join'
			%s
		GROUP BY len_range
		ORDER BY cnt DESC
	`, interval, chatFilter)

	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("length distribution: %w", err)
	}
	defer rows.Close()

	var result []LengthBucket
	for rows.Next() {
		var b LengthBucket
		if err := rows.Scan(&b.Range, &b.Count); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}

// GetTableStats returns ClickHouse table statistics.
func (c *Client) GetTableStats(ctx context.Context) (*TableStats, error) {
	var stats TableStats

	row := c.conn.QueryRow(ctx, `
		SELECT count(), toUnixTimestamp(min(timestamp)), toUnixTimestamp(max(timestamp))
		FROM telegram_bot.messages
	`)
	if err := row.Scan(&stats.TotalRows, &stats.OldestMessage, &stats.NewestMessage); err != nil {
		return nil, fmt.Errorf("table stats: %w", err)
	}

	row = c.conn.QueryRow(ctx, `
		SELECT sum(bytes_on_disk)
		FROM system.parts
		WHERE database = 'telegram_bot' AND active
	`)
	_ = row.Scan(&stats.DiskBytes)

	return &stats, nil
}
