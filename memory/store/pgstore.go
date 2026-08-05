package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// PGStore implements IMemoryStore backed by PostgreSQL + pgvector.
// PGStore is safe for concurrent use; each WithTx returns a new instance bound to one tx.
type PGStore struct {
	pool *pgxpool.Pool
	tx   pgx.Tx // nil when not in transaction
}

// pgxExecutor abstracts pool vs tx so the same code path serves both.
type pgxExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewPGStore binds the store to a pool. For transactions use WithTx.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (s *PGStore) executor() pgxExecutor {
	if s.tx != nil {
		return s.tx
	}
	return s.pool
}

func (s *PGStore) inTx() bool { return s.tx != nil }

// --- Node CRUD ---

func (s *PGStore) CreateNode(ctx context.Context, in types.CreateNodeInput) (types.MemoryNode, error) {
	if in.UserID == uuid.Nil {
		return types.MemoryNode{}, ErrInvalidInput
	}
	if in.Content == "" {
		return types.MemoryNode{}, ErrInvalidInput
	}
	if in.Type == "" {
		in.Type = types.MemoryTypeSemantic
	}
	if in.ContentType == "" {
		in.ContentType = types.ContentTypeFact
	}
	if in.Source == "" {
		in.Source = types.SourceInference
	}
	if in.Salience == "" {
		in.Salience = types.SalienceNormal
	}
	if in.SourceTrust == 0 {
		in.SourceTrust = 0.5
	}
	if in.Weight == 0 {
		in.Weight = 0.5
	}
	if in.Keywords == nil {
		in.Keywords = []string{}
	}

	now := time.Now().UTC()
	_ = now
	sources := in.Sources
	if sources == nil {
		sources = []types.SourceEntry{}
	}
	sourcesJSON, _ := json.Marshal(sources)

	var node types.MemoryNode
	var id uuid.UUID
	var lastAccess *time.Time
	var activityCtx, sceneCtx *string
	err := s.executor().QueryRow(ctx, `
        INSERT INTO memory_node_meta
          (user_id, tenant_id, session_id, content, summary, content_type, keywords,
           source, sources_json, type, salience, emotional_tone, state, weight, source_trust,
           activity_context, scene_context)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'active', $13, $14, $15, $16)
        RETURNING id, user_id, tenant_id, session_id, content, summary, content_type, keywords,
                  source, sources_json, type, salience, emotional_tone, state, weight, source_trust,
                  access_count, last_access_at, created_at, updated_at, version, activity_context, scene_context`,
		in.UserID, in.TenantID, in.SessionID,
		in.Content, in.Summary, string(in.ContentType), in.Keywords,
		string(in.Source), sourcesJSON,
		string(in.Type), string(in.Salience), in.EmotionalTone,
		in.Weight, in.SourceTrust,
		in.ActivityContext, in.SceneContext,
	).Scan(&id, &node.UserID, &node.TenantID, &node.SessionID,
		&node.Content, &node.Summary, &node.ContentType, &node.Keywords,
		&node.Source, &sourcesJSON, &node.Type, &node.Salience, &node.EmotionalTone,
		&node.State, &node.Weight, &node.SourceTrust,
		&node.AccessCount, &lastAccess, &node.CreatedAt, &node.UpdatedAt, &node.Version,
		&activityCtx, &sceneCtx)
	if err != nil {
		return types.MemoryNode{}, fmt.Errorf("insert memory_node_meta: %w", err)
	}
	node.ID = id
	if lastAccess != nil {
		node.LastAccessAt = *lastAccess
	}
	if activityCtx != nil {
		node.ActivityContext = *activityCtx
	}
	if sceneCtx != nil {
		node.SceneContext = *sceneCtx
	}
	if len(sourcesJSON) > 0 && string(sourcesJSON) != "null" {
		_ = json.Unmarshal(sourcesJSON, &node.Sources)
	}

	if len(in.Embedding) > 0 {
		_, err := s.executor().Exec(ctx, `
            INSERT INTO memory_embeddings (node_id, user_id, tenant_id, embedding, model, dim)
            VALUES ($1, $2, $3, $4, $5, $6)
            ON CONFLICT (node_id) DO UPDATE SET embedding = EXCLUDED.embedding,
                model = EXCLUDED.model, dim = EXCLUDED.dim, updated_at = now()`,
			node.ID, in.UserID, in.TenantID, pgvector.NewVector(in.Embedding),
			"nomic-embed-text", len(in.Embedding))
		if err != nil {
			return types.MemoryNode{}, fmt.Errorf("insert memory_embeddings: %w", err)
		}
	}
	return node, nil
}

