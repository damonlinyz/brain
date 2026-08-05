package metacognition

import (
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

func node(confidence, consistency, sourceTrust float64, lastAccess time.Time, summary string) types.MemoryNode {
	return types.MemoryNode{
		ID:               uuid.New(),
		Summary:          summary,
		Confidence:       confidence,
		ConsistencyScore: consistency,
		SourceTrust:      sourceTrust,
		LastAccessAt:     lastAccess,
	}
}

func TestAssess_AllConfident(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"confidentThreshold": 0.7, "hedgeThreshold": 0.4})

	now := time.Now()
	rep := p.Assess(AssessInput{
		Nodes: []types.MemoryNode{
			node(0.85, 0.8, 0.9, now, "User likes Go"),
			node(0.9, 0.85, 0.95, now, "User is a backend dev"),
		},
		Now: now,
	})
	if rep.OverallLevel != Confident {
		t.Fatalf("expected Confident overall, got %s", rep.OverallLevel)
	}
	if !rep.LiveInjectOK {
		t.Fatal("expected live inject OK when all confident")
	}
	if len(rep.Assessments) != 2 {
		t.Fatalf("expected 2 assessments, got %d", len(rep.Assessments))
	}
}

func TestAssess_HedgeWhenLowConsistency(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"confidentThreshold": 0.7})

	now := time.Now()
	rep := p.Assess(AssessInput{
		Nodes: []types.MemoryNode{node(0.8, 0.3, 0.9, now, "single-source fact")},
		Now: now,
	})
	if rep.Assessments[0].Level != Hedge {
		t.Fatalf("high conf but low consistency → Hedge, got %s", rep.Assessments[0].Level)
	}
}

func TestAssess_BlurryWhenLowConfidence(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"hedgeThreshold": 0.5})

	now := time.Now()
	rep := p.Assess(AssessInput{
		Nodes: []types.MemoryNode{node(0.3, 0.3, 0.4, now, "weak memory")},
		Now: now,
	})
	if rep.Assessments[0].Level != Blurry {
		t.Fatalf("low conf + recent → Blurry, got %s", rep.Assessments[0].Level)
	}
	if rep.LiveInjectOK {
		t.Fatal("blurry should disable live inject")
	}
}

func TestAssess_AskWhenOldAndLowConfidence(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"hedgeThreshold": 0.5, "blurryMaxAgeDays": 7})

	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour)
	rep := p.Assess(AssessInput{
		Nodes: []types.MemoryNode{node(0.2, 0.2, 0.3, old, "old weak memory")},
		Now: now,
	})
	if rep.Assessments[0].Level != Ask {
		t.Fatalf("low conf + old → Ask, got %s", rep.Assessments[0].Level)
	}
}

func TestAssess_OverallLevelIsMin(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"confidentThreshold": 0.7, "hedgeThreshold": 0.4})

	now := time.Now()
	rep := p.Assess(AssessInput{
		Nodes: []types.MemoryNode{
			node(0.9, 0.9, 0.9, now, "confident"),
			node(0.3, 0.3, 0.3, now, "blurry"),
			node(0.6, 0.5, 0.6, now, "hedge"),
		},
		Now: now,
	})
	if rep.OverallLevel != Blurry {
		t.Fatalf("overall should be min(=Blurry), got %s", rep.OverallLevel)
	}
}

func TestAssess_EmptyInput(t *testing.T) {
	p := New()
	rep := p.Assess(AssessInput{})
	if rep.OverallLevel != Confident {
		t.Fatalf("empty → confident, got %s", rep.OverallLevel)
	}
	if !rep.LiveInjectOK {
		t.Fatal("empty should be live-inject OK")
	}
}

func TestAssess_FallsBackToAvgWhenConfidenceZero(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	now := time.Now()
	// Confidence=0, consistency=0.8, source_trust=0.6 → fallback avg=0.7 → Hedge
	rep := p.Assess(AssessInput{
		Nodes: []types.MemoryNode{node(0, 0.8, 0.6, now, "fallback")},
		Now: now,
	})
	a := rep.Assessments[0]
	if a.Level != Confident {
		t.Fatalf("fallback avg=0.7, consistency=0.8 ≥ 0.7 → Confident, got %s", a.Level)
	}
	if a.Confidence != 0.7 {
		t.Fatalf("expected fallback avg=0.7, got %f", a.Confidence)
	}
}
