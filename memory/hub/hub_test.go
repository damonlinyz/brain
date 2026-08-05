package hub

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// fakeStore is a tiny in-memory IMemoryStore good enough for Hub tests.
type fakeStore struct {
	mu      sync.Mutex
	nodes   map[uuid.UUID]types.MemoryNode
	edges   []types.MemoryEdge
	history []types.MemoryNodeHistory
	version map[uuid.UUID]int
	nextErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nodes:   make(map[uuid.UUID]types.MemoryNode),
		version: make(map[uuid.UUID]int),
	}
}

func (s *fakeStore) CreateNode(ctx context.Context, in types.CreateNodeInput) (types.MemoryNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nextErr != nil {
		err := s.nextErr
		s.nextErr = nil
		return types.MemoryNode{}, err
	}
	id := uuid.New()
	now := time.Now()
	n := types.MemoryNode{
		ID: id, UserID: in.UserID, TenantID: in.TenantID, SessionID: in.SessionID,
		Content: in.Content, Summary: in.Summary, ContentType: in.ContentType,
		Keywords: in.Keywords, Source: in.Source, Type: in.Type, Salience: in.Salience,
		EmotionalTone: in.EmotionalTone, Weight: in.Weight, SourceTrust: in.SourceTrust,
		State: types.NodeStateActive, Version: 1,
		CreatedAt: now, UpdatedAt: now, LastAccessAt: now,
	}
	s.nodes[id] = n
	s.version[id] = 1
	return n, nil
}

func (s *fakeStore) GetNode(ctx context.Context, id uuid.UUID) (types.MemoryNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return types.MemoryNode{}, store.ErrNotFound
	}
	return n, nil
}

func (s *fakeStore) UpdateNode(ctx context.Context, id uuid.UUID, expectedVersion int, mutators ...store.NodeMutator) (types.MemoryNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return types.MemoryNode{}, store.ErrNotFound
	}
	if n.Version != expectedVersion {
		return types.MemoryNode{}, store.ErrOptimisticLockConflict
	}
	for _, m := range mutators {
		m(&n)
	}
	n.Version++
	n.UpdatedAt = time.Now()
	s.nodes[id] = n
	s.version[id] = n.Version
	return n, nil
}

func (s *fakeStore) SoftDelete(context.Context, uuid.UUID) error { return nil }

func (s *fakeStore) RecordHistory(_ context.Context, h types.MemoryNodeHistory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, h)
	return nil
}

func (s *fakeStore) ListNodes(context.Context, store.SearchFilter) (store.SearchResults, error) {
	return store.SearchResults{}, nil
}

func (s *fakeStore) SearchSimilar(ctx context.Context, q store.SimilarQuery) ([]store.SimilarResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Return existing nodes with a synthetic sim so PatternSeparation can merge/link.
	out := []store.SimilarResult{}
	for _, n := range s.nodes {
		// Caller controls "similarity" by seeding n.DgraphUID with a number via the
		// test helper seedNode. Here we just echo it.
		out = append(out, store.SimilarResult{Node: n, Sim: 0})
	}
	return out, nil
}

