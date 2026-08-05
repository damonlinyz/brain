package migrate

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubEmbedder returns a deterministic 8-dim vector so the test doesn't need Ollama.
type stubEmbedder struct{ err error }

func (s stubEmbedder) Embed(ctx context.Context, content string) ([]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	v := make([]float32, 8)
	for i := range v {
		v[i] = float32(len(content) + i)
	}
	return v, nil
}

// --- pure helper tests (no DB) ---

func TestClassify(t *testing.T) {
	cases := []struct {
		category        string
		wantContentType string
		wantMemType     string
	}{
		{"preference", "preference", "profile"},
		{"skill", "skill", "procedural"},
		{"event", "event", "episodic"},
		{"relationship", "relationship", "semantic"},
		{"personal", "preference", "profile"},
		{"fact", "fact", "semantic"},
		{"knowledge", "fact", "semantic"},
		{"", "fact", "semantic"},
	}
	for _, c := range cases {
		ct, mt := classify(c.category)
		if string(ct) != c.wantContentType || string(mt) != c.wantMemType {
			t.Errorf("classify(%q) = (%s,%s), want (%s,%s)", c.category, ct, mt, c.wantContentType, c.wantMemType)
		}
	}
}

func TestV1SourceToV2(t *testing.T) {
	if got := v1SourceToV2("inference"); got != "inference" {
		t.Errorf("inference -> %s", got)
	}
	if got := v1SourceToV2("user_correction"); got != "user_correction" {
		t.Errorf("user_correction -> %s", got)
	}
	if got := v1SourceToV2("manual"); got != "human_input" {
		t.Errorf("default -> %s, want human_input", got)
	}
}

func TestTruncateAndFirstNonEmpty(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("truncate should be no-op when shorter")
	}
	if truncate("hello world", 5) != "hello…" {
		t.Errorf("truncate: %q", truncate("hello world", 5))
	}
	if firstNonEmpty("", "b") != "b" {
		t.Error("firstNonEmpty should fall back")
	}
	if firstNonEmpty("a", "b") != "a" {
		t.Error("firstNonEmpty should prefer first")
	}
}

