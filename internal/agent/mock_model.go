package agent

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/contextmgr"
)

// MockChatModel implements model.ToolCallingChatModel for demo purposes.
// It satisfies Eino's interface so it can be used with react.NewAgent.
// When MODEL_PROVIDER=mock, this is used instead of OpenAI/Ark.
type MockChatModel struct {
	boundTools []*schema.ToolInfo // tools bound via WithTools
}

// NewMockChatModel creates a new MockChatModel.
func NewMockChatModel() *MockChatModel {
	return &MockChatModel{}
}

// Generate implements model.BaseChatModel.Generate.
func (m *MockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if len(input) == 0 {
		return schema.AssistantMessage("您好，请问有什么可以帮您的？", nil), nil
	}

	// Find the last user message
	lastUserMsg := ""
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Role == schema.User {
			lastUserMsg = input[i].Content
			break
		}
	}

	// If the last non-system message has tool results, generate a summary
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Role == schema.System {
			continue
		}
		if input[i].Role == schema.Tool {
			summary := summarizeToolResults(input)
			return schema.AssistantMessage(summary, nil), nil
		}
		break
	}

	if lastUserMsg == "" {
		return schema.AssistantMessage("您好，请问有什么可以帮您的？", nil), nil
	}

	// Handle summary prompts from the context manager.
	// When Summarizer calls LLM for compression, detect the summary prompt pattern
	// and return a structured summary. This satisfies the task requirement of
	// "使用 LLM 进行摘要压缩" even in mock mode.
	if isSummaryRequest(input) {
		summary := generateSummary(input)
		return schema.AssistantMessage(summary, nil), nil
	}

	// A memory-extraction prompt from the memory service is deliberately NOT
	// special-cased: the mock answers it as ordinary chat, the service cannot
	// parse the reply as JSON, and extraction falls back to its rule matcher.
	// That is the documented mock-mode behaviour, and it keeps the mock from
	// pretending to do extraction it cannot really do — a hand-written matcher
	// here would silently disagree with the real model's judgement (and did:
	// "javascript" matched a bare "java", "django" matched a bare "go").
	// A previous unreachable implementation of exactly that is why this note
	// exists.

	// Parse system prompt for user preferences
	preferredLang := preferredLanguageFromPrompt(input)

	// Try to match a tool call
	lower := toLower(lastUserMsg)
	toolCall := matchToolCall(lower, lastUserMsg, m.boundTools)

	if toolCall != nil {
		// Return assistant message with tool calls — Eino ReAct loop will dispatch to ToolsNode
		return &schema.Message{
			Role:      schema.Assistant,
			Content:   "",
			ToolCalls: []schema.ToolCall{*toolCall},
		}, nil
	}

	// No tool call matched — generate a final text answer
	answer := generateFinalAnswer(lower, lastUserMsg, preferredLang)
	return schema.AssistantMessage(answer, nil), nil
}

// mockStreamChunkRunes is how many runes each simulated stream chunk carries.
// Small enough that the UI shows a typing effect, large enough that a long
// answer does not turn into hundreds of SSE frames.
const mockStreamChunkRunes = 6

// mockStreamChunkDelay paces simulated chunks so the typing effect is visible
// in the browser. Zero in tests keeps the suite fast.
var mockStreamChunkDelay = 8 * time.Millisecond

