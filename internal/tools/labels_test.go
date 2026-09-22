package tools

import "testing"

// TestDisplayLabel_FallsBackToTheInternalName pins the fallback: a tool with no
// declared label shows its own name. That is the honest answer for external MCP
// tools — their names come from a third party, and inventing a Chinese label for
// someone else's tool would be a guess.
func TestDisplayLabel_FallsBackToTheInternalName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"read_notes", "read_notes"},
		{"some_third_party_tool", "some_third_party_tool"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := DisplayLabel(tc.name); got != tc.want {
			t.Errorf("DisplayLabel(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestDisplayLabel_BuiltinsAreLabelledInChinese checks the labels themselves:
// present, not the internal name, and actually Chinese. A label equal to the
// name would mean the table entry does nothing, which is how a "translated" UI
// ends up still showing calculator.
func TestDisplayLabel_BuiltinsAreLabelledInChinese(t *testing.T) {
	builtins := []string{"calculator", "weather", "grep", "query_order", "delete_order", "send_email"}
	seen := map[string]string{}
	for _, name := range builtins {
		label := DisplayLabel(name)
		if label == name {
			t.Errorf("%s has no label: DisplayLabel returned the internal name", name)
			continue
		}
		if !HasDisplayLabel(name) {
			t.Errorf("%s resolves to %q but HasDisplayLabel says it has no entry", name, label)
		}
		if !containsHan(label) {
			t.Errorf("label for %s is %q, which is not Chinese", name, label)
		}
		if prev, dup := seen[label]; dup {
			t.Errorf("%s and %s share the label %q; the UI cannot tell them apart", prev, name, label)
		}
		seen[label] = name
	}
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
