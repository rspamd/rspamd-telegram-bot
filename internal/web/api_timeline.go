package web

import (
	"net/http"
	"strconv"
)

func (s *Server) handleLengthDist(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(r.URL.Query().Get("chat_id"), 10, 64)
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "day"
	}

	data, err := s.storage.MessageLengthDistribution(r.Context(), chatID, period)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, data)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	chatID, _ := strconv.ParseInt(r.URL.Query().Get("chat_id"), 10, 64)
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "day"
	}

	data, err := s.storage.MessageTimeline(r.Context(), chatID, period)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, data)
}
