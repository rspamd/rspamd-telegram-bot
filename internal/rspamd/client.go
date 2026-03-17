package rspamd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// CheckResult represents the response from Rspamd /checkv2.
type CheckResult struct {
	Score         float64                 `json:"score"`
	RequiredScore float64                `json:"required_score"`
	Action        string                 `json:"action"`
	Symbols       map[string]SymbolResult `json:"symbols"`
}

// SymbolResult represents a single symbol in the Rspamd response.
type SymbolResult struct {
	Name        string   `json:"name"`
	Score       float64  `json:"score"`
	Description string   `json:"description"`
	Options     []string `json:"options,omitempty"`
}

// Client is an HTTP client for the Rspamd API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	password   string
	logger     *slog.Logger
}

// NewClient creates a new Rspamd HTTP client.
func NewClient(baseURL, password string, timeout time.Duration, logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		password:   password,
		logger:     logger,
	}
}

// Version returns the rspamd version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ping", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return resp.Header.Get("Server"), nil
}

// Check sends a message to Rspamd for scanning via /checkv2.
func (c *Client) Check(ctx context.Context, msg *TelegramMessage) (*CheckResult, error) {
	return c.CheckWithSettings(ctx, msg, "")
}

// CheckWithSettings sends a message to Rspamd with a custom Settings-ID.
func (c *Client) CheckWithSettings(ctx context.Context, msg *TelegramMessage, settingsID string) (*CheckResult, error) {
	mimeData, err := BuildMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("build message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/checkv2", bytes.NewReader(mimeData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(req, msg)
	if settingsID != "" {
		req.Header.Set("Settings-ID", settingsID)
	}
	if msg.Readonly {
		req.Header.Set("X-Telegram-Readonly", "true")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rspamd request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rspamd returned %d: %s", resp.StatusCode, string(body))
	}

	var result CheckResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	c.logger.Debug("rspamd check result",
		"message_id", msg.MessageID,
		"score", result.Score,
		"action", result.Action,
	)

	return &result, nil
}

// NeuralLearn sends a message through /checkv2 with ANN-Train header for neural training.
func (c *Client) NeuralLearn(ctx context.Context, msg *TelegramMessage, class string) error {
	mimeData, err := BuildMessage(msg)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/checkv2", bytes.NewReader(mimeData))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(req, msg)
	req.Header.Set("ANN-Train", class)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("rspamd request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rspamd returned %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Info("rspamd neural learn",
		"class", class,
		"message_id", msg.MessageID,
	)

	return nil
}

// LearnSpam trains the message as spam.
func (c *Client) LearnSpam(ctx context.Context, msg *TelegramMessage) error {
	return c.learn(ctx, "/learnspam", msg)
}

// LearnHam trains the message as ham.
func (c *Client) LearnHam(ctx context.Context, msg *TelegramMessage) error {
	return c.learn(ctx, "/learnham", msg)
}

// FuzzyAdd adds a fuzzy hash for the message (flag 1 = spam).
func (c *Client) FuzzyAdd(ctx context.Context, msg *TelegramMessage) error {
	return c.fuzzy(ctx, "/fuzzyadd", msg, 1)
}

// FuzzyDel deletes a fuzzy hash for the message.
func (c *Client) FuzzyDel(ctx context.Context, msg *TelegramMessage) error {
	return c.fuzzy(ctx, "/fuzzydel", msg, 1)
}

func (c *Client) learn(ctx context.Context, endpoint string, msg *TelegramMessage) error {
	mimeData, err := BuildMessage(msg)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(mimeData))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(req, msg)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("rspamd request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rspamd returned %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Info("rspamd learn",
		"endpoint", endpoint,
		"message_id", msg.MessageID,
	)

	return nil
}

func (c *Client) fuzzy(ctx context.Context, endpoint string, msg *TelegramMessage, flag int) error {
	mimeData, err := BuildMessage(msg)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(mimeData))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(req, msg)
	req.Header.Set("Flag", fmt.Sprintf("%d", flag))
	req.Header.Set("Weight", "10")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("rspamd request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rspamd returned %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Info("rspamd fuzzy",
		"endpoint", endpoint,
		"message_id", msg.MessageID,
		"flag", flag,
	)

	return nil
}

func (c *Client) setHeaders(req *http.Request, msg *TelegramMessage) {
	req.Header.Set("Password", c.password)
	req.Header.Set("Settings-ID", "telegram")
	req.Header.Set("Queue-Id", fmt.Sprintf("tg-%d", msg.MessageID))
	req.Header.Set("From", fmt.Sprintf("user_%d@telegram.local", msg.UserID))
	req.Header.Set("Rcpt", fmt.Sprintf("chat_%d@telegram.local", msg.ChatID))
}
