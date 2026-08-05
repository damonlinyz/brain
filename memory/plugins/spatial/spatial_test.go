package spatial

import (
	"context"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

type mini struct{ nodes map[uuid.UUID]types.MemoryNode }

func (m *mini) GetNode(_ context.Context, id uuid.UUID) (types.MemoryNode, error) {
	n, ok := m.nodes[id]; if !ok { return types.MemoryNode{}, store.ErrNotFound }; return n, nil
}
func (m *mini) UpdateNode(_ context.Context, id uuid.UUID, ver int, ms ...store.NodeMutator) (types.MemoryNode, error) {
	n := m.nodes[id]; for _, f := range ms { f(&n) }; n.Version++; m.nodes[id] = n; return n, nil
}
func (m *mini) ListNodes(_ context.Context, f store.SearchFilter) (store.SearchResults, error) {
	var items []types.MemoryNode
	for _, n := range m.nodes {
		if n.UserID == f.UserID && f.SessionID != nil && n.SessionID != nil && *n.SessionID == *f.SessionID {
			items = append(items, n)
		}
	}
	return store.SearchResults{Items: items}, nil
}
func (m *mini) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) { return types.MemoryNode{}, nil }
func (m *mini) SoftDelete(context.Context, uuid.UUID) error { return nil }
func (m *mini) SearchSimilar(context.Context, store.SimilarQuery) ([]store.SimilarResult, error) { return nil, nil }
func (m *mini) SearchByKeywords(context.Context, uuid.UUID, []string, int) ([]store.SimilarResult, error) { return nil, nil }
func (m *mini) SearchHybrid(context.Context, store.SimilarQuery, []string) ([]store.SimilarResult, error) { return nil, nil }
func (m *mini) CreateEdge(context.Context, types.CreateEdgeInput) (types.MemoryEdge, error) { return types.MemoryEdge{}, nil }
func (m *mini) GetEdges(context.Context, uuid.UUID, []types.EdgeKind) ([]types.MemoryEdge, error) { return nil, nil }
func (m *mini) DecayAll(context.Context, uuid.UUID, time.Time, store.DecayFn) (int, error) { return 0, nil }
func (m *mini) BulkUpdateWeight(context.Context, uuid.UUID, []store.WeightUpdate) (int, error) { return 0, nil }
func (m *mini) FindUnconnectedSimilarPairs(context.Context, uuid.UUID, float64, int) ([]store.SimilarPair, error) { return nil, nil }
func (m *mini) ListMemoryUserIDs(context.Context) ([]uuid.UUID, error) { return nil, nil }
func (m *mini) RecordHistory(context.Context, types.MemoryNodeHistory) error { return nil }
func (m *mini) WithTx(context.Context) (store.IMemoryStore, error) { return m, nil }
func (m *mini) CommitTx(context.Context) error { return nil }
func (m *mini) RollbackTx(context.Context) error { return nil }

func TestBindSession(t *testing.T) {
	p := New(); m := &mini{nodes: map[uuid.UUID]types.MemoryNode{}}
	uid := uuid.New(); sid := uuid.New(); nid := uuid.New()
	m.nodes[nid] = types.MemoryNode{ID: nid, UserID: uid, Version: 1}
	if err := p.BindSession(context.Background(), m, nid, sid); err != nil { t.Fatal(err) }
	if m.nodes[nid].SessionID == nil || *m.nodes[nid].SessionID != sid { t.Fatal("session not bound") }
}

func TestListBySession(t *testing.T) {
	p := New(); m := &mini{nodes: map[uuid.UUID]types.MemoryNode{}}
	uid := uuid.New(); sid := uuid.New()
	n1 := uuid.New(); n2 := uuid.New()
	m.nodes[n1] = types.MemoryNode{ID: n1, UserID: uid, SessionID: &sid}
	m.nodes[n2] = types.MemoryNode{ID: n2, UserID: uid, SessionID: &sid}
	m.nodes[uuid.New()] = types.MemoryNode{ID: uuid.New(), UserID: uid} // no session
	items, err := p.ListBySession(context.Background(), m, uid, sid)
	if err != nil { t.Fatal(err) }
	if len(items) != 2 { t.Fatalf("expected 2, got %d", len(items)) }
}
