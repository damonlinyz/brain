// Package hub is the V2 memory hub orchestrator. It wires the store, embedder,
// and the MVP plugins (Builder → Attention → PatternSeparation → Weighter on the
// ingest path; Trigger → Compressor on the recall path) behind a small facade
// that the HTTP layer and the chat-service shadow hook both call.
//
// Hub is safe for concurrent use. It is intentionally transport-agnostic: no
// HTTP, no LLM coupling beyond what Builder already abstracts.
package hub

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/damonlinyz/brain/memory/embedder"
	"github.com/damonlinyz/brain/memory/eventbus"
	"github.com/damonlinyz/brain/memory/plugins/attention"
	"github.com/damonlinyz/brain/memory/plugins/builder"
	"github.com/damonlinyz/brain/memory/plugins/compressor"
	"github.com/damonlinyz/brain/memory/plugins/consolidation"
	"github.com/damonlinyz/brain/memory/plugins/extinction"
	"github.com/damonlinyz/brain/memory/plugins/forgetting"
	"github.com/damonlinyz/brain/memory/plugins/interference"
	"github.com/damonlinyz/brain/memory/plugins/metacognition"
	"github.com/damonlinyz/brain/memory/plugins/neuromodulator"
	"github.com/damonlinyz/brain/memory/plugins/patternsep"
	"github.com/damonlinyz/brain/memory/plugins/realitymonitor"
	"github.com/damonlinyz/brain/memory/plugins/reconsolidation"
	"github.com/damonlinyz/brain/memory/plugins/spatial"
	"github.com/damonlinyz/brain/memory/plugins/trigger"
	"github.com/damonlinyz/brain/memory/plugins/weighter"
	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Hub owns the plugin instances and the store. Construct once at app start.
type Hub struct {
	store         store.IMemoryStore
	embedder      embedder.Embedder
	bus           *eventbus.Bus
	builder       *builder.Plugin
	attention     *attention.Plugin
	patternsep    *patternsep.Plugin
	trigger       *trigger.Plugin
	compressor    *compressor.Plugin
	weighter        *weighter.Plugin
	consolidation   *consolidation.Plugin
	reconsolidation *reconsolidation.Plugin
	realitymonitor  *realitymonitor.Plugin
	metacognition   *metacognition.Plugin
	interference    *interference.Plugin
	extinction       *extinction.Plugin
	neuromodulator   *neuromodulator.Plugin
	spatial          *spatial.Plugin
	logger           *slog.Logger
	now           func() time.Time // injectable clock for tests
}

// Deps bundles the constructor inputs.
type Deps struct {
	Store     store.IMemoryStore
	Embedder  embedder.Embedder
	Bus       *eventbus.Bus // optional; nil → no events emitted
	Logger    *slog.Logger
	// Plugins may be nil — Hub constructs defaults so callers can override
	// individual plugins (e.g. to share an LLM-backed builder).
	Builder    *builder.Plugin
	Attention  *attention.Plugin
	PatternSep *patternsep.Plugin
	Trigger    *trigger.Plugin
	Compressor *compressor.Plugin
	Weighter   *weighter.Plugin
	// Consolidation is optional; if nil, RunConsolidationAll is a no-op. The
	// engine layer should pass one wired with WithWeighter + WithForgetting.
	Consolidation *consolidation.Plugin
	// Reconsolidation is optional; if nil, Correct() is disabled and recalled
	// nodes are not marked unstable.
	Reconsolidation *reconsolidation.Plugin
	// RealityMonitor is optional; if nil, merge doesn't record multi-source trust.
	RealityMonitor *realitymonitor.Plugin
	// MetaCognition is optional; if nil, recall doesn't filter by confidence.
	MetaCognition *metacognition.Plugin
	// Interference is optional; if nil, recall doesn't detect contradictions.
	Interference *interference.Plugin
	// Extinction is optional; if nil, low-weight nodes aren't marked extinct.
	Extinction *extinction.Plugin
	// Neuromodulator is optional; pool-injected, no Hub default (pool comes from appcore).
	Neuromodulator *neuromodulator.Plugin
	// Spatial is optional; if nil, BindSession is a no-op.
	Spatial *spatial.Plugin
}

