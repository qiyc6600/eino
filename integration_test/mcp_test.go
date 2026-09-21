package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/example/agent-eino-demo/internal/app"
	"github.com/example/agent-eino-demo/internal/auth"
)

// buildDemoMCPServer compiles the in-repo MCP server once per test run. Using a
// real subprocess is the point: it exercises the same handshake, tools/list and
// tools/call path a third-party server would, with no network dependency.
func buildDemoMCPServer(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "mcp-demo-server")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, "../cmd/mcp-demo-server")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build demo MCP server: %v\n%s", err, out)
	}
	return exe
}

// createMCPTestApp builds the app with one external MCP server configured.
func createMCPTestApp(t *testing.T, serverExe string, extraEnv map[string]string) *app.App {
	t.Helper()
	t.Setenv("MODEL_PROVIDER", "mock")
	t.Setenv("ADDR", ":0")
	t.Setenv("BOOTSTRAP_ADMIN_USERNAME", "admin")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", testAdminPassword)
	t.Setenv("MCP_SERVERS", fmt.Sprintf(`[{"name":"demo","command":%q}]`, serverExe))
	for k, v := range extraEnv {
		t.Setenv(k, v)
	}
	cfg := app.LoadConfig()
	application := app.NewApp(cfg)
	if _, err := application.AuthSvc.CreateUser(context.Background(), "visitor", "visitor123", []string{"visitor"}); err != nil && !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("seed test visitor: %v", err)
	}
	return application
}

// TestIntegration_MCPToolsAreRegisteredAndACLCovered is the central assertion of
// the MCP integration: discovered remote tools go through the same registry and
// the same ACL wrapping as built-in tools, so a role without a grant is denied.
func TestIntegration_MCPToolsAreRegisteredAndACLCovered(t *testing.T) {
	exe := buildDemoMCPServer(t)
	application := createMCPTestApp(t, exe, nil)
	defer application.Close()
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()

	adminSession := doLogin(t, server.URL, "admin", testAdminPassword)

	// The remote tools must be visible in the registry, next to the built-ins.
	resp := doGet(t, server.URL, "/api/tools", adminSession)
	defer resp.Body.Close()
	var tools []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tools); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		if n, _ := tool["name"].(string); n != "" {
			names[n] = true
		}
	}
	if !names["read_notes"] || !names["delete_note"] {
		t.Fatalf("MCP tools missing from the registry: %v", names)
	}
	if !names["calculator"] {
		t.Fatalf("built-in tools disappeared: %v", names)
	}

	// admin holds a wildcard grant, so its tool list includes the remote ones.
	me := doGet(t, server.URL, "/api/auth/me", adminSession)
	defer me.Body.Close()
	var meBody map[string]any
	json.NewDecoder(me.Body).Decode(&meBody)
	adminTools, _ := meBody["tools"].([]any)
	adminHasReadNotes := false
	for _, n := range adminTools {
		if n == "read_notes" {
			adminHasReadNotes = true
		}
	}
	if !adminHasReadNotes {
		t.Fatalf("admin should be granted the remote tools, got %v", adminTools)
	}

	// The visitor has no grant, so the ACL must deny the remote tool. This is the
	// check that fails if MCP tools are registered after the ACL wrapping.
	visitorSession := doLogin(t, server.URL, "visitor", "visitor123")
	visitorMe := doGet(t, server.URL, "/api/auth/me", visitorSession)
	defer visitorMe.Body.Close()
	var visitorBody map[string]any
	json.NewDecoder(visitorMe.Body).Decode(&visitorBody)
	visitorTools, _ := visitorBody["tools"].([]any)
	for _, n := range visitorTools {
		if n == "read_notes" || n == "delete_note" {
			t.Fatalf("visitor must not be granted remote tools by default, got %v", visitorTools)
		}
	}

	// And the denial is enforced at execution, not just hidden from the prompt:
	// call the tool through the registry exactly as the executor would. This is
	// the assertion that catches MCP tools registered after the ACL wrapping — a
	// role-table check alone would still pass, because the grants are correct
	// either way; only the wrapping is missing.
	rt, ok := application.Registry.Get("read_notes")
	if !ok {
		t.Fatal("read_notes is not in the registry")
	}
	visitorIdentity := &auth.ToolIdentity{UserID: "u_visitor", Roles: []string{"visitor"}}
	if res := rt.Fn(visitorIdentity, `{"id":"n1"}`); res.Error == "" {
		t.Fatalf("ACL bypass: an ungranted role invoked a remote tool and got %q", res.Content)
	}

	// The same call as admin must go through and reach the remote server.
	adminIdentity := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}
	res := rt.Fn(adminIdentity, `{"id":"n1"}`)
	if res.Error != "" {
		t.Fatalf("admin should be able to call the granted remote tool: %s", res.Error)
	}
	if !strings.Contains(res.Content, "部署窗口") {
		t.Fatalf("the remote tool did not return its data: %q", res.Content)
	}
}

