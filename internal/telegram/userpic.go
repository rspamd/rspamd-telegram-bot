package telegram

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-telegram/bot/models"

	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
	"github.com/vstakhov/rspamd-telegram-bot/internal/userpic"
)

// analyzeUserpicIfNew checks Redis for cached analysis, otherwise downloads
// and analyzes the user's profile photo for NEW users only. Sets tgMsg.UserpicRisk.
func (tb *Bot) analyzeUserpicIfNew(ctx context.Context, user *models.User, tgMsg *rspamd.TelegramMessage) {
	profileKey := fmt.Sprintf("tg_profile:%d", user.ID)

	// Check if analysis already cached in profile
	cached, err := tb.redis.HGet(ctx, profileKey, "userpic_analysis").Result()
	if err == nil && cached != "" {
		var analysis userpic.Analysis
		if json.Unmarshal([]byte(cached), &analysis) == nil {
			tgMsg.UserpicRisk = analysis.RiskScore
			tb.logger.Info("userpic: using cached result",
				"user_id", user.ID,
				"risk_score", analysis.RiskScore,
				"reason", analysis.Reason,
			)
			return
		}
	}

	// Check if profile exists (has msg_count) — skip analysis for known users
	msgCount, _ := tb.redis.HGet(ctx, profileKey, "msg_count").Result()
	if msgCount != "" && msgCount != "0" {
		return // existing user, don't re-analyze
	}

	// New user — run analysis
	tb.doUserpicAnalysis(ctx, user.ID, tgMsg)
}

// analyzeUserpicForce always downloads and analyzes the userpic,
// regardless of whether the user is known. Used by /checkprofile and forward scan.
func (tb *Bot) analyzeUserpicForce(ctx context.Context, userID int64, tgMsg *rspamd.TelegramMessage) {
	profileKey := fmt.Sprintf("tg_profile:%d", userID)

	// Use cache if available
	cached, err := tb.redis.HGet(ctx, profileKey, "userpic_analysis").Result()
	if err == nil && cached != "" {
		var analysis userpic.Analysis
		if json.Unmarshal([]byte(cached), &analysis) == nil {
			tgMsg.UserpicRisk = analysis.RiskScore
			tb.logger.Info("userpic: using cached result",
				"user_id", userID,
				"risk_score", analysis.RiskScore,
				"reason", analysis.Reason,
			)
			return
		}
	}

	tb.doUserpicAnalysis(ctx, userID, tgMsg)
}

func (tb *Bot) doUserpicAnalysis(ctx context.Context, userID int64, tgMsg *rspamd.TelegramMessage) {
	tb.logger.Info("userpic: downloading photo", "user_id", userID)

	photoData, err := userpic.DownloadUserPhoto(ctx, tb.bot, userID)
	if err != nil {
		tb.logger.Warn("userpic: download failed", "user_id", userID, "error", err)
		return
	}

	if photoData == nil {
		tb.logger.Info("userpic: no photo set", "user_id", userID)
		tgMsg.UserpicRisk = 0
		return
	}

	tb.logger.Info("userpic: sending to vision API", "user_id", userID, "photo_size", len(photoData))

	analysis, err := tb.userpic.Analyze(ctx, photoData)
	if err != nil {
		tb.logger.Warn("userpic: vision analysis failed", "user_id", userID, "error", err)
		return
	}

	tgMsg.UserpicRisk = analysis.RiskScore

	// Cache in Redis profile
	profileKey := fmt.Sprintf("tg_profile:%d", userID)
	analysisJSON, _ := json.Marshal(analysis)
	tb.redis.HSet(ctx, profileKey, "userpic_analysis", string(analysisJSON))

	tb.logger.Info("userpic: analysis complete",
		"user_id", userID,
		"risk_score", analysis.RiskScore,
		"has_face", analysis.HasFace,
		"face_type", analysis.FaceType,
		"is_stock", analysis.IsStock,
		"has_text", analysis.HasText,
		"reason", analysis.Reason,
	)
}
