package desktop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

type blockingWebFetchAuthorizationReconciler struct {
	started chan struct{}
}

func TestWebFetchAuthorizationReconcilerRequiresEveryStartupGate(t *testing.T) {
	enabled := ControlPlaneConfig{ControlToken: "control-token-present",
		RunExecutionEnabled: true, ApprovalControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true}}
	if !webFetchAuthorizationReconcilerEnabled(enabled) {
		t.Fatal("all enabled startup gates did not enable the reconciler")
	}
	for _, mutate := range []func(*ControlPlaneConfig){
		func(value *ControlPlaneConfig) { value.ControlToken = "" },
		func(value *ControlPlaneConfig) { value.RunExecutionEnabled = false },
		func(value *ControlPlaneConfig) { value.ApprovalControlEnabled = false },
		func(value *ControlPlaneConfig) {
			value.ExecutionPermissionCapabilities.OperatorApprovalEnabled = false
		},
	} {
		candidate := enabled
		mutate(&candidate)
		if webFetchAuthorizationReconcilerEnabled(candidate) {
			t.Fatalf("disabled startup gate enabled reconciler: %+v", candidate)
		}
	}
}

func (r blockingWebFetchAuthorizationReconciler) ReconcileWebFetchAuthorizations(
	ctx context.Context, _ string, _ int,
) (int, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestWebFetchAuthorizationReconcilerIsOwnedByProcessContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started, done := make(chan struct{}, 1), make(chan struct{})
	go func() {
		defer close(done)
		runWebFetchAuthorizationReconciler(ctx,
			blockingWebFetchAuthorizationReconciler{started: started}, time.Millisecond)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("process-owned Web fetch reconciler did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("process-owned Web fetch reconciler did not join after cancellation")
	}
}

type persistedWebFetchRecoveryFixture struct {
	run           domain.Run
	authorization domain.WebFetchAuthorization
	checkpoint    domain.SupervisorCheckpoint
	eventCount    int
}

func TestControlPlaneRestartWithApprovalGateOffDoesNotResumeDecidedWebFetch(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "web-fetch-restart-gate-off.db")
	seeded := seedDecidedWebFetchRecoveryFixture(t, databasePath)

	// This is intentionally a production-shaped restart: execution and operator
	// approval capabilities are present, but the HTTP approval control gate is
	// disabled. One missing gate must prevent the process-owned reconciler from
	// acquiring authority over persisted work.
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath:                      databasePath,
		ReadToken:                         desktopControlPlaneTestToken,
		ControlToken:                      desktopControlPlaneControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true,
		},
		RunExecutionEnabled:    true,
		ApprovalControlEnabled: false,
		AppVersion:             "desktop-web-fetch-restart-gate-off-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plane.Close() })

	if plane.webFetchReconcileCancel != nil || plane.webFetchReconcileDone != nil {
		t.Fatal("gates-off restart created a Web fetch authorization reconciler worker")
	}
	assertWebFetchRecoveryFixtureUnchanged(t, plane.stateStore, seeded)

	// The enabled reconciler performs its first pass immediately. Give any
	// accidentally-started background worker a bounded opportunity to mutate the
	// durable continuation, then prove the exact facts are still stationary.
	time.Sleep(50 * time.Millisecond)
	assertWebFetchRecoveryFixtureUnchanged(t, plane.stateStore, seeded)
}

