// Package reconsolidation implements the G1 Reconsolidation plugin.
//
// In cognitive terms, recalling a memory makes it briefly labile — it can be
// updated before it re-consolidates. We model this with an "unstable window":
//
//  1. MarkUnstable opens the window (sets unstable_until = now + window) on a
//     node right after it's recalled. The Hub calls this for every memory it
//     surfaces.
//  2. ApplyCorrection, if invoked while the window is open, snapshots the old
//     content into memory_node_history, overwrites the node, and closes the
//     window. Outside the window a correction is rejected (ErrNotUnstable)
//     unless Force=true.
//
// Detection of *whether* a user message is a correction (BuilderContradiction,
// an LLM judge) is a separate concern; this plugin is just the mechanism.
package reconsolidation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Plugin is the Reconsolidation mechanism.
type Plugin struct {
	mu              sync.RWMutex
	unstableWindow  time.Duration // how long a recalled node stays labile
	forceWhenClosed bool          // apply corrections even outside the window
}

// Defaults — mirror seed 044_v2_memory_hub_seed.sql.
var Defaults = struct {
	UnstableWindowSec int
}{
	UnstableWindowSec: 86400, // 24h
}

func New() *Plugin {
	return &Plugin{
		unstableWindow: time.Duration(Defaults.UnstableWindowSec) * time.Second,
	}
}

func (p *Plugin) Name() string                   { return "Reconsolidation" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["unstableWindowSeconds"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.unstableWindow = time.Duration(n) * time.Second
		}
	}
	if v, ok := cfg["forceWhenClosed"]; ok {
		if b, ok := v.(bool); ok {
			p.forceWhenClosed = b
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// ErrNotUnstable means a correction was attempted outside the node's
// reconsolidation window and Force is not enabled.
var ErrNotUnstable = errors.New("reconsolidation: node is outside its unstable window")

// IsUnstable reports whether node is within its reconsolidation window at now.
func (p *Plugin) IsUnstable(node types.MemoryNode, now time.Time) bool {
	if node.UnstableUntil == nil {
		return false
	}
	return now.Before(*node.UnstableUntil)
}

// MarkUnstable opens the reconsolidation window on a recalled node. Best-effort
// on the recall hot path: a conflict or missing node is returned but callers
// may ignore it. Retries once on optimistic-lock conflict.
func (p *Plugin) MarkUnstable(ctx context.Context, s store.IMemoryStore, nodeID uuid.UUID, now time.Time) error {
	p.mu.RLock()
	window := p.unstableWindow
	p.mu.RUnlock()

	until := now.Add(window)
	for attempt := 0; attempt < 2; attempt++ {
		node, err := s.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}
		_, err = s.UpdateNode(ctx, nodeID, node.Version, func(n *types.MemoryNode) {
			n.UnstableUntil = &until
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, store.ErrOptimisticLockConflict) {
			return err
		}
	}
	return store.ErrConflictRetry
}

// CorrectionInput is what a caller hands to ApplyCorrection.
type CorrectionInput struct {
	NodeID        uuid.UUID
	NewContent    string
	NewSummary    string
	ChangeSummary string // human-readable reason for the history row
	Event         string // superseded_by_event: correction / consolidation / ...
}

// ApplyCorrection snapshots the current node into history, then overwrites its
// content/summary and closes the unstable window. Returns the updated node.
//
// If the node is outside its window and Force is disabled, returns
// ErrNotUnstable and leaves the node untouched. The history row's ValidFrom is
// the node's CreatedAt (its superseded version's birth) and ValidTo is now.
func (p *Plugin) ApplyCorrection(ctx context.Context, s store.IMemoryStore, in CorrectionInput, now time.Time) (types.MemoryNode, error) {
	p.mu.RLock()
	force := p.forceWhenClosed
	p.mu.RUnlock()
	return p.applyCorrection(ctx, s, in, now, force)
}

func (p *Plugin) applyCorrection(ctx context.Context, s store.IMemoryStore, in CorrectionInput, now time.Time, force bool) (types.MemoryNode, error) {
	if in.Event == "" {
		in.Event = "correction"
	}
	if in.NewContent == "" {
		return types.MemoryNode{}, store.ErrInvalidInput
	}

	node, err := s.GetNode(ctx, in.NodeID)
	if err != nil {
		return types.MemoryNode{}, err
	}

	if !p.IsUnstable(node, now) && !force {
		return types.MemoryNode{}, ErrNotUnstable
	}

	// 1) Snapshot the superseded version into history.
	validFrom := node.CreatedAt
	if validFrom.IsZero() {
		validFrom = now
	}
	historyRow := types.MemoryNodeHistory{
		CurrentNodeID:     node.ID,
		UserID:            node.UserID,
		Content:           node.Content,
		ContentType:       node.ContentType,
		Source:            node.Source,
		Weight:            node.Weight,
		ValidFrom:         validFrom,
		ValidTo:           now,
		SupersededByEvent: in.Event,
		ChangeSummary:     in.ChangeSummary,
	}
	if err := s.RecordHistory(ctx, historyRow); err != nil {
		return types.MemoryNode{}, err
	}

	// 2) Overwrite + close window, retrying once on optimistic-lock conflict.
	for attempt := 0; attempt < 2; attempt++ {
		cur, err := s.GetNode(ctx, in.NodeID)
		if err != nil {
			return types.MemoryNode{}, err
		}
		updated, err := s.UpdateNode(ctx, in.NodeID, cur.Version, func(n *types.MemoryNode) {
			n.Content = in.NewContent
			if in.NewSummary != "" {
				n.Summary = in.NewSummary
			}
			n.UnstableUntil = nil
			n.Source = types.SourceUserCorrection
			n.LastAccessAt = now
			n.ConsistencyScore = 1.0 // corrected = authoritative
		})
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, store.ErrOptimisticLockConflict) {
			return types.MemoryNode{}, err
		}
	}
	return types.MemoryNode{}, store.ErrConflictRetry
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

var errInvalid = errors.New("reconsolidation: invalid config value")
