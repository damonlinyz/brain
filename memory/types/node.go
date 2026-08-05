package types

import (
	"time"

	"github.com/google/uuid"
)

// MemoryType categorizes V2 memory nodes by cognitive role.
type MemoryType string

const (
	MemoryTypeSemantic   MemoryType = "semantic"
	MemoryTypeEpisodic   MemoryType = "episodic"
	MemoryTypeProcedural MemoryType = "procedural"
	MemoryTypeProfile    MemoryType = "profile"
)

// Salience indicates emotional / priority weight class.
type Salience string

const (
	SalienceHigh   Salience = "high"
	SalienceNormal Salience = "normal"
	SalienceLow    Salience = "low"
)

// NodeState mirrors the state machine: active → suppressed → archived → extinct.
type NodeState string

const (
	NodeStateActive     NodeState = "active"
	NodeStateSuppressed NodeState = "suppressed"
	NodeStateArchived   NodeState = "archived"
	NodeStateExtinct    NodeState = "extinct"
)

// ContentType describes the kind of fact (used by Builder classification).
type ContentType string

const (
	ContentTypeFact       ContentType = "fact"
	ContentTypePreference ContentType = "preference"
	ContentTypeEvent      ContentType = "event"
	ContentTypeRelation   ContentType = "relationship"
	ContentTypeSkill      ContentType = "skill"
	ContentTypeHistory    ContentType = "historical_version"
)

// Source indicates where the memory originated.
type Source string

const (
	SourceHumanInput     Source = "human_input"
	SourceSearchResult   Source = "search_result"
	SourceInference      Source = "inference"
	SourceTraining       Source = "training"
	SourceUserCorrection Source = "user_correction"
)

// SourceEntry captures one contributing source for multi-source verification.
type SourceEntry struct {
	Source  Source    `json:"source"`
	Trust   float64   `json:"trust"`
	AddedAt time.Time `json:"added_at"`
}

// MemoryNode is the unified memory record persisted to memory_node_meta.
type MemoryNode struct {
	ID            uuid.UUID       `json:"id"`
	UserID        uuid.UUID       `json:"user_id"`
	TenantID      *uuid.UUID      `json:"tenant_id,omitempty"`
	SessionID     *uuid.UUID      `json:"session_id,omitempty"`

	Content       string          `json:"content"`
	Summary       string          `json:"summary"`
	ContentType   ContentType     `json:"content_type"`
	Keywords      []string        `json:"keywords"`

	Source        Source          `json:"source"`
	Sources       []SourceEntry   `json:"sources,omitempty"`

	Type          MemoryType      `json:"type"`
	Salience      Salience        `json:"salience"`
	EmotionalTone string          `json:"emotional_tone"`

	State         NodeState       `json:"state"`
	Weight        float64         `json:"weight"`
	SourceTrust   float64         `json:"source_trust"`

	AccessCount   int             `json:"access_count"`
	LastAccessAt  time.Time       `json:"last_access_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	DeletedAt     *time.Time      `json:"deleted_at,omitempty"`
	Version       int             `json:"version"`

	ActivityContext string        `json:"activity_context,omitempty"`
	SceneContext    string        `json:"scene_context,omitempty"`

	CumulativeReward float64      `json:"cumulative_reward,omitempty"`
	EmotionValence   float64      `json:"emotion_valence,omitempty"`
	EmotionArousal   float64      `json:"emotion_arousal,omitempty"`

	Confidence       float64      `json:"confidence,omitempty"`
	ConsistencyScore float64      `json:"consistency_score,omitempty"`

	UnstableUntil *time.Time      `json:"unstable_until,omitempty"`
	ColdRef       string          `json:"cold_ref,omitempty"`

	DgraphUID     string          `json:"dgraph_uid,omitempty"`
	DgraphSyncedAt time.Time      `json:"dgraph_synced_at,omitempty"`
}

// CreateNodeInput is the write shape for IMemoryStore.CreateNode.
type CreateNodeInput struct {
	UserID        uuid.UUID
	TenantID      *uuid.UUID
	SessionID     *uuid.UUID

	Content       string
	Summary       string
	ContentType   ContentType
	Keywords      []string
	Embedding     []float32

	Source        Source
	Sources       []SourceEntry

	Type          MemoryType
	Salience      Salience
	EmotionalTone string

	SourceTrust   float64
	Weight        float64

	ActivityContext string
	SceneContext    string
}

// MemoryNodeHistory mirrors memory_node_history rows (superseded versions).
type MemoryNodeHistory struct {
	ID                uuid.UUID
	CurrentNodeID     uuid.UUID
	UserID            uuid.UUID
	Content           string
	ContentType       ContentType
	Source            Source
	Weight            float64
	ValidFrom         time.Time
	ValidTo           time.Time
	SupersededByEvent string
	ChangeSummary     string
	CreatedAt         time.Time
}
