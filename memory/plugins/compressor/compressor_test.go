package compressor

import (
	"context"
	"testing"

	"github.com/damonlinyz/brain/memory/types"
)

func cand(id string, score float64, summary string) types.TriggerCandidate {
	return types.TriggerCandidate{NodeID: id, Score: score, Summary: summary, Source: "vector"}
}

func TestCompress_HighScoreFirst(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"maxBudget": 1000})
	out := p.Compress(context.Background(), CompressInput{
		Candidates: []types.TriggerCandidate{
			cand("low", 0.1, "low-relevance memory"),
			cand("high", 0.9, "high-relevance memory"),
			cand("mid", 0.5, "mid-relevance memory"),
		},
	})
	if len(out.Memories) != 3 {
		t.Fatalf("expected 3 picked, got %d", len(out.Memories))
	}
	if out.Memories[0].Relevance != 0.9 {
		t.Fatalf("expected first to be 0.9, got %f", out.Memories[0].Relevance)
	}
}

func TestCompress_DropsWhenBudgetExhausted(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"maxBudget": 50, "minBudget": 10})
	out := p.Compress(context.Background(), CompressInput{
		Candidates: []types.TriggerCandidate{
			cand("a", 0.9, "first memory with fairly long summary"),
			cand("b", 0.5, "second memory with another long summary that should not fit"),
			cand("c", 0.3, "third"),
		},
	})
	if len(out.Memories) < 1 || len(out.Memories) == 3 {
		t.Fatalf("expected 1-2 memories picked (budget hit), got %d", len(out.Memories))
	}
	if out.TokenUsed > out.TokenBudget {
		t.Fatalf("used %d > budget %d", out.TokenUsed, out.TokenBudget)
	}
}

func TestCompress_EmptyInput(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})
	out := p.Compress(context.Background(), CompressInput{})
	if len(out.Memories) != 0 {
		t.Fatalf("expected 0 memories, got %d", len(out.Memories))
	}
	if out.SystemPrompt == "" {
		t.Fatal("expected non-empty system prompt (prefix)")
	}
}

func TestCompress_MinBudgetFloor(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"minBudget": 100, "maxBudget": 500})
	out := p.Compress(context.Background(), CompressInput{DesiredBudget: 10})
	if out.TokenBudget != 100 {
		t.Fatalf("expected budget floored to minBudget=100, got %d", out.TokenBudget)
	}
}
