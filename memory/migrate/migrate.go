// Package migrate implements the one-shot V1→V2 memory data migration.
//
// V1 memory lives in three flat tables — core_memories, extracted_memories,
// user_profiles — keyed by category/dimension. V2 stores everything as
// memory_node_meta + memory_embeddings rows. This package reads each V1 row,
// embeds its content via the project embedder, and inserts the V2 node in a
// single transaction together with a v2_migration_ledger row that makes the
// whole operation idempotent (re-runnable, crash-safe).
//
// Invoked via `mybrain migrate-v2` (see internal/cli). Not called by the
// running server.
package migrate

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Embedder is the slice of the V2 embedder the migration needs.
type Embedder interface {
	Embed(ctx context.Context, content string) ([]float32, error)
}

// SourceReport tallies one V1 table's migration.
type SourceReport struct {
	Table    string
	Scanned  int
	Migrated int
	Skipped  int
	Failed   int
	Duration time.Duration
}

// Report aggregates all sources.
type Report struct {
	CoreMemories      SourceReport
	ExtractedMemories SourceReport
	UserProfiles      SourceReport
}

// Total across all sources.
func (r Report) Total() SourceReport {
	sum := func(s ...SourceReport) SourceReport {
		var out SourceReport
		for _, x := range s {
			out.Scanned += x.Scanned
			out.Migrated += x.Migrated
			out.Skipped += x.Skipped
			out.Failed += x.Failed
			out.Duration += x.Duration
		}
		return out
	}
	t := sum(r.CoreMemories, r.ExtractedMemories, r.UserProfiles)
	t.Table = "total"
	return t
}

// Options controls which sources run and how.
type Options struct {
	// Sources filters which tables migrate; empty = all three.
	// Valid: "core_memories", "extracted_memories", "user_profiles".
	Sources []string
	// Limit caps rows per source (0 = no cap). Useful for smoke tests.
	Limit int
	// DryRun scans + embeds nothing; just reports what would migrate (respects ledger).
	DryRun bool
}

func (o Options) wants(table string) bool {
	if len(o.Sources) == 0 {
		return true
	}
	for _, s := range o.Sources {
		if s == table {
			return true
		}
	}
	return false
}

// Migrate runs the V1→V2 migration. Each source is independent — one source's
// error doesn't abort the others. Embedding failures count as Failed, not fatal.
func Migrate(ctx context.Context, pool *pgxpool.Pool, emb Embedder, opts Options, log *slog.Logger) (Report, error) {
	if log == nil {
		log = slog.Default()
	}
	if pool == nil || emb == nil {
		return Report{}, fmt.Errorf("migrate: pool and embedder are required")
	}
	var report Report

	if opts.wants("core_memories") {
		report.CoreMemories = migrateSource(ctx, pool, emb, opts, log, coreMemoriesQuery, "core_memories", rowToNodeCore)
	}
	if opts.wants("extracted_memories") {
		report.ExtractedMemories = migrateSource(ctx, pool, emb, opts, log, extractedMemoriesQuery, "extracted_memories", rowToNodeExtracted)
	}
	if opts.wants("user_profiles") {
		report.UserProfiles = migrateSource(ctx, pool, emb, opts, log, userProfilesQuery, "user_profiles", rowToNodeProfile)
	}
	return report, nil
}

// v1Row is the common shape extracted from any V1 table.
type v1Row struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Content   string // text to embed + store
	Summary   string // short label (may be empty → derived)
	Category  string // drives content_type mapping
	Source    types.Source
	CreatedAt time.Time
}

const (
	coreMemoriesQuery = `SELECT id, user_id, content, category, source, created_at FROM core_memories ORDER BY priority DESC, created_at`
	extractedMemoriesQuery = `SELECT id, user_id, content, category, created_at FROM extracted_memories WHERE is_duplicate = false ORDER BY created_at`
	userProfilesQuery = `SELECT id, user_id, summary, details, dimension, created_at FROM user_profiles ORDER BY created_at`
)

func rowToNodeCore(rows pgx.Rows) (v1Row, error) {
	var r v1Row
	var cat, src string
	if err := rows.Scan(&r.ID, &r.UserID, &r.Content, &cat, &src, &r.CreatedAt); err != nil {
		return v1Row{}, err
	}
	r.Category = cat
	r.Source = v1SourceToV2(src)
	r.Summary = truncate(r.Content, 60)
	return r, nil
}

func rowToNodeExtracted(rows pgx.Rows) (v1Row, error) {
	var r v1Row
	var cat string
	if err := rows.Scan(&r.ID, &r.UserID, &r.Content, &cat, &r.CreatedAt); err != nil {
		return v1Row{}, err
	}
	r.Category = cat
	r.Source = types.SourceInference
	r.Summary = truncate(r.Content, 60)
	return r, nil
}

func rowToNodeProfile(rows pgx.Rows) (v1Row, error) {
	var r v1Row
	var summary, details, dimension string
	if err := rows.Scan(&r.ID, &r.UserID, &summary, &details, &dimension, &r.CreatedAt); err != nil {
		return v1Row{}, err
	}
	r.Category = dimension
	// Prefer details (richer); fall back to summary. Both can't be empty (NOT NULL summary).
	if strings.TrimSpace(details) != "" {
		r.Content = summary + ": " + details
	} else {
		r.Content = summary
	}
	r.Summary = summary
	r.Source = types.SourceInference
	return r, nil
}

