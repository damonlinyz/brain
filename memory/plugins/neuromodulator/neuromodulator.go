// Package neuromodulator implements the G7 Neuromodulator plugin.
//
// A thin event-source: when the user expresses positive/negative feedback
// (dopamine boost) or focuses on a specific topic, this plugin publishes
// structured events on the bus and persists a rolling dopamine+serotonin
// state in user_neuro_state.
//
// Subscribers (Weighter, AttentionFilter) read the neuro-state to modulate
// weight adjustments and attention scores. The actual neuro-regulation logic
// is in those plugins; this is just the event source + state keeper.
package neuromodulator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/eventbus"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Plugin is the G7 Neuromodulator.
type Plugin struct {
	mu sync.RWMutex
	pool *pgxpool.Pool
	dopamineDecayPerHour float64
}

const (
	DopamineMax  = 1.0
	SerotoninMax = 1.0
)

var Defaults = struct{ DopamineDecayPerHour float64 }{DopamineDecayPerHour: 0.05}

func New(pool *pgxpool.Pool) *Plugin {
	return &Plugin{
		pool:                 pool,
		dopamineDecayPerHour: Defaults.DopamineDecayPerHour,
	}
}

func (p *Plugin) Name() string                   { return "Neuromodulator" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["dopamineDecayPerHour"]; ok {
		if f, ok := toFloat(v); ok && f >= 0 {
			p.dopamineDecayPerHour = f
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// Reward records a positive or negative reward signal. Amount is the dopamine
// delta (-1 to +1). Publishes a memory.reward event for subscribers.
func (p *Plugin) Reward(ctx context.Context, bus *eventbus.Bus, userID uuid.UUID, amount float64, reason string) error {
	if bus == nil || p.pool == nil {
		return nil
	}

	// Read current state from DB, apply delta, persist.
	state, err := p.getState(ctx, userID)
	if err != nil {
		return err
	}
	state.Dopamine = clamp(state.Dopamine+amount, 0, DopamineMax)
	state.Serotonin = clamp(state.Serotonin+amount*0.3, 0, SerotoninMax) // slower serotonin
	state.UpdatedAt = time.Now().UTC()

	if err := p.saveState(ctx, userID, state); err != nil {
		return err
	}

	bus.Publish(ctx, eventbus.Event{
		Topic: "memory.reward",
		Payload: map[string]any{
			"user_id":    userID,
			"amount":     amount,
			"dopamine":   state.Dopamine,
			"serotonin":   state.Serotonin,
			"reason":     reason,
			"updated_at": state.UpdatedAt,
		},
	})
	return nil
}

// GetState returns the current neuro-state for a user.
func (p *Plugin) GetState(ctx context.Context, userID uuid.UUID) (types.NeuroSnapshot, error) {
	if p.pool == nil {
		return types.NeuroSnapshot{}, nil
	}
	return p.getState(ctx, userID)
}

func (p *Plugin) getState(ctx context.Context, userID uuid.UUID) (types.NeuroSnapshot, error) {
	var s types.NeuroSnapshot
	var dopamine, serotonin, ach float64
	var updatedAt time.Time
	err := p.pool.QueryRow(ctx, `
        SELECT dopamine, serotonin, ach, updated_at
          FROM user_neuro_state WHERE user_id = $1`, userID,
	).Scan(&dopamine, &serotonin, &ach, &updatedAt)
	if err != nil {
		// Not found → return default.
		return types.NeuroSnapshot{}, nil
	}
	s.Dopamine = dopamine
	s.Serotonin = serotonin
	s.ACH = ach
	s.UpdatedAt = updatedAt
	return s, nil
}

func (p *Plugin) saveState(ctx context.Context, userID uuid.UUID, s types.NeuroSnapshot) error {
	b, _ := json.Marshal(map[string]any{
		"dopamine": s.Dopamine, "serotonin": s.Serotonin, "ach": s.ACH,
	})
	_, err := p.pool.Exec(ctx, `
        INSERT INTO user_neuro_state (user_id, dopamine, serotonin, ach, snapshot_json, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (user_id) DO UPDATE SET
            dopamine = EXCLUDED.dopamine, serotonin = EXCLUDED.serotonin, ach = EXCLUDED.ach,
            snapshot_json = EXCLUDED.snapshot_json, updated_at = EXCLUDED.updated_at`,
		userID, s.Dopamine, s.Serotonin, s.ACH, b, s.UpdatedAt)
	return err
}

func clamp(v, lo, hi float64) float64 {
	if v < lo { return lo }
	if v > hi { return hi }
	return v
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64: return x, true
	case float32: return float64(x), true
	case int: return float64(x), true
	case int64: return float64(x), true
	}
	return 0, false
}

// Ensure unused imports compile.
var _ = fmt.Sprintf
