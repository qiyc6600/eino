package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decisionStream posts a streaming decision and returns the parsed SSE frames.
func decisionStream(t *testing.T, serverURL, sessionID, interruptID string, approved bool) []sseFrame {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"approved": approved, "reason": "", "stream": true})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/approvals/"+interruptID+"/decision", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stream failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for a streaming decision, got %d: %s", resp.StatusCode, raw)
	}
	return parseSSEFrames(t, raw)
}

// pendingInterruptID returns the first pending approval's interrupt id.
func pendingInterruptID(t *testing.T, serverURL, sessionID string) string {
	t.Helper()
	resp := doGet(t, serverURL, "/api/approvals", sessionID)
	defer resp.Body.Close()
	var approvals []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&approvals); err != nil {
		t.Fatalf("decode approvals: %v", err)
	}
	if len(approvals) == 0 {
		t.Fatal("expected at least one pending approval")
	}
	id, _ := approvals[0]["InterruptID"].(string)
	if id == "" {
		t.Fatalf("pending approval carries no interrupt id: %v", approvals[0])
	}
	return id
}

// TestIntegration_DecisionStreamsProgress covers the streaming decision endpoint:
// a resume runs the gated tool and continues the ReAct loop, so its progress has
// to reach the client instead of being hidden behind one JSON response.
func TestIntegration_DecisionStreamsProgress(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// Drive the run into a tool-level interrupt.
	resp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "删除订单A-1002",
		"threadId": "t_stream_decision",
	})
	defer resp.Body.Close()
	var chatResp map[string]any
	json.NewDecoder(resp.Body).Decode(&chatResp)
	if chatResp["status"] != "interrupted" {
		t.Fatalf("expected an interrupt to approve, got %v", chatResp["status"])
	}
	interruptID := pendingInterruptID(t, server.URL, sessionID)

	frames := decisionStream(t, server.URL, sessionID, interruptID, true)

	var doneIndex = -1
	phases := map[string]bool{}
	chunks := 0
	for i, frame := range frames {
		switch frame.event {
		case "done":
			doneIndex = i
		case "tool_call":
			if doneIndex >= 0 {
				t.Fatalf("progress arrived after done: %v", frame.data)
			}
			phase, _ := frame.data["phase"].(string)
			phases[phase] = true
		case "chunk":
			chunks++
		}
	}
	if doneIndex < 0 {
		t.Fatal("decision stream ended without a done frame")
	}
	done := frames[doneIndex].data
	if done["status"] != "completed" {
		t.Fatalf("expected the resume to complete, got %v (%v)", done["status"], done["answer"])
	}
	if done["approved"] != true {
		t.Fatalf("done frame should echo the decision, got %v", done["approved"])
	}
	if done["streamed"] != true {
		t.Fatalf("done frame should report streamed=true, got %v", done["streamed"])
	}
	// The gated tool's execution must be visible live, not only in the final payload.
	if !phases["start"] || !phases["end"] {
		t.Fatalf("expected live tool progress frames, got phases %v", phases)
	}
	if chunks == 0 {
		t.Fatal("expected the resumed answer to arrive as chunk frames")
	}
}

// TestIntegration_DecisionStreamSurfacesSecondInterrupt covers the consecutive
// approval case over SSE: a resumed run that interrupts again must hand the
// client an actionable interrupt in the terminal frame.
func TestIntegration_DecisionStreamSurfacesSecondInterrupt(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// The node-level plan review interrupts before any tool runs; approving the
	// plan then hits the tool-level gate for delete_order. The body is built here
	// rather than with doPost because confirmBeforeExecute is a JSON boolean.
	chatBody, _ := json.Marshal(map[string]any{
		"message":              "删除订单A-1002",
		"threadId":             "t_stream_second",
		"confirmBeforeExecute": true,
	})
	chatReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/agent/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Authorization", "Bearer "+sessionID)
	chatReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(chatReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var chatResp map[string]any
	json.NewDecoder(resp.Body).Decode(&chatResp)
	if chatResp["status"] != "interrupted" {
		t.Fatalf("expected a plan-review interrupt, got %v (%v)", chatResp["status"], chatResp["answer"])
	}
	interruptID := pendingInterruptID(t, server.URL, sessionID)

	frames := decisionStream(t, server.URL, sessionID, interruptID, true)
	var done map[string]any
	for _, frame := range frames {
		if frame.event == "done" {
			done = frame.data
		}
	}
	if done == nil {
		t.Fatal("decision stream ended without a done frame")
	}
	if done["status"] != "interrupted" {
		t.Fatalf("expected a second interrupt, got %v (%v)", done["status"], done["answer"])
	}
	next, _ := done["interrupt"].(map[string]any)
	if next == nil {
		t.Fatalf("done frame carries no interrupt: %v", done)
	}
	if next["tool_name"] != "delete_order" {
		t.Fatalf("expected the follow-on interrupt to be the gated tool, got %v", next)
	}
	if next["interrupt_id"] == interruptID {
		t.Fatal("the follow-on interrupt must be a new one, not the approved plan")
	}
}

// TestIntegration_DecisionDefaultStaysJSON pins the default response shape: the
// streaming path is opt-in, and existing clients must keep getting one JSON body.
func TestIntegration_DecisionDefaultStaysJSON(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	resp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "删除订单A-1002",
		"threadId": "t_json_decision",
	})
	defer resp.Body.Close()
	interruptID := pendingInterruptID(t, server.URL, sessionID)

	// No stream field: the historical JSON contract.
	body, _ := json.Marshal(map[string]any{"approved": true, "reason": ""})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/approvals/"+interruptID+"/decision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	decisionResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer decisionResp.Body.Close()
	if ct := decisionResp.Header.Get("Content-Type"); strings.Contains(ct, "event-stream") {
		t.Fatalf("a decision without stream=true must not return SSE, got %q", ct)
	}
	var result map[string]any
	if err := json.NewDecoder(decisionResp.Body).Decode(&result); err != nil {
		t.Fatalf("expected a JSON body: %v", err)
	}
	for _, field := range []string{"runId", "status", "answer", "approved", "events"} {
		if _, ok := result[field]; !ok {
			t.Errorf("JSON decision response is missing %q: %v", field, result)
		}
	}
	if result["status"] != "completed" {
		t.Fatalf("expected completed, got %v", result["status"])
	}
}
