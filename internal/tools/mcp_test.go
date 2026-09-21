package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
)

// fakeMCPTool stands in for one tool returned by the Eino MCP adapter, so the
// conversion can be tested without a server subprocess.
type fakeMCPTool struct {
	info  *schema.ToolInfo
	reply string
	err   error
	calls int
}

func (f *fakeMCPTool) Info(context.Context) (*schema.ToolInfo, error) { return f.info, nil }

func (f *fakeMCPTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	f.calls++
	return f.reply, f.err
}

// infoOnlyTool implements BaseTool but not InvokableTool: it can be described but
// not called, so it must be skipped rather than registered and then failing.
type infoOnlyTool struct{ info *schema.ToolInfo }

func (i *infoOnlyTool) Info(context.Context) (*schema.ToolInfo, error) { return i.info, nil }

func nestedSchema(t *testing.T) *schema.ParamsOneOf {
	t.Helper()
	// A schema with a nested object — the shape the flat ParamSchema form cannot
	// represent, which is why ParamsOneOf is carried through instead.
	js := &schema.ToolInfo{
		Name: "x",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"filter": {Type: schema.Object, Desc: "nested filter"},
		}),
	}
	return js.ParamsOneOf
}

// TestConvertMCPTool covers the mapping from an Eino MCP tool to the framework's
// RegisteredTool, including the local-only approval policy.
func TestConvertMCPTool(t *testing.T) {
	ctx := context.Background()

	t.Run("carries identity, schema and result", func(t *testing.T) {
		remote := &fakeMCPTool{
			info: &schema.ToolInfo{Name: "read_notes", Desc: "read a note", ParamsOneOf: nestedSchema(t)},
			// The exact shape the MCP adapter hands back: a JSON envelope around
			// the server's text.
			reply: `{"content":[{"type":"text","text":"note body"}]}`,
		}
		rt, err := convertMCPTool(ctx, "demo", remote, MCPToolPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		if rt.Meta.Name != "read_notes" || rt.Meta.Description != "read a note" {
			t.Fatalf("metadata not carried: %+v", rt.Meta)
		}
		if rt.Meta.RequiredPerm != "read_notes" {
			t.Fatalf("permission key must be the tool name, got %q", rt.Meta.RequiredPerm)
		}
		if rt.Meta.ParamsOneOf == nil {
			t.Fatal("the original schema must be preserved for nested parameters")
		}
		if rt.Meta.RequiresApproval {
			t.Fatal("approval must be off unless the local policy asks for it")
		}

		res := rt.Fn(nil, `{"id":"n1"}`)
		if res.Error != "" {
			t.Fatalf("call failed: %s", res.Error)
		}
		if res.Content != "note body" {
			t.Fatalf("the model should receive the note text alone, got %q", res.Content)
		}
		if res.Metadata["mcp_server"] != "demo" {
			t.Fatalf("the originating server should be recorded: %+v", res.Metadata)
		}
		if remote.calls != 1 {
			t.Fatalf("expected one remote call, got %d", remote.calls)
		}
	})

	t.Run("approval comes from local policy only", func(t *testing.T) {
		remote := &fakeMCPTool{info: &schema.ToolInfo{Name: "delete_note", Desc: "delete a note"}}

		// Not listed locally: no approval, even though the name suggests danger.
		plain, err := convertMCPTool(ctx, "demo", remote, MCPToolPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		if plain.Meta.RequiresApproval {
			t.Fatal("approval must not be inferred from the tool name")
		}

		// Listed locally: approval required.
		guarded, err := convertMCPTool(ctx, "demo", remote, MCPToolPolicy{RequireApproval: []string{"delete_note"}})
		if err != nil {
			t.Fatal(err)
		}
		if !guarded.Meta.RequiresApproval {
			t.Fatal("a locally listed tool must require approval")
		}

		// Case and surrounding space are tolerated in configuration.
		spaced, err := convertMCPTool(ctx, "demo", remote, MCPToolPolicy{RequireApproval: []string{" Delete_Note "}})
		if err != nil {
			t.Fatal(err)
		}
		if !spaced.Meta.RequiresApproval {
			t.Fatal("configuration matching should ignore case and spaces")
		}
	})

	t.Run("a non-invokable tool is skipped", func(t *testing.T) {
		_, err := convertMCPTool(ctx, "demo", &infoOnlyTool{info: &schema.ToolInfo{Name: "describe_only"}}, MCPToolPolicy{})
		if err == nil {
			t.Fatal("a tool that cannot be called must not be registered")
		}
	})

	t.Run("a nameless tool is skipped", func(t *testing.T) {
		if _, err := convertMCPTool(ctx, "demo", &fakeMCPTool{info: &schema.ToolInfo{}}, MCPToolPolicy{}); err == nil {
			t.Fatal("a tool without a name must be rejected")
		}
	})

	t.Run("a remote failure is reported without the protocol envelope", func(t *testing.T) {
		remote := &fakeMCPTool{
			info: &schema.ToolInfo{Name: "read_notes"},
			err:  fmt.Errorf(`failed to call mcp tool: {"content":[{"type":"text","text":"笔记 n3 不存在"}]}`),
		}
		rt, err := convertMCPTool(ctx, "demo", remote, MCPToolPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		res := rt.Fn(nil, `{"id":"n3"}`)
		if res.Error == "" {
			t.Fatal("a remote failure must surface as an error result")
		}
		if !strings.Contains(res.Error, "笔记 n3 不存在") {
			t.Fatalf("the server's reason must survive: %q", res.Error)
		}
		if strings.Contains(res.Error, `"content"`) {
			t.Fatalf("the error must not carry protocol scaffolding: %q", res.Error)
		}
	})
}

// TestConnectMCPTools_SkipsUnavailableServer asserts one broken server does not
// take the others down with it, nor fail the caller.
func TestConnectMCPTools_SkipsUnavailableServer(t *testing.T) {
	tools, conns := ConnectMCPTools(context.Background(), []MCPServerSpec{
		{Name: "missing", Command: "definitely-not-a-real-command-xyz"},
	}, MCPToolPolicy{})

	if len(tools) != 0 || len(conns) != 0 {
		t.Fatalf("an unavailable server must yield nothing, got %d tools / %d conns", len(tools), len(conns))
	}
}

// TestMCPToolPolicyDefaults asserts the default risk level applied to remote
// tools, so a discovered tool is never treated as low-risk by accident.
func TestMCPToolPolicyDefaults(t *testing.T) {
	remote := &fakeMCPTool{info: &schema.ToolInfo{Name: "anything"}}
	rt, err := convertMCPTool(context.Background(), "demo", remote, MCPToolPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if rt.Meta.RiskLevel != RiskLevelMedium {
		t.Fatalf("remote tools should default to medium risk, got %q", rt.Meta.RiskLevel)
	}
}

// TestMCPResultText covers unwrapping the protocol envelope. The envelope is
// pure scaffolding for the model: it pays tokens for it on every remote call and
// it reads as noise in the answer, so only the server's text should survive.
func TestMCPResultText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single text part",
			in:   `{"content":[{"type":"text","text":"回滚流程：先切流量。"}]}`,
			want: "回滚流程：先切流量。",
		},
		{
			name: "several parts are joined in order",
			in:   `{"content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}`,
			want: "line one\nline two",
		},
		{
			name: "empty parts are dropped",
			in:   `{"content":[{"type":"text","text":""},{"type":"text","text":"only"}]}`,
			want: "only",
		},
		{
			// An image or embedded resource carries no text; emptying it would
			// silently lose the result, so the raw form is kept.
			name: "no text at all is left alone",
			in:   `{"content":[{"type":"image","data":"aGk=","mimeType":"image/png"}]}`,
			want: `{"content":[{"type":"image","data":"aGk=","mimeType":"image/png"}]}`,
		},
		{
			name: "plain text passes through",
			in:   "just a note",
			want: "just a note",
		},
		{
			name: "malformed json passes through",
			in:   `{"content":[{"type":"text",`,
			want: `{"content":[{"type":"text",`,
		},
		{
			name: "a json object without content passes through",
			in:   `{"id":"n1"}`,
			want: `{"id":"n1"}`,
		},
		{
			// The adapter prefixes its own message before the server's payload;
			// the text is still found and the prefix dropped.
			name: "envelope nested inside an error message",
			in:   `failed to call mcp tool: {"content":[{"type":"text","text":"笔记 n3 不存在"}]}`,
			want: "笔记 n3 不存在",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpResultText(tc.in); got != tc.want {
				t.Fatalf("mcpResultText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestMCPToolResultShape asserts an MCP result is wrapped like any other tool
// result, so the rest of the pipeline (truncation, events, budget) treats it
// identically.
func TestMCPToolResultShape(t *testing.T) {
	remote := &fakeMCPTool{
		info:  &schema.ToolInfo{Name: "read_notes"},
		reply: `{"content":[{"type":"text","text":"plain text"}]}`,
	}
	rt, err := convertMCPTool(context.Background(), "demo", remote, MCPToolPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	res := rt.Fn(nil, "{}")
	if res.Error != "" || res.Content != "plain text" {
		t.Fatalf("unexpected result: %+v", res)
	}
	// The MCP result must not leak the raw CallToolResult envelope into content.
	if strings.Contains(res.Content, "CallToolResult") {
		t.Fatalf("result leaked the protocol envelope: %q", res.Content)
	}
	_ = mcp.NewToolResultText // keep the mcp import meaningful if helpers change
}
