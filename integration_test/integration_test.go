package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/agent-eino-demo/internal/app"
	"github.com/example/agent-eino-demo/internal/auth"
)

const testAdminPassword = "admin-test-1234"

// createTestApp creates a test application with mock model provider.
func createTestApp(t *testing.T) *app.App {
	t.Setenv("MODEL_PROVIDER", "mock")
	t.Setenv("ADDR", ":0")
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", "admin")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", testAdminPassword)
	cfg := app.LoadConfig()
	application := app.NewApp(cfg)
	if _, err := application.AuthSvc.CreateUser(context.Background(), "visitor", "visitor123", []string{"visitor"}); err != nil && !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("seed test visitor: %v", err)
	}
	return application
}

// doLogin logs in and returns the session ID.
func doLogin(t *testing.T, serverURL, username, password string) string {
	t.Helper()
	loginBody, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(serverURL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for login, got %d", resp.StatusCode)
	}

	var loginResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response failed: %v", err)
	}

	sessionID, ok := loginResp["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatal("expected non-empty sessionId")
	}
	return sessionID
}

// doGet performs an authenticated GET request.
func doGet(t *testing.T, serverURL, path, sessionID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", serverURL+path, nil)
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

// doPost performs an authenticated POST request.
func doPost(t *testing.T, serverURL, path, sessionID string, body map[string]string) *http.Response {
	t.Helper()
	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", serverURL+path, bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func TestIntegration_LoginAndChat(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)
	if sessionID == "" {
		t.Fatal("expected non-empty sessionId")
	}

	// Verify user info via /me
	resp := doGet(t, server.URL, "/api/auth/me", sessionID)
	defer resp.Body.Close()

	var meResp map[string]any
	json.NewDecoder(resp.Body).Decode(&meResp)
	meUser, _ := meResp["user"].(map[string]any)
	if meUser["username"] != "admin" {
		t.Errorf("expected username=admin, got %v", meUser["username"])
	}
}

func TestIntegration_UnauthorizedAccess(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	// Chat without session — should fail with 401
	chatBody, _ := json.Marshal(map[string]string{"message": "1+1等于多少"})
	resp, err := http.Post(server.URL+"/api/agent/chat", "application/json", bytes.NewReader(chatBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("expected 401 without session, got %d", resp.StatusCode)
	}
}

func TestIntegration_LoginAndMe(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "visitor", "visitor123")

	resp := doGet(t, server.URL, "/api/auth/me", sessionID)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for /me, got %d", resp.StatusCode)
	}

	var meResp map[string]any
	json.NewDecoder(resp.Body).Decode(&meResp)

	meUser, _ := meResp["user"].(map[string]any)
	if meUser["username"] != "visitor" {
		t.Errorf("expected username=visitor, got %v", meUser["username"])
	}
}

func TestIntegration_ChatWithSession(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	resp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "1+1等于多少",
		"threadId": "t_test",
	})
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 with valid session, got %d", resp.StatusCode)
	}

	var chatResp map[string]any
	json.NewDecoder(resp.Body).Decode(&chatResp)

	if chatResp["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", chatResp["status"])
	}
}

func TestIntegration_ListTools(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	resp := doGet(t, server.URL, "/api/tools", sessionID)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for tools list, got %d", resp.StatusCode)
	}

	var tools []map[string]any
	json.NewDecoder(resp.Body).Decode(&tools)

	if len(tools) == 0 {
		t.Error("expected at least 1 tool")
	}
}

func TestIntegration_MemoryCRUD(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// PUT memory
	resp := doPost(t, server.URL, "/api/memory", sessionID, map[string]string{
		"key":   "test_key",
		"value": "test_value",
	})
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for memory PUT, got %d", resp.StatusCode)
	}

	// GET memory
	resp2 := doGet(t, server.URL, "/api/memory", sessionID)
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("expected 200 for memory GET, got %d", resp2.StatusCode)
	}

	var memEntries []map[string]any
	json.NewDecoder(resp2.Body).Decode(&memEntries)

	found := false
	for _, e := range memEntries {
		if e["key"] == "test_key" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected test_key in memory after PUT")
	}
}

// TestIntegration_MemorySettings covers the "do not remember" switch end to end:
// it toggles over HTTP, keeps the reserved entry out of the user-visible list,
// and refuses to let the memory API overwrite framework state.
func TestIntegration_MemorySettings(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// Default is on.
	resp := doGet(t, server.URL, "/api/memory/settings", sessionID)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for settings GET, got %d", resp.StatusCode)
	}
	var settings map[string]any
	json.NewDecoder(resp.Body).Decode(&settings)
	resp.Body.Close()
	if settings["memory_enabled"] != true {
		t.Fatalf("expected memory enabled by default, got %v", settings)
	}

	// Turn it off.
	putBody, _ := json.Marshal(map[string]any{"enabled": false})
	req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/memory/settings", bytes.NewReader(putBody))
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != 200 {
		t.Fatalf("expected 200 for settings PUT, got %d", putResp.StatusCode)
	}

	resp = doGet(t, server.URL, "/api/memory/settings", sessionID)
	json.NewDecoder(resp.Body).Decode(&settings)
	resp.Body.Close()
	if settings["memory_enabled"] != false {
		t.Fatalf("expected memory disabled after PUT, got %v", settings)
	}

	// The reserved settings entry must not appear as a user memory.
	resp = doGet(t, server.URL, "/api/memory", sessionID)
	var entries []map[string]any
	json.NewDecoder(resp.Body).Decode(&entries)
	resp.Body.Close()
	for _, e := range entries {
		if key, _ := e["key"].(string); strings.HasPrefix(key, "__") {
			t.Fatalf("reserved entry leaked into the memory list: %v", e)
		}
	}

	// And the memory API must refuse to delete it.
	delResp := doDelete(t, server.URL, "/api/memory/__settings", sessionID)
	delResp.Body.Close()
	if delResp.StatusCode == 200 {
		t.Fatal("deleting a reserved key through the memory API must be refused")
	}
}