func (s *PGStore) GetNode(ctx context.Context, nodeID uuid.UUID) (types.MemoryNode, error) {
	row := s.executor().QueryRow(ctx, `
        SELECT `+nodeColumns()+`
          FROM memory_node_meta b
         WHERE b.id = $1 AND b.deleted_at IS NULL`, nodeID)
	n, err := scanNode(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return types.MemoryNode{}, ErrNotFound
	}
	return n, err
}

func (s *PGStore) UpdateNode(ctx context.Context, nodeID uuid.UUID, expectedVersion int, mutators ...NodeMutator) (types.MemoryNode, error) {
	current, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return types.MemoryNode{}, err
	}
	if current.Version != expectedVersion {
		return types.MemoryNode{}, ErrOptimisticLockConflict
	}
	for _, m := range mutators {
		m(&current)
	}
	current.UpdatedAt = time.Now().UTC()

	var sourcesJSON []byte
	if len(current.Sources) > 0 {
		sourcesJSON, _ = json.Marshal(current.Sources)
	}
	tag, err := s.executor().Exec(ctx, `
        UPDATE memory_node_meta
           SET content = $3, summary = $4, content_type = $5, keywords = $6,
               source = $7, sources_json = COALESCE($8, sources_json),
               type = $9, salience = $10, emotional_tone = $11, state = $12,
               weight = $13, source_trust = $14,
               activity_context = $15, scene_context = $16,
               last_access_at = $17, access_count = $18,
               unstable_until = $19, cold_ref = $20,
               confidence = $21, consistency_score = $22,
               cumulative_reward = $23, emotion_valence = $24, emotion_arousal = $25,
               updated_at = $26, version = version + 1
         WHERE id = $1 AND version = $2 AND deleted_at IS NULL`,
		nodeID, expectedVersion,
		current.Content, current.Summary, string(current.ContentType), current.Keywords,
		string(current.Source), sourcesJSON,
		string(current.Type), string(current.Salience), current.EmotionalTone, string(current.State),
		current.Weight, current.SourceTrust,
		nullableStr(current.ActivityContext), nullableStr(current.SceneContext),
		nullableTime(current.LastAccessAt), current.AccessCount,
		nullableTimePtr(current.UnstableUntil), nullableStr(current.ColdRef),
		current.Confidence, current.ConsistencyScore,
		current.CumulativeReward, current.EmotionValence, current.EmotionArousal,
		current.UpdatedAt)
	if err != nil {
		return types.MemoryNode{}, fmt.Errorf("update memory_node_meta: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return types.MemoryNode{}, ErrOptimisticLockConflict
	}
	current.Version++
	return current, nil
}

