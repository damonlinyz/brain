package neuromodulator

import (
	"context"
	"os"
	"testing"

	"github.com/damonlinyz/brain/memory/eventbus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("MYBRAIN_TEST_DB")
	if dsn == "" { t.Skip("set MYBRAIN_TEST_DB") }
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil { t.Fatal(err) }
	t.Cleanup(p.Close)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS user_neuro_state (user_id UUID PRIMARY KEY, dopamine DOUBLE PRECISION NOT NULL DEFAULT 0, serotonin DOUBLE PRECISION NOT NULL DEFAULT 0, ach DOUBLE PRECISION NOT NULL DEFAULT 0, snapshot_json JSONB, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	} {
		if _, err := p.Exec(ctx, ddl); err != nil { t.Fatal(err) }
	}
	p.Exec(ctx, "TRUNCATE user_neuro_state")
	return p
}

func TestReward_IncrementsDopamine(t *testing.T) {
	p := New(pool(t))
	_ = p.Init(map[string]any{})
	bus := eventbus.New(nil)
	uid := uuid.New()

	err := p.Reward(context.Background(), bus, uid, 0.3, "user liked response")
	if err != nil { t.Fatal(err) }

	state, err := p.GetState(context.Background(), uid)
	if err != nil { t.Fatal(err) }
	if state.Dopamine < 0.29 || state.Dopamine > 0.31 {
		t.Fatalf("expected dopamine ~0.3, got %f", state.Dopamine)
	}
}

func TestReward_MultipleAccumulate(t *testing.T) {
	p := New(pool(t))
	_ = p.Init(map[string]any{})
	bus := eventbus.New(nil)
	uid := uuid.New()

	_ = p.Reward(context.Background(), bus, uid, 0.5, "")
	_ = p.Reward(context.Background(), bus, uid, 0.3, "")
	state, _ := p.GetState(context.Background(), uid)
	if state.Dopamine < 0.79 || state.Dopamine > 0.81 {
		t.Fatalf("expected dopamine ~0.8, got %f", state.Dopamine)
	}
}

func TestReward_Clamped(t *testing.T) {
	p := New(pool(t))
	_ = p.Init(map[string]any{})
	bus := eventbus.New(nil)
	uid := uuid.New()

	_ = p.Reward(context.Background(), bus, uid, 2.0, "") // should clamp at 1.0
	state, _ := p.GetState(context.Background(), uid)
	if state.Dopamine != 1.0 { t.Fatalf("expected clamped to 1.0, got %f", state.Dopamine) }
}