// New constructs a Hub with default plugins where the caller didn't supply one.
// It does NOT call Init on the plugins — the engine layer owns DB-backed config.
// If you construct a Hub directly (not via engine), call InitDefaults() once.
func New(d Deps) *Hub {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Builder == nil {
		d.Builder = builder.New()
	}
	if d.Attention == nil {
		d.Attention = attention.New()
	}
	if d.PatternSep == nil {
		d.PatternSep = patternsep.New()
	}
	if d.Trigger == nil {
		d.Trigger = trigger.New()
	}
	if d.Compressor == nil {
		d.Compressor = compressor.New()
	}
	if d.Weighter == nil {
		d.Weighter = weighter.New()
	}
	if d.Consolidation == nil {
		d.Consolidation = consolidation.New(
			consolidation.WithWeighter(d.Weighter),
			consolidation.WithForgetting(forgetting.New()),
		)
	}
	if d.Reconsolidation == nil {
		d.Reconsolidation = reconsolidation.New()
	}
	if d.RealityMonitor == nil {
		d.RealityMonitor = realitymonitor.New()
	}
	if d.MetaCognition == nil {
		d.MetaCognition = metacognition.New()
	}
	if d.Interference == nil {
		d.Interference = interference.New()
	}
	if d.Extinction == nil {
		d.Extinction = extinction.New()
	}
	if d.Spatial == nil {
		d.Spatial = spatial.New()
	}
	return &Hub{
		store:           d.Store,
		embedder:        d.Embedder,
		bus:             d.Bus,
		builder:         d.Builder,
		attention:       d.Attention,
		patternsep:      d.PatternSep,
		trigger:         d.Trigger,
		compressor:      d.Compressor,
		weighter:        d.Weighter,
		consolidation:   d.Consolidation,
		reconsolidation: d.Reconsolidation,
		realitymonitor:  d.RealityMonitor,
		metacognition:   d.MetaCognition,
		interference:    d.Interference,
		extinction:      d.Extinction,
		neuromodulator:  d.Neuromodulator,
		spatial:         d.Spatial,
		logger:          d.Logger,
		now:             time.Now,
	}
}

// InitDefaults calls Init({}) on every plugin so non-engine callers get sane
// defaults. Safe to call once; later Init calls from the engine override.
func (h *Hub) InitDefaults() error {
	for _, p := range []interface{ Init(map[string]any) error }{
		h.builder, h.attention, h.patternsep, h.trigger, h.compressor, h.weighter,
		h.consolidation, h.reconsolidation, h.realitymonitor, h.metacognition, h.interference, h.extinction, h.spatial,
	} {
		if err := p.Init(map[string]any{}); err != nil {
			return err
		}
	}
	return nil
}

// --- Ingest path -------------------------------------------------------

// IngestInput is one turn of raw material handed to the hub.
type IngestInput struct {
	UserID   uuid.UUID
	TenantID *uuid.UUID
	// SessionID is optional; links stored nodes back to a conversation.
	SessionID       *uuid.UUID
	RawText         string
	Source          types.Source
	SceneContext    string
	ActivityContext string
	// DopamineLevel feeds AttentionFilter (0..1). 0 if unknown.
	DopamineLevel float64
	// SourceTrust feeds AttentionFilter per-fact (0..1). Defaults to 0.5.
	SourceTrust float64
}

// StoredNode records what happened to one extracted fact.
type StoredNode struct {
	Action   patternsep.Decision // new / link / merge
	NodeID   uuid.UUID           // the persisted node (created or merged-into)
	Summary  string
	Sim      float64 // similarity to best match (0 if new)
	LinkedTo *uuid.UUID
}

// IngestResult summarises a single ingest call.
type IngestResult struct {
	Facts   []builder.ExtractedFact
	Stored  []StoredNode
	Dropped int // facts below the attention "remember" threshold
	Skipped int // facts that errored during persist
}

