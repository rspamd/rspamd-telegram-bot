package quiz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	sessionTTL = 24 * time.Hour
	configTTL  = 0 // no expiry for quiz config
)

// Session represents an active quiz session.
type Session struct {
	Token     string `json:"token"`
	UserID    int64  `json:"user_id"`
	ChannelID int64  `json:"channel_id"`
	Question  string `json:"question"`
	Status    string `json:"status"` // "pending", "answered", "passed", "failed"
	Answer    string `json:"answer,omitempty"`
	Result    string `json:"result,omitempty"` // LLM evaluation
	CreatedAt int64  `json:"created_at"`
	MessageID int    `json:"message_id,omitempty"` // channel message to delete later
}

// Config holds per-channel quiz configuration.
type Config struct {
	Prompt  string `json:"prompt"`  // LLM prompt for generating questions
	Message string `json:"message"` // Template shown in channel ({question} and {link} placeholders)
	Mode    string `json:"mode"`    // "suspicious" (default) or "all" (quiz on every join)
}

// Manager handles quiz operations.
type Manager struct {
	redis  *redis.Client
	logger *slog.Logger
}

// NewManager creates a quiz manager.
func NewManager(redisClient *redis.Client, logger *slog.Logger) *Manager {
	return &Manager{
		redis:  redisClient,
		logger: logger,
	}
}

// SetPrompt sets the LLM prompt for a channel.
func (m *Manager) SetPrompt(ctx context.Context, channelID int64, prompt string) error {
	key := fmt.Sprintf("tg_quiz:%d:prompt", channelID)
	return m.redis.Set(ctx, key, prompt, configTTL).Err()
}

// SetMessage sets the channel message template.
func (m *Manager) SetMessage(ctx context.Context, channelID int64, message string) error {
	key := fmt.Sprintf("tg_quiz:%d:message", channelID)
	return m.redis.Set(ctx, key, message, configTTL).Err()
}

// SetMode sets the quiz mode: "suspicious" or "all".
func (m *Manager) SetMode(ctx context.Context, channelID int64, mode string) error {
	if mode != "suspicious" && mode != "all" {
		return fmt.Errorf("invalid mode: %s (use 'suspicious' or 'all')", mode)
	}
	key := fmt.Sprintf("tg_quiz:%d:mode", channelID)
	return m.redis.Set(ctx, key, mode, configTTL).Err()
}

// GetConfig returns quiz config for a channel.
func (m *Manager) GetConfig(ctx context.Context, channelID int64) (*Config, error) {
	promptKey := fmt.Sprintf("tg_quiz:%d:prompt", channelID)
	messageKey := fmt.Sprintf("tg_quiz:%d:message", channelID)

	prompt, err := m.redis.Get(ctx, promptKey).Result()
	if err != nil {
		return nil, fmt.Errorf("no quiz prompt configured")
	}

	message, err := m.redis.Get(ctx, messageKey).Result()
	if err != nil {
		return nil, fmt.Errorf("no quiz message configured")
	}

	modeKey := fmt.Sprintf("tg_quiz:%d:mode", channelID)
	mode, _ := m.redis.Get(ctx, modeKey).Result()
	if mode == "" {
		mode = "suspicious"
	}

	return &Config{Prompt: prompt, Message: message, Mode: mode}, nil
}

// IsConfigured returns true if both prompt and message are set for a channel.
func (m *Manager) IsConfigured(ctx context.Context, channelID int64) bool {
	cfg, err := m.GetConfig(ctx, channelID)
	return err == nil && cfg.Prompt != "" && cfg.Message != ""
}

// CreateSession creates a new quiz session and returns the token.
// Question can be empty — it will be generated lazily when user clicks the link.
func (m *Manager) CreateSession(ctx context.Context, userID, channelID int64, question string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	session := &Session{
		Token:     token,
		UserID:    userID,
		ChannelID: channelID,
		Question:  question,
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}

	key := fmt.Sprintf("tg_quiz_session:%s", token)
	if err := m.redis.Set(ctx, key, data, sessionTTL).Err(); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}

	// Also store reverse lookup: user -> active session
	userKey := fmt.Sprintf("tg_quiz_active:%d:%d", channelID, userID)
	m.redis.Set(ctx, userKey, token, sessionTTL)

	m.logger.Info("quiz session created",
		"token", token,
		"user_id", userID,
		"channel_id", channelID,
	)

	return token, nil
}

// GetSession retrieves a quiz session by token.
func (m *Manager) GetSession(ctx context.Context, token string) (*Session, error) {
	key := fmt.Sprintf("tg_quiz_session:%s", token)
	data, err := m.redis.Get(ctx, key).Bytes()
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}

	return &session, nil
}

// UpdateSession saves an updated session.
func (m *Manager) UpdateSession(ctx context.Context, session *Session) error {
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	key := fmt.Sprintf("tg_quiz_session:%s", session.Token)
	return m.redis.Set(ctx, key, data, sessionTTL).Err()
}

// HasActiveQuiz checks if a user has a pending quiz for a channel.
func (m *Manager) HasActiveQuiz(ctx context.Context, channelID, userID int64) bool {
	userKey := fmt.Sprintf("tg_quiz_active:%d:%d", channelID, userID)
	token, err := m.redis.Get(ctx, userKey).Result()
	if err != nil {
		return false
	}

	session, err := m.GetSession(ctx, token)
	if err != nil {
		return false
	}

	return session.Status == "pending"
}

// SetChannelMessageID stores the channel message ID for later deletion.
func (m *Manager) SetChannelMessageID(ctx context.Context, token string, messageID int) error {
	session, err := m.GetSession(ctx, token)
	if err != nil {
		return err
	}
	session.MessageID = messageID
	return m.UpdateSession(ctx, session)
}

// TrackQuestion stores a question in the recent questions list for a channel.
func (m *Manager) TrackQuestion(ctx context.Context, channelID int64, question string) {
	key := fmt.Sprintf("tg_quiz:%d:recent_questions", channelID)
	m.redis.LPush(ctx, key, question)
	m.redis.LTrim(ctx, key, 0, 19) // keep last 20
	m.redis.Expire(ctx, key, 7*24*time.Hour)
}

// RecentQuestions returns the last N questions asked in a channel.
func (m *Manager) RecentQuestions(ctx context.Context, channelID int64) []string {
	key := fmt.Sprintf("tg_quiz:%d:recent_questions", channelID)
	questions, _ := m.redis.LRange(ctx, key, 0, 19).Result()
	return questions
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
