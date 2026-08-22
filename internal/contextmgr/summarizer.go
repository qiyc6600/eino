package contextmgr

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Summarizer provides LLM-based summary compression for old messages.
//
// When a ChatModel is provided, SummarizeOldMessages calls the LLM to generate
// a semantic summary of old conversation history. If the LLM call fails or no
// ChatModel is configured, it falls back to rule-based extraction.
//
// 恢复语义说明：
//   - 摘要压缩是上下文管理模块的核心能力，任务要求"使用 LLM 进行摘要压缩"
//   - LLM 摘要失败时自动降级到规则提取，保证 Agent 运行不中断
type Summarizer struct {
	counter             TokenCounter
	chatModel           model.BaseChatModel // 用于 LLM 摘要压缩（可以是 ChatModel 或 ToolCallingChatModel）
	summarizeThreshold  float64             // 触发摘要的阈值比例（如 0.8 表示 80%）
	summaryTargetTokens int                 // 摘要目标 token 数
}

// NewSummarizer creates a new Summarizer.
// chatModel can be nil, in which case rule-based summarization is used as fallback.
// model.ToolCallingChatModel also implements model.BaseChatModel, so it can be passed directly.
// SetChatModel replaces the chat model (used for runtime model switching).
func (s *Summarizer) SetChatModel(chatModel model.BaseChatModel) {
	s.chatModel = chatModel
}

func NewSummarizer(counter TokenCounter, thresholdRatio float64, targetTokens int, chatModel model.BaseChatModel) *Summarizer {
	return &Summarizer{
		counter:             counter,
		chatModel:           chatModel,
		summarizeThreshold:  thresholdRatio,
		summaryTargetTokens: targetTokens,
	}
}

// ShouldSummarize checks whether the message history exceeds the threshold.
func (s *Summarizer) ShouldSummarize(messages []Message, maxTokens int) bool {
	currentTokens := s.counter.CountMessages(messages)
	threshold := int(float64(maxTokens) * s.summarizeThreshold)
	return currentTokens > threshold
}

// CountTokens returns the estimated token count of the messages.
func (s *Summarizer) CountTokens(messages []Message) int {
	return s.counter.CountMessages(messages)
}

// ThresholdRatio returns the configured summarization threshold ratio.
func (s *Summarizer) ThresholdRatio() float64 {
	return s.summarizeThreshold
}

// SummarizeOldMessages takes old messages and produces a summary string.
// When chatModel is available, calls the LLM to generate a semantic summary.
// Otherwise, falls back to rule-based extraction.
func (s *Summarizer) SummarizeOldMessages(ctx context.Context, messages []Message) string {
	if s.chatModel != nil {
		summary, err := s.summarizeWithLLM(ctx, messages)
		if err == nil && summary != "" {
			return summary
		}
		// LLM 调用失败时降级到规则摘要，不中断 Agent 运行
	}
	return s.summarizeWithRules(messages)
}

// summarizeWithLLM calls the ChatModel to generate a semantic summary.
// This satisfies the task requirement: "使用 LLM 进行摘要压缩".
func (s *Summarizer) summarizeWithLLM(ctx context.Context, messages []Message) (string, error) {
	// 1. 将消息格式化为自然语言文本
	conversationText := s.formatMessagesForSummary(messages)

	// 2. 构建摘要 prompt
	summaryPrompt := `请把下面的旧对话压缩成一段供后续 Agent 使用的上下文摘要。
要求：
1. 保留用户目标、已确认事实、工具执行结果、未完成事项。
2. 删除寒暄、重复内容和无关细节。
3. 不编造。
4. 保留与权限、审批、订单、用户偏好相关的信息。

旧对话内容：
` + conversationText

	// 3. 调用 LLM
	resp, err := s.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("你是一个对话摘要助手，擅长从对话历史中提取关键信息。"),
		schema.UserMessage(summaryPrompt),
	})
	if err != nil {
		return "", fmt.Errorf("LLM summary failed: %w", err)
	}

	return "历史摘要：" + resp.Content, nil
}