// Ingest runs the full write path: extract facts → score each → embed →
// pattern-separate (merge/link/new) → persist. Facts that AttentionFilter marks
// "drop" or "defer" are skipped. Errors on a single fact don't abort the batch.
func (h *Hub) Ingest(ctx context.Context, in IngestInput) (IngestResult, error) {
	res := IngestResult{}

	if h.store == nil {
		return res, ErrNotConfigured
	}
	if in.SourceTrust == 0 {
		in.SourceTrust = 0.5
	}

	facts, err := h.builder.Extract(ctx, builder.ExtractInput{
		UserID:          in.UserID.String(),
		RawText:         in.RawText,
		Source:          in.Source,
		SceneContext:    in.SceneContext,
		ActivityContext: in.ActivityContext,
	})
	if err != nil {
		return res, err
	}
	res.Facts = facts

	for _, fact := range facts {
		// Attention gate — only persist what's worth remembering.
		sig := h.attention.Assess(ctx, attention.ScoreInput{
			Content:       fact.Content,
			Salience:      fact.Salience,
			SourceTrust:   in.SourceTrust,
			DopamineLevel: in.DopamineLevel,
		})
		if sig.Decision != types.DecisionRemember {
			res.Dropped++
			continue
		}

		sn, err := h.persistFact(ctx, in, fact, sig)
		if err != nil {
			h.logger.Warn("memory ingest: persist failed", "summary", fact.Summary, "error", err)
			res.Skipped++
			continue
		}
		res.Stored = append(res.Stored, sn)
	}

	if h.bus != nil && len(res.Stored) > 0 {
		h.bus.Publish(ctx, eventbus.Event{
			Topic: TopicMemoryIngested,
			Payload: map[string]any{
				"user_id": in.UserID.String(),
				"count":   len(res.Stored),
			},
		})
	}
	return res, nil
}

func (h *Hub) persistFact(ctx context.Context, in IngestInput, fact builder.ExtractedFact, sig types.AttentionSignal) (StoredNode, error) {
	sn := StoredNode{Summary: fact.Summary}

	// Embed once — used both for pattern-separation and for storage.
	emb, err := h.embed(ctx, fact.Content)
	if err != nil {
		return sn, err
	}

	// Pattern separation: merge into / link to / create new.
	outcome, err := h.patternsep.Evaluate(ctx, h.store, patternsep.EvaluateInput{
		UserID:    in.UserID,
		TenantID:  in.TenantID,
		Embedding: emb,
		TypeFilter: []types.MemoryType{fact.Type},
	})
	if err != nil {
		return sn, err
	}
	sn.Sim = outcome.Similarity

	switch outcome.Decision {
	case patternsep.DecisionMerge:
		if outcome.ExistingNode == nil {
			break // defensive — Evaluate shouldn't return Merge with nil node
		}
		merged, err := h.mergeInto(ctx, *outcome.ExistingNode, fact, sig, in)
		if err != nil {
			return sn, err
		}
		sn.Action = patternsep.DecisionMerge
		sn.NodeID = merged.ID
		return sn, nil

	case patternsep.DecisionLink:
		if outcome.ExistingNode == nil {
			break
		}
		created, err := h.createNode(ctx, in, fact, sig, emb)
		if err != nil {
			return sn, err
		}
		// Bidirectional similar_to edge.
		if _, err := h.store.CreateEdge(ctx, types.CreateEdgeInput{
			UserID:       in.UserID,
			TenantID:     in.TenantID,
			FromNodeID:   created.ID,
			ToNodeID:     outcome.ExistingNode.ID,
			EdgeType:     types.EdgeKindSimilarTo,
			Weight:       outcome.Similarity,
			DiscoveredBy: types.DiscovererPatternSeparation,
		}); err != nil {
			h.logger.Warn("memory ingest: edge create failed", "from", created.ID, "error", err)
		}
		sn.Action = patternsep.DecisionLink
		sn.NodeID = created.ID
		existing := outcome.ExistingNode.ID
		sn.LinkedTo = &existing
		return sn, nil
	}

	// Default: brand-new node.
	created, err := h.createNode(ctx, in, fact, sig, emb)
	if err != nil {
		return sn, err
	}
	sn.Action = patternsep.DecisionNew
	sn.NodeID = created.ID
	return sn, nil
}

