package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/tools"
)

// TestSupervisorWithExtraAgentIsRoutable covers the reachability rule: a tool that
// belongs to no sub-agent is unreachable because the dispatch table is built from
// each sub-agent's tool list. An extra agent must therefore be both created and
// actually routable by the supervisor's model.
func TestSupervisorWithExtraAgentIsRoutable(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewToolRegistry()
	if err := registry.Register(tools.RegisteredTool{
		Meta: tools.ToolMeta{Name: "read_notes", Description: "read a note"},
		Fn: func(_ *auth.ToolIdentity, _ string) tools.ToolResult {
			return tools.SuccessResult("read_notes", "note body")
		},
	}); err != nil {
		t.Fatal(err)
	}
	adapter := tools.NewEinoRegistryAdapter(registry)

	mock := NewMockChatModel()
	sup, err := BuildSupervisorWithExtraAgent(ctx, mock, adapter, &ExtraSubAgent{
		Name:        "mcp_agent",
		Instruction: "外部工具助手。",
		ToolNames:   []string{"read_notes"},
		Description: "外部工具助手，可用 read_notes。",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The extra agent must be present in the compiled supervisor.
	if len(sup.AgentToolWrappers()) != 4 {
		t.Fatalf("expected 4 sub-agents, got %d", len(sup.AgentToolWrappers()))
	}
	names := map[string]bool{}
	for _, w := range sup.AgentToolWrappers() {
		info, _ := w.Info(ctx)
		if info != nil {
			names[info.Name] = true
		}
	}
	if !names["mcp_agent"] {
		t.Fatalf("mcp_agent is not exposed to the supervisor: %v", names)
	}

	// And the mock model must be able to route to it, otherwise the offline demo
	// could never reach an external tool.
	call := matchToolCall("帮我读取笔记 n2", "帮我读取笔记 n2", boundToolInfos(sup, ctx))
	if call == nil || call.Function.Name != "mcp_agent" {
		got := "<nil>"
		if call != nil {
			got = call.Function.Name
		}
		t.Fatalf("the mock should route a notes question to mcp_agent, got %s", got)
	}
}

// TestSupervisorWithoutExtraAgentKeepsThreeAgents is the regression guard: the
// default supervisor is unchanged when no external tools exist.
func TestSupervisorWithoutExtraAgentKeepsThreeAgents(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewToolRegistry()
	adapter := tools.NewEinoRegistryAdapter(registry)

	sup, err := BuildDefaultSupervisor(ctx, NewMockChatModel(), adapter)
	if err != nil {
		t.Fatal(err)
	}
	if len(sup.AgentToolWrappers()) != 3 {
		t.Fatalf("expected the standard 3 sub-agents, got %d", len(sup.AgentToolWrappers()))
	}

	// An extra agent with no tools must be ignored rather than creating an empty
	// sub-agent.
	sup2, err := BuildSupervisorWithExtraAgent(ctx, NewMockChatModel(), adapter, &ExtraSubAgent{Name: "empty", ToolNames: nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(sup2.AgentToolWrappers()) != 3 {
		t.Fatalf("an extra agent with no tools must be skipped, got %d", len(sup2.AgentToolWrappers()))
	}
}

// boundToolInfos collects the tool definitions the supervisor presents to its
// model, which is what the mock matches keywords against.
func boundToolInfos(sup *SupervisorAgent, ctx context.Context) []*schema.ToolInfo {
	infos := make([]*schema.ToolInfo, 0)
	for _, w := range sup.AgentToolWrappers() {
		if info, err := w.Info(ctx); err == nil && info != nil {
			infos = append(infos, info)
		}
	}
	return infos
}
