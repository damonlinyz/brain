package store

import (
	"context"
	"fmt"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGraphStore is a standalone graph backend backed by PostgreSQL — no external
// graph DB needed. It implements the graph-related subset of IMemoryStore
// (CreateEdge, GetEdges, GetGraphNeighbors, FindUnconnectedSimilarPairs)
// using PG recursive CTEs and JOINs. Node methods are stubbed — PGraphStore is
// intended to be composed with a node store via SplitStore.
type PGraphStore struct {
	pool *pgxpool.Pool
}

// NewPGraphStore binds a graph backend to a PG connection pool.
func NewPGraphStore(pool *pgxpool.Pool) *PGraphStore {
	return &PGraphStore{pool: pool}
}

// --- graph operations (real implementations via PG) ---

func (g *PGraphStore) CreateEdge(ctx context.Context, in types.CreateEdgeInput) (types.MemoryEdge, error) {
	id := uuid.New()
	now := time.Now().UTC()
	_, err := g.pool.Exec(ctx,
		`INSERT INTO memory_edges (id, user_id, tenant_id, from_node_id, to_node_id, edge_type, weight, discovered_by, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id, in.UserID, in.TenantID, in.FromNodeID, in.ToNodeID,
		string(in.EdgeType), in.Weight, string(in.DiscoveredBy), in.Metadata, now)
	if err != nil {
		return types.MemoryEdge{}, fmt.Errorf("pgraph: create edge: %w", err)
	}
	return types.MemoryEdge{ID: id, FromNodeID: in.FromNodeID, ToNodeID: in.ToNodeID, EdgeType: in.EdgeType, Weight: in.Weight, CreatedAt: now, UpdatedAt: now}, nil
}

func (g *PGraphStore) GetEdges(ctx context.Context, nodeID uuid.UUID, kinds []types.EdgeKind) ([]types.MemoryEdge, error) {
	q := `SELECT id, user_id, from_node_id, to_node_id, edge_type, weight, discovered_by, created_at
            FROM memory_edges WHERE deleted_at IS NULL AND (from_node_id = $1 OR to_node_id = $1)`
	args := []any{nodeID}
	if len(kinds) > 0 {
		q += " AND edge_type = ANY($2)"
		args = append(args, kindStrings(kinds))
	}
	rows, err := g.pool.Query(ctx, q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []types.MemoryEdge
	for rows.Next() {
		var e types.MemoryEdge
		var edgeType string
		if err := rows.Scan(&e.ID, &e.UserID, &e.FromNodeID, &e.ToNodeID, &edgeType, &e.Weight, &e.DiscoveredBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.EdgeType = types.EdgeKind(edgeType)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (g *PGraphStore) GetGraphNeighbors(ctx context.Context, userID uuid.UUID, seedIDs []uuid.UUID, depth int) ([]GraphNeighbor, error) {
	if depth < 1 { depth = 1 }
	if len(seedIDs) == 0 { return nil, nil }

	rows, err := g.pool.Query(ctx, `
        WITH RECURSIVE graph_walk AS (
            -- Base: direct edges from seed set (depth=1).
            SELECT e.from_node_id AS src, e.to_node_id AS dst, e.id AS edge_id,
                   e.edge_type, e.weight, e.discovered_by, 1 AS depth
              FROM memory_edges e
             WHERE e.user_id = $1 AND e.deleted_at IS NULL
               AND (e.from_node_id = ANY($2) OR e.to_node_id = ANY($2))
             UNION
            -- Recurse: from previously-visited nodes, one more hop.
            SELECT e.from_node_id, e.to_node_id, e.id,
                   e.edge_type, e.weight, e.discovered_by, g.depth + 1
              FROM memory_edges e
              JOIN graph_walk g ON (e.from_node_id = g.dst OR e.to_node_id = g.dst)
             WHERE e.user_id = $1 AND e.deleted_at IS NULL
               AND g.depth < $3
        )
        SELECT DISTINCT ON (n.id, gw.edge_id)
               n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               gw.edge_id, gw.edge_type, gw.weight, gw.discovered_by, gw.depth
          FROM graph_walk gw
          JOIN memory_node_meta n ON n.id IN (gw.src, gw.dst) AND n.deleted_at IS NULL
         WHERE n.id != ALL($2)  -- exclude seeds; we already have them
         ORDER BY n.id, gw.edge_id`,
		userID, seedIDs, depth)
	if err != nil { return nil, fmt.Errorf("pgraph: graph neighbors: %w", err) }
	defer rows.Close()

	var out []GraphNeighbor
	for rows.Next() {
		var gn GraphNeighbor
		var edgeType, discBy string
		if err := rows.Scan(&gn.Node.ID, &gn.Node.Content, &gn.Node.Summary, &gn.Node.Keywords,
			&gn.Node.Type, &gn.Node.Weight, &gn.Node.State,
			&gn.Edge.ID, &edgeType, &gn.Edge.Weight, &discBy, &gn.Depth); err != nil {
			return nil, err
		}
		gn.Edge.EdgeType = types.EdgeKind(edgeType)
		gn.Edge.DiscoveredBy = types.EdgeDiscoverer(discBy)
		out = append(out, gn)
	}
	return out, rows.Err()
}

func (g *PGraphStore) FindUnconnectedSimilarPairs(ctx context.Context, userID uuid.UUID, threshold float64, limit int) ([]SimilarPair, error) {
	if limit == 0 { limit = 50 }
	rows, err := g.pool.Query(ctx, `
        SELECT a.id, a.content, a.summary, a.keywords, a.type, a.weight, a.state,
               b.id, b.content, b.summary, b.keywords, b.type, b.weight, b.state,
               1 - (ea.embedding <=> nbr.embedding) AS sim
          FROM memory_embeddings ea
          JOIN memory_node_meta a ON a.id = ea.node_id AND a.user_id = $1 AND a.deleted_at IS NULL
          CROSS JOIN LATERAL (
            SELECT eb.embedding, eb.node_id AS bid
              FROM memory_embeddings eb
              JOIN memory_node_meta b ON b.id = eb.node_id AND b.user_id = $1 AND b.deleted_at IS NULL
             WHERE eb.node_id <> ea.node_id
               AND 1 - (eb.embedding <=> ea.embedding) > $2
               AND NOT EXISTS (
                     SELECT 1 FROM memory_edges e
                      WHERE e.user_id = $1 AND e.deleted_at IS NULL
                        AND ((e.from_node_id = ea.node_id AND e.to_node_id = eb.node_id)
                          OR (e.from_node_id = eb.node_id AND e.to_node_id = ea.node_id)))
             ORDER BY eb.embedding <=> ea.embedding LIMIT 1
          ) AS nbr
          JOIN memory_node_meta b ON b.id = nbr.bid
         LIMIT $3`, userID, threshold, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []SimilarPair
	for rows.Next() {
		var a, b types.MemoryNode
		var sim float64
		if err := rows.Scan(&a.ID, &a.Content, &a.Summary, &a.Keywords, &a.Type, &a.Weight, &a.State,
			&b.ID, &b.Content, &b.Summary, &b.Keywords, &b.Type, &b.Weight, &b.State, &sim); err != nil {
			return nil, err
		}
		out = append(out, SimilarPair{A: a, B: b, Sim: sim})
	}
	return out, rows.Err()
}

func (g *PGraphStore) FindPath(ctx context.Context, userID uuid.UUID, from, to uuid.UUID, maxDepth int) ([]GraphNeighbor, error) {
	if maxDepth < 1 { maxDepth = 5 }
	rows, err := g.pool.Query(ctx, `
        WITH RECURSIVE walk AS (
            SELECT from_node_id AS src, to_node_id AS dst, id AS edge_id,
                   edge_type, weight, discovered_by, 1 AS depth,
                   ARRAY[id] AS path_edges
              FROM memory_edges
             WHERE user_id = $1 AND deleted_at IS NULL
               AND (from_node_id = $2 OR to_node_id = $2)
             UNION
            SELECT e.from_node_id, e.to_node_id, e.id,
                   e.edge_type, e.weight, e.discovered_by,
                   w.depth + 1, w.path_edges || e.id
              FROM memory_edges e
              JOIN walk w ON (e.from_node_id = w.dst OR e.to_node_id = w.dst)
                             AND e.id <> ALL(w.path_edges)
             WHERE e.user_id = $1 AND e.deleted_at IS NULL AND w.depth < $4
        )
        SELECT n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               w.edge_id, w.edge_type, w.weight, w.discovered_by, w.depth
          FROM walk w
          JOIN memory_node_meta n ON n.id = w.dst AND n.deleted_at IS NULL
         WHERE w.dst = $3
         ORDER BY w.depth LIMIT 1`, userID, from, to, maxDepth)
	if err != nil { return nil, fmt.Errorf("pgraph: findpath: %w", err) }
	defer rows.Close()
	return scanGraphNeighbors(rows)
}

func (g *PGraphStore) ExpandSubgraph(ctx context.Context, userID uuid.UUID, seedIDs []uuid.UUID, depth int) ([]GraphNeighbor, error) {
	if depth < 1 { depth = 1 }
	if len(seedIDs) == 0 { return nil, nil }
	// Reuse GetGraphNeighbors but include seeds as depth-0 entries.
	rows, err := g.pool.Query(ctx, `
        WITH RECURSIVE graph_walk AS (
            SELECT from_node_id AS src, to_node_id AS dst, id AS edge_id,
                   edge_type, weight, discovered_by, 1 AS depth
              FROM memory_edges
             WHERE user_id = $1 AND deleted_at IS NULL
               AND (from_node_id = ANY($2) OR to_node_id = ANY($2))
             UNION
            SELECT e.from_node_id, e.to_node_id, e.id,
                   e.edge_type, e.weight, e.discovered_by, g.depth + 1
              FROM memory_edges e
              JOIN graph_walk g ON (e.from_node_id = g.dst OR e.to_node_id = g.dst)
             WHERE e.user_id = $1 AND e.deleted_at IS NULL AND g.depth < $3
        )
        -- First: seed nodes (depth=0, no edge).
        SELECT n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               '00000000-0000-0000-0000-000000000000'::uuid, ''::text, 0::float8, ''::text, 0
          FROM memory_node_meta n
         WHERE n.id = ANY($2) AND n.user_id = $1 AND n.deleted_at IS NULL
         UNION ALL
        -- Then: neighbour nodes from the walk.
        SELECT n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               gw.edge_id, gw.edge_type, gw.weight, gw.discovered_by, gw.depth
          FROM graph_walk gw
          JOIN memory_node_meta n ON n.id IN (gw.src, gw.dst) AND n.deleted_at IS NULL
         WHERE n.id != ALL($2)
         ORDER BY depth`, userID, seedIDs, depth)
	if err != nil { return nil, fmt.Errorf("pgraph: expand subgraph: %w", err) }
	defer rows.Close()
	return scanGraphNeighbors(rows)
}

// --- node stubs (PGraphStore is a graph-only backend) ---

func (g *PGraphStore) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) {
	return types.MemoryNode{}, fmt.Errorf("pgraph: CreateNode not supported (use SplitStore)")
}
func (g *PGraphStore) GetNode(context.Context, uuid.UUID) (types.MemoryNode, error) {
	return types.MemoryNode{}, ErrNotFound
}
func (g *PGraphStore) UpdateNode(context.Context, uuid.UUID, int, ...NodeMutator) (types.MemoryNode, error) {
	return types.MemoryNode{}, fmt.Errorf("pgraph: UpdateNode not supported")
}
func (g *PGraphStore) SoftDelete(context.Context, uuid.UUID) error { return fmt.Errorf("pgraph: not supported") }
func (g *PGraphStore) ListNodes(context.Context, SearchFilter) (SearchResults, error) {
	return SearchResults{}, fmt.Errorf("pgraph: not supported")
}
func (g *PGraphStore) RecordHistory(context.Context, types.MemoryNodeHistory) error { return fmt.Errorf("pgraph: not supported") }
func (g *PGraphStore) SearchSimilar(context.Context, SimilarQuery) ([]SimilarResult, error) {
	return nil, fmt.Errorf("pgraph: SearchSimilar not supported")
}
func (g *PGraphStore) SearchByKeywords(context.Context, uuid.UUID, []string, int) ([]SimilarResult, error) {
	return nil, fmt.Errorf("pgraph: not supported")
}
func (g *PGraphStore) SearchHybrid(context.Context, SimilarQuery, []string) ([]SimilarResult, error) {
	return nil, fmt.Errorf("pgraph: not supported")
}
func (g *PGraphStore) DecayAll(context.Context, uuid.UUID, time.Time, DecayFn) (int, error) {
	return 0, fmt.Errorf("pgraph: not supported")
}
func (g *PGraphStore) BulkUpdateWeight(context.Context, uuid.UUID, []WeightUpdate) (int, error) {
	return 0, fmt.Errorf("pgraph: not supported")
}
func (g *PGraphStore) ListMemoryUserIDs(context.Context) ([]uuid.UUID, error) {
	return nil, fmt.Errorf("pgraph: not supported")
}
func (g *PGraphStore) WithTx(context.Context) (IMemoryStore, error) { return g, nil }
func (g *PGraphStore) CommitTx(context.Context) error               { return nil }
func (g *PGraphStore) RollbackTx(context.Context) error             { return nil }
