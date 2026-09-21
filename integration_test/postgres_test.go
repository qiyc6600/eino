package integration_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/app"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/memory"
	pgstore "github.com/example/agent-eino-demo/internal/postgres"
	"github.com/example/agent-eino-demo/internal/tools"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func postApprovalDecision(t *testing.T, serverURL, sessionID, interruptID string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/approvals/"+interruptID+"/decision", strings.NewReader(`{"approved":true,"reason":""}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+sessionID)
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func TestPostgresConcurrentMigration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	schemaName := "migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schemaName)); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schemaName))

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schemaName
	isolatedDSN := stdlib.RegisterConnConfig(config)
	defer stdlib.UnregisterConnConfig(isolatedDSN)

	const instances = 2
	start := make(chan struct{})
	backends := make(chan *pgstore.Backend, instances)
	errs := make(chan error, instances)
	var wg sync.WaitGroup
	for range instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			backend, err := pgstore.Open(ctx, isolatedDSN, 3, 1, time.Minute)
			if err != nil {
				errs <- err
				return
			}
			backends <- backend
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(backends)
	for err := range errs {
		t.Errorf("concurrent open: %v", err)
	}
	var opened []*pgstore.Backend
	for backend := range backends {
		opened = append(opened, backend)
	}
	defer func() {
		for _, backend := range opened {
			_ = backend.Close()
		}
	}()
	if len(opened) != instances {
		t.Fatalf("expected %d open backends, got %d", instances, len(opened))
	}

	var migrationCount int
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount == 0 {
		t.Fatal("expected at least one recorded migration")
	}
	var tableCount int
	if err := opened[1].DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=$1`, schemaName).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 12 {
		t.Fatalf("expected migration table plus 11 application tables, got %d", tableCount)
	}
	run := agent.ChatRunResult{RunID: "r_" + uuid.NewString(), Status: agent.StatusInterrupted, Events: []agent.Event{{ID: "e_1", Type: agent.EventModelCallStart}}}
	if err := opened[0].Runs.Save(ctx, "run-owner", run, time.Hour); err != nil {
		t.Fatalf("save run on first instance: %v", err)
	}
	loaded, ok, err := opened[1].Runs.Load(ctx, "run-owner", run.RunID)
	if err != nil || !ok || len(loaded.Events) != 1 || loaded.Status != agent.StatusInterrupted {
		t.Fatalf("load run on second instance: ok=%v result=%+v err=%v", ok, loaded, err)
	}
	if _, ok, err := opened[1].Runs.Load(ctx, "other-user", run.RunID); err != nil || ok {
		t.Fatalf("cross-user run access: ok=%v err=%v", ok, err)
	}
	run.Status = agent.StatusCompleted
	run.Events = append(run.Events, agent.Event{ID: "e_2", Type: agent.EventModelCallEnd})
	if err := opened[1].Runs.Save(ctx, "run-owner", run, time.Hour); err != nil {
		t.Fatalf("update resumed run on second instance: %v", err)
	}
	loaded, ok, err = opened[0].Runs.Load(ctx, "run-owner", run.RunID)
	if err != nil || !ok || len(loaded.Events) != 2 || loaded.Status != agent.StatusCompleted {
		t.Fatalf("load updated run on first instance: ok=%v result=%+v err=%v", ok, loaded, err)
	}
	if _, err := opened[0].DB.ExecContext(ctx, `UPDATE agent_runs SET expires_at=NOW()-INTERVAL '1 second' WHERE user_id=$1 AND run_id=$2`, "run-owner", run.RunID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := opened[1].Runs.Load(ctx, "run-owner", run.RunID); err != nil || ok {
		t.Fatalf("expired run still readable: ok=%v err=%v", ok, err)
	}
	if err := opened[1].Runs.Save(ctx, "run-owner", agent.ChatRunResult{RunID: "r_cleanup_" + uuid.NewString()}, time.Hour); err != nil {
		t.Fatal(err)
	}
	var expiredCount int
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_runs WHERE user_id=$1 AND run_id=$2`, "run-owner", run.RunID).Scan(&expiredCount); err != nil || expiredCount != 0 {
		t.Fatalf("expired run was not pruned: count=%d err=%v", expiredCount, err)
	}
	for i, backend := range opened {
		if err := backend.Ready(ctx); err != nil {
			t.Fatalf("backend %d is not ready after concurrent migration: %v", i, err)
		}
	}
	bootstrapServices := make([]*auth.Service, 0, len(opened))
	for _, backend := range opened {
		service, err := auth.NewServiceWithUserStore(backend.Sessions, backend.Users, auth.NewRBACManager())
		if err != nil {
			t.Fatal(err)
		}
		bootstrapServices = append(bootstrapServices, service)
	}
	bootstrapErrors := make(chan error, len(bootstrapServices))
	for _, service := range bootstrapServices {
		go func(service *auth.Service) {
			bootstrapErrors <- service.EnsureBootstrapAdmin(ctx, "pg-admin", "postgres-admin-password")
		}(service)
	}
	for range bootstrapServices {
		if err := <-bootstrapErrors; err != nil {
			t.Fatalf("concurrent administrator bootstrap: %v", err)
		}
	}
	if _, err := bootstrapServices[1].Login(ctx, "pg-admin", "postgres-admin-password"); err != nil {
		t.Fatalf("login with concurrently bootstrapped administrator: %v", err)
	}
	policy := auth.LoginRatePolicy{MaxFailures: 3, Window: time.Minute, Lockout: time.Minute}
	for i, service := range bootstrapServices {
		limiter, err := auth.NewLoginLimiter(opened[i].LoginLimits, policy)
		if err != nil {
			t.Fatal(err)
		}
		service.SetLoginLimiter(limiter)
	}
	for i := 0; i < policy.MaxFailures; i++ {
		_, err := bootstrapServices[i%len(bootstrapServices)].LoginWithSource(ctx, "pg-admin", "wrong", "192.0.2.1")
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("failure %d: %v", i, err)
		}
	}
	if _, err := bootstrapServices[1].LoginWithSource(ctx, "pg-admin", "postgres-admin-password", "192.0.2.2"); !errors.Is(err, auth.ErrLoginRateLimited) {
		t.Fatalf("cross-instance username lockout was bypassed: %v", err)
	}
	if _, err := bootstrapServices[1].LoginWithSource(ctx, "other-user", "wrong", "192.0.2.1"); !errors.Is(err, auth.ErrLoginRateLimited) {
		t.Fatalf("cross-instance source lockout was bypassed: %v", err)
	}
	var limitCount int
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_login_rate_limits`).Scan(&limitCount); err != nil || limitCount != 2 {
		t.Fatalf("expected two hashed limit buckets: count=%d err=%v", limitCount, err)
	}
	concurrentPolicy := auth.LoginRatePolicy{MaxFailures: 50, Window: time.Minute, Lockout: time.Minute}
	const failures = 16
	var failureWG sync.WaitGroup
	for i := 0; i < failures; i++ {
		failureWG.Add(1)
		go func(index int) {
			defer failureWG.Done()
			if err := opened[index%len(opened)].LoginLimits.RecordFailure(ctx, []string{"concurrent-source", "concurrent-user"}, time.Now(), concurrentPolicy); err != nil {
				t.Errorf("concurrent failed login: %v", err)
			}
		}(i)
	}
	failureWG.Wait()
	var concurrentCount int
	if err := opened[1].DB.QueryRowContext(ctx, `SELECT MIN(failure_count) FROM agent_login_rate_limits WHERE limit_key IN ('concurrent-source','concurrent-user')`).Scan(&concurrentCount); err != nil || concurrentCount != failures {
		t.Fatalf("concurrent failures lost updates: count=%d err=%v", concurrentCount, err)
	}

	legacyPassword := "legacy-password"
	legacyDigest := sha256.Sum256([]byte(legacyPassword))
	legacyUser := auth.User{
		ID: "u_legacy", Username: "legacy", PasswordHash: fmt.Sprintf("%x", legacyDigest), Roles: []string{"visitor"},
	}
	if err := opened[0].Users.Create(ctx, legacyUser); err != nil {
		t.Fatal(err)
	}
	authService, err := auth.NewServiceWithUserStore(opened[0].Sessions, opened[0].Users, auth.NewRBACManager())
	if err != nil {
		t.Fatal(err)
	}
	login, err := authService.Login(ctx, legacyUser.Username, legacyPassword)
	if err != nil {
		t.Fatalf("legacy PostgreSQL login: %v", err)
	}
	upgraded, ok, err := opened[1].Users.GetByID(ctx, legacyUser.ID)
	if err != nil || !ok || !strings.HasPrefix(upgraded.PasswordHash, "$argon2id$") {
		t.Fatalf("legacy PostgreSQL hash was not upgraded: ok=%v hash=%q err=%v", ok, upgraded.PasswordHash, err)
	}
	if err := authService.UpdateUserRoles(ctx, legacyUser.ID, []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := authService.ValidateSession(ctx, login.SessionID); err == nil {
		t.Fatal("role update did not revoke PostgreSQL session")
	}
	stale := auth.Session{ID: "s_stale", UserID: legacyUser.ID, Username: legacyUser.Username, Roles: []string{"visitor"}}
	if err := opened[1].Sessions.Create(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := authService.ValidateSession(ctx, stale.ID); err == nil {
		t.Fatal("stale cross-instance role session was accepted")
	}

	// Exercise the application wiring and authenticated HTTP endpoint through
	// two independent app instances, not just the underlying SQL store.
	cfg := app.LoadConfig()
	cfg.DatabaseURL = isolatedDSN
	cfg.ModelProvider = "mock"
	cfg.BootstrapAdminUsername, cfg.BootstrapAdminPassword = "", ""
	cfg.UserStoreKind, cfg.SessionStoreKind = "postgres", "postgres"
	cfg.CheckpointStoreKind, cfg.MemoryStoreKind = "postgres", "postgres"
	cfg.ThreadStoreKind, cfg.ApprovalStoreKind, cfg.BusinessStoreKind = "postgres", "postgres", "postgres"
	first, second := app.NewApp(cfg), app.NewApp(cfg)
	defer first.Close()
	defer second.Close()
	server1 := httptest.NewServer(first.Router.Handler())
	server2 := httptest.NewServer(second.Router.Handler())
	defer server1.Close()
	defer server2.Close()
	session := doLogin(t, server1.URL, legacyUser.Username, legacyPassword)
	chat := doPost(t, server1.URL, "/api/agent/chat", session, map[string]string{"threadId": "cross-instance", "message": "1+1等于多少"})
	var chatBody struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	decodeErr := json.NewDecoder(chat.Body).Decode(&chatBody)
	chat.Body.Close()
	if chat.StatusCode != http.StatusOK || decodeErr != nil || chatBody.Status != "completed" || chatBody.RunID == "" {
		t.Fatalf("chat through first instance: status=%d body=%+v decode=%v", chat.StatusCode, chatBody, decodeErr)
	}
	eventsResponse := doGet(t, server2.URL, "/api/agent/runs/"+chatBody.RunID+"/events", session)
	var events []agent.Event
	decodeErr = json.NewDecoder(eventsResponse.Body).Decode(&events)
	eventsResponse.Body.Close()
	if eventsResponse.StatusCode != http.StatusOK || decodeErr != nil || len(events) == 0 {
		t.Fatalf("events through second instance: status=%d count=%d decode=%v", eventsResponse.StatusCode, len(events), decodeErr)
	}
	if _, err := first.AuthSvc.CreateUser(ctx, "run-other", "run-other-pass-1234", []string{"visitor"}); err != nil {
		t.Fatal(err)
	}
	otherSession := doLogin(t, server2.URL, "run-other", "run-other-pass-1234")
	otherResponse := doGet(t, server2.URL, "/api/agent/runs/"+chatBody.RunID+"/events", otherSession)
	otherResponse.Body.Close()
	if otherResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("other user saw run events: status=%d", otherResponse.StatusCode)
	}
	interrupted := chatJSON(t, server1.URL, session, map[string]any{
		"threadId": "cross-instance-approval", "message": "查询北京天气", "confirmBeforeExecute": true,
	})
	if interrupted["status"] != "interrupted" {
		t.Fatalf("expected approval interrupt: %v", interrupted)
	}
	approvalRunID, _ := interrupted["run_id"].(string)
	interrupt, _ := interrupted["interrupt"].(map[string]any)
	interruptID, _ := interrupt["interrupt_id"].(string)
	initialEvents, _ := interrupted["events"].([]any)
	if approvalRunID == "" || interruptID == "" || len(initialEvents) == 0 {
		t.Fatalf("missing run, approval or initial events: %v", interrupted)
	}
	checkpoint, ok, err := opened[1].Checkpoints.Load(ctx, memory.CheckpointKey{UserID: legacyUser.ID, ThreadID: "cross-instance-approval", RunID: approvalRunID})
	if err != nil || !ok || !checkpoint.Interrupted {
		t.Fatalf("published checkpoint missing on second instance: ok=%v err=%v", ok, err)
	}
	threadMessages, err := opened[1].Threads.CopyContext(ctx, legacyUser.ID, "cross-instance-approval")
	if err != nil || len(threadMessages) == 0 {
		t.Fatalf("published thread missing on second instance: messages=%d err=%v", len(threadMessages), err)
	}
	publishedApproval, ok, err := opened[1].Approvals.Get(ctx, interruptID)
	if err != nil || !ok || publishedApproval.Status != hitl.StatusPending {
		t.Fatalf("published approval missing on second instance: ok=%v err=%v", ok, err)
	}
	decisionRequest, err := http.NewRequest(http.MethodPost, server2.URL+"/api/approvals/"+interruptID+"/decision", strings.NewReader(`{"approved":true,"reason":""}`))
	if err != nil {
		t.Fatal(err)
	}
	decisionRequest.Header.Set("Authorization", "Bearer "+session)
	decisionRequest.Header.Set("Content-Type", "application/json")
	decisionResponse, err := http.DefaultClient.Do(decisionRequest)
	if err != nil {
		t.Fatal(err)
	}
	var decisionBody map[string]any
	decodeErr = json.NewDecoder(decisionResponse.Body).Decode(&decisionBody)
	decisionResponse.Body.Close()
	if decisionResponse.StatusCode != http.StatusOK || decodeErr != nil || decisionBody["status"] != "completed" {
		t.Fatalf("resume through second instance: status=%d body=%v decode=%v", decisionResponse.StatusCode, decisionBody, decodeErr)
	}
	resumedEvents := doGet(t, server1.URL, "/api/agent/runs/"+approvalRunID+"/events", session)
	events = nil
	decodeErr = json.NewDecoder(resumedEvents.Body).Decode(&events)
	resumedEvents.Body.Close()
	if resumedEvents.StatusCode != http.StatusOK || decodeErr != nil || len(events) <= len(initialEvents) {
		t.Fatalf("resumed events were not appended: status=%d initial=%d final=%d decode=%v", resumedEvents.StatusCode, len(initialEvents), len(events), decodeErr)
	}
	for i, event := range events {
		if event.ID != fmt.Sprintf("e_%d", i+1) || event.RunID != approvalRunID {
			t.Fatalf("event %d is not continuous: %+v", i, event)
		}
	}

	// Force the last SQL statement in publication to fail. The earlier
	// checkpoint and thread writes must roll back with it.
	if _, err := opened[0].DB.ExecContext(ctx, `ALTER TABLE agent_approvals ADD CONSTRAINT test_reject_pending CHECK (status <> 'pending')`); err != nil {
		t.Fatal(err)
	}
	defer opened[0].DB.ExecContext(context.Background(), `ALTER TABLE agent_approvals DROP CONSTRAINT IF EXISTS test_reject_pending`)
	failed := chatJSON(t, server1.URL, session, map[string]any{
		"threadId": "tx-failure", "message": "查询北京天气", "confirmBeforeExecute": true,
	})
	if failed["status"] != "error" {
		t.Fatalf("expected publication failure: %v", failed)
	}
	for _, table := range []string{"agent_checkpoints", "agent_threads", "agent_approvals"} {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE user_id=$1 AND thread_id=$2", table)
		if err := opened[1].DB.QueryRowContext(ctx, query, legacyUser.ID, "tx-failure").Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s was not rolled back: count=%d err=%v", table, count, err)
		}
	}
	if _, err := opened[0].DB.ExecContext(ctx, `ALTER TABLE agent_approvals DROP CONSTRAINT test_reject_pending`); err != nil {
		t.Fatal(err)
	}
	retry := chatJSON(t, server2.URL, session, map[string]any{
		"threadId": "tx-failure", "message": "查询北京天气", "confirmBeforeExecute": true,
	})
	if retry["status"] != "interrupted" {
		t.Fatalf("same thread could not retry after rollback: %v", retry)
	}

	// A node approval can produce another, tool-level approval. Both the old
	// receipt and the new pending interrupt must be visible together.
	plan := chatJSON(t, server1.URL, session, map[string]any{
		"threadId": "resume-chain", "message": "删除订单 A-1001", "confirmBeforeExecute": true,
	})
	planRunID, _ := plan["run_id"].(string)
	planInterrupt, _ := plan["interrupt"].(map[string]any)
	planID, _ := planInterrupt["interrupt_id"].(string)
	if plan["status"] != "interrupted" || planID == "" {
		t.Fatalf("plan did not interrupt: %v", plan)
	}
	decisionStatus, followOn := postApprovalDecision(t, server2.URL, session, planID)
	followOnInterrupt, _ := followOn["interrupt"].(map[string]any)
	followOnID, _ := followOnInterrupt["interrupt_id"].(string)
	if decisionStatus != http.StatusOK || followOn["status"] != "interrupted" || followOnID == "" || followOnID == planID || followOn["runId"] != planRunID {
		t.Fatalf("plan did not produce a follow-on approval: status=%d result=%v", decisionStatus, followOn)
	}
	oldApproval, ok, err := opened[0].Approvals.Get(ctx, planID)
	if err != nil || !ok || oldApproval.Phase != "finished" || len(oldApproval.Result) == 0 {
		t.Fatalf("prior approval has no receipt: ok=%v approval=%+v err=%v", ok, oldApproval, err)
	}
	nextApproval, ok, err := opened[0].Approvals.Get(ctx, followOnID)
	if err != nil || !ok || nextApproval.Status != hitl.StatusPending {
		t.Fatalf("next approval is missing: ok=%v approval=%+v err=%v", ok, nextApproval, err)
	}
	chainRun, ok, err := opened[0].Runs.Load(ctx, legacyUser.ID, planRunID)
	if err != nil || !ok || chainRun.Status != agent.StatusInterrupted || chainRun.Interrupt == nil || chainRun.Interrupt.InterruptID != followOnID {
		t.Fatalf("run result is out of sync with next approval: ok=%v run=%+v err=%v", ok, chainRun, err)
	}

	// Reject the last write of a completed resume. The thread update and
	// approval receipt must roll back while the claim stays running.
	failedPlan := chatJSON(t, server1.URL, session, map[string]any{
		"threadId": "resume-failure", "message": "查询北京天气", "confirmBeforeExecute": true,
	})
	failedRunID, _ := failedPlan["run_id"].(string)
	failedInterrupt, _ := failedPlan["interrupt"].(map[string]any)
	failedID, _ := failedInterrupt["interrupt_id"].(string)
	if failedPlan["status"] != "interrupted" || failedID == "" {
		t.Fatalf("expected initial approval: %v", failedPlan)
	}
	var threadBefore string
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT messages::text FROM agent_threads WHERE user_id=$1 AND thread_id=$2`, legacyUser.ID, "resume-failure").Scan(&threadBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := opened[0].DB.ExecContext(ctx, `ALTER TABLE agent_runs ADD CONSTRAINT test_reject_completed CHECK (result->>'status' <> 'completed') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer opened[0].DB.ExecContext(context.Background(), `ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS test_reject_completed`)
	decisionStatus, failedDecision := postApprovalDecision(t, server2.URL, session, failedID)
	if decisionStatus != http.StatusBadRequest || !strings.Contains(fmt.Sprint(failedDecision["error"]), "保存执行结果失败") {
		t.Fatalf("expected failed resume publication: status=%d result=%v", decisionStatus, failedDecision)
	}
	failedApproval, ok, err := opened[0].Approvals.Get(ctx, failedID)
	if err != nil || !ok || failedApproval.Phase != "running" || len(failedApproval.Result) != 0 {
		t.Fatalf("claim was incorrectly finished: ok=%v approval=%+v err=%v", ok, failedApproval, err)
	}
	var threadAfter string
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT messages::text FROM agent_threads WHERE user_id=$1 AND thread_id=$2`, legacyUser.ID, "resume-failure").Scan(&threadAfter); err != nil || threadAfter != threadBefore {
		t.Fatalf("thread changed despite rollback: equal=%v err=%v", threadAfter == threadBefore, err)
	}
	storedRun, ok, err := opened[1].Runs.Load(ctx, legacyUser.ID, failedRunID)
	if err != nil || !ok || storedRun.Status != agent.StatusInterrupted {
		t.Fatalf("run result changed despite rollback: ok=%v run=%+v err=%v", ok, storedRun, err)
	}
	if _, err := opened[0].DB.ExecContext(ctx, `ALTER TABLE agent_runs DROP CONSTRAINT test_reject_completed`); err != nil {
		t.Fatal(err)
	}
	retryStatus, blockedRetry := postApprovalDecision(t, server1.URL, session, failedID)
	if retryStatus != http.StatusBadRequest || !strings.Contains(fmt.Sprint(blockedRetry["error"]), "核对") {
		t.Fatalf("running claim was replayed: %v", blockedRetry)
	}

	failedChain := chatJSON(t, server1.URL, session, map[string]any{
		"threadId": "resume-chain-failure", "message": "删除订单 A-1001", "confirmBeforeExecute": true,
	})
	failedChainRunID, _ := failedChain["run_id"].(string)
	failedChainInterrupt, _ := failedChain["interrupt"].(map[string]any)
	failedChainID, _ := failedChainInterrupt["interrupt_id"].(string)
	if failedChain["status"] != "interrupted" || failedChainID == "" {
		t.Fatalf("expected initial plan approval: %v", failedChain)
	}
	var chainThreadBefore string
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT messages::text FROM agent_threads WHERE user_id=$1 AND thread_id=$2`, legacyUser.ID, "resume-chain-failure").Scan(&chainThreadBefore); err != nil {
		t.Fatal(err)
	}
	chainCheckpointBefore, ok, err := opened[0].Checkpoints.Load(ctx, memory.CheckpointKey{UserID: legacyUser.ID, ThreadID: "resume-chain-failure", RunID: failedChainRunID})
	if err != nil || !ok {
		t.Fatalf("initial chain checkpoint missing: ok=%v err=%v", ok, err)
	}
	if _, err := opened[0].DB.ExecContext(ctx, `ALTER TABLE agent_runs ADD CONSTRAINT test_reject_interrupted CHECK (result->>'status' <> 'interrupted') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer opened[0].DB.ExecContext(context.Background(), `ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS test_reject_interrupted`)
	decisionStatus, failedChainDecision := postApprovalDecision(t, server2.URL, session, failedChainID)
	if decisionStatus != http.StatusBadRequest || !strings.Contains(fmt.Sprint(failedChainDecision["error"]), "保存执行结果失败") {
		t.Fatalf("expected failed chained resume: status=%d result=%v", decisionStatus, failedChainDecision)
	}
	var chainThreadAfter string
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT messages::text FROM agent_threads WHERE user_id=$1 AND thread_id=$2`, legacyUser.ID, "resume-chain-failure").Scan(&chainThreadAfter); err != nil || chainThreadAfter != chainThreadBefore {
		t.Fatalf("chained thread changed despite rollback: equal=%v err=%v", chainThreadAfter == chainThreadBefore, err)
	}
	chainCheckpointAfter, ok, err := opened[1].Checkpoints.Load(ctx, memory.CheckpointKey{UserID: legacyUser.ID, ThreadID: "resume-chain-failure", RunID: failedChainRunID})
	if err != nil || !ok || string(chainCheckpointAfter.State) != string(chainCheckpointBefore.State) {
		t.Fatalf("chained checkpoint changed despite rollback: ok=%v err=%v", ok, err)
	}
	var approvalCount int
	if err := opened[0].DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_approvals WHERE user_id=$1 AND thread_id=$2`, legacyUser.ID, "resume-chain-failure").Scan(&approvalCount); err != nil || approvalCount != 1 {
		t.Fatalf("follow-on approval leaked after rollback: count=%d err=%v", approvalCount, err)
	}
	chainClaim, ok, err := opened[1].Approvals.Get(ctx, failedChainID)
	if err != nil || !ok || chainClaim.Phase != "running" || len(chainClaim.Result) != 0 {
		t.Fatalf("chained claim incorrectly finished: ok=%v approval=%+v err=%v", ok, chainClaim, err)
	}
	if _, err := opened[0].DB.ExecContext(ctx, `ALTER TABLE agent_runs DROP CONSTRAINT test_reject_interrupted`); err != nil {
		t.Fatal(err)
	}
	retryStatus, blockedRetry = postApprovalDecision(t, server1.URL, session, failedChainID)
	if retryStatus != http.StatusBadRequest || !strings.Contains(fmt.Sprint(blockedRetry["error"]), "核对") {
		t.Fatalf("chained running claim was replayed: %v", blockedRetry)
	}
}

