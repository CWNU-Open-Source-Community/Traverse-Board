package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
)

type riskEscalationStoreExecutor struct {
	calls     int
	uncertain bool
}

func (*riskEscalationStoreExecutor) Available() bool { return true }

func (e *riskEscalationStoreExecutor) Execute(_ context.Context,
	request runner.HostExecutionRequest,
) (runner.HostExecutionResult, error) {
	e.calls++
	if e.uncertain {
		return runner.HostExecutionResult{}, errors.New("simulated uncertain host start")
	}
	return hostCommandStoreExecution(request.Intent,
		[]byte("approved risk escalation side effect\n")), nil
}

type riskEscalationStoreFixture struct {
	store      *SQLiteStore
	database   string
	run        domain.Run
	proposal   runner.RiskEscalationProposal
	supervisor *application.RunSupervisor
	provider   *hostCommandProposalStoreProvider
	router     *llm.Router
	payload    json.RawMessage
}

func newRiskEscalationStoreFixture(t *testing.T) riskEscalationStoreFixture {
	t.Helper()
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "risk-escalation.db")
	state, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	workspaceRoot := t.TempDir()
	executablePath := filepath.Join(workspaceRoot, "risk-helper.exe")
	if err := os.WriteFile(executablePath, []byte(strings.Repeat("x", 1_024)), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := WorkspaceRecord{ID: "workspace-risk-escalation",
		Name: "risk-escalation", RootPath: workspaceRoot}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	_, runRecord, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "review one exact high-risk host action", Profile: "code",
		ModelRoute: "host-proposal-store/model", WorkspaceID: workspace.ID,
		Budget: domain.Budget{MaxTurns: 4, MaxTokens: 2_000,
			MaxToolCalls: 4, TimeoutSeconds: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionProfileService(state).Change(ctx,
		application.ChangeRunExecutionProfileRequest{
			RunID: runRecord.ID, Profile: "local",
			OperationKey: "risk-profile-local-0001", RequestedBy: "test_operator",
			Reason: "prepare controlled local risk escalation",
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionInteractionService(state).Change(ctx,
		application.ChangeRunExecutionInteractionRequest{
			RunID: runRecord.ID, Mode: "controlled", Trust: "trusted",
			OperationKey: "risk-interaction-controlled-0001", RequestedBy: "test_operator",
			Reason: "trust the test-owned workspace", ConfirmWorkspaceTrust: true,
		}); err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
	}
	if _, err := application.NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: runRecord.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey: "risk-permission-workspace-0001", RequestedBy: "test_operator",
			Reason: "use Standard Code Workspace Access", ConfirmWorkspaceAccess: true,
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Start(ctx, runRecord.ID); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"version": runner.RiskEscalationProtocolVersion, "transport": "process",
		"executable_path": executablePath, "argv": []string{"--exact", "one-shot"},
		"working_directory": workspaceRoot, "timeout_milliseconds": int64(1_000),
		"purpose": "exercise the exact operator-approved risk escalation",
		"risk_kinds": []string{"network", "credential", "host_path", "policy_denial",
			"non_whitelisted_tool", "other_high_risk"},
		"network_targets":   []string{"api.example.test:443"},
		"network_purpose":   "submit one bounded verification request",
		"credential_kinds":  []string{"github_app_installation"},
		"host_paths":        []string{filepath.Join(filepath.Dir(workspaceRoot), "outside-risk-target")},
		"policy_code":       "workspace.network_denied",
		"policy_reason":     "the requested target is outside Workspace Access",
		"requested_tool":    "mcp.github.create_pull_request",
		"other_risk_reason": "the external side effect requires exact operator review",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &hostCommandProposalStoreProvider{responses: []*llm.ChatResponse{
		{Provider: "host-proposal-store", Model: "model",
			Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4},
			ToolCalls: []llm.ToolCall{{ID: "provider-risk-1",
				Name: "host_command_propose", Arguments: payload}}},
		{Text: hostCommandProposalRootAction(t), Provider: "host-proposal-store",
			Model: "model", Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}},
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	supervisor := application.NewRunSupervisor(state, router, policy.NewDefaultChecker())
	waiting, err := supervisor.Step(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.RunStatus != domain.RunWaitingApproval ||
		waiting.Action.Kind != domain.RootActionWait || waiting.ToolCalls != 1 {
		t.Fatalf("risk escalation did not park the Run: %+v", waiting)
	}
	proposals, err := state.ListRiskEscalationProposals(ctx, runRecord.ID, 10)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("risk proposal list=%+v err=%v", proposals, err)
	}
	proposal := proposals[0]
	if len(proposal.Scope.Kinds) != 6 || proposal.InstructionAuthorized ||
		proposal.ExecutionAuthorized || proposal.CapabilityBearer {
		t.Fatalf("risk proposal authority or categories are invalid: %+v", proposal)
	}
	record, err := state.GetApprovalByProposal(ctx, proposal.ID)
	if err != nil || record.Status != approval.StatusPending ||
		record.ToolName != "host_command_propose" ||
		record.ActionClass != "risk_escalation" {
		t.Fatalf("risk approval=%+v err=%v", record, err)
	}
	lease, found, err := state.GetRunExecutionLease(ctx, runRecord.ID)
	if err != nil || !found || lease.Status != domain.RunExecutionLeaseReleased {
		t.Fatalf("waiting Run retained its turn lease: lease=%+v found=%t err=%v",
			lease, found, err)
	}
	visible, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	visibleRun, runErr := visible.GetRun(ctx, runRecord.ID)
	visibleProposals, listErr := visible.ListRiskEscalationProposals(ctx, runRecord.ID, 10)
	if closeErr := visible.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if runErr != nil || listErr != nil || visibleRun.Status != domain.RunWaitingApproval ||
		len(visibleProposals) != 1 || visibleProposals[0].Fingerprint != proposal.Fingerprint {
		t.Fatalf("restart view lost waiting proposal: run=%+v proposals=%+v run_err=%v list_err=%v",
			visibleRun, visibleProposals, runErr, listErr)
	}
	return riskEscalationStoreFixture{store: state, database: database,
		run: runRecord, proposal: proposal, supervisor: supervisor, provider: provider,
		router: router, payload: payload}
}

func riskEscalationGrantRequest(proposal runner.RiskEscalationProposal,
	operationKey string, ttl time.Duration, maxUses int,
) approval.CreateGrantRequest {
	return approval.CreateGrantRequest{
		SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		ToolName: "host_command_propose", ActionClass: "risk_escalation",
		Reason: "bounded exact risk escalation", GrantedBy: "test_operator",
		IdempotencyKey: operationKey, ScopeFingerprint: proposal.Scope.Fingerprint,
		Generation: 1, MaxUses: maxUses, TTL: ttl,
		ModeSnapshotID: proposal.ModeSnapshotID, ModeRevision: proposal.ModeRevision,
		InteractionSnapshotID:      proposal.InteractionSnapshotID,
		InteractionRevision:        proposal.InteractionRevision,
		ExecutionProfileSnapshotID: proposal.ExecutionProfileSnapshotID,
		ExecutionProfileRevision:   proposal.ExecutionProfileRevision,
		PermissionSnapshotID:       proposal.PermissionSnapshotID,
		PermissionRevision:         proposal.PermissionRevision,
		PermissionMode:             string(proposal.PermissionMode),
		WorkspaceRootFingerprint:   proposal.WorkspaceRootFingerprint,
		CapabilityGeneration:       proposal.CapabilityGeneration,
	}
}

func TestRiskEscalationApproveOnceResumesExactCallAtMostOnce(t *testing.T) {
	fixture := newRiskEscalationStoreFixture(t)
	ctx := context.Background()
	executor := &riskEscalationStoreExecutor{}
	service := application.NewHostCommandProposalReviewService(fixture.store, executor,
		domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		})
	request := application.ReviewHostCommandProposalRequest{
		ProposalID: fixture.proposal.ID, Decision: "approve",
		OperationKey: "risk-approve-once-0001", ReviewedBy: "test_operator",
		Reason: "approve only this exact call", ConfirmExecution: true,
		Authorization: "once",
	}
	approved, err := service.Review(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || approved.View.Approval == nil ||
		approved.View.Approval.Status != approval.StatusApproved ||
		approved.View.Grant != nil || approved.View.RiskResult == nil ||
		approved.View.RiskResult.Status != "completed" {
		t.Fatalf("approve-once result=%+v calls=%d", approved, executor.calls)
	}
	replayed, err := service.Review(ctx, request)
	if err != nil || !replayed.ReviewReplayed || !replayed.ExecutionReplayed ||
		executor.calls != 1 {
		t.Fatalf("approval replay executed again: result=%+v calls=%d err=%v",
			replayed, executor.calls, err)
	}
	resumeService := application.NewRunExecutionHandoffService(fixture.store,
		fixture.router, policy.NewDefaultChecker())
	resumed, err := resumeService.ResumeRiskEscalation(ctx,
		application.ResumeRiskEscalationRequest{
			Version: application.RiskEscalationResumeProtocolVersion,
			RunID:   fixture.run.ID, ProposalID: fixture.proposal.ID,
		})
	continued := resumed.Execution
	if err != nil || resumed.Replayed || continued.RunStatus != domain.RunRunning ||
		continued.Action.Kind != domain.RootActionContinue || executor.calls != 1 {
		t.Fatalf("exact pending call did not resume once: result=%+v calls=%d err=%v",
			continued, executor.calls, err)
	}
	resumeReplay, err := resumeService.ResumeRiskEscalation(ctx,
		application.ResumeRiskEscalationRequest{
			Version: application.RiskEscalationResumeProtocolVersion,
			RunID:   fixture.run.ID, ProposalID: fixture.proposal.ID,
		})
	if err != nil || !resumeReplay.Replayed || executor.calls != 1 ||
		len(fixture.provider.requests) != 2 {
		t.Fatalf("resume replay created work: result=%+v calls=%d requests=%d err=%v",
			resumeReplay, executor.calls, len(fixture.provider.requests), err)
	}
	if _, err := service.Review(ctx, application.ReviewHostCommandProposalRequest{
		ProposalID: fixture.proposal.ID, Decision: "deny",
		OperationKey: "risk-conflicting-replay-0001", ReviewedBy: "test_operator",
		Reason: "a terminal approval cannot be changed to denial",
	}); err == nil || executor.calls != 1 {
		t.Fatalf("conflicting terminal replay was accepted: calls=%d err=%v",
			executor.calls, err)
	}
	rounds, err := fixture.store.ListRunSupervisorToolRoundsPage(ctx,
		fixture.run.ID, 0, 10)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 ||
		rounds[0].Calls[0].CallID != fixture.proposal.SupervisorToolCallID ||
		rounds[0].Calls[0].Status != domain.SupervisorToolCompleted {
		t.Fatalf("resumed a different durable call: rounds=%+v err=%v", rounds, err)
	}
	timeline, err := fixture.store.ListRunEvents(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{events.RiskEscalationProposedEvent,
		events.ApprovalDecidedEvent, events.RiskEscalationExecutionPreparedEvent,
		events.RiskEscalationExecutionCompletedEvent} {
		if hostProposalEventCount(timeline, kind) != 1 {
			t.Fatalf("risk audit event %s count is not one: %+v", kind, timeline)
		}
	}
	assertRiskEscalationEventOrder(t, timeline, events.ApprovalRequestedEvent,
		events.RiskEscalationProposedEvent, events.ApprovalDecidedEvent,
		events.RiskEscalationExecutionPreparedEvent,
		events.RiskEscalationExecutionCompletedEvent)
}

func TestRiskEscalationDenialIsOrdinaryResumableToolResult(t *testing.T) {
	fixture := newRiskEscalationStoreFixture(t)
	ctx := context.Background()
	executor := &riskEscalationStoreExecutor{}
	service := application.NewHostCommandProposalReviewService(fixture.store, executor,
		domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		})
	request := application.ReviewHostCommandProposalRequest{
		ProposalID: fixture.proposal.ID, Decision: "deny",
		OperationKey: "risk-deny-once-0001", ReviewedBy: "test_operator",
		Reason: "deny the external side effect",
	}
	denied, err := service.Review(ctx, request)
	if err != nil || denied.View.Approval == nil ||
		denied.View.Approval.Status != approval.StatusDenied || executor.calls != 0 {
		t.Fatalf("denial result=%+v calls=%d err=%v", denied, executor.calls, err)
	}
	replayed, err := service.Review(ctx, request)
	if err != nil || !replayed.ReviewReplayed || executor.calls != 0 {
		t.Fatalf("denial replay result=%+v calls=%d err=%v", replayed, executor.calls, err)
	}
	continued, err := fixture.supervisor.Step(ctx, fixture.run.ID)
	if err != nil || continued.RunStatus != domain.RunRunning || executor.calls != 0 {
		t.Fatalf("denied call did not resume as data: result=%+v calls=%d err=%v",
			continued, executor.calls, err)
	}
	rounds, err := fixture.store.ListRunSupervisorToolRoundsPage(ctx,
		fixture.run.ID, 0, 10)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 ||
		rounds[0].Calls[0].Status != domain.SupervisorToolDenied {
		t.Fatalf("denied tool result was not durable: rounds=%+v err=%v", rounds, err)
	}
}

func TestRiskEscalationUncertainIntentNeverRetries(t *testing.T) {
	fixture := newRiskEscalationStoreFixture(t)
	ctx := context.Background()
	executor := &riskEscalationStoreExecutor{uncertain: true}
	service := application.NewHostCommandProposalReviewService(fixture.store, executor,
		domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		})
	request := application.ReviewHostCommandProposalRequest{
		ProposalID: fixture.proposal.ID, Decision: "approve",
		OperationKey: "risk-uncertain-once-0001", ReviewedBy: "test_operator",
		Reason: "exercise the write-ahead uncertainty fence", ConfirmExecution: true,
		Authorization: "once",
	}
	if _, err := service.Review(ctx, request); err == nil || executor.calls != 1 {
		t.Fatalf("uncertain first execution calls=%d err=%v", executor.calls, err)
	}
	replayed, err := service.Review(ctx, request)
	if err != nil || !replayed.ExecutionReplayed || !replayed.View.Uncertain ||
		replayed.View.Invalidation == nil ||
		replayed.View.Invalidation.ReasonCode != "execution_uncertain" || executor.calls != 1 {
		t.Fatalf("uncertain replay was not fenced: result=%+v calls=%d err=%v",
			replayed, executor.calls, err)
	}
	continued, err := fixture.supervisor.Step(ctx, fixture.run.ID)
	if err != nil || continued.RunStatus != domain.RunRunning || executor.calls != 1 {
		t.Fatalf("uncertain tool result did not resume without retry: result=%+v calls=%d err=%v",
			continued, executor.calls, err)
	}
	intent, found, err := fixture.store.GetRiskEscalationExecutionIntentByProposal(
		ctx, fixture.proposal.ID)
	if err != nil || !found || intent.AutomaticRetryAllowed {
		t.Fatalf("uncertain write-ahead intent=%+v found=%t err=%v", intent, found, err)
	}
	if _, found, err := fixture.store.GetRiskEscalationResult(ctx,
		fixture.proposal.ID); err != nil || found {
		t.Fatalf("uncertain execution fabricated a terminal result: found=%t err=%v", found, err)
	}
}

func TestRiskEscalationBoundedRunGrantConsumesExactUsesAndExhausts(t *testing.T) {
	fixture := newRiskEscalationStoreFixture(t)
	ctx := context.Background()
	executor := &riskEscalationStoreExecutor{}
	service := application.NewHostCommandProposalReviewService(fixture.store, executor,
		domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		})
	if _, err := service.Review(ctx, application.ReviewHostCommandProposalRequest{
		ProposalID: fixture.proposal.ID, Decision: "approve",
		OperationKey: "risk-bounded-missing-limits-0001", ReviewedBy: "test_operator",
		Reason: "bounds must be explicit", ConfirmExecution: true,
		Authorization: "run_scope",
	}); err == nil || executor.calls != 0 {
		t.Fatalf("implicit bounded grant was accepted: calls=%d err=%v", executor.calls, err)
	}
	first, err := service.Review(ctx, application.ReviewHostCommandProposalRequest{
		ProposalID: fixture.proposal.ID, Decision: "approve",
		OperationKey: "risk-bounded-first-0001", ReviewedBy: "test_operator",
		Reason: "approve two calls in this exact Run scope", ConfirmExecution: true,
		Authorization: "run_scope", GrantTTLSeconds: 60, GrantMaxUses: 2,
	})
	if err != nil || first.View.Grant == nil || first.View.GrantConsumption == nil ||
		first.View.Grant.Status != approval.GrantActive ||
		first.View.Grant.UsesRemaining != 1 ||
		first.View.GrantConsumption.UseOrdinal != 1 || executor.calls != 1 {
		t.Fatalf("first bounded use result=%+v calls=%d err=%v", first, executor.calls, err)
	}
	if _, err := fixture.store.CreateSessionGrant(ctx,
		riskEscalationGrantRequest(fixture.proposal,
			"risk-bounded-different-limits-0001", time.Minute, 3)); err == nil {
		t.Fatal("active bounded grant was silently reused with different limits")
	}
	unchanged, err := fixture.store.GetSessionGrant(ctx, first.View.Grant.ID)
	if err != nil || unchanged.MaxUses != 2 || unchanged.UsesRemaining != 1 {
		t.Fatalf("mismatched grant request changed durable limits: grant=%+v err=%v",
			unchanged, err)
	}
	firstTimeline, err := fixture.store.ListRunEvents(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRiskEscalationEventOrder(t, firstTimeline,
		events.ApprovalGrantCreatedEvent, events.ApprovalGrantConsumedEvent,
		events.ApprovalDecidedEvent, events.RiskEscalationExecutionPreparedEvent,
		events.RiskEscalationExecutionCompletedEvent)
	grantID := first.View.Grant.ID
	if _, err := fixture.supervisor.Step(ctx, fixture.run.ID); err != nil {
		t.Fatal(err)
	}
	fixture.provider.responses = append(fixture.provider.responses,
		&llm.ChatResponse{Provider: "host-proposal-store", Model: "model",
			Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4},
			ToolCalls: []llm.ToolCall{{ID: "provider-risk-2",
				Name: "host_command_propose", Arguments: fixture.payload}}},
		&llm.ChatResponse{Text: hostCommandProposalRootAction(t),
			Provider: "host-proposal-store", Model: "model",
			Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4}})
	waiting, err := fixture.supervisor.Step(ctx, fixture.run.ID)
	if err != nil || waiting.RunStatus != domain.RunWaitingApproval {
		t.Fatalf("second exact proposal did not wait: result=%+v err=%v", waiting, err)
	}
	proposals, err := fixture.store.ListRiskEscalationProposals(ctx, fixture.run.ID, 10)
	if err != nil || len(proposals) != 2 {
		t.Fatalf("second proposal list=%+v err=%v", proposals, err)
	}
	secondProposal := proposals[0]
	if secondProposal.ID == fixture.proposal.ID {
		secondProposal = proposals[1]
	}
	second, err := service.Review(ctx, application.ReviewHostCommandProposalRequest{
		ProposalID: secondProposal.ID, Decision: "approve",
		OperationKey: "risk-bounded-second-0001", ReviewedBy: "test_operator",
		Reason: "consume the second and final exact use", ConfirmExecution: true,
		Authorization: "run_scope", GrantTTLSeconds: 60, GrantMaxUses: 2,
	})
	if err != nil || second.View.Grant == nil || second.View.GrantConsumption == nil ||
		second.View.Grant.ID != grantID || second.View.Grant.Status != approval.GrantRevoked ||
		second.View.Grant.UsesRemaining != 0 ||
		second.View.GrantConsumption.UseOrdinal != 2 || executor.calls != 2 {
		t.Fatalf("second bounded use result=%+v calls=%d err=%v", second, executor.calls, err)
	}
	consumption, found, err := fixture.store.GetGrantConsumptionByProposal(ctx,
		secondProposal.ID)
	if err != nil || !found || consumption.GrantID != grantID ||
		consumption.GrantGeneration != 1 || consumption.UseOrdinal != 2 {
		t.Fatalf("second grant consumption=%+v found=%t err=%v", consumption, found, err)
	}
}

