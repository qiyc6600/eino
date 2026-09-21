package hitl

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/example/agent-eino-demo/internal/memory"
	"github.com/example/agent-eino-demo/internal/storage"
	"github.com/google/uuid"
)

// InterruptManager uses its in-process map for memory/file deployments and
// delegates to ApprovalStore for database-backed multi-instance deployments.
type InterruptManager struct {
	path       string
	mu         sync.RWMutex
	pending    map[string]*ApprovalRequest
	checkpoint memory.CheckpointStore
	store      ApprovalStore
}

func NewInterruptManager(checkpointStore memory.CheckpointStore) *InterruptManager {
	return &InterruptManager{pending: make(map[string]*ApprovalRequest), checkpoint: checkpointStore}
}

// UseStore switches approval coordination to an external transactional store.
// It must be called during application startup, before serving requests.
func (m *InterruptManager) UseStore(store ApprovalStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
}

// RequestInterrupt is retained for callers that create an interrupt directly.
func (m *InterruptManager) RequestInterrupt(ctx context.Context, payload map[string]any) (*ApprovalRequest, error) {
	interruptID := "i_" + uuid.New().String()
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
		InterruptID: interruptID, Type: interruptType, RunID: runID, UserID: userID,
		ThreadID: threadID, NodeName: nodeName, ToolName: toolName, Arguments: arguments,
		Payload: payload, RiskLevel: riskLevel, Message: message, Status: StatusPending,
		CreatedAt: time.Now(),
	}
	cp := memory.Checkpoint{
		UserID: userID, ThreadID: threadID, RunID: runID,
		State:       []byte(fmt.Sprintf(`{"interrupt_id":"%s"}`, interruptID)),
		Interrupted: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := m.checkpoint.Save(ctx, cp); err != nil {
		return nil, fmt.Errorf("failed to save checkpoint: %w", err)
	}
	if err := m.SaveContext(ctx, req); err != nil {
		return nil, err
	}
	return cloneApproval(req), nil
}

func (m *InterruptManager) GetPendingForUser(ctx context.Context, userID string) []*ApprovalRequest {
	result, _ := m.GetPendingForUserE(ctx, userID)
	return result
}

func (m *InterruptManager) GetPendingForUserE(ctx context.Context, userID string) ([]*ApprovalRequest, error) {
	m.mu.RLock()
	store := m.store
	if store == nil {
		defer m.mu.RUnlock()
		var result []*ApprovalRequest
		for _, req := range m.pending {
			if req.Status == StatusPending && req.UserID == userID {
				result = append(result, cloneApproval(req))
			}
		}
		return result, nil
	}
	m.mu.RUnlock()
	return store.ListPending(ctx, userID)
}

func (m *InterruptManager) GetRequest(interruptID string) (*ApprovalRequest, bool) {
	req, ok, _ := m.GetRequestContext(context.Background(), interruptID)
	return req, ok
}

func (m *InterruptManager) GetRequestContext(ctx context.Context, interruptID string) (*ApprovalRequest, bool, error) {
	m.mu.RLock()
	store := m.store
	if store == nil {
		defer m.mu.RUnlock()
		req, ok := m.pending[interruptID]
		return cloneApproval(req), ok, nil
	}
	m.mu.RUnlock()
	return store.Get(ctx, interruptID)
}

func (m *InterruptManager) UseFile(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	records := make(map[string]*ApprovalRequest)
	if err := storage.LoadJSONFile(path, &records); err != nil {
		return err
	}
	m.path = path
	if records != nil {
		m.pending = records
	}
	return nil
}

func cloneApproval(req *ApprovalRequest) *ApprovalRequest {
	if req == nil {
		return nil
	}
	data, _ := json.Marshal(req)
	var copied ApprovalRequest
	_ = json.Unmarshal(data, &copied)
	return &copied
}

func (m *InterruptManager) putLocked(req *ApprovalRequest) error {
	if _, err := json.Marshal(req); err != nil {
		return err
	}
	records := make(map[string]*ApprovalRequest, len(m.pending)+1)
	for k, v := range m.pending {
		records[k] = v
	}
	records[req.InterruptID] = cloneApproval(req)
	if m.path != "" {
		if err := storage.WriteJSONFileAtomic(m.path, records); err != nil {
			return err
		}
	}
	m.pending = records
	return nil
}

func (m *InterruptManager) Save(req *ApprovalRequest) error {
	return m.SaveContext(context.Background(), req)
}

func (m *InterruptManager) SaveContext(ctx context.Context, req *ApprovalRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		return m.store.Save(ctx, cloneApproval(req))
	}
	return m.putLocked(req)
}

// Claim moves a pending approval to running. In database mode the store owns
// the atomic compare-and-set; in memory/file mode the manager lock does.
func (m *InterruptManager) Claim(ctx context.Context, interruptID, userID string, decision ApprovalDecision, claimToken string) (*ApprovalRequest, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		return m.store.Claim(ctx, interruptID, userID, decision, claimToken)
	}
	req, ok := m.pending[interruptID]
	if !ok || req.UserID != userID {
		return nil, false, nil
	}
	copyReq := cloneApproval(req)
	if copyReq.Phase != "" || copyReq.Status != StatusPending {
		return copyReq, false, nil
	}
	now := time.Now()
	copyReq.Decision = &decision
	copyReq.DecidedAt = &now
	copyReq.Phase = "running"
	copyReq.ClaimToken = claimToken
	copyReq.Status = StatusRejected
	if decision.Approved {
		copyReq.Status = StatusApproved
	}
	if err := m.putLocked(copyReq); err != nil {
		return nil, false, err
	}
	return cloneApproval(copyReq), true, nil
}

func (m *InterruptManager) Complete(ctx context.Context, req *ApprovalRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		return m.store.Complete(ctx, cloneApproval(req))
	}
	current, ok := m.pending[req.InterruptID]
	if !ok || current.Phase != "running" || current.ClaimToken == "" || current.ClaimToken != req.ClaimToken {
		return fmt.Errorf("approval claim no longer owned: %s", req.InterruptID)
	}
	return m.putLocked(req)
}
