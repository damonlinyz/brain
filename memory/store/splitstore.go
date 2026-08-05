package store

import (
	"context"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// SplitStore composes two IMemoryStores — a node store and a graph store — into
// one. Node operations (CRUD, vectors, decay, weights) go to the node store;
// graph operations (edges, neighbours, paths, subgraphs) go to the graph store.
//
// This is the architecture gate: a future Memgraph / Neo4j / Apache AGE backend
// only needs to implement the graph subset of IMemoryStore, then
// SplitStore{nodes: PGStore, graph: MemgraphAdapter} works without a single
// caller-side change.
type SplitStore struct {
	nodes IMemoryStore
	graph IMemoryStore
}

// NewSplitStore creates a composed store. Both backends may be the same (PG-only
// with SplitStore(nil pgstore, pgraph)——the Hub sees no difference.
func NewSplitStore(nodes, graph IMemoryStore) *SplitStore {
	return &SplitStore{nodes: nodes, graph: graph}
}

// --- graph ops → graph store ---

func (s *SplitStore) CreateEdge(ctx context.Context, in types.CreateEdgeInput) (types.MemoryEdge, error) {
	return s.graph.CreateEdge(ctx, in)
}
func (s *SplitStore) GetEdges(ctx context.Context, nodeID uuid.UUID, kinds []types.EdgeKind) ([]types.MemoryEdge, error) {
	return s.graph.GetEdges(ctx, nodeID, kinds)
}
func (s *SplitStore) GetGraphNeighbors(ctx context.Context, userID uuid.UUID, seedIDs []uuid.UUID, depth int) ([]GraphNeighbor, error) {
	return s.graph.GetGraphNeighbors(ctx, userID, seedIDs, depth)
}
func (s *SplitStore) FindPath(ctx context.Context, userID uuid.UUID, from, to uuid.UUID, maxDepth int) ([]GraphNeighbor, error) {
	return s.graph.FindPath(ctx, userID, from, to, maxDepth)
}
func (s *SplitStore) ExpandSubgraph(ctx context.Context, userID uuid.UUID, seedIDs []uuid.UUID, depth int) ([]GraphNeighbor, error) {
	return s.graph.ExpandSubgraph(ctx, userID, seedIDs, depth)
}
func (s *SplitStore) FindUnconnectedSimilarPairs(ctx context.Context, userID uuid.UUID, threshold float64, limit int) ([]SimilarPair, error) {
	return s.graph.FindUnconnectedSimilarPairs(ctx, userID, threshold, limit)
}

// --- node ops → node store ---

func (s *SplitStore) CreateNode(ctx context.Context, in types.CreateNodeInput) (types.MemoryNode, error) {
	return s.nodes.CreateNode(ctx, in)
}
func (s *SplitStore) GetNode(ctx context.Context, nodeID uuid.UUID) (types.MemoryNode, error) {
	return s.nodes.GetNode(ctx, nodeID)
}
func (s *SplitStore) UpdateNode(ctx context.Context, nodeID uuid.UUID, ver int, muts ...NodeMutator) (types.MemoryNode, error) {
	return s.nodes.UpdateNode(ctx, nodeID, ver, muts...)
}
func (s *SplitStore) SoftDelete(ctx context.Context, nodeID uuid.UUID) error { return s.nodes.SoftDelete(ctx, nodeID) }
func (s *SplitStore) ListNodes(ctx context.Context, f SearchFilter) (SearchResults, error) {
	return s.nodes.ListNodes(ctx, f)
}
func (s *SplitStore) RecordHistory(ctx context.Context, h types.MemoryNodeHistory) error {
	return s.nodes.RecordHistory(ctx, h)
}
func (s *SplitStore) SearchSimilar(ctx context.Context, q SimilarQuery) ([]SimilarResult, error) {
	return s.nodes.SearchSimilar(ctx, q)
}
func (s *SplitStore) SearchByKeywords(ctx context.Context, userID uuid.UUID, kw []string, limit int) ([]SimilarResult, error) {
	return s.nodes.SearchByKeywords(ctx, userID, kw, limit)
}
func (s *SplitStore) SearchHybrid(ctx context.Context, q SimilarQuery, kw []string) ([]SimilarResult, error) {
	return s.nodes.SearchHybrid(ctx, q, kw)
}
func (s *SplitStore) DecayAll(ctx context.Context, userID uuid.UUID, before time.Time, fn DecayFn) (int, error) {
	return s.nodes.DecayAll(ctx, userID, before, fn)
}
func (s *SplitStore) BulkUpdateWeight(ctx context.Context, userID uuid.UUID, updates []WeightUpdate) (int, error) {
	return s.nodes.BulkUpdateWeight(ctx, userID, updates)
}
func (s *SplitStore) ListMemoryUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	return s.nodes.ListMemoryUserIDs(ctx)
}
func (s *SplitStore) WithTx(ctx context.Context) (IMemoryStore, error) {
	ns, err := s.nodes.WithTx(ctx)
	if err != nil { return nil, err }
	return &SplitStore{nodes: ns, graph: s.graph}, nil
}
func (s *SplitStore) CommitTx(ctx context.Context) error  { return s.nodes.CommitTx(ctx) }
func (s *SplitStore) RollbackTx(ctx context.Context) error { return s.nodes.RollbackTx(ctx) }
