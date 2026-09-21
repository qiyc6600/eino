package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Service provides long-term memory, short-term checkpoint, and vector retrieval operations.
type Service struct {
	store           MemoryStore
	checkpointStore CheckpointStore     // for conversation state snapshots
	vectorStore     VectorStore         // for semantic memory retrieval
	docStore        VectorStore         // for document chunk retrieval (separate index)
	chatModel       model.BaseChatModel // 用于 LLM 提取偏好（可为 nil，走规则降级）

	budgetTokens         int     // token budget for RetrieveRelevant injection
	documentBudgetTokens int     // token budget for the document section (0 = disabled)
	vectorMinScore       float64 // relevance cut-off; its scale depends on the embedder
	consolidateThreshold int     // active entries before consolidation pays off

	// indexMu guards indexedUsers. The vector backends are in-process only, so
	// the index is rebuilt from the KV store the first time a user needs it.
	indexMu      sync.Mutex
	indexedUsers map[string]bool
}

// NewService creates a new memory service.
// chatModel can be nil, in which case rule-based extraction is used as fallback.
// checkpointStore can be nil, in which case snapshot operations are no-ops.
// vectorStore can be nil, in which case vector retrieval is disabled (KV-only mode).
func NewService(store MemoryStore, checkpointStore CheckpointStore, vectorStore VectorStore, chatModel model.BaseChatModel) *Service {
	return &Service{
		store:                store,
		checkpointStore:      checkpointStore,
		vectorStore:          vectorStore,
		chatModel:            chatModel,
		budgetTokens:         DefaultBudgetTokens,
		documentBudgetTokens: DefaultDocumentBudgetTokens,
		consolidateThreshold: DefaultConsolidateThresh,
		indexedUsers:         make(map[string]bool),
	}
}

// SetDocumentStore attaches the index used for document chunks. It is a separate
// store from the memory index on purpose: sharing one per-user collection would
// rank document chunks against memory episodes and let either crowd the other out
// of the top-K.
func (s *Service) SetDocumentStore(vs VectorStore) {
	s.docStore = vs
}

// SetChatModel replaces the chat model (used for runtime model switching).
func (s *Service) SetChatModel(chatModel model.BaseChatModel) {
	s.chatModel = chatModel
}

// GetCheckpointStore returns the checkpoint store (for SteppedRunState checkpoint save).
func (s *Service) GetCheckpointStore() CheckpointStore {
	return s.checkpointStore
}

// EntryMeta carries optional metadata for memory upserts.
type EntryMeta struct {
	Type       string // MemoryType* constant; empty = preference
	Importance int    // 1-5; 0 = DefaultImportance
	Source     string // user_stated | llm_extracted | consolidated; empty = user_stated
	ThreadID   string // conversation provenance
	Excerpt    string // truncated original message
}

