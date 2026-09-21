package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// countingThreadStore records which write path the runner chose, so a test can
// assert that the append optimization is actually taken rather than silently
// falling back to a full replace.
type countingThreadStore struct {
	*threadStore
	appends  int
	replaces int
}

func (s *countingThreadStore) AppendHistoryContext(ctx context.Context, userID, threadID string, expectedLen int, msgs []*schema.Message) error {
	s.appends++
	return s.threadStore.AppendHistoryContext(ctx, userID, threadID, expectedLen, msgs)
}

func (s *countingThreadStore) Replace(userID, threadID string, msgs []*schema.Message) error {
	s.replaces++
	return s.threadStore.Replace(userID, threadID, msgs)
}

// rejectingAppendStore always reports a prefix mismatch, like a store whose
// history changed underneath the run.
type rejectingAppendStore struct {
	*threadStore
	appends  int
	replaces int
}

func (s *rejectingAppendStore) AppendHistoryContext(context.Context, string, string, int, []*schema.Message) error {
	s.appends++
	return ErrThreadAppendMismatch
}

func (s *rejectingAppendStore) Replace(userID, threadID string, msgs []*schema.Message) error {
	s.replaces++
	return s.threadStore.Replace(userID, threadID, msgs)
}

func historyText(msgs []*schema.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func hasToolResultFor(msgs []*schema.Message, callID string) bool {
	for _, m := range msgs {
		if m.Role == schema.Tool && m.ToolCallID == callID {
			return true
		}
	}
	return false
}

// TestThreadStore_AppendHistoryRejectsStalePrefix covers the guard itself: an
// append whose expected length does not match the stored history must be
// refused rather than blindly appended to.
func TestThreadStore_AppendHistoryRejectsStalePrefix(t *testing.T) {
	ctx := context.Background()
	ts := newThreadStore()

	// A thread that already holds messages must refuse an append claiming it is
	// empty — this is what protects checkpoints written before the field existed.
	if err := ts.Append("u_admin", "t1", schema.UserMessage("existing")); err != nil {
		t.Fatal(err)
	}
	if err := ts.AppendHistoryContext(ctx, "u_admin", "t1", 0, []*schema.Message{schema.UserMessage("new")}); !errors.Is(err, ErrThreadAppendMismatch) {
		t.Fatalf("expected ErrThreadAppendMismatch, got %v", err)
	}
	if got := len(ts.Copy("u_admin", "t1")); got != 1 {
		t.Fatalf("a rejected append must not modify the thread, len=%d", got)
	}

	// The matching length appends.
	if err := ts.AppendHistoryContext(ctx, "u_admin", "t1", 1, []*schema.Message{schema.UserMessage("new")}); err != nil {
		t.Fatalf("expected append to succeed: %v", err)
	}
	if got := len(ts.Copy("u_admin", "t1")); got != 2 {
		t.Fatalf("expected 2 messages after append, got %d", got)
	}

	// A missing thread counts as length zero, which is how a first turn appends.
	if err := ts.AppendHistoryContext(ctx, "u_admin", "fresh", 0, []*schema.Message{schema.UserMessage("first")}); err != nil {
		t.Fatalf("expected first append to succeed: %v", err)
	}
	if got := len(ts.Copy("u_admin", "fresh")); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
}

// TestExecution_ThreadHistoryAppendsAcrossTurns asserts the normal path uses the
// append, and that repeated turns neither duplicate nor lose history.
func TestExecution_ThreadHistoryAppendsAcrossTurns(t *testing.T) {
	store := &countingThreadStore{threadStore: newThreadStore()}
	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	r.threads = store

	turns := []string{"第一轮：你好", "第二轮：再聊一句", "第三轮：继续"}
	for i, turn := range turns {
		result := r.Chat(testIdentity(), "thread", turn)
		if result.Status != StatusCompleted {
			t.Fatalf("turn %d failed: %+v", i+1, result)
		}
	}

	if store.replaces != 0 {
		t.Fatalf("expected every turn to append, but %d replace(s) happened", store.replaces)
	}
	if store.appends != len(turns) {
		t.Fatalf("expected %d appends, got %d", len(turns), store.appends)
	}

	history := historyText(r.GetThreadMessages("u_admin", "thread"))
	for _, turn := range turns {
		if n := strings.Count(history, turn); n != 1 {
			t.Fatalf("turn %q appears %d times, want exactly once:\n%s", turn, n, history)
		}
	}
}

// TestExecution_RepairedHistoryFallsBackToReplace asserts that repairing an
// orphaned tool call changes messages the store already holds, so the run must
// be persisted by replacing — and that the repair actually reaches the store.
func TestExecution_RepairedHistoryFallsBackToReplace(t *testing.T) {
	store := &countingThreadStore{threadStore: newThreadStore()}
	// Seed an orphaned tool call directly, bypassing the counter: an assistant
	// message that asks for a tool whose result never arrived.
	orphan := schema.AssistantMessage("", []schema.ToolCall{
		{ID: "call_orphan", Function: schema.FunctionCall{Name: "calculator", Arguments: `{"expression":"1+1"}`}},
	})
	if err := store.threadStore.Replace("u_admin", "thread", []*schema.Message{orphan}); err != nil {
		t.Fatal(err)
	}

	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	r.threads = store

	result := r.Chat(testIdentity(), "thread", "你好")
	if result.Status != StatusCompleted {
		t.Fatalf("run failed: %+v", result)
	}
	if store.appends != 0 {
		t.Fatalf("a repaired history must not be appended to (appends=%d)", store.appends)
	}
	if store.replaces == 0 {
		t.Fatal("a repaired history must be persisted by replacing")
	}

	msgs := r.GetThreadMessages("u_admin", "thread")
	if !hasToolResultFor(msgs, "call_orphan") {
		t.Fatalf("the orphan repair was not persisted: %+v", msgs)
	}
}

// TestExecution_AppendMismatchFallsBackToReplace asserts the optimization is
// self-correcting: when the store rejects the prefix, the run still persists
// correctly through a full replace.
func TestExecution_AppendMismatchFallsBackToReplace(t *testing.T) {
	store := &rejectingAppendStore{threadStore: newThreadStore()}
	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	r.threads = store

	result := r.Chat(testIdentity(), "thread", "第一轮")
	if result.Status != StatusCompleted {
		t.Fatalf("run failed: %+v", result)
	}
	if store.appends == 0 {
		t.Fatal("expected the append path to be attempted first")
	}
	if store.replaces == 0 {
		t.Fatal("expected a replace after the append was rejected")
	}
	if got := len(r.GetThreadMessages("u_admin", "thread")); got == 0 {
		t.Fatal("history was not persisted after the fallback")
	}
}

// measuringThreadStore records how many bytes each write path was asked to store.
type measuringThreadStore struct {
	*threadStore
	appendBytes  int
	replaceBytes int
}

func (s *measuringThreadStore) AppendHistoryContext(ctx context.Context, userID, threadID string, expectedLen int, msgs []*schema.Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	s.appendBytes += len(data)
	return s.threadStore.AppendHistoryContext(ctx, userID, threadID, expectedLen, msgs)
}

func (s *measuringThreadStore) Replace(userID, threadID string, msgs []*schema.Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	s.replaceBytes += len(data)
	return s.threadStore.Replace(userID, threadID, msgs)
}

// TestExecution_AppendWritesOnlyNewMessages measures the write volume the
// optimization removes: with appending, each turn stores just its own messages,
// whereas a full replace rewrites the entire conversation every turn.
func TestExecution_AppendWritesOnlyNewMessages(t *testing.T) {
	store := &measuringThreadStore{threadStore: newThreadStore()}
	r := testRuntime(t, NewMockChatModel(), nil, nil, "")
	r.threads = store

	const turns = 6
	for i := 0; i < turns; i++ {
		if result := r.Chat(testIdentity(), "thread", "第几轮了？请简短回答"); result.Status != StatusCompleted {
			t.Fatalf("turn %d failed: %+v", i+1, result)
		}
	}

	if store.replaceBytes != 0 {
		t.Fatalf("expected no full replaces, got %d bytes", store.replaceBytes)
	}

	// What a replace-per-turn implementation would have written.
	history := r.GetThreadMessages("u_admin", "thread")
	replaceWouldWrite := 0
	for i := 1; i <= len(history); i++ {
		data, err := json.Marshal(history[:i])
		if err != nil {
			t.Fatal(err)
		}
		replaceWouldWrite += len(data)
	}

	t.Logf("append total: %d bytes; replace-every-turn total: %d bytes; history: %d messages",
		store.appendBytes, replaceWouldWrite, len(history))
	if store.appendBytes >= replaceWouldWrite {
		t.Fatalf("appending should write less than replacing every turn: %d >= %d",
			store.appendBytes, replaceWouldWrite)
	}
}

// file store embeds *threadStore, so without an explicit override it would
// promote an append that only touches memory and never reaches disk.
func TestFileThreadStore_AppendHistoryPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "threads.json")

	store, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatalf("create file thread store: %v", err)
	}
	if err := store.AppendHistoryContext(ctx, "u_admin", "t1", 0, []*schema.Message{schema.UserMessage("persisted")}); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	// Reload from disk: an in-memory-only append would lose the message here.
	reloaded, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	msgs := reloaded.Copy("u_admin", "t1")
	if len(msgs) != 1 || msgs[0].Content != "persisted" {
		t.Fatalf("append did not reach disk: %+v", msgs)
	}

	// A stale prefix is refused, and refusing must not disturb the stored data.
	if err := store.AppendHistoryContext(ctx, "u_admin", "t1", 7, []*schema.Message{schema.UserMessage("x")}); !errors.Is(err, ErrThreadAppendMismatch) {
		t.Fatalf("expected ErrThreadAppendMismatch, got %v", err)
	}
	if got := len(store.Copy("u_admin", "t1")); got != 1 {
		t.Fatalf("rejected append modified the thread, len=%d", got)
	}
}
