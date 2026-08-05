// Package engine is the V2 memory hub orchestrator. It owns the plugin
// registry, wires per-category plugin lists, and invokes them in a
// deterministic order per turn.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/damonlinyz/brain/memory/eventbus"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Registry holds all plugins keyed by name, plus category-sorted lists
// for deterministic dispatch.
type Registry struct {
	mu        sync.RWMutex
	plugins   map[string]Entry
	byName    []string
	byCat     map[types.PluginCategory][]Entry
	logger    *slog.Logger
}

// Entry wraps a registered Plugin with its DB-backed weight (engine_plugins.weight).
type Entry struct {
	Plugin types.Plugin
	Weight float64
}

// NewRegistry constructs an empty registry. nil logger falls back to slog.Default().
func NewRegistry(log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{
		plugins: make(map[string]Entry),
		byCat:   make(map[types.PluginCategory][]Entry),
		logger:  log,
	}
}

// Register adds a plugin. Multiple plugins of the same Name replace earlier
// registrations (so test setup can override).
func (r *Registry) Register(p types.Plugin, weight float64) error {
	if p == nil {
		return errors.New("engine.Register: nil plugin")
	}
	if p.Name() == "" {
		return fmt.Errorf("engine.Register: plugin missing Name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[p.Name()]; !exists {
		r.byName = append(r.byName, p.Name())
	}
	r.plugins[p.Name()] = Entry{Plugin: p, Weight: weight}
	r.reindexLocked()
	return nil
}

// Get returns the entry registered under name, or ErrUnknownPlugin.
func (r *Registry) Get(name string) (Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.plugins[name]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, name)
	}
	return e, nil
}

// ListByCategory returns entries in the given category, sorted by Weight desc
// (stable on ties) — the order Engine dispatches them in.
func (r *Registry) ListByCategory(cat types.PluginCategory) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, len(r.byCat[cat]))
	copy(out, r.byCat[cat])
	return out
}

func (r *Registry) reindexLocked() {
	for cat := range r.byCat {
		r.byCat[cat] = nil
	}
	for _, name := range r.byName {
		e := r.plugins[name]
		r.byCat[e.Plugin.Category()] = append(r.byCat[e.Plugin.Category()], e)
	}
	for cat := range r.byCat {
		sort.SliceStable(r.byCat[cat], func(i, j int) bool {
			if r.byCat[cat][i].Weight != r.byCat[cat][j].Weight {
				return r.byCat[cat][i].Weight > r.byCat[cat][j].Weight
			}
			return r.byCat[cat][i].Plugin.Name() < r.byCat[cat][j].Plugin.Name()
		})
	}
}

// Engine wires Registry + Bus + DB together. Plugins talk to the Engine via
// EngineContext passed to Process hooks (Phase 2+).
type Engine struct {
	Registry *Registry
	Bus      *eventbus.Bus
	DB       *pgxpool.Pool
	logger   *slog.Logger
}

// New constructs an Engine. db may be nil for tests that only exercise plugin
// logic without persistence.
func New(reg *Registry, bus *eventbus.Bus, db *pgxpool.Pool, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{Registry: reg, Bus: bus, DB: db, logger: log}
}

// Close calls Close() on every registered plugin. Errors are collected; the
// first non-nil error is returned after attempting all plugins.
func (e *Engine) Close(ctx context.Context) error {
	e.Registry.mu.RLock()
	names := make([]string, len(e.Registry.byName))
	copy(names, e.Registry.byName)
	e.Registry.mu.RUnlock()

	var firstErr error
	for _, name := range names {
		entry, err := e.Registry.Get(name)
		if err != nil {
			continue
		}
		if err := entry.Plugin.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("plugin %s close: %w", name, err)
		}
	}
	return firstErr
}

// InitPlugins walks every registered plugin and calls Init with its DB-backed
// config. Errors short-circuit (caller decides whether to disable plugin or abort).
func (e *Engine) InitPlugins(ctx context.Context, configs map[string]map[string]any) error {
	e.Registry.mu.RLock()
	names := make([]string, len(e.Registry.byName))
	copy(names, e.Registry.byName)
	e.Registry.mu.RUnlock()

	for _, name := range names {
		entry, err := e.Registry.Get(name)
		if err != nil {
			continue
		}
		cfg := configs[name]
		if cfg == nil {
			cfg = map[string]any{}
		}
		if err := entry.Plugin.Init(cfg); err != nil {
			return fmt.Errorf("init plugin %s: %w", name, err)
		}
	}
	return nil
}

// Errors
var (
	ErrUnknownPlugin = errors.New("engine: unknown plugin")
)
