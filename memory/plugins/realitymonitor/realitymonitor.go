// Package realitymonitor implements the G3 RealityMonitor plugin.
//
// Multi-source verification: when the same memory is confirmed by different
// source types (human_input, search_result, inference, user_correction), it
// gains trust. Each source contributes an individual trust score; aggregate
// consistency is the mean trust, with a bonus when ≥2 distinct source types
// agree. The more corroborating sources, the higher the confidence.
//
// The plugin is a pure computation layer. The Hub calls AddSource on merge
// and passes the mutated Sources + ConsistencyScore + Confidence through
// UpdateNode mutators. No store or Redis dependency — pragmatic reconciliation
// with the actual Plugin.Init(cfg map[string]any) contract (the design spec
// assumes Redis injection that the built contract doesn't provide; we store
// per-source trust in the Sources JSONB column instead).
package realitymonitor

import (
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/types"
)

// Plugin is the RealityMonitor (G3).
type Plugin struct {
	mu                sync.RWMutex
	multiSourceBonus  float64 // added to consistency when ≥2 distinct source types
}

// Defaults — mirror seed 044 (RealityMonitor: {"multiSourceBonus":0.2}).
var Defaults = struct {
	MultiSourceBonus float64
}{
	MultiSourceBonus: 0.2,
}

func New() *Plugin {
	return &Plugin{multiSourceBonus: Defaults.MultiSourceBonus}
}

func (p *Plugin) Name() string                   { return "RealityMonitor" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["multiSourceBonus"]; ok {
		if f, ok := toFloat(v); ok && f >= 0 {
			p.multiSourceBonus = f
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// AddSource deduplicates and appends a SourceEntry, then returns the mutated
// Sources slice plus the new aggregate ConsistencyScore and Confidence. Callers
// should persist the result through store.UpdateNode mutators.
//
// Dedup: two entries with the same Source type are collapsed — the newer trust
// replaces the older (more recent evidence wins). Different source types are
// additive (corroboration).
func (p *Plugin) AddSource(existing []types.SourceEntry, new types.SourceEntry) (sources []types.SourceEntry, consistency float64, confidence float64) {
	p.mu.RLock()
	bonus := p.multiSourceBonus
	p.mu.RUnlock()

	if new.AddedAt.IsZero() {
		new.AddedAt = time.Now().UTC()
	}
	if new.Trust <= 0 {
		new.Trust = 0.5
	}
	if new.Source == "" {
		new.Source = types.SourceInference
	}

	sources = mergeSources(existing, new)
	consistency, confidence = aggregateTrust(sources, bonus)
	return sources, consistency, confidence
}

// AggregateTrust computes the consistency score (mean trust across sources,
// with bonus if ≥2 distinct source types) and confidence (consistency
// weighted by source count, bounded [0,1]).
func (p *Plugin) AggregateTrust(sources []types.SourceEntry) (consistency float64, confidence float64) {
	p.mu.RLock()
	bonus := p.multiSourceBonus
	p.mu.RUnlock()
	return aggregateTrust(sources, bonus)
}

// mergeSources returns a copy of existing with new deduplicated in. Same Source
// type overwrites; different type appends. Preserves order.
func mergeSources(existing []types.SourceEntry, new types.SourceEntry) []types.SourceEntry {
	out := make([]types.SourceEntry, 0, len(existing)+1)
	found := false
	for _, s := range existing {
		if s.Source == new.Source {
			out = append(out, new) // replace with newer
			found = true
		} else {
			out = append(out, s)
		}
	}
	if !found {
		out = append(out, new)
	}
	return out
}

// aggregateTrust computes raw scores from a Sources slice.
func aggregateTrust(sources []types.SourceEntry, bonus float64) (consistency float64, confidence float64) {
	n := len(sources)
	if n == 0 {
		return 0, 0
	}

	// Mean trust over all source entries.
	var sum float64
	seen := map[types.Source]struct{}{}
	for _, s := range sources {
		sum += clampTrust(s.Trust)
		seen[s.Source] = struct{}{}
	}
	avg := sum / float64(n)

	// Bonus when ≥2 distinct source types corroborate.
	distinct := len(seen)
	if distinct >= 2 {
		consistency = avg + bonus
	} else {
		consistency = avg
	}
	if consistency > 1.0 {
		consistency = 1.0
	}

	// Confidence mirrors consistency — the multi-source bonus (applied when
	// ≥2 distinct types) is the confidence mechanism. Individual source count
	// only affects consistency through the mean-trust average.
	confidence = consistency
	return consistency, confidence
}

func clampTrust(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
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