// --- integration (needs MYBRAIN_TEST_DB) ---

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("MYBRAIN_TEST_DB")
	if dsn == "" {
		t.Skip("set MYBRAIN_TEST_DB to run migrate integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// Hermetic schema: only the tables/columns the migrate package actually
	// touches. Avoids depending on the full mybrain migration chain (which has
	// FKs into chat_sessions etc.). pgvector for the embedding column.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS memory_node_meta (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL,
			content TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '',
			content_type TEXT NOT NULL DEFAULT 'fact', source TEXT NOT NULL DEFAULT 'human_input',
			type TEXT NOT NULL DEFAULT 'semantic', salience TEXT NOT NULL DEFAULT 'normal',
			state TEXT NOT NULL DEFAULT 'active', weight DOUBLE PRECISION NOT NULL DEFAULT 0.5,
			source_trust DOUBLE PRECISION NOT NULL DEFAULT 0.5,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ, version INT NOT NULL DEFAULT 1)`,
		`CREATE TABLE IF NOT EXISTS memory_embeddings (
			node_id UUID PRIMARY KEY, user_id UUID NOT NULL,
			embedding vector(8) NOT NULL, model TEXT NOT NULL, dim INT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS v2_migration_ledger (
			source_table TEXT NOT NULL, source_id UUID NOT NULL, node_id UUID NOT NULL,
			user_id UUID NOT NULL, migrated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (source_table, source_id))`,
		// V1 tables (mirror pgmemhub schema).
		`CREATE TABLE IF NOT EXISTS core_memories (id UUID PRIMARY KEY, user_id UUID NOT NULL, content TEXT NOT NULL, category TEXT NOT NULL, priority INT NOT NULL DEFAULT 0, source TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS extracted_memories (id UUID PRIMARY KEY, user_id UUID NOT NULL, category TEXT NOT NULL, content TEXT NOT NULL, is_duplicate BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS user_profiles (id UUID PRIMARY KEY, user_id UUID NOT NULL, dimension TEXT NOT NULL, summary TEXT NOT NULL, details TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	// Truncate for isolation.
	for _, tbl := range []string{"v2_migration_ledger", "memory_embeddings", "memory_node_meta", "core_memories", "extracted_memories", "user_profiles"} {
		if _, err := pool.Exec(ctx, "TRUNCATE "+tbl+" CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	return pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	uid := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, slug, display_name, timezone, language)
		 VALUES ($1, $2, $3, 'UTC', 'zh-CN') ON CONFLICT (id) DO NOTHING`,
		uid, "m-"+uid.String()[:8], "Migrator")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return uid
}

func TestMigrate_CoreMemories_HappyPath(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	uid := seedUser(t, pool)

	// Seed 2 core memories for the user.
	rows := []struct {
		content, category string
	}{
		{"User prefers concise answers without lists.", "preference"},
		{"User is a Go developer.", "skill"},
	}
	for _, r := range rows {
		if _, err := pool.Exec(ctx,
			`INSERT INTO core_memories (id, user_id, content, category, priority, source)
			 VALUES ($1, $2, $3, $4, 1, 'manual')`,
			uuid.New(), uid, r.content, r.category); err != nil {
			t.Fatalf("seed core_memories: %v", err)
		}
	}

	rep, err := Migrate(ctx, pool, stubEmbedder{}, Options{Sources: []string{"core_memories"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CoreMemories.Migrated != 2 {
		t.Fatalf("expected 2 migrated, got %+v", rep.CoreMemories)
	}
	if rep.CoreMemories.Failed != 0 {
		t.Fatalf("expected 0 failed, got %d", rep.CoreMemories.Failed)
	}

	// Verify nodes + embeddings landed.
	var nodeCount, embCount, ledgerCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM memory_node_meta WHERE user_id=$1`, uid).Scan(&nodeCount)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM memory_embeddings WHERE user_id=$1`, uid).Scan(&embCount)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM v2_migration_ledger WHERE user_id=$1 AND source_table='core_memories'`, uid).Scan(&ledgerCount)
	if nodeCount != 2 || embCount != 2 || ledgerCount != 2 {
		t.Fatalf("expected 2/2/2, got nodes=%d emb=%d ledger=%d", nodeCount, embCount, ledgerCount)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	uid := seedUser(t, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO core_memories (id, user_id, content, category, priority, source)
		 VALUES ($1, $2, 'Allergic to peanuts.', 'personal', 1, 'manual')`,
		uuid.New(), uid)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Migrate(ctx, pool, stubEmbedder{}, Options{Sources: []string{"core_memories"}}, nil); err != nil {
		t.Fatal(err)
	}
	// Second run → everything skipped, no new nodes.
	rep, err := Migrate(ctx, pool, stubEmbedder{}, Options{Sources: []string{"core_memories"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.CoreMemories.Migrated != 0 || rep.CoreMemories.Skipped != 1 {
		t.Fatalf("expected 0 migrated / 1 skipped on re-run, got %+v", rep.CoreMemories)
	}

	var nodeCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM memory_node_meta WHERE user_id=$1`, uid).Scan(&nodeCount)
	if nodeCount != 1 {
		t.Fatalf("idempotency broken: %d nodes after re-run", nodeCount)
	}
}

func TestMigrate_EmptyContentSkipped(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	uid := seedUser(t, pool)

	if _, err := pool.Exec(ctx,
		`INSERT INTO core_memories (id, user_id, content, category, source) VALUES ($1, $2, '   ', 'fact', 'manual')`,
		uuid.New(), uid); err != nil {
		t.Fatal(err)
	}

	rep, _ := Migrate(ctx, pool, stubEmbedder{}, Options{Sources: []string{"core_memories"}}, nil)
	if rep.CoreMemories.Migrated != 0 || rep.CoreMemories.Skipped != 1 {
		t.Fatalf("expected empty content skipped, got %+v", rep.CoreMemories)
	}
}

func TestMigrate_DryRun(t *testing.T) {
	pool := setupDB(t)
	ctx := context.Background()
	uid := seedUser(t, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO core_memories (id, user_id, content, category, source) VALUES ($1, $2, 'A fact.', 'fact', 'manual')`,
		uuid.New(), uid)
	if err != nil {
		t.Fatal(err)
	}

	rep, _ := Migrate(ctx, pool, stubEmbedder{}, Options{Sources: []string{"core_memories"}, DryRun: true}, nil)
	if rep.CoreMemories.Migrated != 1 {
		t.Fatalf("dry-run should count 1, got %+v", rep.CoreMemories)
	}
	var nodeCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM memory_node_meta WHERE user_id=$1`, uid).Scan(&nodeCount)
	if nodeCount != 0 {
		t.Fatalf("dry-run must not write nodes, got %d", nodeCount)
	}
}

// ensure time import stays live if future tests drop its direct use.
var _ = time.Second
