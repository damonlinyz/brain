package types

import (
	"time"

	"github.com/google/uuid"
)

// EdgeKind enumerates the relationships between memory nodes.
type EdgeKind string

const (
	EdgeKindRelated       EdgeKind = "related"
	EdgeKindDiscovered    EdgeKind = "discovered_link"
	EdgeKindTemporal      EdgeKind = "temporal"
	EdgeKindCausal        EdgeKind = "causal"
	EdgeKindContradicts   EdgeKind = "contradicts"
	EdgeKindSupersedes    EdgeKind = "supersedes"
	EdgeKindSimilarTo     EdgeKind = "similar_to"
)

// EdgeDiscoverer records which plugin produced this edge.
type EdgeDiscoverer string

const (
	DiscovererPatternSeparation EdgeDiscoverer = "pattern_separation"
	DiscovererConsolidation     EdgeDiscoverer = "consolidation"
	DiscovererExplicit          EdgeDiscoverer = "explicit"
	DiscovererInference         EdgeDiscoverer = "inference"
)

// MemoryEdge mirrors memory_edges rows.
type MemoryEdge struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	TenantID     *uuid.UUID
	FromNodeID   uuid.UUID
	ToNodeID     uuid.UUID
	EdgeType     EdgeKind
	Weight       float64
	DiscoveredBy EdgeDiscoverer
	Metadata     map[string]any

	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time

	DgraphUID     string
	DgraphSyncedAt time.Time
}

// CreateEdgeInput is the write shape for IMemoryStore.CreateEdge.
type CreateEdgeInput struct {
	UserID       uuid.UUID
	TenantID     *uuid.UUID
	FromNodeID   uuid.UUID
	ToNodeID     uuid.UUID
	EdgeType     EdgeKind
	Weight       float64
	DiscoveredBy EdgeDiscoverer
	Metadata     map[string]any
}
