// Command mcp-demo-server is a minimal MCP server over stdio, used to exercise
// the MCP integration offline.
//
// It exists so the tests and the default demo do not depend on npx or the
// network: the MCP client path (handshake, tools/list, tools/call) is identical
// whether the server is this binary or the official filesystem server, so
// pointing MCP_SERVERS at a real server needs no code change.
//
// The two tools are deliberately asymmetric: read_notes is harmless, delete_note
// mutates state. That is what makes it possible to verify that the framework's
// ACL and approval policy really do cover external tools.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// notes is the demo state the tools operate on.
var (
	mu    sync.Mutex
	notes = map[string]string{
		"n1": "部署窗口：每周三 22:00 之后，避开业务高峰。",
		"n2": "回滚流程：先切流量，再回退版本，最后核对监控。",
		"n3": "值班电话：内部短号 8821。",
	}
)

func main() {
	s := server.NewMCPServer("mcp-demo-server", "1.0.0")

	s.AddTool(
		mcp.NewTool("read_notes",
			mcp.WithDescription("读取一条运维笔记的内容。只读操作。"),
			mcp.WithString("id", mcp.Required(), mcp.Description("笔记编号，如 n1；留空则列出全部编号")),
		),
		handleReadNotes,
	)

	s.AddTool(
		mcp.NewTool("delete_note",
			mcp.WithDescription("删除一条运维笔记。此操作不可恢复。"),
			mcp.WithString("id", mcp.Required(), mcp.Description("要删除的笔记编号")),
		),
		handleDeleteNote,
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("mcp-demo-server: %v", err)
	}
}

func handleReadNotes(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := req.GetString("id", "")

	mu.Lock()
	defer mu.Unlock()

	if id == "" {
		ids := make([]string, 0, len(notes))
		for k := range notes {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		return mcp.NewToolResultText(fmt.Sprintf("可用笔记编号：%v", ids)), nil
	}
	content, ok := notes[id]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("笔记 %s 不存在", id)), nil
	}
	return mcp.NewToolResultText(content), nil
}

func handleDeleteNote(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := req.GetString("id", "")
	if id == "" {
		return mcp.NewToolResultError("id 不能为空"), nil
	}

	mu.Lock()
	defer mu.Unlock()

	if _, ok := notes[id]; !ok {
		return mcp.NewToolResultError(fmt.Sprintf("笔记 %s 不存在", id)), nil
	}
	delete(notes, id)
	return mcp.NewToolResultText(fmt.Sprintf("已删除笔记 %s", id)), nil
}

// Ensure os is used on every platform build (stdio server owns stdin/stdout).
var _ = os.Stdin