func (s *fakeStore) SearchByKeywords(context.Context, uuid.UUID, []string, int) ([]store.SimilarResult, error) {
	return nil, nil
}
func (s *fakeStore) SearchHybrid(ctx context.Context, q store.SimilarQuery, kw []string) ([]store.SimilarResult, error) {
	return s.SearchSimilar(ctx, q)
}
func (s *fakeStore) CreateEdge(ctx context.Context, in types.CreateEdgeInput) (types.MemoryEdge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := types.MemoryEdge{ID: uuid.New(), FromNodeID: in.FromNodeID, ToNodeID: in.ToNodeID, EdgeType: in.EdgeType, Weight: in.Weight}
	s.edges = append(s.edges, e)
	return e, nil
}
func (s *fakeStore) GetEdges(context.Context, uuid.UUID, []types.EdgeKind) ([]types.MemoryEdge, error) {
	return nil, nil
}
func (s *fakeStore) DecayAll(context.Context, uuid.UUID, time.Time, store.DecayFn) (int, error) {
	return 0, nil
}
func (s *fakeStore) BulkUpdateWeight(context.Context, uuid.UUID, []store.WeightUpdate) (int, error) {
	return 0, nil
}
func (s *fakeStore) FindUnconnectedSimilarPairs(context.Context, uuid.UUID, float64, int) ([]store.SimilarPair, error) {
	return nil, nil
}
func (s *fakeStore) ListMemoryUserIDs(context.Context) ([]uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[uuid.UUID]struct{}{}
	out := []uuid.UUID{}
	for _, n := range s.nodes {
		if _, ok := seen[n.UserID]; ok {
			continue
		}
		seen[n.UserID] = struct{}{}
		out = append(out, n.UserID)
	}
	return out, nil
}
func (s *fakeStore) WithTx(context.Context) (store.IMemoryStore, error) { return s, nil }
func (s *fakeStore) CommitTx(context.Context) error                    { return nil }
func (s *fakeStore) RollbackTx(context.Context) error                  { return nil }

func (s *fakeStore) GetGraphNeighbors(context.Context, uuid.UUID, []uuid.UUID, int) ([]store.GraphNeighbor, error) { return nil, nil }
func (s *fakeStore) FindPath(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) ([]store.GraphNeighbor, error) { return nil, nil }
func (s *fakeStore) ExpandSubgraph(context.Context, uuid.UUID, []uuid.UUID, int) ([]store.GraphNeighbor, error) { return nil, nil }
// fakeEmbedder returns deterministic vectors.
type fakeEmbedder struct {
	dim int
}

