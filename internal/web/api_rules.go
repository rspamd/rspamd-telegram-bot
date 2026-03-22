package web

import (
	"encoding/json"
	"net/http"
)

type ruleRequest struct {
	Pattern string `json:"pattern,omitempty"`
	URL     string `json:"url,omitempty"`
}

func (s *Server) handleListRegexp(w http.ResponseWriter, r *http.Request) {
	patterns, err := s.maps.ListPatterns()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, patterns)
}

func (s *Server) handleAddRegexp(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pattern == "" {
		writeError(w, "pattern is required", http.StatusBadRequest)
		return
	}

	if err := s.maps.AddPattern(req.Pattern); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"status": "ok", "pattern": req.Pattern})
}

func (s *Server) handleDeleteRegexp(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pattern == "" {
		writeError(w, "pattern is required", http.StatusBadRequest)
		return
	}

	if err := s.maps.RemovePattern(req.Pattern); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleListURLs(w http.ResponseWriter, r *http.Request) {
	urls, err := s.maps.ListURLs()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, urls)
}

func (s *Server) handleAddURL(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, "url is required", http.StatusBadRequest)
		return
	}

	if err := s.maps.AddURL(req.URL); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"status": "ok", "url": req.URL})
}

func (s *Server) handleDeleteURL(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeError(w, "url is required", http.StatusBadRequest)
		return
	}

	if err := s.maps.RemoveURL(req.URL); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}
