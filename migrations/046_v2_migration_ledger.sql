-- Idempotency ledger for the V1→V2 data migration (migrate package).
-- One row per successfully migrated V1 record, so re-running the migration
-- (or resuming after a crash) skips records already moved. PK (source_table,
-- source_id) makes the skip check a fast point lookup.

CREATE TABLE IF NOT EXISTS v2_migration_ledger (
    source_table TEXT NOT NULL,
    source_id    UUID NOT NULL,
    node_id      UUID NOT NULL REFERENCES memory_node_meta(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    migrated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_table, source_id)
);

CREATE INDEX IF NOT EXISTS idx_v2_migration_ledger_user
    ON v2_migration_ledger (user_id, source_table);
