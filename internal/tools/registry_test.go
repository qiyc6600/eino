package tools

import "testing"

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	registry := NewToolRegistry()
	tool := NewCalculatorTool()
	err := registry.Register(tool)
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	got, ok := registry.Get("calculator")
	if !ok {
		t.Fatal("expected to find calculator tool")
	}
	if got.Meta.Name != "calculator" {
		t.Errorf("expected name=calculator, got %s", got.Meta.Name)
	}
	if got.Meta.RiskLevel != RiskLevelLow {
		t.Errorf("expected risk=low, got %s", got.Meta.RiskLevel)
	}
}

func TestToolRegistry_DuplicateRegister(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())
	err := registry.Register(NewCalculatorTool())
	if err == nil {
		t.Error("expected error on duplicate registration")
	}
}

func TestToolRegistry_GetNonExistent(t *testing.T) {
	registry := NewToolRegistry()
	_, ok := registry.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent tool")
	}
}

func TestToolRegistry_List(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())
	registry.Register(NewWeatherTool())

	tools := registry.List()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

func TestToolRegistry_ListForRoles(t *testing.T) {
	registry := NewToolRegistry()
	orderStore := NewOrderStore()
	registry.Register(NewCalculatorTool())
	registry.Register(NewWeatherTool())
	registry.Register(NewGrepTool())
	registry.Register(NewQueryOrderTool(orderStore))
	registry.Register(NewDeleteOrderTool(orderStore))
	registry.Register(NewSendEmailTool(NewEmailStore()))

	// Admin can see all tools
	rbac := buildTestRBAC()
	adminTools := rbac.GetToolsForRoles([]string{"admin"})
	adminAllowed := registry.ListForRoles(toSet(adminTools))
	if len(adminAllowed) != 6 {
		t.Errorf("expected 6 admin tools, got %d", len(adminAllowed))
	}

	// Visitor can only see 3
	visitorTools := rbac.GetToolsForRoles([]string{"visitor"})
	visitorAllowed := registry.ListForRoles(toSet(visitorTools))
	if len(visitorAllowed) != 3 {
		t.Errorf("expected 3 visitor tools, got %d", len(visitorAllowed))
	}
}

func TestToolRegistry_ListNames(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewCalculatorTool())
	registry.Register(NewWeatherTool())

	names := registry.ListNames()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d", len(names))
	}
}

func toSet(names []string) map[string]bool {
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}
