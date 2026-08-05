// Package weighter implements the Weighter plugin — Ebbinghaus-style exponential
// decay applied to memory node weights. The engine calls Apply at consolidation
// time, or callers invoke per-node for on-demand reweighting.
package weighter

import (
	"math"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/types"
)

// Plugin is the Weighter. Configurable via Init.
type Plugin struct {
	mu        sync.RWMutex
	tauDays   float64 // decay time constant
	minWeight float64 // floor; below this → eligible for forgetting
	maxBoost  float64 // reinforcement boost cap per access
}

// Defaults
var Defaults = struct {
	TauDays   float64
	MinWeight float64
	MaxBoost  float64
}{
	TauDays:   7.0,
	MinWeight: 0.05,
	MaxBoost:  0.1,
}

func New() *Plugin {
	p := &Plugin{
		tauDays:   Defaults.TauDays,
		minWeight: Defaults.MinWeight,
		maxBoost:  Defaults.MaxBoost,
	}
	return p
}

func (p *Plugin) Name() string                     { return "Weighter" }
func (p *Plugin) Category() types.PluginCategory   { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["tauDays"]; ok {
		if f, ok := toFloat(v); ok && f > 0 {
			p.tauDays = f
		}
	}
	if v, ok := cfg["tau"]; ok {
		if f, ok := toFloat(v); ok && f > 0 {
			p.tauDays = f / 86400 // seconds → days
		}
	}
	if v, ok := cfg["minWeight"]; ok {
		if f, ok := toFloat(v); ok {
			p.minWeight = f
		}
	}
	if v, ok := cfg["maxBoost"]; ok {
		if f, ok := toFloat(v); ok {
			p.maxBoost = f
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// Decay applies the Ebbinghaus forgetting curve to node.Weight given the time
// elapsed since lastTouch. Formula: w * exp(-elapsed / tau).
// Returns the new weight (clamped to [minWeight, 1.0]).
func (p *Plugin) Decay(node types.MemoryNode, lastTouch time.Time) float64 {
	p.mu.RLock()
	tau := p.tauDays
	min := p.minWeight
	p.mu.RUnlock()

	if node.Weight <= 0 {
		return min
	}
	// Defensive: a zero/NULL lastTouch (e.g. migrated node before the
	// last_access_at fix) would make time.Since ≈ 2000 years and nuke the
	// weight to the floor in one sweep. Fall back to UpdatedAt, then CreatedAt,
	// then "now" (no decay) so decay never sees an ancient epoch.
	ref := lastTouch
	if ref.IsZero() {
		if !node.UpdatedAt.IsZero() {
			ref = node.UpdatedAt
		} else if !node.CreatedAt.IsZero() {
			ref = node.CreatedAt
		} else {
			return clamp(node.Weight, min, 1.0) // nothing to go on → don't decay
		}
	}
	elapsed := time.Since(ref)
	if elapsed <= 0 {
		return clamp(node.Weight, min, 1.0)
	}
	days := elapsed.Hours() / 24.0
	factor := math.Exp(-days / tau)
	newW := node.Weight * factor
	return clamp(newW, min, 1.0)
}

// Reinforce bumps the weight on access (boost capped by maxBoost).
// Returns the new weight in [minWeight, 1.0].
func (p *Plugin) Reinforce(node types.MemoryNode, intensity float64) float64 {
	p.mu.RLock()
	max := p.maxBoost
	min := p.minWeight
	p.mu.RUnlock()

	if intensity <= 0 {
		intensity = 1.0
	}
	boost := max * clamp(intensity, 0, 2.0)
	newW := node.Weight + boost
	return clamp(newW, min, 1.0)
}

// BelowForgetThreshold returns true if weight has decayed below the floor.
func (p *Plugin) BelowForgetThreshold(node types.MemoryNode) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return node.Weight < p.minWeight
}

// ForgetEligible filters a node list to those below the floor.
func (p *Plugin) ForgetEligible(nodes []types.MemoryNode) []types.MemoryNode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := []types.MemoryNode{}
	for _, n := range nodes {
		if n.Weight < p.minWeight {
			out = append(out, n)
		}
	}
	return out
}

func clamp(f, lo, hi float64) float64 {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
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
