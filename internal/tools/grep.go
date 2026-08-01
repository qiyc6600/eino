package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GrepArgs represents arguments for the grep tool.
type GrepArgs struct {
	Pattern string `json:"pattern"`
	File    string `json:"file,omitempty"`
}

// NewGrepTool creates the grep tool (admin only).
func NewGrepTool() RegisteredTool {
	return RegisteredTool{
		Meta: ToolMeta{
			Name:             "grep",
			Description:      "Search for a pattern in sample log files. Admin only.",
			RequiredPerm:     "grep",
			RiskLevel:        RiskLevelMedium,
			RequiresApproval: false,
			ParamSchema: `{
				"type": "object",
				"properties": {
					"pattern": {"type": "string", "description": "Search pattern"},
					"file": {"type": "string", "description": "Optional file name to search in"}
				},
				"required": ["pattern"]
			}`,
		},
		Fn: executeGrep,
	}
}

// Mock log data for demo
var mockLogs = []string{
	"[2026-07-20 10:00:01] INFO  order-service: Order A-1001 created by user u_admin",
	"[2026-07-20 10:05:23] WARN  order-service: Order A-1001 payment pending",
	"[2026-07-20 10:10:45] ERROR order-service: Order A-1001 payment timeout",
	"[2026-07-20 10:15:00] INFO  order-service: Order A-1002 created by user u_visitor",
	"[2026-07-20 10:20:30] INFO  email-service: Email sent to admin@example.com",
	"[2026-07-20 10:25:00] ERROR auth-service: Failed login attempt from 192.168.1.100",
	"[2026-07-20 10:30:00] INFO  system: Scheduled backup completed",
}

func executeGrep(ctx map[string]any, arguments string) ToolResult {
	var args GrepArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return BusinessErrorResult("grep", "invalid arguments: "+err.Error())
	}

	var matches []string
	for _, line := range mockLogs {
		if strings.Contains(strings.ToLower(line), strings.ToLower(args.Pattern)) {
			matches = append(matches, line)
		}
	}

	if len(matches) == 0 {
		return SuccessResult("grep",
			fmt.Sprintf("No matches found for pattern: %s", args.Pattern),
			map[string]any{"pattern": args.Pattern, "match_count": 0},
		)
	}

	content := fmt.Sprintf("Found %d matches for '%s':\n%s", len(matches), args.Pattern, strings.Join(matches, "\n"))
	return SuccessResult("grep", content, map[string]any{"pattern": args.Pattern, "match_count": len(matches)})
}
