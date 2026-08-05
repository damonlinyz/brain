package trigger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

type fakeStore struct {
	similarResults []store.SimilarResult
	hybridResults  []store.SimilarResult
	capturedQuery  store.SimilarQuery
	capturedKeys   []string
	similarCalls   int
	hybridCalls    int
	err            error
}

func (f *fakeStore) SearchSimilar(ctx context.Context, q store.SimilarQuery) ([]store.SimilarResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.similarCalls++
	f.capturedQuery = q
	return f.similarResults, nil
}
func (f *fakeStore) SearchHybrid(ctx context.Context, q store.SimilarQuery, keys []string) ([]store.SimilarResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.hybridCalls++
	f.capturedQuery = q
	f.capturedKeys = keys
	return f.hybridResults, nil
}

// stubs
func (f *fakeStore) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) GetNode(context.Context, uuid.UUID) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
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
func (f *fakeStore) WithTx(context.Context) (store.IMemoryStore, error) { return f, nil }
func (f *fakeStore) CommitTx(context.Context) error                    { return nil }
func (f *fakeStore) RollbackTx(context.Context) error                  { return nil }

func res(sim float64, summary string) store.SimilarResult {
	return store.SimilarResult{Node: types.MemoryNode{ID: uuid.New(), Summary: summary}, Sim: sim}
}

func TestRecall_FallsBackToSimilar_WhenNoKeywords(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	s := &fakeStore{similarResults: []store.SimilarResult{
		res(0.9, "top"),
		res(0.5, "mid"),
	}}
	out, err := p.Recall(context.Background(), s, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1, 0.2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.hybridCalls != 0 || s.similarCalls != 1 {
		t.Fatalf("expected only SearchSimilar called, got sim=%d hybrid=%d", s.similarCalls, s.hybridCalls)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out))
	}
}

func TestRecall_UsesHybrid_WhenKeywordsProvided(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	s := &fakeStore{hybridResults: []store.SimilarResult{
		res(0.95, "kw-match"),
	}}
	out, err := p.Recall(context.Background(), s, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
		Keywords:  []string{"go", "concurrency"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.hybridCalls != 1 || s.similarCalls != 0 {
		t.Fatalf("expected only SearchHybrid called, got sim=%d hybrid=%d", s.similarCalls, s.hybridCalls)
	}
	if s.capturedKeys == nil {
		t.Fatal("expected keywords forwarded to store")
	}
	if len(out) != 1 || out[0].Sim != 0.95 {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestRecall_DropsBelowScoreFloor(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"scoreFloor": 0.4})

	s := &fakeStore{similarResults: []store.SimilarResult{
		res(0.9, "keep"),
		res(0.2, "drop"),
		res(0.5, "keep2"),
	}}
	out, _ := p.Recall(context.Background(), s, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 above floor, got %d", len(out))
	}
	for _, r := range out {
		if r.Sim < 0.4 {
			t.Fatalf("floor leak: %f", r.Sim)
		}
	}
}

func TestRecall_SortedByScoreDesc(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	s := &fakeStore{similarResults: []store.SimilarResult{
		res(0.5, "mid"),
		res(0.9, "top"),
		res(0.7, "high"),
	}}
	out, _ := p.Recall(context.Background(), s, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if len(out) != 3 || out[0].Sim != 0.9 || out[2].Sim != 0.5 {
		t.Fatalf("expected sorted desc, got %+v", out)
	}
}

func TestRecall_RespectsTopKOverride(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"topK": 10})

	s := &fakeStore{similarResults: []store.SimilarResult{
		res(0.9, "a"),
		res(0.8, "b"),
		res(0.7, "c"),
		res(0.6, "d"),
	}}
	out, _ := p.Recall(context.Background(), s, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
		TopK:      2,
	})
	if len(out) != 2 {
		t.Fatalf("expected topK=2, got %d", len(out))
	}
}

func TestRecall_OverFetchesFromStore(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"topK": 5})

	s := &fakeStore{similarResults: []store.SimilarResult{}}
	_, _ = p.Recall(context.Background(), s, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if s.capturedQuery.TopK != 10 {
		t.Fatalf("expected store to over-fetch topK*2=10, got %d", s.capturedQuery.TopK)
	}
}

func TestRecall_NilStoreOrEmptyEmbedding_NoOp(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	out, err := p.Recall(context.Background(), nil, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if err != nil || out != nil {
		t.Fatalf("expected nil-out for nil store")
	}

	out2, _ := p.Recall(context.Background(), &fakeStore{}, RecallInput{
		UserID: uuid.New(),
	})
	if out2 != nil {
		t.Fatalf("expected nil-out for empty embedding")
	}
}

func TestRecall_PropagatesStoreError(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	s := &fakeStore{err: errors.New("store down")}
	_, err := p.Recall(context.Background(), s, RecallInput{
		UserID:    uuid.New(),
		Embedding: []float32{0.1},
	})
	if err == nil || err.Error() != "store down" {
		t.Fatalf("expected store error, got %v", err)
	}
}

func TestInit_KeywordWeightClampedAndComplementary(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"keywordWeight": 2.0}) // out of range

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.keywordWeight != Defaults.KeywordWeight {
		t.Fatalf("expected out-of-range to fall back to default, got %f", p.keywordWeight)
	}
	if p.vectorWeight != 1.0-p.keywordWeight {
		t.Fatalf("expected vector = 1 - keyword, got kw=%f vw=%f", p.keywordWeight, p.vectorWeight)
	}
}

func TestInit_VectorWeightPinsComplement(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"vectorWeight": 0.8})

	p.mu.RLock()
	defer p.mu.RUnlock()
	// Float arithmetic on complements can introduce small drift, compare by tolerance.
	if abs(p.vectorWeight-0.8) > 1e-9 || abs(p.keywordWeight-0.2) > 1e-9 {
		t.Fatalf("expected vector≈0.8 keyword≈0.2, got vw=%f kw=%f", p.vectorWeight, p.keywordWeight)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
