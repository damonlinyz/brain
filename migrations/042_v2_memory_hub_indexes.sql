-- V2 Memory Hub indexes — HNSW (768d) + GIN + partial B-tree.

BEGIN;

-- memory_node_meta lookups
CREATE INDEX IF NOT EXISTS idx_memory_node_meta_user_state
    ON memory_node_meta (user_id, state)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_tenant
    ON memory_node_meta (tenant_id, state)
    WHERE deleted_at IS NULL AND tenant_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_weight
    ON memory_node_meta (user_id, weight DESC)
    WHERE deleted_at IS NULL AND state = 'active';

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_last_access
    ON memory_node_meta (user_id, last_access_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_session
    ON memory_node_meta (session_id)
    WHERE deleted_at IS NULL AND session_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_type
    ON memory_node_meta (user_id, type)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_source
    ON memory_node_meta (user_id, source)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_unstable
    ON memory_node_meta (unstable_until)
    WHERE unstable_until IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_dgraph_pending
    ON memory_node_meta (updated_at)
    WHERE dgraph_uid IS NULL AND deleted_at IS NULL;

-- Keywords GIN (Trigger Jaccard recall + PatternSeparation)
CREATE INDEX IF NOT EXISTS idx_memory_node_meta_keywords
    ON memory_node_meta USING GIN (keywords);

-- Full-text search (simple config — fine for mixed CJK/English; trigram covers substring)
ALTER TABLE memory_node_meta ADD COLUMN IF NOT EXISTS tsv TSVECTOR
    GENERATED ALWAYS AS (to_tsvector('simple', coalesce(content, ''))) STORED;
CREATE INDEX IF NOT EXISTS idx_memory_node_meta_tsv
    ON memory_node_meta USING GIN (tsv);

CREATE INDEX IF NOT EXISTS idx_memory_node_meta_content_trgm
    ON memory_node_meta USING GIN (content gin_trgm_ops);

-- memory_node_history
CREATE INDEX IF NOT EXISTS idx_memory_node_history_node
    ON memory_node_history (current_node_id, valid_to DESC);

CREATE INDEX IF NOT EXISTS idx_memory_node_history_user_time
    ON memory_node_history (user_id, valid_to DESC);

-- memory_edges
CREATE INDEX IF NOT EXISTS idx_memory_edges_from
    ON memory_edges (from_node_id, edge_type)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_edges_to
    ON memory_edges (to_node_id, edge_type)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_edges_user_type
    ON memory_edges (user_id, edge_type)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_edges_dgraph_pending
    ON memory_edges (updated_at)
    WHERE dgraph_uid IS NULL AND deleted_at IS NULL;

-- memory_embeddings
CREATE INDEX IF NOT EXISTS idx_memory_embeddings_user
    ON memory_embeddings (user_id);

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_model
    ON memory_embeddings (model);

-- HNSW vector index (nomic-embed-text 768d; pgvector recommendation m=16, ef_construction=64)
CREATE INDEX IF NOT EXISTS idx_memory_embeddings_hnsw
    ON memory_embeddings USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- cold_storage_refs
CREATE INDEX IF NOT EXISTS idx_cold_storage_refs_node
    ON cold_storage_refs (node_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_cold_storage_refs_user_adapter
    ON cold_storage_refs (user_id, adapter)
    WHERE deleted_at IS NULL;

-- user_neuro_state
CREATE INDEX IF NOT EXISTS idx_user_neuro_state_activity
    ON user_neuro_state (last_activity_at);

-- outbox_events (queue + lookup)
CREATE INDEX IF NOT EXISTS idx_outbox_events_pending
    ON outbox_events (target_store, created_at)
    WHERE processed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_outbox_events_aggregate
    ON outbox_events (aggregate_type, aggregate_id);

-- engine_plugins — small table, no extra indexes needed beyond PK

COMMIT;
