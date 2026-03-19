package userpic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-telegram/bot"
)

// Analysis holds the result of userpic analysis.
type Analysis struct {
	HasPhoto        bool    `json:"has_photo"`
	HasFace         bool    `json:"has_face"`
	FaceType        string  `json:"face_type"`         // "male", "female", "unclear", "none"
	IsStock         bool    `json:"is_stock"`           // looks like stock photo
	HasText         bool    `json:"has_text"`           // text/watermark on image
	HasLogo         bool    `json:"has_logo"`           // logo or QR code
	IsExplicit      bool    `json:"is_explicit"`        // explicit/provocative content
	RiskScore       float64 `json:"risk_score"`         // 0.0 = safe, 1.0 = likely spam
	Reason          string  `json:"reason"`
	AnalyzedAt      int64   `json:"analyzed_at"`
}

// Analyzer performs userpic analysis via vision LLM.
type Analyzer struct {
	apiKey     string
	model      string
	apiURL     string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewAnalyzer creates a new userpic analyzer.
func NewAnalyzer(apiKey, model, apiURL string, logger *slog.Logger) *Analyzer {
	if apiURL == "" {
		apiURL = "https://openrouter.ai/api/v1/chat/completions"
	}
	if model == "" {
		model = "google/gemini-2.0-flash-001"
	}
	return &Analyzer{
		apiKey: apiKey,
		model:  model,
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// Enabled returns true if the analyzer has an API key configured.
func (a *Analyzer) Enabled() bool {
	return a.apiKey != ""
}

const visionPrompt = `Analyze this Telegram user profile picture for spam/scam indicators.

Evaluate:
1. Is there a human face? If yes, what type? (male/female/unclear)
2. Does it look like a stock photo or professional model photo? (vs real selfie/casual)
3. Is there text, watermarks, or overlaid graphics on the image?
4. Are there logos, QR codes, or promotional elements?
5. Is the image explicit or provocative (used to attract attention)?

Spam account indicators:
- Attractive female face + stock/professional photo = HIGH RISK
- Text overlays, promotional graphics = HIGH RISK
- QR codes, business logos = HIGH RISK
- Explicit/provocative images = MODERATE RISK
- Real casual photos, pets, landscapes, cartoons, anime = LOW RISK
- Default/no avatar = NEUTRAL (not indicative either way)

Respond in JSON:
{"has_face":bool,"face_type":"male|female|unclear|none","is_stock":bool,"has_text":bool,"has_logo":bool,"is_explicit":bool,"risk_score":0.0-1.0,"reason":"brief explanation"}`

// DownloadUserPhoto downloads the user's profile photo via Telegram Bot API.
// Returns the photo bytes or nil if no photo.
func DownloadUserPhoto(ctx context.Context, b *bot.Bot, userID int64) ([]byte, error) {
	photos, err := b.GetUserProfilePhotos(ctx, &bot.GetUserProfilePhotosParams{
		UserID: userID,
		Limit:  1,
	})
	if err != nil {
		return nil, fmt.Errorf("get profile photos: %w", err)
	}

	if photos.TotalCount == 0 || len(photos.Photos) == 0 || len(photos.Photos[0]) == 0 {
		return nil, nil // no photo
	}

	// Get the smallest size (sufficient for analysis, saves bandwidth)
	photo := photos.Photos[0][0]

	file, err := b.GetFile(ctx, &bot.GetFileParams{
		FileID: photo.FileID,
	})
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}

	// Download via Bot API file URL
	fileURL := b.FileDownloadLink(file)
	resp, err := http.Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return data, nil
}

// Analyze sends a profile photo to a vision LLM and returns the analysis.
func (a *Analyzer) Analyze(ctx context.Context, photoData []byte) (*Analysis, error) {
	if len(photoData) == 0 {
		return &Analysis{
			HasPhoto:   false,
			RiskScore:  0.0,
			Reason:     "no profile photo",
			AnalyzedAt: time.Now().Unix(),
		}, nil
	}

	b64 := base64.StdEncoding.EncodeToString(photoData)
	imageURL := fmt.Sprintf("data:image/jpeg;base64,%s", b64)

	reqBody := map[string]interface{}{
		"model": a.model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": visionPrompt,
					},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": imageURL,
						},
					},
				},
			},
		},
		"max_tokens": 300,
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse OpenAI-compatible response
	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("empty response from API")
	}

	content := apiResp.Choices[0].Message.Content

	// Extract JSON from response (may have markdown wrapping)
	analysis := &Analysis{
		HasPhoto:   true,
		AnalyzedAt: time.Now().Unix(),
	}

	jsonStr := extractJSON(content)
	if err := json.Unmarshal([]byte(jsonStr), analysis); err != nil {
		a.logger.Warn("failed to parse vision response as JSON, using raw",
			"content", content,
			"error", err,
		)
		analysis.Reason = content
		analysis.RiskScore = 0.5
	}
	analysis.HasPhoto = true
	analysis.AnalyzedAt = time.Now().Unix()

	return analysis, nil
}

// extractJSON finds JSON object in a string (handles markdown code blocks).
func extractJSON(s string) string {
	// Try to find ```json ... ``` block
	start := 0
	if idx := bytes.Index([]byte(s), []byte("```json")); idx >= 0 {
		start = idx + 7
		if end := bytes.Index([]byte(s[start:]), []byte("```")); end >= 0 {
			return s[start : start+end]
		}
	}
	if idx := bytes.Index([]byte(s), []byte("```")); idx >= 0 {
		start = idx + 3
		if end := bytes.Index([]byte(s[start:]), []byte("```")); end >= 0 {
			return s[start : start+end]
		}
	}
	// Try to find raw { ... }
	if idx := bytes.IndexByte([]byte(s), '{'); idx >= 0 {
		if end := bytes.LastIndexByte([]byte(s), '}'); end > idx {
			return s[idx : end+1]
		}
	}
	return s
}
