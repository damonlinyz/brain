package store

import (
	"context"
	"errors"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Store errors. Use errors.Is for checks; PGStore wraps pgx errors onto these.
var (
	ErrNotFound               = errors.New("memory: not found")
	ErrOptimisticLockConflict = errors.New("memory: optimistic lock conflict")
	ErrConflictRetry          = errors.New("memory: conflict retry exhausted")
	ErrInvalidInput           = errors.New("memory: invalid input")
)

// SearchFilter restricts ListNodes results.
type SearchFilter struct {
	UserID    uuid.UUID
	TenantID  *uuid.UUID
	Types     []types.MemoryType
	States    []types.NodeState
	Sources   []types.Source
	Since     *time.Time
	Until     *time.Time
	SessionID *uuid.UUID
	Limit     int
	Cursor    string // opaque, currently the last seen id
}

// SearchResults wraps a ListNodes page.
type SearchResults struct {
	Items      []types.MemoryNode
	NextCursor string
}

// SimilarQuery is the input to vector / hybrid search.
type SimilarQuery struct {
	UserID     uuid.UUID
	TenantID   *uuid.UUID
	Embedding  []float32
	TopK       int
	MinSim     float64
	TypeFilter []types.MemoryType
}

// SimilarResult pairs a node with its similarity score.
type SimilarResult struct {
	Node types.MemoryNode
	Sim  float64
}

// WeightUpdate is a single delta applied by BulkUpdateWeight.
type WeightUpdate struct {
	NodeID uuid.UUID
	Delta  float64
}

// SimilarPair is FindUnconnectedSimilarPairs output (used by Consolidation).
type SimilarPair struct {
	A, B types.MemoryNode
	Sim  float64
}

// NodeMutator mutates a node in-place during UpdateNode.
type NodeMutator func(n *types.MemoryNode)

// DecayFn computes a new weight from the current node (used by DecayAll).
type DecayFn func(n types.MemoryNode) float64

// IMemoryStore is the persistence interface every plugin talks to.
// PGStore is the only implementation for MVP; Plan 2 adds DGraph-backed graph methods.
type IMemoryStore interface {
	// Node CRUD
	CreateNode(ctx context.Context, in types.CreateNodeInput) (types.MemoryNode, error)
	GetNode(ctx context.Context, nodeID uuid.UUID) (types.MemoryNode, error)
	UpdateNode(ctx context.Context, nodeID uuid.UUID, expectedVersion int, mutators ...NodeMutator) (types.MemoryNode, error)
	SoftDelete(ctx context.Context, nodeID uuid.UUID) error
	ListNodes(ctx context.Context, filter SearchFilter) (SearchResults, error)
	// RecordHistory appends a superseded-version row to memory_node_history
	// (used by Reconsolidation when a node is corrected).
	RecordHistory(ctx context.Context, h types.MemoryNodeHistory) error

	// Search
	SearchSimilar(ctx context.Context, q SimilarQuery) ([]SimilarResult, error)
	SearchByKeywords(ctx context.Context, userID uuid.UUID, keywords []string, limit int) ([]SimilarResult, error)
	SearchHybrid(ctx context.Context, q SimilarQuery, keywords []string) ([]SimilarResult, error)

	// Edges
	CreateEdge(ctx context.Context, in types.CreateEdgeInput) (types.MemoryEdge, error)
	GetEdges(ctx context.Context, nodeID uuid.UUID, kinds []types.EdgeKind) ([]types.MemoryEdge, error)

	// Bulk operations (Consolidation, Forgetting)
	DecayAll(ctx context.Context, userID uuid.UUID, before time.Time, fn DecayFn) (int, error)
	BulkUpdateWeight(ctx context.Context, userID uuid.UUID, updates []WeightUpdate) (int, error)
	FindUnconnectedSimilarPairs(ctx context.Context, userID uuid.UUID, threshold float64, limit int) ([]SimilarPair, error)
	// ListMemoryUserIDs returns every distinct user that owns at least one
	// non-deleted node. Used by the Consolidation sweep to fan out per-user.
	ListMemoryUserIDs(ctx context.Context) ([]uuid.UUID, error)

	// Transactions
	WithTx(ctx context.Context) (IMemoryStore, error)
	CommitTx(ctx context.Context) error
	RollbackTx(ctx context.Context) error
}
