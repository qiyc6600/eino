package memory

import (
	"context"
	"strings"
	"time"
)

// threadTitleKeyPrefix namespaces a conversation's display name.
//
// Titles live in the memory store's reserved-key space, the same mechanism the
// documents use: they inherit user isolation and all three storage backends
// without a schema change, and IsReservedKey keeps them out of the memory list,
// retrieval and consolidation. The prefix is deliberately not "__thread_" — that
// one is the thread-scoped *preference* namespace, and a title is not a
// preference: ListThreadPreferences would return it.
const threadTitleKeyPrefix = ReservedKeyPrefix + "title_"

// MaxThreadTitleRunes bounds a title. It is a sidebar label, not a message: a
// long one is truncated by the UI anyway, and an unbounded field would let a
// client store an essay under a name.
const MaxThreadTitleRunes = 60

func threadTitleKey(threadID string) string {
	return threadTitleKeyPrefix + threadID
}

// ThreadTitle returns a conversation's display name, or ok=false when it has
// none (a thread created before titles existed, or one never chatted in).
func (s *Service) ThreadTitle(ctx context.Context, userID, threadID string) (string, bool, error) {
	if threadID == "" {
		return "", false, nil
	}
	entry, ok, err := s.store.Get(ctx, userID, threadTitleKey(threadID))
	if err != nil || !ok {
		return "", false, err
	}
	return entry.Value, entry.Value != "", nil
}

// ThreadTitles returns every titled conversation for a user, keyed by thread ID.
func (s *Service) ThreadTitles(ctx context.Context, userID string) (map[string]string, error) {
	entries, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	titles := make(map[string]string)
	for _, e := range entries {
		if !strings.HasPrefix(e.Key, threadTitleKeyPrefix) || e.Value == "" {
			continue
		}
		titles[strings.TrimPrefix(e.Key, threadTitleKeyPrefix)] = e.Value
	}
	return titles, nil
}

// SetThreadTitle stores a conversation's display name. An empty title clears it,
// which puts the sidebar back to showing the thread ID — the same state a
// conversation starts in, so "clear" is reversible rather than destructive.
func (s *Service) SetThreadTitle(ctx context.Context, userID, threadID, title string) error {
	if threadID == "" {
		return nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return s.DeleteThreadTitle(ctx, userID, threadID)
	}
	if runes := []rune(title); len(runes) > MaxThreadTitleRunes {
		title = string(runes[:MaxThreadTitleRunes])
	}

	key := threadTitleKey(threadID)
	now := time.Now().Format(time.RFC3339)
	entry := MemoryEntry{
		UserID:     userID,
		Key:        key,
		Value:      title,
		Source:     "thread_title",
		Type:       MemoryTypeRule,
		Importance: MaxImportance,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	// Keep the original creation time: this is the same entry being renamed, not
	// a new one, and the revision history of a title is noise.
	if existing, ok, err := s.store.Get(ctx, userID, key); err == nil && ok {
		entry.CreatedAt = existing.CreatedAt
	}
	return s.store.Put(ctx, entry)
}

// DeleteThreadTitle drops a conversation's title. Called when the conversation
// itself is deleted, so a deleted thread does not leave its name behind.
func (s *Service) DeleteThreadTitle(ctx context.Context, userID, threadID string) error {
	if threadID == "" {
		return nil
	}
	return s.store.Delete(ctx, userID, threadTitleKey(threadID))
}