func TestRiskEscalationBoundedReviewRecoversDurableGrantWindows(t *testing.T) {
	for _, afterDecision := range []bool{false, true} {
		name := "after_grant_creation"
		if afterDecision {
			name = "after_grant_consumption_and_decision"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newRiskEscalationStoreFixture(t)
			ctx := context.Background()
			const reviewKey = "risk-bounded-recovery-0001"
			created, err := fixture.store.CreateSessionGrant(ctx,
				riskEscalationGrantRequest(fixture.proposal,
					"risk-grant:"+reviewKey, time.Minute, 2))
			if err != nil {
				t.Fatal(err)
			}
			if afterDecision {
				if _, err := fixture.store.AuthorizeApprovalWithSessionGrant(ctx,
					fixture.proposal.ID, created.Grant.ID); err != nil {
					t.Fatal(err)
				}
			}
			executor := &riskEscalationStoreExecutor{}
			service := application.NewHostCommandProposalReviewService(fixture.store,
				executor, domain.ExecutionPermissionRuntimeCapabilities{
					WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
				})
			result, err := service.Review(ctx,
				application.ReviewHostCommandProposalRequest{
					ProposalID: fixture.proposal.ID, Decision: "approve",
					OperationKey: reviewKey, ReviewedBy: "test_operator",
					Reason: "bounded exact risk escalation", ConfirmExecution: true,
					Authorization: "run_scope", GrantTTLSeconds: 60,
					GrantMaxUses: 2,
				})
			if err != nil || executor.calls != 1 || result.View.Grant == nil ||
				result.View.Grant.ID != created.Grant.ID ||
				result.View.GrantConsumption == nil ||
				result.View.GrantConsumption.UseOrdinal != 1 {
				t.Fatalf("durable grant window did not converge: result=%+v calls=%d err=%v",
					result, executor.calls, err)
			}
			grants, err := fixture.store.ListSessionGrants(ctx,
				approval.GrantListFilter{RunID: fixture.run.ID,
					ToolName: "host_command_propose", Limit: 10})
			if err != nil || len(grants) != 1 || grants[0].Generation != 1 ||
				grants[0].UsesRemaining != 1 {
				t.Fatalf("recovery minted or consumed the wrong grant: grants=%+v err=%v",
					grants, err)
			}
		})
	}
}

