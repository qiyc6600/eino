package tools

import (
	"fmt"
	"sync"
)

// ToolFunc is the function signature for tool execution.
// It receives a context-like map and JSON arguments, returning a ToolResult.
type ToolFunc func(ctx map[string]any, arguments string) ToolResult

// RegisteredTool pairs metadata with the actual tool function.
type RegisteredTool struct {
	Meta ToolMeta
	Fn   ToolFunc
}

// ToolRegistry manages tool registration and lookup.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]RegisteredTool
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]RegisteredTool),
	}
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(t RegisteredTool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[t.Meta.Name]; exists {
		return fmt.Errorf("tool already registered: %s", t.Meta.Name)
	}
	r.tools[t.Meta.Name] = t
	return nil
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (RegisteredTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools.
func (r *ToolRegistry) List() []RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]RegisteredTool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ListForRoles returns tools that are allowed for the given roles.
// If roles is empty, returns all tools (for display purposes).
func (r *ToolRegistry) ListForRoles(allowedTools map[string]bool) []RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]RegisteredTool, 0, len(r.tools))
	for _, t := range r.tools {
		if allowedTools == nil || allowedTools[t.Meta.Name] {
			result = append(result, t)
		}
	}
	return result
}

// ListNames returns all registered tool names.
func (r *ToolRegistry) ListNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.tools))
	for name := range r.tools {
		result = append(result, name)
	}
	return result
}
