// Package consolidation implements the Consolidation plugin — the "sleep"
// routine that fires after a user has been idle for a configurable window.
// One sweep does four things, in order:
//
//  1. Apply Ebbinghaus decay to every active node (via Weighter.DecayAll)
//  2. Run Forgetting state transitions (suppress/archive/extinct)
//  3. Discover unconnected similar pairs and link them
//  4. Publish a `memory.consolidated` event on the bus
//
// The scheduler in internal/jobs is the expected caller, but RunOnce is also
// safe to invoke from tests or an admin endpoint.
package consolidation

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/eventbus"
	"github.com/damonlinyz/brain/memory/plugins/extinction"
	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/damonlinyz/brain/memory/plugins/forgetting"
	"github.com/damonlinyz/brain/memory/plugins/weighter"
	"github.com/google/uuid"
)

// Plugin is the Consolidation.
type Plugin struct {
	mu                     sync.RWMutex
	inactivityDefault      time.Duration
	minSleep               time.Duration
	maxSleep               time.Duration
	shardPerMinute         int           // rate cap for FindUnconnectedSimilarPairs
	linkSimThreshold       float64       // minimum similarity for auto-linking
	maxLinksPerSweep       int           // cap to bound work
	logger                 *slog.Logger
	weighter               *weighter.Plugin
	forgetting             *forgetting.Plugin
	extinction             *extinction.Plugin
}

// Defaults — mirror seed 044_v2_memory_hub_seed.sql.
var Defaults = struct {
	InactivityDefaultSec int
	MinSeconds           int
	MaxSeconds           int
	ShardPerMinute       int
	LinkSimThreshold     float64
	MaxLinksPerSweep     int
}{
	InactivityDefaultSec: 14400, // 4h
	MinSeconds:           10800, // 3h
	MaxSeconds:           36000, // 10h
	ShardPerMinute:       60,
	LinkSimThreshold:     0.75,
	MaxLinksPerSweep:     50,
}

// Option wires optional dependencies.
type Option func(*Plugin)

func WithWeighter(w *weighter.Plugin) Option        { return func(p *Plugin) { p.weighter = w } }
func WithForgetting(f *forgetting.Plugin) Option    { return func(p *Plugin) { p.forgetting = f } }
func WithExtinction(e *extinction.Plugin) Option    { return func(p *Plugin) { p.extinction = e } }
func WithLogger(l *slog.Logger) Option {
	return func(p *Plugin) {
		if l != nil {
			p.logger = l
		}
	}
}

