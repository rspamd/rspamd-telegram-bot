package web

import (
	"net/http"
	"runtime"
)

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	// Rspamd version
	rspamdVersion := "unknown"
	if ver, err := s.rspamd.Version(r.Context()); err == nil {
		rspamdVersion = ver
	}

	// ClickHouse stats
	var chStats interface{}
	stats, err := s.storage.GetTableStats(r.Context())
	if err != nil {
		s.logger.Error("clickhouse stats failed", "error", err)
	} else {
		chStats = stats
	}

	// Redis stats
	redisInfo := map[string]string{}
	if info, err := s.redis.Info(r.Context(), "memory", "clients", "keyspace").Result(); err == nil {
		redisInfo["raw"] = info
	}
	dbSize, _ := s.redis.DBSize(r.Context()).Result()

	result := map[string]interface{}{
		"rspamd_version": rspamdVersion,
		"go_version":     runtime.Version(),
		"clickhouse":     chStats,
		"redis": map[string]interface{}{
			"total_keys": dbSize,
		},
	}

	writeJSON(w, result)
}
