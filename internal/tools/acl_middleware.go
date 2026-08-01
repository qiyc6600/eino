package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/example/agent-eino-demo/internal/auth"
)

// ACLMiddleware checks tool-level permissions before execution.
// The middleware chain order is fixed: ACL -> Audit -> HITL -> Real tool execution.
// ACL must execute first; if denied, no approval request is created and no HITL interrupt is triggered.
type ACLMiddleware struct {
	rbac *auth.RBACManager
}

// NewACLMiddleware creates a new ACL middleware.
func NewACLMiddleware(rbac *auth.RBACManager) *ACLMiddleware {
	return &ACLMiddleware{rbac: rbac}
}

// CheckPermission checks whether the user with given roles can invoke the named tool.
// Returns (allowed bool, denyResult ToolResult).
// If allowed is true, denyResult is zero-value and should be ignored.
func (m *ACLMiddleware) CheckPermission(roles []string, toolName string) (bool, ToolResult) {
	if m.rbac.CanInvokeTool(context.Background(), roles, toolName) {
		return true, ToolResult{}
	}

	reason := fmt.Sprintf("❌ 权限不足：角色 [%s] 无权调用工具 %s，操作已被拒绝。",
		strings.Join(roles, ", "), toolName)
	return false, ACLDeniedResult(toolName, reason, "", roles)
}

// WrapTool returns a ToolFunc that applies ACL checking before delegating to the real tool.
// If the user does not have permission, the real tool is never called and the denial
// is returned as a ToolResult observation for the LLM.
func (m *ACLMiddleware) WrapTool(reg RegisteredTool) RegisteredTool {
	origFn := reg.Fn
	reg.Fn = func(ctx map[string]any, arguments string) ToolResult {
		// Extract roles from context
		rolesVal, _ := ctx["roles"]
		var roles []string
		switch v := rolesVal.(type) {
		case []string:
			roles = v
		case []interface{}:
			for _, r := range v {
				if s, ok := r.(string); ok {
					roles = append(roles, s)
				}
			}
		}

		allowed, denyResult := m.CheckPermission(roles, reg.Meta.Name)
		if !allowed {
			// Fill in user_id from context
			if userID, ok := ctx["user_id"].(string); ok {
				denyResult.Metadata["user_id"] = userID
			}
			return denyResult
		}

		return origFn(ctx, arguments)
	}
	return reg
}

// WrapAllTools wraps all tools in the registry with ACL middleware.
func (m *ACLMiddleware) WrapAllTools(registry *ToolRegistry) {
	tools := registry.List()
	for _, t := range tools {
		wrapped := m.WrapTool(t)
		registry.mu.Lock()
		registry.tools[t.Meta.Name] = wrapped
		registry.mu.Unlock()
	}
}
