package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Service provides long-term memory, short-term checkpoint, and vector retrieval operations.
type Service struct {
	store           MemoryStore
	checkpointStore CheckpointStore      // for conversation state snapshots
	vectorStore     VectorStore          // for semantic memory retrieval
	chatModel       model.BaseChatModel  // 用于 LLM 提取偏好（可为 nil，走规则降级）
}

// NewService creates a new memory service.
// chatModel can be nil, in which case rule-based extraction is used as fallback.
// checkpointStore can be nil, in which case snapshot operations are no-ops.
// vectorStore can be nil, in which case vector retrieval is disabled (KV-only mode).
func NewService(store MemoryStore, checkpointStore CheckpointStore, vectorStore VectorStore, chatModel model.BaseChatModel) *Service {
	return &Service{store: store, checkpointStore: checkpointStore, vectorStore: vectorStore, chatModel: chatModel}
}

// SetChatModel replaces the chat model (used for runtime model switching).
func (s *Service) SetChatModel(chatModel model.BaseChatModel) {
	s.chatModel = chatModel
}

// GetCheckpointStore returns the checkpoint store (for SteppedRunState checkpoint save).
func (s *Service) GetCheckpointStore() CheckpointStore {
	return s.checkpointStore
}

// PutPreference writes a user preference to long-term memory.
func (s *Service) PutPreference(ctx context.Context, userID, key, value string) error {
	now := time.Now().Format(time.RFC3339)
	entry := MemoryEntry{
		UserID:    userID,
		Key:       key,
		Value:     value,
		Source:    "user_stated",
		CreatedAt: now,
		UpdatedAt: now,
	}
	return s.store.Put(ctx, entry)
}

// GetPreference reads a single preference from long-term memory.
func (s *Service) GetPreference(ctx context.Context, userID, key string) (string, bool, error) {
	entry, ok, err := s.store.Get(ctx, userID, key)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return entry.Value, true, nil
}

// ListPreferences returns all preferences for a user.
func (s *Service) ListPreferences(ctx context.Context, userID string) ([]MemoryEntry, error) {
	return s.store.List(ctx, userID)
}

// DeletePreference removes a preference from long-term memory.
func (s *Service) DeletePreference(ctx context.Context, userID, key string) error {
	return s.store.Delete(ctx, userID, key)
}

// ExtractAndSave extracts user preferences from a message and saves them to long-term memory.
// ExtractAndSave extracts user preferences from a message and saves them to long-term memory.
// Strategy: rule-based extraction runs first (precise, deterministic), then LLM extraction
// supplements any preferences the rules missed (broader coverage, but less reliable).
func (s *Service) ExtractAndSave(ctx context.Context, userID, message string) error {
	// 1. Always run rule-based extraction first (precise)
	_ = s.extractWithRules(ctx, userID, message)

	// 2. If chatModel is available, run LLM extraction as supplement
	if s.chatModel != nil {
		extracted, err := s.extractWithLLM(ctx, message)
		if err == nil && len(extracted) > 0 {
			for _, e := range extracted {
				// Only save LLM-extracted entries that don't already exist
				// (rules take priority — don't overwrite rule-extracted values)
				_, exists, _ := s.store.Get(ctx, userID, e.Key)
				if !exists {
					s.PutPreference(ctx, userID, e.Key, e.Value)
				}
			}
		}
	}

	// 3. Store original message to vector store for semantic retrieval
	if s.vectorStore != nil {
		_ = s.vectorStore.Store(ctx, userID, message, map[string]any{
			"timestamp": time.Now().Format(time.RFC3339),
			"type":      "user_message",
		})
	}

	return nil
}

