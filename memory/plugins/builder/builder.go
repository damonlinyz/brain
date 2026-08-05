// Package builder implements the Builder plugin — takes raw user input
// (chat message, daily note, search result) and extracts structured memory
// facts ready to be persisted. Uses an LLM to do the extraction; falls back
// to a trivial keyword splitter if the LLM call fails or is unavailable.
package builder

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/types"
)

// LLMClient is the minimal slice of services.LLM / ProviderRouter that Builder
// needs. Keep it small so tests can stub it.
type LLMClient interface {
	Available() bool
	Chat(systemPrompt, userPrompt string) (string, error)
}

// ExtractInput is what a caller hands to Extract.
type ExtractInput struct {
	UserID   string // for logging only — not in extracted fact
	RawText  string
	Source   types.Source
	// SessionID / SceneContext / ActivityContext pass-through.
	SceneContext    string
	ActivityContext string
}

// ExtractedFact is one memory extracted from the raw input. The caller is
// responsible for embedding + persistence.
type ExtractedFact struct {
	Content       string
	Summary       string
	ContentType   types.ContentType
	Keywords      []string
	Type          types.MemoryType
	Salience      types.Salience
	EmotionalTone string
}

// Plugin is the Builder.
type Plugin struct {
	mu              sync.RWMutex
	llm             LLMClient
	extractorModel  string
	llmTimeout      time.Duration
	fallbackOnFail  bool
	maxFactsPerTurn int
}

// Defaults — mirror seed 044_v2_memory_hub_seed.sql.
var Defaults = struct {
	ExtractorModel  string
	LLMTimeoutMs    int
	FallbackOnFail  bool
	MaxFactsPerTurn int
}{
	ExtractorModel:  "deepseek-chat",
	LLMTimeoutMs:    2500,
	FallbackOnFail:  true,
	MaxFactsPerTurn: 10,
}

func New() *Plugin {
	return &Plugin{
		extractorModel:  Defaults.ExtractorModel,
		llmTimeout:      time.Duration(Defaults.LLMTimeoutMs) * time.Millisecond,
		fallbackOnFail:  Defaults.FallbackOnFail,
		maxFactsPerTurn: Defaults.MaxFactsPerTurn,
	}
}

func (p *Plugin) Name() string                   { return "Builder" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEngine }

