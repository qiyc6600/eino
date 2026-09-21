package tools

import "github.com/cloudwego/eino/schema"

// RiskLevel indicates the risk level of a tool.
type RiskLevel string

const (
	RiskLevelLow    RiskLevel = "low"
	RiskLevelMedium RiskLevel = "medium"
	RiskLevelHigh   RiskLevel = "high"
)

// ToolMeta holds business metadata about a tool, beyond what Eino provides.
type ToolMeta struct {
	Name             string
	Description      string
	RequiredPerm     string // tool name used as permission key
	RiskLevel        RiskLevel
	RequiresApproval bool   // if true, HITL interrupt before execution
	ParamSchema      string // JSON Schema string for tool parameters
	// ParamsOneOf carries a tool's original Eino parameter schema when it is
	// richer than ParamSchema's flat {properties:{type,description}} form.
	// External MCP tools routinely declare nested objects and arrays, which that
	// flat round-trip would silently drop; when set, it wins over ParamSchema.
	ParamsOneOf *schema.ParamsOneOf `json:"-"`
}

// ToolResult is the unified result type for all tool invocations.
type ToolResult struct {
	Content  string         `json:"content"`
	Error    string         `json:"error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SuccessResult creates a ToolResult for a successful execution.
func SuccessResult(toolName, content string, extra ...map[string]any) ToolResult {
	meta := map[string]any{
		"tool":   toolName,
		"status": "success",
	}
	if len(extra) > 0 {
		for k, v := range extra[0] {
			meta[k] = v
		}
	}
	return ToolResult{
		Content:  content,
		Metadata: meta,
	}
}

// BusinessErrorResult creates a ToolResult for a business-level error.
func BusinessErrorResult(toolName, errMsg string, extra ...map[string]any) ToolResult {
	meta := map[string]any{
		"tool":   toolName,
		"status": "business_error",
	}
	if len(extra) > 0 {
		for k, v := range extra[0] {
			meta[k] = v
		}
	}
	return ToolResult{
		Error:    errMsg,
		Metadata: meta,
	}
}

// ACLDeniedResult creates a ToolResult for an ACL permission denial.
func ACLDeniedResult(toolName, reason string, userID string, roles []string) ToolResult {
	return ToolResult{
		Error: reason,
		Metadata: map[string]any{
			"tool":      toolName,
			"status":    "denied",
			"denied_by": "acl_middleware",
			"user_id":   userID,
			"roles":     roles,
		},
	}
}

// SystemErrorResult creates a ToolResult for a system-level error.
func SystemErrorResult(toolName, errMsg string, traceID string) ToolResult {
	return ToolResult{
		Error: errMsg,
		Metadata: map[string]any{
			"tool":     toolName,
			"status":   "system_error",
			"trace_id": traceID,
		},
	}
}

func conditionalSuffix(reason string) string {
	if reason != "" {
		return "：" + reason
	}
	return ""
}
