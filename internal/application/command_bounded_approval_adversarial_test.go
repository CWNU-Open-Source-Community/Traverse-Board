package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

// Both reviewers must observe the real empty SQLite list before either may
// create a grant. All other reads, transactions and native execution are real.
type concurrentCommandGrantStore struct {
	*store.SQLiteStore
	entered chan struct{}
	release chan struct{}
	reads   *atomic.Int32
}

func (s *concurrentCommandGrantStore) ListSessionGrants(ctx context.Context, filter approval.GrantListFilter) ([]approval.SessionGrant, error) {
	values, err := s.SQLiteStore.ListSessionGrants(ctx, filter)
	if err == nil && s.reads.Add(1) <= 2 {
		if len(values) != 0 {
			return nil, fmt.Errorf("first-review fixture expected an empty list, got %d", len(values))
		}
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return values, err
}

type staleCommandGrantLookupStore struct {
	*store.SQLiteStore
	lookups      atomic.Int32
	lists        atomic.Int32
	entered      chan struct{}
	release      chan struct{}
	firstDecided chan struct{}
}

func (s *staleCommandGrantLookupStore) FindActiveSessionGrant(ctx context.Context, query approval.GrantQuery) (approval.SessionGrant, bool, error) {
	value, found, err := s.SQLiteStore.FindActiveSessionGrant(ctx, query)
	if err == nil && s.lookups.Add(1) <= 2 {
		if found {
			return value, found, fmt.Errorf("fixture expected both initial exact lookups to be empty")
		}
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return value, false, ctx.Err()
		}
	}
	return value, found, err
}