func (h *Hub) createNode(ctx context.Context, in IngestInput, fact builder.ExtractedFact, sig types.AttentionSignal, emb []float32) (types.MemoryNode, error) {
	initialWeight := h.weighter.Reinforce(types.MemoryNode{Weight: 0.3}, 1.0)
	return h.store.CreateNode(ctx, types.CreateNodeInput{
		UserID:          in.UserID,
		TenantID:        in.TenantID,
		SessionID:       in.SessionID,
		Content:         fact.Content,
		Summary:         fact.Summary,
		ContentType:     fact.ContentType,
		Keywords:        fact.Keywords,
		Embedding:       emb,
		Source:          in.Source,
		Type:            fact.Type,
		Salience:        fact.Salience,
		EmotionalTone:   fact.EmotionalTone,
		SourceTrust:     in.SourceTrust,
		Weight:          initialWeight,
		ActivityContext: in.ActivityContext,
		SceneContext:    in.SceneContext,
	})
}

// mergeInto reinforces the existing node and appends the new content to its
// summary. We do not overwrite — the merged weight reflects both access and
// attention signal.
func (h *Hub) mergeInto(ctx context.Context, existing types.MemoryNode, fact builder.ExtractedFact, sig types.AttentionSignal, in IngestInput) (types.MemoryNode, error) {
	newWeight := h.weighter.Reinforce(existing, 0.5)

	// Multi-source corroboration: record the new source and compute aggregate
	// trust before the update so the mutator can set the new values.
	var origSources []types.SourceEntry
	var consistency, confidence float64
	if h.realitymonitor != nil {
		origSources = existing.Sources // snapshot before the mutator runs
		now := h.now()
		origSources, consistency, confidence = h.realitymonitor.AddSource(existing.Sources, types.SourceEntry{
			Source: in.Source, Trust: in.SourceTrust, AddedAt: now,
		})
	}

	return h.store.UpdateNode(ctx, existing.ID, existing.Version, func(n *types.MemoryNode) {
		n.Weight = newWeight
		n.AccessCount = n.AccessCount + 1
		n.LastAccessAt = h.now()
		if len(fact.Summary) > len(n.Summary) {
			n.Summary = fact.Summary
		}
		if h.realitymonitor != nil {
			n.Sources = origSources
			n.ConsistencyScore = consistency
			n.Confidence = confidence
		}
		_ = sig
	})
}

