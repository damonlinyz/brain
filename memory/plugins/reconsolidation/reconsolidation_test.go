package reconsolidation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// miniStore is a minimal IMemoryStore good enough for reconsolidation tests:
// it only needs GetNode, UpdateNode, RecordHistory.
type miniStore struct {
	nodes   map[uuid.UUID]types.MemoryNode
	history []types.MemoryNodeHistory
}

func newMini() *miniStore { return &miniStore{nodes: map[uuid.UUID]types.MemoryNode{}} }

func (m *miniStore) GetNode(ctx context.Context, id uuid.UUID) (types.MemoryNode, error) {
	n, ok := m.nodes[id]
	if !ok {
		return types.MemoryNode{}, store.ErrNotFound
	}
	return n, nil
}
func (m *miniStore) UpdateNode(ctx context.Context, id uuid.UUID, ver int, ms ...store.NodeMutator) (types.MemoryNode, error) {
	n, ok := m.nodes[id]
	if !ok {
		return types.MemoryNode{}, store.ErrNotFound
	}
	if n.Version != ver {
		return types.MemoryNode{}, store.ErrOptimisticLockConflict
	}
	for _, f := range ms {
		f(&n)
	}
	n.Version++
	m.nodes[id] = n
	return n, nil
}
func (m *miniStore) RecordHistory(_ context.Context, h types.MemoryNodeHistory) error {
	m.history = append(m.history, h)
	return nil
}

// stubs for the rest of the interface
func (m *miniStore) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (m *miniStore) SoftDelete(context.Context, uuid.UUID) error                { return nil }
func (m *miniStore) ListNodes(context.Context, store.SearchFilter) (store.SearchResults, error) {
	return store.SearchResults{}, nil
}
func (m *miniStore) SearchSimilar(context.Context, store.SimilarQuery) ([]store.SimilarResult, error) {
	return nil, nil
}
func (m *miniStore) SearchByKeywords(context.Context, uuid.UUID, []string, int) ([]store.SimilarResult, error) {
	return nil, nil
}
func (m *miniStore) SearchHybrid(context.Context, store.SimilarQuery, []string) ([]store.SimilarResult, error) {
	return nil, nil
}
func (m *miniStore) CreateEdge(context.Context, types.CreateEdgeInput) (types.MemoryEdge, error) {
	return types.MemoryEdge{}, nil
}
func (m *miniStore) GetEdges(context.Context, uuid.UUID, []types.EdgeKind) ([]types.MemoryEdge, error) {
	return nil, nil
}
func (m *miniStore) DecayAll(context.Context, uuid.UUID, time.Time, store.DecayFn) (int, error) {
	return 0, nil
}
func (m *miniStore) BulkUpdateWeight(context.Context, uuid.UUID, []store.WeightUpdate) (int, error) {
	return 0, nil
}
func (m *miniStore) FindUnconnectedSimilarPairs(context.Context, uuid.UUID, float64, int) ([]store.SimilarPair, error) {
	return nil, nil
}
func (m *miniStore) ListMemoryUserIDs(context.Context) ([]uuid.UUID, error) { return nil, nil }
func (m *miniStore) WithTx(context.Context) (store.IMemoryStore, error)     { return m, nil }
func (m *miniStore) CommitTx(context.Context) error                         { return nil }
func (m *miniStore) RollbackTx(context.Context) error                       { return nil }

func seedNode(m *miniStore, id uuid.UUID, content string) types.MemoryNode {
	n := types.MemoryNode{
		ID: id, UserID: uuid.New(), Content: content, Summary: content,
		ContentType: types.ContentTypeFact, Source: types.SourceHumanInput,
		State: types.NodeStateActive, Weight: 0.5, Version: 1,
		CreatedAt: time.Now().Add(-time.Hour),
	}
	m.nodes[id] = n
	return n
}

