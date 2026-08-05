# brain

A standalone, brain-like long-term memory engine for AI agents and CLIs. Extracted from [MyBrain](https://github.com/damonlinyz/mybrain) so any coding CLI (OpenCode, Codex, Aider, Claude Code, …) can share one persistent memory — switch CLI, memory follows.

## What it is

A cognitive memory hub implementing 17 biological memory mechanisms over PostgreSQL + pgvector:

- **Ingest path:** Builder (LLM fact extraction) → AttentionFilter → PatternSeparation (merge/link/new) → Weighter → RealityMonitor (multi-source trust)
- **Recall path:** Trigger (vector+keyword hybrid) → MetaCognition (confidence filter) → Interference (contradiction suppression) → ContextCompressor (budget)
- **Lifecycle:** Consolidation sleep job (decay → forgetting → extinction → link discovery) + Reconsolidation (correction loop) + Neuromodulator (dopamine reward) + Spatial (session binding)

Memory is **model-agnostic**: the embedder talks to any OpenAI-compatible `/embeddings` endpoint (Ollama by default), and the gateway forwards chat to whatever LLM the CLI is configured to use.

## Packages

```
memory/
  types/        domain types (MemoryNode, edges, context, plugin contract)
  store/        IMemoryStore + PGStore (Postgres + pgvector, HNSW 768d)
  embedder/     Embedder interface + OllamaAdapter + standalone OllamaHTTP + Redis cache
  engine/       plugin Registry + Engine (category-sorted weighted dispatch)
  eventbus/     in-process pub/sub
  hub/          orchestrator: Ingest / Recall / Correct / Reward / BindSession / RunConsolidationAll
  plugins/      17 mechanism plugins (attention, weighter, trigger, … + G1-G7)
  migrate/      V1→V2 data migration tool (idempotent, ledger-backed)
migrations/     041-046 (tables, HNSW, RLS, seed, ledger)
```

## Status

Standalone extraction — builds + all 19 test packages green. The gateway endpoints (OpenAI `/v1/chat/completions` + Anthropic `/v1/messages`) and CLI (`gateway-config`, `migrate-v2`) currently live in the MyBrain monolith and will be ported here next.

## Roadmap (additive, doesn't break the 17 mechanisms)

1. Readable/editable memory layer (Markdown export — humans can read/edit/delete).
2. Pinned/core memory tier (always-injected, not probabilistic recall).
3. One-fact-per-record + human-readable index (à la CLAUDE.md MEMORY.md).
4. Observability: recall "explain" view (which N nodes, why — for debugging).
5. Deterministic fallback (keyword/core injection when vector recall is empty/low-confidence).

## License

TBD (open-source after optimization).