// Stream implements model.BaseChatModel.Stream. The mock replays the answer it
// would have generated as several content chunks so streaming can be exercised
// without a real provider; tool calls are delivered in a single chunk because
// their arguments are not meaningful to split.
func (m *MockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	usage := mockUsageFor(input, msg)
	if msg == nil || msg.Content == "" {
		if msg != nil {
			msg.ResponseMeta = &schema.ResponseMeta{Usage: usage}
		}
		return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
	}

	runes := []rune(msg.Content)
	chunks := make([]*schema.Message, 0, len(runes)/mockStreamChunkRunes+1)
	for start := 0; start < len(runes); start += mockStreamChunkRunes {
		end := start + mockStreamChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunk := schema.AssistantMessage(string(runes[start:end]), nil)
		if len(chunks) == 0 {
			// ConcatMessages merges usage across chunks, so one carrier suffices.
			chunk.ResponseMeta = &schema.ResponseMeta{Usage: usage}
		}
		chunks = append(chunks, chunk)
	}

	// A real piped stream, not a pre-built array: the pacing has to happen while
	// the consumer reads, otherwise every chunk is delivered in one burst and the
	// UI never shows a typing effect.
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer writer.Close()
		for _, chunk := range chunks {
			if mockStreamChunkDelay > 0 {
				select {
				case <-ctx.Done():
					writer.Send(nil, ctx.Err())
					return
				case <-time.After(mockStreamChunkDelay):
				}
			}
			if writer.Send(chunk, nil) {
				return // consumer went away
			}
		}
	}()
	return reader, nil
}

