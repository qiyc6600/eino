package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/agent-eino-demo/internal/app"
)

// createTestApp creates a test application with mock model provider.
func createTestApp(t *testing.T) *app.App {
	t.Setenv("MODEL_PROVIDER", "mock")
	t.Setenv("ADDR", ":0")
	cfg := app.LoadConfig()
	return app.NewApp(cfg)
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

	sessionID := doLogin(t, server.URL, "admin", "admin123")
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

	sessionID := doLogin(t, server.URL, "admin", "admin123")

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

	sessionID := doLogin(t, server.URL, "admin", "admin123")

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

	sessionID := doLogin(t, server.URL, "admin", "admin123")

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

func TestIntegration_ApprovalFlow(t *testing.T) {
	application := createTestApp(t)
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	sessionID := doLogin(t, server.URL, "admin", "admin123")

	// Chat: request to delete an order (should trigger HITL interrupt in mock mode)
	resp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "删除订单A-1001",
		"threadId": "t_approval",
	})
	resp.Body.Close()

	// GET pending approvals
	resp2 := doGet(t, server.URL, "/api/approvals", sessionID)
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("expected 200 for approvals list, got %d", resp2.StatusCode)
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
