package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ReservedKeyPrefix marks entries the framework owns. They live in the same
// store as ordinary entries — so they inherit user isolation and backend
// switching — but they are never listed, retrieved, consolidated, or editable
// through the memory API.
const ReservedKeyPrefix = "__"

// settingsKey is the reserved entry holding a user's memory preferences.
const settingsKey = ReservedKeyPrefix + "settings"

// IsReservedKey reports whether a key belongs to the framework rather than the user.
func IsReservedKey(key string) bool {
	return strings.HasPrefix(key, ReservedKeyPrefix)
}

// MemorySettings holds a user's memory preferences.
type MemorySettings struct {
	// Enabled=false means "do not remember": nothing is extracted from new turns
	// and nothing existing is injected. Entries are kept rather than deleted, so
	// turning it back on restores the previous state.
	Enabled bool `json:"memory_enabled"`
}

// DefaultMemorySettings is what a user who never changed anything gets.
func DefaultMemorySettings() MemorySettings {
	return MemorySettings{Enabled: true}
}

// GetSettings reads a user's memory preferences. A missing or unreadable entry
// falls back to the default rather than silently changing behavior.
func (s *Service) GetSettings(ctx context.Context, userID string) (MemorySettings, error) {
	entry, ok, err := s.store.Get(ctx, userID, settingsKey)
	if err != nil {
		return DefaultMemorySettings(), err
	}
	if !ok || entry.Value == "" {
		return DefaultMemorySettings(), nil
	}
	settings := DefaultMemorySettings()
	if err := json.Unmarshal([]byte(entry.Value), &settings); err != nil {
		return DefaultMemorySettings(), nil
	}
	return settings, nil
}

// SetSettings persists a user's memory preferences.
func (s *Service) SetSettings(ctx context.Context, userID string, settings MemorySettings) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	entry := MemoryEntry{
		UserID:     userID,
		Key:        settingsKey,
		Value:      string(raw),
		Source:     "user_setting",
		Type:       MemoryTypeRule,
		Importance: MaxImportance,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if existing, ok, err := s.store.Get(ctx, userID, settingsKey); err == nil && ok {
		entry.CreatedAt = existing.CreatedAt
	}
	return s.store.Put(ctx, entry)
}

// MemoryEnabled reports whether long-term memory is active for this user. Both
// the write path (ExtractAndSave) and the read path (RetrieveRelevant) consult
// it internally, so no caller can bypass the switch.
func (s *Service) MemoryEnabled(ctx context.Context, userID string) bool {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		// Fail open: an unreadable setting must not silently stop memory.
		return true
	}
	return settings.Enabled
}
