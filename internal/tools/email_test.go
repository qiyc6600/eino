package tools

import (
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
)

func TestSendEmailTool_IdempotentReplay(t *testing.T) {
	store := NewEmailStore()
	tool := NewSendEmailTool(store)
	identity := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}, RunID: "r-1", ToolCallID: "tc-1"}
	args := `{"to":"a@example.com","subject":"hello","body":"test"}`
	first := tool.Fn(identity, args)
	second := tool.Fn(identity, args)
	if first.Error != "" || second.Error != "" {
		t.Fatalf("idempotent email failed: first=%q second=%q", first.Error, second.Error)
	}
	if got := len(store.ListByUser("u_admin")); got != 1 {
		t.Fatalf("expected one email record, got %d", got)
	}
	if replayed, _ := second.Metadata["idempotent_replay"].(bool); !replayed {
		t.Fatalf("second send was not reported as replay: %+v", second.Metadata)
	}
}

func TestSendEmailTool_RequiresIdentity(t *testing.T) {
	result := NewSendEmailTool(NewEmailStore()).Fn(nil, `{"to":"a@example.com","subject":"hello","body":"test"}`)
	if result.Metadata["status"] != "system_error" {
		t.Fatalf("expected system error, got %+v", result)
	}
}
