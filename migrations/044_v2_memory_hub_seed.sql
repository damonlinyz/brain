-- V2 Memory Hub — seed 27 engine_plugins (19 mechanisms + store + 4 cold storage + enhancements).
-- MVP-active set marked enabled=true; full-scope plugins enabled=false until their Plan lands.
-- NOTE: Reconsolidation (G1) is implemented and enabled; ColdStorage.local is NOT
-- implemented (Plan 3) and disabled — see migration 045 for the reconciliation.

BEGIN;

INSERT INTO engine_plugins (name, category, layer, enabled, weight, config, description) VALUES
    -- Edge layer (5)
    ('SignalCollector',     'edge', 'edge', false, 1.0, '{"useLLM":false}'::jsonb, 'Physiological/affective signal collection (Plan 4: LLM valence scoring)'),
    ('AttentionFilter',     'edge', 'edge', true,  1.0, '{"weights":{"explicit":0.95,"salience":0.2,"emotion":0.15,"dopamine":0.1,"trust":0.1},"thresholds":{"remember":0.6,"defer":0.3}}'::jsonb, 'AttentionFilter (rule-based)'),
    ('WorkingMemory',       'edge', 'edge', true,  1.0, '{"capacity":9,"ttlSeconds":300}'::jsonb, 'WorkingMemory in-process map'),
    ('ContextCompressor',   'edge', 'edge', true,  1.0, '{"minBudget":200,"maxBudget":4000}'::jsonb, 'Compresses memories into prompt budget'),
    ('Neuromodulator',      'edge', 'edge', true, 1.0, '{"dopamineDecayPerHour":0.05}'::jsonb, 'Dopamine/serotonin analog state (Plan 1 G7)'),
    -- Engine layer (13 mechanisms + 3 enhancements)
    ('Builder',             'engine', 'engine', true,  1.0, '{"llmTimeoutMs":2500,"fallbackOnLLMFail":true,"extractorModel":"deepseek-chat"}'::jsonb, 'Builds memory nodes from material'),
    ('Weighter',            'engine', 'engine', true,  1.0, '{"tauDays":7,"minWeight":0.05}'::jsonb, 'Ebbinghaus weight decay'),
    ('PatternSeparation',   'engine', 'engine', true,  1.0, '{"mergeSim":0.9,"linkSim":0.7}'::jsonb, 'Similarity detection (merge/link/new)'),
    ('Trigger',             'engine', 'engine', true,  1.0, '{"topK":10,"scoreFloor":0.4,"keywordWeight":0.3}'::jsonb, 'Recalls relevant memories for current flow'),
    ('Forgetting',          'engine', 'engine', true,  1.0, '{"suppressThreshold":0.1,"archiveThreshold":0.05,"confirmTtlSeconds":300}'::jsonb, 'Three-stage forgetting (suppress/archive/delete)'),
    ('Consolidation',       'engine', 'engine', true,  1.0, '{"inactivityDefaultSeconds":14400,"minSeconds":10800,"maxSeconds":36000,"shardPerMinute":60}'::jsonb, 'Sleep consolidation + decay + link discovery'),
    ('Reconsolidation',     'engine', 'engine', true, 1.0, '{"unstableWindowSeconds":86400}'::jsonb, 'Re-consolidation on retrieval (G1 implemented)'),
    ('Spatial',             'engine', 'engine', true, 1.0, '{}'::jsonb, 'Spatial context binding (Plan 1 G2)'),
    ('RealityMonitor',      'engine', 'engine', true, 1.0, '{"multiSourceBonus":0.2}'::jsonb, 'Multi-source reality check (G3 implemented)'),
    ('MetaCognition',       'edge', 'edge', true, 1.0, '{"confidentThreshold":0.7,"askThreshold":0.4,"blurryMaxAgeDays":30}'::jsonb, 'Meta-cognition confidence assessment (G4 implemented)'),
    ('Interference',        'engine', 'engine', true, 1.0, '{"conflictThreshold":0.6}'::jsonb, 'Interference competition (G5 implemented)'),
    ('Extinction',          'engine', 'engine', true, 1.0, '{"suppressDurationSeconds":2592000}'::jsonb, 'Extinction learning (Plan 1 G6)'),
    -- Enhancements (Plan 1 G8/G9 + others)
    ('ContextCompressor_LLM',           'engine', 'engine', false, 0.5, '{}'::jsonb, 'LLM-assisted intent assessment (Plan 4)'),
    ('TriggerRecencyBoost',             'engine', 'engine', false, 0.3, '{}'::jsonb, 'Temporal recency boost'),
    ('PatternSeparationDedup',          'engine', 'engine', false, 0.3, '{}'::jsonb, 'Strict dedup at recall'),
    ('BuilderContradiction',            'engine', 'engine', false, 0.3, '{}'::jsonb, 'Contradiction detection'),
    ('WeighterReinforceEmotion',        'engine', 'engine', false, 0.3, '{}'::jsonb, 'Emotional reinforcement'),
    -- Store (1)
    ('MemoryStore',         'store', 'store', true, 1.0, '{"dgraphAsync":true}'::jsonb, 'PG+pgvector memory store (DGraph optional via Plan 2)'),
    -- Cold storage (4 — only local enabled)
    ('ColdStorage.local',   'cold', 'cold', false, 1.0, '{"root":"./.coldstore"}'::jsonb, 'Local filesystem cold storage adapter (Plan 3, not yet implemented)'),
    ('ColdStorage.github',  'cold', 'cold', false, 1.0, '{}'::jsonb, 'GitHub cold storage adapter (Plan 3)'),
    ('ColdStorage.obsidian','cold', 'cold', false, 1.0, '{}'::jsonb, 'Obsidian cold storage adapter (Plan 3)'),
    ('ColdStorage.notion',  'cold', 'cold', false, 1.0, '{}'::jsonb, 'Notion cold storage adapter (Plan 3)')
ON CONFLICT (name) DO NOTHING;

COMMIT;