func TestMarkUnstable_OpensWindow(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"unstableWindowSeconds": 60})
	m := newMini()
	id := uuid.New()
	seedNode(m, id, "User is allergic to shellfish")

	now := time.Now()
	if err := p.MarkUnstable(context.Background(), m, id, now); err != nil {
		t.Fatal(err)
	}
	n := m.nodes[id]
	if n.UnstableUntil == nil {
		t.Fatal("expected unstable_until set")
	}
	if !p.IsUnstable(n, now.Add(30*time.Second)) {
		t.Fatal("expected node unstable within window")
	}
	if p.IsUnstable(n, now.Add(2*time.Minute)) {
		t.Fatal("expected node stable past window")
	}
}

func TestApplyCorrection_RecordsHistoryAndUpdates(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"unstableWindowSeconds": 3600})
	m := newMini()
	id := uuid.New()
	seedNode(m, id, "User is allergic to shellfish")

	now := time.Now()
	_ = p.MarkUnstable(context.Background(), m, id, now)

	updated, err := p.ApplyCorrection(context.Background(), m, CorrectionInput{
		NodeID:        id,
		NewContent:    "User is allergic to peanuts (not shellfish)",
		NewSummary:    "allergic to peanuts",
		ChangeSummary: "user corrected the allergen",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "User is allergic to peanuts (not shellfish)" {
		t.Fatalf("content not updated: %q", updated.Content)
	}
	if updated.UnstableUntil != nil {
		t.Fatal("expected unstable window closed after correction")
	}
	if updated.Source != types.SourceUserCorrection {
		t.Fatalf("expected source=user_correction, got %s", updated.Source)
	}

	if len(m.history) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(m.history))
	}
	h := m.history[0]
	if h.Content != "User is allergic to shellfish" {
		t.Fatalf("history should snapshot OLD content, got %q", h.Content)
	}
	if h.SupersededByEvent != "correction" {
		t.Fatalf("expected event=correction, got %s", h.SupersededByEvent)
	}
}

func TestApplyCorrection_RejectedOutsideWindow(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"unstableWindowSeconds": 60})
	m := newMini()
	id := uuid.New()
	seedNode(m, id, "old content")
	// No MarkUnstable → window never opened.

	_, err := p.ApplyCorrection(context.Background(), m, CorrectionInput{
		NodeID:     id,
		NewContent: "new content",
	}, time.Now())
	if !errors.Is(err, ErrNotUnstable) {
		t.Fatalf("expected ErrNotUnstable, got %v", err)
	}
	if len(m.history) != 0 {
		t.Fatal("must not record history when rejecting")
	}
	if m.nodes[id].Content != "old content" {
		t.Fatal("must not mutate node when rejecting")
	}
}

func TestApplyCorrection_ForceWhenClosed(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"unstableWindowSeconds": 60, "forceWhenClosed": true})
	m := newMini()
	id := uuid.New()
	seedNode(m, id, "old content")

	updated, err := p.ApplyCorrection(context.Background(), m, CorrectionInput{
		NodeID: id, NewContent: "forced update",
	}, time.Now())
	if err != nil {
		t.Fatalf("force should succeed outside window, got %v", err)
	}
	if updated.Content != "forced update" {
		t.Fatalf("expected forced update, got %q", updated.Content)
	}
}

func TestApplyCorrection_NotFound(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	m := newMini()
	_, err := p.ApplyCorrection(context.Background(), m, CorrectionInput{
		NodeID: uuid.New(), NewContent: "x",
	}, time.Now())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestApplyCorrection_EmptyContentRejected(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"unstableWindowSeconds": 3600})
	m := newMini()
	id := uuid.New()
	seedNode(m, id, "old")
	_ = p.MarkUnstable(context.Background(), m, id, time.Now())

	_, err := p.ApplyCorrection(context.Background(), m, CorrectionInput{
		NodeID: id, NewContent: "",
	}, time.Now())
	if !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}
