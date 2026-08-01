package auth

import "context"

// RBACManager manages role-based access control.
type RBACManager struct {
	roles map[string]*Role
}

// NewRBACManager creates an RBACManager with the built-in roles.
func NewRBACManager() *RBACManager {
	m := &RBACManager{
		roles: make(map[string]*Role),
	}

	// admin can invoke all tools
	m.roles["admin"] = &Role{
		Name: "admin",
		Permissions: []Permission{
			{Resource: "tool", Action: "invoke", Name: "calculator"},
			{Resource: "tool", Action: "invoke", Name: "weather"},
			{Resource: "tool", Action: "invoke", Name: "grep"},
			{Resource: "tool", Action: "invoke", Name: "query_order"},
			{Resource: "tool", Action: "invoke", Name: "delete_order"},
			{Resource: "tool", Action: "invoke", Name: "send_email"},
		},
	}

	// visitor can only invoke a subset of tools
	m.roles["visitor"] = &Role{
		Name: "visitor",
		Permissions: []Permission{
			{Resource: "tool", Action: "invoke", Name: "calculator"},
			{Resource: "tool", Action: "invoke", Name: "weather"},
			{Resource: "tool", Action: "invoke", Name: "query_order"},
		},
	}

	return m
}

// CanInvokeTool checks whether the given roles allow invoking the named tool.
func (m *RBACManager) CanInvokeTool(ctx context.Context, roles []string, toolName string) bool {
	for _, roleName := range roles {
		role, ok := m.roles[roleName]
		if !ok {
			continue
		}
		for _, perm := range role.Permissions {
			if perm.Resource == "tool" && perm.Action == "invoke" && perm.Name == toolName {
				return true
			}
		}
	}
	return false
}

// GetRole returns a role by name.
func (m *RBACManager) GetRole(name string) (*Role, bool) {
	r, ok := m.roles[name]
	return r, ok
}

// ListRoles returns all defined roles.
func (m *RBACManager) ListRoles() []*Role {
	result := make([]*Role, 0, len(m.roles))
	for _, r := range m.roles {
		result = append(result, r)
	}
	return result
}

// GetToolsForRoles returns all tool names the given roles can invoke.
func (m *RBACManager) GetToolsForRoles(roles []string) []string {
	toolSet := make(map[string]bool)
	for _, roleName := range roles {
		role, ok := m.roles[roleName]
		if !ok {
			continue
		}
		for _, perm := range role.Permissions {
			if perm.Resource == "tool" && perm.Action == "invoke" {
				toolSet[perm.Name] = true
			}
		}
	}
	result := make([]string, 0, len(toolSet))
	for name := range toolSet {
		result = append(result, name)
	}
	return result
}
