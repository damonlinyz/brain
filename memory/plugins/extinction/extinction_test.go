package extinction

import (
	"context"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

type miniStore struct{ nodes map[uuid.UUID]types.MemoryNode }

func newMini() *miniStore { return &miniStore{nodes: map[uuid.UUID]types.MemoryNode{}} }

func (m *miniStore) ListNodes(_ context.Context, f store.SearchFilter) (store.SearchResults, error) {
	var items []types.MemoryNode
	for _, n := range m.nodes {
		for _, s := range f.States {
			if n.State == s && n.UserID == f.UserID {
				items = append(items, n)
			}
		}
	}
	return store.SearchResults{Items: items}, nil
}
func (m *miniStore) UpdateNode(_ context.Context, id uuid.UUID, ver int, ms ...store.NodeMutator) (types.MemoryNode, error) {
	n := m.nodes[id]
	for _, f := range ms { f(&n) }
	n.Version++
	m.nodes[id] = n
	return n, nil
}
func (m *miniStore) GetNode(_ context.Context, id uuid.UUID) (types.MemoryNode, error) {
	n, ok := m.nodes[id]
	if !ok { return types.MemoryNode{}, store.ErrNotFound }
	return n, nil
}
// stubs
func (m *miniStore) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) { return types.MemoryNode{}, nil }
func (m *miniStore) SoftDelete(context.Context, uuid.UUID) error { return nil }
func (m *miniStore) SearchSimilar(context.Context, store.SimilarQuery) ([]store.SimilarResult, error) { return nil, nil }
func (m *miniStore) SearchByKeywords(context.Context, uuid.UUID, []string, int) ([]store.SimilarResult, error) { return nil, nil }
func (m *miniStore) SearchHybrid(context.Context, store.SimilarQuery, []string) ([]store.SimilarResult, error) { return nil, nil }
func (m *miniStore) CreateEdge(context.Context, types.CreateEdgeInput) (types.MemoryEdge, error) { return types.MemoryEdge{}, nil }
func (m *miniStore) GetEdges(context.Context, uuid.UUID, []types.EdgeKind) ([]types.MemoryEdge, error) { return nil, nil }
func (m *miniStore) DecayAll(context.Context, uuid.UUID, time.Time, store.DecayFn) (int, error) { return 0, nil }
func (m *miniStore) BulkUpdateWeight(context.Context, uuid.UUID, []store.WeightUpdate) (int, error) { return 0, nil }
func (m *miniStore) FindUnconnectedSimilarPairs(context.Context, uuid.UUID, float64, int) ([]store.SimilarPair, error) { return nil, nil }
func (m *miniStore) ListMemoryUserIDs(context.Context) ([]uuid.UUID, error) { return nil, nil }
func (m *miniStore) RecordHistory(context.Context, types.MemoryNodeHistory) error { return nil }
func (m *miniStore) WithTx(context.Context) (store.IMemoryStore, error) { return m, nil }
func (m *miniStore) CommitTx(context.Context) error { return nil }
func (m *miniStore) RollbackTx(context.Context) error { return nil }

func TestProcess_MarksLowWeightExtinct(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"extinctThreshold": 0.05})
	m := newMini()
	uid := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	m.nodes[id1] = types.MemoryNode{ID: id1, UserID: uid, State: types.NodeStateActive, Weight: 0.01, Version: 1}
	m.nodes[id2] = types.MemoryNode{ID: id2, UserID: uid, State: types.NodeStateActive, Weight: 0.5, Version: 1}

	n, err := p.Process(context.Background(), m, uid)
	if err != nil { t.Fatal(err) }
	if n != 1 { t.Fatalf("expected 1 extinct, got %d", n) }
	if m.nodes[id1].State != types.NodeStateExtinct { t.Fatal("id1 should be extinct") }
	if m.nodes[id2].State != types.NodeStateActive { t.Fatal("id2 should stay active") }
}

func TestRevive_ResurrectsExtinct(t *testing.T) {
	p := New()
	m := newMini()
	id := uuid.New()
	m.nodes[id] = types.MemoryNode{ID: id, UserID: uuid.New(), State: types.NodeStateExtinct, Weight: 0.01, Version: 1}

	node, err := p.Revive(context.Background(), m, id)
	if err != nil { t.Fatal(err) }
	if node.State != types.NodeStateActive { t.Fatal("should be active") }
	if node.Weight != 0.3 { t.Fatalf("weight should reset, got %f", node.Weight) }
}

func TestRevive_NoopOnActive(t *testing.T) {
	p := New()
	m := newMini()
	id := uuid.New()
	m.nodes[id] = types.MemoryNode{ID: id, State: types.NodeStateActive, Weight: 0.5, Version: 1}

	_, err := p.Revive(context.Background(), m, id)
	if err != nil { t.Fatal(err) }
	if m.nodes[id].Weight != 0.5 { t.Fatal("active node should not change") }
}
