package integration_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegration_FrontendContract pins the JSON keys web/assets/app.js reads.
//
// The page has no semantic test coverage: `node --check` verifies syntax only,
// so a renamed JSON tag or a field the page expects but the server never sends
// breaks the UI while every Go test stays green. Both have happened in this
// project — a session field-name mismatch that emptied the memory panel, and a
// `memory` key read from a response type that has none.
//
// Two directions are asserted for each key, and the second is what keeps this
// list honest: the key must be present in the response, and the page must
// actually mention it. Without the second check the list rots into assertions
// about a contract nobody uses.
func TestIntegration_FrontendContract(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)
	appJS := readAppJS(t)

	// assertKeys checks a response object against the keys the page reads.
	assertKeys := func(what string, obj map[string]any, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s does not contain %q, but the page reads it", what, k)
			}
			if !strings.Contains(appJS, k) {
				t.Errorf("%s asserts %q, which web/assets/app.js never reads — drop it from this list", what, k)
			}
		}
	}

	decodeObject := func(t *testing.T, resp *http.Response) map[string]any {
		t.Helper()
		defer resp.Body.Close()
		var obj map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return obj
	}
	decodeArray := func(t *testing.T, resp *http.Response) []map[string]any {
		t.Helper()
		defer resp.Body.Close()
		var arr []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return arr
	}

	t.Run("auth me", func(t *testing.T) {
		me := decodeObject(t, doGet(t, server.URL, "/api/auth/me", sessionID))
		assertKeys("GET /api/auth/me", me, "user", "tools")
		user, _ := me["user"].(map[string]any)
		if user == nil {
			t.Fatalf("user is not an object: %v", me["user"])
		}
		assertKeys("GET /api/auth/me user", user, "username", "roles")
	})

	t.Run("tools", func(t *testing.T) {
		tools := decodeArray(t, doGet(t, server.URL, "/api/tools", sessionID))
		if len(tools) == 0 {
			t.Fatal("no tools registered")
		}
		assertKeys("GET /api/tools[]", tools[0], "name", "risk_level")
	})

	t.Run("memory and settings", func(t *testing.T) {
		// Write the same key twice so the entry carries a superseded revision,
		// which is the only way `history` appears at all.
		for _, value := range []string{"Go", "Python"} {
			resp := doPost(t, server.URL, "/api/memory", sessionID, map[string]string{
				"key": "preferred_language", "value": value,
			})
			resp.Body.Close()
		}

		entries := decodeArray(t, doGet(t, server.URL, "/api/memory", sessionID))
		if len(entries) == 0 {
			t.Fatal("the entry written above was not listed")
		}
		entry := entries[0]
		assertKeys("GET /api/memory[]", entry, "key", "value", "source", "updated_at")

		// history is omitempty, so it only exists once a value was superseded.
		history, ok := entry["history"].([]any)
		if !ok || len(history) == 0 {
			t.Fatalf("a superseded entry should carry history, got %v", entry["history"])
		}
		first, _ := history[0].(map[string]any)
		assertKeys("GET /api/memory[] history[]", first, "value")

		// The remaining v2 fields are omitempty, and the page reads each of them
		// with a truthiness guard — so their absence on a hand-written entry is
		// the normal case, not a contract break. Only the "the page reads it"
		// direction is asserted here; driving all of them non-zero would take a
		// whole memory lifecycle (a retrieval for access_count, a consolidation
		// pass for archived) to pin a shape the list already covers.
		for _, k := range []string{
			"type", "importance", "archived", "access_count",
			"source_thread_id", "source_excerpt",
		} {
			if !strings.Contains(appJS, k) {
				t.Errorf("this test lists %q, which web/assets/app.js never reads — drop it", k)
			}
		}

		settings := decodeObject(t, doGet(t, server.URL, "/api/memory/settings", sessionID))
		assertKeys("GET /api/memory/settings", settings, "enabled")

		// The write direction matters too: the page PUTs {"enabled": …}.
		body, _ := json.Marshal(map[string]bool{"enabled": true})
		req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/memory/settings", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+sessionID)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		assertKeys("PUT /api/memory/settings", decodeObject(t, resp), "enabled")

		result := decodeObject(t, doPost(t, server.URL, "/api/memory/consolidate", sessionID, map[string]string{}))
		assertKeys("POST /api/memory/consolidate", result, "archived_count", "profile_updated", "skipped")
	})

	t.Run("documents", func(t *testing.T) {
		ingest := decodeObject(t, doPost(t, server.URL, "/api/documents", sessionID, map[string]string{
			"name": "运维手册", "content": "重启服务前先确认备份完成。",
		}))
		assertKeys("POST /api/documents", ingest, "id", "name", "chunks", "chars")

		docs := decodeArray(t, doGet(t, server.URL, "/api/documents", sessionID))
		if len(docs) == 0 {
			t.Fatal("the ingested document was not listed")
		}
		assertKeys("GET /api/documents[]", docs[0], "id", "name", "chunks", "chars")
	})

	t.Run("models", func(t *testing.T) {
		models := decodeObject(t, doGet(t, server.URL, "/api/models", sessionID))
		assertKeys("GET /api/models", models, "models", "current_model")
		list, _ := models["models"].([]any)
		if len(list) == 0 {
			t.Fatal("no model profiles returned")
		}
		first, _ := list[0].(map[string]any)
		assertKeys("GET /api/models models[]", first, "id", "name", "needs_api_key", "has_api_key")
	})

	t.Run("token bar", func(t *testing.T) {
		info := decodeObject(t, doGet(t, server.URL, "/api/chat/t_default/tokens", sessionID))
		assertKeys("GET /api/chat/{id}/tokens", info, "current", "max", "threshold")
	})

	t.Run("approvals", func(t *testing.T) {
		// A destructive tool as admin: the run interrupts and the request becomes
		// pending, which is what the page lists.
		resp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
			"threadId": "t_contract", "message": "删除订单A-1001",
		})
		resp.Body.Close()

		pending := decodeArray(t, doGet(t, server.URL, "/api/approvals", sessionID))
		if len(pending) == 0 {
			t.Fatal("expected a pending approval after the destructive tool call")
		}
		// These names are Go field names, not snake_case: ApprovalRequest carries
		// no JSON tags, so the page reads them capitalised. That is what the page
		// does, so that is what this pins.
		assertKeys("GET /api/approvals[]", pending[0],
			"InterruptID", "Status", "ThreadID", "ToolName", "NodeName",
			"RiskLevel", "Message", "Arguments", "Payload")
	})
}