func New(opts ...Option) *Plugin {
	p := &Plugin{
		inactivityDefault: time.Duration(Defaults.InactivityDefaultSec) * time.Second,
		minSleep:          time.Duration(Defaults.MinSeconds) * time.Second,
		maxSleep:          time.Duration(Defaults.MaxSeconds) * time.Second,
		shardPerMinute:    Defaults.ShardPerMinute,
		linkSimThreshold:  Defaults.LinkSimThreshold,
		maxLinksPerSweep:  Defaults.MaxLinksPerSweep,
		logger:            slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Plugin) Name() string                   { return "Consolidation" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["inactivityDefaultSeconds"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.inactivityDefault = time.Duration(n) * time.Second
		}
	}
	if v, ok := cfg["minSeconds"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.minSleep = time.Duration(n) * time.Second
		}
	}
	if v, ok := cfg["maxSeconds"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.maxSleep = time.Duration(n) * time.Second
		}
	}
	if v, ok := cfg["shardPerMinute"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.shardPerMinute = n
		}
	}
	if v, ok := cfg["linkSimThreshold"]; ok {
		if f, ok := toFloat(v); ok {
			p.linkSimThreshold = f
		}
	}
	if v, ok := cfg["maxLinksPerSweep"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.maxLinksPerSweep = n
		}
	}
	if p.minSleep > p.maxSleep {
		p.minSleep = p.maxSleep
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// SleepWindow clamps a requested sleep duration between minSleep and maxSleep.
// If requested <= 0 we return inactivityDefault.
func (p *Plugin) SleepWindow(requested time.Duration) time.Duration {
	p.mu.RLock()
	def, mn, mx := p.inactivityDefault, p.minSleep, p.maxSleep
	p.mu.RUnlock()
	if requested <= 0 {
		return def
	}
	if requested < mn {
		return mn
	}
	if requested > mx {
		return mx
	}
	return requested
}

// ShouldRun returns true if (now - lastActivity) >= window.
func (p *Plugin) ShouldRun(lastActivity, now time.Time) bool {
	window := p.SleepWindow(0)
	return now.Sub(lastActivity) >= window
}

// RunOnce executes a full sweep for one user. All four stages run even if
// individual stages fail — we collect errors per stage and report them.
// Returns a Report describing what happened.
func (p *Plugin) RunOnce(ctx context.Context, s store.IMemoryStore, bus *eventbus.Bus, userID uuid.UUID, now time.Time) Report {
	p.mu.RLock()
	linkThr, maxLinks := p.linkSimThreshold, p.maxLinksPerSweep
	p.mu.RUnlock()

	report := Report{StartedAt: now}

	if s == nil {
		report.FinishedAt = time.Now()
		return report
	}

	// Stage 1: decay
	if p.weighter != nil {
		n, err := s.DecayAll(ctx, userID, now, func(n types.MemoryNode) float64 {
			return p.weighter.Decay(n, n.LastAccessAt)
		})
		if err != nil {
			report.Errors = append(report.Errors, StageError{Stage: "decay", Err: err})
		} else {
			report.Decayed = n
		}
	}

	// Stage 2: forgetting
	if p.forgetting != nil {
		counts, err := p.forgetting.Process(ctx, s, userID, now)
		if err != nil {
			report.Errors = append(report.Errors, StageError{Stage: "forgetting", Err: err})
		} else {
			report.Transitions = counts
		}
	}

	// Stage 2.5: extinction — mark very-low-weight nodes extinct.
	if p.extinction != nil {
		n, err := p.extinction.Process(ctx, s, userID)
		if err != nil {
			report.Errors = append(report.Errors, StageError{Stage: "extinction", Err: err})
		} else {
			report.Extinct = n
		}
	}

	// Stage 3: link discovery
	pairs, err := s.FindUnconnectedSimilarPairs(ctx, userID, linkThr, maxLinks)
	if err != nil {
		report.Errors = append(report.Errors, StageError{Stage: "link_find", Err: err})
	} else {
		linked := 0
		for _, pair := range pairs {
			_, eErr := s.CreateEdge(ctx, types.CreateEdgeInput{
				UserID:       userID,
				FromNodeID:   pair.A.ID,
				ToNodeID:     pair.B.ID,
				EdgeType:     types.EdgeKindDiscovered,
				Weight:       pair.Sim,
				DiscoveredBy: types.DiscovererConsolidation,
			})
			if eErr != nil {
				report.Errors = append(report.Errors, StageError{Stage: "link_create", Err: eErr})
				continue
			}
			linked++
		}
		report.Linked = linked
	}

	// Stage 4: notify
	if bus != nil {
		bus.Publish(ctx, eventbus.Event{
			Topic: eventbus.TopicConsolidationDone,
			Payload: map[string]any{
				"user_id":  userID,
				"at":       now,
				"decayed":  report.Decayed,
				"linked":   report.Linked,
				"suppress": report.Transitions.Suppress,
				"archive":  report.Transitions.Archive,
				"extinct":  report.Transitions.Extinct,
			},
		})
	}

	report.FinishedAt = time.Now()
	return report
}

// Report summarises one RunOnce invocation.
type Report struct {
	StartedAt   time.Time
	FinishedAt  time.Time
	Decayed     int
	Transitions forgetting.TransitionCounts
	Extinct     int
	Linked      int
	Errors      []StageError
}

// Duration is FinishedAt - StartedAt.
func (r Report) Duration() time.Duration {
	if r.FinishedAt.IsZero() {
		return 0
	}
	return r.FinishedAt.Sub(r.StartedAt)
}

// OK is true iff no stage errored.
func (r Report) OK() bool { return len(r.Errors) == 0 }

// StageError pairs a failing stage name with its error.
type StageError struct {
	Stage string
	Err   error
}

func (e StageError) Error() string {
	if e.Err == nil {
		return e.Stage
	}
	return e.Stage + ": " + e.Err.Error()
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

var errInvalid = &cerr{"consolidation: invalid config value"}

type cerr struct{ msg string }

func (e *cerr) Error() string { return e.msg }