// mockUsageFor reports a plausible token count so the provider-usage path is
// exercised without a real provider. It uses the same heuristic as the local
// counter, which is what a demo needs: numbers that look right and move.
func mockUsageFor(input []*schema.Message, output *schema.Message) *schema.TokenUsage {
	counter := contextmgr.NewSimpleTokenCounter()
	prompt := counter.CountMessages(einoToContextMessages(input))
	completion := 0
	if output != nil {
		completion = counter.CountMessages(einoToContextMessages([]*schema.Message{output}))
	}
	return &schema.TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

// WithTools implements model.ToolCallingChatModel.WithTools.
// Returns a new MockChatModel with the given tools bound (immutable pattern).
func (m *MockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newM := &MockChatModel{
		boundTools: make([]*schema.ToolInfo, len(tools)),
	}
	copy(newM.boundTools, tools)
	return newM, nil
}

// ---- Tool matching logic ----

func matchToolCall(lower, raw string, boundTools []*schema.ToolInfo) *schema.ToolCall {
	// Build a set of available tool names for quick lookup
	available := make(map[string]bool)
	for _, t := range boundTools {
		available[t.Name] = true
	}

	type match struct {
		toolName string
		argFunc  func(string) string
	}

	// Order matters: sub-agent routes first (priority when bound), then real tools
	matches := []match{
		// Sub-agent routes (take priority when bound via WithTools)
		{"math_agent", func(s string) string {
			return fmt.Sprintf(`{"message":"%s"}`, raw)
		}},
		{"search_agent", func(s string) string {
			return fmt.Sprintf(`{"message":"%s"}`, raw)
		}},
		{"general_agent", func(s string) string {
			return fmt.Sprintf(`{"message":"%s"}`, raw)
		}},
		// The MCP sub-agent passes the request straight through, like the other
		// sub-agents: the remote tool decides what its arguments mean.
		{"mcp_agent", func(s string) string {
			return fmt.Sprintf(`{"message":"%s"}`, raw)
		}},
		// Real tools (used when sub-agents are not bound)
		{"read_notes", func(s string) string {
			return fmt.Sprintf(`{"id":"%s"}`, extractNoteID(s))
		}},
		{"delete_note", func(s string) string {
			return fmt.Sprintf(`{"id":"%s"}`, extractNoteID(s))
		}},
		{"delete_order", func(s string) string {
			id := extractOrderID(s)
			if id == "" {
				id = "A-1001"
			}
			return fmt.Sprintf(`{"order_id":"%s"}`, id)
		}},
		{"send_email", func(s string) string {
			return `{"to":"admin@example.com","subject":"通知","body":"测试邮件内容"}`
		}},
		{"calculator", func(s string) string {
			return fmt.Sprintf(`{"expression":"%s"}`, extractMathExpr(s))
		}},
		{"weather", func(s string) string {
			return fmt.Sprintf(`{"city":"%s"}`, extractCity(s))
		}},
		{"grep", func(s string) string {
			return fmt.Sprintf(`{"pattern":"%s"}`, extractPattern(s))
		}},
		{"query_order", func(s string) string {
			id := extractOrderID(s)
			if id != "" {
				return fmt.Sprintf(`{"order_id":"%s"}`, id)
			}
			return `{}`
		}},
	}

	// Keyword mapping
	keywords := map[string][]string{
		// Sub-agent routes (priority when bound via WithTools)
		"math_agent":    {"计算", "等于多少", "加", "减", "乘", "除", "×", "÷", "*"},
		"search_agent":  {"天气", "气温", "温度", "搜索日志", "查日志", "grep", "搜索error", "搜索错误", "定位异常"},
		"general_agent": {"删除订单", "删掉订单", "取消订单", "发邮件", "发送邮件", "发一封", "订单", "查我的", "查询订单", "我的订单"},
		// The MCP sub-agent owns whatever tools an external server exposes, so the
		// mock cannot key on tool names it does not know. It routes on the demo
		// server's vocabulary instead; a real model routes by the agent
		// description. Without this the offline demo could never reach an
		// external tool.
		"mcp_agent": {"笔记", "notes", "read_notes", "delete_note"},
		// Tools of the demo MCP server (cmd/mcp-demo-server). The longest matching
		// keyword wins, so "删除笔记" beats read_notes' plain "笔记".
		"read_notes":  {"笔记", "notes", "读取笔记"},
		"delete_note": {"删除笔记", "删掉笔记"},
		// Real tools (fallback when sub-agents not bound)
		"delete_order": {"删除订单", "删掉订单", "取消订单"},
		"send_email":   {"发邮件", "发送邮件", "发一封"},
		"calculator":   {"计算", "等于多少", "加", "减", "乘", "除", "×", "÷", "*"},
		"weather":      {"天气", "气温", "温度"},
		"grep":         {"搜索日志", "查日志", "grep", "搜索error", "搜索错误", "定位异常"},
		"query_order":  {"订单", "查我的", "查询订单", "我的订单", "查一下"},
	}

	// Find the best match: prefer the match with the longest matching keyword.
	// This ensures "删除订单" (4 chars) beats "订单" (2 chars) when both are available.
	type scoredMatch struct {
		toolName string
		argFunc  func(string) string
		matchLen int
	}
	var best scoredMatch

	for _, m := range matches {
		if !available[m.toolName] {
			continue
		}
		kws, ok := keywords[m.toolName]
		if !ok {
			continue
		}
		for _, kw := range kws {
			if len(kw) > best.matchLen && containsAny(lower, kw) {
				best = scoredMatch{
					toolName: m.toolName,
					argFunc:  m.argFunc,
					matchLen: len(kw),
				}
			}
		}
	}

	if best.matchLen > 0 {
		args := best.argFunc(raw)
		return &schema.ToolCall{
			ID: "tc_" + best.toolName + "_1",
			Function: schema.FunctionCall{
				Name:      best.toolName,
				Arguments: args,
			},
		}
	}

	return nil
}

func generateFinalAnswer(lower, raw, preferredLang string) string {
	switch {
	case containsAny(lower, "写脚本", "写个脚本", "写代码", "写程序", "编程", "帮我写"):
		lang := "Python"
		if preferredLang != "" {
			lang = preferredLang
		}
		return fmt.Sprintf("好的，我将使用 %s 为您编写脚本。请问您需要什么功能的脚本？", lang)
	case containsAny(lower, "我喜欢", "我偏好", "以后请", "我喜欢用"):
		lang := detectLanguagePreference(lower)
		if lang != "" {
			return fmt.Sprintf("好的，我已记住您喜欢使用 %s，以后会默认使用它。", lang)
		}
		return "好的，我已记住您的偏好。"
	case containsAny(lower, "你好", "hi", "hello", "嗨"):
		return "您好！我是智能助手，可以帮您：计算数学、查询天气、搜索日志、查询/删除订单、发送邮件等。"
	default:
		return "我可以帮您完成以下任务：\n• 数学计算（如：23 * 19 等于多少）\n• 天气查询（如：查询北京天气）\n• 订单查询（如：查我的订单）\n• 删除订单（如：删除订单 A-1001）\n• 搜索日志（如：搜索日志中的error）\n• 发送邮件（如：发邮件给 admin）"
	}
}

func summarizeToolResults(messages []*schema.Message) string {
	// Only this turn's tool results. The message list carries the whole thread, so
	// summarizing all of it made every answer repeat every earlier result: a
	// weather lookup from an earlier turn reappeared in the summary of a later
	// arithmetic question. A run's tool results always follow the user message
	// that started it.
	start := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == schema.User {
			start = i + 1
			break
		}
	}

	var parts []string
	for _, m := range messages[start:] {
		if m.Role == schema.Tool {
			name := m.Name
			if name == "" {
				name = "工具"
			}
			parts = append(parts, fmt.Sprintf("%s：%s", name, m.Content))
		}
	}
	if len(parts) == 0 {
		return "工具执行完成。"
	}
	return fmt.Sprintf("根据工具执行结果——%s", joinStrings(parts, "；"))
}