// extractWithLLM calls the ChatModel to extract structured preferences from a user message.
// The LLM returns a JSON array of {key, value} pairs.
func (s *Service) extractWithLLM(ctx context.Context, message string) ([]MemoryEntry, error) {
	prompt := `你是一个用户偏好提取助手。从用户消息中提取偏好信息，以 JSON 数组格式返回。

提取规则：
1. 只提取明确的偏好表达（如"我喜欢X""请用Y""我偏好Z""以后请X"），不提取模糊或无关信息。
2. 如果没有可提取的偏好，返回空数组 []。
3. key 使用英文蛇形命名（如 preferred_language, answer_style, preferred_framework）。
4. value 使用简短明确的值（如 Python, concise, Go）。

常见 key 参考：
- preferred_language: 用户偏好的编程语言
- preferred_framework: 用户偏好的框架
- preferred_editor: 用户偏好的编辑器/IDE
- answer_style: 用户偏好的回答风格（concise/detailed）
- answer_language: 用户偏好的回答语言（Chinese/English）
- preferred_city: 用户所在或默认的城市
- output_format: 用户偏好的输出格式（table/list/code）
- diet_preference: 用户的饮食偏好
- work_style: 用户的工作风格（remote/office/hybrid）
- communication_style: 用户的沟通偏好（formal/casual）

示例：
用户消息："我喜欢用Python写代码，回答请简洁"
返回：[{"key":"preferred_language","value":"Python"},{"key":"answer_style","value":"concise"}]

用户消息："我在北京，用React开发，VSCode写代码"
返回：[{"key":"preferred_city","value":"北京"},{"key":"preferred_framework","value":"React"},{"key":"preferred_editor","value":"VSCode"}]

用户消息：` + message

	resp, err := s.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("你是一个用户偏好提取助手，只返回 JSON 数组，不返回其他内容。"),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return nil, fmt.Errorf("LLM preference extraction failed: %w", err)
	}

	// 解析 LLM 返回的 JSON
	content := strings.TrimSpace(resp.Content)

	// 尝试提取 JSON 数组（LLM 可能返回带 markdown 代码块的）
	jsonStr := extractJSONArray(content)
	if jsonStr == "" {
		return nil, fmt.Errorf("LLM did not return valid JSON array")
	}

	var pairs []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &pairs); err != nil {
		return nil, fmt.Errorf("LLM JSON parse failed: %w", err)
	}

	now := time.Now().Format(time.RFC3339)
	entries := make([]MemoryEntry, 0, len(pairs))
	for _, p := range pairs {
		if p.Key == "" || p.Value == "" {
			continue
		}
		entries = append(entries, MemoryEntry{
			Key:       p.Key,
			Value:     p.Value,
			Source:    "llm_extracted",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	return entries, nil
}

// extractJSONArray tries to extract a JSON array from a string that might contain
// markdown code blocks or other surrounding text.
func extractJSONArray(s string) string {
	// Try to find JSON array between code blocks
	if idx := strings.Index(s, "["); idx != -1 {
		// Find the matching closing bracket
		depth := 0
		for i := idx; i < len(s); i++ {
			if s[i] == '[' {
				depth++
			} else if s[i] == ']' {
				depth--
				if depth == 0 {
					return s[idx : i+1]
				}
			}
		}
	}
	return ""
}

// extractWithRules is the fallback rule-based preference extraction.
func (s *Service) extractWithRules(ctx context.Context, userID, message string) error {
	lower := strings.ToLower(message)

	// Trigger: only extract when user expresses a preference
	hasPreferenceTrigger := strings.Contains(lower, "我喜欢") ||
		strings.Contains(lower, "偏好") ||
		strings.Contains(lower, "喜欢用") ||
		strings.Contains(lower, "prefer") ||
		strings.Contains(lower, "以后请") ||
		strings.Contains(lower, "默认用") ||
		strings.Contains(lower, "请用") ||
		strings.Contains(lower, "用...风格") ||
		strings.Contains(lower, "简洁") ||
		strings.Contains(lower, "简短") ||
		strings.Contains(lower, "详细") ||
		strings.Contains(lower, "详尽")

	if !hasPreferenceTrigger {
		return nil
	}

	// --- Programming language preference ---
	switch {
	case strings.Contains(lower, "python"):
		s.PutPreference(ctx, userID, "preferred_language", "Python")
	case strings.Contains(lower, "java") && !strings.Contains(lower, "javascript"):
		s.PutPreference(ctx, userID, "preferred_language", "Java")
	case strings.Contains(lower, "go") || strings.Contains(lower, "golang"):
		s.PutPreference(ctx, userID, "preferred_language", "Go")
	case strings.Contains(lower, "javascript") || strings.Contains(lower, "js"):
		s.PutPreference(ctx, userID, "preferred_language", "JavaScript")
	case strings.Contains(lower, "typescript") || strings.Contains(lower, "ts"):
		s.PutPreference(ctx, userID, "preferred_language", "TypeScript")
	case strings.Contains(lower, "rust"):
		s.PutPreference(ctx, userID, "preferred_language", "Rust")
	case strings.Contains(lower, "c++") || strings.Contains(lower, "cpp"):
		s.PutPreference(ctx, userID, "preferred_language", "C++")
	case strings.Contains(lower, "c#") || strings.Contains(lower, "csharp"):
		s.PutPreference(ctx, userID, "preferred_language", "C#")
	case strings.Contains(lower, "swift"):
		s.PutPreference(ctx, userID, "preferred_language", "Swift")
	case strings.Contains(lower, "kotlin"):
		s.PutPreference(ctx, userID, "preferred_language", "Kotlin")
	}

	// --- Framework preference ---
	switch {
	case strings.Contains(lower, "react") && !strings.Contains(lower, "vue"):
		s.PutPreference(ctx, userID, "preferred_framework", "React")
	case strings.Contains(lower, "vue"):
		s.PutPreference(ctx, userID, "preferred_framework", "Vue")
	case strings.Contains(lower, "angular"):
		s.PutPreference(ctx, userID, "preferred_framework", "Angular")
	case strings.Contains(lower, "spring"):
		s.PutPreference(ctx, userID, "preferred_framework", "Spring")
	case strings.Contains(lower, "django"):
		s.PutPreference(ctx, userID, "preferred_framework", "Django")
	case strings.Contains(lower, "flask"):
		s.PutPreference(ctx, userID, "preferred_framework", "Flask")
	case strings.Contains(lower, "gin") && !strings.Contains(lower, "begin"):
		s.PutPreference(ctx, userID, "preferred_framework", "Gin")
	case strings.Contains(lower, "fib") || strings.Contains(lower, "fiber"):
		s.PutPreference(ctx, userID, "preferred_framework", "Fiber")
	}

	// --- Editor/IDE preference ---
	switch {
	case strings.Contains(lower, "vscode") || strings.Contains(lower, "vs code"):
		s.PutPreference(ctx, userID, "preferred_editor", "VSCode")
	case strings.Contains(lower, "vim") || strings.Contains(lower, "neovim"):
		s.PutPreference(ctx, userID, "preferred_editor", "Vim")
	case strings.Contains(lower, "emacs"):
		s.PutPreference(ctx, userID, "preferred_editor", "Emacs")
	case strings.Contains(lower, "idea") || strings.Contains(lower, "intellij"):
		s.PutPreference(ctx, userID, "preferred_editor", "IntelliJ IDEA")
	case strings.Contains(lower, "pycharm"):
		s.PutPreference(ctx, userID, "preferred_editor", "PyCharm")
	case strings.Contains(lower, "goland"):
		s.PutPreference(ctx, userID, "preferred_editor", "GoLand")
	}

	// --- Answer style preference ---
	if strings.Contains(lower, "简洁") || strings.Contains(lower, "简短") {
		s.PutPreference(ctx, userID, "answer_style", "concise")
	}
	if strings.Contains(lower, "详细") || strings.Contains(lower, "详尽") {
		s.PutPreference(ctx, userID, "answer_style", "detailed")
	}

	// --- Answer language preference ---
	switch {
	case strings.Contains(lower, "用中文回答") || strings.Contains(lower, "中文回复"):
		s.PutPreference(ctx, userID, "answer_language", "Chinese")
	case strings.Contains(lower, "用英文回答") || strings.Contains(lower, "英文回复") || strings.Contains(lower, "answer in english"):
		s.PutPreference(ctx, userID, "answer_language", "English")
	}

	// --- City/location preference ---
	cities := []string{"北京", "上海", "深圳", "广州", "杭州", "成都", "武汉", "南京", "苏州", "西安"}
	for _, city := range cities {
		if strings.Contains(lower, "默认城市") || strings.Contains(lower, "所在城市") || strings.Contains(lower, "我在") {
			if strings.Contains(lower, city) {
				s.PutPreference(ctx, userID, "preferred_city", city)
				break
			}
		}
	}

	// --- Output format preference ---
	switch {
	case strings.Contains(lower, "表格形式") || strings.Contains(lower, "表格展示"):
		s.PutPreference(ctx, userID, "output_format", "table")
	case strings.Contains(lower, "列表形式") || strings.Contains(lower, "列表展示"):
		s.PutPreference(ctx, userID, "output_format", "list")
	case strings.Contains(lower, "代码形式") || strings.Contains(lower, "代码展示"):
		s.PutPreference(ctx, userID, "output_format", "code")
	}

	return nil
}

// BuildMemoryContext builds a text representation of the user's preferences
// for injection into the system prompt.
// BuildMemoryContext builds a text representation of user memories for system prompt injection.
// It combines KV preferences (precise) and vector-retrieved semantic memories (fuzzy).
// The userID parameter is used as the namespace for both stores.
func (s *Service) BuildMemoryContext(ctx context.Context, userID string) string {
	var parts []string

	// 1. KV preferences (precise, structured)
	entries, err := s.store.List(ctx, userID)
	if err == nil && len(entries) > 0 {
		var sb strings.Builder
		sb.WriteString("用户偏好信息：\n")
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", e.Key, e.Value))
		}
		parts = append(parts, sb.String())
	}

	// 2. Vector-retrieved semantic memories (fuzzy, contextual)
	if s.vectorStore != nil {
		// Use a generic query to find relevant memories
		// We don't have the user's current message here, so we retrieve
		// recent/salient memories instead. The runner will call QueryWithMessage
		// for query-specific retrieval.
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// QueryVectorMemory performs a semantic search on the user's memory and returns
// formatted results for system prompt injection.
func (s *Service) QueryVectorMemory(ctx context.Context, userID, query string, topK int) string {
	if s.vectorStore == nil {
		return ""
	}

	results, err := s.vectorStore.Query(ctx, userID, query, topK)
	if err != nil || len(results) == 0 {
		return ""
	}

	return FormatVectorResults(results)
}

// SaveSnapshot saves a conversation state snapshot to the CheckpointStore.
// This enables crash recovery and session resumption.
func (s *Service) SaveSnapshot(ctx context.Context, userID, threadID, runID string, messages []byte) error {
	if s.checkpointStore == nil {
		return nil
	}
	now := time.Now()
	return s.checkpointStore.Save(ctx, Checkpoint{
		UserID:      userID,
		ThreadID:    threadID,
		RunID:       runID,
		State:       messages,
		Interrupted: false,
		Snapshot:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}

// LoadSnapshot loads the latest conversation snapshot for a thread.
// Returns the checkpoint and true if found, or zero value and false if not.
func (s *Service) LoadSnapshot(ctx context.Context, userID, threadID string) (Checkpoint, bool, error) {
	if s.checkpointStore == nil {
		return Checkpoint{}, false, nil
	}
	checkpoints, err := s.checkpointStore.ListByThread(ctx, userID, threadID)
	if err != nil {
		return Checkpoint{}, false, err
	}
	// Find the latest snapshot
	var latest Checkpoint
	found := false
	for _, cp := range checkpoints {
		if cp.Snapshot && cp.UpdatedAt.After(latest.UpdatedAt) {
			latest = cp
			found = true
		}
	}
	return latest, found, nil
}
