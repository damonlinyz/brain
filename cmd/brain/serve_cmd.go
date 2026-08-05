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

func runServe() {
	cfg := gateway.LoadConfig()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil { log.Error("brain: cannot connect", "error", err); os.Exit(1) }
	defer pool.Close()

	emb := embedder.NewOllamaAdapter(embedder.NewOllamaHTTP(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel))
	hub := memoryhub.New(memoryhub.Deps{Store: memstore.NewPGStore(pool), Embedder: emb, Logger: log})
	_ = hub.InitDefaults()

	mux := gateway.NewServer(cfg, hub, pool, log).Routes()
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("brain listening", "port", cfg.Port, "upstream", cfg.UpstreamLLMBaseURL, "model", cfg.UpstreamLLMModel)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("brain: serve failed", "error", err); os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
