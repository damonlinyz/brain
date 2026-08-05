package forgetting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

func mkNode(state types.NodeState, weight float64, lastTouch time.Time) types.MemoryNode {
	return types.MemoryNode{
		ID:          uuid.New(),
		State:       state,
		Weight:      weight,
		LastAccessAt: lastTouch,
		Version:     1,
	}
}

func TestClassify_KeepWhenWeightAboveSuppress(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"suppressThreshold": 0.1})

	now := time.Now()
	node := mkNode(types.NodeStateActive, 0.5, now)
	if got := p.Classify(node, now); got != StageKeep {
		t.Fatalf("expected Keep, got %s", got)
	}
}

func TestClassify_SuppressWhenBelowSuppressFloor(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"suppressThreshold": 0.1, "archiveThreshold": 0.05, "confirmTtlSeconds": 1})

	now := time.Now()
	node := mkNode(types.NodeStateActive, 0.08, now) // recently touched
	if got := p.Classify(node, now); got != StageSuppress {
		t.Fatalf("expected Suppress on recent dip below floor, got %s", got)
	}
}

func TestClassify_ArchiveAfterConfirmTTL(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"suppressThreshold": 0.1, "archiveThreshold": 0.05, "confirmTtlSeconds": 1})

	now := time.Now()
	old := now.Add(-10 * time.Second)
	node := mkNode(types.NodeStateActive, 0.04, old) // long cold + below archive
	if got := p.Classify(node, now); got != StageArchive {
		t.Fatalf("expected Archive after TTL, got %s", got)
	}
}

func TestClassify_ExtinctWhenWeightNearZero(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"suppressThreshold": 0.1, "archiveThreshold": 0.05, "confirmTtlSeconds": 1, "extinctFloor": 0.01})

	now := time.Now()
	old := now.Add(-10 * time.Second)
	node := mkNode(types.NodeStateArchived, 0.005, old)
	if got := p.Classify(node, now); got != StageExtinct {
		t.Fatalf("expected Extinct, got %s", got)
	}
}

func TestClassify_ReinforcementBeatsDecay(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"suppressThreshold": 0.1, "archiveThreshold": 0.05, "confirmTtlSeconds": 1})

	now := time.Now()
	node := mkNode(types.NodeStateSuppressed, 0.5, now.Add(-1*time.Hour)) // weight high despite old age
	if got := p.Classify(node, now); got != StageKeep {
		t.Fatalf("high-weight suppressed node should return Keep, got %s", got)
	}
}

func TestApplyTransition_SuppressChangesState(t *testing.T) {
	p := New()
	node := mkNode(types.NodeStateActive, 0.08, time.Now())
	if !p.ApplyTransition(&node, StageSuppress) {
		t.Fatal("expected mutation")
	}
	if node.State != types.NodeStateSuppressed {
		t.Fatalf("expected suppressed, got %s", node.State)
	}
}

func TestApplyTransition_NoOpReturnsFalse(t *testing.T) {
	p := New()
	node := mkNode(types.NodeStateActive, 0.5, time.Now())
	if p.ApplyTransition(&node, StageKeep) {
		t.Fatal("active node + Keep target should be no-op")
	}
}

func TestApplyTransition_NilSafe(t *testing.T) {
	p := New()
	if p.ApplyTransition(nil, StageSuppress) {
		t.Fatal("nil node should not report mutation")
	}
}

// fakeStore is a minimal recorder for Process tests.
type fakeStore struct {
	listed   []types.MemoryNode
	listErr  error
	deletes  []uuid.UUID
	updState map[uuid.UUID]types.NodeState
	updErr   error
}

