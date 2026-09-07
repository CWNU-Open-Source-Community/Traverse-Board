package application_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type applicationWebFetchBackend struct {
	calls            int
	lastMode         string
	lastTargets      []string
	lastRobotsPolicy webevidence.RobotsPolicy
	robotsState      string
}

type applicationWebSearchProvider struct{ calls int }

func (p *applicationWebSearchProvider) Name() string { return "application-search" }

func (p *applicationWebSearchProvider) Endpoint() string {
	return "https://docs.example.com/search"
}

func (p *applicationWebSearchProvider) Search(_ context.Context, _ string, _ int,
	_ webevidence.NetworkAuthority,
) ([]webevidence.ProviderResult, error) {
	p.calls++
	return []webevidence.ProviderResult{{URL: "https://docs.example.com/result",
		Title: "Application search result", Rank: 1}}, nil
}

func (f *applicationWebFetchBackend) Fetch(_ context.Context, rawURL string,
	authority webevidence.NetworkAuthority, robotsPolicy webevidence.RobotsPolicy,
) (webevidence.FetchedContent, error) {
	f.calls++
	f.lastMode = authority.Mode
	f.lastTargets = append([]string(nil), authority.AllowedTargets...)
	f.lastRobotsPolicy = robotsPolicy
	robotsState := f.robotsState
	if robotsState == "" {
		robotsState = "allowed"
	}
	return webevidence.FetchedContent{RequestedURL: rawURL, FinalURL: rawURL,
		HTTPStatus: http.StatusOK,
		RawDigest:  webevidence.DigestBytes([]byte("raw application evidence")),
		Robots:     robotsState, Parsed: webevidence.ParsedDocument{Title: "Application evidence",
			Body: "private bounded evidence body", MIME: "text/html", Charset: "utf-8"}}, nil
}