// ---- Helper functions ----

// preferredLanguageFromPrompt reads the language out of the memory line the
// framework injects into the system prompt ("- preferred_language: JavaScript").
//
// It parses the value instead of scanning the prompt for language names. A
// substring scan cannot tell JavaScript from Java — the prompt contains the
// former, and "java" matches it — so the mock used to answer "Java" for a user
// whose stored preference was JavaScript, contradicting the very memory it was
// reading.
func preferredLanguageFromPrompt(messages []*schema.Message) string {
	const marker = "preferred_language:"
	for _, m := range messages {
		if m.Role != schema.System {
			continue
		}
		idx := strings.Index(m.Content, marker)
		if idx < 0 {
			continue
		}
		rest := m.Content[idx+len(marker):]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		if v := strings.TrimSpace(rest); v != "" {
			return v
		}
	}
	return ""
}

// detectLanguagePreference reads a language name out of the user's own message.
//
// Candidates are ordered longest-first, and short ambiguous names must appear as
// a whole word: a plain substring scan reports JavaScript as Java and Django as
// Go, the same defect class that made the mock's deleted extraction matcher
// wrong. "golang" is listed separately because the whole-word rule rightly
// rejects "go" inside it.
func detectLanguagePreference(s string) string {
	candidates := []struct{ name, label string }{
		{"typescript", "TypeScript"},
		{"javascript", "JavaScript"},
		{"golang", "Go"},
		{"python", "Python"},
		{"kotlin", "Kotlin"},
		{"swift", "Swift"},
		{"rust", "Rust"},
		{"java", "Java"},
		{"go", "Go"},
	}
	for _, c := range candidates {
		if containsWord(s, c.name) {
			return c.label
		}
	}
	return ""
}

// containsWord reports whether s contains needle delimited by non-alphanumerics,
// so "go" does not match "django" and "java" does not match "javascript". Bytes
// ≥ 0x80 count as delimiters, which is what makes a preceding CJK character a
// boundary rather than part of a word.
func containsWord(s, needle string) bool {
	isAlnum := func(b byte) bool {
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
	}
	for idx := 0; idx < len(s); {
		i := strings.Index(s[idx:], needle)
		if i < 0 {
			return false
		}
		i += idx
		before := i == 0 || !isAlnum(s[i-1])
		end := i + len(needle)
		after := end >= len(s) || !isAlnum(s[end])
		if before && after {
			return true
		}
		idx = i + 1
	}
	return false
}