func (s *staleCommandGrantLookupStore) ListSessionGrants(ctx context.Context, filter approval.GrantListFilter) ([]approval.SessionGrant, error) {
	if s.lists.Add(1) == 2 {
		select {
		case <-s.firstDecided:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.SQLiteStore.ListSessionGrants(ctx, filter)
}

func TestCommandBoundedApprovalStaleLookupDoesNotChangeDecision(t *testing.T) {
	for _, sameCall := range []bool{true, false} {
		t.Run(fmt.Sprintf("same_call_%t", sameCall), func(t *testing.T) {
			f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
			requests := recordTwoBoundedCommands(t, f)
			if sameCall {
				requests[1] = requests[0]
			} else {
				requests[1].Reason = "independent operator review of the second exact command"
			}
			st := &staleCommandGrantLookupStore{SQLiteStore: f.st, entered: make(chan struct{}, 2), release: make(chan struct{}), firstDecided: make(chan struct{})}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			control := NewApprovalControlService(st, toolgateway.New(nil, f.checker), f.checker)
			type outcome struct {
				value DecideApprovalControlResult
				err   error
			}
			results := make(chan outcome, 2)
			for _, request := range requests {
				go func() { value, err := control.Decide(ctx, request); results <- outcome{value, err} }()
			}
			for i := 0; i < 2; i++ {
				select {
				case <-st.entered:
				case <-ctx.Done():
					t.Fatal("both exact lookups did not reach the empty-scope barrier", ctx.Err())
				}
			}
			close(st.release)
			first := <-results
			close(st.firstDecided)
			second := <-results
			if first.err != nil || second.err != nil || first.value.Grant == nil || second.value.Grant == nil {
				t.Fatalf("stale absence/list race changed an exact review: first=%v second=%v", first.err, second.err)
			}
			remaining := 0
			if sameCall {
				remaining = 1
			}
			grant, err := f.st.GetSessionGrant(ctx, first.value.Grant.ID)
			if err != nil || grant.ID != second.value.Grant.ID || grant.Generation != 1 || grant.MaxUses != 2 || grant.UsesRemaining != remaining {
				t.Fatalf("stale lookup changed the original grant: %+v %v", grant, err)
			}
			if sameCall != (first.value.Consumption.ID == second.value.Consumption.ID) {
				t.Fatal("separate exact calls/replays lost their consumption identity")
			}
			for _, request := range requests {
				replayed, err := control.Decide(ctx, request)
				if err != nil || !replayed.Replayed || replayed.Grant.ID != grant.ID || replayed.Grant.UsesRemaining != remaining {
					t.Fatalf("subsequent exact replay changed the scope: %+v %v", replayed, err)
				}
			}
			t.Logf("both exact lookups were empty; the second history list observed the first completed decision; sameCall=%t, one generation=1 grant, remaining=%d", sameCall, remaining)
		})
	}
}

func recordTwoBoundedCommands(t *testing.T, f *commandApprovalFixture) []DecideApprovalControlRequest {
	t.Helper()
	ctx := t.Context()
	permission, err := f.st.GetRunExecutionPermission(ctx, f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	advertised, err := f.supervisor.supervisorCommandRuntimeTools(ctx, f.turn.Run.ID, permission.Mode)
	if err != nil {
		t.Fatal(err)
	}
	var planned []llm.ToolCall
	for i := 1; i <= 2; i++ {
		raw, err := json.Marshal(boundedCommandInput(t, fmt.Sprint(i)))
		if err != nil {
			t.Fatal(err)
		}
		planned = append(planned, llm.ToolCall{ID: fmt.Sprintf("separate-exact-command-%d", i), Name: string(toolgateway.CommandRuntimeTool), Arguments: raw})
	}
	calls, err := prepareSupervisorToolCalls(planned, f.turn.Run.ID, f.turn.Checkpoint.NextTurn, 1,
		f.turn.Mode.Surface, f.turn.Mode.Phase, permission.Mode, false, false, supervisorToolOptions{CommandRuntime: advertised})
	if err != nil {
		t.Fatal(err)
	}
	calls, err = f.supervisor.bindCommandRuntimeCalls(ctx, calls)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, ToolRound: 0, TransportAttempt: 1, MaxAttempts: 1, Provider: "offline-two-command-fixture", Model: "fixture"}
	if _, err := f.st.RecordSupervisorModelStarted(ctx, f.turn.Checkpoint, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	f.turn.Checkpoint, err = f.st.RecordSupervisorModelCompleted(ctx, f.turn.Checkpoint, attempt, llm.ChatResponse{Provider: attempt.Provider, Model: attempt.Model, ToolCalls: calls})
	if err != nil {
		t.Fatal(err)
	}
	rounds, err := f.st.ListSupervisorToolRounds(ctx, f.turn.Checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 2 {
		t.Fatalf("two separate durable calls missing: %+v %v", rounds, err)
	}
	var requests []DecideApprovalControlRequest
	for _, call := range rounds[0].Calls {
		waiting, result, err := f.supervisor.preflightCommandApproval(ctx, call)
		if err != nil || !waiting || result != nil {
			t.Fatalf("separate command did not require exact review: %t %+v %v", waiting, result, err)
		}
		f.call = call
		requests = append(requests, boundedCommandRequest(t, f, 2))
	}
	if requests[0].ApprovalID == requests[1].ApprovalID || requests[0].OperationKey == requests[1].OperationKey || calls[0].Arguments == nil || string(calls[0].Arguments) == string(calls[1].Arguments) {
		t.Fatal("fixture did not create different exact commands and review keys")
	}
	return requests
}

func boundedFixtureDB(t *testing.T, f *commandApprovalFixture) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(f.path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCommandBoundedApprovalDistinctFirstCallsShareBudgetConcurrently(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	requests := recordTwoBoundedCommands(t, f)
	secondStore, err := store.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.Close() })
	st := &concurrentCommandGrantStore{SQLiteStore: f.st, entered: make(chan struct{}, 2), release: make(chan struct{}), reads: &atomic.Int32{}}
	other := &concurrentCommandGrantStore{SQLiteStore: secondStore, entered: st.entered, release: st.release, reads: st.reads}
	controls := []*ApprovalControlService{
		NewApprovalControlService(st, toolgateway.New(nil, f.checker), f.checker),
		NewApprovalControlService(other, toolgateway.New(nil, f.checker), f.checker),
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	type outcome struct {
		value DecideApprovalControlResult
		err   error
	}
	results := make(chan outcome, 2)
	for i, request := range requests {
		go func() { value, err := controls[i].Decide(ctx, request); results <- outcome{value, err} }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-st.entered:
		case <-ctx.Done():
			t.Fatal("both independent reviewers did not reach the list/create boundary", ctx.Err())
		}
	}
	close(st.release)
	var grantID, scope string
	ordinals := map[int]bool{}
	consumptions := map[string]bool{}
	created := 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil || result.value.Grant == nil || result.value.Consumption == nil {
			t.Fatalf("independent review failed: %+v %v", result.value, result.err)
		}
		if i == 0 {
			grantID, scope = result.value.Grant.ID, result.value.Grant.ScopeFingerprint
		}
		if result.value.Grant.ID != grantID {
			t.Fatal("concurrent first decisions doubled the scope budget")
		}
		ordinals[result.value.Consumption.UseOrdinal] = true
		consumptions[result.value.Consumption.ID] = true
		if result.value.GrantCreated {
			created++
		}
	}
	grant, err := f.st.GetSessionGrant(ctx, grantID)
	if err != nil || grant.MaxUses != 2 || grant.UsesRemaining != 0 || created != 1 || len(consumptions) != 2 || !ordinals[1] || !ordinals[2] {
		t.Fatalf("scope budget or independent consumptions changed: %+v created=%d ordinals=%v error=%v", grant, created, ordinals, err)
	}
	db := boundedFixtureDB(t, f)
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_session_grants WHERE run_id=? AND scope_fingerprint=?`, f.call.RunID, scope).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected exactly one stored grant, got %d: %v", count, err)
	}
	var index string
	if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_approval_session_grants_active_scope'`).Scan(&index); err != nil || !strings.Contains(index, "scope_fingerprint) WHERE status = 'active'") {
		t.Fatalf("real database lost its active-scope uniqueness: %q %v", index, err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
	if err != nil || string(data) != "12" {
		t.Fatalf("separately reviewed commands did not each execute once: %q %v", data, err)
	}
	t.Logf("two stores/connections and different calls/keys reached the empty-list barrier; one grant, two distinct consumptions, remaining=0; SQLite index: %s", index)
}

func TestCommandBoundedApprovalActiveScopeBeyondListLimitKeepsBudget(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	f.record(t, boundedCommandInput(t, "1"), 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
	request := boundedCommandRequest(t, f, 3)
	request.GrantTTLSeconds = 900
	first, err := control.Decide(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	query, err := f.st.GetCommandApprovalGrantScope(t.Context(), f.call.CallID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		input := boundedCommandInput(t, "other")
		input.ReviewScope.OtherRiskReason = fmt.Sprintf("unrelated recorded scope %d", i)
		scope, err := input.ReviewScope.RiskScope()
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.st.CreateSessionGrant(t.Context(), approval.CreateGrantRequest{
			SessionID: query.SessionID, WorkspaceID: query.WorkspaceID, ToolName: query.ToolName, ActionClass: query.ActionClass,
			Reason: "other operator scope", GrantedBy: "operator", IdempotencyKey: fmt.Sprintf("other-scope-%d", i),
			ScopeFingerprint: scope.Fingerprint, Generation: int64(i + 2), MaxUses: 2, TTL: 900 * time.Second,
			ModeSnapshotID: query.ModeSnapshotID, ModeRevision: query.ModeRevision, InteractionSnapshotID: query.InteractionSnapshotID, InteractionRevision: query.InteractionRevision,
			ExecutionProfileSnapshotID: query.ExecutionProfileSnapshotID, ExecutionProfileRevision: query.ExecutionProfileRevision,
			PermissionSnapshotID: query.PermissionSnapshotID, PermissionRevision: query.PermissionRevision, PermissionMode: query.PermissionMode,
			WorkspaceRootFingerprint: query.WorkspaceRootFingerprint, CapabilityGeneration: query.CapabilityGeneration})
		if err != nil {
			t.Fatal(i, err)
		}
	}
	filter := approval.GrantListFilter{RunID: f.call.RunID, ToolName: "command_runtime", Limit: 500}
	listed, err := f.st.ListSessionGrants(t.Context(), filter)
	if err != nil || len(listed) != 500 {
		t.Fatal(len(listed), err)
	}
	for i, value := range listed {
		if value.ID == first.Grant.ID {
			t.Fatal("fixture did not push the original active scope beyond the limit")
		}
		if i > 0 && (listed[i-1].UpdatedAt.Before(value.UpdatedAt) || (listed[i-1].UpdatedAt.Equal(value.UpdatedAt) && listed[i-1].ID < value.ID)) {
			t.Fatal("list is not ordered by updated_at DESC, id DESC")
		}
	}
	filter.Limit = 501
	if _, err := f.st.ListSessionGrants(t.Context(), filter); err == nil {
		t.Fatal("list did not enforce the hard 500 limit")
	}
	exact, found, err := f.st.FindActiveSessionGrant(t.Context(), query)
	if err != nil || !found || exact.ID != first.Grant.ID {
		t.Fatal("exact store lookup lost the older active scope", err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	f.record(t, boundedCommandInput(t, "2"), 2)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	request = boundedCommandRequest(t, f, 3)
	request.GrantTTLSeconds = 900
	second, decisionErr := control.Decide(t.Context(), request)
	stored, err := f.st.GetSessionGrant(t.Context(), first.Grant.ID)
	if err != nil || !stored.CreatedAt.Equal(first.Grant.CreatedAt) || !stored.ExpiresAt.Equal(*first.Grant.ExpiresAt) || stored.MaxUses != 3 {
		t.Fatalf("list truncation reset original TTL or cap: %+v %v", stored, err)
	}
	var count int
	if err := boundedFixtureDB(t, f).QueryRowContext(t.Context(), `SELECT COUNT(*) FROM approval_session_grants WHERE run_id=? AND scope_fingerprint=?`, f.call.RunID, query.ScopeFingerprint).Scan(&count); err != nil || count != 1 {
		t.Fatalf("list truncation created another scope grant: %d %v", count, err)
	}
	t.Logf("501 rows; original active scope omitted from list; exact lookup found it; decision error=%v; scope rows=%d original expiry retained=%t", decisionErr, count, stored.ExpiresAt.Equal(*first.Grant.ExpiresAt))
	if decisionErr != nil || second.Grant.ID != first.Grant.ID || second.GrantCreated || second.Grant.UsesRemaining != 1 || second.Consumption.UseOrdinal != 2 {
		t.Fatalf("older active scope cannot be reused without resetting limits: %+v %v", second, decisionErr)
	}
}

func TestCommandNonBoundedBackgroundSurvivesTwoAuthorityHeartbeats(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	input := commandApprovalNativeInput(t, true)
	input.Commands[0].TimeoutMilliseconds = 30000
	input.Commands[0].Arguments[1] = `setInterval(()=>require('fs').appendFileSync('ticks.txt','x'),50)`
	f.record(t, input, 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	f.decide(t, ApprovalControlApproveOnce)
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	record, err := f.st.GetApprovalByProposal(t.Context(), f.call.CallID)
	if err != nil || record.GrantID != "" {
		t.Fatal("fixture is not a nonbounded exact approval", err)
	}
	job := soleBoundedTestJob(t, f)
	renewed := job.OwnerRenewedAt
	ctx, cancel := context.WithTimeout(t.Context(), 16*time.Second)
	defer cancel()
	for seen := 0; seen < 2; {
		job, err = f.st.GetCommandRuntimeJob(ctx, job.ID)
		if err != nil || job.State != runner.CommandRuntimeJobRunning || !f.manager.OwnsActiveJob(job) {
			t.Fatalf("nonbounded job lost running authority: %+v %v", job, err)
		}
		if job.OwnerRenewedAt.After(renewed) {
			seen++
			renewed = job.OwnerRenewedAt
			t.Logf("successful ordinary authority heartbeat %d: %s", seen, renewed.Format(time.RFC3339Nano))
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("two real owner renewals were not observed", ctx.Err())
		}
	}
	data, err := os.ReadFile(filepath.Join(f.root, "ticks.txt"))
	if err != nil || len(data) < 2 {
		t.Fatal("normal native background process did not stay alive", err)
	}
}

func soleBoundedTestJob(t *testing.T, f *commandApprovalFixture) runner.CommandRuntimeJob {
	t.Helper()
	jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10})
	if err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("native background job was not running: %+v %v", jobs, err)
	}
	return jobs[0]
}

func TestCommandBoundedApprovalRunningTTLReapsNativeTree(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	input := commandApprovalNativeInput(t, true)
	input.ReviewScope = boundedCommandInput(t, "1").ReviewScope
	input.Commands[0].TimeoutMilliseconds = 30000
	input.Commands[0].Arguments[1] = `require('child_process').spawn(process.execPath,['-e',"setInterval(()=>require('fs').appendFileSync('child-ticks.txt','c'),50)"],{stdio:'ignore'});setInterval(()=>require('fs').appendFileSync('parent-ticks.txt','p'),50)`
	f.record(t, input, 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	request := boundedCommandRequest(t, f, 2)
	request.GrantTTLSeconds = 3
	decision, err := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker).Decide(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	job := soleBoundedTestJob(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	// The terminal transaction commits before the owner updates its in-memory
	// entry. Wait for both observable completion points before testing reap.
	for !job.State.Terminal() || f.manager.OwnsActiveJob(job) {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("expired bounded command was not reaped before its separate 30s timeout", ctx.Err())
		}
		job, err = f.st.GetCommandRuntimeJob(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if job.State != runner.CommandRuntimeJobInterrupted || !job.TreeReaped || job.CompletedAt == nil || time.Now().Before(*decision.Grant.ExpiresAt) || f.manager.OwnsActiveJob(job) {
		t.Fatalf("TTL did not revoke/reap running authority: %+v", job)
	}
	before := map[string]int{}
	for _, name := range []string{"parent-ticks.txt", "child-ticks.txt"} {
		data, err := os.ReadFile(filepath.Join(f.root, name))
		if err != nil || len(data) < 2 {
			t.Fatalf("actual native process never produced ticks before expiry: %s %v", name, err)
		}
		before[name] = len(data)
	}
	select {
	case <-time.After(350 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for name, size := range before {
		data, err := os.ReadFile(filepath.Join(f.root, name))
		if err != nil || len(data) != size {
			t.Fatalf("native tree kept producing effects after the interrupted receipt: %s %d -> %d %v", name, size, len(data), err)
		}
	}
	t.Logf("3s grant expired; native parent and child reaped with state=%s before 30s process timeout; tick counts=%v", job.State, before)
}