func (s *PGStore) SoftDelete(ctx context.Context, nodeID uuid.UUID) error {
	tag, err := s.executor().Exec(ctx, `
        UPDATE memory_node_meta
           SET deleted_at = now(), updated_at = now(), version = version + 1
         WHERE id = $1 AND deleted_at IS NULL`, nodeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordHistory appends a superseded-version row to memory_node_history.
func (s *PGStore) RecordHistory(ctx context.Context, h types.MemoryNodeHistory) error {
	_, err := s.executor().Exec(ctx, `
        INSERT INTO memory_node_history
            (current_node_id, user_id, content, content_type, source, weight,
             valid_from, valid_to, superseded_by_event, change_summary)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		h.CurrentNodeID, h.UserID, h.Content, h.ContentType, h.Source, h.Weight,
		h.ValidFrom, h.ValidTo, h.SupersededByEvent, h.ChangeSummary)
	return err
}

func (s *PGStore) ListNodes(ctx context.Context, f SearchFilter) (SearchResults, error) {
	if f.Limit == 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{f.UserID, f.Limit}
	where := "user_id = $1 AND deleted_at IS NULL"
	if f.TenantID != nil {
		args = append(args, f.TenantID)
		where += fmt.Sprintf(" AND tenant_id = $%d", len(args))
	}
	if f.Cursor != "" {
		curID, err := uuid.Parse(f.Cursor)
		if err == nil {
			args = append(args, curID)
			where += fmt.Sprintf(" AND id > $%d", len(args))
		}
	}
	if len(f.Types) > 0 {
		args = append(args, typeStrings(f.Types))
		where += fmt.Sprintf(" AND type = ANY($%d)", len(args))
	}
	if len(f.States) > 0 {
		args = append(args, stateStrings(f.States))
		where += fmt.Sprintf(" AND state = ANY($%d)", len(args))
	}
	if f.SessionID != nil {
		args = append(args, f.SessionID)
		where += fmt.Sprintf(" AND session_id = $%d", len(args))
	}
	if f.Since != nil {
		args = append(args, *f.Since)
		where += fmt.Sprintf(" AND updated_at >= $%d", len(args))
	}
	if f.Until != nil {
		args = append(args, *f.Until)
		where += fmt.Sprintf(" AND updated_at < $%d", len(args))
	}

	query := fmt.Sprintf(`
        SELECT %s
          FROM memory_node_meta
         WHERE %s
         ORDER BY id
         LIMIT $2`, nodeColumns(), where)
	rows, err := s.executor().Query(ctx, query, args...)
	if err != nil {
		return SearchResults{}, err
	}
	defer rows.Close()

	items := []types.MemoryNode{}
	for rows.Next() {
		n, err := scanNode(rows.Scan)
		if err != nil {
			return SearchResults{}, err
		}
		items = append(items, n)
	}
	next := ""
	if len(items) == f.Limit {
		next = items[len(items)-1].ID.String()
	}
	return SearchResults{Items: items, NextCursor: next}, nil
}

// --- Search ---

func (s *PGStore) SearchSimilar(ctx context.Context, q SimilarQuery) ([]SimilarResult, error) {
	if q.TopK == 0 {
		q.TopK = 10
	}
	if q.MinSim == 0 {
		q.MinSim = 0.3
	}
	if len(q.Embedding) == 0 {
		return nil, ErrInvalidInput
	}
	args := []any{pgvector.NewVector(q.Embedding), q.UserID, 1 - q.MinSim, q.TopK}
	query := fmt.Sprintf(`
        SELECT %s, 1 - (e.embedding <=> $1::vector) AS sim
          FROM memory_embeddings e
          JOIN memory_node_meta b ON b.id = e.node_id
         WHERE b.user_id = $2
           AND b.deleted_at IS NULL
           AND (e.embedding <=> $1::vector) < $3
      ORDER BY e.embedding <=> $1::vector
         LIMIT $4`, nodeColumnsAliased("b"))
	if len(q.TypeFilter) > 0 {
		args = append(args, typeStrings(q.TypeFilter))
		query = strings.Replace(query, "AND b.deleted_at IS NULL",
			fmt.Sprintf("AND b.deleted_at IS NULL AND b.type = ANY($%d)", len(args)), 1)
	}
	rows, err := s.executor().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SimilarResult{}
	for rows.Next() {
		n, sim, err := scanNodeWithSim(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, SimilarResult{Node: n, Sim: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// (Placeholder retained to make the struct self-contained; replaced by real impl below.)
func (s *PGStore) SearchByKeywords(ctx context.Context, userID uuid.UUID, keywords []string, limit int) ([]SimilarResult, error) {
	if limit == 0 {
		limit = 10
	}
	if len(keywords) == 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.executor().Query(ctx, fmt.Sprintf(`
        SELECT %s
          FROM memory_node_meta b
         WHERE b.user_id = $1
           AND b.deleted_at IS NULL
           AND b.keywords && $2::TEXT[]
         ORDER BY b.weight DESC
         LIMIT $3`, nodeColumnsAliased("b")), userID, keywords, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SimilarResult{}
	for rows.Next() {
		n, err := scanNode(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, SimilarResult{Node: n, Sim: 0.5})
	}
	return out, nil
}

func (s *PGStore) SearchHybrid(ctx context.Context, q SimilarQuery, keywords []string) ([]SimilarResult, error) {
	sim, err := s.SearchSimilar(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(keywords) == 0 {
		return sim, nil
	}
	kw, err := s.SearchByKeywords(ctx, q.UserID, keywords, q.TopK)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]int{}
	merged := make([]SimilarResult, 0, len(sim)+len(kw))
	for _, r := range sim {
		seen[r.Node.ID] = len(merged)
		merged = append(merged, SimilarResult{Node: r.Node, Sim: 0.7 * r.Sim})
	}
	for _, r := range kw {
		if idx, ok := seen[r.Node.ID]; ok {
			merged[idx].Sim += 0.3 * r.Sim
		} else {
			merged = append(merged, SimilarResult{Node: r.Node, Sim: 0.3 * r.Sim})
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Sim > merged[j].Sim })
	if q.TopK > 0 && len(merged) > q.TopK {
		merged = merged[:q.TopK]
	}
	return merged, nil
}

// --- Edges ---

func (s *PGStore) CreateEdge(ctx context.Context, in types.CreateEdgeInput) (types.MemoryEdge, error) {
	if in.Weight == 0 {
		in.Weight = 0.5
	}
	if in.EdgeType == "" {
		in.EdgeType = types.EdgeKindRelated
	}
	metaJSON := []byte("{}")
	if in.Metadata != nil {
		metaJSON, _ = json.Marshal(in.Metadata)
	}
	var e types.MemoryEdge
	err := s.executor().QueryRow(ctx, `
        INSERT INTO memory_edges (user_id, tenant_id, from_node_id, to_node_id, edge_type, kind, weight, discovered_by, metadata)
        VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8)
        ON CONFLICT (from_node_id, to_node_id, edge_type)
        DO UPDATE SET weight = EXCLUDED.weight, metadata = EXCLUDED.metadata, updated_at = now()
        RETURNING id, user_id, tenant_id, from_node_id, to_node_id, edge_type, weight, discovered_by, metadata, created_at, updated_at, deleted_at`,
		in.UserID, in.TenantID, in.FromNodeID, in.ToNodeID, string(in.EdgeType), in.Weight,
		string(in.DiscoveredBy), metaJSON,
	).Scan(&e.ID, &e.UserID, &e.TenantID, &e.FromNodeID, &e.ToNodeID, &e.EdgeType,
		&e.Weight, &e.DiscoveredBy, &e.Metadata, &e.CreatedAt, &e.UpdatedAt, &e.DeletedAt)
	if err != nil {
		return types.MemoryEdge{}, fmt.Errorf("insert memory_edges: %w", err)
	}
	return e, nil
}

func (s *PGStore) GetEdges(ctx context.Context, nodeID uuid.UUID, kinds []types.EdgeKind) ([]types.MemoryEdge, error) {
	query := `
        SELECT id, user_id, tenant_id, from_node_id, to_node_id, edge_type, weight, discovered_by, metadata, created_at, updated_at, deleted_at
          FROM memory_edges
         WHERE (from_node_id = $1 OR to_node_id = $1) AND deleted_at IS NULL`
	args := []any{nodeID}
	if len(kinds) > 0 {
		query += " AND edge_type = ANY($2)"
		args = append(args, kindStrings(kinds))
	}
	rows, err := s.executor().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []types.MemoryEdge{}
	for rows.Next() {
		var e types.MemoryEdge
		if err := rows.Scan(&e.ID, &e.UserID, &e.TenantID, &e.FromNodeID, &e.ToNodeID,
			&e.EdgeType, &e.Weight, &e.DiscoveredBy, &e.Metadata,
			&e.CreatedAt, &e.UpdatedAt, &e.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// --- Bulk operations ---

func (s *PGStore) DecayAll(ctx context.Context, userID uuid.UUID, before time.Time, fn DecayFn) (int, error) {
	rows, err := s.executor().Query(ctx, fmt.Sprintf(`
        SELECT %s
          FROM memory_node_meta
         WHERE user_id = $1 AND deleted_at IS NULL AND updated_at < $2`,
		nodeColumns()), userID, before)
	if err != nil {
		return 0, err
	}
	type upd struct {
		id     uuid.UUID
		weight float64
	}
	updates := []upd{}
	for rows.Next() {
		n, err := scanNode(rows.Scan)
		if err != nil {
			rows.Close()
			return 0, err
		}
		updates = append(updates, upd{id: n.ID, weight: fn(n)})
	}
	rows.Close()

	if len(updates) == 0 {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	for _, u := range updates {
		if _, err := tx.Exec(ctx, `
            UPDATE memory_node_meta SET weight = $2, updated_at = now(), version = version + 1
             WHERE id = $1`, u.id, u.weight); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(updates), nil
}

func (s *PGStore) BulkUpdateWeight(ctx context.Context, userID uuid.UUID, updates []WeightUpdate) (int, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	applied := 0
	for _, u := range updates {
		tag, err := tx.Exec(ctx, `
            UPDATE memory_node_meta SET weight = weight + $3, updated_at = now(), version = version + 1
             WHERE id = $1 AND user_id = $2`,
			u.NodeID, userID, u.Delta)
		if err != nil {
			return applied, err
		}
		applied += int(tag.RowsAffected())
	}
	return applied, tx.Commit(ctx)
}

func (s *PGStore) FindUnconnectedSimilarPairs(ctx context.Context, userID uuid.UUID, threshold float64, limit int) ([]SimilarPair, error) {
	if limit == 0 {
		limit = 50
	}
	rows, err := s.executor().Query(ctx, `
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
             ORDER BY eb.embedding <=> ea.embedding
             LIMIT 1
          ) AS nbr
          JOIN memory_node_meta b ON b.id = nbr.bid
         LIMIT $3`, userID, threshold, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SimilarPair{}
	for rows.Next() {
		var a, b types.MemoryNode
		var sim float64
		if err := rows.Scan(
			&a.ID, &a.Content, &a.Summary, &a.Keywords, &a.Type, &a.Weight, &a.State,
			&b.ID, &b.Content, &b.Summary, &b.Keywords, &b.Type, &b.Weight, &b.State,
			&sim); err != nil {
			return nil, err
		}
		out = append(out, SimilarPair{A: a, B: b, Sim: sim})
	}
	return out, nil
}

// ListMemoryUserIDs returns every distinct user owning at least one non-deleted
// memory node. Used by the Consolidation sweep to fan out per-user.
func (s *PGStore) ListMemoryUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.executor().Query(ctx,
		`SELECT DISTINCT user_id FROM memory_node_meta WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// --- Graph methods (same SQL as PGraphStore, executor()-aware for tx support) ---

func (s *PGStore) GetGraphNeighbors(ctx context.Context, userID uuid.UUID, seedIDs []uuid.UUID, depth int) ([]GraphNeighbor, error) {
	if depth < 1 { depth = 1 }
	if len(seedIDs) == 0 { return nil, nil }
	rows, err := s.executor().Query(ctx, `
        WITH RECURSIVE graph_walk AS (
            SELECT e.from_node_id AS src, e.to_node_id AS dst, e.id AS edge_id,
                   e.edge_type, e.weight, e.discovered_by, 1 AS depth
              FROM memory_edges e
             WHERE e.user_id = $1 AND e.deleted_at IS NULL
               AND (e.from_node_id = ANY($2) OR e.to_node_id = ANY($2))
             UNION
            SELECT e.from_node_id, e.to_node_id, e.id, e.edge_type, e.weight, e.discovered_by, g.depth + 1
              FROM memory_edges e
              JOIN graph_walk g ON (e.from_node_id = g.dst OR e.to_node_id = g.dst)
             WHERE e.user_id = $1 AND e.deleted_at IS NULL AND g.depth < $3
        )
        SELECT DISTINCT ON (n.id, gw.edge_id) n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               gw.edge_id, gw.edge_type, gw.weight, gw.discovered_by, gw.depth
          FROM graph_walk gw
          JOIN memory_node_meta n ON n.id IN (gw.src, gw.dst) AND n.deleted_at IS NULL
         WHERE n.id != ALL($2)
         ORDER BY n.id, gw.edge_id`, userID, seedIDs, depth)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []GraphNeighbor
	for rows.Next() {
		var gn GraphNeighbor
		var et, db string
		if err := rows.Scan(&gn.Node.ID, &gn.Node.Content, &gn.Node.Summary, &gn.Node.Keywords,
			&gn.Node.Type, &gn.Node.Weight, &gn.Node.State, &gn.Edge.ID, &et, &gn.Edge.Weight, &db, &gn.Depth); err != nil {
			return nil, err
		}
		gn.Edge.EdgeType = types.EdgeKind(et)
		gn.Edge.DiscoveredBy = types.EdgeDiscoverer(db)
		out = append(out, gn)
	}
	return out, rows.Err()
}

func (s *PGStore) FindPath(ctx context.Context, userID uuid.UUID, from, to uuid.UUID, maxDepth int) ([]GraphNeighbor, error) {
	if maxDepth < 1 { maxDepth = 5 }
	rows, err := s.executor().Query(ctx, `
        WITH RECURSIVE walk AS (
            SELECT from_node_id AS src, to_node_id AS dst, id AS edge_id,
                   edge_type, weight, discovered_by, 1 AS depth, ARRAY[id] AS path_edges
              FROM memory_edges WHERE user_id = $1 AND deleted_at IS NULL
               AND (from_node_id = $2 OR to_node_id = $2)
             UNION
            SELECT e.from_node_id, e.to_node_id, e.id, e.edge_type, e.weight, e.discovered_by,
                   w.depth + 1, w.path_edges || e.id
              FROM memory_edges e
              JOIN walk w ON (e.from_node_id = w.dst OR e.to_node_id = w.dst) AND e.id <> ALL(w.path_edges)
             WHERE e.user_id = $1 AND e.deleted_at IS NULL AND w.depth < $4
        )
        SELECT n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               w.edge_id, w.edge_type, w.weight, w.discovered_by, w.depth
          FROM walk w JOIN memory_node_meta n ON n.id = w.dst AND n.deleted_at IS NULL
         WHERE w.dst = $3 ORDER BY w.depth LIMIT 1`, userID, from, to, maxDepth)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanGraphNeighbors(rows)
}

func (s *PGStore) ExpandSubgraph(ctx context.Context, userID uuid.UUID, seedIDs []uuid.UUID, depth int) ([]GraphNeighbor, error) {
	if depth < 1 { depth = 1 }
	if len(seedIDs) == 0 { return nil, nil }
	rows, err := s.executor().Query(ctx, `
        WITH RECURSIVE graph_walk AS (
            SELECT from_node_id AS src, to_node_id AS dst, id AS edge_id,
                   edge_type, weight, discovered_by, 1 AS depth
              FROM memory_edges WHERE user_id = $1 AND deleted_at IS NULL
               AND (from_node_id = ANY($2) OR to_node_id = ANY($2))
             UNION
            SELECT e.from_node_id, e.to_node_id, e.id, e.edge_type, e.weight, e.discovered_by, g.depth + 1
              FROM memory_edges e
              JOIN graph_walk g ON (e.from_node_id = g.dst OR e.to_node_id = g.dst)
             WHERE e.user_id = $1 AND e.deleted_at IS NULL AND g.depth < $3
        )
        SELECT n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               '00000000-0000-0000-0000-000000000000'::uuid, ''::text, 0::float8, ''::text, 0
          FROM memory_node_meta n WHERE n.id = ANY($2) AND n.user_id = $1 AND n.deleted_at IS NULL
         UNION ALL
        SELECT n.id, n.content, n.summary, n.keywords, n.type, n.weight, n.state,
               gw.edge_id, gw.edge_type, gw.weight, gw.discovered_by, gw.depth
          FROM graph_walk gw
          JOIN memory_node_meta n ON n.id IN (gw.src, gw.dst) AND n.deleted_at IS NULL
         WHERE n.id != ALL($2) ORDER BY depth`, userID, seedIDs, depth)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanGraphNeighbors(rows)
}

// scanGraphNeighbors is shared by PGStore + PGraphStore.
func scanGraphNeighbors(rows pgxRows) ([]GraphNeighbor, error) { return scanGraphNeighborRows(rows) }

type pgxRows = pgx.Rows // avoids import cycle — pgx is already imported

func scanGraphNeighborRows(rows pgx.Rows) ([]GraphNeighbor, error) {
	var out []GraphNeighbor
	for rows.Next() {
		var gn GraphNeighbor
		var et, db string
		if err := rows.Scan(&gn.Node.ID, &gn.Node.Content, &gn.Node.Summary, &gn.Node.Keywords,
			&gn.Node.Type, &gn.Node.Weight, &gn.Node.State, &gn.Edge.ID, &et, &gn.Edge.Weight, &db, &gn.Depth); err != nil {
			return nil, err
		}
		gn.Edge.EdgeType = types.EdgeKind(et)
		gn.Edge.DiscoveredBy = types.EdgeDiscoverer(db)
		out = append(out, gn)
	}
	return out, rows.Err()
}

// --- Transactions ---

func (s *PGStore) WithTx(ctx context.Context) (IMemoryStore, error) {
	if s.pool == nil {
		return nil, errors.New("PGStore.WithTx: nil pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &PGStore{pool: s.pool, tx: tx}, nil
}

func (s *PGStore) CommitTx(ctx context.Context) error {
	if s.tx == nil {
		return errors.New("PGStore.CommitTx: not in transaction")
	}
	return s.tx.Commit(ctx)
}

func (s *PGStore) RollbackTx(ctx context.Context) error {
	if s.tx == nil {
		return nil
	}
	return s.tx.Rollback(ctx)
}

// --- helpers ---

func nodeColumns() string {
	return `id, user_id, tenant_id, session_id, content, summary, content_type, keywords,
            source, sources_json, type, salience, emotional_tone, state, weight, source_trust,
            access_count, last_access_at, created_at, updated_at, deleted_at, version,
            activity_context, scene_context, cumulative_reward, emotion_valence, emotion_arousal,
            confidence, consistency_score, unstable_until, cold_ref, dgraph_uid, dgraph_synced_at`
}

// nodeColumnsAliased prefixes each column with `alias.` so joins don't produce
// "column reference is ambiguous" errors.
func nodeColumnsAliased(alias string) string {
	cols := []string{"id", "user_id", "tenant_id", "session_id", "content", "summary",
		"content_type", "keywords", "source", "sources_json", "type", "salience",
		"emotional_tone", "state", "weight", "source_trust", "access_count", "last_access_at",
		"created_at", "updated_at", "deleted_at", "version", "activity_context", "scene_context",
		"cumulative_reward", "emotion_valence", "emotion_arousal", "confidence",
		"consistency_score", "unstable_until", "cold_ref", "dgraph_uid", "dgraph_synced_at"}
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = alias + "." + c
	}
	return strings.Join(out, ", ")
}

func nodeSelectClause() string { return "SELECT " + nodeColumns() + " FROM memory_node_meta b" }

type scanFn func(dest ...any) error

func scanNode(scan scanFn) (types.MemoryNode, error) {
	var n types.MemoryNode
	var sourcesJSON []byte
	// Nullable columns need pointer targets
	var lastAccess, dgraphSynced *time.Time
	var activityCtx, sceneCtx, coldRef, dgraphUID *string
	err := scan(
		&n.ID, &n.UserID, &n.TenantID, &n.SessionID,
		&n.Content, &n.Summary, &n.ContentType, &n.Keywords,
		&n.Source, &sourcesJSON, &n.Type, &n.Salience, &n.EmotionalTone,
		&n.State, &n.Weight, &n.SourceTrust,
		&n.AccessCount, &lastAccess, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.Version,
		&activityCtx, &sceneCtx,
		&n.CumulativeReward, &n.EmotionValence, &n.EmotionArousal,
		&n.Confidence, &n.ConsistencyScore,
		&n.UnstableUntil, &coldRef, &dgraphUID, &dgraphSynced,
	)
	if err != nil {
		return n, err
	}
	if lastAccess != nil {
		n.LastAccessAt = *lastAccess
	}
	if activityCtx != nil {
		n.ActivityContext = *activityCtx
	}
	if sceneCtx != nil {
		n.SceneContext = *sceneCtx
	}
	if coldRef != nil {
		n.ColdRef = *coldRef
	}
	if dgraphUID != nil {
		n.DgraphUID = *dgraphUID
	}
	if dgraphSynced != nil {
		n.DgraphSyncedAt = *dgraphSynced
	}
	if len(sourcesJSON) > 0 && string(sourcesJSON) != "null" {
		_ = json.Unmarshal(sourcesJSON, &n.Sources)
	}
	return n, nil
}

// scanNodeWithSim scans node columns + a trailing similarity float (used by SearchSimilar).
func scanNodeWithSim(scan scanFn) (types.MemoryNode, float64, error) {
	var n types.MemoryNode
	var sourcesJSON []byte
	var sim float64
	var lastAccess, dgraphSynced *time.Time
	var activityCtx, sceneCtx, coldRef, dgraphUID *string
	err := scan(
		&n.ID, &n.UserID, &n.TenantID, &n.SessionID,
		&n.Content, &n.Summary, &n.ContentType, &n.Keywords,
		&n.Source, &sourcesJSON, &n.Type, &n.Salience, &n.EmotionalTone,
		&n.State, &n.Weight, &n.SourceTrust,
		&n.AccessCount, &lastAccess, &n.CreatedAt, &n.UpdatedAt, &n.DeletedAt, &n.Version,
		&activityCtx, &sceneCtx,
		&n.CumulativeReward, &n.EmotionValence, &n.EmotionArousal,
		&n.Confidence, &n.ConsistencyScore,
		&n.UnstableUntil, &coldRef, &dgraphUID, &dgraphSynced,
		&sim,
	)
	if err != nil {
		return n, 0, err
	}
	if lastAccess != nil {
		n.LastAccessAt = *lastAccess
	}
	if activityCtx != nil {
		n.ActivityContext = *activityCtx
	}
	if sceneCtx != nil {
		n.SceneContext = *sceneCtx
	}
	if coldRef != nil {
		n.ColdRef = *coldRef
	}
	if dgraphUID != nil {
		n.DgraphUID = *dgraphUID
	}
	if dgraphSynced != nil {
		n.DgraphSyncedAt = *dgraphSynced
	}
	if len(sourcesJSON) > 0 && string(sourcesJSON) != "null" {
		_ = json.Unmarshal(sourcesJSON, &n.Sources)
	}
	return n, sim, nil
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullableTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func typeStrings(ts []types.MemoryType) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

func stateStrings(ss []types.NodeState) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}
	return out
}

func kindStrings(ks []types.EdgeKind) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = string(k)
	}
	return out
}

// strings import kept (used by SearchSimilar SQL rewrite)
