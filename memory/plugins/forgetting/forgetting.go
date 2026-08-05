// Package forgetting implements the Forgetting plugin — three-stage state
// machine that retires memory nodes as their weight decays:
//
//	active → suppressed → archived → extinct
//
// Transitions are decided by weight thresholds plus a confirmation window so
// short-term dips don't lose memories. Consolidation drives bulk sweeps; the
// per-node Classify function is also useful inline (e.g. on retrieval).
package forgetting

import (
	"context"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Stage is the target state for a node, decided by Classify.
type Stage string

const (
	StageKeep       Stage = "keep"
	StageSuppress   Stage = "suppress"
	StageArchive    Stage = "archive"
	StageExtinct    Stage = "extinct"
)

// Plugin is the Forgetting.
type Plugin struct {
	mu                 sync.RWMutex
	suppressThreshold  float64
	archiveThreshold   float64
	confirmTTL         time.Duration
	// Hard floor — once weight is below this AND archived for confirmTTL, we extinct.
	extinctWeightFloor float64
}

// Defaults — mirror seed 044_v2_memory_hub_seed.sql.
var Defaults = struct {
	SuppressThreshold  float64
	ArchiveThreshold   float64
	ConfirmTTLSeconds  int
	ExtinctFloor       float64
}{
	SuppressThreshold:  0.10,
	ArchiveThreshold:   0.05,
	ConfirmTTLSeconds:  300,
	ExtinctFloor:       0.01,
}

func New() *Plugin {
	return &Plugin{
		suppressThreshold:  Defaults.SuppressThreshold,
		archiveThreshold:   Defaults.ArchiveThreshold,
		confirmTTL:         time.Duration(Defaults.ConfirmTTLSeconds) * time.Second,
		extinctWeightFloor: Defaults.ExtinctFloor,
	}
}

func (p *Plugin) Name() string                   { return "Forgetting" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["suppressThreshold"]; ok {
		if f, ok := toFloat(v); ok {
			p.suppressThreshold = f
		}
	}
	if v, ok := cfg["archiveThreshold"]; ok {
		if f, ok := toFloat(v); ok {
			p.archiveThreshold = f
		}
	}
	if v, ok := cfg["confirmTtlSeconds"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.confirmTTL = time.Duration(n) * time.Second
		}
	}
	if v, ok := cfg["confirmTTL"]; ok {
		if s, err := toString(v); err == nil {
			if d, err := time.ParseDuration(s); err == nil {
				p.confirmTTL = d
			}
		}
	}
	if v, ok := cfg["extinctFloor"]; ok {
		if f, ok := toFloat(v); ok {
			p.extinctWeightFloor = f
		}
	}
	// Sanity: thresholds should descend so we don't skip stages.
	if p.suppressThreshold < p.archiveThreshold {
		p.suppressThreshold = p.archiveThreshold
	}
	if p.archiveThreshold < p.extinctWeightFloor {
		p.archiveThreshold = p.extinctWeightFloor
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// Classify decides the target stage for a node based on its current state,
// weight, and how long it has been suppressed/archived.
//
// Rules:
//   - weight >= suppressThreshold → Keep
//   - weight < archiveThreshold   → eligible for Archive (after confirmTTL)
//   - weight < extinctFloor       → eligible for Extinct (after confirmTTL)
//   - otherwise                    → eligible for Suppress
//
// "Eligible" means the node has been at-or-below the trigger threshold for at
// least confirmTTL. lastTouch should be the node's last reinforcement time
// (typically LastAccessAt or UpdatedAt).
func (p *Plugin) Classify(node types.MemoryNode, now time.Time) Stage {
	p.mu.RLock()
	suppress, archive, extinct, ttl := p.suppressThreshold, p.archiveThreshold, p.extinctWeightFloor, p.confirmTTL
	p.mu.RUnlock()

	w := node.Weight
	elapsed := now.Sub(node.LastAccessAt)

	// Keep wins outright — even suppressed nodes regain active status when reinforced.
	if w >= suppress {
		return StageKeep
	}

	// Extinct: archived long enough at near-zero weight.
	if w < extinct && elapsed >= ttl {
		return StageExtinct
	}

	// Archive: below archive threshold for the confirmation window.
	if w < archive && elapsed >= ttl {
		return StageArchive
	}

	// Suppress: first sign of decay below the suppress floor.
	if w < suppress {
		return StageSuppress
	}

	return StageKeep
}

// ApplyTransition mutates the node in-place to match the target stage.
// Returns true if anything changed. Caller is responsible for persisting
// (UpdateNode / SoftDelete).
func (p *Plugin) ApplyTransition(node *types.MemoryNode, target Stage) bool {
	if node == nil {
		return false
	}
	before := *node
	switch target {
	case StageKeep:
		// Reinforcement path — clear any suppressed/archived state.
		if node.State != types.NodeStateActive {
			node.State = types.NodeStateActive
		}
	case StageSuppress:
		if node.State == types.NodeStateActive {
			node.State = types.NodeStateSuppressed
		}
	case StageArchive:
		if node.State != types.NodeStateArchived {
			node.State = types.NodeStateArchived
		}
	case StageExtinct:
		// SoftDelete will be called by the store executor.
		node.State = types.NodeStateExtinct
	}
	return before.State != node.State
}

// Process iterates all active+suppressed+archived nodes for a user and runs
// them through Classify → ApplyTransition. Returns counts per transition.
//
// Extinct transitions call store.SoftDelete; Suppress/Archive/Keep call
// store.UpdateNode with the new State.
func (p *Plugin) Process(ctx context.Context, s store.IMemoryStore, userID uuid.UUID, now time.Time) (TransitionCounts, error) {
	counts := TransitionCounts{}
	if s == nil {
		return counts, nil
	}

	limit := 200
	cursor := ""
	for {
		results, err := s.ListNodes(ctx, store.SearchFilter{
			UserID: userID,
			States: []types.NodeState{types.NodeStateActive, types.NodeStateSuppressed, types.NodeStateArchived},
			Limit:  limit,
			Cursor: cursor,
		})
		if err != nil {
			return counts, err
		}
		for _, node := range results.Items {
			target := p.Classify(node, now)
			switch target {
			case StageKeep:
				if node.State != types.NodeStateActive {
					if _, err := s.UpdateNode(ctx, node.ID, node.Version, setActive); err == nil {
						counts.Keep++
					}
				} else {
					counts.Keep++
				}
			case StageSuppress:
				if p.ApplyTransition(&node, target) {
					if _, err := s.UpdateNode(ctx, node.ID, node.Version, setState(types.NodeStateSuppressed)); err == nil {
						counts.Suppress++
					}
				}
			case StageArchive:
				if p.ApplyTransition(&node, target) {
					if _, err := s.UpdateNode(ctx, node.ID, node.Version, setState(types.NodeStateArchived)); err == nil {
						counts.Archive++
					}
				}
			case StageExtinct:
				if err := s.SoftDelete(ctx, node.ID); err == nil {
					counts.Extinct++
				}
			}
		}
		if results.NextCursor == "" || len(results.Items) == 0 {
			break
		}
		cursor = results.NextCursor
	}
	return counts, nil
}

// TransitionCounts summarises one Process pass.
type TransitionCounts struct {
	Keep     int
	Suppress int
	Archive  int
	Extinct  int
}

func (c TransitionCounts) Total() int {
	return c.Keep + c.Suppress + c.Archive + c.Extinct
}

func setActive(n *types.MemoryNode)       { n.State = types.NodeStateActive }
func setState(s types.NodeState) store.NodeMutator {
	return func(n *types.MemoryNode) { n.State = s }
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

func toString(v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	return "", errInvalid
}

var errInvalid = &cerr{"forgetting: invalid config value"}

type cerr struct{ msg string }

func (e *cerr) Error() string { return e.msg }
