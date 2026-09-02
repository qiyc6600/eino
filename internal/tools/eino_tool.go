package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/auth"
)

// EinoTool wraps a RegisteredTool into an Eino tool.InvokableTool.
type EinoTool struct {
	meta ToolMeta
	fn   ToolFunc
}

// NewEinoTool creates an Eino-compatible tool from a RegisteredTool.
func NewEinoTool(rt RegisteredTool) *EinoTool {
	return &EinoTool{meta: rt.Meta, fn: rt.Fn}
}

// Info implements tool.BaseTool.Info.
func (t *EinoTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	// Build params from our simple JSON schema string
	params := buildParamsFromSchema(t.meta.ParamSchema)
	return &schema.ToolInfo{
		Name:        t.meta.Name,
		Desc:        t.meta.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

// InvokableRun implements tool.InvokableTool.InvokableRun.
func (t *EinoTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	// Derive the typed tool identity from the Go context. A nil identity
	// means the request chain carried no authenticated user — the tool
	// implementations reject such invocations.
	identity := auth.ToolIdentityFromContext(ctx)

	// Execute the tool function
	result := t.fn(identity, argumentsInJSON)

	// If the tool returned an error (e.g. ACL denied), return it as a clear error
	// so the LLM cannot ignore it and must report the denial to the user.
	if result.Error != "" {
		// Return a human-readable error string that the LLM will relay to the user
		return result.Error, nil
	}

	// Return the result as JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"content":"","error":"marshal error: %v"}`, err), nil
	}
	return string(resultJSON), nil
}

// buildParamsFromSchema parses a simple JSON schema string and converts to Eino ParameterInfo map.
func buildParamsFromSchema(schemaStr string) map[string]*schema.ParameterInfo {
	// Parse the simple schema to extract parameter names and descriptions
	var schemaObj struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}

	if err := json.Unmarshal([]byte(schemaStr), &schemaObj); err != nil {
		return map[string]*schema.ParameterInfo{}
	}

	params := make(map[string]*schema.ParameterInfo, len(schemaObj.Properties))
	for name, prop := range schemaObj.Properties {
		params[name] = &schema.ParameterInfo{
			Type: schema.DataType(prop.Type),
			Desc: prop.Description,
		}
	}
	return params
}

// ConvertToEinoTools converts RegisteredTools from the registry to Eino InvokableTools.
func ConvertToEinoTools(registry *ToolRegistry) []tool.InvokableTool {
	toolList := registry.List()
	result := make([]tool.InvokableTool, 0, len(toolList))
	for _, t := range toolList {
		result = append(result, NewEinoTool(t))
	}
	return result
}

// ConvertToolsForNames converts specific tools by name to Eino InvokableTools.
func ConvertToolsForNames(registry *ToolRegistry, names []string) []tool.InvokableTool {
	result := make([]tool.InvokableTool, 0, len(names))
	for _, name := range names {
		if t, ok := registry.Get(name); ok {
			result = append(result, NewEinoTool(t))
		}
	}
	return result
}
