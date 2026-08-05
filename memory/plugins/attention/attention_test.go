package attention

import (
	"context"
	"testing"

	"github.com/damonlinyz/brain/memory/types"
)

func TestAssess_ExplicitCueTriggersRemember(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	sig := p.Assess(context.Background(), ScoreInput{
		Content:     "Please remember my flight is at 8am",
		Salience:    types.SalienceNormal,
		SourceTrust: 0.5,
	})
	if sig.Decision != types.DecisionRemember {
		t.Fatalf("expected remember, got %s (score=%f)", sig.Decision, sig.Score)
	}
	if !contains(sig.Reasons, "explicit_cue") {
		t.Fatalf("expected explicit_cue in reasons: %v", sig.Reasons)
	}
}

func TestAssess_HighSaliencePushesUp(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	lowSig := p.Assess(context.Background(), ScoreInput{
		Content: "the meeting is at noon",
		Salience: types.SalienceLow,
		SourceTrust: 0.5,
	})
	highSig := p.Assess(context.Background(), ScoreInput{
		Content: "the meeting is at noon",
		Salience: types.SalienceHigh,
		SourceTrust: 0.5,
	})
	if highSig.Score <= lowSig.Score {
		t.Fatalf("expected high salience > low salience: %f vs %f", highSig.Score, lowSig.Score)
	}
}

func TestAssess_EmotionalMarkersAdd(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	neutral := p.Assess(context.Background(), ScoreInput{
		Content: "the report is ready",
		Salience: types.SalienceNormal,
	})
	emotional := p.Assess(context.Background(), ScoreInput{
		Content: "I love this amazing result!",
		Salience: types.SalienceNormal,
	})
	if emotional.EmotionalValence <= 0 {
		t.Fatalf("expected positive valence, got %f", emotional.EmotionalValence)
	}
	if emotional.Score <= neutral.Score {
		t.Fatalf("expected emotional > neutral: %f vs %f", emotional.Score, neutral.Score)
	}
}

func TestAssess_ExplicitBeatsLowTrust(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	sig := p.Assess(context.Background(), ScoreInput{
		Content:    "note that my name is Damon",
		Salience:   types.SalienceNormal,
		SourceTrust: 0.0,
	})
	if sig.Decision != types.DecisionRemember {
		t.Fatalf("explicit cue should override low trust: %s", sig.Decision)
	}
}

func TestAssess_BoringTextDrops(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	sig := p.Assess(context.Background(), ScoreInput{
		Content: "ok",
		Salience: types.SalienceLow,
		SourceTrust: 0.0,
	})
	if sig.Decision != types.DecisionDrop {
		t.Fatalf("expected drop, got %s (score=%f)", sig.Decision, sig.Score)
	}
}

func TestInit_OverrideWeights(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{
		"weights": map[string]any{"explicit": 0.5},
		"thresholds": map[string]any{"remember": 0.9, "defer": 0.5},
	})
	// explicit cue now contributes 0.5 (not 0.95), so plain "remember X" no longer hits 0.9
	sig := p.Assess(context.Background(), ScoreInput{
		Content: "remember this",
		Salience: types.SalienceNormal,
		SourceTrust: 0.5,
	})
	if sig.Decision == types.DecisionRemember {
		t.Fatalf("expected lower weight to fall below remember threshold, got score=%f", sig.Score)
	}
}

func contains(s []string, want string) bool {
	for _, x := range s {
		if x == want {
			return true
		}
	}
	return false
}
