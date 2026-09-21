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

// TestService_ExtractAndSave_Style covers a presentation preference stated with
// persistence. The phrasing matters: only "以后请…" asks for a standing change.
// See TestService_ExtractAndSave_TransientRequestsDoNotPersist for the other side.
func TestService_ExtractAndSave_Style(t *testing.T) {
	store := NewInMemoryMemoryStore()
	svc := NewService(store, nil, nil, nil)

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "以后请简洁回答")
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

	err := svc.ExtractAndSave(context.Background(), "u_admin", "t1", "以后请详细回答")
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	value, ok, _ := svc.GetPreference(context.Background(), "u_admin", "answer_style")
	if !ok || value != "detailed" {
		t.Errorf("expected answer_style=detailed, got %s (ok=%v)", value, ok)
	}
}

// TestService_ExtractAndSave_TransientRequestsDoNotPersist is the guard on the
// other side of the line, and the more important one.
//
// Every message here is a request about the answer the user is reading. None
// carries a persistence marker, so none may write a standing preference: a
// single "这个回答太长了" used to set answer_style=concise permanently, for every
// future conversation. Repeating them the other way ("简洁点", then "详细讲讲")
// made the profile oscillate with each turn's wording.
func TestService_ExtractAndSave_TransientRequestsDoNotPersist(t *testing.T) {
	cases := []string{
		"请详细说明这个函数的作用",
		"这个回答太长了，请简短一些",
		"简洁点",
		"帮我详细讲讲这段代码",
		"请用简洁的方式回答", // an instruction about this answer, not a standing change
		"展开说说",
	}
	for _, msg := range cases {
		store := NewInMemoryMemoryStore()
		svc := NewService(store, nil, nil, nil)
		ctx := context.Background()

		if err := svc.ExtractAndSave(ctx, "u1", "t1", msg); err != nil {
			t.Fatalf("extract %q: %v", msg, err)
		}
		if _, ok, _ := svc.GetPreference(ctx, "u1", "answer_style"); ok {
			t.Errorf("%q must not persist a standing style preference", msg)
		}
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
