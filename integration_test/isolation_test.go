package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Cross-user isolation tests: every authenticated user must only be able to
// access their own threads, run events, and approvals.

func doDelete(t *testing.T, serverURL, path, sessionID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("DELETE", serverURL+path, nil)
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

// TestIntegration_ThreadIsolation verifies that conversation threads are
// namespaced per user: visitor cannot read, list, or delete admin's thread
// even when the client-side thread ID is identical.
func TestIntegration_ThreadIsolation(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	adminSession := doLogin(t, server.URL, "admin", testAdminPassword)
	visitorSession := doLogin(t, server.URL, "visitor", "visitor123")

	// Admin chats into thread "t_shared".
	resp := doPost(t, server.URL, "/api/agent/chat", adminSession, map[string]string{
		"message":  "你好",
		"threadId": "t_shared",
	})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("admin chat failed: %d", resp.StatusCode)
	}

	// Visitor reading the same thread ID must see an empty history
	// (their own namespace), never admin's messages.
	resp2 := doGet(t, server.URL, "/api/chat/t_shared/messages", visitorSession)
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("expected 200 for visitor thread read, got %d", resp2.StatusCode)
	}
	var visitorMsgs []map[string]any
	json.NewDecoder(resp2.Body).Decode(&visitorMsgs)
	if len(visitorMsgs) != 0 {
		t.Errorf("user isolation violated: visitor sees admin's messages: %v", visitorMsgs)
	}

	// Admin does see their own messages.
	resp3 := doGet(t, server.URL, "/api/chat/t_shared/messages", adminSession)
	defer resp3.Body.Close()
	var adminMsgs []map[string]any
	json.NewDecoder(resp3.Body).Decode(&adminMsgs)
	if len(adminMsgs) == 0 {
		t.Error("admin should see own thread messages")
	}

	// Visitor cannot delete admin's thread.
	resp4 := doDelete(t, server.URL, "/api/chat/t_shared/delete", visitorSession)
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 when visitor deletes admin's thread, got %d", resp4.StatusCode)
	}

	// The thread survives.
	resp5 := doGet(t, server.URL, "/api/chat/t_shared/messages", adminSession)
	defer resp5.Body.Close()
	var stillThere []map[string]any
	json.NewDecoder(resp5.Body).Decode(&stillThere)
	if len(stillThere) == 0 {
		t.Error("admin's thread must survive visitor's delete attempt")
	}

	// Thread listings are scoped: visitor's list must not contain t_shared.
	resp6 := doGet(t, server.URL, "/api/chat/threads", visitorSession)
	defer resp6.Body.Close()
	var visitorThreads []string
	json.NewDecoder(resp6.Body).Decode(&visitorThreads)
	for _, id := range visitorThreads {
		if id == "t_shared" {
			t.Error("user isolation violated: visitor's thread list contains admin's t_shared")
		}
	}
}

// TestIntegration_RunEventsOwnership verifies that run events are only
// readable by the user who started the run.
func TestIntegration_RunEventsOwnership(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	adminSession := doLogin(t, server.URL, "admin", testAdminPassword)
	visitorSession := doLogin(t, server.URL, "visitor", "visitor123")

	resp := doPost(t, server.URL, "/api/agent/chat", adminSession, map[string]string{
		"message":  "你好",
		"threadId": "t_events",
	})
	defer resp.Body.Close()

	var chatResp map[string]any
	json.NewDecoder(resp.Body).Decode(&chatResp)
	runID, _ := chatResp["run_id"].(string)
	if runID == "" {
		t.Fatalf("expected run_id in chat response, got %v", chatResp)
	}

	// Visitor must get 404 for admin's run events.
	resp2 := doGet(t, server.URL, "/api/agent/runs/"+runID+"/events", visitorSession)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for visitor reading admin's run events, got %d", resp2.StatusCode)
	}

	// Owner gets the events.
	resp3 := doGet(t, server.URL, "/api/agent/runs/"+runID+"/events", adminSession)
	defer resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Errorf("expected 200 for owner reading own run events, got %d", resp3.StatusCode)
	}
}

