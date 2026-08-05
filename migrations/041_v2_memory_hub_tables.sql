-- V2 Memory Hub — 8 tables (full scope schema, MVP-enabled subset seeded by 044).
-- Reconciled: vector dim 768 (nomic-embed-text via Ollama), FK -> users(id).
-- tenant_id columns kept nullable for Plan 7 multi-tenant; dgraph_uid columns for Plan 2.

BEGIN;

CREATE TABLE IF NOT EXISTS memory_node_meta (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id       UUID,

    -- Content
    content         TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    content_type    VARCHAR(64) NOT NULL DEFAULT 'fact',   -- fact/preference/event/relationship/skill/historical_version
    keywords        TEXT[] NOT NULL DEFAULT '{}',

    -- Source
    source          VARCHAR(64) NOT NULL DEFAULT 'inference',  -- human_input/search_result/inference/training/user_correction
    sources_json    JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Memory classification (V2 type system)
    type            VARCHAR(32) NOT NULL DEFAULT 'semantic',   -- semantic/episodic/procedural/profile
    salience        VARCHAR(16) NOT NULL DEFAULT 'normal' CHECK (salience IN ('high','normal','low')),
    emotional_tone  VARCHAR(32) NOT NULL DEFAULT 'neutral',

    -- State machine
    state           VARCHAR(32) NOT NULL DEFAULT 'active',     -- active/suppressed/archived/extinct
    weight          DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (weight BETWEEN 0.0 AND 1.0),
    source_trust    DOUBLE PRECISION NOT NULL DEFAULT 0.5,

    -- Access & decay
    access_count    INTEGER NOT NULL DEFAULT 0,
    last_access_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    version         INTEGER NOT NULL DEFAULT 1,

    -- Context binding
    session_id      UUID REFERENCES chat_sessions(id) ON DELETE SET NULL,
    activity_context TEXT,
    scene_context   TEXT,

    -- Neuromodulator accumulators (Plan 1 G7)
    cumulative_reward   DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    emotion_valence     DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    emotion_arousal     DOUBLE PRECISION NOT NULL DEFAULT 0.0,

    -- Metacognition (Plan 1 G4)
    confidence      DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (confidence BETWEEN 0.0 AND 1.0),
    consistency_score DOUBLE PRECISION NOT NULL DEFAULT 1.0 CHECK (consistency_score BETWEEN 0.0 AND 1.0),

    -- Reconsolidation (Plan 1 G1)
    unstable_until  TIMESTAMPTZ,

    -- Cold storage pointer (Plan 3)
    cold_ref        TEXT,

    -- DGraph sync (Plan 2)
    dgraph_uid      VARCHAR(128),
    dgraph_synced_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS memory_node_history (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    current_node_id     UUID NOT NULL REFERENCES memory_node_meta(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    content             TEXT NOT NULL,
    content_type        VARCHAR(64) NOT NULL,
    source              VARCHAR(64) NOT NULL,
    weight              DOUBLE PRECISION,

    valid_from          TIMESTAMPTZ NOT NULL,
    valid_to            TIMESTAMPTZ NOT NULL,
    superseded_by_event VARCHAR(128) NOT NULL,    -- correction/consolidation/forgetting
    change_summary      TEXT,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memory_edges (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id       UUID,

    from_node_id    UUID NOT NULL REFERENCES memory_node_meta(id) ON DELETE CASCADE,
    to_node_id      UUID NOT NULL REFERENCES memory_node_meta(id) ON DELETE CASCADE,

    edge_type       VARCHAR(64) NOT NULL,     -- related/discovered_link/temporal/causal/contradicts/supersedes/similar_to
    kind            VARCHAR(64) NOT NULL DEFAULT 'related',  -- alias kept for V2 type compat
    weight          DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (weight BETWEEN 0.0 AND 1.0),
    discovered_by   VARCHAR(64),              -- pattern_separation/consolidation/explicit/inference
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    dgraph_uid      VARCHAR(128),
    dgraph_synced_at TIMESTAMPTZ,

    UNIQUE (from_node_id, to_node_id, edge_type)
);

CREATE TABLE IF NOT EXISTS memory_embeddings (
    node_id     UUID PRIMARY KEY REFERENCES memory_node_meta(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id   UUID,

    embedding   VECTOR(768) NOT NULL,         -- nomic-embed-text via Ollama (reconciled from bge-m3 1024)
    model       VARCHAR(64) NOT NULL DEFAULT 'nomic-embed-text',
    dim         INTEGER NOT NULL DEFAULT 768,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cold_storage_refs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id       UUID,
    node_id         UUID NOT NULL REFERENCES memory_node_meta(id) ON DELETE CASCADE,

    adapter         VARCHAR(64) NOT NULL,     -- local/github/obsidian/notion
    location        TEXT NOT NULL,
    format          VARCHAR(32) NOT NULL DEFAULT 'md',
    size_bytes      BIGINT,
    checksum        VARCHAR(128),

    archived_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS engine_plugins (
    name            VARCHAR(128) PRIMARY KEY,
    category        VARCHAR(32) NOT NULL,     -- edge/engine/store/cold
    layer           VARCHAR(32) NOT NULL DEFAULT 'engine',  -- compat alias
    version         VARCHAR(64) NOT NULL DEFAULT '0.1.0',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    weight          DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    config          JSONB NOT NULL DEFAULT '{}'::jsonb,
    description     TEXT NOT NULL DEFAULT '',
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_neuro_state (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    neuro_type  VARCHAR(32) NOT NULL,         -- dopamine/ach/glutamate/serotonin
    level       DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (level BETWEEN 0.0 AND 1.0),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_consolidation_at TIMESTAMPTZ,
    consolidation_count INTEGER NOT NULL DEFAULT 0,
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, neuro_type)
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    user_id         UUID,
    aggregate_type  VARCHAR(64) NOT NULL,     -- memory_node/memory_edge
    aggregate_id    UUID NOT NULL,
    event_type      VARCHAR(64) NOT NULL,     -- created/updated/deleted/archived
    payload         JSONB NOT NULL,
    target_store    VARCHAR(32) NOT NULL,     -- dgraph/redis/cold
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ,
    retry_count     INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT
);

-- updated_at auto-maintenance trigger (shared function; idempotent)
CREATE OR REPLACE FUNCTION trg_set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_memory_node_meta_updated_at ON memory_node_meta;
CREATE TRIGGER trg_memory_node_meta_updated_at
    BEFORE UPDATE ON memory_node_meta
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

DROP TRIGGER IF EXISTS trg_memory_edges_updated_at ON memory_edges;
CREATE TRIGGER trg_memory_edges_updated_at
    BEFORE UPDATE ON memory_edges
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

DROP TRIGGER IF EXISTS trg_memory_embeddings_updated_at ON memory_embeddings;
CREATE TRIGGER trg_memory_embeddings_updated_at
    BEFORE UPDATE ON memory_embeddings
    FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

COMMIT;