func TestIntegration_ApprovalFlow(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// Chat: request to delete an order (should trigger HITL interrupt in mock mode)
	resp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "删除订单A-1001",
		"threadId": "t_approval",
	})
	defer resp.Body.Close()

	// The chat response (non-streaming JSON) should report an interrupted run.
	var chatResp map[string]any
	json.NewDecoder(resp.Body).Decode(&chatResp)
	if chatResp["status"] != "interrupted" {
		t.Fatalf("expected status=interrupted for delete_order, got %v", chatResp["status"])
	}

	// GET pending approvals — should contain the delete_order approval.
	resp2 := doGet(t, server.URL, "/api/approvals", sessionID)
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("expected 200 for approvals list, got %d", resp2.StatusCode)
	}

	var approvals []map[string]any
	json.NewDecoder(resp2.Body).Decode(&approvals)
	if len(approvals) == 0 {
		t.Fatal("expected at least 1 pending approval after delete_order interrupt")
	}

	// Regression test for the orphaned-tool_call bug: send a second delete
	// request to the SAME thread without resolving the first approval.
	// Previously this caused a 400 error from the LLM API ("An assistant
	// message with 'tool_calls' must be followed by tool messages") because
	// the thread contained an orphaned tool_call. sanitizeMessages should now
	// insert a placeholder tool result so the request succeeds.
	resp3 := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "删除订单B-2003",
		"threadId": "t_approval",
	})
	defer resp3.Body.Close()

	var chatResp3 map[string]any
	json.NewDecoder(resp3.Body).Decode(&chatResp3)
	if chatResp3["status"] == nil {
		t.Errorf("expected a valid response for same-thread retry, got: %v", chatResp3)
	}
	if answer, _ := chatResp3["answer"].(string); strings.Contains(answer, "400 Bad Request") ||
		strings.Contains(answer, "tool_calls") {
		t.Errorf("same-thread retry hit the orphaned tool_call 400 error: %v", chatResp3["answer"])
	}
}

// TestIntegration_ApprovalResumeNoDoubleExec is a regression test for the
// double-execution bug: patchInterruptToolResult and resolveNestedToolInterrupt
// both executed delete_order, so the second call found the order already deleted
// and returned "not found" instead of the real success result.
func TestIntegration_ApprovalResumeNoDoubleExec(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// 1. Request delete of A-1002 (which exists in seed data for u_admin)
	resp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "删除订单A-1002",
		"threadId": "t_dbl_exec",
	})
	defer resp.Body.Close()

	var chatResp map[string]any
	json.NewDecoder(resp.Body).Decode(&chatResp)
	if chatResp["status"] != "interrupted" {
		t.Fatalf("expected status=interrupted, got %v", chatResp["status"])
	}

	// 2. Get the interrupt ID from approvals
	resp2 := doGet(t, server.URL, "/api/approvals", sessionID)
	defer resp2.Body.Close()
	var approvals []map[string]any
	json.NewDecoder(resp2.Body).Decode(&approvals)
	if len(approvals) == 0 {
		t.Fatal("expected at least 1 pending approval")
	}
	interruptID, _ := approvals[0]["InterruptID"].(string)

	// 3. Approve the delete — the tool should execute ONCE and succeed
	decReq, _ := json.Marshal(map[string]any{"approved": true, "reason": ""})
	req, _ := http.NewRequest("POST", server.URL+"/api/approvals/"+interruptID+"/decision", bytes.NewReader(decReq))
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("approve request failed: %v", err)
	}
	defer resp3.Body.Close()

	var approveResp map[string]any
	json.NewDecoder(resp3.Body).Decode(&approveResp)
	answer, _ := approveResp["answer"].(string)

	// The answer should indicate SUCCESS (order deleted), NOT "not found"
	// (which would happen if the tool ran twice and the second call found
	// the order already gone).
	if strings.Contains(answer, "not found") || strings.Contains(answer, "未找到") || strings.Contains(answer, "不存在") {
		t.Errorf("approve returned 'not found' — tool was likely executed twice: %s", answer)
	}
	if approveResp["approved"] != true {
		t.Errorf("expected approved=true, got %v", approveResp["approved"])
	}
}

func TestIntegration_WrongPassword(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	resp, err := http.Post(server.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("expected 401 for wrong password, got %d", resp.StatusCode)
	}
}
