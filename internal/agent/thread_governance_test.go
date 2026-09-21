package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// TestTrimHistoryDropsOrphanedToolResults covers the hazard of capping from the
// front: the cut can land between a tool_call and its result, and a result
// without its request confuses the model (some providers reject it outright).
func TestTrimHistoryDropsOrphanedToolResults(t *testing.T) {
	messages := []*schema.Message{
		schema.UserMessage("u1"),
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "c1", Function: schema.FunctionCall{Name: "query_order", Arguments: "{}"}},
		}),
		&schema.Message{Role: schema.Tool, Content: "订单 A-1001", ToolCallID: "c1", Name: "query_order"},
		schema.AssistantMessage("a1", nil),
		schema.UserMessage("u2"),
		schema.AssistantMessage("a2", nil),
	}

	// Keeping the last 4 would start at the tool result, orphaning it.
	kept := trimHistory(messages, 4)

	for _, m := range kept {
		if m.Role != schema.Tool {
			continue
		}
		found := false
		for _, candidate := range kept {
			for _, tc := range candidate.ToolCalls {
				if tc.ID == m.ToolCallID {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("trim left a tool result without its call: %+v", kept)
		}
	}
	if len(kept) > 4 {
		t.Fatalf("trim kept %d messages, want at most 4", len(kept))
	}

	// A cap at or above the length changes nothing.
	if got := trimHistory(messages, len(messages)); len(got) != len(messages) {
		t.Fatalf("trim with a cap equal to the length altered the history: %d", len(got))
	}
}

// TestExecution_HistoryCapIsAppliedOnWrite asserts the configured cap bounds the
// stored history, and that hitting the cap falls back to a rewrite.
func TestExecution_HistoryCapIsAppliedOnWrite(t *testing.T) {
	store := &countingThreadStore{threadStore: newThreadStore()}
	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	r.threads = store
	r.SetThreadHistoryLimit(4)

	for i := 0; i < 4; i++ {
		if result := r.Chat(testIdentity(), "thread", "继续聊"); result.Status != StatusCompleted {
			t.Fatalf("turn %d failed: %+v", i+1, result)
		}
	}

	if got := len(r.GetThreadMessages("u_admin", "thread")); got > 4 {
		t.Fatalf("history cap not applied: %d messages stored", got)
	}
	// Reaching the cap means every later turn must rewrite rather than append.
	if store.replaces == 0 {
		t.Fatal("expected the capped path to rewrite the history")
	}
}

// TestExecution_NoCapKeepsAppending is the default-behavior guard: with the
// governance knobs untouched, nothing is trimmed and the append path still runs.
func TestExecution_NoCapKeepsAppending(t *testing.T) {
	store := &countingThreadStore{threadStore: newThreadStore()}
	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	r.threads = store

	for i := 0; i < 3; i++ {
		if result := r.Chat(testIdentity(), "thread", "默认不应裁剪"); result.Status != StatusCompleted {
			t.Fatalf("turn %d failed: %+v", i+1, result)
		}
	}
	if store.replaces != 0 {
		t.Fatalf("default configuration must keep appending, got %d replaces", store.replaces)
	}
	if got := len(r.GetThreadMessages("u_admin", "thread")); got != 6 {
		t.Fatalf("expected 3 turns x 2 messages to be preserved, got %d", got)
	}
}

// TestThreadStore_PruneThreadsBefore covers the sweep itself, including the
// per-user scope that keeps the store's isolation invariant intact.
func TestThreadStore_PruneThreadsBefore(t *testing.T) {
	ctx := context.Background()
	stale := time.Now().Add(-48 * time.Hour)
	fresh := time.Now()

	ts := newThreadStore()
	ts.Restore([]ThreadSnapshot{
		{UserID: "u_a", ThreadID: "stale", Messages: []*schema.Message{schema.UserMessage("old")}, UpdatedAt: stale},
		{UserID: "u_a", ThreadID: "recent", Messages: []*schema.Message{schema.UserMessage("new")}, UpdatedAt: fresh},
		{UserID: "u_a", ThreadID: "undated", Messages: []*schema.Message{schema.UserMessage("?")}},
		{UserID: "u_b", ThreadID: "stale", Messages: []*schema.Message{schema.UserMessage("other")}, UpdatedAt: stale},
	})

	removed, err := ts.PruneThreadsBefore(ctx, "u_a", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d threads, want 1", removed)
	}
	remaining := ts.List("u_a")
	if len(remaining) != 2 {
		t.Fatalf("expected the recent and undated threads to survive, got %v", remaining)
	}
	// The sweep must not reach into another user's namespace.
	if got := ts.List("u_b"); len(got) != 1 {
		t.Fatalf("the sweep touched another user's threads: %v", got)
	}
}

// TestFileThreadStore_PruneThreadsPersists guards the embedded-store trap again:
// a promoted prune would delete from memory only, and the threads would return
// on the next restart.
func TestFileThreadStore_PruneThreadsPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "threads.json")

	store, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.threadStore.Restore([]ThreadSnapshot{
		{UserID: "u_admin", ThreadID: "stale", Messages: []*schema.Message{schema.UserMessage("old")}, UpdatedAt: time.Now().Add(-48 * time.Hour)},
		{UserID: "u_admin", ThreadID: "recent", Messages: []*schema.Message{schema.UserMessage("new")}, UpdatedAt: time.Now()},
	})
	// Persist the seeded state through the file store's own write path.
	if err := store.Replace("u_admin", "recent", []*schema.Message{schema.UserMessage("new")}); err != nil {
		t.Fatal(err)
	}

	removed, err := store.PruneThreadsBefore(ctx, "u_admin", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d threads, want 1", removed)
	}

	reloaded, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if ids := reloaded.List("u_admin"); len(ids) != 1 || ids[0] != "recent" {
		t.Fatalf("prune did not reach disk, reloaded threads: %v", ids)
	}
}

// TestExecution_PruneThreadsOnWrite asserts retention runs on the write path and
// stays off by default.
func TestExecution_PruneThreadsOnWrite(t *testing.T) {
	newStore := func() *countingThreadStore {
		store := &countingThreadStore{threadStore: newThreadStore()}
		store.threadStore.Restore([]ThreadSnapshot{
			{UserID: "u_admin", ThreadID: "stale", Messages: []*schema.Message{schema.UserMessage("old")}, UpdatedAt: time.Now().Add(-48 * time.Hour)},
		})
		return store
	}

	t.Run("disabled by default", func(t *testing.T) {
		store := newStore()
		r := testRuntime(t, NewMockChatModel(), nil, nil, "")
		r.threads = store

		if result := r.Chat(testIdentity(), "thread", "你好"); result.Status != StatusCompleted {
			t.Fatalf("run failed: %+v", result)
		}
		ids := r.ListThreads("u_admin")
		if len(ids) != 2 {
			t.Fatalf("default configuration must not prune, threads: %v", ids)
		}
	})

	t.Run("enabled prunes stale threads", func(t *testing.T) {
		store := newStore()
		r := testRuntime(t, NewMockChatModel(), nil, nil, "")
		r.threads = store
		r.SetThreadRetention(24 * time.Hour)

		if result := r.Chat(testIdentity(), "thread", "你好"); result.Status != StatusCompleted {
			t.Fatalf("run failed: %+v", result)
		}
		ids := r.ListThreads("u_admin")
		if len(ids) != 1 || ids[0] != "thread" {
			t.Fatalf("expected only the active thread to remain, got %v", ids)
		}
	})
}