func (f fakeEmbedder) Embed(ctx context.Context, content string) ([]float32, error) {
	v := make([]float32, f.dim)
	for i := range v {
		v[i] = float32(len(content) + i)
	}
	return v, nil
}
func (f fakeEmbedder) EmbedBatch(ctx context.Context, contents []string) ([][]float32, error) {
	out := make([][]float32, len(contents))
	for i, c := range contents {
		v, _ := f.Embed(ctx, c)
		out[i] = v
	}
	return out, nil
}
func (f fakeEmbedder) Dim() int { return f.dim }

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	h := New(Deps{
		Store:    newFakeStore(),
		Embedder: fakeEmbedder{dim: 8},
	})
	if err := h.InitDefaults(); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestIngest_PersistsRememberedFact(t *testing.T) {
	h := newTestHub(t)

	res, err := h.Ingest(context.Background(), IngestInput{
		UserID:  uuid.New(),
		RawText: "Remember that I am allergic to peanuts.",
		Source:  types.SourceHumanInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	// No existing nodes → everything is "new".
	if len(res.Stored) == 0 {
		t.Fatal("expected at least one stored node")
	}
	if res.Stored[0].Action != "new" {
		t.Fatalf("expected new, got %s", res.Stored[0].Action)
	}
	if res.Stored[0].NodeID == uuid.Nil {
		t.Fatal("expected non-nil node id")
	}
}

func TestIngest_DropsSmallTalk(t *testing.T) {
	h := newTestHub(t)

	res, err := h.Ingest(context.Background(), IngestInput{
		UserID:  uuid.New(),
		RawText: "hi",
		Source:  types.SourceHumanInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	// "hi" → fallback extractor trims it (< 4 runes) → 0 facts → 0 stored.
	if len(res.Stored) != 0 {
		t.Fatalf("expected nothing stored for small talk, got %d", len(res.Stored))
	}
}

func TestIngest_MergesIntoExistingNode(t *testing.T) {
	h := newTestHub(t)
	store := h.store.(*fakeStore)

	// Seed an existing node so SearchSimilar returns it with high sim.
	// We hijack SearchSimilar via a closure-free trick: patch nextErr is not it.
	// Instead, pre-create a node and rely on the fake returning it (sim=0 means
	// PatternSeparation classifies as New). To force a merge we need sim >= mergeSim.
	// Use a custom store variant.
	id := uuid.New()
	now := time.Now()
	store.nodes[id] = types.MemoryNode{
		ID: id, UserID: uuid.New(), Summary: "existing", Weight: 0.3,
		State: types.NodeStateActive, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	store.version[id] = 1

	// Override SearchSimilar to return high similarity.
	mergeStore := &simStore{fakeStore: store, sim: 0.95}
	h.store = mergeStore

	res, err := h.Ingest(context.Background(), IngestInput{
		UserID:  mergeStore.nodes[id].UserID,
		RawText: "Remember that I am allergic to peanuts.",
		Source:  types.SourceHumanInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stored) != 1 {
		t.Fatalf("expected 1 stored, got %d", len(res.Stored))
	}
	if res.Stored[0].Action != "merge" {
		t.Fatalf("expected merge, got %s", res.Stored[0].Action)
	}
	if res.Stored[0].NodeID != id {
		t.Fatalf("expected merge into seeded node %s, got %s", id, res.Stored[0].NodeID)
	}
	// Merged node should have gained weight + access count.
	merged := mergeStore.nodes[id]
	if merged.AccessCount == 0 {
		t.Fatal("expected access count bumped on merge")
	}
}

func TestIngest_LinksToExistingNode(t *testing.T) {
	h := newTestHub(t)
	store := h.store.(*fakeStore)

	id := uuid.New()
	uid := uuid.New()
	now := time.Now()
	store.nodes[id] = types.MemoryNode{
		ID: id, UserID: uid, Summary: "related", Weight: 0.3,
		State: types.NodeStateActive, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	store.version[id] = 1

	linkStore := &simStore{fakeStore: store, sim: 0.78}
	h.store = linkStore

	res, err := h.Ingest(context.Background(), IngestInput{
		UserID:  uid,
		RawText: "Remember that I am allergic to peanuts.",
		Source:  types.SourceHumanInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stored) != 1 || res.Stored[0].Action != "link" {
		t.Fatalf("expected link, got %+v", res.Stored)
	}
	if res.Stored[0].LinkedTo == nil || *res.Stored[0].LinkedTo != id {
		t.Fatalf("expected link to seeded node %s", id)
	}
	// An edge should have been created.
	if len(linkStore.edges) == 0 {
		t.Fatal("expected a similarity edge created on link")
	}
	if linkStore.edges[0].EdgeType != types.EdgeKindSimilarTo {
		t.Fatalf("expected similar_to edge, got %s", linkStore.edges[0].EdgeType)
	}
}

func TestRecall_ReturnsCompressedContext(t *testing.T) {
	h := newTestHub(t)
	store := h.store.(*fakeStore)

	// Seed two nodes that SearchSimilar will return.
	uid := uuid.New()
	now := time.Now()
	for i, summary := range []string{"User loves hiking", "User hates cilantro"} {
		id := uuid.New()
		store.nodes[id] = types.MemoryNode{
			ID: id, UserID: uid, Summary: summary, Weight: 0.5,
			Confidence: 0.8, ConsistencyScore: 0.8, SourceTrust: 0.8,
			State: types.NodeStateActive, Version: 1, CreatedAt: now, UpdatedAt: now, LastAccessAt: now,
		}
		store.version[id] = 1
		_ = i
	}
	h.store = &simStore{fakeStore: store, sim: 0.8}

	ctx, err := h.Recall(context.Background(), RecallInput{
		UserID: uid,
		Query:  "outdoor activities",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.SystemPrompt == "" {
		t.Fatal("expected non-empty system prompt")
	}
	if len(ctx.Memories) != 2 {
		t.Fatalf("expected 2 memories in compressed context, got %d", len(ctx.Memories))
	}
	if ctx.TokenUsed > ctx.TokenBudget {
		t.Fatalf("used %d > budget %d", ctx.TokenUsed, ctx.TokenBudget)
	}
}

func TestRecall_NoResults_EmptyContext(t *testing.T) {
	h := newTestHub(t)
	// Empty store.
	ctx, err := h.Recall(context.Background(), RecallInput{
		UserID: uuid.New(),
		Query:  "anything",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.Memories) != 0 {
		t.Fatalf("expected 0 memories on empty store, got %d", len(ctx.Memories))
	}
}

func TestReinforce_BumpsWeight(t *testing.T) {
	h := newTestHub(t)
	store := h.store.(*fakeStore)

	id := uuid.New()
	now := time.Now()
	store.nodes[id] = types.MemoryNode{
		ID: id, UserID: uuid.New(), Summary: "x", Weight: 0.3, Version: 1,
		State: types.NodeStateActive, CreatedAt: now, UpdatedAt: now,
	}
	store.version[id] = 1

	before := store.nodes[id].Weight
	if err := h.Reinforce(context.Background(), id, 1.0); err != nil {
		t.Fatal(err)
	}
	after := store.nodes[id].Weight
	if after <= before {
		t.Fatalf("expected weight bumped: before=%f after=%f", before, after)
	}
	if store.nodes[id].AccessCount == 0 {
		t.Fatal("expected access count bumped")
	}
}

func TestReinforce_NotFound(t *testing.T) {
	h := newTestHub(t)
	err := h.Reinforce(context.Background(), uuid.New(), 1.0)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestNotConfigured(t *testing.T) {
	h := New(Deps{}) // no store, no embedder
	_ = h.InitDefaults()

	if _, err := h.Ingest(context.Background(), IngestInput{UserID: uuid.New(), RawText: "x"}); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured on ingest, got %v", err)
	}
	if _, err := h.Recall(context.Background(), RecallInput{UserID: uuid.New()}); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured on recall, got %v", err)
	}
}

func TestRunConsolidationAll_SweepsEveryUser(t *testing.T) {
	h := newTestHub(t)
	store := h.store.(*fakeStore)

	// Seed two users with one node each.
	now := time.Now()
	uid1, uid2 := uuid.New(), uuid.New()
	store.nodes[uuid.New()] = types.MemoryNode{ID: uuid.New(), UserID: uid1, State: types.NodeStateActive, Weight: 0.5, Version: 1, LastAccessAt: now}
	store.nodes[uuid.New()] = types.MemoryNode{ID: uuid.New(), UserID: uid2, State: types.NodeStateActive, Weight: 0.5, Version: 1, LastAccessAt: now}

	summary, err := h.RunConsolidationAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Users != 2 {
		t.Fatalf("expected 2 users swept, got %d", summary.Users)
	}
}

func TestRunConsolidationAll_EmptyStore(t *testing.T) {
	h := newTestHub(t)
	summary, err := h.RunConsolidationAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Users != 0 {
		t.Fatalf("expected 0 users on empty store, got %d", summary.Users)
	}
}

// simStore wraps fakeStore, returning a fixed similarity for every node so
// tests can drive PatternSeparation into merge/link/new branches.
type simStore struct {
	*fakeStore
	sim float64
}

func (s *simStore) SearchSimilar(ctx context.Context, q store.SimilarQuery) ([]store.SimilarResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.SimilarResult{}
	for _, n := range s.nodes {
		out = append(out, store.SimilarResult{Node: n, Sim: s.sim})
	}
	return out, nil
}
func (s *simStore) SearchHybrid(ctx context.Context, q store.SimilarQuery, kw []string) ([]store.SimilarResult, error) {
	return s.SearchSimilar(ctx, q)
}
func (s *simStore) WithTx(context.Context) (store.IMemoryStore, error) { return s, nil }
