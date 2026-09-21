package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/example/agent-eino-demo/internal/auth"
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
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	UserID  string    `json:"user_id"`
	SentAt  time.Time `json:"sent_at"`
	RunID   string    `json:"run_id,omitempty"`
}

type EmailRepository interface {
	RecordContext(ctx context.Context, rec SentEmailRecord, idempotencyKey string) (created bool, err error)
	ListByUserContext(ctx context.Context, userID string) ([]SentEmailRecord, error)
}

// EmailStore keeps an in-memory log of all "sent" emails, isolated by userID.
type EmailStore struct {
	mu      sync.RWMutex
	records []SentEmailRecord
	effects map[string]struct{}
}

// NewEmailStore creates a new EmailStore.
func NewEmailStore() *EmailStore {
	return &EmailStore{effects: make(map[string]struct{})}
}

// Record appends a sent email record.
func (s *EmailStore) Record(rec SentEmailRecord) {
	_, _ = s.RecordContext(context.Background(), rec, "")
}

func (s *EmailStore) RecordContext(ctx context.Context, rec SentEmailRecord, idempotencyKey string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := auth.CheckUserScope(ctx, rec.UserID); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if idempotencyKey != "" {
		if _, ok := s.effects[idempotencyKey]; ok {
			return false, nil
		}
		s.effects[idempotencyKey] = struct{}{}
	}
	s.records = append(s.records, rec)
	return true, nil
}

// ListByUser returns all email records for a given user.
func (s *EmailStore) ListByUser(userID string) []SentEmailRecord {
	records, _ := s.ListByUserContext(context.Background(), userID)
	return records
}

func (s *EmailStore) ListByUserContext(ctx context.Context, userID string) ([]SentEmailRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []SentEmailRecord
	for _, r := range s.records {
		if r.UserID == userID {
			result = append(result, r)
		}
	}
	return result, nil
}

// NewSendEmailTool creates the send_email tool (admin, requires approval).
// emailStore is used to record "sent" emails so the action is observable.
func NewSendEmailTool(emailStore EmailRepository) RegisteredTool {
	return RegisteredTool{
		Meta: ToolMeta{
			Name:             "send_email",
			Description:      "Send an email to a recipient. This is a high-risk operation that will trigger an automatic human approval flow before execution — you MUST call this tool directly, do NOT ask the user for confirmation.",
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
		Fn: func(identity *auth.ToolIdentity, arguments string) ToolResult {
			return executeSendEmail(identity, arguments, emailStore)
		},
	}
}

func executeSendEmail(identity *auth.ToolIdentity, arguments string, emailStore EmailRepository) ToolResult {
	if identity == nil || identity.UserID == "" {
		return SystemErrorResult("send_email", "missing authenticated user identity", "")
	}
	var args EmailArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return BusinessErrorResult("send_email", "invalid arguments: "+err.Error())
	}

	if args.To == "" || args.Subject == "" {
		return BusinessErrorResult("send_email", "to and subject are required")
	}

	// Record the email in the store for verification and demonstration.
	userID, runID := identity.UserID, identity.RunID
	replayed := false
	if emailStore != nil {
		created, err := emailStore.RecordContext(toolContext(identity), SentEmailRecord{
			To:      args.To,
			Subject: args.Subject,
			Body:    args.Body,
			UserID:  userID,
			SentAt:  time.Now(),
			RunID:   runID,
		}, toolIdempotencyKey(identity, "send_email", arguments))
		if err != nil {
			return SystemErrorResult("send_email", err.Error(), runID)
		}
		replayed = !created
	}

	content := fmt.Sprintf("邮件已发送至 %s，主题：%s", args.To, args.Subject)
	if args.Body != "" {
		content += fmt.Sprintf("，正文长度：%d字符", len(args.Body))
	}
	return SuccessResult("send_email", content,
		map[string]any{"to": args.To, "subject": args.Subject, "body_length": len(args.Body), "idempotent_replay": replayed})
}

var _ EmailRepository = (*EmailStore)(nil)
