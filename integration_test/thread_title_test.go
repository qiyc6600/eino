package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTitleServer starts the app and returns its server, matching how the rest of
// this suite builds one.
func newTitleServer(t *testing.T) *httptest.Server {
	t.Helper()
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	t.Cleanup(server.Close)
	return server
}

// putJSON is postJSON with PUT: renaming is a PUT, and sending POST here returned
// 404 from the router instead — which the assertions read as "the endpoint is
// missing" rather than "the test used the wrong method".
func putJSON(t *testing.T, serverURL, path, sessionID string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, serverURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// threadList fetches the conversation list as {id, title} objects.
func threadList(t *testing.T, serverURL, sessionID string) []map[string]any {
	t.Helper()
	return decodeArray(t, doGet(t, serverURL, "/api/chat/threads", sessionID))
}

func titleOf(rows []map[string]any, id string) string {
	for _, row := range rows {
		if row["id"] == id {
			title, _ := row["title"].(string)
			return title
		}
	}
	return ""
}

// A conversation names itself from its first message, so the sidebar shows
// something recognisable instead of "t_mf3k2j1a".
func TestIntegration_ThreadIsTitledFromItsFirstMessage(t *testing.T) {
	server := newTitleServer(t)
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	resp := postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_titled", "message": "查询北京天气", "stream": false,
	})
	resp.Body.Close()

	rows := threadList(t, server.URL, sessionID)
	if got := titleOf(rows, "t_titled"); got != "查询北京天气" {
		t.Errorf("title = %q, want the first message; list was %v", got, rows)
	}
}

// The title comes from the *first* message of the thread, not from the message
// that happens to arrive when the title is first written. That distinction is
// invisible on a fresh thread — the title is written during the first chat, when
// the current message is the first message — so this test recreates the case
// where it matters: a thread that already has history but no title, which is what
// every conversation created before this feature existed looks like.
func TestIntegration_ThreadTitleComesFromTheFirstMessage(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	chat := func(message string) {
		t.Helper()
		postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
			"threadId": "t_first_wins", "message": message, "stream": false,
		}).Body.Close()
	}

	chat("查询北京天气")
	chat("那上海呢")

	// Drop the title, leaving messages without one: the pre-upgrade shape.
	userID := "u_admin"
	if err := application.MemorySvc.DeleteThreadTitle(context.Background(), userID, "t_first_wins"); err != nil {
		t.Fatalf("could not clear the title: %v", err)
	}
	if got := titleOf(threadList(t, server.URL, sessionID), "t_first_wins"); got != "" {
		t.Fatalf("the title was not cleared, got %q", got)
	}

	// A third turn. The title must come from the first message, not this one.
	chat("第三个问题")

	if got := titleOf(threadList(t, server.URL, sessionID), "t_first_wins"); got != "查询北京天气" {
		t.Errorf("title = %q, want the first message of the thread", got)
	}
}

// Renaming is the user's: a later message must not overwrite the name they chose.
func TestIntegration_ThreadTitleSurvivesLaterMessages(t *testing.T) {
	server := newTitleServer(t)
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// The thread has to exist before it can be renamed — a rename for an unknown
	// thread is a 404, which is why the status is checked here rather than
	// assumed: an ignored 404 made this test pass for the wrong reason once.
	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_renamed", "message": "查询北京天气", "stream": false,
	}).Body.Close()
	if got := titleOf(threadList(t, server.URL, sessionID), "t_renamed"); got != "查询北京天气" {
		t.Fatalf("the thread was not auto-titled first, got %q", got)
	}

	renameResp := putJSON(t, server.URL, "/api/chat/t_renamed", sessionID, map[string]any{
		"title": "我的天气查询",
	})
	renameResp.Body.Close()
	if renameResp.StatusCode != http.StatusOK {
		t.Fatalf("rename returned %d", renameResp.StatusCode)
	}

	// Another turn on the same thread: the auto-title must not fire again.
	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_renamed", "message": "那上海呢", "stream": false,
	}).Body.Close()

	rows := threadList(t, server.URL, sessionID)
	if got := titleOf(rows, "t_renamed"); got != "我的天气查询" {
		t.Errorf("title = %q; the auto-title overwrote a name the user chose", got)
	}
}

// A rename is metadata: it must not touch the thread ID, which is the key for
// the stored messages, the checkpoints and the thread-scoped memories.
func TestIntegration_RenameKeepsMessages(t *testing.T) {
	server := newTitleServer(t)
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_keep_msgs", "message": "1+4", "stream": false,
	}).Body.Close()
	before := decodeArray(t, doGet(t, server.URL, "/api/chat/t_keep_msgs/messages", sessionID))
	if len(before) == 0 {
		t.Fatal("no messages stored before the rename")
	}

	putJSON(t, server.URL, "/api/chat/t_keep_msgs", sessionID, map[string]any{
		"title": "算术",
	}).Body.Close()

	after := decodeArray(t, doGet(t, server.URL, "/api/chat/t_keep_msgs/messages", sessionID))
	if len(after) != len(before) {
		t.Errorf("message count changed across a rename: %d -> %d", len(before), len(after))
	}
	if got := titleOf(threadList(t, server.URL, sessionID), "t_keep_msgs"); got != "算术" {
		t.Errorf("title = %q, want 算术", got)
	}
}

