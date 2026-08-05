// Package interference implements the G5 Interference plugin — detects
// contradictions between recalled memory candidates and recommends resolutions
// (suppress-weaker, suppress-older, keep-both, merge).
//
// MVP is word-overlap + negation-marker based (no LLM judge). Full LLM-based
// conflict analysis is the BuilderContradiction enhancement.
package interference

import (
	"strings"
	"sync"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// ConflictType labels the logical contradiction.
type ConflictType string

const (
	ConflictContradict  ConflictType = "contradict"
	ConflictSupersede   ConflictType = "supersede"
	ConflictDuplicate   ConflictType = "duplicate"
)

// Resolution is the recommended action.
type Resolution string

const (
	ResolveKeepBoth      Resolution = "keep_both"
	ResolveSuppressOlder  Resolution = "suppress_older"
	ResolveSuppressWeaker Resolution = "suppress_weaker"
	ResolveMerge          Resolution = "merge"
)

// Conflict is one detected contradiction between two recalled nodes.
type Conflict struct {
	NodeA      uuid.UUID
	NodeB      uuid.UUID
	Type       ConflictType
	Resolution Resolution
	Reason     string
}

// Report is the interference analysis output.
type Report struct {
	Conflicts   []Conflict
	SuppressIDs []uuid.UUID
}

// Plugin is the Interference mechanism.
type Plugin struct {
	mu                sync.RWMutex
	conflictThreshold float64
}

var Defaults = struct{ ConflictThreshold float64 }{ConflictThreshold: 0.6}

// Contradiction markers — if one side contains one of these, check for topic overlap.
var negationMarkers = []string{
	"no ", "not ", "never ", "don't ", "doesn't ", "isn't ", "aren't ",
	"instead ", "rather ", "actually ",
	"不", "不是", "没有", "从不", "并非",
}

func New() *Plugin {
	return &Plugin{conflictThreshold: Defaults.ConflictThreshold}
}

func (p *Plugin) Name() string                   { return "Interference" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["conflictThreshold"]; ok {
		if f, ok := toFloat(v); ok && f > 0 {
			p.conflictThreshold = f
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// Analyze compares recall candidates pairwise for contradictions.
func (p *Plugin) Analyze(nodes []types.MemoryNode, similarities map[[2]uuid.UUID]float64) Report {
	p.mu.RLock()
	threshold := p.conflictThreshold
	p.mu.RUnlock()

	var report Report
	seen := map[[2]uuid.UUID]bool{}

	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			a, b := nodes[i], nodes[j]
			key := pairKey(a.ID, b.ID)
			if seen[key] {
				continue
			}
			seen[key] = true

			sim := similarities[key]
			if sim < threshold {
				continue
			}

			cType := detectContradiction(a.Content, b.Content)
			if cType == "" {
				continue
			}

			resolution := pickResolution(a, b, cType)
			conflict := Conflict{
				NodeA: a.ID, NodeB: b.ID, Type: cType,
				Resolution: resolution,
				Reason:     "'" + a.Summary + "' vs '" + b.Summary + "'",
			}
			report.Conflicts = append(report.Conflicts, conflict)

			switch resolution {
			case ResolveSuppressOlder:
				if a.CreatedAt.Before(b.CreatedAt) {
					report.SuppressIDs = append(report.SuppressIDs, a.ID)
				} else {
					report.SuppressIDs = append(report.SuppressIDs, b.ID)
				}
			case ResolveSuppressWeaker:
				if a.Confidence < b.Confidence || (a.Confidence == b.Confidence && a.Weight < b.Weight) {
					report.SuppressIDs = append(report.SuppressIDs, a.ID)
				} else {
					report.SuppressIDs = append(report.SuppressIDs, b.ID)
				}
			}
		}
	}
	return report
}

func detectContradiction(a, b string) ConflictType {
	la, lb := strings.ToLower(a), strings.ToLower(b)

	if hasNegation(la) && topicOverlap(stripNegation(la), lb) {
		return ConflictContradict
	}
	if hasNegation(lb) && topicOverlap(stripNegation(lb), la) {
		return ConflictContradict
	}
	return ""
}

func hasNegation(s string) bool {
	for _, m := range negationMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func stripNegation(s string) string {
	for _, prefix := range []string{
		"do not ", "does not ", "did not ", "doesn't ", "don't ", "didn't ",
		"not ", "never ", "no ", "instead ", "rather ", "actually ",
		"不", "不是", "没有", "从不", "并非",
	} {
		s = strings.ReplaceAll(s, prefix, "")
	}
	return strings.TrimSpace(s)
}

// topicOverlap returns true when the two contents share ≥40% significant words.
func topicOverlap(a, b string) bool {
	wa := significantWords(a)
	wb := significantWords(b)
	if len(wa) < 1 || len(wb) < 1 {
		return false
	}
	set := map[string]bool{}
	for _, w := range wa {
		set[w] = true
	}
	match := 0
	for _, w := range wb {
		if set[w] {
			match++
		}
	}
	return float64(match)/float64(len(wb)) >= 0.4
}

func significantWords(s string) []string {
	words := strings.Fields(s)
	out := make([]string, 0, len(words))
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
		"were": true, "be": true, "been": true, "for": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true, "of": true,
		"with": true, "by": true, "from": true, "as": true, "this": true, "that": true,
		"it": true, "its": true, "has": true, "have": true, "had": true,
		"的": true, "是": true, "了": true, "在": true, "很": true, "都": true, "也": true,
	}
	for _, w := range words {
		w = strings.Trim(w, ",.;:!?'\"()[]{}。！？，；：")
		if len(w) >= 3 && !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

func pickResolution(a, b types.MemoryNode, cType ConflictType) Resolution {
	switch cType {
	case ConflictContradict:
		return ResolveSuppressWeaker
	case ConflictSupersede:
		return ResolveSuppressOlder
	case ConflictDuplicate:
		return ResolveMerge
	}
	return ResolveKeepBoth
}

func pairKey(a, b uuid.UUID) [2]uuid.UUID {
	if a.String() < b.String() {
		return [2]uuid.UUID{a, b}
	}
	return [2]uuid.UUID{b, a}
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