// UpsertPreference writes a preference with conflict resolution: when the key
// already exists with a different value, the old value is archived into the
// entry's history (capped) instead of being silently dropped. Identical
// values are a no-op.
func (s *Service) UpsertPreference(ctx context.Context, userID, key, value string, meta EntryMeta) error {
	// Framework entries are written through their own typed setters, never by
	// extraction or the memory API.
	if IsReservedKey(key) {
		return fmt.Errorf("保留键不可通过记忆接口写入：%s", key)
	}
	now := time.Now().Format(time.RFC3339)

	existing, ok, err := s.store.Get(ctx, userID, key)
	if err != nil {
		return err
	}

	if ok {
		if existing.Value == value {
			return nil // unchanged — nothing to record
		}
		// Archive the superseded value (newest first, capped).
		rev := ValueRevision{Value: existing.Value, Source: existing.Source, SupersededAt: now}
		existing.History = append([]ValueRevision{rev}, existing.History...)
		if len(existing.History) > MaxValueHistory {
			existing.History = existing.History[:MaxValueHistory]
		}
		existing.Value = value
		existing.UpdatedAt = now
		if meta.Type != "" {
			existing.Type = meta.Type
		}
		if meta.Importance > 0 {
			existing.Importance = meta.Importance
		}
		if meta.Source != "" {
			existing.Source = meta.Source
		}
		if meta.ThreadID != "" {
			existing.SourceThreadID = meta.ThreadID
		}
		if meta.Excerpt != "" {
			existing.SourceExcerpt = meta.Excerpt
		}
		return s.store.Put(ctx, existing)
	}

	entry := MemoryEntry{
		UserID:         userID,
		Key:            key,
		Value:          value,
		Source:         meta.Source,
		Type:           meta.Type,
		Importance:     meta.Importance,
		SourceThreadID: meta.ThreadID,
		SourceExcerpt:  meta.Excerpt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return s.store.Put(ctx, entry)
}

// PutPreference writes a user preference (simple form, upsert semantics).
//
// It writes at core importance: a memory the user created by hand is itself the
// statement that it matters, and the API exposes no importance field, so without
// this every hand-written entry would sit at the default and be the first thing
// crowded out by query-matching episodes.
func (s *Service) PutPreference(ctx context.Context, userID, key, value string) error {
	return s.UpsertPreference(ctx, userID, key, value, EntryMeta{
		Source:     "user_stated",
		Importance: corePreferenceImportance,
	})
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

// ListPreferences returns all user-visible preferences for a user. Framework
// entries (reserved keys) are internal bookkeeping and never listed.
func (s *Service) ListPreferences(ctx context.Context, userID string) ([]MemoryEntry, error) {
	entries, err := s.store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	visible := make([]MemoryEntry, 0, len(entries))
	for _, e := range entries {
		if IsReservedKey(e.Key) {
			continue
		}
		visible = append(visible, e)
	}
	return visible, nil
}

// DeletePreference removes a preference from long-term memory. Reserved keys are
// refused so the memory API cannot corrupt framework state.
func (s *Service) DeletePreference(ctx context.Context, userID, key string) error {
	if IsReservedKey(key) {
		return fmt.Errorf("保留键不可通过记忆接口修改：%s", key)
	}
	return s.store.Delete(ctx, userID, key)
}

// ExtractAndSave extracts memory from a message and saves it to long-term memory.
// Strategy (LLM-led with rule fallback):
//  1. When a chat model is configured it owns the turn: entries are upserted
//     with conflict resolution, after a light anti-hallucination check for
//     enumerable keys (the value must appear in the message text).
//  2. Rule-based extraction runs only as fallback — no model configured, or
//     the model call/parse failed (e.g. the mock model) — keeping extraction
//     deterministic and demoable without a real LLM.
//  3. Messages that actually carry memory value are additionally stored as
//     episodes (KV + vector index); plain chit-chat is never indexed.
//
// A user who turned memory off ("do not remember") is never extracted from.
func (s *Service) ExtractAndSave(ctx context.Context, userID, threadID, message string) error {
	if !s.MemoryEnabled(ctx, userID) {
		return nil
	}
	excerpt := truncateExcerpt(message, 100)

	// 1. LLM extraction owns the turn when the model is usable.
	var llmEntries []MemoryEntry
	llmOwned := false
	if s.chatModel != nil {
		extracted, err := s.extractWithLLM(ctx, message)
		if err == nil {
			llmOwned = true
			for _, e := range extracted {
				if !valueSupportedByMessage(e.Key, e.Value, message) {
					continue // hallucinated value for a closed-list key
				}
				if err := s.UpsertPreference(ctx, userID, e.Key, e.Value, EntryMeta{
					Type: e.Type, Importance: e.Importance,
					Source: "llm_extracted", ThreadID: threadID, Excerpt: excerpt,
				}); err == nil {
					llmEntries = append(llmEntries, e)
				}
			}
		}
	}

	// 2. Rule fallback: no model, or the model path failed (mock model etc.).
	var written []string
	if !llmOwned {
		written, _ = s.extractWithRules(ctx, userID, threadID, excerpt, message)
	}

	// 3. Episode indexing: only messages with real memory value reach the
	//    stores (a turn the LLM owned but found nothing in is not indexed).
	if len(llmEntries) > 0 || len(written) > 0 || hasPreferenceSignal(message) {
		s.storeEpisode(ctx, userID, threadID, message)
	}

	return nil
}

// enumerableMemoryKeys are keys whose values come from a closed list
// (language/framework/editor/city names). The LLM may only assert them when
// the value verbatim appears in the message — a cheap anti-hallucination
// guard for exactly the keys where fabrication is most likely.
var enumerableMemoryKeys = map[string]bool{
	"preferred_language":  true,
	"preferred_framework": true,
	"preferred_editor":    true,
	"preferred_city":      true,
}

// valueSupportedByMessage reports whether an extracted entry is supported by the
// message text.
//
//   - Closed-list keys (language/framework/editor/city): the value must appear
//     verbatim — fabrication is most likely exactly there.
//   - Presentation keys (style/format): the message must carry a persistence
//     marker. Without this the model can turn "请详细说明这个函数" — a request
//     about the current answer — into a standing preference, which is the same
//     defect the rule path guards against.
//   - Everything else passes: those values are paraphrases, not verbatim quotes.
func valueSupportedByMessage(key, value, message string) bool {
	if enumerableMemoryKeys[key] {
		return strings.Contains(strings.ToLower(message), strings.ToLower(value))
	}
	if persistentMemoryKeys[key] {
		return hasStandingMarker(message)
	}
	return true
}

// storeEpisode records a valuable message as an episode entry (source of
// truth, listable for consolidation) and indexes it in the vector store
// (semantic retrieval).
func (s *Service) storeEpisode(ctx context.Context, userID, threadID, message string) {
	now := time.Now()
	excerpt := truncateExcerpt(message, 200)
	key := "ep_" + now.UTC().Format("20060102150405")

	// Episode entry in the KV store (listable, consolidatable).
	_ = s.UpsertPreference(ctx, userID, key, excerpt, EntryMeta{
		Type: MemoryTypeEpisode, Importance: 2,
		Source: "episode", ThreadID: threadID, Excerpt: excerpt,
	})

	// Vector index for semantic retrieval.
	if s.vectorStore != nil {
		_ = s.vectorStore.Store(ctx, userID, excerpt, map[string]any{
			"timestamp": now.Format(time.RFC3339),
			"type":      "episode",
			"thread_id": threadID,
			"key":       key,
		})
	}
}

// hasPreferenceSignal reports whether a message plausibly carries memory
// value (preference/identity statements). Used to filter episode indexing.
func hasPreferenceSignal(message string) bool {
	lower := strings.ToLower(message)
	signals := []string{
		"我喜欢", "我偏好", "我喜欢用", "以后请", "默认用", "请用", "请直接",
		"我是", "我在", "我的", "记住", "prefer", "always", "default",
	}
	for _, s := range signals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// truncateExcerpt cuts a string to at most max runes with an ellipsis.
func truncateExcerpt(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// extractWithLLM calls the ChatModel to extract structured preferences from a user message.
// The LLM returns a JSON array of {key, value, type, importance} objects.
func (s *Service) extractWithLLM(ctx context.Context, message string) ([]MemoryEntry, error) {
	prompt := `你是一个用户记忆提取助手。从用户消息中提取值得长期记住的信息，以 JSON 数组格式返回。

提取规则：
1. 只提取明确的偏好、身份或事实表达（如"我喜欢X""请用Y""我在Z""记住W"），不提取模糊或无关信息。
2. 如果没有可提取的信息，返回空数组 []。
3. key 使用英文蛇形命名（如 preferred_language, answer_style, preferred_framework）。
4. value 使用简短明确的值（如 Python, concise, Go）。
5. type 从以下选一：preference（偏好）/ identity（身份）/ fact（事实）/ rule（用户给的长期规则）。
6. importance 为 1-5 整数：5=核心身份或强烈偏好，3=一般偏好，1=边缘信息。

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
返回：[{"key":"preferred_language","value":"Python","type":"preference","importance":4},{"key":"answer_style","value":"concise","type":"preference","importance":3}]

用户消息："以后请都用Go，这是团队规范"
返回：[{"key":"preferred_language","value":"Go","type":"rule","importance":5}]

用户消息：` + message

	resp, err := s.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("你是一个用户记忆提取助手，只返回 JSON 数组，不返回其他内容。"),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return nil, fmt.Errorf("LLM memory extraction failed: %w", err)
	}

	// 解析 LLM 返回的 JSON
	content := strings.TrimSpace(resp.Content)

	// 尝试提取 JSON 数组（LLM 可能返回带 markdown 代码块的）
	jsonStr := extractJSONArray(content)
	if jsonStr == "" {
		return nil, fmt.Errorf("LLM did not return valid JSON array")
	}

	var pairs []struct {
		Key        string `json:"key"`
		Value      string `json:"value"`
		Type       string `json:"type"`
		Importance int    `json:"importance"`
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
			Key:        p.Key,
			Value:      p.Value,
			Type:       p.Type,
			Importance: p.Importance,
			Source:     "llm_extracted",
			CreatedAt:  now,
			UpdatedAt:  now,
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

// standingMarkers express persistence — "from now on", "by default", "always".
// They are what separates a standing preference from a request about the answer
// the user is reading right now.
//
// Only explicit persistence counts. "请用简洁的方式回答" and "请简洁回答" mean
// the same thing as each other — "make this answer concise" — so neither writes
// a preference; "以后请用简洁的方式回答" does. That line is drawn here because
// the data model has no per-conversation scope: the only scope an entry can
// have is the whole user, so only an explicit request for the whole user is
// recorded. Widening this list is how the extractor starts rewriting a user's
// profile from a passing remark.
var standingMarkers = []string{
	"以后", "今后", "默认", "一直", "始终", "每次", "都要", "记住",
	"我喜欢", "我偏好", "喜欢用", "偏好",
	"prefer", "always", "by default", "from now on",
}

// presentationWords name how an answer should look. On their own they are
// requests about the current answer, not preferences: "请详细说明这个函数"
// means "expand this one", and persisting it would rewrite the user's profile
// for every future conversation. They only become a preference alongside a
// standing marker ("以后请用简洁的方式回答").
var presentationWords = []string{"简洁", "简短", "详细", "详尽", "具体", "展开"}

// persistentMemoryKeys are keys whose value describes a standing preference
// about presentation. The LLM path must see a standing marker for these, for
// the same reason the rule path requires one.
var persistentMemoryKeys = map[string]bool{
	"answer_style":        true,
	"output_format":       true,
	"communication_style": true,
}

// hasStandingMarker reports whether the message expresses persistence.
func hasStandingMarker(message string) bool {
	lower := strings.ToLower(message)
	for _, m := range standingMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// extractWithRules is the fallback rule-based preference extraction.
// Returns the keys written during this call so the LLM pass can skip them.
func (s *Service) extractWithRules(ctx context.Context, userID, threadID, excerpt, message string) ([]string, error) {
	lower := strings.ToLower(message)

	// A standing marker is required for everything this extractor writes. A
	// presentation word alone is not enough — see presentationWords.
	standing := hasStandingMarker(message)
	if !standing {
		return nil, nil
	}

	// write records one rule-extracted preference with provenance metadata.
	var written []string
	write := func(key, value string, importance int) {
		if err := s.UpsertPreference(ctx, userID, key, value, EntryMeta{
			Type:       MemoryTypePreference,
			Importance: importance,
			Source:     "user_stated",
			ThreadID:   threadID,
			Excerpt:    excerpt,
		}); err == nil {
			written = append(written, key)
		}
	}

	// --- Programming language preference ---
	switch {
	case strings.Contains(lower, "python"):
		write("preferred_language", "Python", 4)
	case strings.Contains(lower, "java") && !strings.Contains(lower, "javascript"):
		write("preferred_language", "Java", 4)
	case strings.Contains(lower, "go") || strings.Contains(lower, "golang"):
		write("preferred_language", "Go", 4)
	case strings.Contains(lower, "javascript") || strings.Contains(lower, "js"):
		write("preferred_language", "JavaScript", 4)
	case strings.Contains(lower, "typescript") || strings.Contains(lower, "ts"):
		write("preferred_language", "TypeScript", 4)
	case strings.Contains(lower, "rust"):
		write("preferred_language", "Rust", 4)
	case strings.Contains(lower, "c++") || strings.Contains(lower, "cpp"):
		write("preferred_language", "C++", 4)
	case strings.Contains(lower, "c#") || strings.Contains(lower, "csharp"):
		write("preferred_language", "C#", 4)
	case strings.Contains(lower, "swift"):
		write("preferred_language", "Swift", 4)
	case strings.Contains(lower, "kotlin"):
		write("preferred_language", "Kotlin", 4)
	}

	// --- Framework preference ---
	switch {
	case strings.Contains(lower, "react") && !strings.Contains(lower, "vue"):
		write("preferred_framework", "React", 4)
	case strings.Contains(lower, "vue"):
		write("preferred_framework", "Vue", 4)
	case strings.Contains(lower, "angular"):
		write("preferred_framework", "Angular", 4)
	case strings.Contains(lower, "spring"):
		write("preferred_framework", "Spring", 4)
	case strings.Contains(lower, "django"):
		write("preferred_framework", "Django", 4)
	case strings.Contains(lower, "flask"):
		write("preferred_framework", "Flask", 4)
	case strings.Contains(lower, "gin") && !strings.Contains(lower, "begin"):
		write("preferred_framework", "Gin", 4)
	case strings.Contains(lower, "fib") || strings.Contains(lower, "fiber"):
		write("preferred_framework", "Fiber", 4)
	}

	// --- Editor/IDE preference ---
	switch {
	case strings.Contains(lower, "vscode") || strings.Contains(lower, "vs code"):
		write("preferred_editor", "VSCode", 4)
	case strings.Contains(lower, "vim") || strings.Contains(lower, "neovim"):
		write("preferred_editor", "Vim", 4)
	case strings.Contains(lower, "emacs"):
		write("preferred_editor", "Emacs", 4)
	case strings.Contains(lower, "idea") || strings.Contains(lower, "intellij"):
		write("preferred_editor", "IntelliJ IDEA", 4)
	case strings.Contains(lower, "pycharm"):
		write("preferred_editor", "PyCharm", 4)
	case strings.Contains(lower, "goland"):
		write("preferred_editor", "GoLand", 4)
	}

	// --- Answer style preference ---
	// Two conditions, both required: the function already returned unless a
	// standing marker is present, and a presentation word must be present too.
	// That is exactly what makes "以后请用简洁的方式回答" a preference and
	// "请详细说明这个函数" not one.
	for _, w := range presentationWords {
		if !strings.Contains(lower, w) {
			continue
		}
		switch w {
		case "简洁", "简短":
			write("answer_style", "concise", 3)
		case "详细", "详尽":
			write("answer_style", "detailed", 3)
		}
	}

	// --- Answer language preference ---
	switch {
	case strings.Contains(lower, "用中文回答") || strings.Contains(lower, "中文回复"):
		write("answer_language", "Chinese", 3)
	case strings.Contains(lower, "用英文回答") || strings.Contains(lower, "英文回复") || strings.Contains(lower, "answer in english"):
		write("answer_language", "English", 3)
	}

	// --- City/location preference ---
	cities := []string{"北京", "上海", "深圳", "广州", "杭州", "成都", "武汉", "南京", "苏州", "西安"}
	for _, city := range cities {
		if strings.Contains(lower, "默认城市") || strings.Contains(lower, "所在城市") || strings.Contains(lower, "我在") {
			if strings.Contains(lower, city) {
				write("preferred_city", city, 3)
				break
			}
		}
	}

	// --- Output format preference ---
	switch {
	case strings.Contains(lower, "表格形式") || strings.Contains(lower, "表格展示"):
		write("output_format", "table", 3)
	case strings.Contains(lower, "列表形式") || strings.Contains(lower, "列表展示"):
		write("output_format", "list", 3)
	case strings.Contains(lower, "代码形式") || strings.Contains(lower, "代码展示"):
		write("output_format", "code", 3)
	}

	return written, nil
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
// formatted results for system prompt injection, skipping entries that would
// exceed maxTokens (0 = no limit).
func (s *Service) QueryVectorMemory(ctx context.Context, userID, query string, topK, maxTokens int) string {
	if s.vectorStore == nil {
		return ""
	}

	results, err := s.vectorStore.Query(ctx, userID, query, topK)
	if err != nil || len(results) == 0 {
		return ""
	}

	return FormatVectorResultsWithin(results, maxTokens, s.vectorMinScore)
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
