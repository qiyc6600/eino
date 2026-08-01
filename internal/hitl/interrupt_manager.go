package hitl

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/google/uuid"
)

// InterruptManager provides the unified interrupt/resume entry point.
// Both node-level and tool-level interrupts go through this manager.
type InterruptManager struct {
	mu        sync.RWMutex
	pending   map[string]*ApprovalRequest // interruptID -> request
	checkpoint memory.CheckpointStore
}

// NewInterruptManager creates a new InterruptManager.
func NewInterruptManager(checkpointStore memory.CheckpointStore) *InterruptManager {
	return &InterruptManager{
		pending:    make(map[string]*ApprovalRequest),
		checkpoint: checkpointStore,
	}
}

// RequestInterrupt creates an interrupt for a running agent.
// It saves the checkpoint and creates a pending approval request.
func (m *InterruptManager) RequestInterrupt(ctx context.Context, payload map[string]any) (*ApprovalRequest, error) {
	interruptID := "i_" + uuid.New().String()[:8]

	userID, _ := payload["user_id"].(string)
	threadID, _ := payload["thread_id"].(string)
	runID, _ := payload["run_id"].(string)
	interruptType, _ := payload["type"].(InterruptType)
	nodeName, _ := payload["node_name"].(string)
	toolName, _ := payload["tool_name"].(string)
	arguments, _ := payload["arguments"].(string)
	riskLevel, _ := payload["risk_level"].(string)
	message, _ := payload["message"].(string)

	req := &ApprovalRequest{
		InterruptID: interruptID,
		Type:        interruptType,
		RunID:       runID,
		UserID:      userID,
		ThreadID:    threadID,
		NodeName:    nodeName,
		ToolName:    toolName,
		Arguments:   arguments,
		Payload:     payload,
		RiskLevel:   riskLevel,
		Message:     message,
		Status:      StatusPending,
		CreatedAt:   time.Now(),
	}

	// Save checkpoint
	cp := memory.Checkpoint{
		UserID:      userID,
		ThreadID:    threadID,
		RunID:       runID,
		State:       []byte(fmt.Sprintf(`{"interrupt_id":"%s"}`, interruptID)),
		Interrupted: true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := m.checkpoint.Save(ctx, cp); err != nil {
		return nil, fmt.Errorf("failed to save checkpoint: %w", err)
	}

	m.mu.Lock()
	m.pending[interruptID] = req
	m.mu.Unlock()

	return req, nil
}

// Resume processes an approval decision and returns the request.
func (m *InterruptManager) Resume(ctx context.Context, interruptID string, decision ApprovalDecision) (*ApprovalRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.pending[interruptID]
	if !ok {
		return nil, fmt.Errorf("interrupt not found: %s", interruptID)
	}

	if req.Status != StatusPending {
		return nil, fmt.Errorf("interrupt already decided: %s (status: %s)", interruptID, req.Status)
	}

	now := time.Now()
	req.DecidedAt = &now
	req.Decision = &decision

	if decision.Approved {
		req.Status = StatusApproved
	} else {
		req.Status = StatusRejected
	}

	// Update checkpoint
	cp := memory.Checkpoint{
		UserID:      req.UserID,
		ThreadID:    req.ThreadID,
		RunID:       req.RunID,
		State:       []byte(fmt.Sprintf(`{"interrupt_id":"%s","approved":%v}`, interruptID, decision.Approved)),
		Interrupted: false,
		CreatedAt:   req.CreatedAt,
		UpdatedAt:   now,
	}
	if err := m.checkpoint.Save(ctx, cp); err != nil {
		return nil, fmt.Errorf("failed to update checkpoint: %w", err)
	}

	return req, nil
}

// GetPending returns all pending approval requests.
func (m *InterruptManager) GetPending(ctx context.Context) []*ApprovalRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*ApprovalRequest
	for _, req := range m.pending {
		if req.Status == StatusPending {
			result = append(result, req)
		}
	}
	return result
}

// GetPendingForUser returns pending requests for a specific user/thread.
func (m *InterruptManager) GetPendingForUser(ctx context.Context, userID string) []*ApprovalRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*ApprovalRequest
	for _, req := range m.pending {
		if req.Status == StatusPending && req.UserID == userID {
			result = append(result, req)
		}
	}
	return result
}

// GetRequest returns a specific approval request.
func (m *InterruptManager) GetRequest(interruptID string) (*ApprovalRequest, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	req, ok := m.pending[interruptID]
	return req, ok
}
