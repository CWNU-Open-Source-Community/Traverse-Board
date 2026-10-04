package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

type hostCommandProposalControllerStub struct {
	view application.HostCommandProposalView
}

func (s *hostCommandProposalControllerStub) List(
	_ context.Context, _ string, _ int,
) ([]application.HostCommandProposalView, error) {
	return []application.HostCommandProposalView{s.view}, nil
}

func (s *hostCommandProposalControllerStub) Get(
	_ context.Context, _ string,
) (application.HostCommandProposalView, error) {
	return s.view, nil
}

func testHostCommandReceipt(now time.Time) *runner.HostExecutionReceipt {
	return &runner.HostExecutionReceipt{
		RequestID: "host-command-request-http-test", Backend: "windows-host-job-v1",
		StdoutPrefixSHA256: strings.Repeat("f", 64), StderrPrefixSHA256: strings.Repeat("0", 64),
		StartedAt: now, CompletedAt: now, TreeReaped: true, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: runner.MaxHostActiveProcesses,
		JobMemoryLimit:     runner.MaxHostProcessMemoryBytes, StdinClosed: true,
		NetworkRequested: true, ProductExecutionEnabled: true,
	}
}

type riskEscalationResumeControllerStub struct {
	calls   int
	request application.ResumeRiskEscalationRequest
}

func (*riskEscalationResumeControllerStub) Execute(context.Context,
	application.ExecuteRunHandoffRequest,
) (application.ExecuteRunHandoffResult, error) {
	return application.ExecuteRunHandoffResult{}, nil
}

func (s *riskEscalationResumeControllerStub) ResumeRiskEscalation(_ context.Context,
	request application.ResumeRiskEscalationRequest,
) (application.ResumeRiskEscalationResult, error) {
	s.calls++
	s.request = request
	return application.ResumeRiskEscalationResult{}, nil
}

func testHostCommandProposalView(t *testing.T, runID, missionID, sessionID,
	workspaceID string,
) application.HostCommandProposalView {
	t.Helper()
	spec := runner.HostCommandSpec{
		ProtocolVersion:   runner.HostCommandProtocolVersion,
		PolicyVersion:     runner.HostCommandPolicyVersion,
		ExecutablePath:    `C:\Program Files\Go\bin\go.exe`,
		ExecutableSHA256:  strings.Repeat("a", 64),
		Argv:              []string{"test", "./internal/application"},
		WorkingDirectory:  `D:\GitProjects\Prayu`,
		EnvironmentPolicy: runner.HostEnvironmentPolicy,
		EnvironmentKeys:   []string{"PATH", "SYSTEMROOT"},
		EnvironmentSHA256: strings.Repeat("b", 64),
		NetworkIntent:     runner.HostNetworkIntentHost, TimeoutMilliseconds: 120000,
		Purpose: "run focused application tests", Fingerprint: strings.Repeat("c", 64),
	}
	return application.HostCommandProposalView{Proposal: runner.HostCommandProposal{
		ID:              "host-command-proposal-http-test",
		ProtocolVersion: runner.HostCommandProposalProtocolVersion,
		PolicyVersion:   runner.HostCommandPolicyVersion,
		RunID:           runID, MissionID: missionID, SessionID: sessionID, WorkspaceID: workspaceID,
		PermissionMode: domain.RunExecutionPermissionApproval, PermissionRevision: 3,
		Spec: spec, Fingerprint: strings.Repeat("d", 64), CreatedAt: time.Now().UTC(),
	}}
}

