package tools

import (
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
)

// buildTestRBAC creates an RBACManager for tests.
func buildTestRBAC() *auth.RBACManager {
	return auth.NewRBACManager()
}

func TestACLMiddleware_AdminCanInvokeAllowedTools(t *testing.T) {
	rbac := buildTestRBAC()
	middleware := NewACLMiddleware(rbac)
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())
	registry.Register(NewWeatherTool())
	middleware.WrapAllTools(registry)

	ctx := map[string]any{"user_id": "u_admin", "roles": []string{"admin"}}

	// admin should be able to invoke calculator
	tool, _ := registry.Get("calculator")
	result := tool.Fn(ctx, `{"expression": "1+1"}`)
	if result.Error != "" {
		t.Errorf("admin should be able to use calculator, got error: %s", result.Error)
	}
}

func TestACLMiddleware_VisitorDeniedDangerousTools(t *testing.T) {
	rbac := buildTestRBAC()
	middleware := NewACLMiddleware(rbac)
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewDeleteOrderTool(orderStore))
	registry.Register(NewSendEmailTool(NewEmailStore()))
	registry.Register(NewGrepTool())
	middleware.WrapAllTools(registry)

	ctx := map[string]any{"user_id": "u_visitor", "roles": []string{"visitor"}}

	for _, toolName := range []string{"delete_order", "send_email", "grep"} {
		tool, ok := registry.Get(toolName)
		if !ok {
			t.Fatalf("tool %s not found in registry", toolName)
		}
		result := tool.Fn(ctx, `{}`)
		if result.Error == "" {
			t.Errorf("visitor should be denied for %s, but got no error", toolName)
		}
		if result.Metadata["status"] != "denied" {
			t.Errorf("expected status=denied for %s, got %v", toolName, result.Metadata["status"])
		}
	}
}

func TestACLMiddleware_VisitorCanUseAllowedTools(t *testing.T) {
	rbac := buildTestRBAC()
	middleware := NewACLMiddleware(rbac)
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())
	registry.Register(NewWeatherTool())
	registry.Register(NewQueryOrderTool(NewOrderStore()))
	middleware.WrapAllTools(registry)

	ctx := map[string]any{"user_id": "u_visitor", "roles": []string{"visitor"}}

	for _, toolName := range []string{"calculator", "weather", "query_order"} {
		tool, ok := registry.Get(toolName)
		if !ok {
			t.Fatalf("tool %s not found", toolName)
		}
		result := tool.Fn(ctx, `{}`)
		// ACL should not deny — any error should be business-level, not permission
		if result.Error != "" && result.Metadata["status"] == "denied" {
			t.Errorf("visitor should pass ACL for %s, but got denied", toolName)
		}
	}
}

func TestACLMiddleware_DenyResultMetadata(t *testing.T) {
	rbac := buildTestRBAC()
	middleware := NewACLMiddleware(rbac)
	registry := NewToolRegistry()
	registry.Register(NewDeleteOrderTool(NewOrderStore()))
	middleware.WrapAllTools(registry)

	ctx := map[string]any{"user_id": "u_visitor", "roles": []string{"visitor"}}
	tool, _ := registry.Get("delete_order")
	result := tool.Fn(ctx, `{"order_id":"A-1001"}`)

	if result.Metadata["denied_by"] != "acl_middleware" {
		t.Errorf("expected denied_by=acl_middleware, got %v", result.Metadata["denied_by"])
	}
	if result.Metadata["user_id"] != "u_visitor" {
		t.Errorf("expected user_id=u_visitor in metadata, got %v", result.Metadata["user_id"])
	}
}

func TestACLMiddleware_EmptyRolesDenied(t *testing.T) {
	rbac := buildTestRBAC()
	middleware := NewACLMiddleware(rbac)
	registry := NewToolRegistry()
	registry.Register(NewDeleteOrderTool(NewOrderStore()))
	middleware.WrapAllTools(registry)

	ctx := map[string]any{"user_id": "u_nobody", "roles": []string{}}
	tool, _ := registry.Get("delete_order")
	result := tool.Fn(ctx, `{"order_id":"A-1001"}`)

	if result.Error == "" {
		t.Error("user with no roles should be denied for delete_order")
	}
}
