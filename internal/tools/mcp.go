package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/example/agent-eino-demo/internal/auth"
)

// MCPServerSpec describes one external MCP server reached over stdio.
type MCPServerSpec struct {
	Name    string
	Command string
	Args    []string
	Env     []string
}

// MCPToolPolicy is the local security policy for discovered remote tools. It is
// deliberately local: the MCP spec's tool annotations (readOnlyHint and friends)
// are self-reported by the server, so they may inform a human reading the UI but
// must never decide whether something needs approval.
type MCPToolPolicy struct {
	// RequireApproval lists remote tool names that must be approved by a human.
	RequireApproval []string
	// RiskLevel is applied to every discovered remote tool.
	RiskLevel RiskLevel
}

// MCPConnection is one live MCP server session plus the tools it exposed.
type MCPConnection struct {
	Name   string
	Client *mcpclient.Client
	Tools  []string
}

// Close terminates the server subprocess.
func (c *MCPConnection) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}
	return c.Client.Close()
}

// ConnectMCPTools starts each configured MCP server, discovers its tools and
// returns them as RegisteredTool values ready to be registered.
//
// A server that fails to start is logged and skipped rather than failing the
// caller: external tools are an optional capability, and one broken server should
// not stop the framework from serving its built-in tools. That is deliberately
// the opposite of the storage policy, where a failed backend refuses startup —
// storage holds data the framework must not silently lose.
//
// The returned connections must be closed by the caller to stop the subprocesses.
func ConnectMCPTools(ctx context.Context, specs []MCPServerSpec, policy MCPToolPolicy) ([]RegisteredTool, []*MCPConnection) {
	var (
		registered  []RegisteredTool
		connections []*MCPConnection
	)
	seen := make(map[string]string) // tool name -> server that provided it

	for _, spec := range specs {
		conn, serverTools, err := connectOneMCPServer(ctx, spec, policy)
		if err != nil {
			log.Printf("MCP server %q unavailable, skipping: %v", spec.Name, err)
			continue
		}
		connections = append(connections, conn)

		for _, rt := range serverTools {
			if from, dup := seen[rt.Meta.Name]; dup {
				// Built-in tools are registered first and win; between two remote
				// servers the first one wins. Skipping loudly beats silently
				// shadowing a tool.
				log.Printf("MCP tool %q from server %q conflicts with server %q, skipping", rt.Meta.Name, spec.Name, from)
				continue
			}
			seen[rt.Meta.Name] = spec.Name
			registered = append(registered, rt)
			conn.Tools = append(conn.Tools, rt.Meta.Name)
		}
		log.Printf("MCP server %q provided %d tool(s): %v", spec.Name, len(conn.Tools), conn.Tools)
	}
	return registered, connections
}

// connectOneMCPServer opens one stdio session, initializes it and converts the
// tools it advertises.
func connectOneMCPServer(ctx context.Context, spec MCPServerSpec, policy MCPToolPolicy) (*MCPConnection, []RegisteredTool, error) {
	client, err := mcpclient.NewStdioMCPClient(spec.Command, spec.Env, spec.Args...)
	if err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", spec.Command, err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{Name: "agent-eino-demo", Version: "1.0.0"}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}
	if _, err := client.Initialize(ctx, initRequest); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("initialize: %w", err)
	}

	remoteTools, err := mcpp.GetTools(ctx, &mcpp.Config{Cli: client})
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}

	conn := &MCPConnection{Name: spec.Name, Client: client}
	converted := make([]RegisteredTool, 0, len(remoteTools))
	for _, remote := range remoteTools {
		rt, err := convertMCPTool(ctx, spec.Name, remote, policy)
		if err != nil {
			log.Printf("MCP tool from %q skipped: %v", spec.Name, err)
			continue
		}
		converted = append(converted, rt)
	}
	return conn, converted, nil
}

// convertMCPTool turns one Eino-wrapped MCP tool into the framework's
// RegisteredTool shape.
func convertMCPTool(ctx context.Context, serverName string, remote tool.BaseTool, policy MCPToolPolicy) (RegisteredTool, error) {
	info, err := remote.Info(ctx)
	if err != nil {
		return RegisteredTool{}, fmt.Errorf("read tool info: %w", err)
	}
	if info == nil || info.Name == "" {
		return RegisteredTool{}, fmt.Errorf("tool has no name")
	}
	// Only invokable tools can actually be called by the agent.
	invokable, ok := remote.(tool.InvokableTool)
	if !ok {
		return RegisteredTool{}, fmt.Errorf("tool %q is not invokable", info.Name)
	}

	risk := policy.RiskLevel
	if risk == "" {
		risk = RiskLevelMedium
	}

	rt := RegisteredTool{
		Meta: ToolMeta{
			Name:        info.Name,
			Description: info.Desc,
			// The permission key is the tool name, so ACL decisions use exactly
			// the identifier the model calls.
			RequiredPerm: info.Name,
			RiskLevel:    risk,
			// Approval is decided locally. Remote annotations are self-reported
			// and therefore not a security boundary.
			RequiresApproval: containsName(policy.RequireApproval, info.Name),
			// Keep the original schema: MCP tools commonly declare nested objects
			// and arrays that the flat ParamSchema form cannot represent.
			ParamsOneOf: info.ParamsOneOf,
		},
	}
	rt.Meta.ParamSchema = renderSchema(info)

	rt.Fn = func(identity *auth.ToolIdentity, arguments string) ToolResult {
		// The tool identity carries the request context, which is how
		// cancellation and deadlines reach the remote server — the same pattern
		// the network-backed built-in tools use.
		callCtx := context.Background()
		if identity != nil && identity.Context != nil {
			callCtx = identity.Context
		}
		out, err := invokable.InvokableRun(callCtx, arguments)
		if err != nil {
			return SystemErrorResult(info.Name, fmt.Sprintf("MCP 工具调用失败：%s", mcpResultText(err.Error())), "")
		}
		return SuccessResult(info.Name, mcpResultText(out), map[string]any{"mcp_server": serverName})
	}
	return rt, nil
}

// mcpResultText unwraps the MCP result envelope
// ({"content":[{"type":"text","text":"…"}]}) so the model receives the tool's
// text rather than protocol scaffolding — the envelope is charged as tokens on
// every call and reads as noise in the answer.
//
// Anything that is not a recognisable envelope comes back unchanged, so a result
// carrying no text (an image or embedded resource) is never silently emptied.
func mcpResultText(raw string) string {
	start := strings.Index(raw, "{")
	if start < 0 {
		return raw
	}
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw[start:]), &payload); err != nil || len(payload.Content) == 0 {
		return raw
	}
	parts := make([]string, 0, len(payload.Content))
	for _, part := range payload.Content {
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	if len(parts) == 0 {
		return raw
	}
	return strings.Join(parts, "\n")
}

// renderSchema writes the tool's schema as JSON for display and for the tools
// API; ParamsOneOf remains the authoritative form used to call the model.
func renderSchema(info *schema.ToolInfo) string {
	if info.ParamsOneOf == nil {
		return ""
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil || js == nil {
		return ""
	}
	raw, err := json.Marshal(js)
	if err != nil {
		return ""
	}
	return string(raw)
}

func containsName(names []string, target string) bool {
	for _, n := range names {
		if strings.EqualFold(strings.TrimSpace(n), target) {
			return true
		}
	}
	return false
}
