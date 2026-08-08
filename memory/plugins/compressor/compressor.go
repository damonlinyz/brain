// Package compressor implements the ContextCompressor plugin — given recalled
// memories + a token budget, pick the highest-relevance subset and render a
// CompressedContext ready for LLM prompt injection.
package compressor

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Plugin is the ContextCompressor.
type Plugin struct {
	mu         sync.RWMutex
	minBudget  int
	maxBudget  int
	tokensPer  float64 // tokens per character (rough)
	systemPfx  string
}

// Defaults — tuned for DeepSeek-class tokenizers on mixed CJK/English.
var Defaults = struct {
	MinBudget int
	MaxBudget int
	TokensPer float64
	SystemPfx string
}{
	MinBudget: 200,
	MaxBudget: 4000,
	TokensPer: 0.3,
	SystemPfx: "Relevant memories:\n",
}

func New() *Plugin {
	p := &Plugin{
		minBudget: Defaults.MinBudget,
		maxBudget: Defaults.MaxBudget,
		tokensPer: Defaults.TokensPer,
		systemPfx: Defaults.SystemPfx,
	}
	return p
}

func (p *Plugin) Name() string                     { return "ContextCompressor" }
func (p *Plugin) Category() types.PluginCategory   { return types.CategoryEdge }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["minBudget"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.minBudget = n
		}
	}
	if v, ok := cfg["maxBudget"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.maxBudget = n
		}
	}
	if v, ok := cfg["tokensPerChar"]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			p.tokensPer = f
		}
	}
	if v, ok := cfg["systemPrefix"]; ok {
		if s, ok := v.(string); ok {
			p.systemPfx = s
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// CompressInput is the per-turn input.
type CompressInput struct {
	Candidates []types.TriggerCandidate
	DesiredBudget int // 0 → fall back to maxBudget
}

// Compress selects the highest-relevance candidates that fit within the budget
// and renders them into a CompressedContext.
func (p *Plugin) Compress(ctx context.Context, in CompressInput) types.CompressedContext {
	p.mu.RLock()
	minB, maxB, per, pfx := p.minBudget, p.maxBudget, p.tokensPer, p.systemPfx
	p.mu.RUnlock()

	budget := in.DesiredBudget
	if budget == 0 {
		budget = maxB
	}
	if budget < minB {
		budget = minB
	}
	if budget > maxB {
		budget = maxB
	}

	// Sort by score desc (stable on ties)
	sorted := make([]types.TriggerCandidate, len(in.Candidates))
	copy(sorted, in.Candidates)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})

	picked := make([]types.MemoryRef, 0, len(sorted))
	used := estimateTokens(pfx, per)
	sources := []string{}
	for _, c := range sorted {
		cost := estimateTokens(c.Summary, per) + 8 // bullet prefix + newline
		if used+cost > budget {
			continue
		}
		picked = append(picked, types.MemoryRef{
			NodeID:    parseNodeID(c.NodeID),
			Summary:   c.Summary,
			Relevance: c.Score,
			Source:    c.Source,
			Detail:    c.Detail,
			Tier:      c.Tier,
		})
		sources = append(sources, c.Source)
		used += cost
	}

	prompt := pfx
	for _, ref := range picked {
		prompt += "- " + ref.Summary + "\n"
	}

	return types.CompressedContext{
		SystemPrompt: prompt,
		TokenBudget:  budget,
		TokenUsed:    used,
		Memories:     picked,
		Sources:      sources,
	}
}

func estimateTokens(s string, per float64) int {
	if s == "" {
		return 0
	}
	n := int(float64(len([]rune(s))) * per)
	if n < 1 {
		n = 1
	}
	return n
}

func parseNodeID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
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

var errInvalid = &cerr{"compressor: invalid config value"}

type cerr struct{ msg string }

func (e *cerr) Error() string { return e.msg }

// _ keeps strings import live (used in caller tests).
var _ = strings.Contains
