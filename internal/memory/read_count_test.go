package memory

import (
	"context"
	"strings"
	"testing"
)

// countingStore counts how many times the user's namespace is read, so a test
// can assert retrieval does it once per turn.
//
// This matters beyond the memory backend. The PostgreSQL List is
// `SELECT data FROM agent_memories WHERE user_id=$1`, which materialises every
// row — including each document chunk's text — so a second read per turn
// re-reads and re-decodes the user's whole corpus for nothing.
type countingStore struct {
	MemoryStore
	lists int
}

func (c *countingStore) List(ctx context.Context, userID string) ([]MemoryEntry, error) {
	c.lists++
	return c.MemoryStore.List(ctx, userID)
}

// TestRetrieveRelevant_ReadsTheNamespaceOnce pins the read count, and with it
// the consistency property: one read means the user-wide and thread-scoped parts
// of the injected context cannot disagree about the state of the store.
func TestRetrieveRelevant_ReadsTheNamespaceOnce(t *testing.T) {
	store := &countingStore{MemoryStore: NewInMemoryMemoryStore()}
	svc := NewService(store, nil, nil, nil)
	ctx := context.Background()

	if err := svc.PutPreference(ctx, "u1", "preferred_language", "Go"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpsertPreference(ctx, "u1", "answer_style", "concise", EntryMeta{
		Type: MemoryTypePreference, Importance: 3, Source: "user_stated",
		ThreadID: "t_a", Scope: ScopeThread,
	}); err != nil {
		t.Fatal(err)
	}

	// Warm up: ensureVectorIndex reads once per user per process, which is a
	// different concern from the per-turn cost being measured here.
	svc.RetrieveRelevant(ctx, "u1", "t_a", "帮我写脚本", 400, false)

	store.lists = 0
	out := svc.RetrieveRelevant(ctx, "u1", "t_a", "帮我写脚本", 400, false)
	if store.lists != 1 {
		t.Fatalf("retrieval read the user's namespace %d times, want 1", store.lists)
	}

	// And the single read must still serve both scopes: a cheap implementation
	// that simply dropped the thread section would pass the count assertion.
	if !strings.Contains(out, "本次会话的偏好") || !strings.Contains(out, "answer_style") {
		t.Fatalf("the thread-scoped entry was not injected:\n%s", out)
	}
	if !strings.Contains(out, "preferred_language") {
		t.Fatalf("the user-wide entry was not injected:\n%s", out)
	}
}

// TestScopedEntriesIn covers the pure filter directly, including that it strips
// the encoding and ignores other threads' entries.
func TestScopedEntriesIn(t *testing.T) {
	entries := []MemoryEntry{
		{Key: "preferred_language", Value: "Go"},
		{Key: threadScopedKey("t_a", "answer_style"), Value: "concise"},
		{Key: threadScopedKey("t_b", "answer_style"), Value: "detailed"},
		{Key: threadScopedKey("t_ab", "answer_style"), Value: "detailed"},
	}

	got := scopedEntriesIn(entries, "t_a")
	// Exactly one: "t_a" must not sweep in "t_ab", whose id starts with its own.
	// That is what the terminating "__" in the storage key is for.
	if len(got) != 1 {
		t.Fatalf("expected only t_a's entry, got %v", got)
	}
	entry, ok := got["answer_style"]
	if !ok {
		t.Fatalf("the logical key was not stripped: %v", got)
	}
	if entry.Value != "concise" {
		t.Fatalf("wrong entry: %+v", entry)
	}
	if n := len(scopedEntriesIn(entries, "t_ab")); n != 1 {
		t.Fatalf("t_ab should match exactly its own entry, got %d", n)
	}
	if n := len(scopedEntriesIn(entries, "t_missing")); n != 0 {
		t.Fatalf("an unknown thread must match nothing, got %d", n)
	}
}