// AttachLLM wires the LLM client. Optional — without it Builder always falls
// back to the trivial extractor.
func (p *Plugin) AttachLLM(c LLMClient) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.llm = c
}

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["extractorModel"]; ok {
		if s, ok := v.(string); ok && s != "" {
			p.extractorModel = s
		}
	}
	if v, ok := cfg["llmTimeoutMs"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.llmTimeout = time.Duration(n) * time.Millisecond
		}
	}
	if v, ok := cfg["fallbackOnLLMFail"]; ok {
		if b, ok := v.(bool); ok {
			p.fallbackOnFail = b
		}
	}
	if v, ok := cfg["maxFactsPerTurn"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.maxFactsPerTurn = n
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// Extract pulls structured facts out of in.RawText. Returns at most
// maxFactsPerTurn; if LLM is unavailable or errors and fallbackOnFail is true,
// we split on sentence boundaries and emit at most one fact per sentence.
func (p *Plugin) Extract(ctx context.Context, in ExtractInput) ([]ExtractedFact, error) {
	if strings.TrimSpace(in.RawText) == "" {
		return nil, nil
	}

	p.mu.RLock()
	llm, timeout, fallback, maxFacts := p.llm, p.llmTimeout, p.fallbackOnFail, p.maxFactsPerTurn
	p.mu.RUnlock()

	if llm == nil || !llm.Available() {
		if !fallback {
			return nil, nil
		}
		return fallbackExtract(in, maxFacts), nil
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan llmResult, 1)
	go func() {
		sys := buildSystemPrompt(maxFacts)
		out, err := llm.Chat(sys, in.RawText)
		done <- llmResult{content: out, err: err}
	}()

	select {
	case <-cctx.Done():
		if fallback {
			return fallbackExtract(in, maxFacts), nil
		}
		return nil, cctx.Err()
	case r := <-done:
		if r.err != nil {
			if fallback {
				return fallbackExtract(in, maxFacts), nil
			}
			return nil, r.err
		}
		facts := parseLLMJSON(r.content, maxFacts)
		if len(facts) == 0 && fallback {
			return fallbackExtract(in, maxFacts), nil
		}
		return facts, nil
	}
}

type llmResult struct {
	content string
	err     error
}

func buildSystemPrompt(maxFacts int) string {
	return strings.TrimSpace(`
You are a memory extractor. Read the user's text and pull out durable facts
worth remembering long-term. Return STRICT JSON only — no commentary.

Output schema:
{
  "facts": [
    {
      "content": "<the fact in one short sentence, third person>",
      "summary": "<5-8 word label>",
      "content_type": "fact|preference|event|relationship|skill|historical_version",
      "keywords": ["a", "b"],
      "type": "semantic|episodic|procedural|profile",
      "salience": "high|normal|low",
      "emotional_tone": "<one word or empty>"
    }
  ]
}

Rules:
- Skip small talk, greetings, and transient requests ("what's the weather").
- At most `+itoa(maxFacts)+` facts.
- Each fact must be self-contained — no "as mentioned earlier".
- Prefer "User is allergic to peanuts" over "allergic to peanuts".
`)
}

// parseLLMJSON is permissive: it tolerates leading/trailing prose, code fences,
// and partial JSON. On any error it returns nil (caller decides fallback).
func parseLLMJSON(raw string, maxFacts int) []ExtractedFact {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Strip ```json fences if present.
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	// Find first { … } balanced envelope to skip any preamble.
	start := strings.Index(raw, "{")
	if start < 0 {
		return nil
	}
	end := strings.LastIndex(raw, "}")
	if end < start {
		return nil
	}
	body := raw[start : end+1]

	var decoded struct {
		Facts []struct {
			Content       string   `json:"content"`
			Summary       string   `json:"summary"`
			ContentType   string   `json:"content_type"`
			Keywords      []string `json:"keywords"`
			Type          string   `json:"type"`
			Salience      string   `json:"salience"`
			EmotionalTone string   `json:"emotional_tone"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil
	}

	out := make([]ExtractedFact, 0, len(decoded.Facts))
	for _, f := range decoded.Facts {
		if strings.TrimSpace(f.Content) == "" {
			continue
		}
		out = append(out, ExtractedFact{
			Content:       strings.TrimSpace(f.Content),
			Summary:       firstNonEmpty(f.Summary, truncate(f.Content, 60)),
			ContentType:   normalizeContentType(f.ContentType),
			Keywords:      f.Keywords,
			Type:          normalizeType(f.Type),
			Salience:      normalizeSalience(f.Salience),
			EmotionalTone: strings.TrimSpace(f.EmotionalTone),
		})
		if len(out) >= maxFacts {
			break
		}
	}
	return out
}

// fallbackExtract is the no-LLM path: split on sentence enders, emit one fact
// per non-trivial sentence as a semantic/normal memory.
func fallbackExtract(in ExtractInput, maxFacts int) []ExtractedFact {
	sentences := splitSentences(in.RawText)
	out := make([]ExtractedFact, 0, len(sentences))
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < 4 {
			continue
		}
		out = append(out, ExtractedFact{
			Content:     s,
			Summary:     truncate(s, 60),
			ContentType: types.ContentTypeFact,
			Keywords:    extractKeywords(s),
			Type:        types.MemoryTypeSemantic,
			Salience:    types.SalienceNormal,
		})
		if len(out) >= maxFacts {
			break
		}
	}
	return out
}

func splitSentences(text string) []string {
	text = strings.ReplaceAll(text, "\n", " ")
	for _, sep := range []string{". ", "! ", "? ", "。", "！", "？"} {
		text = strings.ReplaceAll(text, sep, sep+"|")
	}
	parts := strings.Split(text, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

func extractKeywords(s string) []string {
	words := strings.Fields(s)
	out := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, ",.;:!?'\"()[]{}。！？，；：")
		if len([]rune(w)) < 2 {
			continue
		}
		out = append(out, strings.ToLower(w))
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func normalizeContentType(s string) types.ContentType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "preference":
		return types.ContentTypePreference
	case "event":
		return types.ContentTypeEvent
	case "relationship":
		return types.ContentTypeRelation
	case "skill":
		return types.ContentTypeSkill
	case "historical_version":
		return types.ContentTypeHistory
	default:
		return types.ContentTypeFact
	}
}

func normalizeType(s string) types.MemoryType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "episodic":
		return types.MemoryTypeEpisodic
	case "procedural":
		return types.MemoryTypeProcedural
	case "profile":
		return types.MemoryTypeProfile
	default:
		return types.MemoryTypeSemantic
	}
}

func normalizeSalience(s string) types.Salience {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high":
		return types.SalienceHigh
	case "low":
		return types.SalienceLow
	default:
		return types.SalienceNormal
	}
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

var errInvalid = &cerr{"builder: invalid config value"}

type cerr struct{ msg string }

func (e *cerr) Error() string { return e.msg }
