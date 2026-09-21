package agent

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestThreadStore_PerUserNamespacing(t *testing.T) {
	ts := newThreadStore()

	// Same client-side threadID under different users must be different threads.
	ts.Append("u_admin", "t_default", schema.UserMessage("hello from admin"))
	ts.Append("u_visitor", "t_default", schema.UserMessage("hello from visitor"))

	adminMsgs := ts.Copy("u_admin", "t_default")
	visitorMsgs := ts.Copy("u_visitor", "t_default")

	if len(adminMsgs) != 1 || adminMsgs[0].Content != "hello from admin" {
		t.Errorf("admin thread should contain only admin's message, got %v", adminMsgs)
	}
	if len(visitorMsgs) != 1 || visitorMsgs[0].Content != "hello from visitor" {
		t.Errorf("visitor thread should contain only visitor's message, got %v", visitorMsgs)
	}
}

func TestThreadStore_CopyIsSnapshot(t *testing.T) {
	ts := newThreadStore()
	ts.Append("u_admin", "t1", schema.UserMessage("a"))

	msgs := ts.Copy("u_admin", "t1")
	msgs = append(msgs, schema.UserMessage("b"))

	if len(ts.Copy("u_admin", "t1")) != 1 {
		t.Error("mutating the copy must not affect the stored thread")
	}
}

func TestThreadStore_DeleteScopedToOwner(t *testing.T) {
	ts := newThreadStore()
	ts.Append("u_admin", "t1", schema.UserMessage("admin's data"))

	// A different user has no thread "t1" — delete must not touch admin's.
	if deleted, err := ts.Delete("u_visitor", "t1"); err != nil || deleted {
		t.Error("visitor should not be able to delete admin's thread")
	}
	if len(ts.Copy("u_admin", "t1")) != 1 {
		t.Error("admin's thread must survive another user's delete attempt")
	}

	// The owner can delete it.
	if deleted, err := ts.Delete("u_admin", "t1"); err != nil || !deleted {
		t.Error("owner should be able to delete own thread")
	}
	if ts.Copy("u_admin", "t1") != nil {
		t.Error("thread should be gone after owner deletes it")
	}
}

func TestThreadStore_ListScopedToUser(t *testing.T) {
	ts := newThreadStore()
	ts.Create("u_admin", "t_admin1")
	ts.Create("u_admin", "t_admin2")
	ts.Create("u_visitor", "t_default")

	adminThreads := ts.List("u_admin")
	if len(adminThreads) != 2 {
		t.Errorf("admin should see exactly 2 threads, got %v", adminThreads)
	}
	visitorThreads := ts.List("u_visitor")
	if len(visitorThreads) != 1 || visitorThreads[0] != "t_default" {
		t.Errorf("visitor should see only own thread, got %v", visitorThreads)
	}
}

func TestThreadStore_ReplaceGetOrCreate(t *testing.T) {
	ts := newThreadStore()
	ts.Append("u_admin", "t1", schema.UserMessage("first"))
	ts.Replace("u_admin", "t1", []*schema.Message{schema.UserMessage("second")})

	msgs := ts.Copy("u_admin", "t1")
	if len(msgs) != 1 || msgs[0].Content != "second" {
		t.Errorf("Replace should overwrite thread contents, got %v", msgs)
	}
}
