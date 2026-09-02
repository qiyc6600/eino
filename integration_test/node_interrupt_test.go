package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Node-level plan review is triggered by an explicit per-request flag
// (confirmBeforeExecute), not by keyword matching on the message text.

func chatJSON(t *testing.T, serverURL, sessionID string, body map[string]any) map[string]any {
	t.Helper()
	bodyBytes, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", serverURL+"/api/agent/chat", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode chat response failed: %v", err)
	}
	return result
}

// TestIntegration_NodeInterrupt_ExplicitFlag verifies that setting
// confirmBeforeExecute pauses the run with a node-level plan review
// interrupt before any tool is executed.
func TestIntegration_NodeInterrupt_ExplicitFlag(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", "admin123")

	result := chatJSON(t, server.URL, sessionID, map[string]any{
		"threadId":             "t_node_flag",
		"message":              "查询北京天气",
		"confirmBeforeExecute": true,
	})

	if result["status"] != "interrupted" {
		t.Fatalf("expected status=interrupted with confirmBeforeExecute=true, got %v", result)
	}
	interrupt, _ := result["interrupt"].(map[string]any)
	if interrupt == nil {
		t.Fatal("expected interrupt payload in response")
	}
	if interrupt["type"] != "node" {
		t.Errorf("expected interrupt type=node, got %v", interrupt["type"])
	}
	if interrupt["node_name"] != "plan_review" {
		t.Errorf("expected node_name=plan_review, got %v", interrupt["node_name"])
	}
	if msg, _ := interrupt["message"].(string); !strings.Contains(msg, "weather") && !strings.Contains(msg, "search_agent") {
		t.Errorf("plan message should list the planned tool calls, got: %s", msg)
	}
}

// TestIntegration_NodeInterrupt_OffByDefault verifies that the same message
// without the flag completes normally — no keyword in the text can trigger
// the node-level interrupt anymore.
func TestIntegration_NodeInterrupt_OffByDefault(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", "admin123")

	// The message contains the old trigger keywords — they must no longer work.
	result := chatJSON(t, server.URL, sessionID, map[string]any{
		"threadId": "t_node_off",
		"message":  "查询北京天气，请确认后再执行",
	})

	if result["status"] != "completed" {
		t.Fatalf("expected status=completed without the flag, got %v (interrupt: %v)", result["status"], result["interrupt"])
	}
}

// TestIntegration_NodeInterrupt_ApproveResume verifies the full flow:
// flag on -> node interrupt -> approve via decision endpoint -> run continues
// and the tool actually executes.
func TestIntegration_NodeInterrupt_ApproveResume(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", "admin123")

	result := chatJSON(t, server.URL, sessionID, map[string]any{
		"threadId":             "t_node_resume",
		"message":              "查询北京天气",
		"confirmBeforeExecute": true,
	})
	if result["status"] != "interrupted" {
		t.Fatalf("expected status=interrupted, got %v", result)
	}
	interrupt, _ := result["interrupt"].(map[string]any)
	interruptID, _ := interrupt["interrupt_id"].(string)
	if interruptID == "" {
		t.Fatal("expected interrupt_id in interrupt payload")
	}

	decReq, _ := json.Marshal(map[string]any{"approved": true, "reason": ""})
	req, _ := http.NewRequest("POST", server.URL+"/api/approvals/"+interruptID+"/decision", strings.NewReader(string(decReq)))
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("decision request failed: %v", err)
	}
	defer resp.Body.Close()

	var decisionResp map[string]any
	json.NewDecoder(resp.Body).Decode(&decisionResp)
	if decisionResp["status"] != "completed" {
		t.Errorf("expected status=completed after approving the plan, got %v (answer: %v)", decisionResp["status"], decisionResp["answer"])
	}
	answer, _ := decisionResp["answer"].(string)
	if answer == "" {
		t.Error("expected a final answer after resume")
	}
}
