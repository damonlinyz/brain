-- 047_pinned_core_tier.sql
-- Pinned-core always-inject tier (improvement #2).
--
-- A node's tier controls whether it is recall-gated (normal) or always injected
-- (core). Core-tier nodes bypass similarity scoring in Hub.Recall — they are
-- foundational facts the owner pinned as always-on context (like a personal
-- CLAUDE.md). All existing nodes default to 'normal'.

ALTER TABLE memory_node_meta
    ADD COLUMN IF NOT EXISTS tier VARCHAR(16) NOT NULL DEFAULT 'normal'
        CHECK (tier IN ('normal', 'core'));

-- Core nodes should be cheap to surface (small per user). Index the tier so the
-- Recall core-fetch stays an index scan.
CREATE INDEX IF NOT EXISTS idx_memory_node_tier
    ON memory_node_meta (user_id, tier)
    WHERE deleted_at IS NULL AND tier = 'core';

INSERT INTO schema_migrations (version) VALUES ('047') ON CONFLICT DO NOTHING;
