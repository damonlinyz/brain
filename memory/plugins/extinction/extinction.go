// Package extinction implements the G6 Extinction plugin.
//
// When a memory node's weight falls below a threshold (due to Ebbinghaus decay
// without reinforcement), it transitions to the "extinct" state — retained in
// storage but excluded from recall. If a matching node is later ingested
// (PatternSeparation finds a similarity match against an extinct node), the
// node is revived back to "active" with a fresh weight.
//
// This is a thin plugin; the heavy lifting is in Weighter (decay) and
// PatternSeparation (match/resurrect). Extinction just flips the state.
package extinction

import (
	"context"
	"sync"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Plugin is the Extinction mechanism.
type Plugin struct {
	mu                 sync.RWMutex
	extinctThreshold   float64   // weight below this → extinct (default 0.02)
}

var Defaults = struct{ ExtinctThreshold float64 }{ExtinctThreshold: 0.02}

func New() *Plugin {
	return &Plugin{extinctThreshold: Defaults.ExtinctThreshold}
}

func (p *Plugin) Name() string                   { return "Extinction" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["extinctThreshold"]; ok {
		if f, ok := toFloat(v); ok && f >= 0 {
			p.extinctThreshold = f
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// Process marks nodes with weight below threshold as extinct. Returns the count
// of newly-extinct nodes. Idempotent — already-extinct nodes are skipped.
func (p *Plugin) Process(ctx context.Context, s store.IMemoryStore, userID uuid.UUID) (int, error) {
	p.mu.RLock()
	threshold := p.extinctThreshold
	p.mu.RUnlock()

	// List active nodes with weight below threshold.
	results, err := s.ListNodes(ctx, store.SearchFilter{
		UserID: userID,
		States: []types.NodeState{types.NodeStateActive},
		Limit:  500,
	})
	if err != nil {
		return 0, err
	}

	count := 0
	for _, n := range results.Items {
		if n.Weight >= threshold {
			continue
		}
		_, err := s.UpdateNode(ctx, n.ID, n.Version, func(nd *types.MemoryNode) {
			nd.State = types.NodeStateExtinct
		})
		if err != nil {
			continue // skip lock conflicts, try next sweep
		}
		count++
	}
	return count, nil
}

// Revive ressurrects an extinct node back to active, resetting its weight.
// Returns the revived node, or ErrNotFound. Caller should also reinforce
// via Weighter to set the proper weight.
func (p *Plugin) Revive(ctx context.Context, s store.IMemoryStore, nodeID uuid.UUID) (types.MemoryNode, error) {
	node, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return types.MemoryNode{}, err
	}
	if node.State != types.NodeStateExtinct {
		return node, nil // already active or suppressed; no-op
	}
	return s.UpdateNode(ctx, nodeID, node.Version, func(n *types.MemoryNode) {
		n.State = types.NodeStateActive
		n.Weight = 0.3 // fresh start
	})
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