func (f *fakeStore) ListNodes(ctx context.Context, filter store.SearchFilter) (store.SearchResults, error) {
	if f.listErr != nil {
		return store.SearchResults{}, f.listErr
	}
	return store.SearchResults{Items: f.listed}, nil
}
func (f *fakeStore) SoftDelete(ctx context.Context, id uuid.UUID) error {
	if f.updErr != nil {
		return f.updErr
	}
	f.deletes = append(f.deletes, id)
	return nil
}
func (f *fakeStore) RecordHistory(context.Context, types.MemoryNodeHistory) error { return nil }
func (f *fakeStore) UpdateNode(ctx context.Context, id uuid.UUID, ver int, ms ...store.NodeMutator) (types.MemoryNode, error) {
	if f.updErr != nil {
		return types.MemoryNode{}, f.updErr
	}
	if f.updState == nil {
		f.updState = map[uuid.UUID]types.NodeState{}
	}
	// Find the node, apply mutators, record new state.
	for i := range f.listed {
		if f.listed[i].ID == id {
			tmp := f.listed[i]
			for _, m := range ms {
				m(&tmp)
			}
			f.updState[id] = tmp.State
			f.listed[i].State = tmp.State
			return tmp, nil
		}
	}
	return types.MemoryNode{}, store.ErrNotFound
}
func (f *fakeStore) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) GetNode(context.Context, uuid.UUID) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) SearchSimilar(context.Context, store.SimilarQuery) ([]store.SimilarResult, error) {
	return nil, nil
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
func (f *fakeStore) WithTx(context.Context) (store.IMemoryStore, error) { return f, nil }
func (f *fakeStore) CommitTx(context.Context) error                    { return nil }
func (f *fakeStore) RollbackTx(context.Context) error                  { return nil }

func TestProcess_SuppressArchiveExtinct(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"suppressThreshold": 0.1, "archiveThreshold": 0.05, "confirmTtlSeconds": 1, "extinctFloor": 0.01})

	now := time.Now()
	old := now.Add(-10 * time.Second)

	suppressNode := mkNode(types.NodeStateActive, 0.08, now)        // recent dip → Suppress
	archiveNode := mkNode(types.NodeStateActive, 0.04, old)         // cold dip → Archive
	extinctNode := mkNode(types.NodeStateArchived, 0.005, old)      // cold + tiny → Extinct
	keepNode := mkNode(types.NodeStateActive, 0.6, old)             // strong → Keep

	s := &fakeStore{listed: []types.MemoryNode{suppressNode, archiveNode, extinctNode, keepNode}}

	counts, err := p.Process(context.Background(), s, uuid.New(), now)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Suppress != 1 || counts.Archive != 1 || counts.Extinct != 1 || counts.Keep != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
	if len(s.deletes) != 1 || s.deletes[0] != extinctNode.ID {
		t.Fatalf("expected extinct node soft-deleted, got %v", s.deletes)
	}
	if s.updState[suppressNode.ID] != types.NodeStateSuppressed {
		t.Errorf("suppress node not flipped: %+v", s.updState)
	}
	if s.updState[archiveNode.ID] != types.NodeStateArchived {
		t.Errorf("archive node not flipped: %+v", s.updState)
	}
}

func TestProcess_NilStoreSafe(t *testing.T) {
	p := New()
	counts, err := p.Process(context.Background(), nil, uuid.New(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total() != 0 {
		t.Fatalf("expected zero counts for nil store, got %+v", counts)
	}
}

func TestProcess_PropagatesListError(t *testing.T) {
	p := New()
	s := &fakeStore{listErr: errors.New("db down")}
	_, err := p.Process(context.Background(), s, uuid.New(), time.Now())
	if err == nil || err.Error() != "db down" {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestInit_NormalizesThresholdOrder(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"suppressThreshold": 0.01, "archiveThreshold": 0.5}) // inverted

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.suppressThreshold < p.archiveThreshold {
		t.Fatalf("suppress %f must be >= archive %f", p.suppressThreshold, p.archiveThreshold)
	}
}

func TestInit_ConfirmTTLAsString(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"confirmTTL": "10m"})

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.confirmTTL != 10*time.Minute {
		t.Fatalf("expected 10m ttl, got %s", p.confirmTTL)
	}
}

func TestTransitionCounts_Total(t *testing.T) {
	c := TransitionCounts{Keep: 2, Suppress: 1, Archive: 3, Extinct: 0}
	if c.Total() != 6 {
		t.Fatalf("total mismatch: %d", c.Total())
	}
}
