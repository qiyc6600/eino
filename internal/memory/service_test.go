package memory

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestService_ExtractAndSave_Language(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "我喜欢用Python")
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	value, ok, _ := svc.GetPreference(context.Background(), "u_admin", "preferred_language")
	if !ok || value != "Python" {
		t.Errorf("expected preferred_language=Python, got %s (ok=%v)", value, ok)
	}
}

func TestService_ExtractAndSave_Style(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "请简洁回答")
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	value, ok, _ := svc.GetPreference(context.Background(), "u_admin", "answer_style")
	if !ok || value != "concise" {
		t.Errorf("expected answer_style=concise, got %s (ok=%v)", value, ok)
	}
}

func TestService_ExtractAndSave_DetailedStyle(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "请详细回答")
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	value, ok, _ := svc.GetPreference(context.Background(), "u_admin", "answer_style")
	if !ok || value != "detailed" {
		t.Errorf("expected answer_style=detailed, got %s (ok=%v)", value, ok)
	}
}

func TestService_ExtractAndSave_NoPreference(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "今天天气怎么样")
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	// No preference should be saved for a neutral message
	entries, _ := svc.ListPreferences(context.Background(), "u_admin")
	if len(entries) != 0 {
		t.Errorf("expected 0 preferences for neutral message, got %d", len(entries))
	}
}

func TestService_BuildMemoryContext(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	svc.PutPreference(context.Background(), "u_admin", "preferred_language", "Python")
	svc.PutPreference(context.Background(), "u_admin", "answer_style", "concise")

	ctx := svc.BuildMemoryContext(context.Background(), "u_admin")
	if ctx == "" {
		t.Error("expected non-empty memory context")
	}
}

func TestService_BuildMemoryContext_Empty(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	ctx := svc.BuildMemoryContext(context.Background(), "u_nobody")
	if ctx != "" {
		t.Error("expected empty memory context for unknown user")
	}
}

func TestService_CrossSessionMemory(t *testing.T) {
	// Simulate session A writing and session B reading
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	// Session A: user states preference
	svc.ExtractAndSave(context.Background(), "u_admin", "t1", "我喜欢用Python")

	// Session B: same user, should read the preference
	ctx := svc.BuildMemoryContext(context.Background(), "u_admin")
	if ctx == "" {
		t.Error("expected cross-session memory to be accessible")
	}
}

func TestService_PutAndGetPreference(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	svc.PutPreference(context.Background(), "u_admin", "preferred_language", "Go")
	value, ok, _ := svc.GetPreference(context.Background(), "u_admin", "preferred_language")
	if !ok || value != "Go" {
		t.Errorf("expected Go, got %s", value)
	}
}

func TestService_DeletePreference(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	svc.PutPreference(context.Background(), "u_admin", "lang", "Go")
	svc.DeletePreference(context.Background(), "u_admin", "lang")

	_, ok, _ := svc.GetPreference(context.Background(), "u_admin", "lang")
	if ok {
		t.Error("expected preference to be deleted")
	}
}

func TestService_ExtractAndSave_WithLLM(t *testing.T) {
	store := NewInMemoryMemoryStore()
	// Pass a mock chatModel that returns structured JSON for preference extraction
	mockModel := &mockPrefExtractModel{}
	svc := NewService(store, nil, nil, mockModel)

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "我喜欢用Python，请简洁回答")
	if err != nil {
		t.Fatalf("extract with LLM failed: %v", err)
	}

	// Verify preferences were saved
	lang, ok, _ := svc.GetPreference(context.Background(), "u_admin", "preferred_language")
	if !ok || lang != "Python" {
		t.Errorf("expected preferred_language=Python, got %s (ok=%v)", lang, ok)
	}

	style, ok, _ := svc.GetPreference(context.Background(), "u_admin", "answer_style")
	if !ok || style != "concise" {
		t.Errorf("expected answer_style=concise, got %s (ok=%v)", style, ok)
	}
}

func TestService_ExtractAndSave_LLMFallbackToRules(t *testing.T) {
	store := NewInMemoryMemoryStore()
	// Pass a mock chatModel that returns invalid JSON — should fall back to rules
	mockModel := &mockPrefExtractModel{invalidResponse: true}
	svc := NewService(store, nil, nil, mockModel)

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "我喜欢用Python")
	if err != nil {
		t.Fatalf("extract with fallback failed: %v", err)
	}

	// Should have fallen back to rule-based extraction
	lang, ok, _ := svc.GetPreference(context.Background(), "u_admin", "preferred_language")
	if !ok || lang != "Python" {
		t.Errorf("expected fallback to extract preferred_language=Python, got %s (ok=%v)", lang, ok)
	}
}

// mockPrefExtractModel is a minimal mock implementing model.BaseChatModel
// for testing LLM-based preference extraction.
type mockPrefExtractModel struct {
	invalidResponse bool
}

func (m *mockPrefExtractModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.invalidResponse {
		return schema.AssistantMessage("I cannot extract preferences", nil), nil
	}
	// Simulate LLM returning a JSON array of preferences
	return schema.AssistantMessage(`[{"key":"preferred_language","value":"Python"},{"key":"answer_style","value":"concise"}]`, nil), nil
}

func (m *mockPrefExtractModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](0)
	go func() {
		sw.Send(msg, nil)
		sw.Close()
	}()
	return sr, nil
}