// TestIntegration_FrontendSSEContract pins the streaming frame names and the
// keys of the done frame, which is where a failed run reports itself.
func TestIntegration_FrontendSSEContract(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	body, _ := json.Marshal(map[string]any{
		"threadId": "t_sse_contract", "message": "你好", "stream": true,
	})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/agent/chat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	appJS := readAppJS(t)
	frames := map[string]map[string]any{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	current := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			current = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err == nil {
				if _, seen := frames[current]; !seen {
					frames[current] = payload
				}
			}
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatal(err)
	}

	// The page dispatches on exactly these three. A frame the server stops
	// sending leaves a dead branch in the page; a frame it starts sending that
	// the page does not handle is invisible progress. Both are contract breaks.
	for _, name := range []string{"chunk", "done"} {
		if _, ok := frames[name]; !ok {
			t.Errorf("the stream never emitted a %q frame; the page handles it", name)
		}
		if !strings.Contains(appJS, "eventType === '"+name+"'") {
			t.Errorf("the server emits %q but the page does not handle it", name)
		}
	}
	// The page used to branch on an "error" frame that no code path emits; a
	// failed run arrives as done with status=error. Pin that.
	if _, ok := frames["error"]; ok {
		t.Error("an error frame appeared: the page no longer handles one")
	}

	done := frames["done"]
	if done == nil {
		t.Fatal("no done frame to inspect")
	}
	for _, k := range []string{"status", "answer", "memory", "events", "contextTokens", "actualTokens"} {
		if _, ok := done[k]; !ok {
			t.Errorf("the done frame has no %q, but the page reads it", k)
		}
		if !strings.Contains(appJS, k) {
			t.Errorf("this test asserts done.%s, which the page never reads — drop it", k)
		}
	}
	if done["status"] != "completed" {
		t.Fatalf("expected a completed run, got %v", done["status"])
	}
}

// readAppJS loads the page script, so an assertion about a key nobody reads can
// be caught rather than silently kept.
func readAppJS(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "web", "assets", "app.js"))
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	return string(raw)
}
