package quiz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLM handles question generation and answer evaluation.
type LLM struct {
	apiKey     string
	model      string
	apiURL     string
	httpClient *http.Client
}

// NewLLM creates a new LLM client for quiz operations.
func NewLLM(apiKey, model, apiURL string) *LLM {
	if apiURL == "" {
		apiURL = "https://openrouter.ai/api/v1/chat/completions"
	}
	if model == "" {
		model = "google/gemini-2.0-flash-001"
	}
	return &LLM{
		apiKey: apiKey,
		model:  model,
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GenerateQuestion generates a quiz question using the channel prompt.
func (l *LLM) GenerateQuestion(ctx context.Context, channelPrompt string) (string, error) {
	messages := []map[string]string{
		{
			"role": "system",
			"content": "You generate a single short quiz question for a Telegram channel. " +
				"The question should be easy for a real member but hard for a spam bot. " +
				"Reply with ONLY the question text, nothing else. " +
				"The question should be in the same language as the channel prompt.",
		},
		{
			"role":    "user",
			"content": channelPrompt,
		},
	}

	reply, err := l.chat(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("generate question: %w", err)
	}

	return strings.TrimSpace(reply), nil
}

// EvaluateAnswer evaluates a user's answer to a quiz question.
// Returns "pass", "fail", and a reason.
func (l *LLM) EvaluateAnswer(ctx context.Context, question, answer, channelPrompt string) (string, string, error) {
	messages := []map[string]string{
		{
			"role": "system",
			"content": "You evaluate answers to a quiz question in a Telegram channel. " +
				"The quiz is designed to verify that the user is a real person interested in the channel topic, not a spam bot. " +
				"Be lenient — the answer doesn't need to be perfectly correct, just show basic knowledge or genuine interest. " +
				"Reply with exactly two lines:\n" +
				"Line 1: PASS or FAIL\n" +
				"Line 2: Brief reason",
		},
		{
			"role": "user",
			"content": fmt.Sprintf("Channel context: %s\n\nQuestion: %s\n\nUser's answer: %s",
				channelPrompt, question, answer),
		},
	}

	reply, err := l.chat(ctx, messages)
	if err != nil {
		return "", "", fmt.Errorf("evaluate answer: %w", err)
	}

	lines := strings.SplitN(strings.TrimSpace(reply), "\n", 2)
	verdict := strings.ToLower(strings.TrimSpace(lines[0]))
	reason := ""
	if len(lines) > 1 {
		reason = strings.TrimSpace(lines[1])
	}

	if strings.Contains(verdict, "pass") {
		return "pass", reason, nil
	}
	return "fail", reason, nil
}

func (l *LLM) chat(ctx context.Context, messages []map[string]string) (string, error) {
	reqBody := map[string]interface{}{
		"model":      l.model,
		"messages":   messages,
		"max_tokens": 300,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.apiURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.apiKey)

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}

	return apiResp.Choices[0].Message.Content, nil
}
