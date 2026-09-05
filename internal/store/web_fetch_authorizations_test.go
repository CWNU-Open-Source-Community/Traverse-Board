package store

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
	"cyberagent-workbench/internal/toolgateway"
)

func TestWebFetchAuthorizationWaitsDecidesAndResumesExactPendingCall(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "web-fetch-inline-approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	_, run := createStructuredToolTestRun(t, ctx, state, "approve one public HTTPS fetch")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	turn, err := state.BeginSupervisorTurn(ctx, lease, "fetch public evidence")
	if err != nil {
		t.Fatal(err)
	}
	started := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 3,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint, started); err != nil || !inserted {
		t.Fatalf("record model start: inserted=%t err=%v", inserted, err)
	}
	canonicalURL := "https://docs.example.com/reference"
	payload := json.RawMessage(`{"version":"web_fetch.v1","url":"` + canonicalURL + `"}`)
	operationKey := runmutation.SupervisorToolOperationKey(run.ID,
		turn.Checkpoint.NextTurn, "web_fetch", string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondURL := "https://papers.example.org/latest"
	secondPayload := json.RawMessage(`{"version":"web_fetch.v1","url":"` + secondURL + `"}`)
	secondOperationKey := runmutation.SupervisorToolOperationKey(run.ID,
		turn.Checkpoint.NextTurn, "web_fetch", string(secondPayload))
	secondCallID, err := runmutation.SupervisorToolCallID(secondOperationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	completed := started
	completed.Outcome = llm.OutcomeSuccess
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root agent missing: found=%t err=%v", found, err)
	}
	authority, err := toolgateway.NewWebEvidenceCallAuthority(
		toolgateway.WebEvidenceCapabilityContext{RunID: run.ID, MissionID: run.MissionID,
			SessionID: run.SessionID, RootAgentID: root.ID, WorkspaceID: "ws-structured",
			Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
			Role: domain.AgentRoleRoot, Profile: domain.ProfileCode,
			PermissionMode:     domain.RunExecutionPermissionConservative,
			PermissionRevision: 1, ModeRevision: 1, NetworkMode: "allowlist",
			AllowedTargets: []string{"search.example.com"}, ProviderAvailable: true,
			ProviderFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	encodedAuthority, err := toolgateway.EncodeWebEvidenceCallAuthority(authority)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, completed,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{
				{ID: callID, Name: "web_fetch", Arguments: payload,
					Authority: encodedAuthority},
				{ID: secondCallID, Name: "web_fetch", Arguments: secondPayload,
					Authority: encodedAuthority},
			}})
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.NextTurn != turn.Checkpoint.NextTurn {
		t.Fatalf("checkpoint turn changed: got=%d want=%d", checkpoint.NextTurn,
			turn.Checkpoint.NextTurn)
	}
	fingerprint := approval.Fingerprint(domain.WebFetchAuthorizationProtocolVersion,
		run.ID, run.SessionID, callID, canonicalURL, "docs.example.com")
	value, authorized, err := state.PrepareWebFetchAuthorization(ctx,
		domain.WebFetchAuthorizationRequest{ID: "web-fetch-authorization-1",
			RunID: run.ID, MissionID: run.MissionID, SessionID: run.SessionID,
			WorkspaceID: "ws-structured", SupervisorTurn: checkpoint.NextTurn,
			SupervisorToolCallID: callID, CanonicalURL: canonicalURL,
			ExactTarget: "docs.example.com", RequestFingerprint: fingerprint,
			RequestedBy: "run_supervisor"})
	if err != nil || authorized || value.Status != domain.WebFetchAuthorizationPending {
		t.Fatalf("prepare authorization: value=%+v authorized=%t err=%v", value, authorized, err)
	}
	if _, _, err := state.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	waiting, err := state.GetRun(ctx, run.ID)
	if err != nil || waiting.Status != domain.RunWaitingApproval {
		t.Fatalf("Run did not enter waiting_approval: run=%+v err=%v", waiting, err)
	}
	replay, replayAuthorized, err := state.PrepareWebFetchAuthorization(ctx,
		domain.WebFetchAuthorizationRequest{ID: "ignored-replay-id", RunID: run.ID,
			MissionID: run.MissionID, SessionID: run.SessionID, WorkspaceID: "ws-structured",
			SupervisorTurn: checkpoint.NextTurn, SupervisorToolCallID: callID,
			CanonicalURL: canonicalURL, ExactTarget: "docs.example.com",
			RequestFingerprint: fingerprint, RequestedBy: "run_supervisor"})
	if err != nil || replayAuthorized || replay.ID != value.ID || replay.ApprovalID != value.ApprovalID {
		t.Fatalf("prepare replay: value=%+v authorized=%t err=%v", replay, replayAuthorized, err)
	}
	if _, err := state.db.ExecContext(ctx, `CREATE TRIGGER fail_web_fetch_decision_test
		BEFORE UPDATE ON web_fetch_authorizations WHEN NEW.status <> 'pending'
		BEGIN SELECT RAISE(ABORT, 'injected web fetch decision failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.DecideWebFetchAuthorization(ctx, value.ID,
		domain.WebFetchAuthorizationOnce, true, "approve-web-fetch-rollback-1",
		"operator", ""); err == nil {
		t.Fatal("expected injected Web fetch decision failure")
	}
	approvalAfterRollback, err := state.GetApproval(ctx, value.ApprovalID)
	if err != nil || approvalAfterRollback.Status != approval.StatusPending {
		t.Fatalf("generic approval escaped rolled-back Web decision: approval=%+v err=%v",
			approvalAfterRollback, err)
	}
	sourceAfterRollback, err := state.GetWebFetchAuthorization(ctx, value.ID)
	if err != nil || sourceAfterRollback.Status != domain.WebFetchAuthorizationPending {
		t.Fatalf("Web decision rollback changed source: source=%+v err=%v",
			sourceAfterRollback, err)
	}
	if _, err := state.db.ExecContext(ctx, `DROP TRIGGER fail_web_fetch_decision_test`); err != nil {
		t.Fatal(err)
	}
	decided, decisionReplay, err := state.DecideWebFetchAuthorization(ctx, value.ID,
		domain.WebFetchAuthorizationOnce, true, "approve-web-fetch-once-1", "operator", "")
	if err != nil || decisionReplay || decided.Status != domain.WebFetchAuthorizationApproved ||
		decided.Scope != domain.WebFetchAuthorizationOnce {
		t.Fatalf("approve once: value=%+v replay=%t err=%v", decided, decisionReplay, err)
	}
	recoverable, err := state.ListRecoverableWebFetchAuthorizations(ctx, run.ID, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != decided.ID {
		t.Fatalf("decided authorization outbox=%+v err=%v", recoverable, err)
	}
	if _, _, err := state.ResumeWebFetchAuthorizationRun(ctx, decided.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := state.GetRun(ctx, run.ID)
	if err != nil || resumed.Status != domain.RunRunning {
		t.Fatalf("Run did not resume in place: run=%+v err=%v", resumed, err)
	}
	approved, authorized, err := state.PrepareWebFetchAuthorization(ctx,
		domain.WebFetchAuthorizationRequest{ID: "ignored-approved-replay", RunID: run.ID,
			MissionID: run.MissionID, SessionID: run.SessionID, WorkspaceID: "ws-structured",
			SupervisorTurn: checkpoint.NextTurn, SupervisorToolCallID: callID,
			CanonicalURL: canonicalURL, ExactTarget: "docs.example.com",
			RequestFingerprint: fingerprint, RequestedBy: "run_supervisor"})
	if err != nil || !authorized || approved.ID != value.ID {
		t.Fatalf("approved replay: value=%+v authorized=%t err=%v", approved, authorized, err)
	}
	continuationLease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	continuation, err := state.BeginSupervisorTurn(ctx, continuationLease, "")
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := state.RecordSupervisorToolExecutionStarted(ctx,
		continuation.Checkpoint, callID); err != nil || !inserted {
		t.Fatalf("start first fetch result: inserted=%t err=%v", inserted, err)
	}
	if _, _, err := state.RecordSupervisorToolResult(ctx, continuation.Checkpoint,
		domain.SupervisorToolResult{CallID: callID,
			Status: domain.SupervisorToolCompleted, ResultJSON: `{"status":"completed"}`,
			CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.ReleaseRunExecutionLease(ctx, continuationLease); err != nil {
		t.Fatal(err)
	}
	// Persisting the exact tool result is not the end of the continuation. If
	// the worker dies before the following provider step, the same Turn must
	// remain visible to reconciliation while the Run is still running.
	recoverable, err = state.ListRecoverableWebFetchAuthorizations(ctx, run.ID, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != decided.ID {
		t.Fatalf("post-result continuation outbox=%+v err=%v", recoverable, err)
	}
	// Simulate a different approval class parking the same Run. An old Web
	// authorization whose exact call is already terminal must not unlock that
	// unrelated wait merely because its decision is replayed.
	if _, err := state.db.ExecContext(ctx, `UPDATE runs SET status = 'waiting_approval'
		WHERE id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.ResumeWebFetchAuthorizationRun(ctx, decided.ID); err == nil {
		t.Fatal("old Web authorization unlocked an unrelated approval wait")
	}
	unrelatedWait, err := state.GetRun(ctx, run.ID)
	if err != nil || unrelatedWait.Status != domain.RunWaitingApproval {
		t.Fatalf("old Web authorization polluted unrelated wait: run=%+v err=%v",
			unrelatedWait, err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE runs SET status = 'running'
		WHERE id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	secondFingerprint := approval.Fingerprint(domain.WebFetchAuthorizationProtocolVersion,
		run.ID, run.SessionID, secondCallID, secondURL, "papers.example.org")
	second, authorized, err := state.PrepareWebFetchAuthorization(ctx,
		domain.WebFetchAuthorizationRequest{ID: "web-fetch-authorization-2",
			RunID: run.ID, MissionID: run.MissionID, SessionID: run.SessionID,
			WorkspaceID: "ws-structured", SupervisorTurn: checkpoint.NextTurn,
			SupervisorToolCallID: secondCallID, CanonicalURL: secondURL,
			ExactTarget: "papers.example.org", RequestFingerprint: secondFingerprint,
			RequestedBy: "run_supervisor"})
	if err != nil || authorized || second.Status != domain.WebFetchAuthorizationPending {
		t.Fatalf("prepare second authorization: value=%+v authorized=%t err=%v",
			second, authorized, err)
	}
	if _, _, err := state.ResumeWebFetchAuthorizationRun(ctx, decided.ID); err == nil {
		t.Fatal("old authorization unlocked a Run waiting for a different call")
	}
	stillWaiting, err := state.GetRun(ctx, run.ID)
	if err != nil || stillWaiting.Status != domain.RunWaitingApproval {
		t.Fatalf("old authorization polluted waiting Run: run=%+v err=%v", stillWaiting, err)
	}
	denied, _, err := state.DecideWebFetchAuthorization(ctx, second.ID,
		domain.WebFetchAuthorizationOnce, false, "deny-web-fetch-2", "operator",
		"unwanted source")
	if err != nil || denied.Status != domain.WebFetchAuthorizationDenied {
		t.Fatalf("deny second authorization: value=%+v err=%v", denied, err)
	}
	recoverable, err = state.ListRecoverableWebFetchAuthorizations(ctx, run.ID, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != denied.ID {
		t.Fatalf("denied authorization outbox=%+v err=%v", recoverable, err)
	}
	if _, _, err := state.ResumeWebFetchAuthorizationRun(ctx, denied.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.ConsumeWebFetchAuthorization(ctx, run.ID, callID); err != nil {
		t.Fatal(err)
	}
	consumed, err := state.GetWebFetchAuthorization(ctx, approved.ID)
	if err != nil || consumed.Status != domain.WebFetchAuthorizationConsumed {
		t.Fatalf("allow-once grant was not consumed: value=%+v err=%v", consumed, err)
	}
	consumedReplay, replayed, err := state.DecideWebFetchAuthorization(ctx, consumed.ID,
		domain.WebFetchAuthorizationOnce, true, "approve-web-fetch-once-1", "operator", "")
	if err != nil || !replayed || consumedReplay.Status != domain.WebFetchAuthorizationConsumed {
		t.Fatalf("consumed allow-once decision replay: value=%+v replayed=%t err=%v",
			consumedReplay, replayed, err)
	}
}
