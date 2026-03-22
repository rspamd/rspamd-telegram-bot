package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/redis/go-redis/v9"

	"github.com/vstakhov/rspamd-telegram-bot/internal/config"
	"github.com/vstakhov/rspamd-telegram-bot/internal/maps"
	"github.com/vstakhov/rspamd-telegram-bot/internal/rspamd"
	"github.com/vstakhov/rspamd-telegram-bot/internal/storage"
)

// Server is the web UI HTTP server.
type Server struct {
	cfg     *config.Config
	storage *storage.Client
	redis   *redis.Client
	rspamd  *rspamd.Client
	maps    *maps.Manager
	logger  *slog.Logger
	mux     *http.ServeMux
}

// New creates a new web server.
func New(cfg *config.Config, store *storage.Client, redisClient *redis.Client, rspamdClient *rspamd.Client, mapsManager *maps.Manager, logger *slog.Logger) *Server {
	s := &Server{
		cfg:     cfg,
		storage: store,
		redis:   redisClient,
		rspamd:  rspamdClient,
		maps:    mapsManager,
		logger:  logger,
		mux:     http.NewServeMux(),
	}

	s.routes()
	return s
}

func (s *Server) routes() {
	// Serve static frontend (Next.js export)
	s.mux.Handle("/", http.FileServer(http.Dir("web/out")))

	// API routes
	s.mux.HandleFunc("GET /api/stats/overview", s.authMiddleware(s.handleStatsOverview))
	s.mux.HandleFunc("GET /api/stats/top-talkers", s.authMiddleware(s.handleTopTalkers))
	s.mux.HandleFunc("GET /api/stats/channels", s.authMiddleware(s.handleChannels))
	s.mux.HandleFunc("GET /api/stats/timeline", s.authMiddleware(s.handleTimeline))
	s.mux.HandleFunc("GET /api/stats/lengths", s.authMiddleware(s.handleLengthDist))
	s.mux.HandleFunc("GET /api/stats/user-messages", s.authMiddleware(s.handleUserMessages))
	s.mux.HandleFunc("GET /api/stats/actions", s.authMiddleware(s.handleEventStats))

	s.mux.HandleFunc("POST /api/search", s.authMiddleware(s.handleSearch))

	s.mux.HandleFunc("GET /api/rules/regexp", s.authMiddleware(s.handleListRegexp))
	s.mux.HandleFunc("POST /api/rules/regexp", s.authMiddleware(s.handleAddRegexp))
	s.mux.HandleFunc("DELETE /api/rules/regexp", s.authMiddleware(s.handleDeleteRegexp))
	s.mux.HandleFunc("GET /api/rules/urls", s.authMiddleware(s.handleListURLs))
	s.mux.HandleFunc("POST /api/rules/urls", s.authMiddleware(s.handleAddURL))
	s.mux.HandleFunc("DELETE /api/rules/urls", s.authMiddleware(s.handleDeleteURL))

	s.mux.HandleFunc("GET /api/system", s.authMiddleware(s.handleSystem))
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.corsMiddleware(s.mux)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		expected := "Bearer " + s.cfg.Web.AuthToken
		if s.cfg.Web.AuthToken != "" && token != expected && token != s.cfg.Web.AuthToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
