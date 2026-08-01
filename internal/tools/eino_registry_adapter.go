package tools

import (
	"github.com/cloudwego/eino/components/tool"
)

// EinoRegistryAdapter adapts our ToolRegistry to the agent.Registry interface.
// It converts project-level tools to Eino BaseTool.
type EinoRegistryAdapter struct {
	registry *ToolRegistry
}

// NewEinoRegistryAdapter creates a new adapter.
func NewEinoRegistryAdapter(registry *ToolRegistry) *EinoRegistryAdapter {
	return &EinoRegistryAdapter{registry: registry}
}

// GetToolsForNames returns Eino BaseTool for the given tool names.
func (a *EinoRegistryAdapter) GetToolsForNames(names []string) []tool.BaseTool {
	result := make([]tool.BaseTool, 0, len(names))
	for _, name := range names {
		if t, ok := a.registry.Get(name); ok {
			result = append(result, NewEinoTool(t))
		}
	}
	return result
}
