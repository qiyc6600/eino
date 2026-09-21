package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestFileThreadStore_PersistenceRoundTrip verifies conversation histories
// (including tool-call messages) survive a store restart with the file backend.
func TestFileThreadStore_PersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.json")

	store1, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatalf("create file thread store: %v", err)
	}

	// Admin's thread: user message + assistant tool_call + tool result
	store1.Append("u_admin", "t1",
		schema.UserMessage("删除订单A-1001"),
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "tc_1", Function: schema.FunctionCall{Name: "delete_order", Arguments: `{"order_id":"A-1001"}`}},
		}),
		&schema.Message{Role: schema.Tool, Content: "已删除", ToolCallID: "tc_1", Name: "delete_order"},
		schema.AssistantMessage("订单已删除", nil),
	)
	// Visitor's thread must stay isolated
	store1.Append("u_visitor", "t1", schema.UserMessage("visitor's secret chat"))

	store2, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatalf("reopen file thread store: %v", err)
	}

	msgs := store2.Copy("u_admin", "t1")
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages after restart, got %d", len(msgs))
	}
	if msgs[0].Role != schema.User || msgs[0].Content != "删除订单A-1001" {
		t.Errorf("user message mismatch: %+v", msgs[0])
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].Function.Name != "delete_order" {
		t.Errorf("assistant tool_call mismatch: %+v", msgs[1])
	}
	if msgs[2].Role != schema.Tool || msgs[2].ToolCallID != "tc_1" {
		t.Errorf("tool result mismatch: %+v", msgs[2])
	}

	// Isolation survives persistence too.
	visitorMsgs := store2.Copy("u_visitor", "t1")
	if len(visitorMsgs) != 1 || visitorMsgs[0].Content != "visitor's secret chat" {
		t.Errorf("visitor thread mismatch: %+v", visitorMsgs)
	}

	// Delete persists as well.
	if deleted, err := store2.Delete("u_visitor", "t1"); err != nil || !deleted {
		t.Fatal("delete should succeed for owner")
	}
	store3, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	if store3.Copy("u_visitor", "t1") != nil {
		t.Error("deleted thread should stay deleted after restart")
	}
	if len(store3.Copy("u_admin", "t1")) != 4 {
		t.Error("admin's thread must be untouched")
	}

	// Listing is scoped per user after restart.
	if len(store3.List("u_admin")) != 1 || len(store3.List("u_visitor")) != 0 {
		t.Error("thread listing mismatch after restart")
	}
}

func TestFileThreadStore_FailedMutationsLeaveOriginalState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "threads.json")
	store, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append("u", "t", schema.UserMessage("original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace("u", "t", []*schema.Message{schema.UserMessage("replacement")}); err == nil {
		t.Fatal("replace swallowed write error")
	}
	if err := store.Append("u", "t", schema.UserMessage("extra")); err == nil {
		t.Fatal("append swallowed write error")
	}
	if err := store.Create("u", "new"); err == nil {
		t.Fatal("create swallowed write error")
	}
	if deleted, err := store.Delete("u", "t"); err == nil || deleted {
		t.Fatal("delete swallowed write error")
	}
	for _, current := range []*FileThreadStore{store, mustReopenThreads(t, path)} {
		msgs := current.Copy("u", "t")
		if len(msgs) != 1 || msgs[0].Content != "original" || len(current.List("u")) != 1 {
			t.Fatal("failed write changed stored state")
		}
	}
}
func mustReopenThreads(t *testing.T, path string) *FileThreadStore {
	t.Helper()
	s, err := NewFileThreadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