// extractMathExpr pulls an arithmetic expression out of a message, or returns ""
// when there is none.
//
// It used to return a hardcoded "1 + 1", which made the mock answer a question
// nobody asked: the old parser split on whitespace and required the operator to be
// its own field, so "计算1+4" — no spaces around the operator — found nothing and
// came back as "1 + 1 = 2". A fabricated result that looks plausible is worse than
// a failure, because the user has to notice that the expression changed to catch
// it. Returning "" makes the calculator report "unsupported expression", which is
// visible.
func extractMathExpr(s string) string {
	// Normalise spelled-out operators so "23 乘以 19" and "1加4" reach the same
	// path as their symbolic forms. Multi-character forms first, or "除以" would be
	// eaten by "除".
	normalised := strings.NewReplacer(
		"乘以", "*", "除以", "/", "加上", "+", "减去", "-",
		"乘", "*", "除", "/", "加", "+", "减", "-",
		"×", "*", "÷", "/",
	).Replace(s)

	// Keep only what an expression can contain. Everything else — prose,
	// punctuation — becomes a separator, so it cannot be glued onto an operand.
	var filtered []rune
	for _, r := range normalised {
		switch {
		case r >= '0' && r <= '9', r == '.', r == '(', r == ')':
			filtered = append(filtered, r)
		case r == '+' || r == '-' || r == '*' || r == '/':
			// Padded, so "1+4" and "1 + 4" yield the same fields.
			filtered = append(filtered, ' ', r, ' ')
		default:
			filtered = append(filtered, ' ')
		}
	}
	fields := strings.Fields(string(filtered))

	isOperand := func(f string) bool {
		if _, err := strconv.ParseFloat(f, 64); err == nil {
			return true
		}
		// A parenthesised operand counts as an operand; it is not parsed further.
		return strings.HasPrefix(f, "(") && strings.HasSuffix(f, ")")
	}
	isOperator := func(f string) bool {
		return f == "+" || f == "-" || f == "*" || f == "/"
	}

	// The longest alternating operand/operator run. Requiring an operand on both
	// sides is what rejects an operator left over from a word — "除了计算 3*2"
	// normalises to a leading "*", which is not an expression.
	best := ""
	for i := range fields {
		if !isOperator(fields[i]) || i == 0 || i+1 >= len(fields) {
			continue
		}
		if !isOperand(fields[i-1]) || !isOperand(fields[i+1]) {
			continue
		}
		start, end := i-1, i+1
		for start-2 >= 0 && isOperand(fields[start-2]) && isOperator(fields[start-1]) {
			start -= 2
		}
		for end+2 < len(fields) && isOperator(fields[end+1]) && isOperand(fields[end+2]) {
			end += 2
		}
		if candidate := strings.Join(fields[start:end+1], " "); len(candidate) > len(best) {
			best = candidate
		}
	}
	return best
}

func extractCity(s string) string {
	cities := []string{"北京", "上海", "深圳", "广州", "杭州", "成都", "武汉", "南京"}
	for _, c := range cities {
		if containsAny(s, c) {
			return c
		}
	}
	return "北京"
}

// extractNoteID finds a note id like "n2" in the message, defaulting to n1 so the
// demo always produces a callable argument.
func extractNoteID(s string) string {
	words := splitFields(s)
	for _, w := range words {
		w = trimRightPunct(w)
		if len(w) == 2 && (w[0] == 'n' || w[0] == 'N') && w[1] >= '0' && w[1] <= '9' {
			return "n" + string(w[1])
		}
	}
	return "n1"
}

func extractOrderID(s string) string {
	words := splitFields(s)
	for _, w := range words {
		w = trimRightPunct(w)
		if len(w) > 2 && w[0] >= 'A' && w[0] <= 'B' && w[1] == '-' {
			return w
		}
		if hasPrefix(w, "A-") || hasPrefix(w, "B-") {
			return w
		}
	}
	return ""
}

func extractPattern(s string) string {
	if idx := indexOf(s, "搜索"); idx >= 0 {
		rest := trimLeft(s[idx+len("搜索"):], "中的")
		if rest != "" {
			return rest
		}
	}
	if idx := indexOf(s, "grep"); idx >= 0 {
		return trimSpaces(s[idx+4:])
	}
	if containsAny(s, "error") {
		return "error"
	}
	if containsAny(s, "异常") {
		return "error"
	}
	return "error"
}

func toLower(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result = append(result, c)
	}
	return string(result)
}

func containsAny(s string, keywords ...string) bool {
	for _, k := range keywords {
		if len(k) <= len(s) {
			for i := 0; i <= len(s)-len(k); i++ {
				if s[i:i+len(k)] == k {
					return true
				}
			}
		}
	}
	return false
}

