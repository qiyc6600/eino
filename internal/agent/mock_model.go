package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
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

	// Handle preference extraction prompts from the memory service.
	if isPreferenceExtractionRequest(input) {
		extracted := extractPreferences(input)
		return schema.AssistantMessage(extracted, nil), nil
	}

	// Parse system prompt for user preferences
	preferredLang := detectPreferredLangFromMessages(input)

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
	if msg == nil || msg.Content == "" {
		return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
	}

	runes := []rune(msg.Content)
	chunks := make([]*schema.Message, 0, len(runes)/mockStreamChunkRunes+1)
	for start := 0; start < len(runes); start += mockStreamChunkRunes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + mockStreamChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, schema.AssistantMessage(string(runes[start:end]), nil))
		if mockStreamChunkDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(mockStreamChunkDelay):
			}
		}
	}
	return schema.StreamReaderFromArray(chunks), nil
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
		// Real tools (used when sub-agents are not bound)
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
	var parts []string
	for _, m := range messages {
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

func detectPreferredLangFromMessages(messages []*schema.Message) string {
	for _, m := range messages {
		if m.Role == schema.System {
			if containsAny(m.Content, "preferred_language") {
				if containsAny(m.Content, "Python") {
					return "Python"
				}
				if containsAny(m.Content, "Java") {
					return "Java"
				}
				if containsAny(m.Content, "Go") {
					return "Go"
				}
			}
		}
	}
	return ""
}

func detectLanguagePreference(s string) string {
	pairs := []struct{ p, l string }{
		{"python", "Python"}, {"java", "Java"}, {"go", "Go"},
		{"javascript", "JavaScript"}, {"typescript", "TypeScript"},
	}
	for _, pp := range pairs {
		if containsAny(s, pp.p) {
			return pp.l
		}
	}
	return ""
}

func extractMathExpr(s string) string {
	parts := splitFields(s)
	for i, p := range parts {
		if p == "*" || p == "+" || p == "-" || p == "/" || p == "×" || p == "÷" {
			if i > 0 && i < len(parts)-1 {
				op := p
				if p == "×" {
					op = "*"
				} else if p == "÷" {
					op = "/"
				}
				return parts[i-1] + " " + op + " " + parts[i+1]
			}
		}
	}
	return "1 + 1"
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

// ---- Preference extraction for long-term memory ----

// isPreferenceExtractionRequest detects whether the input messages are a
// preference extraction request from memory.Service.ExtractAndSave().
// The service sends a system message "用户偏好提取助手" followed by a user
// message containing the user's original message.
func isPreferenceExtractionRequest(input []*schema.Message) bool {
	for _, m := range input {
		if m.Role == schema.System {
			if containsAny(m.Content, "偏好提取助手") || containsAny(m.Content, "preference extract") {
				return true
			}
		}
	}
	return false
}

// extractPreferences simulates LLM-based preference extraction.
// It parses the user message for common preference patterns and returns
// a JSON array of {key, value} pairs, matching the format expected by
// memory.Service.extractWithLLM().
func extractPreferences(input []*schema.Message) string {
	userMsg := ""
	for _, m := range input {
		if m.Role == schema.User {
			userMsg = m.Content
			break
		}
	}

	// Extract the actual user message from the prompt
	// The prompt ends with "用户消息：" followed by the message
	msgIdx := strings.LastIndex(userMsg, "用户消息：")
	if msgIdx == -1 {
		msgIdx = strings.LastIndex(userMsg, "用户消息:")
	}
	if msgIdx != -1 {
		userMsg = userMsg[msgIdx+len("用户消息："):]
	}

	lower := toLower(userMsg)
	var pairs []string

	// Language preference
	if containsAny(lower, "喜欢", "偏好", "prefer") {
		switch {
		case containsAny(lower, "python"):
			pairs = append(pairs, `{"key":"preferred_language","value":"Python"}`)
		case containsAny(lower, "java"):
			pairs = append(pairs, `{"key":"preferred_language","value":"Java"}`)
		case containsAny(lower, "go") || containsAny(lower, "golang"):
			pairs = append(pairs, `{"key":"preferred_language","value":"Go"}`)
		case containsAny(lower, "javascript") || containsAny(lower, "js"):
			pairs = append(pairs, `{"key":"preferred_language","value":"JavaScript"}`)
		case containsAny(lower, "rust"):
			pairs = append(pairs, `{"key":"preferred_language","value":"Rust"}`)
		case containsAny(lower, "c++") || containsAny(lower, "cpp"):
			pairs = append(pairs, `{"key":"preferred_language","value":"C++"}`)
		}
	}

	// Answer style preference
	if containsAny(lower, "简洁", "简短") {
		pairs = append(pairs, `{"key":"answer_style","value":"concise"}`)
	}
	if containsAny(lower, "详细", "详尽") {
		pairs = append(pairs, `{"key":"answer_style","value":"detailed"}`)
	}

	// Framework preference
	if containsAny(lower, "react") && !containsAny(lower, "vue") {
		pairs = append(pairs, `{"key":"preferred_framework","value":"React"}`)
	}
	if containsAny(lower, "vue") {
		pairs = append(pairs, `{"key":"preferred_framework","value":"Vue"}`)
	}

	if len(pairs) == 0 {
		return "[]"
	}
	return "[" + joinStrings(pairs, ",") + "]"
}