func TestRiskEscalationBoundedGrantExpiryAndRevocationCannotAuthorize(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		fixture := newRiskEscalationStoreFixture(t)
		ctx := context.Background()
		created, err := fixture.store.CreateSessionGrant(ctx,
			riskEscalationGrantRequest(fixture.proposal,
				"risk-expiring-grant-0001", time.Millisecond, 2))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		if _, err := fixture.store.AuthorizeApprovalWithSessionGrant(ctx,
			fixture.proposal.ID, created.Grant.ID); err == nil {
			t.Fatal("expired bounded grant authorized a proposal")
		}
		stored, err := fixture.store.GetSessionGrant(ctx, created.Grant.ID)
		if err != nil || stored.Status != approval.GrantRevoked ||
			stored.UsesRemaining != 2 {
			t.Fatalf("expired grant=%+v err=%v", stored, err)
		}
		record, err := fixture.store.GetApprovalByProposal(ctx, fixture.proposal.ID)
		if err != nil || record.Status != approval.StatusPending || record.GrantID != "" {
			t.Fatalf("expiry changed pending approval=%+v err=%v", record, err)
		}
		timeline, err := fixture.store.ListRunEvents(ctx, fixture.run.ID)
		if err != nil || hostProposalEventCount(timeline,
			events.ApprovalGrantExpiredEvent) != 1 {
			t.Fatalf("grant expiry event is missing: events=%+v err=%v", timeline, err)
		}
	})
	t.Run("revocation", func(t *testing.T) {
		fixture := newRiskEscalationStoreFixture(t)
		ctx := context.Background()
		created, err := fixture.store.CreateSessionGrant(ctx,
			riskEscalationGrantRequest(fixture.proposal,
				"risk-revoked-grant-0001", time.Minute, 2))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.RevokeSessionGrant(ctx, approval.RevokeGrantRequest{
			GrantID: created.Grant.ID, Reason: "operator revoked the bounded scope",
			RevokedBy: "test_operator", IdempotencyKey: "risk-revoke-grant-0001",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.AuthorizeApprovalWithSessionGrant(ctx,
			fixture.proposal.ID, created.Grant.ID); err == nil {
			t.Fatal("revoked bounded grant authorized a proposal")
		}
		record, err := fixture.store.GetApprovalByProposal(ctx, fixture.proposal.ID)
		if err != nil || record.Status != approval.StatusPending || record.GrantID != "" {
			t.Fatalf("revocation changed pending approval=%+v err=%v", record, err)
		}
	})
}

func TestRiskEscalationWorkspaceDriftInvalidatesProposalAndActiveGrant(t *testing.T) {
	fixture := newRiskEscalationStoreFixture(t)
	ctx := context.Background()
	created, err := fixture.store.CreateSessionGrant(ctx,
		riskEscalationGrantRequest(fixture.proposal,
			"risk-drift-grant-0001", time.Minute, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.proposal.Spec.ExecutablePath,
		[]byte(strings.Repeat("y", 1_024)), 0o700); err != nil {
		t.Fatal(err)
	}
	executor := &riskEscalationStoreExecutor{}
	service := application.NewHostCommandProposalReviewService(fixture.store, executor,
		domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		})
	_, err = service.Review(ctx, application.ReviewHostCommandProposalRequest{
		ProposalID: fixture.proposal.ID, Decision: "approve",
		OperationKey: "risk-drift-review-0001", ReviewedBy: "test_operator",
		Reason: "this must fail after executable drift", ConfirmExecution: true,
		Authorization: "run_scope", GrantTTLSeconds: 60, GrantMaxUses: 2,
	})
	if err == nil || executor.calls != 0 {
		t.Fatalf("drifted proposal executed: calls=%d err=%v", executor.calls, err)
	}
	invalidation, found, err := fixture.store.GetRiskEscalationInvalidation(ctx,
		fixture.proposal.ID)
	if err != nil || !found || invalidation.ReasonCode != "workspace_drift" ||
		invalidation.GrantID != created.Grant.ID {
		t.Fatalf("drift invalidation=%+v found=%t err=%v", invalidation, found, err)
	}
	grant, err := fixture.store.GetSessionGrant(ctx, created.Grant.ID)
	if err != nil || grant.Status != approval.GrantRevoked {
		t.Fatalf("drifted grant remained usable: grant=%+v err=%v", grant, err)
	}
	timeline, err := fixture.store.ListRunEvents(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRiskEscalationEventOrder(t, timeline,
		events.ApprovalGrantInvalidatedEvent,
		events.RiskEscalationInvalidatedEvent)
	runRecord, err := fixture.store.GetRun(ctx, fixture.run.ID)
	if err != nil || runRecord.Status != domain.RunRunning {
		t.Fatalf("invalidated Run did not resume: run=%+v err=%v", runRecord, err)
	}
	continued, err := fixture.supervisor.Step(ctx, fixture.run.ID)
	if err != nil || continued.RunStatus != domain.RunRunning || executor.calls != 0 {
		t.Fatalf("drifted tool result did not resume safely: result=%+v calls=%d err=%v",
			continued, executor.calls, err)
	}
}

func assertRiskEscalationEventOrder(t *testing.T, timeline []events.Event,
	kinds ...string,
) {
	t.Helper()
	previous := -1
	for _, kind := range kinds {
		found := -1
		for index := previous + 1; index < len(timeline); index++ {
			if timeline[index].Type == kind {
				found = index
				break
			}
		}
		if found < 0 {
			t.Fatalf("risk event %s is missing or out of order after index %d: %+v",
				kind, previous, timeline)
		}
		previous = found
	}
}
