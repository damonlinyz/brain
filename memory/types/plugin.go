package types

import (
	"time"
)

// PluginCategory locates the plugin in the stack (edge / engine / store / cold).
type PluginCategory string

const (
	CategoryEdge   PluginCategory = "edge"
	CategoryEngine PluginCategory = "engine"
	CategoryStore  PluginCategory = "store"
	CategoryCold   PluginCategory = "cold"
)

// Plugin is the universal interface every V2 memory mechanism implements.
// Plugins live in registry, are configured via DB-backed engine_plugins.config,
// and are invoked by the Engine in deterministic order.
type Plugin interface {
	Name() string
	Category() PluginCategory
	Init(cfg map[string]any) error
	Close() error
}

// Decision is AttentionFilter's output for a single turn.
type Decision string

const (
	DecisionRemember Decision = "remember"
	DecisionDefer    Decision = "defer"
	DecisionDrop     Decision = "drop"
)

// BuildAction enumerates what Builder did with incoming material.
type BuildAction string

const (
	ActionCreated BuildAction = "created"
	ActionMerged  BuildAction = "merged"
	ActionLinked  BuildAction = "linked"
	ActionSkipped BuildAction = "skipped"
)

// BuildResult is the Builder output: created a new node, merged into existing,
// linked to existing, or skipped (below threshold).
type BuildResult struct {
	Action        BuildAction
	NewNodeID    *string
	MergedNodeID *string
	LinkedIDs    []string
	Reason       string
}

// IntentAssessment is the ContextCompressor output describing the user's intent
// for the current turn — used to decide whether retrieval is needed.
type IntentAssessment struct {
	Intent      string
	BudgetRatio float64
	NeedsMemory bool
	Reason      string
}

// SimilarityReport is PatternSeparation's verdict on incoming material.
type SimilarityReport struct {
	BestMatchID  *string
	BestMatchSim float64
	Decision     BuildAction // created / merged / linked
	Reason       string
}

// TriggerCandidate is one memory recalled by Trigger for current context.
type TriggerCandidate struct {
	NodeID    string
	Summary   string
	Score     float64
	Source    string // vector / keyword / graph / hybrid
}

// ForgettingAction is the Forgetting plugin's verdict on a node.
type ForgettingAction string

const (
	ForgettingKeep      ForgettingAction = "keep"
	ForgettingSuppress  ForgettingAction = "suppress"
	ForgettingArchive   ForgettingAction = "archive"
	ForgettingDelete    ForgettingAction = "delete"
)

// ForgettingDecision captures one node's lifecycle recommendation.
type ForgettingDecision struct {
	NodeID    string
	Action    ForgettingAction
	Reason    string
	Effective time.Time
}

// ConsolidationPlan is what Consolidation schedules for the next sleep cycle.
type ConsolidationPlan struct {
	UserID         string
	DecayCandidates   []string
	LinkCandidates    []string
	ReconsolidateIDs  []string
	ScheduledAt    time.Time
}
