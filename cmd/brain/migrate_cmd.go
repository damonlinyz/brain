package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/damonlinyz/brain/gateway"
	"github.com/damonlinyz/brain/memory/embedder"
	"github.com/damonlinyz/brain/memory/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runMigrate(dryRun bool, sources string) {
	cfg := gateway.LoadConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil { fmt.Fprintf(os.Stderr, "db: %v\n", err); os.Exit(1) }
	defer pool.Close()

	emb := embedder.NewOllamaAdapter(
		embedder.NewOllamaHTTP(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel),
	)
	// Probe embedder.
	if v, err := emb.Embed(ctx, "ping"); err != nil || len(v) == 0 {
		fmt.Fprintf(os.Stderr, "embedder probe failed (EMBEDDING_BASE_URL=%s): %v\n", cfg.EmbeddingBaseURL, err)
		os.Exit(1)
	}
	opts := migrate.Options{DryRun: dryRun}
	if s := strings.TrimSpace(sources); s != "" {
		for _, part := range strings.Split(s, ",") {
			if p := strings.TrimSpace(part); p != "" { opts.Sources = append(opts.Sources, p) }
		}
	}
	report, err := migrate.Migrate(ctx, pool, emb, opts, nil)
	if err != nil { fmt.Fprintf(os.Stderr, "migrate: %v\n", err); os.Exit(1) }
	for _, r := range []struct{ name string; rep migrate.SourceReport }{
		{"core_memories", report.CoreMemories},
		{"extracted_memories", report.ExtractedMemories},
		{"user_profiles", report.UserProfiles},
	} {
		if r.rep.Scanned > 0 || r.rep.Migrated > 0 || r.rep.Skipped > 0 || r.rep.Failed > 0 {
			fmt.Fprintf(os.Stderr, "  %-20s scanned=%-6d migrated=%-6d skipped=%-6d failed=%-6d (%s)\n",
				r.name, r.rep.Scanned, r.rep.Migrated, r.rep.Skipped, r.rep.Failed, r.rep.Duration.Round(time.Millisecond))
		}
	}
	t := report.Total()
	fmt.Fprintf(os.Stderr, "  %-20s scanned=%-6d migrated=%-6d skipped=%-6d failed=%-6d (%s)\n",
		"TOTAL", t.Scanned, t.Migrated, t.Skipped, t.Failed, t.Duration.Round(time.Millisecond))
	if t.Failed > 0 { os.Exit(1) }
}
