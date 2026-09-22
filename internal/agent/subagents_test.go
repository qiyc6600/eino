package agent

import (
	"strings"
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
)

// TestBuiltinSubAgentsAreWellFormed pins the invariants the single table has to
// keep: unique names, a Chinese label that is not the internal name, and a tool
// list (the tool list is also what decides whether a role can reach the agent).
func TestBuiltinSubAgentsAreWellFormed(t *testing.T) {
	specs := BuiltinSubAgents()
	if len(specs) == 0 {
		t.Fatal("BuiltinSubAgents returned nothing")
	}

	names := map[string]bool{}
	labels := map[string]string{}
	for _, spec := range specs {
		if spec.Name == "" {
			t.Error("a sub-agent has an empty name")
		}
		if names[spec.Name] {
			t.Errorf("duplicate sub-agent name %q", spec.Name)
		}
		names[spec.Name] = true

		if spec.Label == "" {
			t.Errorf("%s has no label", spec.Name)
		} else if spec.Label == spec.Name {
			t.Errorf("%s has a label equal to its internal name", spec.Name)
		} else if prev, dup := labels[spec.Label]; dup {
			t.Errorf("%s and %s share the label %q", prev, spec.Name, spec.Label)
		}
		labels[spec.Label] = spec.Name

		if len(spec.ToolNames) == 0 {
			t.Errorf("%s owns no tools; it would be unreachable and un-grantable", spec.Name)
		}
		if spec.Description == "" || spec.Instruction == "" {
			t.Errorf("%s is missing its description or instruction", spec.Name)
		}
	}
}

// TestSupervisorPromptMatchesTheSubAgentTable is the regression test for the
// duplication this table replaced: runner.go used to carry its own copy of the
// names, descriptions and tool lists, and the two copies had already drifted.
// If someone reintroduces a second table, the prompt stops matching this one and
// the test fails.
func TestSupervisorPromptMatchesTheSubAgentTable(t *testing.T) {
	// admin can invoke every tool, so every sub-agent gets listed. A nil RBAC
	// manager lists none of them, which is why this test needs a real one.
	r := &Runner{rbac: auth.NewRBACManager()}
	prompt := r.buildSupervisorPrompt([]string{"admin"})

	for _, spec := range BuiltinSubAgents() {
		if !strings.Contains(prompt, spec.Name) {
			t.Errorf("prompt does not mention sub-agent %s", spec.Name)
		}
		if !strings.Contains(prompt, spec.Description) {
			t.Errorf("prompt does not use %s's description from the table", spec.Name)
		}
	}
	// The old copy said "内部工具：" where the table says "可用工具：" — a phrase
	// only the removed duplicate used.
	if strings.Contains(prompt, "内部工具：") {
		t.Error("prompt contains wording from the removed duplicate table")
	}
}

// TestDisplayLabelForResolvesEveryNamespace covers the three kinds of internal
// name the interface can be handed, plus the fallback.
func TestDisplayLabelForResolvesEveryNamespace(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"math_agent", "数学计算"},       // sub-agent
		{"calculator", "数学计算"},       // tool
		{"plan_review", "执行计划审批"},    // node the run paused at
		{"read_notes", "read_notes"}, // external MCP tool: no label, show the name
		{"", ""},                     // nothing to label
	}
	for _, tc := range cases {
		if got := DisplayLabelFor(tc.name); got != tc.want {
			t.Errorf("DisplayLabelFor(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestInterruptTargetNamePrefersToolThenNode(t *testing.T) {
	if got := InterruptTargetName("delete_order", ""); got != "delete_order" {
		t.Errorf("tool interrupt: got %q", got)
	}
	if got := InterruptTargetName("", "plan_review"); got != "plan_review" {
		t.Errorf("node interrupt: got %q", got)
	}
	if got := InterruptTargetName("", ""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}