func splitFields(s string) []string {
	var fields []string
	var buf []byte
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '　' {
			if len(buf) > 0 {
				fields = append(fields, string(buf))
				buf = buf[:0]
			}
		} else {
			buf = append(buf, string(r)...)
		}
	}
	if len(buf) > 0 {
		fields = append(fields, string(buf))
	}
	return fields
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}

func trimRightPunct(s string) string {
	runes := []rune(s)
	for len(runes) > 0 {
		r := runes[len(runes)-1]
		if r == '。' || r == '，' || r == '！' || r == '？' || r == '、' || r == '.' || r == ',' {
			runes = runes[:len(runes)-1]
		} else {
			break
		}
	}
	return string(runes)
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimLeft(s, cutset string) string {
	for hasPrefix(s, cutset) {
		s = s[len(cutset):]
	}
	return s
}

func trimSpaces(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

// Suppress unused import warning
var _ = io.EOF

// ---- Summary generation for context compression ----

// isSummaryRequest detects whether the input messages are a summary request
// from the context manager's Summarizer. The Summarizer sends a system message
// "你是一个对话摘要助手" followed by a user message containing "旧对话内容".
func isSummaryRequest(input []*schema.Message) bool {
	for _, m := range input {
		if m.Role == schema.System {
			if containsAny(m.Content, "对话摘要助手") || containsAny(m.Content, "摘要助手") {
				return true
			}
		}
		if m.Role == schema.User {
			if containsAny(m.Content, "压缩成一段") || containsAny(m.Content, "旧对话内容") || containsAny(m.Content, "旧对话") {
				return true
			}
		}
	}
	return false
}

// generateSummary produces a structured summary from the conversation history
// embedded in the summary request. This simulates what a real LLM would produce.
func generateSummary(input []*schema.Message) string {
	// Extract the conversation content from the user message
	convContent := ""
	for _, m := range input {
		if m.Role == schema.User {
			convContent = m.Content
			break
		}
	}

	var parts []string

	// Parse the formatted conversation lines: [用户], [助手], [工具结果-...]
	lines := splitByNewline(convContent)
	for _, line := range lines {
		line = trimSpaces(line)
		if line == "" {
			continue
		}

		if hasPrefix(line, "[用户]") {
			content := trimSpaces(line[len("[用户]"):])
			// Extract key user intents
			if containsAny(content, "计算", "等于") {
				parts = append(parts, "用户请求了数学计算")
			} else if containsAny(content, "天气") {
				parts = append(parts, "用户查询了天气信息")
			} else if containsAny(content, "订单") {
				parts = append(parts, "用户查询了订单相关内容")
			} else if containsAny(content, "删除") {
				parts = append(parts, "用户请求了删除操作")
			} else if containsAny(content, "喜欢") || containsAny(content, "偏好") {
				parts = append(parts, "用户表达了偏好："+content)
			} else if containsAny(content, "邮件") {
				parts = append(parts, "用户请求了邮件操作")
			} else {
				parts = append(parts, "用户询问："+truncateStr(content, 40))
			}
		} else if hasPrefix(line, "[助手]") {
			// Already captured by tool results or user intents
		} else if hasPrefix(line, "[工具结果-") {
			// Extract tool name
			endIdx := indexOf(line, "]")
			if endIdx > 0 {
				toolName := line[5:endIdx] // skip "[工具结果-"
				parts = append(parts, fmt.Sprintf("工具 %s 已执行", toolName))
			}
		}
	}

	if len(parts) == 0 {
		return "之前的对话中用户进行了若干查询和操作，助手使用工具完成了相应任务。"
	}

	return "之前的对话摘要：" + joinStrings(parts, "；") + "。"
}

func splitByNewline(s string) []string {
	var lines []string
	var buf []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, string(buf))
			buf = buf[:0]
		} else {
			buf = append(buf, s[i])
		}
	}
	if len(buf) > 0 {
		lines = append(lines, string(buf))
	}
	return lines
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
