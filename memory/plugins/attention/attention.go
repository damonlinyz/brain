// Package attention implements the AttentionFilter plugin — rule-based
// scoring of incoming conversation material. Decides remember / defer / drop
// per turn based on weighted signals: explicit cue, salience, emotion,
// dopamine, trust.
package attention

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/types"
)

// Weights tunes each signal's contribution to the final score.
type Weights struct {
	Explicit float64
	Salience float64
	Emotion  float64
	Dopamine float64
	Trust    float64
}

// Thresholds map score to Decision.
type Thresholds struct {
	Remember float64 // >= Remember → remember
	Defer    float64 // >= Defer (but < Remember) → defer; lower → drop
}

// Plugin is the AttentionFilter. State is read-only after Init.
type Plugin struct {
	mu          sync.RWMutex
	weights     Weights
	thresholds  Thresholds
	maxEmotion  float64 // clamp emotion contribution
}

// Default config used when cfg omits fields.
var Defaults = struct {
	Weights    Weights
	Thresholds Thresholds
}{
	Weights:    Weights{Explicit: 0.95, Salience: 0.2, Emotion: 0.15, Dopamine: 0.1, Trust: 0.1},
	Thresholds: Thresholds{Remember: 0.6, Defer: 0.3},
}

// explicitCues are substrings that mark a turn as must-remember.
var explicitCues = []string{
	"remember", "don't forget", "note that", "take note",
	"remember this", "记住", "别忘了", "记一下", "备注", "重要",
}

// emotionMarkers bump emotional_valence / arousal estimates from text heuristics.
var positiveMarkers = []string{"love", "great", "excited", "happy", "amazing", "爱", "开心", "兴奋", "太棒"}
var negativeMarkers = []string{"hate", "terrible", "angry", "sad", "fear", "讨厌", "生气", "害怕", "糟糕"}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string                     { return "AttentionFilter" }
func (p *Plugin) Category() types.PluginCategory   { return types.CategoryEdge }

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.weights = Defaults.Weights
	p.thresholds = Defaults.Thresholds
	p.maxEmotion = 1.0
	if v, ok := cfg["weights"].(map[string]any); ok {
		applyFloat(&p.weights.Explicit, v["explicit"])
		applyFloat(&p.weights.Salience, v["salience"])
		applyFloat(&p.weights.Emotion, v["emotion"])
		applyFloat(&p.weights.Dopamine, v["dopamine"])
		applyFloat(&p.weights.Trust, v["trust"])
	}
	if v, ok := cfg["thresholds"].(map[string]any); ok {
		applyFloat(&p.thresholds.Remember, v["remember"])
		applyFloat(&p.thresholds.Defer, v["defer"])
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// ScoreInput is what callers supply per turn.
type ScoreInput struct {
	Content       string
	Salience      types.Salience
	SourceTrust   float64
	DopamineLevel float64 // current user dopamine from NeuroSnapshot, 0..1
}

// Assess produces an AttentionSignal for the current turn.
func (p *Plugin) Assess(ctx context.Context, in ScoreInput) types.AttentionSignal {
	p.mu.RLock()
	w := p.weights
	th := p.thresholds
	p.mu.RUnlock()

	lower := strings.ToLower(in.Content)
	explicit := 0.0
	if hasAny(lower, explicitCues) {
		explicit = 1.0
	}
	salienceVal := salienceToFloat(in.Salience)
	valence, arousal := estimateEmotion(lower)
	emotionMag := abs(valence) + arousal
	if emotionMag > p.maxEmotion {
		emotionMag = p.maxEmotion
	}
	trust := clamp(in.SourceTrust, 0, 1)
	dopamine := clamp(in.DopamineLevel, 0, 1)

	score := w.Explicit*explicit +
		w.Salience*salienceVal +
		w.Emotion*emotionMag +
		w.Dopamine*dopamine +
		w.Trust*trust
	if score > 1.0 {
		score = 1.0
	}

	decision := types.DecisionDrop
	if score >= th.Remember {
		decision = types.DecisionRemember
	} else if score >= th.Defer {
		decision = types.DecisionDefer
	}

	reasons := []string{}
	if explicit > 0 {
		reasons = append(reasons, "explicit_cue")
	}
	if salienceVal > 0.5 {
		reasons = append(reasons, "high_salience")
	}
	if emotionMag > 0.4 {
		reasons = append(reasons, "emotional")
	}
	if dopamine > 0.6 {
		reasons = append(reasons, "dopamine_boost")
	}
	if trust > 0.7 {
		reasons = append(reasons, "trusted_source")
	}

	sal := in.Salience
	if sal == "" {
		sal = types.SalienceNormal
	}

	return types.AttentionSignal{
		Score:             score,
		Decision:          decision,
		TriggeredSalience: sal,
		EmotionalValence:  valence,
		EmotionalArousal:  arousal,
		Reasons:           reasons,
	}
}

func salienceToFloat(s types.Salience) float64 {
	switch s {
	case types.SalienceHigh:
		return 1.0
	case types.SalienceLow:
		return 0.2
	default:
		return 0.5
	}
}

func estimateEmotion(lower string) (valence, arousal float64) {
	pos := countAny(lower, positiveMarkers)
	neg := countAny(lower, negativeMarkers)
	total := pos + neg
	if total == 0 {
		return 0, 0
	}
	valence = float64(pos-neg) / float64(total)
	arousal = float64(total) / 10.0
	if arousal > 1.0 {
		arousal = 1.0
	}
	return
}

func hasAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func countAny(s string, subs []string) int {
	n := 0
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func clamp(f, lo, hi float64) float64 {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
}

func applyFloat(dst *float64, v any) {
	if v == nil {
		return
	}
	switch x := v.(type) {
	case float64:
		*dst = x
	case float32:
		*dst = float64(x)
	case int:
		*dst = float64(x)
	}
}

// touchTime avoids unused import if future hooks need timestamps.
var _ = time.Now

// touchContext expresses intent — Assess honors ctx for cancellation in future hooks.
var _ = context.Background