// extractKeywords splits a query into simple keyword tokens.
func extractKeywords(query string) []string {
	words := strings.Fields(query)
	out := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, ",.;:!?'\"()[]{}")
		if len([]rune(w)) >= 2 {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

func (h *Hub) embed(ctx context.Context, content string) ([]float32, error) {
	if h.embedder == nil {
		return nil, ErrNotConfigured
	}
	return h.embedder.Embed(ctx, content)
}

// --- Recall path -------------------------------------------------------

// RecallInput is one recall request.
type RecallInput struct {
	UserID        uuid.UUID
	TenantID      *uuid.UUID
	Query         string
	Keywords      []string
	TypeFilter    []types.MemoryType
	TopK          int
	DesiredBudget int // 0 → use Compressor maxBudget default
}

// Recall embeds the query, runs hybrid search, and renders a CompressedContext
// ready to inject into an LLM prompt. Returns an empty context (not an error)
// when nothing clears the score floor.
func (h *Hub) Recall(ctx context.Context, in RecallInput) (types.CompressedContext, error) {
	empty := types.CompressedContext{SystemPrompt: ""}
	if h.store == nil {
		return empty, ErrNotConfigured
	}

	emb, err := h.embed(ctx, in.Query)
	if err != nil {
		return empty, err
	}

	results, err := h.trigger.Recall(ctx, h.store, trigger.RecallInput{
		UserID:     in.UserID,
		TenantID:   in.TenantID,
		Embedding:  emb,
		Keywords:   in.Keywords,
		TypeFilter: in.TypeFilter,
		TopK:       in.TopK,
	})
	if err != nil {
		return empty, err
	}

	// Deterministic fallback: if vector recall returns nothing, try keywords.
	if len(results) == 0 && in.Query != "" {
		kwResults, kwErr := h.store.SearchByKeywords(ctx, in.UserID, extractKeywords(in.Query), in.TopK)
		if kwErr == nil && len(kwResults) > 0 {
			results = kwResults
			h.logger.Debug("v2 recall: vector empty, fell back to keywords", "hits", len(results))
		}
	}

	candidates := make([]types.TriggerCandidate, 0, len(results))
	recalledIDs := make([]uuid.UUID, 0, len(results))
	for _, r := range results {
		candidates = append(candidates, types.TriggerCandidate{
			NodeID:  r.Node.ID.String(),
			Summary: r.Node.Summary,
			Score:   r.Sim,
			Source:  "hybrid",
		})
		recalledIDs = append(recalledIDs, r.Node.ID)
	}

	// Open a reconsolidation window on surfaced nodes (fire-and-forget).
	h.markRecalledUnstable(recalledIDs)

	// G4 MetaCognition: assess recall confidence, filter out unreliable nodes.
	liveInjectOK := true
	if h.metacognition != nil {
		nodes := make([]types.MemoryNode, 0, len(results))
		for _, r := range results {
			nodes = append(nodes, r.Node)
		}
		report := h.metacognition.Assess(metacognition.AssessInput{Nodes: nodes, Now: h.now()})
		liveInjectOK = report.LiveInjectOK
		keep := map[string]bool{}
		for _, a := range report.Assessments {
			if a.Level == metacognition.Confident || a.Level == metacognition.Hedge {
				keep[a.NodeID.String()] = true
			}
		}
		filtered := make([]types.TriggerCandidate, 0, len(candidates))
		for _, c := range candidates {
			if keep[c.NodeID] {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}

	// G5 Interference: detect contradictions among surviving candidates.
	if h.interference != nil && len(candidates) > 1 {
		// Collect surviving nodes + their scores.
		scoreMap := map[uuid.UUID]float64{}
		for _, r := range results {
			scoreMap[r.Node.ID] = r.Sim
		}
		surviving := make([]types.MemoryNode, 0, len(candidates))
		for _, c := range candidates {
			id, _ := uuid.Parse(c.NodeID)
			for _, r := range results {
				if r.Node.ID == id {
					surviving = append(surviving, r.Node)
					break
				}
			}
		}
		// Build pairwise similarity map (min-score proxy).
		pairSim := map[[2]uuid.UUID]float64{}
		for i := 0; i < len(surviving); i++ {
			for j := i + 1; j < len(surviving); j++ {
				a, b := surviving[i].ID, surviving[j].ID
				sim := scoreMap[a]
				if scoreMap[b] < sim {
					sim = scoreMap[b]
				}
				if a.String() < b.String() {
					pairSim[[2]uuid.UUID{a, b}] = sim
				} else {
					pairSim[[2]uuid.UUID{b, a}] = sim
				}
			}
		}
		report := h.interference.Analyze(surviving, pairSim)
		// Filter out suppressed nodes.
		if len(report.SuppressIDs) > 0 {
			suppress := map[uuid.UUID]bool{}
			for _, id := range report.SuppressIDs {
				suppress[id] = true
			}
			keep := make([]types.TriggerCandidate, 0, len(candidates))
			for _, c := range candidates {
				id, _ := uuid.Parse(c.NodeID)
				if !suppress[id] {
					keep = append(keep, c)
				}
			}
			candidates = keep
		}
	}

	// Graph neighbor expansion: for every surviving candidate, pull its
	// 1-hop neighbors via memory_edges and add their summaries as extra
	// "graph" candidates with a 0.8× base score. This enriches the recall
	// set with graph-related memories that vector search missed.
	if len(candidates) > 0 {
		survivingIDs := make([]uuid.UUID, 0, len(candidates))
		for _, c := range candidates {
			id, _ := uuid.Parse(c.NodeID)
			survivingIDs = append(survivingIDs, id)
		}
		neighbors, err := h.store.GetGraphNeighbors(ctx, in.UserID, survivingIDs, 1)
		if err == nil && len(neighbors) > 0 {
			for _, gn := range neighbors {
				candidates = append(candidates, types.TriggerCandidate{
					NodeID:  gn.Node.ID.String(),
					Summary: gn.Node.Summary,
					Score:   0.5 * gn.Edge.Weight, // graph relevance, damped
					Source:  "graph",
					Detail:  string(gn.Edge.EdgeType), // provenance: edge kind
				})
			}
			h.logger.Debug("v2 recall: graph neighbors enriched", "neighbors", len(neighbors), "candidates", len(candidates))
		}
	}

	cc := h.compressor.Compress(ctx, compressor.CompressInput{
		Candidates:    candidates,
		DesiredBudget: in.DesiredBudget,
	})
	cc.LiveInjectOK = liveInjectOK

	// [#2 pinned-core tier] Always-inject the owner's core-tier nodes. These
	// bypass similarity scoring — foundational facts pinned as always-on context.
	// They are prepended so they sit at the top of the prompt. Capped to keep the
	// slice bounded; errors are best-effort (a missing core fetch must not break recall).
	h.injectCore(ctx, in.UserID, &cc)

	return cc, nil
}

// injectCore prepends the owner's core-tier nodes to a CompressedContext. It is
// best-effort: any store error is logged and swallowed so recall never fails on
// the core fetch. Core nodes get Source="core", Tier="core".
func (h *Hub) injectCore(ctx context.Context, userID uuid.UUID, cc *types.CompressedContext) {
	results, err := h.store.ListNodes(ctx, store.SearchFilter{
		UserID: userID,
		Tiers:  []types.NodeTier{types.NodeTierCore},
		States: []types.NodeState{types.NodeStateActive},
		Limit:  10,
	})
	if err != nil {
		h.logger.Debug("v2 recall: core fetch failed (skipped)", "err", err)
		return
	}
	if len(results.Items) == 0 {
		return
	}
	refs := make([]types.MemoryRef, 0, len(results.Items))
	var block strings.Builder
	block.WriteString("\n## Core (always-on)\n")
	for _, n := range results.Items {
		refs = append(refs, types.MemoryRef{
			NodeID:    n.ID,
			Summary:   n.Summary,
			Relevance: 1.0,
			Source:    "core",
			Detail:    "pinned",
			Tier:      "core",
		})
		block.WriteString("- " + n.Summary + "\n")
	}
	// Prepend core refs + prompt block so core sits above recall-gated memories.
	cc.Memories = append(refs, cc.Memories...)
	cc.Sources = append(make([]string, len(refs)), cc.Sources...)
	for i := range refs {
		cc.Sources[i] = "core"
	}
	cc.SystemPrompt += block.String()
	h.logger.Debug("v2 recall: core injected", "core", len(refs))
}

// markRecalledUnstable opens a reconsolidation window on every surfaced node,
// fire-and-forget so it never adds latency to recall. Best-effort: errors are
// logged and swallowed. Skipped when no reconsolidation plugin is wired.
func (h *Hub) markRecalledUnstable(nodeIDs []uuid.UUID) {
	if h.reconsolidation == nil || len(nodeIDs) == 0 {
		return
	}
	go func(ids []uuid.UUID) {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		now := h.now()
		for _, id := range ids {
			if err := h.reconsolidation.MarkUnstable(bg, h.store, id, now); err != nil {
				h.logger.Debug("reconsolidation: mark unstable failed", "node_id", id, "error", err)
			}
		}
	}(nodeIDs)
}

// Correct applies a user correction to a node via the Reconsolidation plugin:
// snapshots the old content to history, overwrites it, and closes the unstable
// window. Returns ErrNotConfigured if no reconsolidation plugin is wired.
func (h *Hub) Correct(ctx context.Context, nodeID uuid.UUID, newContent, newSummary, reason string) (types.MemoryNode, error) {
	if h.store == nil || h.reconsolidation == nil {
		return types.MemoryNode{}, ErrNotConfigured
	}
	return h.reconsolidation.ApplyCorrection(ctx, h.store, reconsolidation.CorrectionInput{
		NodeID:        nodeID,
		NewContent:    newContent,
		NewSummary:    newSummary,
		ChangeSummary: reason,
	}, h.now())
}

// --- Reinforce ---------------------------------------------------------

// BindSession links a memory node to a conversation session.
func (h *Hub) BindSession(ctx context.Context, nodeID, sessionID uuid.UUID) error {
	if h.store == nil || h.spatial == nil {
		return ErrNotConfigured
	}
	return h.spatial.BindSession(ctx, h.store, nodeID, sessionID)
}

// Reward records a dopamine reward signal. Amount in [-1, 1].
func (h *Hub) Reward(ctx context.Context, userID uuid.UUID, amount float64, reason string) error {
	if h.neuromodulator == nil {
		return nil // no-op when neuromodulator not wired
	}
	return h.neuromodulator.Reward(ctx, h.bus, userID, amount, reason)
}

// Reinforce bumps a node's weight on access (e.g. it was cited in a response).
// Uses optimistic locking via the node's current version.
func (h *Hub) Reinforce(ctx context.Context, nodeID uuid.UUID, intensity float64) error {
	if h.store == nil {
		return ErrNotConfigured
	}
	node, err := h.store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	newWeight := h.weighter.Reinforce(node, intensity)
	_, err = h.store.UpdateNode(ctx, nodeID, node.Version, func(n *types.MemoryNode) {
		n.Weight = newWeight
		n.AccessCount = n.AccessCount + 1
		n.LastAccessAt = h.now()
	})
	return err
}

// SetTier pins (core) or unpins (normal) a node. Core-tier nodes are always
// injected by Recall regardless of similarity score. [#2 pinned-core tier]
func (h *Hub) SetTier(ctx context.Context, nodeID uuid.UUID, tier types.NodeTier) error {
	if h.store == nil {
		return ErrNotConfigured
	}
	if tier != types.NodeTierCore && tier != types.NodeTierNormal {
		return fmt.Errorf("hub: invalid tier %q", tier)
	}
	node, err := h.store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	_, err = h.store.UpdateNode(ctx, nodeID, node.Version, func(n *types.MemoryNode) {
		n.Tier = tier
	})
	return err
}

// --- Passthrough to store ----------------------------------------------

func (h *Hub) GetNode(ctx context.Context, nodeID uuid.UUID) (types.MemoryNode, error) {
	if h.store == nil {
		return types.MemoryNode{}, ErrNotConfigured
	}
	return h.store.GetNode(ctx, nodeID)
}

func (h *Hub) ListNodes(ctx context.Context, filter store.SearchFilter) (store.SearchResults, error) {
	if h.store == nil {
		return store.SearchResults{}, ErrNotConfigured
	}
	return h.store.ListNodes(ctx, filter)
}

func (h *Hub) SoftDelete(ctx context.Context, nodeID uuid.UUID) error {
	if h.store == nil {
		return ErrNotConfigured
	}
	return h.store.SoftDelete(ctx, nodeID)
}

// --- Consolidation (sleep sweep) --------------------------------------

// ConsolidationSummary aggregates one global sweep across every user.
type ConsolidationSummary struct {
	Users    int
	Decayed  int
	Linked   int
	Suppress int
	Archive  int
	Extinct  int
	Errors   int
}

// RunConsolidationAll runs the Consolidation sweep for every user that owns
// memory nodes. This is the entry point the background scheduler calls. Each
// user sweep is independent — one user's error doesn't abort the rest.
func (h *Hub) RunConsolidationAll(ctx context.Context) (ConsolidationSummary, error) {
	summary := ConsolidationSummary{}
	if h.store == nil || h.consolidation == nil {
		return summary, ErrNotConfigured
	}

	userIDs, err := h.store.ListMemoryUserIDs(ctx)
	if err != nil {
		return summary, err
	}

	now := h.now()
	for _, uid := range userIDs {
		select {
		case <-ctx.Done():
			return summary, ctx.Err()
		default:
		}
		report := h.consolidation.RunOnce(ctx, h.store, h.bus, uid, now)
		summary.Users++
		summary.Decayed += report.Decayed
		summary.Linked += report.Linked
		summary.Suppress += report.Transitions.Suppress
		summary.Archive += report.Transitions.Archive
		summary.Extinct += report.Transitions.Extinct
		summary.Errors += len(report.Errors)
		if len(report.Errors) > 0 {
			h.logger.Warn("v2 consolidation: stage errors",
				"user_id", uid, "errors", report.Errors)
		}
	}
	h.logger.Info("v2 consolidation sweep done",
		"users", summary.Users, "decayed", summary.Decayed,
		"linked", summary.Linked, "errors", summary.Errors)
	return summary, nil
}

// --- Errors ------------------------------------------------------------

// Topic constants mirror eventbus conventions; defined locally so callers
// don't need to import eventbus just to subscribe.
const (
	TopicMemoryIngested = "memory.ingested"
)

// ErrNotConfigured means the hub was built without a store or embedder.
var ErrNotConfigured = hubErr("memory hub: not configured (store or embedder missing)")

type hubErr string

func (e hubErr) Error() string { return string(e) }
