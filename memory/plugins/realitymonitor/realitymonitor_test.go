package realitymonitor

import (
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/types"
)

func src(source types.Source, trust float64) types.SourceEntry {
	return types.SourceEntry{Source: source, Trust: trust, AddedAt: time.Now()}
}

func TestAddSource_AppendsNewType(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"multiSourceBonus": 0.2})

	sources, consistency, conf := p.AddSource(
		[]types.SourceEntry{src(types.SourceHumanInput, 0.8)},
		src(types.SourceSearchResult, 0.6),
	)
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
	// Two distinct types → bonus should apply.
	if consistency < 0.7 || consistency > 1.0 {
		t.Fatalf("expected consistency with bonus, got %f", consistency)
	}
	if conf <= 0 {
		t.Fatalf("expected positive confidence, got %f", conf)
	}
}

func TestAddSource_DedupSameType(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	sources, _, _ := p.AddSource(
		[]types.SourceEntry{src(types.SourceHumanInput, 0.8)},
		src(types.SourceHumanInput, 0.3), // newer, lower trust
	)
	if len(sources) != 1 {
		t.Fatalf("expected dedup keeps 1 source, got %d", len(sources))
	}
	if sources[0].Trust != 0.3 {
		t.Fatalf("newer trust should replace: got %f", sources[0].Trust)
	}
}

func TestAddSource_DefaultsTrustAndTime(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	sources, _, _ := p.AddSource(nil, types.SourceEntry{Source: types.SourceInference})
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].Trust != 0.5 {
		t.Fatalf("expected default trust 0.5, got %f", sources[0].Trust)
	}
	if sources[0].AddedAt.IsZero() {
		t.Fatal("expected non-zero AddedAt")
	}
}

func TestAggregateTrust_SingleSourceNoBonus(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"multiSourceBonus": 0.3})

	c, _ := p.AggregateTrust([]types.SourceEntry{src(types.SourceHumanInput, 0.7)})
	// Single source, no bonus. consistency = 0.7.
	if c != 0.7 {
		t.Fatalf("single source no bonus: expected 0.7, got %f", c)
	}
}

func TestAggregateTrust_MultiSourceWithBonus(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"multiSourceBonus": 0.2})

	sources := []types.SourceEntry{
		src(types.SourceHumanInput, 0.8),
		src(types.SourceSearchResult, 0.6),
		src(types.SourceInference, 0.7),
	}
	c, conf := p.AggregateTrust(sources)
	avg := (0.8 + 0.6 + 0.7) / 3.0 // 0.7
	wantC := avg + 0.2              // 0.9
	if c < wantC-0.01 || c > wantC+0.01 {
		t.Fatalf("3 sources, 3 types → consistency ≈ %f, got %f", wantC, c)
	}
	if conf != c {
		t.Fatalf("confidence (%f) should equal consistency (%f)", conf, c)
	}
}

func TestAggregateTrust_EmptySources(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	c, conf := p.AggregateTrust(nil)
	if c != 0 || conf != 0 {
		t.Fatalf("empty: expected (0,0), got (%f,%f)", c, conf)
	}
}

func TestAggregateTrust_SameTypeMultipleNoBonus(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"multiSourceBonus": 0.5})

	// Two entries, both HumanInput → only 1 distinct type, no bonus.
	sources := []types.SourceEntry{src(types.SourceHumanInput, 0.8), src(types.SourceHumanInput, 0.6)}
	c, _ := p.AggregateTrust(sources)
	avg := (0.8 + 0.6) / 2 // 0.7
	if c != avg {
		t.Fatalf("same type dedup: expected no bonus (%f), got %f", avg, c)
	}
}

func TestAggregateTrust_ClampedAtOne(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"multiSourceBonus": 0.5})
	c, _ := p.AggregateTrust([]types.SourceEntry{
		src(types.SourceHumanInput, 1.0),
		src(types.SourceSearchResult, 1.0),
	})
	// avg=1.0 + bonus=0.5 → 1.5 → clamped to 1.0
	if c > 1.0 {
		t.Fatalf("expected clamped to 1.0, got %f", c)
	}
}
