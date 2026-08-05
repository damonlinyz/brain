package patternsep

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// fakeStore lets us drive Evaluate without a database.
type fakeStore struct {
	results []store.SimilarResult
	err     error
}

func (f *fakeStore) SearchSimilar(ctx context.Context, q store.SimilarQuery) ([]store.SimilarResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

// stub the rest of the interface so *fakeStore satisfies store.IMemoryStore.
func (f *fakeStore) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) GetNode(context.Context, uuid.UUID) (types.MemoryNode, error) {
	return types.MemoryNode{}, store.ErrNotFound
}
func (f *fakeStore) UpdateNode(context.Context, uuid.UUID, int, ...store.NodeMutator) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) SoftDelete(context.Context, uuid.UUID) error { return nil }
func (f *fakeStore) RecordHistory(context.Context, types.MemoryNodeHistory) error { return nil }
func (f *fakeStore) ListNodes(context.Context, store.SearchFilter) (store.SearchResults, error) {
	return store.SearchResults{}, nil
}
func (f *fakeStore) SearchByKeywords(context.Context, uuid.UUID, []string, int) ([]store.SimilarResult, error) {
	return nil, nil
}
func (f *fakeStore) SearchHybrid(context.Context, store.SimilarQuery, []string) ([]store.SimilarResult, error) {
	return nil, nil
}
func (f *fakeStore) CreateEdge(context.Context, types.CreateEdgeInput) (types.MemoryEdge, error) {
	return types.MemoryEdge{}, nil
}
func (f *fakeStore) GetEdges(context.Context, uuid.UUID, []types.EdgeKind) ([]types.MemoryEdge, error) {
	return nil, nil
}
func (f *fakeStore) DecayAll(context.Context, uuid.UUID, time.Time, store.DecayFn) (int, error) {
	return 0, nil
}
func (f *fakeStore) BulkUpdateWeight(context.Context, uuid.UUID, []store.WeightUpdate) (int, error) {
	return 0, nil
}
func (f *fakeStore) FindUnconnectedSimilarPairs(context.Context, uuid.UUID, float64, int) ([]store.SimilarPair, error) {
	return nil, nil
}
func (f *fakeStore) ListMemoryUserIDs(context.Context) ([]uuid.UUID, error) { return nil, nil }
func (f *fakeStore) FindPath(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) ([]store.GraphNeighbor, error) { return nil, nil }
func (f *fakeStore) ExpandSubgraph(context.Context, uuid.UUID, []uuid.UUID, int) ([]store.GraphNeighbor, error) { return nil, nil }
func (f *fakeStore) WithTx(context.Context) (store.IMemoryStore, error) { return f, nil }
func (f *fakeStore) CommitTx(context.Context) error                    { return nil }
func (f *fakeStore) RollbackTx(context.Context) error                  { return nil }
func (f *fakeStore) GetGraphNeighbors(context.Context, uuid.UUID, []uuid.UUID, int) ([]store.GraphNeighbor, error) { return nil, nil }

func mkNode(id string, summary string) types.MemoryNode {
	return types.MemoryNode{ID: uuid.New(), Summary: summary}
}

func similar(sim float64, summary string) store.SimilarResult {
	return store.SimilarResult{Node: mkNode("x", summary), Sim: sim}
}

func TestEvaluate_NewWhenBelowLinkThreshold(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"mergeSim": 0.9, "linkSim": 0.7, "minSim": 0.5})

	s := &fakeStore{results: []store.SimilarResult{similar(0.55, "weak match")}}
	out, err := p.Evaluate(context.Background(), s, EvaluateInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1, 0.2, 0.3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != DecisionNew {
		t.Fatalf("expected New, got %s", out.Decision)
	}
	if out.ExistingNode != nil {
		t.Fatal("expected ExistingNode nil for New decision")
	}
}

func TestEvaluate_LinkWhenBetweenThresholds(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"mergeSim": 0.9, "linkSim": 0.7})

	s := &fakeStore{results: []store.SimilarResult{
		similar(0.82, "related concept"),
		similar(0.65, "weaker"),
	}}
	out, err := p.Evaluate(context.Background(), s, EvaluateInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != DecisionLink {
		t.Fatalf("expected Link at 0.82, got %s (sim=%f)", out.Decision, out.Similarity)
	}
	if out.ExistingNode == nil || out.ExistingNode.Summary != "related concept" {
		t.Fatalf("expected top match pointer, got %+v", out.ExistingNode)
	}
	if len(out.OtherCandidates) != 1 {
		t.Fatalf("expected 1 other candidate, got %d", len(out.OtherCandidates))
	}
}

func TestEvaluate_MergeWhenAboveMergeThreshold(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"mergeSim": 0.9, "linkSim": 0.7})

	s := &fakeStore{results: []store.SimilarResult{
		similar(0.95, "near-duplicate"),
	}}
	out, err := p.Evaluate(context.Background(), s, EvaluateInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != DecisionMerge {
		t.Fatalf("expected Merge at 0.95, got %s", out.Decision)
	}
}

func TestEvaluate_NewWhenStoreEmpty(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	s := &fakeStore{results: nil}
	out, err := p.Evaluate(context.Background(), s, EvaluateInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != DecisionNew {
		t.Fatalf("expected New on empty store, got %s", out.Decision)
	}
	if out.Similarity != 0 || out.ExistingNode != nil {
		t.Fatal("expected zero similarity and nil node for empty store")
	}
}

func TestEvaluate_NilStoreOrEmbedding_NoOp(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	out, err := p.Evaluate(context.Background(), nil, EvaluateInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != DecisionNew {
		t.Fatalf("expected New for nil store, got %s", out.Decision)
	}

	out2, _ := p.Evaluate(context.Background(), &fakeStore{}, EvaluateInput{
		UserID:    uuid.New(),
		Embedding: nil,
	})
	if out2.Decision != DecisionNew {
		t.Fatalf("expected New for empty embedding, got %s", out2.Decision)
	}
}

func TestEvaluate_PropagatesStoreError(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	s := &fakeStore{err: errors.New("boom")}
	_, err := p.Evaluate(context.Background(), s, EvaluateInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom error, got %v", err)
	}
}

func TestInit_NormalizesThresholdOrder(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"mergeSim": 0.5, "linkSim": 0.9}) // inverted

	// After Init the plugin must keep mergeSim >= linkSim so the classifier has
	// a sensible ordering. We verify by checking the merged threshold behaves
	// as linkSim (the larger of the two) — anything below 0.9 falls through to New.
	p.mu.RLock()
	if p.mergeSim < p.linkSim {
		t.Fatalf("mergeSim %f must be >= linkSim %f after Init", p.mergeSim, p.linkSim)
	}
	p.mu.RUnlock()
}

func TestInit_CustomTopK(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"topK": 3})

	// SearchSimilar just echoes whatever fakeStore returns; we just verify no panic.
	s := &fakeStore{results: []store.SimilarResult{
		similar(0.95, "a"),
		similar(0.94, "b"),
		similar(0.93, "c"),
	}}
	out, _ := p.Evaluate(context.Background(), s, EvaluateInput{
		UserID: uuid.New(), Embedding: []float32{0.1},
	})
	if out.Decision != DecisionMerge {
		t.Fatalf("expected Merge, got %s", out.Decision)
	}
	if len(out.OtherCandidates) != 2 {
		t.Fatalf("expected 2 trailing candidates, got %d", len(out.OtherCandidates))
	}
}
