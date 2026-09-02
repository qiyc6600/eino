package memory

import "context"

// MemoryStore is the interface for long-term memory persistence.
// Must be separate from CheckpointStore (short-term memory).
// Data model and lifecycle are fundamentally different:
// - Short-term memory = state snapshot (CheckpointStore)
// - Long-term memory = fact/preference entries (MemoryStore)
type MemoryStore interface {
	Put(ctx context.Context, entry MemoryEntry) error
	Get(ctx context.Context, userID, key string) (MemoryEntry, bool, error)
	List(ctx context.Context, userID string) ([]MemoryEntry, error)
	Delete(ctx context.Context, userID, key string) error
}

// Memory entry types. An empty Type is treated as MemoryTypePreference.
const (
	MemoryTypePreference string = "preference" // 用户偏好（语言、风格、格式…）
	MemoryTypeIdentity   string = "identity"   // 用户身份（城市、角色、画像）
	MemoryTypeFact       string = "fact"       // 沉淀的事实
	MemoryTypeEpisode    string = "episode"    // 对话情景摘录（可被整合为 fact）
	MemoryTypeRule       string = "rule"       // 用户给定的长期规则
)

// Memory importance bounds (1 = marginal, 5 = essential).
const (
	MinImportance = 1
	MaxImportance = 5
	// DefaultImportance applies when an entry carries no explicit importance.
	DefaultImportance = 3
)

// MaxValueHistory caps how many superseded values are retained per entry.
const MaxValueHistory = 5

// ValueRevision records a superseded value so memory evolution keeps its
// provenance instead of silently overwriting.
type ValueRevision struct {
	Value        string `json:"value"`
	Source       string `json:"source,omitempty"`
	SupersededAt string `json:"superseded_at"`
}

// MemoryEntry represents a single long-term memory fact/preference.
// All fields beyond the original core are omitempty so store files written
// by older versions load cleanly (missing fields fall back to defaults).
type MemoryEntry struct {
	UserID    string `json:"user_id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Source    string `json:"source"` // e.g., "user_stated", "llm_extracted", "consolidated"
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	// --- v2 fields ---
	Type       string `json:"type,omitempty"`       // see MemoryType* constants
	Importance int    `json:"importance,omitempty"` // 1-5; 0 treated as DefaultImportance

	SourceThreadID string `json:"source_thread_id,omitempty"`
	SourceExcerpt  string `json:"source_excerpt,omitempty"` // truncated original message

	AccessCount    int    `json:"access_count,omitempty"`
	LastAccessedAt string `json:"last_accessed_at,omitempty"`

	Archived bool `json:"archived,omitempty"` // excluded from retrieval & default listing

	History []ValueRevision `json:"history,omitempty"` // superseded values, newest first
}

// EffectiveImportance returns the entry's importance with the zero-value
// default applied.
func (e MemoryEntry) EffectiveImportance() int {
	if e.Importance < MinImportance || e.Importance > MaxImportance {
		return DefaultImportance
	}
	return e.Importance
}

// EffectiveType returns the entry's type with the legacy default applied.
func (e MemoryEntry) EffectiveType() string {
	if e.Type == "" {
		return MemoryTypePreference
	}
	return e.Type
}