func TestWebEvidenceExecutorInlineApprovalProjectsDisabledToExactAuthority(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "web-evidence-inline-approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	mission, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "Read one operator-approved public source",
			Profile: "review", NetworkMode: "disabled",
			Budget: domain.Budget{MaxTurns: 4, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	mode, _ := state.GetRunMode(ctx, run.ID)
	permission, _ := state.GetRunExecutionPermission(ctx, run.ID)
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root found=%t err=%v", found, err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "web-inline-approval-test", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx, lease.Lease, "fetch approved source")
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 3,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil || !inserted {
		t.Fatalf("record model start: inserted=%t err=%v", inserted, err)
	}
	payload := json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.net/approved"}`)
	operationKey := runmutation.SupervisorToolOperationKey(run.ID,
		turn.Checkpoint.NextTurn, string(toolgateway.WebFetchTool), string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	capabilityContext := toolgateway.WebEvidenceCapabilityContext{RunID: run.ID,
		MissionID: mission.ID, SessionID: run.SessionID, RootAgentID: root.ID,
		WorkspaceID: mission.WorkspaceID, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: mode.Revision,
		NetworkMode: "disabled", InlineWebFetchApprovalAvailable: true}
	callAuthority, err := toolgateway.NewWebEvidenceCallAuthority(capabilityContext)
	if err != nil {
		t.Fatal(err)
	}
	encodedAuthority, err := toolgateway.EncodeWebEvidenceCallAuthority(callAuthority)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: string(toolgateway.WebFetchTool),
				Arguments: payload, Authority: encodedAuthority}}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &applicationWebFetchBackend{}
	executor, err := application.NewWebEvidenceToolExecutor(state,
		webevidence.NewService(state, nil, backend))
	if err != nil {
		t.Fatal(err)
	}
	scope := toolgateway.WebEvidenceExecutionScope{InvocationID: "web-inline-invocation-1",
		OperationKey: operationKey, RunID: run.ID, SupervisorTurn: checkpoint.NextTurn,
		SupervisorToolCallID: callID, MissionID: mission.ID, SessionID: run.SessionID,
		RootAgentID: root.ID, WorkspaceID: mission.WorkspaceID, Surface: mode.Surface,
		Phase: mode.Phase, Role: root.Role, Profile: mode.Profile,
		PermissionMode: permission.Mode, PermissionRevision: permission.Revision,
		ModeRevision:         mode.Revision,
		CapabilityGeneration: toolgateway.WebEvidenceCapabilitySnapshot(capabilityContext).Generation,
		LeaseID:              lease.Lease.LeaseID, LeaseGeneration: lease.Lease.Generation,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "medium", Reason: "inline approval test"}}
	if _, err := executor.ExecuteWebEvidence(ctx, scope, toolgateway.WebFetchTool,
		payload); apperror.CodeOf(err) != apperror.CodeFailedPrecondition || backend.calls != 0 {
		t.Fatalf("scheduler-disabled fetch code=%s err=%v calls=%d",
			apperror.CodeOf(err), err, backend.calls)
	}
	if pending, err := state.ListApprovals(ctx, approval.ListFilter{RunID: run.ID,
		Status: approval.StatusPending, Limit: 10}); err != nil || len(pending) != 0 {
		t.Fatalf("scheduler-disabled pending approvals=%+v err=%v", pending, err)
	}
	executor.WithWebFetchAuthorizationScheduler(true)
	if _, err := executor.ExecuteWebEvidence(ctx, scope, toolgateway.WebFetchTool, payload); err == nil || backend.calls != 0 {
		t.Fatalf("unapproved fetch err=%v calls=%d", err, backend.calls)
	}
	if _, _, err := state.ReleaseRunExecutionLease(ctx, lease.Lease); err != nil {
		t.Fatal(err)
	}
	approvals, err := state.ListApprovals(ctx, approval.ListFilter{RunID: run.ID,
		Status: approval.StatusPending, Limit: 10})
	if err != nil || len(approvals) != 1 {
		t.Fatalf("pending approvals=%+v err=%v", approvals, err)
	}
	authorization, err := state.GetWebFetchAuthorizationByApproval(ctx, approvals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.DecideWebFetchAuthorization(ctx, authorization.ID,
		domain.WebFetchAuthorizationOnce, true, "approve-inline-disabled-1", "operator", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.ResumeWebFetchAuthorizationRun(ctx, authorization.ID); err != nil {
		t.Fatal(err)
	}
	retryLease, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "web-inline-approval-retry", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	scope.InvocationID = "web-inline-invocation-2"
	scope.LeaseID, scope.LeaseGeneration = retryLease.Lease.LeaseID, retryLease.Lease.Generation
	result, err := executor.ExecuteWebEvidence(ctx, scope, toolgateway.WebFetchTool, payload)
	if err != nil || backend.calls != 1 || backend.lastMode != "allowlist" ||
		len(backend.lastTargets) != 1 || backend.lastTargets[0] != "docs.example.net" ||
		backend.lastRobotsPolicy != webevidence.RobotsPolicyEnforce ||
		result.Metadata["untrusted"] != "true" ||
		result.Metadata["http_status"] != "200" ||
		result.Metadata["redirects"] != "0" {
		t.Fatalf("approved result=%#v calls=%d mode=%q targets=%v err=%v",
			result, backend.calls, backend.lastMode, backend.lastTargets, err)
	}
}

func TestWebEvidenceExecutorRechecksPersistedRunAuthorityAndHasNoSearchFallback(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "web-evidence-application.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	mission, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "Review a public source", Profile: "review",
			NetworkMode: "allowlist", AllowedTargets: []string{"docs.example.com"},
			Budget: domain.Budget{MaxTurns: 4, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root found=%t err=%v", found, err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "web-evidence-application-test", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	backend := &applicationWebFetchBackend{}
	oldFetchTime := time.Now().UTC().Add(-48 * time.Hour)
	service := webevidence.NewService(state, nil, backend).WithClock(func() time.Time {
		return oldFetchTime
	})
	executor, err := application.NewWebEvidenceToolExecutor(state, service)
	if err != nil {
		t.Fatal(err)
	}
	capabilityContext := toolgateway.WebEvidenceCapabilityContext{RunID: run.ID,
		MissionID: mission.ID, SessionID: run.SessionID, RootAgentID: root.ID,
		WorkspaceID: mission.WorkspaceID, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: mode.Revision,
		NetworkMode:    mode.Scope.NetworkMode,
		AllowedTargets: append([]string(nil), mode.Scope.AllowedTargets...)}
	scope := toolgateway.WebEvidenceExecutionScope{InvocationID: "web-invocation-1",
		OperationKey: "application-fetch-operation", RunID: run.ID,
		SupervisorTurn: 1, SupervisorToolCallID: "web-call-1",
		MissionID: mission.ID, SessionID: run.SessionID, RootAgentID: root.ID,
		WorkspaceID: mission.WorkspaceID, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: mode.Revision,
		CapabilityGeneration: toolgateway.WebEvidenceCapabilitySnapshot(capabilityContext).Generation,
		LeaseID:              lease.Lease.LeaseID, LeaseGeneration: lease.Lease.Generation,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "medium", Reason: "test policy"}}
	if err := scope.Validate(); err != nil {
		t.Fatalf("constructed web evidence scope=%#v err=%v", scope, err)
	}

	result, err := executor.ExecuteWebEvidence(ctx, scope, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`))
	if err != nil || backend.calls != 1 || result.Metadata["untrusted"] != "true" ||
		result.Metadata["instruction_authorized"] != "false" ||
		result.Metadata["stale"] != "true" || result.Metadata["state"] != "stale" ||
		!strings.Contains(result.Content, "private bounded evidence body") {
		t.Fatalf("result=%#v calls=%d err=%v", result, backend.calls, err)
	}
	replayScope := scope
	replayScope.InvocationID = "web-invocation-replay"
	replay, err := executor.ExecuteWebEvidence(ctx, replayScope, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`))
	if err != nil || backend.calls != 1 || replay.Content != result.Content ||
		replay.Metadata["replayed"] != "true" || result.Metadata["replayed"] != "false" {
		t.Fatalf("replay=%#v original=%#v calls=%d err=%v", replay, result, backend.calls, err)
	}

	stale := scope
	stale.InvocationID = "web-invocation-2"
	stale.OperationKey = "application-stale-operation"
	stale.ModeRevision++
	if _, err := executor.ExecuteWebEvidence(ctx, stale, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`)); apperror.CodeOf(err) != apperror.CodeFailedPrecondition || backend.calls != 1 {
		t.Fatalf("stale code=%s calls=%d err=%v", apperror.CodeOf(err), backend.calls, err)
	}

	searchProvider := &applicationWebSearchProvider{}
	searchService := webevidence.NewService(state, searchProvider, backend)
	searchExecutor, err := application.NewWebEvidenceToolExecutor(state, searchService)
	if err != nil {
		t.Fatal(err)
	}
	searchFingerprint := searchService.SearchProviderFingerprintForScope(ctx,
		webevidence.ExecutionScope{RunID: run.ID, MissionID: mission.ID,
			WorkspaceID: mission.WorkspaceID, ModelRoute: run.Config.ModelRoute,
			Authority: webevidence.NetworkAuthority{Mode: mode.Scope.NetworkMode,
				AllowedTargets: append([]string(nil), mode.Scope.AllowedTargets...)}})
	searchCapabilityContext := capabilityContext
	searchCapabilityContext.ProviderAvailable = true
	searchCapabilityContext.ProviderFingerprint = searchFingerprint
	searchCapabilityContext.ProviderSearchIndependent =
		searchService.SearchProviderIndependentForScope(ctx,
			webevidence.ExecutionScope{RunID: run.ID, MissionID: mission.ID,
				WorkspaceID: mission.WorkspaceID, ModelRoute: run.Config.ModelRoute,
				Authority: webevidence.NetworkAuthority{Mode: mode.Scope.NetworkMode,
					AllowedTargets: append([]string(nil), mode.Scope.AllowedTargets...)}})
	searchScope := scope
	searchScope.InvocationID = "web-invocation-3"
	searchScope.OperationKey = "application-search-operation"
	searchScope.CapabilityGeneration = toolgateway.WebEvidenceCapabilitySnapshot(
		searchCapabilityContext).Generation
	searchScope.ProviderFingerprint = searchFingerprint
	searchResult, err := searchExecutor.ExecuteWebEvidence(ctx, searchScope,
		toolgateway.WebSearchTool,
		json.RawMessage(`{"version":"web_search.v1","query":"public spec","limit":3}`))
	if err != nil || searchProvider.calls != 1 ||
		searchResult.Metadata["provider"] != searchProvider.Name() {
		t.Fatalf("search result=%#v calls=%d err=%v", searchResult, searchProvider.calls, err)
	}
	missingFingerprint := searchScope
	missingFingerprint.InvocationID = "web-invocation-4"
	missingFingerprint.OperationKey = "application-search-missing-fingerprint"
	missingFingerprint.ProviderFingerprint = ""
	if _, err := searchExecutor.ExecuteWebEvidence(ctx, missingFingerprint,
		toolgateway.WebSearchTool,
		json.RawMessage(`{"version":"web_search.v1","query":"public spec","limit":3}`)); apperror.CodeOf(err) != apperror.CodeFailedPrecondition || searchProvider.calls != 1 {
		t.Fatalf("missing fingerprint code=%s calls=%d err=%v", apperror.CodeOf(err),
			searchProvider.calls, err)
	}

	noProviderSearchScope := scope
	noProviderSearchScope.InvocationID = "web-invocation-5"
	noProviderSearchScope.OperationKey = "application-search-no-provider"
	_, err = executor.ExecuteWebEvidence(ctx, noProviderSearchScope, toolgateway.WebSearchTool,
		json.RawMessage(`{"version":"web_search.v1","query":"public spec","limit":3}`))
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "authority no longer matches") || backend.calls != 1 {
		t.Fatalf("search code=%s calls=%d err=%v", apperror.CodeOf(err), backend.calls, err)
	}
}

func TestWebEvidenceExecutorProjectsFullAccessToSafePublicHTTPS(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "web-evidence-full-access.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	mission, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "Read public documentation", Profile: "review",
			NetworkMode: "disabled", Budget: domain.Budget{MaxTurns: 4, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true,
		DangerFullAccessEnabled: true}
	if _, err := application.NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{RunID: created.ID,
			Mode:         string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "web-evidence-full-access-permission-0001",
			RequestedBy:  "test_operator", Reason: "exercise safe public HTTPS projection",
			ConfirmDangerFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root found=%t err=%v", found, err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "web-evidence-full-access-test", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	backend := &applicationWebFetchBackend{robotsState: "bypassed_disallow"}
	executor, err := application.NewWebEvidenceToolExecutor(state,
		webevidence.NewService(state, nil, backend))
	if err != nil {
		t.Fatal(err)
	}
	capabilityContext := toolgateway.WebEvidenceCapabilityContext{RunID: run.ID,
		MissionID: mission.ID, SessionID: run.SessionID, RootAgentID: root.ID,
		WorkspaceID: mission.WorkspaceID, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: mode.Revision,
		NetworkMode: "allowlist", AllowedTargets: []string{webevidence.PublicHTTPSTarget}}
	scope := toolgateway.WebEvidenceExecutionScope{InvocationID: "web-full-access-invocation-1",
		OperationKey: "web-full-access-fetch-0001", RunID: run.ID,
		SupervisorTurn: 1, SupervisorToolCallID: "web-full-access-call-1",
		MissionID: mission.ID, SessionID: run.SessionID, RootAgentID: root.ID,
		WorkspaceID: mission.WorkspaceID, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: mode.Revision,
		CapabilityGeneration: toolgateway.WebEvidenceCapabilitySnapshot(capabilityContext).Generation,
		LeaseID:              lease.Lease.LeaseID, LeaseGeneration: lease.Lease.Generation,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "high", Reason: "test policy"}}
	if err := scope.Validate(); err != nil {
		t.Fatalf("constructed Full Access web evidence scope=%#v err=%v", scope, err)
	}

	result, err := executor.ExecuteWebEvidence(ctx, scope, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.org/reference"}`))
	if err != nil || backend.calls != 1 || result.Metadata["untrusted"] != "true" ||
		backend.lastRobotsPolicy != webevidence.RobotsPolicyAuditOnly ||
		result.Metadata["robots_policy"] != string(webevidence.RobotsPolicyAuditOnly) ||
		result.Metadata["robots"] != "bypassed_disallow" ||
		!strings.Contains(result.Content, `"robots":"bypassed_disallow"`) {
		t.Fatalf("public result=%#v calls=%d err=%v", result, backend.calls, err)
	}
	unsafeScope := scope
	unsafeScope.InvocationID = "web-full-access-invocation-2"
	unsafeScope.OperationKey = "web-full-access-fetch-unsafe-0001"
	if _, err := executor.ExecuteWebEvidence(ctx, unsafeScope, toolgateway.WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":"https://127.0.0.1/private"}`)); err == nil || backend.calls != 1 {
		t.Fatalf("unsafe target err=%v calls=%d", err, backend.calls)
	}
}
