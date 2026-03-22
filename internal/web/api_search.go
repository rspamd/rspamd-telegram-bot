package web

import (
	"encoding/json"
	"net/http"
	"time"
)

type searchRequest struct {
	Query  string `json:"query"`
	ChatID int64  `json:"chat_id,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		writeError(w, "query is required", http.StatusBadRequest)
		return
	}

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 20
	}

	results, err := s.storage.FuzzySearch(r.Context(), req.Query, req.ChatID, req.Limit)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Fetch context for top results (first 5)
	type resultWithContext struct {
		Match   interface{}   `json:"match"`
		Context []interface{} `json:"context,omitempty"`
	}

	var enriched []resultWithContext
	for i, res := range results {
		entry := resultWithContext{Match: res}
		if i < 5 && res.Timestamp > 0 {
			ctx, _ := s.storage.MessageContext(r.Context(), res.ChatID,
				time.Unix(int64(res.Timestamp), 0), 5)
			if len(ctx) > 0 {
				contextList := make([]interface{}, len(ctx))
				for j, c := range ctx {
					contextList[j] = c
				}
				entry.Context = contextList
			}
		}
		enriched = append(enriched, entry)
	}

	writeJSON(w, enriched)
}