func testRiskEscalationHTTPView(t *testing.T, runID, missionID, sessionID,
	workspaceID string,
) application.HostCommandProposalView {
	t.Helper()
	legacy := testHostCommandProposalView(t, runID, missionID, sessionID, workspaceID)
	scope, err := runner.NewRiskEscalationScope(runner.RiskEscalationScopeRequest{
		Kinds: []runner.RiskEscalationKind{runner.RiskEscalationNetwork,
			runner.RiskEscalationCredential},
		NetworkTargets: []string{"api.example.test:443"},
		NetworkPurpose: "submit one exact request", CredentialKinds: []string{"github_app"},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := &runner.RiskEscalationProposal{
		ID: "risk-escalation-http-test", ProtocolVersion: runner.RiskEscalationProtocolVersion,
		PolicyVersion: runner.RiskEscalationPolicyVersion,
		RunID:         runID, MissionID: missionID, SessionID: sessionID, WorkspaceID: workspaceID,
		RootAgentID: "root-risk-http", SupervisorTurn: 2,
		SupervisorToolCallID: "tool-risk-http", ToolInvocationID: "invocation-risk-http",
		ModeSnapshotID: "mode-risk-http", ModeRevision: 3,
		InteractionSnapshotID: "interaction-risk-http", InteractionRevision: 4,
		ExecutionProfileSnapshotID: "profile-risk-http", ExecutionProfileRevision: 5,
		PermissionSnapshotID: "permission-risk-http", PermissionRevision: 6,
		PermissionMode:           domain.RunExecutionPermissionWorkspaceAccess,
		WorkspaceRootFingerprint: strings.Repeat("1", 64),
		CapabilityGeneration:     strings.Repeat("2", 64),
		Spec:                     legacy.Proposal.Spec, Scope: scope,
		ResourceBudget: runner.NewRiskEscalationResourceBudget(legacy.Proposal.Spec),
		Fingerprint:    strings.Repeat("3", 64), CreatedAt: time.Now().UTC(),
	}
	return application.HostCommandProposalView{RiskEscalation: proposal,
		Approval: &approval.Record{ID: "approval-risk-http-test",
			Status: approval.StatusPending}}
}

func TestHistoricalHostHTTPReadAndResumeBoundaries(t *testing.T) {
	f := newAPIFixture(t)
	c := &hostCommandProposalControllerStub{view: testHostCommandProposalView(t, f.run.ID, f.run.MissionID, f.run.SessionID, f.workspace.ID)}
	resume := &riskEscalationResumeControllerStub{}
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true, ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true},
		HostCommandProposalController: c, RunExecutionEnabled: true, RunExecutionController: resume})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + f.run.ID + "/host-command-proposals/" + c.view.ID()
	got := performSessionMessageRequest(t, api, http.MethodGet, path, testAccessToken, "", "", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"argv":["test","./internal/application"]`) || !strings.Contains(got.Body.String(), `"automatic_retry_allowed":false`) {
		t.Fatal(got.Code, got.Body.String())
	}
	for _, token := range []string{testAccessToken, testControlToken} {
		status, code := http.StatusMethodNotAllowed, "INVALID_ARGUMENT"
		if token == testControlToken {
			status, code = http.StatusUnauthorized, "POLICY_DENIED"
		}
		assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, path+"/review", token, "old-review-operation", "application/json", strings.NewReader(`{"decision":"approve","confirm_execution":true}`)), status, code)
	}
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, path+"/resume", testAccessToken, "", "", nil), http.StatusUnauthorized, "POLICY_DENIED")
	for _, body := range []string{`{}`, `{"decision":"approve"}`, `{"argv":["whoami"]}`} {
		assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, path+"/resume", testControlToken, "", "application/json", strings.NewReader(body)), http.StatusBadRequest, "INVALID_ARGUMENT")
	}
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, path+"/resume?approve=true", testControlToken, "", "", nil), http.StatusBadRequest, "INVALID_ARGUMENT")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, strings.Replace(path, f.run.ID, "other-run", 1)+"/resume", testControlToken, "", "", nil), http.StatusNotFound, "NOT_FOUND")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, path+"/resume", testControlToken, "", "", nil), http.StatusPreconditionFailed, "FAILED_PRECONDITION")
	if resume.calls != 0 {
		t.Fatal("unsettled historical command reached continuation")
	}
}

func TestHistoricalRiskHTTPResumesExactSavedOutcomeWithoutApproval(t *testing.T) {
	f := newAPIFixture(t)
	c := &hostCommandProposalControllerStub{view: testRiskEscalationHTTPView(t, f.run.ID, f.run.MissionID, f.run.SessionID, f.workspace.ID)}
	c.view.Approval.Status = approval.StatusDenied
	resume := &riskEscalationResumeControllerStub{}
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true, ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true},
		HostCommandProposalController: c, RunExecutionEnabled: true, RunExecutionController: resume})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + f.run.ID + "/host-command-proposals/" + c.view.ID() + "/resume"
	got := performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "", "", nil)
	if got.Code != http.StatusAccepted || resume.calls != 1 || resume.request.RunID != f.run.ID || resume.request.ProposalID != c.view.ID() || resume.request.Version != application.RiskEscalationResumeProtocolVersion {
		t.Fatal(got.Code, got.Body.String(), resume)
	}
	if c.view.Approval.Status != approval.StatusDenied || c.view.Grant != nil || c.view.RiskResult != nil {
		t.Fatal("resume fabricated authority or result")
	}
}

func TestHistoricalCommandsDefaultToAuthenticatedReadOnlyHistory(t *testing.T) {
	f := newAPIFixture(t)
	api, err := New(f.store, Config{AccessToken: testAccessToken})
	if err != nil {
		t.Fatal(err)
	}
	for _, collection := range []string{"command-proposals", "host-command-proposals"} {
		path := "/api/v1/runs/" + f.run.ID + "/" + collection
		got := performSessionMessageRequest(t, api, http.MethodGet, path, testAccessToken, "", "", nil)
		if got.Code != http.StatusOK {
			t.Fatal(got.Code, got.Body.String())
		}
		assertAPIError(t, performSessionMessageRequest(t, api, http.MethodGet, path, "", "", "", nil), http.StatusUnauthorized, "POLICY_DENIED")
	}
	got := performSessionMessageRequest(t, api, http.MethodPost, "/api/v1/runs/"+f.run.ID+"/host-command-proposals/historical/resume", testAccessToken, "", "", nil)
	assertAPIError(t, got, http.StatusUnauthorized, "POLICY_DENIED")
}
