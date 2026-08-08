package types

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MessageTurn is a single turn in recent conversation history.
type MessageTurn struct {
	Role    string    // user / assistant / system
	Content string
	SentAt  time.Time
}

// ConversationContext is the runtime shape passed into Engine.Process for each turn.
type ConversationContext struct {
	UserID         uuid.UUID
	TenantID       *uuid.UUID
	SessionID      *uuid.UUID
	Now            time.Time
	RecentMessages []MessageTurn
	Profile        map[string]any

	// Edge signals (filled by Edge plugins before Engine.Process is invoked)
	Attention     *AttentionSignal
	WorkingMemory []MemoryRef

	// Neuromodulator state snapshot
	NeuroState *NeuroSnapshot
}

// AttentionSignal is the AttentionFilter output: what's worth remembering from this turn.
type AttentionSignal struct {
	Score         float64   // 0..1
	Decision      Decision  // remember / defer / drop
	TriggeredSalience Salience
	EmotionalValence  float64
	EmotionalArousal  float64
	Reasons       []string
}

// MemoryRef is a lightweight pointer to a memory node used in context compression.
type MemoryRef struct {
	NodeID    uuid.UUID `json:"node_id"`
	Summary   string    `json:"summary"`
	Relevance float64   `json:"relevance"`
	// Source is how this memory was recalled: "vector" / "keyword" / "graph" /
	// "hybrid" / "core". Empty for legacy callers.
	Source string `json:"source,omitempty"`
	// Detail carries provenance colour — e.g. the edge kind ("similar"/"causal")
	// for graph-recalled neighbours, or "pinned" for core-tier nodes.
	Detail string `json:"detail,omitempty"`
	// Tier is the node's storage tier: "core" (always-inject) or "normal".
	Tier string `json:"tier,omitempty"`
}

// CompressedContext is the output of ContextCompressor: the LLM-ready prompt fragment.
type CompressedContext struct {
	SystemPrompt string      `json:"system_prompt"`
	TokenBudget  int         `json:"token_budget"`
	TokenUsed    int         `json:"token_used"`
	Memories     []MemoryRef `json:"memories"`
	Sources      []string    `json:"sources,omitempty"`
	// LiveInjectOK is set by MetaCognition after assessing recall quality.
	// When false, even live-mode callers should skip injection for this batch.
	LiveInjectOK bool `json:"live_inject_ok"`
}

// Markdown renders a human-readable view of the recalled context: one bullet per
// memory with its relevance and provenance. Intended for inspection / debugging,
// not for LLM injection (that's SystemPrompt). [#1 readable layer]
func (c CompressedContext) Markdown() string {
	var sb strings.Builder
	n := len(c.Memories)
	fmt.Fprintf(&sb, "# 🧠 Recalled Memory — %d item", n)
	if n != 1 {
		sb.WriteByte('s')
	}
	fmt.Fprintf(&sb, "  (%d/%d tokens, inject=%t)\n\n", c.TokenUsed, c.TokenBudget, c.LiveInjectOK)
	if n == 0 {
		sb.WriteString("_No memories cleared the recall floor._\n")
		return sb.String()
	}
	for _, m := range c.Memories {
		tag := m.Source
		if m.Detail != "" {
			tag += "·" + m.Detail
		}
		if tag == "" {
			tag = "memory"
		}
		mark := "•"
		if m.Tier == "core" {
			mark = "📌"
		}
		fmt.Fprintf(&sb, "%s `[%s %.0f%%]` %s\n", mark, tag, m.Relevance*100, m.Summary)
	}
	return sb.String()
}

// NeuroSnapshot captures dopamine / serotonin levels for the user at this turn.
type NeuroSnapshot struct {
	Dopamine  float64
	Serotonin float64
	ACH       float64
	UpdatedAt time.Time
}
