package types

import (
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
	NodeID    uuid.UUID
	Summary   string
	Relevance float64
}

// CompressedContext is the output of ContextCompressor: the LLM-ready prompt fragment.
type CompressedContext struct {
	SystemPrompt string
	TokenBudget  int
	TokenUsed    int
	Memories     []MemoryRef
	Sources      []string
	// LiveInjectOK is set by MetaCognition after assessing recall quality.
	// When false, even live-mode callers should skip injection for this batch.
	LiveInjectOK bool
}

// NeuroSnapshot captures dopamine / serotonin levels for the user at this turn.
type NeuroSnapshot struct {
	Dopamine  float64
	Serotonin float64
	ACH       float64
	UpdatedAt time.Time
}