// Titles live in the memory store's reserved-key space, so the one thing that
// must not happen is a title showing up as a memory. This is the guard on that
// reuse.
func TestIntegration_ThreadTitleIsNotAMemory(t *testing.T) {
	server := newTitleServer(t)
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_not_memory", "message": "查询北京天气", "stream": false,
	}).Body.Close()

	for _, entry := range decodeArray(t, doGet(t, server.URL, "/api/memory", sessionID)) {
		key, _ := entry["key"].(string)
		value, _ := entry["value"].(string)
		if value == "查询北京天气" {
			t.Errorf("the thread title appeared as a memory entry: %v", entry)
		}
		if key != "" && key[0] == '_' {
			t.Errorf("a reserved key was listed as a user memory: %q", key)
		}
	}
}

// A title is per user like everything else. The case that matters is not two
// different thread IDs — those are invisible to each other anyway — but the ID
// every user starts on: t_default. Both accounts have their own t_default, and
// one account's name for it must not appear on the other's.
func TestIntegration_ThreadTitlesArePerUser(t *testing.T) {
	server := newTitleServer(t)
	adminSession := doLogin(t, server.URL, "admin", testAdminPassword)
	visitorSession := doLogin(t, server.URL, "visitor", "visitor123")

	// Both chat on t_default, so both have a thread with that ID.
	for _, session := range []string{adminSession, visitorSession} {
		postJSON(t, server.URL, "/api/agent/chat", session, map[string]any{
			"threadId": "t_default", "message": "你好", "stream": false,
		}).Body.Close()
	}

	// The admin names theirs.
	renameResp := putJSON(t, server.URL, "/api/chat/t_default", adminSession, map[string]any{
		"title": "管理员的会话",
	})
	renameResp.Body.Close()
	if renameResp.StatusCode != http.StatusOK {
		t.Fatalf("rename returned %d", renameResp.StatusCode)
	}
	if got := titleOf(threadList(t, server.URL, adminSession), "t_default"); got != "管理员的会话" {
		t.Fatalf("the admin's own rename did not take: %q", got)
	}

	// The visitor's t_default is a different thread and must keep its own name.
	if got := titleOf(threadList(t, server.URL, visitorSession), "t_default"); got == "管理员的会话" {
		t.Errorf("the visitor sees the admin's name for t_default: %q", got)
	}
}

// Deleting a conversation takes its title with it: leaving the name behind would
// accumulate an entry per deleted thread, and reusing the ID would resurrect it.
func TestIntegration_DeletingAThreadRemovesItsTitle(t *testing.T) {
	server := newTitleServer(t)
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_doomed", "message": "临时会话", "stream": false,
	}).Body.Close()
	// A second thread so the delete is not refused for being the last one.
	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_keep", "message": "保留", "stream": false,
	}).Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/chat/t_doomed/delete", nil)
	req.Header.Set("Authorization", "Bearer "+sessionID)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	delResp.Body.Close()

	// Recreate the same ID: the old name must not come back with it.
	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_doomed", "message": "新会话", "stream": false,
	}).Body.Close()

	if got := titleOf(threadList(t, server.URL, sessionID), "t_doomed"); got == "临时会话" {
		t.Errorf("the deleted thread's title came back: %q", got)
	}
}

// The rename endpoint refuses what it cannot honour, rather than storing it.
func TestIntegration_RenameValidation(t *testing.T) {
	server := newTitleServer(t)
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_validate", "message": "验证", "stream": false,
	}).Body.Close()

	cases := []struct {
		name string
		path string
		body map[string]any
		want int
	}{
		{"empty title", "/api/chat/t_validate", map[string]any{"title": "   "}, http.StatusBadRequest},
		{"unknown thread", "/api/chat/t_missing", map[string]any{"title": "名字"}, http.StatusNotFound},
		{"too long", "/api/chat/t_validate", map[string]any{"title": longTitle()}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := putJSON(t, server.URL, tc.path, sessionID, tc.body)
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}

	// And the accepted case still works, so the rejections above are not the
	// endpoint failing wholesale.
	resp := putJSON(t, server.URL, "/api/chat/t_validate", sessionID, map[string]any{"title": "有效名称"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a valid rename was rejected: %d", resp.StatusCode)
	}
	if got := titleOf(threadList(t, server.URL, sessionID), "t_validate"); got != "有效名称" {
		t.Errorf("title = %q, want 有效名称", got)
	}
}

func longTitle() string {
	runes := make([]rune, 200)
	for i := range runes {
		runes[i] = '长'
	}
	return string(runes)
}

// The page must read the fields the server sends. Titles are the newest part of
// this contract, and a rename that silently did nothing would look like a UI bug.
func TestIntegration_FrontendReadsThreadTitles(t *testing.T) {
	server := newTitleServer(t)
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)
	appJS := readAppJS(t)

	postJSON(t, server.URL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": "t_contract", "message": "契约检查", "stream": false,
	}).Body.Close()

	rows := threadList(t, server.URL, sessionID)
	if len(rows) == 0 {
		t.Fatal("no threads listed")
	}
	assertKeys(t, appJS, "GET /api/chat/threads[]", rows[0], "id")
	// title is omitempty, so it is asserted on the entry that has one.
	var titled map[string]any
	for _, row := range rows {
		if row["id"] == "t_contract" {
			titled = row
		}
	}
	if titled == nil {
		t.Fatal("the thread created above is not listed")
	}
	assertKeys(t, appJS, "GET /api/chat/threads[]", titled, "title")

	// The rename response echoes what was stored; the page reads both fields.
	renameResp := putJSON(t, server.URL, "/api/chat/t_contract", sessionID, map[string]any{"title": "契约名称"})
	defer renameResp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(renameResp.Body).Decode(&payload); err != nil {
		t.Fatalf("rename response is not JSON: %v", err)
	}
	assertKeys(t, appJS, "PUT /api/chat/{id}", payload, "title")
}
