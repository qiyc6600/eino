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

// assertKeys checks a response object against the keys the page reads. The second
// direction — the page must actually mention the key — is what keeps the lists in
// this file from rotting into assertions about a contract nobody uses.
func assertKeys(t *testing.T, appJS, what string, obj map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Errorf("%s does not contain %q, but the page reads it", what, k)
		}
		if !strings.Contains(appJS, k) {
			t.Errorf("%s asserts %q, which web/assets/app.js never reads \u2014 drop it from this list", what, k)
		}
	}
}

func decodeObject(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var obj map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return obj
}

func decodeArray(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var arr []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return arr
}

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

	t.Run("auth me", func(t *testing.T) {
		me := decodeObject(t, doGet(t, server.URL, "/api/auth/me", sessionID))
		assertKeys(t, appJS, "GET /api/auth/me", me, "user", "tools")
		user, _ := me["user"].(map[string]any)
		if user == nil {
			t.Fatalf("user is not an object: %v", me["user"])
		}
		assertKeys(t, appJS, "GET /api/auth/me user", user, "username", "roles")
	})

	t.Run("tools", func(t *testing.T) {
		tools := decodeArray(t, doGet(t, server.URL, "/api/tools", sessionID))
		if len(tools) == 0 {
			t.Fatal("no tools registered")
		}
		assertKeys(t, appJS, "GET /api/tools[]", tools[0], "name", "risk_level")
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
		assertKeys(t, appJS, "GET /api/memory[]", entry, "key", "value", "source", "updated_at")

		// history is omitempty, so it only exists once a value was superseded.
		history, ok := entry["history"].([]any)
		if !ok || len(history) == 0 {
			t.Fatalf("a superseded entry should carry history, got %v", entry["history"])
		}
		first, _ := history[0].(map[string]any)
		assertKeys(t, appJS, "GET /api/memory[] history[]", first, "value")

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
		assertKeys(t, appJS, "GET /api/memory/settings", settings, "enabled")

		// The write direction matters too: the page PUTs {"enabled": …}.
		body, _ := json.Marshal(map[string]bool{"enabled": true})
		req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/memory/settings", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+sessionID)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		assertKeys(t, appJS, "PUT /api/memory/settings", decodeObject(t, resp), "enabled")

		result := decodeObject(t, doPost(t, server.URL, "/api/memory/consolidate", sessionID, map[string]string{}))
		assertKeys(t, appJS, "POST /api/memory/consolidate", result, "archived_count", "profile_updated", "skipped")
	})

	t.Run("documents", func(t *testing.T) {
		ingest := decodeObject(t, doPost(t, server.URL, "/api/documents", sessionID, map[string]string{
			"name": "运维手册", "content": "重启服务前先确认备份完成。",
		}))
		assertKeys(t, appJS, "POST /api/documents", ingest, "id", "name", "chunks", "chars")

		docs := decodeArray(t, doGet(t, server.URL, "/api/documents", sessionID))
		if len(docs) == 0 {
			t.Fatal("the ingested document was not listed")
		}
		assertKeys(t, appJS, "GET /api/documents[]", docs[0], "id", "name", "chunks", "chars")
	})

	t.Run("models", func(t *testing.T) {
		models := decodeObject(t, doGet(t, server.URL, "/api/models", sessionID))
		assertKeys(t, appJS, "GET /api/models", models, "models", "current_model")
		list, _ := models["models"].([]any)
		if len(list) == 0 {
			t.Fatal("no model profiles returned")
		}
		first, _ := list[0].(map[string]any)
		assertKeys(t, appJS, "GET /api/models models[]", first, "id", "name", "needs_api_key", "has_api_key")
	})

	t.Run("token bar", func(t *testing.T) {
		info := decodeObject(t, doGet(t, server.URL, "/api/chat/t_default/tokens", sessionID))
		assertKeys(t, appJS, "GET /api/chat/{id}/tokens", info, "current", "max", "threshold")
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
		assertKeys(t, appJS, "GET /api/approvals[]", pending[0],
			"InterruptID", "Status", "ThreadID", "ToolName", "NodeName",
			"RiskLevel", "Message", "Arguments", "Payload")
	})
}

