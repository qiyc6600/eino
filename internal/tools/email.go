package tools

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// EmailArgs represents arguments for the send_email tool.
type EmailArgs struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// SentEmailRecord records a "sent" email for verification and demonstration.
// In production this would integrate with a real email provider; here we record
// to an in-memory store so the action is observable and verifiable.
type SentEmailRecord struct {
	To     string    `json:"to"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	UserID  string   `json:"user_id"`
	SentAt  time.Time `json:"sent_at"`
	RunID   string   `json:"run_id,omitempty"`
}

// EmailStore keeps an in-memory log of all "sent" emails, isolated by userID.
type EmailStore struct {
	mu      sync.RWMutex
	records []SentEmailRecord
}

// NewEmailStore creates a new EmailStore.
func NewEmailStore() *EmailStore {
	return &EmailStore{}
}

// Record appends a sent email record.
func (s *EmailStore) Record(rec SentEmailRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, rec)
}

// ListByUser returns all email records for a given user.
func (s *EmailStore) ListByUser(userID string) []SentEmailRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []SentEmailRecord
	for _, r := range s.records {
		if r.UserID == userID {
			result = append(result, r)
		}
	}
	return result
}

// ListAll returns all email records.
func (s *EmailStore) ListAll() []SentEmailRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SentEmailRecord, len(s.records))
	copy(result, s.records)
	return result
}

// NewSendEmailTool creates the send_email tool (admin, requires approval).
// emailStore is used to record "sent" emails so the action is observable.
func NewSendEmailTool(emailStore *EmailStore) RegisteredTool {
	return RegisteredTool{
		Meta: ToolMeta{
			Name:             "send_email",
			Description:      "Send an email to a recipient. Admin only. Requires approval before execution.",
			RequiredPerm:     "send_email",
			RiskLevel:        RiskLevelHigh,
			RequiresApproval: true,
			ParamSchema: `{
					"type": "object",
					"properties": {
						"to": {"type": "string", "description": "Recipient email address"},
						"subject": {"type": "string", "description": "Email subject"},
						"body": {"type": "string", "description": "Email body"}
					},
					"required": ["to", "subject", "body"]
				}`,
		},
		Fn: func(ctx map[string]any, arguments string) ToolResult {
			return executeSendEmail(ctx, arguments, emailStore)
		},
	}
}

func executeSendEmail(ctx map[string]any, arguments string, emailStore *EmailStore) ToolResult {
	var args EmailArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return BusinessErrorResult("send_email", "invalid arguments: "+err.Error())
	}

	if args.To == "" || args.Subject == "" {
		return BusinessErrorResult("send_email", "to and subject are required")
	}

	// Record the email in the store for verification and demonstration.
	userID, _ := ctx["user_id"].(string)
	runID, _ := ctx["run_id"].(string)
	if emailStore != nil {
		emailStore.Record(SentEmailRecord{
			To:      args.To,
			Subject: args.Subject,
			Body:    args.Body,
			UserID:  userID,
			SentAt:  time.Now(),
			RunID:   runID,
		})
	}

	content := fmt.Sprintf("邮件已发送至 %s，主题：%s", args.To, args.Subject)
	if args.Body != "" {
		content += fmt.Sprintf("，正文长度：%d字符", len(args.Body))
	}
	return SuccessResult("send_email", content,
		map[string]any{"to": args.To, "subject": args.Subject, "body_length": len(args.Body)})
}
