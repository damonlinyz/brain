// Package gateway is brain's HTTP layer: a memory-backed LLM gateway (OpenAI
// /v1/chat/completions) + the memory API (ingest/recall/nodes/correct). It
// injects recalled memories into every chat turn and forwards to the upstream
// LLM, so any CLI pointed at brain gets persistent memory automatically.
package gateway

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	memoryhub "github.com/damonlinyz/brain/memory/hub"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds gateway dependencies.
type Server struct {
	cfg  Config
	hub  *memoryhub.Hub
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewServer constructs the gateway.
func NewServer(cfg Config, hub *memoryhub.Hub, pool *pgxpool.Pool, log *slog.Logger) *Server {
	return &Server{cfg: cfg, hub: hub, pool: pool, log: log}
}

// Routes builds the HTTP mux.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Health.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// OpenAI-compatible chat gateway — the main entry point for CLIs.
	mux.HandleFunc("/v1/chat/completions", s.auth(s.handleChat))

	// Anthropic-protocol gateway (Claude Code).
	mux.HandleFunc("/v1/messages", s.auth(s.handleAnthropicChat))

	// Memory API.
	memux := http.NewServeMux()
	memux.HandleFunc("/api/v1/memory/ingest", s.auth(s.handleIngest))
	memux.HandleFunc("/api/v1/memory/recall", s.auth(s.handleRecall))
	memux.HandleFunc("/api/v1/memory/index", s.auth(s.handleMemoryIndex))
	memux.HandleFunc("/api/v1/memory/nodes", s.auth(s.handleNodes))
	mux.Handle("/api/v1/memory/", memux)

	return s.logReq(mux)
}

// auth is a simple bearer-token gate. When cfg.Token is empty, auth is disabled
// (local dev). The token also resolves to the configured single-user owner.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got != s.cfg.Token {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) logReq(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur_ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
