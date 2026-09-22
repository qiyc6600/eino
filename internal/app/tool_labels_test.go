package app

import (
	"testing"

	"github.com/example/agent-eino-demo/internal/tools"
)

// TestBuiltinToolsAllHaveDisplayLabels is the guard that keeps internal names off
// the screen. It walks the same list the app registers, so adding a seventh
// built-in without a label fails here rather than reaching the UI as "some_tool".
func TestBuiltinToolsAllHaveDisplayLabels(t *testing.T) {
	registered := builtinTools(tools.NewOrderStore(), tools.NewEmailStore())
	if len(registered) == 0 {
		t.Fatal("builtinTools returned nothing; this test would pass vacuously")
	}

	for _, tool := range registered {
		name := tool.Meta.Name
		if !tools.HasDisplayLabel(name) {
			t.Errorf("built-in tool %q has no display label: the interface would show the internal name", name)
			continue
		}
		if label := tools.DisplayLabel(name); label == name {
			t.Errorf("built-in tool %q resolves to its own name", name)
		}
	}
}

// The labels are what the tool panel, the progress line and the approval card
// all show, so a tool reachable by a role must have one. This is the same set the
// RBAC table grants.
func TestBuiltinToolsCoverTheGrantedToolSet(t *testing.T) {
	granted := []string{"calculator", "weather", "grep", "query_order", "delete_order", "send_email"}
	registered := map[string]bool{}
	for _, tool := range builtinTools(tools.NewOrderStore(), tools.NewEmailStore()) {
		registered[tool.Meta.Name] = true
	}

	for _, name := range granted {
		if !registered[name] {
			t.Errorf("%s is granted to a role but is not in builtinTools", name)
		}
		if !tools.HasDisplayLabel(name) {
			t.Errorf("%s is granted to a role but has no display label", name)
		}
	}
}
