// Package main is the brain standalone server — a memory-backed LLM gateway.
// It runs locally (or anywhere), exposes OpenAI + memory APIs, injects recalled
// memories into every chat turn, and forwards to whatever upstream LLM the user
// configured. Point any CLI at it; switch CLI, memory follows.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/damonlinyz/brain/gateway"
	"github.com/damonlinyz/brain/memory/embedder"
	memoryhub "github.com/damonlinyz/brain/memory/hub"
	memstore "github.com/damonlinyz/brain/memory/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := gateway.LoadConfig()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// --- build the memory hub ---
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("brain: cannot connect to database", "error", err, "url", cfg.DatabaseURL)
		os.Exit(1)
	}
	defer pool.Close()

	pgStore := memstore.NewPGStore(pool)
	emb := embedder.NewOllamaAdapter(
		embedder.NewOllamaHTTP(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel),
	)
	hub := memoryhub.New(memoryhub.Deps{
		Store:    pgStore,
		Embedder: emb,
		Logger:   logger,
	})
	if err := hub.InitDefaults(); err != nil {
		logger.Error("brain: hub init failed", "error", err)
		os.Exit(1)
	}

	// --- HTTP server ---
	srv := gateway.NewServer(cfg, hub, pool, logger)
	mux := srv.Routes()
	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		logger.Info("brain server listening", "port", cfg.Port, "upstream", cfg.UpstreamLLMBaseURL, "model", cfg.UpstreamLLMModel)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("brain server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("brain shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
}