// postJSON sends an arbitrary JSON body, so a payload can keep the types the page
// actually sends (booleans stay booleans).
func postJSON(t *testing.T, serverURL, path, sessionID string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(raw))
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

// interruptingChat drives a run that pauses for approval and returns the interrupt
// object, which the page renders as an inline approval card.
func interruptingChat(t *testing.T, serverURL, sessionID, threadID string) map[string]any {
	t.Helper()
	resp := postJSON(t, serverURL, "/api/agent/chat", sessionID, map[string]any{
		"threadId": threadID, "message": "\u5220\u9664\u8ba2\u5355A-1001",
	})
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["status"] != "interrupted" {
		t.Fatalf("expected an interrupt, got status=%v", result["status"])
	}
	interrupt, _ := result["interrupt"].(map[string]any)
	if interrupt == nil {
		t.Fatalf("status is interrupted but no interrupt object was sent: %v", result)
	}
	return interrupt
}

// TestIntegration_FrontendRequestContract pins the payloads the page sends.
//
// This is the other half of the contract, and the half more likely to break
// silently: a renamed request field leaves the handler decoding an empty string,
// so the call is either rejected or quietly does the wrong thing while every
// response assertion stays green.
func TestIntegration_FrontendRequestContract(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)
	appJS := readAppJS(t)

	// Each body below is the page's own payload, key for key. The names are also
	// checked against app.js so the list cannot drift.
	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{"login", "/api/auth/login", map[string]any{"username": "admin", "password": testAdminPassword}},
		{"chat (non-streaming fallback)", "/api/agent/chat", map[string]any{
			"threadId": "t_req", "message": "\u4f60\u597d", "stream": false, "confirmBeforeExecute": false,
		}},
		{"memory write", "/api/memory", map[string]any{"key": "k_req", "value": "v_req"}},
		{"document ingest", "/api/documents", map[string]any{"name": "req-doc", "content": "c"}},
		{"model switch", "/api/models/switch", map[string]any{"profile_id": "mock", "api_key": ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, server.URL, c.path, sessionID, c.body)
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				t.Errorf("POST %s rejected the page's payload: %d %s", c.path, resp.StatusCode, raw)
			}
			for k := range c.body {
				if !strings.Contains(appJS, k) {
					t.Errorf("%s sends %q, which web/assets/app.js never sends \u2014 drop it from this list", c.path, k)
				}
			}
		})
	}

	t.Run("approval decision", func(t *testing.T) {
		interrupt := interruptingChat(t, server.URL, sessionID, "t_decision")
		id, _ := interrupt["interrupt_id"].(string)
		if id == "" {
			t.Fatalf("no interrupt_id to decide on: %v", interrupt)
		}
		// The page sends exactly these three keys.
		resp := postJSON(t, server.URL, "/api/approvals/"+id+"/decision", sessionID, map[string]any{
			"approved": false, "reason": "contract test", "stream": false,
		})
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("the decision payload was rejected: %d %s", resp.StatusCode, raw)
		}
		for _, k := range []string{"approved", "reason", "stream"} {
			if !strings.Contains(appJS, k) {
				t.Errorf("this test sends %q, which web/assets/app.js never sends \u2014 drop it", k)
			}
		}
	})

	t.Run("interrupt object", func(t *testing.T) {
		// The page renders the interrupt as an inline approval card, reading these
		// keys from either transport.
		interrupt := interruptingChat(t, server.URL, sessionID, "t_interrupt_shape")
		for _, k := range []string{"interrupt_id", "tool_name", "message", "arguments", "type"} {
			if _, ok := interrupt[k]; !ok {
				t.Errorf("the interrupt object has no %q, but the page reads it", k)
			}
			if !strings.Contains(appJS, k) {
				t.Errorf("this test asserts interrupt.%s, which the page never reads \u2014 drop it", k)
			}
		}
		// plan is omitempty: present for node-level interrupts, absent for tool
		// ones. The page guards it, so only the "the page reads it" direction holds.
		if !strings.Contains(appJS, "plan") {
			t.Error("the page no longer reads interrupt.plan; drop it from this list")
		}
	})

	t.Run("approval history", func(t *testing.T) {
		// Drive one approval to a decision so the history has a row.
		interrupt := interruptingChat(t, server.URL, sessionID, "t_history_contract")
		id, _ := interrupt["interrupt_id"].(string)
		resp := postJSON(t, server.URL, "/api/approvals/"+id+"/decision", sessionID, map[string]any{
			"approved": false, "reason": "contract", "stream": false,
		})
		resp.Body.Close()

		rows := decodeArray(t, doGet(t, server.URL, "/api/approvals/history", sessionID))
		if len(rows) == 0 {
			t.Fatal("the decision did not reach the history")
		}
		// The capitalised names come from ApprovalRequest having no JSON tags.
		assertKeys(t, appJS, "GET /api/approvals/history[]", rows[0], "InterruptID", "Status", "ToolName", "DecidedAt")
		// The decision is the exception: it does carry tags, so it is lowercase.
		// A Go struct cannot pin this — encoding/json matches field names
		// case-insensitively, so `json:"Reason"` decodes `reason` and the test
		// passes while the page reads undefined.
		decision, ok := rows[0]["Decision"].(map[string]any)
		if !ok {
			t.Fatalf("Decision is not an object: %v", rows[0]["Decision"])
		}
		assertKeys(t, appJS, "GET /api/approvals/history[] Decision", decision, "approved", "reason")
		if _, mixed := decision["Reason"]; mixed {
			t.Error("Decision now also carries a capitalised Reason; the page reads the lowercase one")
		}
		// The case has to be asserted on the expression, not on the word: "reason"
		// appears throughout app.js, so a substring check passes whether the page
		// reads Decision.reason or Decision.Reason — and only one of those works.
		if !strings.Contains(appJS, "Decision.reason") {
			t.Error("the page does not read Decision.reason; the reason would render as nothing")
		}
		if strings.Contains(appJS, "Decision.Reason") {
			t.Error("the page reads Decision.Reason, but the server sends it lowercase")
		}
	})

	t.Run("model switch response", func(t *testing.T) {
		// The page reads current_model back to update its selector.
		resp := postJSON(t, server.URL, "/api/models/switch", sessionID, map[string]any{"profile_id": "mock"})
		assertKeys(t, appJS, "POST /api/models/switch", decodeObject(t, resp), "current_model")
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
		"threadId": "t_sse_contract", "message": "计算 1+1", "stream": true,
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
	// Every payload per frame name, not just the first: the tool_call frames come
	// in sequence (route, then start, then end) and a key can differ between them.
	frames := map[string][]map[string]any{}
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
				frames[current] = append(frames[current], payload)
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

	// The tool_call frame drives the live progress line. The page switches on
	// `phase` and prints `tool`, so both must be there, and the phase must be one
	// the page has a branch for — a new phase would silently fall to its default.
	toolFrames := frames["tool_call"]
	if len(toolFrames) == 0 {
		t.Fatal("no tool_call frame: the progress line would stay empty for this run")
	}
	known := map[string]bool{"route": true, "start": true, "end": true, "denied": true, "approval": true}
	for i, toolFrame := range toolFrames {
		// Each frame is checked, not just the first: a key can differ between the
		// route frame and the ones that follow it.
		for _, k := range []string{"phase", "tool"} {
			if _, ok := toolFrame[k]; !ok {
				t.Errorf("tool_call frame #%d has no %q, but the page reads it: %v", i, k, toolFrame)
			}
			if !strings.Contains(appJS, k) {
				t.Errorf("this test asserts tool_call.%s, which the page never reads — drop it", k)
			}
		}
		if phase, _ := toolFrame["phase"].(string); !known[phase] {
			t.Errorf("tool_call frame #%d sent phase %q, which the page has no branch for "+
				"(it falls back to a bare bullet); add the branch or drop the phase", i, phase)
		}
	}

	doneFrames := frames["done"]
	if len(doneFrames) == 0 {
		t.Fatal("no done frame to inspect")
	}
	done := doneFrames[len(doneFrames)-1]
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
