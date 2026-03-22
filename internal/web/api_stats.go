package web

import (
	"net/http"
	"strconv"
)

func (s *Server) handleStatsOverview(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(r.URL.Query().Get("chat_id"), 10, 64)
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "day"
	}

	stats, err := s.storage.StatsOverview(r.Context(), chatID, period)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, stats)
}

func (s *Server) handleTopTalkers(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(r.URL.Query().Get("chat_id"), 10, 64)
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "day"
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	talkers, err := s.storage.TopTalkers(r.Context(), chatID, period, limit)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, talkers)
}

func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.redis.ZRevRangeWithScores(r.Context(), "tg_channels", 0, 49).Result()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type channelInfo struct {
		ChatID       string `json:"chat_id"`
		Title        string `json:"title"`
		MsgCount     string `json:"msg_count"`
		UserCount    int64  `json:"user_count"`
		LastActivity int64  `json:"last_activity"`
	}

	var result []channelInfo
	for _, ch := range channels {
		chatID := ch.Member.(string)
		infoKey := "tg_channel:" + chatID + ":info"
		info, _ := s.redis.HGetAll(r.Context(), infoKey).Result()

		usersKey := "tg_channel:" + chatID + ":users"
		userCount, _ := s.redis.ZCard(r.Context(), usersKey).Result()

		result = append(result, channelInfo{
			ChatID:       chatID,
			Title:        info["title"],
			MsgCount:     info["msg_count"],
			UserCount:    userCount,
			LastActivity: int64(ch.Score),
		})
	}

	writeJSON(w, result)
}
