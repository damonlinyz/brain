package interference

import (
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

func mkNode(content, summary string, confidence, weight float64, createdAt time.Time) types.MemoryNode {
	return types.MemoryNode{
		ID: uuid.New(), Content: content, Summary: summary,
		Confidence: confidence, Weight: weight, CreatedAt: createdAt,
	}
}

func TestDetectContradiction_Basic(t *testing.T) {
	a := "User prefers Go for backend development"
	b := "User does not prefer Go, actually prefers Python"
	c := detectContradiction(a, b)
	if c != ConflictContradict {
		t.Fatalf("expected Contradict, got %s", c)
	}
}

func TestDetectContradiction_NoConflict(t *testing.T) {
	a := "User likes hiking"
	b := "User enjoys cooking"
	c := detectContradiction(a, b)
	if c != "" {
		t.Fatalf("expected no conflict, got %s", c)
	}
}

func TestDetectContradiction_Chinese(t *testing.T) {
	a := "用户喜欢喝茶"
	b := "用户不喜欢喝茶"
	c := detectContradiction(a, b)
	if c != ConflictContradict {
		t.Fatalf("expected Contradict (CN), got %s", c)
	}
}

func TestAnalyze_DetectsAndSuppressesWeaker(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"conflictThreshold": 0.3})

	now := time.Now()
	a := mkNode("User likes Go", "likes Go", 0.9, 0.8, now)
	b := mkNode("User does not like Go, prefers Rust", "prefers Rust", 0.3, 0.4, now)

	sim := map[[2]uuid.UUID]float64{pairKey(a.ID, b.ID): 0.7}
	rep := p.Analyze([]types.MemoryNode{a, b}, sim)

	if len(rep.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(rep.Conflicts))
	}
	if rep.Conflicts[0].Type != ConflictContradict {
		t.Fatalf("expected Contradict, got %s", rep.Conflicts[0].Type)
	}
	if rep.Conflicts[0].Resolution != ResolveSuppressWeaker {
		t.Fatalf("expected SuppressWeaker, got %s", rep.Conflicts[0].Resolution)
	}
	if len(rep.SuppressIDs) != 1 || rep.SuppressIDs[0] != b.ID {
		t.Fatal("expected weaker node b suppressed")
	}
}

func TestAnalyze_BelowThresholdSkips(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"conflictThreshold": 0.9})

	now := time.Now()
	a := mkNode("User likes Go", "likes Go", 0.9, 0.8, now)
	b := mkNode("User does not like Go", "dislikes Go", 0.3, 0.4, now)

	sim := map[[2]uuid.UUID]float64{pairKey(a.ID, b.ID): 0.5} // below 0.9
	rep := p.Analyze([]types.MemoryNode{a, b}, sim)

	if len(rep.Conflicts) != 0 {
		t.Fatalf("below threshold should skip: got %d conflicts", len(rep.Conflicts))
	}
}

func TestAnalyze_NoConflict(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{})

	a := mkNode("User likes hiking", "hiking", 0.5, 0.5, time.Now())
	b := mkNode("User enjoys music", "music", 0.5, 0.5, time.Now())

	sim := map[[2]uuid.UUID]float64{pairKey(a.ID, b.ID): 0.8}
	rep := p.Analyze([]types.MemoryNode{a, b}, sim)
	if len(rep.Conflicts) != 0 {
		t.Fatalf("no contradiction should be empty, got %d", len(rep.Conflicts))
	}
}
