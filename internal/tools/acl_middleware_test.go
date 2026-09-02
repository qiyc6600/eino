package tools

import (
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
)

// buildTestRBAC creates an RBACManager for tests.
func buildTestRBAC() *auth.RBACManager {
	return auth.NewRBACManager()
}

func adminIdentity() *auth.ToolIdentity {
	return &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}
}

func visitorIdentity() *auth.ToolIdentity {
	return &auth.ToolIdentity{UserID: "u_visitor", Roles: []string{"visitor"}}
}

func TestACLMiddleware_AdminCanInvokeAllowedTools(t *testing.T) {
	rbac := buildTestRBAC()
	middleware := NewACLMiddleware(rbac)
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())
	registry.Register(NewWeatherTool())
	middleware.WrapAllTools(registry)

	// admin should be able to invoke calculator
	tool, _ := registry.Get("calculator")
	result := tool.Fn(adminIdentity(), `{"expression": "1+1"}`)
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

	for _, toolName := range []string{"delete_order", "send_email", "grep"} {
		tool, ok := registry.Get(toolName)
		if !ok {
			t.Fatalf("tool %s not found in registry", toolName)
		}
		result := tool.Fn(visitorIdentity(), `{}`)
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

	for _, toolName := range []string{"calculator", "weather", "query_order"} {
		tool, ok := registry.Get(toolName)
		if !ok {
			t.Fatalf("tool %s not found", toolName)
		}
		result := tool.Fn(visitorIdentity(), `{}`)
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

	tool, _ := registry.Get("delete_order")
	result := tool.Fn(visitorIdentity(), `{"order_id":"A-1001"}`)

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

	identity := &auth.ToolIdentity{UserID: "u_nobody", Roles: []string{}}
	tool, _ := registry.Get("delete_order")
	result := tool.Fn(identity, `{"order_id":"A-1001"}`)

	if result.Error == "" {
		t.Error("user with no roles should be denied for delete_order")
	}
}

// Nil identity (no authenticated user in the framework context) must be
// denied by the ACL layer even before the tool runs.
func TestACLMiddleware_NilIdentityDenied(t *testing.T) {
	rbac := buildTestRBAC()
	middleware := NewACLMiddleware(rbac)
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())
	middleware.WrapAllTools(registry)

	tool, _ := registry.Get("calculator")
	result := tool.Fn(nil, `{"expression": "1+1"}`)

	if result.Error == "" {
		t.Error("nil identity should be denied")
	}
	if result.Metadata["status"] != "denied" {
		t.Errorf("expected status=denied, got %v", result.Metadata["status"])
	}
}