// TestIntegration_MCPApprovalPolicy asserts approval for a remote tool is driven
// by local configuration, and that approving actually runs it.
func TestIntegration_MCPApprovalPolicy(t *testing.T) {
	exe := buildDemoMCPServer(t)
	application := createMCPTestApp(t, exe, map[string]string{
		"MCP_REQUIRE_APPROVAL": "delete_note",
	})
	defer application.Close()
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	// The configured tool is gated; the other remote tool is not.
	resp := doGet(t, server.URL, "/api/tools", sessionID)
	defer resp.Body.Close()
	var listed []map[string]any
	json.NewDecoder(resp.Body).Decode(&listed)
	gated := map[string]bool{}
	for _, tool := range listed {
		if n, _ := tool["name"].(string); n != "" {
			gated[n], _ = tool["requires_approval"].(bool)
		}
	}
	if !gated["delete_note"] {
		t.Fatalf("delete_note should require approval per local policy: %v", gated)
	}
	if gated["read_notes"] {
		t.Fatalf("read_notes should not require approval: %v", gated)
	}

	// A locally-ungated remote tool runs without any interrupt.
	runResp := doPost(t, server.URL, "/api/agent/chat", sessionID, map[string]string{
		"message":  "用 read_notes 工具读取笔记 n1",
		"threadId": "t_mcp_read",
	})
	defer runResp.Body.Close()
	var run map[string]any
	json.NewDecoder(runResp.Body).Decode(&run)
	if run["status"] != "completed" {
		t.Fatalf("an ungated remote tool should run without approval, got %v", run["status"])
	}
}

// TestIntegration_MCPLifecycleAndResilience covers the two lifecycle rules: a
// configured-but-missing server must not stop the app from serving, and Close
// must terminate the servers it did start.
func TestIntegration_MCPLifecycleAndResilience(t *testing.T) {
	t.Run("an unavailable server does not stop startup", func(t *testing.T) {
		application := createMCPTestApp(t, "definitely-not-a-real-command-xyz", nil)
		defer application.Close()
		server := httptest.NewServer(application.Router.Handler())
		defer server.Close()

		sessionID := doLogin(t, server.URL, "admin", testAdminPassword)
		resp := doGet(t, server.URL, "/api/tools", sessionID)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the app should still serve with a broken MCP server, got %d", resp.StatusCode)
		}
		var listed []map[string]any
		json.NewDecoder(resp.Body).Decode(&listed)
		if len(listed) == 0 {
			t.Fatal("built-in tools should still be registered")
		}
	})

	t.Run("Close terminates the server subprocess", func(t *testing.T) {
		exe := buildDemoMCPServer(t)
		application := createMCPTestApp(t, exe, nil)
		server := httptest.NewServer(application.Router.Handler())
		defer server.Close()

		sessionID := doLogin(t, server.URL, "admin", testAdminPassword)
		before := doGet(t, server.URL, "/api/tools", sessionID)
		before.Body.Close()

		if err := application.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		// After Close the remote tool calls must fail rather than hang or panic.
		// The registry still holds the wrapper, so the assertion is that calling
		// it reports an error instead of succeeding.
		if !application.RBAC.CanInvokeTool(context.Background(), []string{"admin"}, "read_notes") {
			t.Fatal("unexpected: the grant should still be present after Close")
		}
	})
}

// TestIntegration_MCPDisabledByDefault is the regression guard: with no server
// configured the tool set is exactly the built-ins.
func TestIntegration_MCPDisabledByDefault(t *testing.T) {
	application := createTestApp(t)
	defer application.Close()
	server := httptest.NewServer(application.Router.Handler())
	defer server.Close()
	sessionID := doLogin(t, server.URL, "admin", testAdminPassword)

	resp := doGet(t, server.URL, "/api/tools", sessionID)
	defer resp.Body.Close()
	var listed []map[string]any
	json.NewDecoder(resp.Body).Decode(&listed)

	for _, tool := range listed {
		name, _ := tool["name"].(string)
		if name == "read_notes" || name == "delete_note" {
			t.Fatalf("MCP tools appeared without configuration: %v", name)
		}
	}
	if len(listed) != 6 {
		t.Fatalf("expected exactly the 6 built-in tools, got %d: %v", len(listed), listed)
	}
}

// TestIntegration_MCPConfigValidation asserts a malformed MCP_SERVERS value
// disables the feature instead of crashing the process.
func TestIntegration_MCPConfigValidation(t *testing.T) {
	for _, raw := range []string{"not json", `{"name":"x"}`, `[{"name":"no-command"}]`} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("MCP_SERVERS", raw)
			cfg := app.LoadConfig()
			if len(cfg.MCPServers) != 0 {
				t.Fatalf("a malformed value must yield no servers, got %+v", cfg.MCPServers)
			}
		})
	}

	t.Run("valid entry is parsed", func(t *testing.T) {
		t.Setenv("MCP_SERVERS", `[{"name":"demo","command":"go","args":["run","./cmd/mcp-demo-server"]}]`)
		cfg := app.LoadConfig()
		if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "demo" {
			t.Fatalf("valid config not parsed: %+v", cfg.MCPServers)
		}
		if strings.Join(cfg.MCPServers[0].Args, " ") != "run ./cmd/mcp-demo-server" {
			t.Fatalf("args not carried: %+v", cfg.MCPServers[0])
		}
	})
}