// migrateSource streams one V1 table, embedding + inserting each un-ledgered row.
func migrateSource(
	ctx context.Context, pool *pgxpool.Pool, emb Embedder, opts Options, log *slog.Logger,
	query, table string, scan func(pgx.Rows) (v1Row, error),
) SourceReport {
	rep := SourceReport{Table: table}
	start := time.Now()
	defer func() { rep.Duration = time.Since(start) }()

	rows, err := pool.Query(ctx, query)
	if err != nil {
		log.Error("migrate: query failed", "table", table, "error", err)
		rep.Failed = -1 // signal query error distinctly
		return rep
	}
	defer rows.Close()

	for rows.Next() {
		if ctx.Err() != nil {
			break
		}
		if opts.Limit > 0 && rep.Scanned >= opts.Limit {
			break
		}
		rep.Scanned++

		r, err := scan(rows)
		if err != nil {
			log.Warn("migrate: scan failed", "table", table, "error", err)
			rep.Failed++
			continue
		}
		if strings.TrimSpace(r.Content) == "" {
			rep.Skipped++
			continue
		}

		// Idempotency: skip if already migrated.
		var exists int
		err = pool.QueryRow(ctx,
			`SELECT 1 FROM v2_migration_ledger WHERE source_table = $1 AND source_id = $2`,
			table, r.ID).Scan(&exists)
		if err != nil && err != pgx.ErrNoRows {
			log.Warn("migrate: ledger check failed", "table", table, "id", r.ID, "error", err)
			rep.Failed++
			continue
		}
		if err == nil { // found → already migrated
			rep.Skipped++
			continue
		}

		if opts.DryRun {
			rep.Migrated++
			continue
		}

		if err := migrateOne(ctx, pool, emb, table, r); err != nil {
			log.Warn("migrate: row failed", "table", table, "id", r.ID, "error", err)
			rep.Failed++
			continue
		}
		rep.Migrated++

		if rep.Migrated%50 == 0 {
			log.Info("migrate: progress", "table", table, "migrated", rep.Migrated, "scanned", rep.Scanned)
		}
	}
	if err := rows.Err(); err != nil {
		log.Error("migrate: rows iteration error", "table", table, "error", err)
	}
	log.Info("migrate: source done", "table", table,
		"scanned", rep.Scanned, "migrated", rep.Migrated, "skipped", rep.Skipped, "failed", rep.Failed,
		"duration", rep.Duration.String())
	return rep
}

// migrateOne embeds the content and inserts node + embedding + ledger in one tx.
func migrateOne(ctx context.Context, pool *pgxpool.Pool, emb Embedder, table string, r v1Row) error {
	embCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	vec, err := emb.Embed(embCtx, r.Content)
	cancel()
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	contentType, memType := classify(r.Category)
	now := time.Now().UTC()
	var nodeID uuid.UUID
	err = tx.QueryRow(ctx, `
        INSERT INTO memory_node_meta
          (user_id, content, summary, content_type, source, type, salience, state,
           weight, source_trust, created_at, updated_at, last_access_at)
        VALUES ($1, $2, $3, $4, $5, $6, 'normal', 'active', $7, $8, $9, $10, $10)
        RETURNING id`,
		r.UserID, r.Content, firstNonEmpty(r.Summary, truncate(r.Content, 60)),
		string(contentType), string(r.Source), string(memType),
		0.5, 0.7, r.CreatedAt, now,
	).Scan(&nodeID)
	if err != nil {
		return fmt.Errorf("insert node: %w", err)
	}

	if len(vec) > 0 {
		if _, err := tx.Exec(ctx, `
            INSERT INTO memory_embeddings (node_id, user_id, embedding, model, dim)
            VALUES ($1, $2, $3, $4, $5)`,
			nodeID, r.UserID, pgvector.NewVector(vec), "nomic-embed-text", len(vec)); err != nil {
			return fmt.Errorf("insert embedding: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
        INSERT INTO v2_migration_ledger (source_table, source_id, node_id, user_id)
        VALUES ($1, $2, $3, $4)`,
		table, r.ID, nodeID, r.UserID); err != nil {
		return fmt.Errorf("insert ledger: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	_ = now
	return nil
}

// classify maps a V1 category/dimension onto V2 (ContentType, MemoryType).
func classify(category string) (types.ContentType, types.MemoryType) {
	c := strings.ToLower(strings.TrimSpace(category))
	switch {
	case strings.Contains(c, "prefer"):
		return types.ContentTypePreference, types.MemoryTypeProfile
	case strings.Contains(c, "skill") || strings.Contains(c, "procedur"):
		return types.ContentTypeSkill, types.MemoryTypeProcedural
	case strings.Contains(c, "event") || strings.Contains(c, "history"):
		return types.ContentTypeEvent, types.MemoryTypeEpisodic
	case strings.Contains(c, "relation"):
		return types.ContentTypeRelation, types.MemoryTypeSemantic
	case strings.Contains(c, "personal") || strings.Contains(c, "profile"):
		return types.ContentTypePreference, types.MemoryTypeProfile
	default: // fact / knowledge / decision / work / unknown
		return types.ContentTypeFact, types.MemoryTypeSemantic
	}
}

func v1SourceToV2(s string) types.Source {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "inference", "llm":
		return types.SourceInference
	case "training":
		return types.SourceTraining
	case "user_correction", "correction":
		return types.SourceUserCorrection
	default:
		return types.SourceHumanInput
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:int(math.Max(0, float64(n)))]) + "…"
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
