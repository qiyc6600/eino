package agent

import (
	"context"
	"testing"
	"time"

	"github.com/example/agent-eino-demo/internal/memory"
)

// TestExecution_DeleteThreadRemovesScopedMemory covers the wiring between thread
// deletion and conversation-scoped memory.
//
// Nothing else ever removes those entries, so a missing or mis-addressed call
// would leave them unreachable but still counted as the user's data — and the
// symptom would only appear as a slow leak, not as a failure.
func TestExecution_DeleteThreadRemovesScopedMemory(t *testing.T) {
	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	ctx := context.Background()
	user := testIdentity()

	if err := r.memorySvc.UpsertPreference(ctx, user.UserID, "answer_style", "concise", memory.EntryMeta{
		Type: memory.MemoryTypePreference, Importance: 3, Source: "user_stated",
		ThreadID: "doomed", Scope: memory.ScopeThread,
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.memorySvc.PutPreference(ctx, user.UserID, "preferred_language", "Go"); err != nil {
		t.Fatal(err)
	}

	// Give the thread some history, so deletion has real work to do.
	if result := r.Chat(user, "doomed", "你好"); result.Status != StatusCompleted {
		t.Fatalf("run failed: %+v", result)
	}

	if _, err := r.DeleteThread(user.UserID, "doomed"); err != nil {
		t.Fatal(err)
	}

	scoped, err := r.memorySvc.ListThreadPreferences(ctx, user.UserID, "doomed")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 0 {
		t.Fatalf("scoped memory outlived its thread: %v", scoped)
	}
	if _, ok, _ := r.memorySvc.GetPreference(ctx, user.UserID, "preferred_language"); !ok {
		t.Fatal("deleting a thread must not touch user-wide memory")
	}
}

// TestExecution_RetentionPrunesScopedMemory covers the other removal path end to
// end: the sweep runs on the write path, and it must take conversation-scoped
// memory with it while leaving the user's own entries alone.
//
// The window is a nanosecond rather than a seeded old timestamp, so every entry
// qualifies as stale without reaching across packages to rewrite one. The code
// path under test — runner → pruneThreads → PruneThreadPreferences — is the same.
func TestExecution_RetentionPrunesScopedMemory(t *testing.T) {
	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	ctx := context.Background()
	user := testIdentity()

	if err := r.memorySvc.UpsertPreference(ctx, user.UserID, "answer_style", "concise", memory.EntryMeta{
		Type: memory.MemoryTypePreference, Importance: 3, Source: "user_stated",
		ThreadID: "old_thread", Scope: memory.ScopeThread,
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.memorySvc.PutPreference(ctx, user.UserID, "preferred_language", "Go"); err != nil {
		t.Fatal(err)
	}

	// Off by default: a normal run must leave the scoped entry alone.
	if result := r.Chat(user, "fresh", "你好"); result.Status != StatusCompleted {
		t.Fatalf("run failed: %+v", result)
	}
	if scoped, _ := r.memorySvc.ListThreadPreferences(ctx, user.UserID, "old_thread"); len(scoped) != 1 {
		t.Fatalf("with retention off nothing should be pruned, got %v", scoped)
	}

	r.SetThreadRetention(time.Nanosecond)
	if result := r.Chat(user, "fresh", "你好"); result.Status != StatusCompleted {
		t.Fatalf("run failed: %+v", result)
	}

	scoped, err := r.memorySvc.ListThreadPreferences(ctx, user.UserID, "old_thread")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 0 {
		t.Fatalf("stale scoped memory survived the retention sweep: %v", scoped)
	}
	if _, ok, _ := r.memorySvc.GetPreference(ctx, user.UserID, "preferred_language"); !ok {
		t.Fatal("the retention sweep must not touch user-wide memory")
	}
}