func seedDecidedWebFetchRecoveryFixture(t *testing.T,
	databasePath string,
) persistedWebFetchRecoveryFixture {
	t.Helper()
	ctx := t.Context()
	state, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = state.Close()
		}
	})

	mission, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{
			Goal:    "resume one approved public HTTPS fetch after restart",
			Profile: "code", Surface: "code", Phase: "deliver",
			WorkspaceID: "ws-web-fetch-restart",
			Budget:      domain.Budget{MaxTurns: 4, MaxToolCalls: 8},
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	acquired, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "desktop-web-fetch-seed-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx, acquired.Lease,
		"fetch one public source")
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 3,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint,
		attempt); err != nil || !inserted {
		t.Fatalf("record model start: inserted=%t err=%v", inserted, err)
	}
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root Agent missing: found=%t err=%v", found, err)
	}
	authority, err := toolgateway.NewWebEvidenceCallAuthority(
		toolgateway.WebEvidenceCapabilityContext{
			RunID: run.ID, MissionID: mission.ID, SessionID: run.SessionID,
			RootAgentID: root.ID, WorkspaceID: "ws-web-fetch-restart",
			Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
			Role: domain.AgentRoleRoot, Profile: domain.ProfileCode,
			PermissionMode:     domain.RunExecutionPermissionConservative,
			PermissionRevision: 1, ModeRevision: 1, NetworkMode: "allowlist",
			AllowedTargets: []string{"search.example.com"}, ProviderAvailable: true,
			ProviderFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		})
	if err != nil {
		t.Fatal(err)
	}
	encodedAuthority, err := toolgateway.EncodeWebEvidenceCallAuthority(authority)
	if err != nil {
		t.Fatal(err)
	}
	const canonicalURL = "https://docs.example.com/reference"
	payload := json.RawMessage(`{"version":"web_fetch.v1","url":"` +
		canonicalURL + `"}`)
	operationKey := runmutation.SupervisorToolOperationKey(run.ID,
		turn.Checkpoint.NextTurn, "web_fetch", string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	completed := attempt
	completed.Outcome = llm.OutcomeSuccess
	checkpoint, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint,
		completed, llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: "web_fetch",
				Arguments: payload, Authority: encodedAuthority}}})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := approval.Fingerprint(domain.WebFetchAuthorizationProtocolVersion,
		run.ID, run.SessionID, callID, canonicalURL, "docs.example.com")
	authorization, authorized, err := state.PrepareWebFetchAuthorization(ctx,
		domain.WebFetchAuthorizationRequest{ID: "web-fetch-restart-authorization",
			RunID: run.ID, MissionID: mission.ID, SessionID: run.SessionID,
			WorkspaceID: "ws-web-fetch-restart", SupervisorTurn: checkpoint.NextTurn,
			SupervisorToolCallID: callID, CanonicalURL: canonicalURL,
			ExactTarget: "docs.example.com", RequestFingerprint: fingerprint,
			RequestedBy: "run_supervisor"})
	if err != nil || authorized || authorization.Status != domain.WebFetchAuthorizationPending {
		t.Fatalf("prepare Web fetch authorization: value=%+v authorized=%t err=%v",
			authorization, authorized, err)
	}
	if _, _, err := state.ReleaseRunExecutionLease(ctx, acquired.Lease); err != nil {
		t.Fatal(err)
	}
	authorization, replayed, err := state.DecideWebFetchAuthorization(ctx,
		authorization.ID, domain.WebFetchAuthorizationOnce, true,
		"approve-web-fetch-before-restart", "operator", "")
	if err != nil || replayed || authorization.Status != domain.WebFetchAuthorizationApproved {
		t.Fatalf("decide Web fetch authorization: value=%+v replayed=%t err=%v",
			authorization, replayed, err)
	}
	persistedRun, err := state.GetRun(ctx, run.ID)
	if err != nil || persistedRun.Status != domain.RunWaitingApproval {
		t.Fatalf("seed Run is not waiting for approval: run=%+v err=%v", persistedRun, err)
	}
	recoverable, err := state.ListRecoverableWebFetchAuthorizations(ctx, run.ID, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != authorization.ID {
		t.Fatalf("seed authorization is not recoverable: values=%+v err=%v", recoverable, err)
	}
	events, err := state.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := persistedWebFetchRecoveryFixture{run: persistedRun,
		authorization: authorization, checkpoint: checkpoint, eventCount: len(events)}
	assertWebFetchRecoveryFixtureUnchanged(t, state, fixture)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return fixture
}

func assertWebFetchRecoveryFixtureUnchanged(t *testing.T, state *store.SQLiteStore,
	want persistedWebFetchRecoveryFixture,
) {
	t.Helper()
	ctx := t.Context()
	gotAuthorization, err := state.GetWebFetchAuthorization(ctx, want.authorization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthorization.Status != want.authorization.Status ||
		gotAuthorization.Scope != want.authorization.Scope ||
		gotAuthorization.Version != want.authorization.Version ||
		!gotAuthorization.UpdatedAt.Equal(want.authorization.UpdatedAt) {
		t.Fatalf("gates-off restart advanced authorization: got=%+v want=%+v",
			gotAuthorization, want.authorization)
	}
	gotRun, err := state.GetRun(ctx, want.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRun.Status != domain.RunWaitingApproval || gotRun.Status != want.run.Status ||
		!gotRun.UpdatedAt.Equal(want.run.UpdatedAt) {
		t.Fatalf("gates-off restart advanced Run: got=%+v want=%+v", gotRun, want.run)
	}
	gotCheckpoint, found, err := state.GetSupervisorCheckpoint(ctx, want.run.ID)
	if err != nil || !found {
		t.Fatalf("get Supervisor checkpoint: found=%t err=%v", found, err)
	}
	if gotCheckpoint.Phase != want.checkpoint.Phase ||
		gotCheckpoint.NextTurn != want.checkpoint.NextTurn ||
		gotCheckpoint.AttemptID != want.checkpoint.AttemptID ||
		!gotCheckpoint.UpdatedAt.Equal(want.checkpoint.UpdatedAt) {
		t.Fatalf("gates-off restart advanced Supervisor checkpoint: got=%+v want=%+v",
			gotCheckpoint, want.checkpoint)
	}
	rounds, err := state.ListSupervisorToolRounds(ctx, gotCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 || len(rounds[0].Calls) != 1 ||
		rounds[0].Calls[0].CallID != want.authorization.SupervisorToolCallID ||
		rounds[0].Calls[0].Status != domain.SupervisorToolPending ||
		rounds[0].Calls[0].ResultJSON != "" {
		t.Fatalf("gates-off restart resumed pending tool call: rounds=%+v", rounds)
	}
	recoverable, err := state.ListRecoverableWebFetchAuthorizations(ctx, want.run.ID, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != want.authorization.ID {
		t.Fatalf("gates-off restart consumed recoverable outbox: values=%+v err=%v",
			recoverable, err)
	}
	events, err := state.ListRunEvents(ctx, want.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != want.eventCount {
		t.Fatalf("gates-off restart appended Run events: got=%d want=%d", len(events),
			want.eventCount)
	}
}