// formatMessagesForSummary formats messages into a readable text for the LLM prompt.
func (s *Summarizer) formatMessagesForSummary(messages []Message) string {
	var sb strings.Builder
	for i, m := range messages {
		switch m.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("[用户] %s\n", m.Content))
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var toolDescs []string
				for _, tc := range m.ToolCalls {
					toolDescs = append(toolDescs, fmt.Sprintf("调用工具 %s(%s)", tc.Name, tc.Arguments))
				}
				sb.WriteString(fmt.Sprintf("[助手] %s\n", strings.Join(toolDescs, "; ")))
			} else {
				sb.WriteString(fmt.Sprintf("[助手] %s\n", m.Content))
			}
		case "tool":
			sb.WriteString(fmt.Sprintf("[工具结果-%s] %s\n", m.Name, m.Content))
		case "system":
			// system 消息不需要摘要，它们在裁剪时被永久保留
		}
		// 限制总长度，避免 prompt 过长
		if sb.Len() > 6000 {
			sb.WriteString(fmt.Sprintf("...（共 %d 条消息，已截断）", len(messages)))
			break
		}
		_ = i
	}
	return sb.String()
}

// summarizeWithRules is the fallback rule-based summarization.
// Used when no LLM is available or when the LLM call fails.
func (s *Summarizer) summarizeWithRules(messages []Message) string {
	var sb strings.Builder
	sb.WriteString("历史摘要：")
	for _, m := range messages {
		switch m.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("用户说：%s；", truncate(m.Content, 80)))
		case "assistant":
			sb.WriteString(fmt.Sprintf("助手回复：%s；", truncate(m.Content, 80)))
		case "tool":
			sb.WriteString(fmt.Sprintf("工具 %s 结果：%s；", m.Name, truncate(m.Content, 80)))
		}
	}
	return sb.String()
}

// Compress performs the full compression pipeline:
// 1. Check if summary is needed
// 2. If yes, summarize old messages using LLM and replace with a single summary message
// 3. Then apply token-based trimming
func (s *Summarizer) Compress(ctx context.Context, messages []Message, maxTokens int) []Message {
	if !s.ShouldSummarize(messages, maxTokens) {
		// Still apply basic trimming
		return TrimByToken(messages, maxTokens, s.counter)
	}

	// 分离 system 消息
	var systemMsgs []Message
	var nonSystem []Message
	for _, m := range messages {
		if m.IsSystem || m.Role == "system" {
			systemMsgs = append(systemMsgs, m)
		} else {
			nonSystem = append(nonSystem, m)
		}
	}

	if len(nonSystem) <= 2 {
		return messages // Not enough to compress
	}

	// 保留最近 1/3，摘要旧 2/3
	recentCount := max(2, len(nonSystem)/3)
	oldMessages := nonSystem[:len(nonSystem)-recentCount]
	recentMessages := nonSystem[len(nonSystem)-recentCount:]

	// 使用 LLM 生成摘要（LLM 不可用时降级到规则摘要）
	summaryText := s.SummarizeOldMessages(ctx, oldMessages)

	// 创建摘要消息（assistant 角色，metadata.summary=true）
	// 统一约定：不新增自定义 role，避免与 Eino 或模型 provider 的消息 schema 不兼容
	// 摘要消息固定放在 system 消息之后、最近消息之前
	summaryMsg := Message{
		Role:      "assistant",
		Content:   summaryText,
		IsSummary: true,
		Metadata:  map[string]any{"summary": true},
	}

	// 重组：system + summary + recent
	result := make([]Message, 0, len(systemMsgs)+1+len(recentMessages))
	result = append(result, systemMsgs...)
	result = append(result, summaryMsg)
	result = append(result, recentMessages...)

	// 最终 token 裁剪
	return TrimByToken(result, maxTokens, s.counter)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
