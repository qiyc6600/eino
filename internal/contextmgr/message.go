package contextmgr

// Message represents a chat message with metadata for context management.
type Message struct {
	Role      string         `json:"role"` // system, user, assistant, tool
	Content   string         `json:"content"`
	Name      string         `json:"name,omitempty"`
	ToolID    string         `json:"tool_id,omitempty"`
	ToolCalls []ToolCallRef  `json:"tool_calls,omitempty"`
	IsSystem  bool           `json:"is_system,omitempty"`
	IsSummary bool           `json:"is_summary,omitempty"` // metadata.summary=true
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ToolCallRef references a tool call within an assistant message.
type ToolCallRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToSimpleMessages converts context messages to the simple agent.Message format.
func ToSimpleMessages(msgs []Message) []map[string]any {
	result := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, map[string]any{
			"role":    m.Role,
			"content": m.Content,
			"name":    m.Name,
		})
	}
	return result
}