// Set TEST_DATABASE_URL to run this test against a disposable PostgreSQL
// database. Rows use unique IDs and are removed after the test.
func TestPostgresStoresAndAtomicApproval(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	one, err := pgstore.Open(ctx, dsn, 5, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := pgstore.Open(ctx, dsn, 5, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()

	suffix := uuid.New().String()
	userID, username := "test-user-"+suffix, "tester-"+suffix
	threadID, runID := "test-thread-"+suffix, "test-run-"+suffix
	interruptID := "test-interrupt-" + suffix
	orderID, deleteKey := "test-order-"+suffix, "delete-key-"+suffix
	emailKey := "email-key-" + suffix
	defer func() {
		_, _ = one.DB.Exec(`DELETE FROM agent_tool_effects WHERE idempotency_key=$1`, deleteKey)
		_, _ = one.DB.Exec(`DELETE FROM agent_email_records WHERE idempotency_key=$1`, emailKey)
		_, _ = one.DB.Exec(`DELETE FROM agent_orders WHERE user_id=$1 AND order_id=$2`, userID, orderID)
		_, _ = one.DB.Exec(`DELETE FROM agent_approvals WHERE interrupt_id=$1`, interruptID)
		_, _ = one.DB.Exec(`DELETE FROM agent_checkpoints WHERE user_id=$1`, userID)
		_, _ = one.DB.Exec(`DELETE FROM agent_memories WHERE user_id=$1`, userID)
		_, _ = one.DB.Exec(`DELETE FROM agent_threads WHERE user_id=$1`, userID)
		_, _ = one.DB.Exec(`DELETE FROM agent_sessions WHERE user_id=$1`, userID)
		_, _ = one.DB.Exec(`DELETE FROM agent_users WHERE id=$1`, userID)
	}()

	account := auth.User{ID: userID, Username: username, PasswordHash: "test-hash", Roles: []string{"visitor"}}
	if err := one.Users.Create(ctx, account); err != nil {
		t.Fatal(err)
	}
	if loaded, ok, err := two.Users.GetByUsername(ctx, username); err != nil || !ok || loaded.ID != userID {
		t.Fatalf("user round trip: ok=%v loaded=%+v err=%v", ok, loaded, err)
	}
	if err := two.Users.UpdateRoles(ctx, userID, []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if loaded, ok, err := one.Users.GetByID(ctx, userID); err != nil || !ok || len(loaded.Roles) != 1 || loaded.Roles[0] != "admin" {
		t.Fatalf("role update: ok=%v loaded=%+v err=%v", ok, loaded, err)
	}
	if err := two.Users.UpdatePasswordHash(ctx, userID, "upgraded-test-hash"); err != nil {
		t.Fatal(err)
	}
	if loaded, ok, err := one.Users.GetByID(ctx, userID); err != nil || !ok || loaded.PasswordHash != "upgraded-test-hash" {
		t.Fatalf("password hash update: ok=%v loaded=%+v err=%v", ok, loaded, err)
	}
	authCtx := auth.WithAuthContext(ctx, &auth.AuthContext{UserID: userID})
	if err := one.Orders.Seed(ctx, []tools.Order{{ID: orderID, UserID: userID, Status: "pending", Amount: "10", Desc: "integration"}}); err != nil {
		t.Fatal(err)
	}
	deleted, replayed, err := one.Orders.DeleteContext(authCtx, userID, orderID, deleteKey)
	if err != nil || !deleted || replayed {
		t.Fatalf("first order delete: deleted=%v replayed=%v err=%v", deleted, replayed, err)
	}
	deleted, replayed, err = two.Orders.DeleteContext(authCtx, userID, orderID, deleteKey)
	if err != nil || !deleted || !replayed {
		t.Fatalf("replayed order delete: deleted=%v replayed=%v err=%v", deleted, replayed, err)
	}
	email := tools.SentEmailRecord{UserID: userID, To: "test@example.com", Subject: "integration", Body: "body", RunID: runID, SentAt: time.Now()}
	if created, err := one.Emails.RecordContext(authCtx, email, emailKey); err != nil || !created {
		t.Fatalf("first email record: created=%v err=%v", created, err)
	}
	if created, err := two.Emails.RecordContext(authCtx, email, emailKey); err != nil || created {
		t.Fatalf("duplicate email record: created=%v err=%v", created, err)
	}

	session := auth.Session{ID: "session-" + suffix, UserID: userID, Username: username, Roles: []string{"admin"}, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := one.Sessions.Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	if loaded, ok, err := two.Sessions.Get(ctx, session.ID); err != nil || !ok || loaded.UserID != userID {
		t.Fatalf("session round trip: ok=%v loaded=%+v err=%v", ok, loaded, err)
	}
	if err := two.Sessions.DeleteByUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := one.Sessions.Get(ctx, session.ID); err != nil || ok {
		t.Fatalf("session revocation: ok=%v err=%v", ok, err)
	}

	if err := one.Threads.Create(userID, threadID); err != nil {
		t.Fatal(err)
	}
	if err := one.Threads.Append(userID, threadID, schema.UserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	unlock, err := one.Threads.LockThread(ctx, userID, threadID)
	if err != nil {
		t.Fatalf("thread advisory lock: %v", err)
	}
	unlock()
	if messages, err := two.Threads.CopyContext(ctx, userID, threadID); err != nil || len(messages) != 1 {
		t.Fatalf("thread round trip: %d messages, err=%v", len(messages), err)
	}

	entry := memory.MemoryEntry{UserID: userID, Key: "language", Value: "Go", CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339)}
	if err := one.Memories.Put(authCtx, entry); err != nil {
		t.Fatal(err)
	}
	if loaded, ok, err := two.Memories.Get(authCtx, userID, entry.Key); err != nil || !ok || loaded.Value != "Go" {
		t.Fatalf("memory round trip: ok=%v loaded=%+v err=%v", ok, loaded, err)
	}
	cp := memory.Checkpoint{UserID: userID, ThreadID: threadID, RunID: runID, State: []byte(`{"step":1}`), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := one.Checkpoints.Save(authCtx, cp); err != nil {
		t.Fatal(err)
	}
	if loaded, ok, err := two.Checkpoints.Load(authCtx, memory.CheckpointKey{UserID: userID, ThreadID: threadID, RunID: runID}); err != nil || !ok || string(loaded.State) != string(cp.State) {
		t.Fatalf("checkpoint round trip: ok=%v loaded=%+v err=%v", ok, loaded, err)
	}

	approval := &hitl.ApprovalRequest{InterruptID: interruptID, UserID: userID, ThreadID: threadID, RunID: runID, Status: hitl.StatusPending, CreatedAt: time.Now(), State: []byte(`{"step":1}`)}
	if err := one.Approvals.Save(ctx, approval); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	winners := make(chan bool, 2)
	for i, store := range []*pgstore.ApprovalStore{one.Approvals, two.Approvals} {
		wg.Add(1)
		go func(index int, approvals *pgstore.ApprovalStore) {
			defer wg.Done()
			_, won, err := approvals.Claim(ctx, interruptID, userID, hitl.ApprovalDecision{Approved: true}, "claim-"+string(rune('a'+index)))
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			winners <- won
		}(i, store)
	}
	wg.Wait()
	close(winners)
	count := 0
	for won := range winners {
		if won {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one approval claimant, got %d", count)
	}
}

// TestPostgresThreadAppendAndPrune validates the SQL behind the guarded append
// and the retention sweep against a real database. The unit tests use sqlmock,
// which checks the statement text but not whether PostgreSQL accepts it or
// behaves as intended.
func TestPostgresThreadAppendAndPrune(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := pgstore.Open(ctx, dsn, 5, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	suffix := uuid.NewString()
	userID := "test-append-" + suffix
	threadID := "t-" + suffix
	staleID := "t-stale-" + suffix
	defer func() {
		_, _ = backend.DB.Exec(`DELETE FROM agent_threads WHERE user_id=$1`, userID)
	}()

	// Migration 004 must have applied: both the recorded version and the index
	// the retention sweep relies on.
	var version int
	if err := backend.DB.QueryRowContext(ctx,
		`SELECT version FROM agent_schema_migrations WHERE name='thread_retention_index'`).Scan(&version); err != nil {
		t.Fatalf("migration 004 is not recorded: %v", err)
	}
	if version != 4 {
		t.Fatalf("thread_retention_index recorded as version %d, want 4", version)
	}
	var indexName string
	if err := backend.DB.QueryRowContext(ctx,
		`SELECT indexname FROM pg_indexes WHERE tablename='agent_threads' AND indexname='agent_threads_user_updated_idx'`).Scan(&indexName); err != nil {
		t.Fatalf("retention index missing — migration 004 did not apply: %v", err)
	}

	store := backend.Threads

	// First turn: the guard expects an empty history and inserts the row.
	if err := store.AppendHistoryContext(ctx, userID, threadID, 0, []*schema.Message{schema.UserMessage("第一轮")}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if got := len(store.Copy(userID, threadID)); got != 1 {
		t.Fatalf("after first append: %d messages, want 1", got)
	}

	// Second turn: a matching length appends in place.
	if err := store.AppendHistoryContext(ctx, userID, threadID, 1, []*schema.Message{schema.AssistantMessage("回复", nil)}); err != nil {
		t.Fatalf("second append: %v", err)
	}
	msgs := store.Copy(userID, threadID)
	if len(msgs) != 2 || msgs[0].Content != "第一轮" || msgs[1].Content != "回复" {
		t.Fatalf("append produced the wrong history: %+v", msgs)
	}

	// A stale expected length must be refused, and refusing must not modify the
	// stored history — this is what protects checkpoints written before the guard
	// existed.
	err = store.AppendHistoryContext(ctx, userID, threadID, 0, []*schema.Message{schema.UserMessage("不该写入")})
	if !errors.Is(err, agent.ErrThreadAppendMismatch) {
		t.Fatalf("expected ErrThreadAppendMismatch for a stale prefix, got %v", err)
	}
	if got := len(store.Copy(userID, threadID)); got != 2 {
		t.Fatalf("a refused append modified the history: %d messages", got)
	}

	// Retention: a thread touched two days ago is swept, a fresh one survives.
	if err := store.AppendHistoryContext(ctx, userID, staleID, 0, []*schema.Message{schema.UserMessage("旧会话")}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.DB.ExecContext(ctx,
		`UPDATE agent_threads SET updated_at = NOW() - INTERVAL '48 hours' WHERE user_id=$1 AND thread_id=$2`,
		userID, staleID); err != nil {
		t.Fatal(err)
	}
	removed, err := store.PruneThreadsBefore(ctx, userID, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 1 {
		t.Fatalf("prune removed %d threads, want 1", removed)
	}
	ids := store.List(userID)
	if len(ids) != 1 || ids[0] != threadID {
		t.Fatalf("wrong thread survived the sweep: %v", ids)
	}
}
