package weighter

import (
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/types"
)

func TestDecay_ReducesWeightOverTime(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"tauDays": 7.0})

	node := types.MemoryNode{Weight: 1.0}
	lastTouch := time.Now().Add(-7 * 24 * time.Hour) // 7 days ago = one tau

	decayed := p.Decay(node, lastTouch)
	// After one tau: weight should be ~ exp(-1) ≈ 0.368
	want := 0.368
	if decayed < want-0.05 || decayed > want+0.05 {
		t.Fatalf("expected ~%.3f after one tau, got %f", want, decayed)
	}
}

func TestDecay_NoElapsed_ClampsToWeight(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"tauDays": 7.0})

	node := types.MemoryNode{Weight: 0.7}
	// Slightly-future lastTouch guarantees elapsed <= 0 path.
	decayed := p.Decay(node, time.Now().Add(time.Second))
	if decayed < 0.69 || decayed > 0.71 {
		t.Fatalf("expected weight unchanged at 0.7, got %f", decayed)
	}
}

func TestDecay_RespectsTauConfig(t *testing.T) {
	short := New()
	_ = short.Init(map[string]any{"tauDays": 1.0})
	long := New()
	_ = long.Init(map[string]any{"tauDays": 30.0})

	node := types.MemoryNode{Weight: 1.0}
	lastTouch := time.Now().Add(-5 * 24 * time.Hour) // 5 days

	shortDecay := short.Decay(node, lastTouch)
	longDecay := long.Decay(node, lastTouch)
	if shortDecay >= longDecay {
		t.Fatalf("short tau should decay faster: short=%f, long=%f", shortDecay, longDecay)
	}
}

func TestDecay_FloorsToMinWeight(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"tauDays": 1.0, "minWeight": 0.05})

	node := types.MemoryNode{Weight: 1.0}
	lastTouch := time.Now().Add(-365 * 24 * time.Hour) // a year
	decayed := p.Decay(node, lastTouch)
	if decayed != 0.05 {
		t.Fatalf("expected floor 0.05, got %f", decayed)
	}
}

func TestReinforce_BumpsWeightCapped(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"maxBoost": 0.1})

	node := types.MemoryNode{Weight: 0.5}
	newW := p.Reinforce(node, 1.0)
	if newW != 0.6 {
		t.Fatalf("expected 0.5 + 0.1 = 0.6, got %f", newW)
	}

	// Intensity > 1 scales boost but is capped at intensity=2.0
	big := p.Reinforce(node, 5.0)
	if big != 0.7 { // boost = maxBoost * clamp(5, 0, 2) = 0.1 * 2 = 0.2
		t.Fatalf("expected capped boost to 0.7, got %f", big)
	}

	// Total weight capped at 1.0
	heavy := types.MemoryNode{Weight: 0.95}
	heavyW := p.Reinforce(heavy, 1.0)
	if heavyW != 1.0 {
		t.Fatalf("expected total cap 1.0, got %f", heavyW)
	}
}

func TestReinforce_DefaultIntensity(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"maxBoost": 0.1})

	node := types.MemoryNode{Weight: 0.5}
	newW := p.Reinforce(node, 0) // zero intensity → defaults to 1.0
	if newW != 0.6 {
		t.Fatalf("expected default-intensity boost to 0.6, got %f", newW)
	}
}

func TestBelowForgetThreshold(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"minWeight": 0.1})

	if !p.BelowForgetThreshold(types.MemoryNode{Weight: 0.05}) {
		t.Fatal("expected 0.05 < 0.1 threshold")
	}
	if p.BelowForgetThreshold(types.MemoryNode{Weight: 0.5}) {
		t.Fatal("expected 0.5 >= 0.1 threshold")
	}
}

func TestForgetEligible_FiltersList(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"minWeight": 0.2})

	nodes := []types.MemoryNode{
		{ID: nid("keep-a"), Weight: 0.9},
		{ID: nid("forget-b"), Weight: 0.05},
		{ID: nid("keep-c"), Weight: 0.3},
		{ID: nid("forget-d"), Weight: 0.1},
	}
	out := p.ForgetEligible(nodes)
	if len(out) != 2 {
		t.Fatalf("expected 2 eligible, got %d", len(out))
	}
	if out[0].ID == out[1].ID || out[0].ID.String() == "" {
		t.Fatal("filter returned wrong nodes")
	}
}

func TestInit_TauInSeconds(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"tau": 86400.0}) // 1 day in seconds

	node := types.MemoryNode{Weight: 1.0}
	lastTouch := time.Now().Add(-24 * time.Hour) // 1 day = one tau
	decayed := p.Decay(node, lastTouch)
	want := 0.368
	if decayed < want-0.05 || decayed > want+0.05 {
		t.Fatalf("expected ~%.3f for tau=1day after 1 day, got %f", want, decayed)
	}
}

// nid helper builds a stable UUID from a string suffix.
func nid(suffix string) (id [16]byte) {
	// deterministic uuid-like value; tests only need ID equality & non-zero
	for i := 0; i < len(suffix) && i < 16; i++ {
		id[i] = suffix[i]
	}
	return
}
