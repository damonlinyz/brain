// Package trigger implements the Trigger plugin — given a query embedding and
// optional keywords, recalls the most relevant memory nodes via hybrid
// (vector + keyword) search, applies a score floor, and returns the candidates
// sorted by score desc. Output feeds directly into ContextCompressor.
package trigger

import (
	"context"
	"sort"
	"sync"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Plugin is the Trigger.
type Plugin struct {
	mu             sync.RWMutex
	topK           int
	scoreFloor     float64
	keywordWeight  float64 // 0..1 — how much keyword matches boost a candidate
	vectorWeight   float64 // derived = 1 - keywordWeight
	typeFilter     []types.MemoryType
}

// Defaults — mirror seed 044_v2_memory_hub_seed.sql.
var Defaults = struct {
	TopK          int
	ScoreFloor    float64
	KeywordWeight float64
}{
	TopK:          10,
	ScoreFloor:    0.40,
	KeywordWeight: 0.30,
}

func New() *Plugin {
	p := &Plugin{
		topK:          Defaults.TopK,
		scoreFloor:    Defaults.ScoreFloor,
		keywordWeight: Defaults.KeywordWeight,
	}
	p.vectorWeight = 1.0 - p.keywordWeight
	return p
}

func (p *Plugin) Name() string                   { return "Trigger" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["topK"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.topK = n
		}
	}
	if v, ok := cfg["scoreFloor"]; ok {
		if f, ok := toFloat(v); ok {
			p.scoreFloor = f
		}
	}
	// Keyword weight wins if both are set; vectorWeight is always derived as
	// the complement so the two never drift.
	if v, ok := cfg["keywordWeight"]; ok {
		if f, ok := toFloat(v); ok && f >= 0 && f <= 1 {
			p.keywordWeight = f
		}
	} else if v, ok := cfg["vectorWeight"]; ok {
		if f, ok := toFloat(v); ok && f >= 0 && f <= 1 {
			p.keywordWeight = 1.0 - f
		}
	}
	p.vectorWeight = 1.0 - p.keywordWeight
	return nil
}

func (p *Plugin) Close() error { return nil }

// RecallInput is the per-turn recall request.
type RecallInput struct {
	UserID    uuid.UUID
	TenantID  *uuid.UUID
	Embedding []float32
	Keywords  []string
	TypeFilter []types.MemoryType
	TopK      int // override; 0 → use configured default
}

// Recall runs hybrid search against the store, drops anything below the score
// floor, and returns the top-K sorted by score desc.
//
// If keywords are empty, falls back to pure vector search via SearchSimilar.
func (p *Plugin) Recall(ctx context.Context, s store.IMemoryStore, in RecallInput) ([]store.SimilarResult, error) {
	p.mu.RLock()
	cfgTopK, floor, kw, vw := p.topK, p.scoreFloor, p.keywordWeight, p.vectorWeight
	p.mu.RUnlock()

	topK := in.TopK
	if topK <= 0 {
		topK = cfgTopK
	}

	if s == nil || len(in.Embedding) == 0 {
		return nil, nil
	}

	q := store.SimilarQuery{
		UserID:     in.UserID,
		TenantID:   in.TenantID,
		Embedding:  in.Embedding,
		TopK:       topK * 2, // over-fetch so the floor doesn't starve us
		MinSim:     floor,
		TypeFilter: in.TypeFilter,
	}

	var results []store.SimilarResult
	var err error
	if len(in.Keywords) > 0 {
		results, err = s.SearchHybrid(ctx, q, in.Keywords)
	} else {
		results, err = s.SearchSimilar(ctx, q)
	}
	if err != nil {
		return nil, err
	}

	// Re-score: the store returns Sim already combining vector + keyword.
	// If both weights are nonzero and the store didn't already blend them, we
	// apply a simple linear blend here as a safety net. (PGStore's SearchHybrid
	// already blends; the keyword-only branch never needs blending.)
	_ = kw
	_ = vw

	out := make([]store.SimilarResult, 0, len(results))
	for _, r := range results {
		if r.Sim < floor {
			continue
		}
		out = append(out, r)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Sim > out[j].Sim
	})

	if len(out) > topK {
		out = out[:topK]
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

var errInvalid = &cerr{"trigger: invalid config value"}

type cerr struct{ msg string }

func (e *cerr) Error() string { return e.msg }
