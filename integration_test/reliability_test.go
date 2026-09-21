package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/agent"
)

func decisionJSON(t *testing.T, url, session, id string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/api/approvals/"+id+"/decision", strings.NewReader(`{"approved":true}`))
	req.Header.Set("Authorization", "Bearer "+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("decision status %d: %v", resp.StatusCode, result)
	}
	return result
}
func TestIntegration_RestartAndConsecutiveApproval(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"SESSION", "CHECKPOINT", "THREAD", "MEMORY"} {
		t.Setenv(name+"_STORE", "file")
		t.Setenv(name+"_STORE_PATH", filepath.Join(dir, strings.ToLower(name)+".json"))
	}
	first := createTestApp(t)
	server := httptest.NewServer(first.Router.Handler())
	session := doLogin(t, server.URL, "admin", testAdminPassword)
	initial := chatJSON(t, server.URL, session, map[string]any{"threadId": "restart", "message": "删除订单 A-1001", "confirmBeforeExecute": true})
	plan, ok := initial["interrupt"].(map[string]any)
	if !ok {
		t.Fatalf("%v", initial)
	}
	server.Close()
	second := createTestApp(t)
	server = httptest.NewServer(second.Router.Handler())
	approvedPlan := decisionJSON(t, server.URL, session, plan["interrupt_id"].(string))
	tool, ok := approvedPlan["interrupt"].(map[string]any)
	if !ok || approvedPlan["status"] != "interrupted" || tool["tool_name"] != "delete_order" {
		t.Fatalf("missing second approval: %v", approvedPlan)
	}
	response := doGet(t, server.URL, "/api/approvals", session)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if bytes.Contains(body, []byte(`"State"`)) || bytes.Contains(body, []byte(`"Result"`)) {
		t.Fatalf("internal state leaked: %s", body)
	}
	server.Close()
	third := createTestApp(t)
	server = httptest.NewServer(third.Router.Handler())
	id := tool["interrupt_id"].(string)
	completed := decisionJSON(t, server.URL, session, id)
	if completed["status"] != "completed" || completed["runId"] != initial["run_id"] {
		t.Fatalf("%v", completed)
	}
	server.Close()
	fourth := createTestApp(t)
	server = httptest.NewServer(fourth.Router.Handler())
	defer server.Close()
	retry := decisionJSON(t, server.URL, session, id)
	if retry["answer"] != completed["answer"] || retry["status"] != "completed" {
		t.Fatalf("receipt changed after restart: %v", retry)
	}
}

type failingModel struct{ *agent.MockChatModel }

func (m *failingModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, errors.New("upstream unavailable")
}
func (m *failingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func TestIntegration_SSEPreservesFailureStatus(t *testing.T) {
	app := createTestApp(t)
	app.Runner.SetSteppedRunner(agent.NewSteppedRunner(&failingModel{agent.NewMockChatModel()}, app.Registry, app.HITLSvc, 20, nil, app.RBAC))
	server := httptest.NewServer(app.Router.Handler())
	defer server.Close()
	session := doLogin(t, server.URL, "admin", testAdminPassword)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/agent/chat", strings.NewReader(`{"message":"hello","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte(`"status":"error"`)) || bytes.Contains(body, []byte(`"status":"completed"`)) {
		t.Fatalf("incorrect SSE status: %s", body)
	}
}
func TestIntegration_ThreadStorageFailureReturnsHTTPError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("THREAD_STORE", "file")
	t.Setenv("THREAD_STORE_PATH", filepath.Join(dir, "threads.json"))
	app := createTestApp(t)
	server := httptest.NewServer(app.Router.Handler())
	defer server.Close()
	session := doLogin(t, server.URL, "admin", testAdminPassword)
	if err := os.Mkdir(filepath.Join(dir, "threads.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	response := doPost(t, server.URL, "/api/chat/threads", session, map[string]string{"threadId": "new"})
	defer response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("got status %d", response.StatusCode)
	}
	if len(app.Runner.ListThreads("u_admin")) != 0 {
		t.Fatal("failed write published a thread")
	}
}
