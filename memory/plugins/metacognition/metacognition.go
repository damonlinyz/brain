// Package metacognition implements the G4 MetaCognition plugin.
//
// After Trigger recalls candidate memories and before the Compressor injects
// them into the prompt, MetaCognition assesses each candidate's reliability
// — is the system confident enough to assert it as fact, or should it hedge
// or ask the user for clarification?
//
// The assessment is a function of:
//   - Confidence score (from multi-source verification, G3 RealityMonitor)
//   - Consistency score (cross-source agreement, G3)
//   - Source trust (how reliable the originating source was)
//   - Recency (how recently the memory was created or reinforced)
//
// Each memory gets one of: Confident, Hedge, Blurry, Ask.
// The aggregate report tells the caller whether the whole recall batch is
// reliable enough to inject without user confirmation.
package metacognition

import (
	"sync"
	"time"

	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// Level is the confidence tier for one memory.
type Level string

const (
	Confident Level = "confident" // inject freely
	Hedge     Level = "hedge"     // inject but with "you might…" language
	Blurry    Level = "blurry"    // don't inject; could be outdated/wrong
	Ask       Level = "ask"       // actively ask the user to confirm
)

// Assessment is one memory's metacognitive verdict.
type Assessment struct {
	NodeID     uuid.UUID `json:"node_id"`
	Summary    string    `json:"summary"`
	Level      Level     `json:"level"`
	Confidence float64   `json:"confidence"` // raw 0..1 score
	Reason     string    `json:"reason"`
}

// Report is the aggregate metacognitive output for one recall batch.
type Report struct {
	Assessments  []Assessment `json:"assessments"`
	OverallLevel Level        `json:"overall_level"` // min level across batch
	// LiveInjectOK is true iff every assessment is Confident or Hedge.
	LiveInjectOK bool `json:"live_inject_ok"`
}

// Plugin is the MetaCognition mechanism.
type Plugin struct {
	mu                 sync.RWMutex
	confidentThreshold float64 // Confidence >= this → Confident (default 0.7)
	hedgeThreshold     float64 // Confidence >= this → Hedge (default 0.4)
	// Below hedgeThreshold + recent memory → Blurry. Very old + low → Ask.
	blurryMaxAge time.Duration // memories older than this with low confidence → Ask (default 30 days)
}

// Defaults — mirror seed 044 (MetaCognition: {"askThreshold":0.5}).
var Defaults = struct {
	ConfidentThreshold float64
	HedgeThreshold     float64
	BlurryMaxAgeDays   int
}{
	ConfidentThreshold: 0.7,
	HedgeThreshold:     0.4,
	BlurryMaxAgeDays:   30,
}

func New() *Plugin {
	return &Plugin{
		confidentThreshold: Defaults.ConfidentThreshold,
		hedgeThreshold:     Defaults.HedgeThreshold,
		blurryMaxAge:       time.Duration(Defaults.BlurryMaxAgeDays) * 24 * time.Hour,
	}
}

func (p *Plugin) Name() string                   { return "MetaCognition" }
func (p *Plugin) Category() types.PluginCategory { return types.CategoryEdge } // edge — runs between Trigger and Compressor

func (p *Plugin) Init(cfg map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := cfg["askThreshold"]; ok {
		// The seed calls it "askThreshold" — the confidence level below which
		// we switch from hedge to ask. We interpret it as the hedge threshold
		// (below this → blurry/ask).
		if f, ok := toFloat(v); ok && f > 0 {
			p.hedgeThreshold = f
		}
	}
	if v, ok := cfg["confidentThreshold"]; ok {
		if f, ok := toFloat(v); ok && f > p.hedgeThreshold {
			p.confidentThreshold = f
		}
	}
	if v, ok := cfg["blurryMaxAgeDays"]; ok {
		if n, err := toInt(v); err == nil && n > 0 {
			p.blurryMaxAge = time.Duration(n) * 24 * time.Hour
		}
	}
	return nil
}

func (p *Plugin) Close() error { return nil }

// AssessInput is the per-recall-batch input.
type AssessInput struct {
	Nodes []types.MemoryNode // the full nodes behind the recalled memories
	Now   time.Time
}

// Assess evaluates each recalled node and returns a structured report.
func (p *Plugin) Assess(in AssessInput) Report {
	p.mu.RLock()
	confT, hedgeT, maxAge := p.confidentThreshold, p.hedgeThreshold, p.blurryMaxAge
	p.mu.RUnlock()

	if in.Now.IsZero() {
		in.Now = time.Now()
	}

	report := Report{LiveInjectOK: true}

	for _, n := range in.Nodes {
		raw := n.Confidence
		if raw <= 0 {
			// Fall back: estimate from consistency + source_trust.
			raw = (n.ConsistencyScore + n.SourceTrust) / 2.0
		}

		level, reason := classify(n, raw, confT, hedgeT, maxAge, in.Now)
		a := Assessment{
			NodeID: n.ID, Summary: n.Summary, Level: level,
			Confidence: raw, Reason: reason,
		}
		report.Assessments = append(report.Assessments, a)

		// Track overall: min level across batch.
		if levelOrder(level) < levelOrder(report.OverallLevel) || report.OverallLevel == "" {
			report.OverallLevel = level
		}
		if level != Confident && level != Hedge {
			report.LiveInjectOK = false
		}
	}

	if report.OverallLevel == "" {
		report.OverallLevel = Confident
		report.LiveInjectOK = true
	}
	return report
}

func classify(n types.MemoryNode, raw, confT, hedgeT float64, maxAge time.Duration, now time.Time) (Level, string) {
	if raw >= confT && n.ConsistencyScore >= confT {
		return Confident, "high confidence + high consistency"
	}
	if raw >= confT {
		return Hedge, "high confidence but low consistency — hedge language recommended"
	}
	if raw >= hedgeT {
		return Hedge, "moderate confidence"
	}

	// Low confidence: check recency.
	age := now.Sub(n.LastAccessAt)
	if age < 0 {
		age = 0
	}
	if age > maxAge {
		return Ask, "low confidence + memory is old — user verification recommended"
	}
	return Blurry, "low confidence — omit from injection"
}

func levelOrder(l Level) int {
	switch l {
	case Confident:
		return 4
	case Hedge:
		return 3
	case Blurry:
		return 2
	case Ask:
		return 1
	default:
		return 0
	}
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

var errInvalid = errMeta{"metacognition: invalid config value"}

type errMeta struct{ msg string }

func (e errMeta) Error() string { return e.msg }