// TestIntegration_ResumeOwnership verifies that only the user who triggered
// an interrupt can view or resolve the approval. A denied cross-user resume
// must not execute the gated tool or disturb the pending approval.
func TestIntegration_ResumeOwnership(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	adminSession := doLogin(t, server.URL, "admin", testAdminPassword)
	visitorSession := doLogin(t, server.URL, "visitor", "visitor123")

	// Admin triggers a delete_order interrupt.
	resp := doPost(t, server.URL, "/api/agent/chat", adminSession, map[string]string{
		"message":  "删除订单A-1003",
		"threadId": "t_resume_iso",
	})
	defer resp.Body.Close()
	var chatResp map[string]any
	json.NewDecoder(resp.Body).Decode(&chatResp)
	if chatResp["status"] != "interrupted" {
		t.Fatalf("expected status=interrupted, got %v", chatResp["status"])
	}

	resp2 := doGet(t, server.URL, "/api/approvals", adminSession)
	defer resp2.Body.Close()
	var approvals []map[string]any
	json.NewDecoder(resp2.Body).Decode(&approvals)
	if len(approvals) == 0 {
		t.Fatal("expected at least 1 pending approval")
	}
	interruptID, _ := approvals[0]["InterruptID"].(string)

	// Visitor cannot even view the approval.
	resp3 := doGet(t, server.URL, "/api/approvals/"+interruptID, visitorSession)
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for visitor viewing admin's approval, got %d", resp3.StatusCode)
	}

	// Visitor cannot resolve the approval via /api/agent/resume.
	// Note: resume body carries a real boolean, so build the request inline.
	resumeReq, _ := json.Marshal(map[string]any{"interrupt_id": interruptID, "approved": true})
	reqV, _ := http.NewRequest("POST", server.URL+"/api/agent/resume", strings.NewReader(string(resumeReq)))
	reqV.Header.Set("Authorization", "Bearer "+visitorSession)
	reqV.Header.Set("Content-Type", "application/json")
	resp4, err := http.DefaultClient.Do(reqV)
	if err != nil {
		t.Fatalf("visitor resume request failed: %v", err)
	}
	defer resp4.Body.Close()
	var resumeResp map[string]any
	json.NewDecoder(resp4.Body).Decode(&resumeResp)
	if resumeResp["status"] != "error" {
		t.Errorf("visitor resume must be rejected, got %v", resumeResp)
	}

	// Visitor cannot resolve it via the decision endpoint either.
	decReq, _ := json.Marshal(map[string]any{"approved": true})
	req, _ := http.NewRequest("POST", server.URL+"/api/approvals/"+interruptID+"/decision", strings.NewReader(string(decReq)))
	req.Header.Set("Authorization", "Bearer "+visitorSession)
	req.Header.Set("Content-Type", "application/json")
	resp5, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("visitor decision request failed: %v", err)
	}
	defer resp5.Body.Close()
	if resp5.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for visitor approving admin's interrupt, got %d", resp5.StatusCode)
	}

	// The order must still be intact — the tool never executed cross-user.
	// Admin resolves their own approval and the delete succeeds normally.
	decReq2, _ := json.Marshal(map[string]any{"approved": true, "reason": ""})
	req2, _ := http.NewRequest("POST", server.URL+"/api/approvals/"+interruptID+"/decision", strings.NewReader(string(decReq2)))
	req2.Header.Set("Authorization", "Bearer "+adminSession)
	req2.Header.Set("Content-Type", "application/json")
	resp6, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("admin decision request failed: %v", err)
	}
	defer resp6.Body.Close()

	var approveResp map[string]any
	json.NewDecoder(resp6.Body).Decode(&approveResp)
	answer, _ := approveResp["answer"].(string)
	if strings.Contains(answer, "not found") || strings.Contains(answer, "不存在") {
		t.Errorf("order was disturbed by cross-user resume attempts: %s", answer)
	}
	if approveResp["approved"] != true {
		t.Errorf("admin approve should succeed, got %v", approveResp)
	}
}
