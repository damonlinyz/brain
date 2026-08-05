-- V2 Memory Hub RLS policies — DISABLED by default for single-tenant (current) deployment.
-- Plan 7 (multi-tenant) will flip the switch via ALTER TABLE ... ENABLE ROW LEVEL SECURITY.
-- Policies exist so the enable path is a one-liner; no schema changes needed at flip time.

BEGIN;

-- memory_node_meta
ALTER TABLE memory_node_meta ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_node_meta FORCE ROW LEVEL SECURITY;
CREATE POLICY memory_node_tenant_isolation ON memory_node_meta
    USING (tenant_id IS NULL OR tenant_id::text = current_setting('app.tenant_id', true));
ALTER TABLE memory_node_meta DISABLE ROW LEVEL SECURITY;

-- memory_edges
ALTER TABLE memory_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_edges FORCE ROW LEVEL SECURITY;
CREATE POLICY memory_edges_tenant_isolation ON memory_edges
    USING (tenant_id IS NULL OR tenant_id::text = current_setting('app.tenant_id', true));
ALTER TABLE memory_edges DISABLE ROW LEVEL SECURITY;

-- memory_embeddings
ALTER TABLE memory_embeddings ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_embeddings FORCE ROW LEVEL SECURITY;
CREATE POLICY memory_embeddings_tenant_isolation ON memory_embeddings
    USING (tenant_id IS NULL OR tenant_id::text = current_setting('app.tenant_id', true));
ALTER TABLE memory_embeddings DISABLE ROW LEVEL SECURITY;

-- memory_node_history (inherits via JOIN to memory_node_meta)
ALTER TABLE memory_node_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_node_history FORCE ROW LEVEL SECURITY;
CREATE POLICY memory_node_history_user_isolation ON memory_node_history
    USING (EXISTS (
        SELECT 1 FROM memory_node_meta n
         WHERE n.id = memory_node_history.current_node_id
           AND (n.tenant_id IS NULL OR n.tenant_id::text = current_setting('app.tenant_id', true))
    ));
ALTER TABLE memory_node_history DISABLE ROW LEVEL SECURITY;

-- cold_storage_refs
ALTER TABLE cold_storage_refs ENABLE ROW LEVEL SECURITY;
ALTER TABLE cold_storage_refs FORCE ROW LEVEL SECURITY;
CREATE POLICY cold_storage_refs_tenant_isolation ON cold_storage_refs
    USING (tenant_id IS NULL OR tenant_id::text = current_setting('app.tenant_id', true));
ALTER TABLE cold_storage_refs DISABLE ROW LEVEL SECURITY;

-- user_neuro_state (keyed by user_id directly, no tenant column — policy keeps RLS-off shape)
-- No RLS policy needed; table is user_id PK, filtered explicitly by service layer.

COMMIT;
