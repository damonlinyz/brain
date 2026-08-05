package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestStore(t *testing.T) (*PGStore, *pgxpool.Pool) {
	t.Helper()
dsn := os.Getenv("MYBRAIN_TEST_DB")
	if dsn == "" {
		t.Skip("set MYBRAIN_TEST_DB to run PGStore tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	// Truncate V2 tables for isolation (order respects FK).
	for _, tbl := range []string{"memory_embeddings", "memory_edges", "memory_node_history",
		"cold_storage_refs", "memory_node_meta", "outbox_events"} {
		if _, err := pool.Exec(ctx, fmt.Sprintf("TRUNCATE %s CASCADE", tbl)); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	return NewPGStore(pool), pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	uid := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO users (id, slug, display_name, timezone, language)
		 VALUES ($1, $2, $3, 'UTC', 'zh-CN')
		 ON CONFLICT (id) DO NOTHING`,
		uid, "u-"+uid.String()[:8], "Tester "+uid.String()[:4])
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return uid
}

// makeVec creates a vector with first `prefix` dimensions set to `value`,
// rest zero. Two vectors with overlapping prefixes have non-trivial cosine sim.
func makeVec(dim, prefix int, value float32) []float32 {
	v := make([]float32, dim)
	for i := 0; i < prefix && i < dim; i++ {
		v[i] = value
	}
	return v
}

func TestCreateNode_AssignsDefaults(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()

	node, err := store.CreateNode(ctx, types.CreateNodeInput{
		UserID:  uid,
		Content: "hello world",
		Type:    types.MemoryTypeEpisodic,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if node.ID == uuid.Nil {
		t.Fatal("expected non-nil ID")
	}
	if node.Version != 1 {
		t.Fatalf("expected version 1, got %d", node.Version)
	}
	if node.State != types.NodeStateActive {
		t.Fatalf("expected state active, got %s", node.State)
	}
	if node.Weight != 0.5 {
		t.Fatalf("expected default weight 0.5, got %f", node.Weight)
	}
	if node.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
	if time.Since(node.CreatedAt) > 5*time.Minute {
		t.Fatalf("created_at stale: %v", node.CreatedAt)
	}
}

func TestGetNode_NotFound(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.GetNode(context.Background(), uuid.New())
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetNode_SoftDeletedReturnsNotFound(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()
	node, _ := store.CreateNode(ctx, types.CreateNodeInput{
		UserID: uid, Content: "x", Type: types.MemoryTypeEpisodic,
	})
	if err := store.SoftDelete(ctx, node.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	_, err := store.GetNode(ctx, node.ID)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after soft-delete, got %v", err)
	}
}

func TestUpdateNode_OptimisticLockConflict(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()
	node, _ := store.CreateNode(ctx, types.CreateNodeInput{
		UserID: uid, Content: "v1", Type: types.MemoryTypeEpisodic,
	})

	// Bump version externally to simulate concurrent update
	_, err := pool.Exec(ctx,
		`UPDATE memory_node_meta SET version = version + 1, updated_at = now() WHERE id = $1`,
		node.ID)
	if err != nil {
		t.Fatalf("external bump: %v", err)
	}

	_, err = store.UpdateNode(ctx, node.ID, node.Version, func(n *types.MemoryNode) {
		n.Content = "v2"
	})
	if err != ErrOptimisticLockConflict {
		t.Fatalf("expected ErrOptimisticLockConflict, got %v", err)
	}
}

func TestUpdateNode_BumpsVersion(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()
	node, _ := store.CreateNode(ctx, types.CreateNodeInput{
		UserID: uid, Content: "v1", Type: types.MemoryTypeEpisodic,
	})
	updated, err := store.UpdateNode(ctx, node.ID, node.Version, func(n *types.MemoryNode) {
		n.Content = "v2"
		n.AccessCount = 5
	})
	if err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("expected version 2, got %d", updated.Version)
	}
	if updated.Content != "v2" {
		t.Fatalf("expected content 'v2', got %s", updated.Content)
	}
	if updated.AccessCount != 5 {
		t.Fatalf("expected access_count 5, got %d", updated.AccessCount)
	}
}

func TestListNodes_PaginationCursor(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		_, err := store.CreateNode(ctx, types.CreateNodeInput{
			UserID: uid, Content: fmt.Sprintf("n%d", i), Type: types.MemoryTypeEpisodic,
		})
		if err != nil {
			t.Fatalf("CreateNode %d: %v", i, err)
		}
	}
	page1, err := store.ListNodes(ctx, SearchFilter{UserID: uid, Limit: 10})
	if err != nil {
		t.Fatalf("ListNodes page1: %v", err)
	}
	if len(page1.Items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(page1.Items))
	}
	if page1.NextCursor == "" {
		t.Fatal("expected non-empty NextCursor")
	}

	page2, err := store.ListNodes(ctx, SearchFilter{UserID: uid, Limit: 10, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("ListNodes page2: %v", err)
	}
	if len(page2.Items) != 10 {
		t.Fatalf("expected 10 items on page2, got %d", len(page2.Items))
	}
}

func TestSearchSimilar_CosineRank(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()

	cat, err := store.CreateNode(ctx, types.CreateNodeInput{
		UserID: uid, Content: "cat", Type: types.MemoryTypeSemantic,
		Embedding: makeVec(768, 100, 1.0),
	})
	if err != nil {
		t.Fatalf("CreateNode cat: %v", err)
	}
	_, err = store.CreateNode(ctx, types.CreateNodeInput{
		UserID: uid, Content: "dog", Type: types.MemoryTypeSemantic,
		Embedding: makeVec(768, 200, 1.0),
	})
	if err != nil {
		t.Fatalf("CreateNode dog: %v", err)
	}

	results, err := store.SearchSimilar(ctx, SimilarQuery{
		UserID:    uid,
		Embedding: makeVec(768, 100, 1.0),
		TopK:      5,
		MinSim:    0.3,
	})
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Node.ID != cat.ID {
		t.Fatalf("expected top result to be cat (%s), got %s", cat.ID, results[0].Node.ID)
	}
	if results[0].Sim < 0.5 {
		t.Fatalf("expected sim > 0.5, got %f", results[0].Sim)
	}
}

func TestDecayAll_AppliesFunction(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_, err := store.CreateNode(ctx, types.CreateNodeInput{
			UserID: uid, Content: fmt.Sprintf("n%d", i), Type: types.MemoryTypeEpisodic,
			Weight: 0.8,
		})
		if err != nil {
			t.Fatalf("CreateNode %d: %v", i, err)
		}
	}
	// Force updated_at into the past so the WHERE matches
	_, _ = pool.Exec(ctx, `UPDATE memory_node_meta SET updated_at = now() - interval '1 hour' WHERE user_id = $1`, uid)

	before := time.Now()
	n, err := store.DecayAll(ctx, uid, before, func(n types.MemoryNode) float64 {
		return n.Weight * 0.9
	})
	if err != nil {
		t.Fatalf("DecayAll: %v", err)
	}
	if n != 10 {
		t.Fatalf("expected 10 rows decayed, got %d", n)
	}

	var avgWeight float64
	err = pool.QueryRow(ctx,
		`SELECT AVG(weight) FROM memory_node_meta WHERE user_id = $1 AND deleted_at IS NULL`, uid).Scan(&avgWeight)
	if err != nil {
		t.Fatalf("AVG: %v", err)
	}
	if avgWeight > 0.73 || avgWeight < 0.71 {
		t.Fatalf("expected avg weight ~0.72 (0.8*0.9), got %f", avgWeight)
	}
}

func TestWithTx_RollsBackOnError(t *testing.T) {
	store, pool := newTestStore(t)
	uid := seedUser(t, pool)
	ctx := context.Background()

	txStore, err := store.WithTx(ctx)
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	_, err = txStore.CreateNode(ctx, types.CreateNodeInput{
		UserID: uid, Content: "in-tx", Type: types.MemoryTypeEpisodic,
	})
	if err != nil {
		t.Fatalf("CreateNode in tx: %v", err)
	}
	// Abandon tx (rollback) — never call CommitTx
	_ = txStore.RollbackTx(ctx)

	var count int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM memory_node_meta WHERE content = 'in-tx'`).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", count)
	}
}
