// Package patternsep implements the PatternSeparation plugin — decides whether
// an incoming memory should merge into an existing node, link to one, or be
// stored as new. The merge/link/new boundaries are configurable similarity
// thresholds; the actual search runs against IMemoryStore.SearchSimilar.
package patternsep

import (
	"context"
	"sync"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Decision labels what the plugin concluded for a candidate.
type Decision string

const (
	DecisionNew   Decision = "new"
	DecisionLink  Decision = "link"
	DecisionMerge Decision = "merge"
)

// Outcome is the result of Evaluate. Caller (Builder) is responsible for
// executing the chosen action against the store.
type Outcome struct {
	Decision     Decision
	ExistingNode *types.MemoryNode
	Similarity   float64
	// Candidates considered, sorted by similarity desc. Useful for diagnostics.
	OtherCandidates []store.SimilarResult
}

// Plugin is the PatternSeparation.
type Plugin struct {
	mu        sync.RWMutex
	mergeSim  float64
	linkSim   float64
	topK      int
	minSim    float64
}

// Defaults — mirror seed 044_v2_memory_hub_seed.sql.
var Defaults = struct {
	MergeSim float64
	LinkSim  float64
	TopK     int
	MinSim   float64
}{
	MergeSim: 0.90,
	LinkSim:  0.70,
	TopK:     10,
	MinSim:   0.50,
}

func New() *Plugin {
	return &Plugin{
		mergeSim: Defaults.MergeSim,
		linkSim:  Defaults.LinkSim,
		topK:     Defaults.TopK,
		minSim:   Defaults.MinSim,
	}
}

func (p *Plugin) Name() string                   { return "PatternSeparation" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["mergeSim"]; ok {
		if f, ok := toFloat(v); ok {
			p.mergeSim = f
		}
	}
	if v, ok := cfg["linkSim"]; ok {
		if f, ok := toFloat(v); ok {
			p.linkSim = f
		}
	}
	if v, ok := cfg["topK"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.topK = n
		}
	}
	if v, ok := cfg["minSim"]; ok {
		if f, ok := toFloat(v); ok {
			p.minSim = f
		}
	}
	// Keep thresholds sane.
	if p.mergeSim < p.linkSim {
		p.mergeSim = p.linkSim
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// EvaluateInput is what the caller (Builder) provides.
type EvaluateInput struct {
	UserID    uuid.UUID
	TenantID  *uuid.UUID
	Embedding []float32
	// TypeFilter restricts candidates to a memory type (e.g. profile vs episodic).
	TypeFilter []types.MemoryType
}

// Evaluate searches the store for the most similar existing node and classifies
// the decision. Returns Outcome with Decision=DecisionNew if nothing exceeds
// minSim or if the store returns no hits.
func (p *Plugin) Evaluate(ctx context.Context, s store.IMemoryStore, in EvaluateInput) (Outcome, error) {
	p.mu.RLock()
	mergeSim, linkSim, topK, minSim := p.mergeSim, p.linkSim, p.topK, p.minSim
	p.mu.RUnlock()

	out := Outcome{Decision: DecisionNew}

	if s == nil || len(in.Embedding) == 0 {
		return out, nil
	}

	results, err := s.SearchSimilar(ctx, store.SimilarQuery{
		UserID:     in.UserID,
		TenantID:   in.TenantID,
		Embedding:  in.Embedding,
		TopK:       topK,
		MinSim:     minSim,
		TypeFilter: in.TypeFilter,
	})
	if err != nil {
		return out, err
	}
	if len(results) == 0 {
		return out, nil
	}

	top := results[0]
	out.Similarity = top.Sim
	node := top.Node
	out.ExistingNode = &node
	if len(results) > 1 {
		out.OtherCandidates = results[1:]
	}

	switch {
	case top.Sim >= mergeSim:
		out.Decision = DecisionMerge
	case top.Sim >= linkSim:
		out.Decision = DecisionLink
	default:
		out.Decision = DecisionNew
		// Don't leak a "match" pointer if similarity is below link threshold —
		// caller should treat this as a brand-new memory.
		out.ExistingNode = nil
	}
	return out, nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func toInt(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	}
	return 0, errInvalid
}

var errInvalid = &cerr{"patternsep: invalid config value"}

type cerr struct{ msg string }

func (e *cerr) Error() string { return e.msg }
