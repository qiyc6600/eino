package tools

import (
	"encoding/json"
	"testing"

	"github.com/example/agent-eino-demo/internal/auth"
)

func TestOrderStore_UserIsolation(t *testing.T) {
	store := NewOrderStore()

	adminOrders := store.QueryByUser("u_admin")
	if len(adminOrders) == 0 {
		t.Error("expected admin to have seed orders")
	}

	visitorOrders := store.QueryByUser("u_visitor")
	if len(visitorOrders) == 0 {
		t.Error("expected visitor to have seed orders")
	}

	// Different users should have different orders
	if adminOrders[0].ID == visitorOrders[0].ID {
		t.Error("user isolation violated: admin and visitor have same order IDs")
	}
}

func TestOrderStore_Delete(t *testing.T) {
	store := NewOrderStore()

	// Admin deletes own order
	deleted := store.Delete("u_admin", "A-1001")
	if !deleted {
		t.Error("expected successful delete of A-1001")
	}

	// Verify it's gone
	orders := store.QueryByUser("u_admin")
	for _, o := range orders {
		if o.ID == "A-1001" {
			t.Error("order A-1001 should have been deleted")
		}
	}
}

func TestOrderStore_DeleteOtherUsersOrder(t *testing.T) {
	store := NewOrderStore()

	// Admin tries to delete visitor's order — should fail (user isolation)
	deleted := store.Delete("u_admin", "B-2001")
	if deleted {
		t.Error("user isolation violated: admin should not be able to delete visitor's order")
	}
}

func TestOrderStore_DeleteNonExistent(t *testing.T) {
	store := NewOrderStore()

	deleted := store.Delete("u_admin", "Z-9999")
	if deleted {
		t.Error("deleting nonexistent order should return false")
	}
}

func TestQueryOrderTool_OnlyOwnOrders(t *testing.T) {
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewQueryOrderTool(orderStore))

	tool, ok := registry.Get("query_order")
	if !ok {
		t.Fatal("query_order tool not found")
	}

	// Admin queries own orders
	adminCtx := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}
	result := tool.Fn(adminCtx, "{}")
	if result.Error != "" {
		t.Errorf("admin query failed: %s", result.Error)
	}

	// Verify admin gets their own orders
	var adminOrders []Order
	json.Unmarshal([]byte(result.Content), &adminOrders)
	for _, o := range adminOrders {
		if o.UserID != "u_admin" {
			t.Errorf("user isolation violated: admin got order belonging to %s", o.UserID)
		}
	}

	// Visitor queries own orders
	visitorCtx := &auth.ToolIdentity{UserID: "u_visitor", Roles: []string{"visitor"}}
	result2 := tool.Fn(visitorCtx, "{}")
	if result2.Error != "" {
		t.Errorf("visitor query failed: %s", result2.Error)
	}

	// Verify results are different
	if result.Content == result2.Content {
		t.Error("user isolation violated: admin and visitor got same order results")
	}
}

func TestQueryOrderTool_SpecificOrder(t *testing.T) {
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewQueryOrderTool(orderStore))

	tool, _ := registry.Get("query_order")
	ctx := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}

	result := tool.Fn(ctx, `{"order_id":"A-1001"}`)
	if result.Error != "" {
		t.Errorf("query specific order failed: %s", result.Error)
	}
}

func TestQueryOrderTool_MissingUserID(t *testing.T) {
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewQueryOrderTool(orderStore))

	tool, _ := registry.Get("query_order")
	ctx := &auth.ToolIdentity{UserID: "", Roles: []string{"admin"}}

	result := tool.Fn(ctx, "{}")
	if result.Error == "" {
		t.Error("expected error for missing user_id")
	}
	if result.Metadata["status"] != "system_error" {
		t.Errorf("expected status=system_error, got %v", result.Metadata["status"])
	}
}

func TestDeleteOrderTool_Success(t *testing.T) {
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewDeleteOrderTool(orderStore))

	tool, _ := registry.Get("delete_order")
	ctx := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}

	result := tool.Fn(ctx, `{"order_id":"A-1001"}`)
	if result.Error != "" {
		t.Errorf("delete failed: %s", result.Error)
	}
	if result.Metadata["status"] != "success" {
		t.Errorf("expected status=success, got %v", result.Metadata["status"])
	}
}

func TestDeleteOrderTool_MissingOrderID(t *testing.T) {
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewDeleteOrderTool(orderStore))

	tool, _ := registry.Get("delete_order")
	ctx := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}

	result := tool.Fn(ctx, "{}")
	if result.Error == "" {
		t.Error("expected error for missing order_id")
	}
}

func TestDeleteOrderTool_OtherUsersOrder(t *testing.T) {
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewDeleteOrderTool(orderStore))

	tool, _ := registry.Get("delete_order")
	ctx := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}

	result := tool.Fn(ctx, `{"order_id":"B-2001"}`)
	if result.Error == "" {
		t.Error("user isolation violated: admin should not be able to delete visitor's order")
	}
}

func TestCalculatorTool(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())

	tool, _ := registry.Get("calculator")
	ctx := &auth.ToolIdentity{UserID: "u_admin", Roles: []string{"admin"}}

	tests := []struct {
		expr     string
		hasError bool
	}{
		{`{"expression": "2 + 3"}`, false},
		{`{"expression": "10 * 5"}`, false},
		{`{"expression": "100 / 4"}`, false},
		{`{"expression": "10 - 3"}`, false},
		{`{}`, true}, // missing expression
	}

	for _, tt := range tests {
		result := tool.Fn(ctx, tt.expr)
		if tt.hasError && result.Error == "" {
			t.Errorf("expected error for %s", tt.expr)
		}
		if !tt.hasError && result.Error != "" {
			t.Errorf("unexpected error for %s: %s", tt.expr, result.Error)
		}
	}
}
