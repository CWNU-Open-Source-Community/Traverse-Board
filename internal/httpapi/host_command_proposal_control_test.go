package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

type hostCommandProposalControllerStub struct {
	view        application.HostCommandProposalView
	reviewCalls int
	review      application.ReviewHostCommandProposalRequest
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

func (s *hostCommandProposalControllerStub) Review(
	_ context.Context, request application.ReviewHostCommandProposalRequest,
) (application.ReviewHostCommandProposalResult, error) {
	s.reviewCalls++
	s.review = request
	view := s.view
	now := time.Now().UTC()
	view.Review = &runner.HostCommandReview{
		ID: "host-command-review-http-test", Decision: runner.HostCommandReviewDecision(request.Decision),
		ReviewedBy: request.ReviewedBy, Reason: request.Reason,
		SingleUseExecutionAuthorized: request.Decision == "approve", CreatedAt: now,
	}
	view.Result = &runner.HostCommandProposalResult{
		ID: "host-command-result-http-test", Status: "completed",
		SourceKind: "go_command_result", SourceRef: "message-http-test",
		ContentSHA256: strings.Repeat("e", 64), CreatedAt: now,
	}
	view.Receipt = &runner.HostExecutionReceipt{
		RequestID: "host-command-request-http-test", Backend: "windows-host-job-v1",
		StdoutPrefixSHA256: strings.Repeat("f", 64), StderrPrefixSHA256: strings.Repeat("0", 64),
		StartedAt: now, CompletedAt: now, TreeReaped: true, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: runner.MaxHostActiveProcesses,
		JobMemoryLimit:     runner.MaxHostProcessMemoryBytes, StdinClosed: true,
		NetworkRequested: true, ProductExecutionEnabled: true,
	}
	return application.ReviewHostCommandProposalResult{
		View: view, EvidenceContent: "UNTRUSTED HOST COMMAND RESULT\nok",
	}, nil
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

func TestHostCommandProposalHTTPUsesSplitAuthorizationAndExactEnvelope(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &hostCommandProposalControllerStub{view: testHostCommandProposalView(
		t, fixture.run.ID, fixture.run.MissionID, fixture.run.SessionID, fixture.workspace.ID)}
	api, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true,
		},
		HostCommandProposalControlEnabled: true,
		HostCommandProposalController:     controller, AppVersion: "host-command-proposal-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := strings.ReplaceAll(HostCommandProposalCollectionPathTemplate,
		"{run_id}", fixture.run.ID)
	list := performSessionMessageRequest(t, api, http.MethodGet, collection+"?limit=10",
		testAccessToken, "", "", nil)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `"executable_path":"C:\\Program Files\\Go\\bin\\go.exe"`) ||
		!strings.Contains(list.Body.String(), `"argv":["test","./internal/application"]`) ||
		!strings.Contains(list.Body.String(), `"network_intent":"host"`) ||
		!strings.Contains(list.Body.String(), `"non_sandboxed":true`) ||
		!strings.Contains(list.Body.String(), `"automatic_retry_allowed":false`) ||
		strings.Contains(list.Body.String(), "SECRET_VALUE") {
		t.Fatalf("host command proposal list lost its exact boundary: status=%d body=%s",
			list.Code, list.Body.String())
	}

	detail := strings.ReplaceAll(HostCommandProposalDetailPathTemplate, "{run_id}", fixture.run.ID)
	detail = strings.ReplaceAll(detail, "{proposal_id}", controller.view.Proposal.ID)
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodGet, detail,
		testControlToken, "", "", nil), http.StatusUnauthorized, "POLICY_DENIED")

	reviewPath := strings.ReplaceAll(HostCommandProposalReviewPathTemplate, "{run_id}", fixture.run.ID)
	reviewPath = strings.ReplaceAll(reviewPath, "{proposal_id}", controller.view.Proposal.ID)
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, reviewPath,
		testAccessToken, "host-command-http-review-0001", "application/json",
		strings.NewReader(`{"version":"host_command_review.v1","decision":"deny"}`)),
		http.StatusUnauthorized, "POLICY_DENIED")
	approved := performSessionMessageRequest(t, api, http.MethodPost, reviewPath,
		testControlToken, "host-command-http-review-0002", "application/json",
		strings.NewReader(`{"version":"host_command_review.v1","decision":"approve",`+
			`"reason":"reviewed exact envelope","confirm_execution":true}`))
	if approved.Code != http.StatusAccepted ||
		!strings.Contains(approved.Body.String(), `"untrusted_evidence":"UNTRUSTED HOST COMMAND RESULT\nok"`) ||
		!strings.Contains(approved.Body.String(), `"job_memory_limit":2147483648`) ||
		!strings.Contains(approved.Body.String(), `"evidence_instruction_authorized":false`) {
		t.Fatalf("host command proposal review is invalid: status=%d body=%s",
			approved.Code, approved.Body.String())
	}
	if controller.reviewCalls != 1 || controller.review.ReviewedBy != "http_control_operator" ||
		!controller.review.ConfirmExecution {
		t.Fatalf("review did not preserve independent operator binding: %+v", controller.review)
	}
}

func TestHostCommandProposalHTTPRejectsDisabledInvalidAndMismatchedRequests(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &hostCommandProposalControllerStub{view: testHostCommandProposalView(
		t, fixture.run.ID, fixture.run.MissionID, fixture.run.SessionID, fixture.workspace.ID)}
	collection := strings.ReplaceAll(HostCommandProposalCollectionPathTemplate,
		"{run_id}", fixture.run.ID)
	assertAPIError(t, performSessionMessageRequest(t, fixture.api, http.MethodGet, collection,
		testAccessToken, "", "", nil), http.StatusNotFound, "NOT_FOUND")

	config := Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true,
		}, HostCommandProposalControlEnabled: true,
		HostCommandProposalController: controller, AppVersion: "host-command-proposal-test"}
	api, err := New(fixture.store, config)
	if err != nil {
		t.Fatal(err)
	}
	mismatchPath := "/api/v1/runs/another-run/host-command-proposals/" +
		controller.view.Proposal.ID + "/review"
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, mismatchPath,
		testControlToken, "host-command-http-review-0003", "application/json",
		strings.NewReader(`{"version":"host_command_review.v1","decision":"deny"}`)),
		http.StatusNotFound, "NOT_FOUND")
	validPath := strings.ReplaceAll(mismatchPath, "another-run", fixture.run.ID)
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, validPath,
		testControlToken, "host-command-http-review-0004", "application/json",
		strings.NewReader(`{"version":"host_command_review.v1","decision":"deny",`+
			`"shell":"whoami"}`)), http.StatusBadRequest, "INVALID_ARGUMENT")
	if controller.reviewCalls != 0 {
		t.Fatal("invalid host command review reached the controller")
	}
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, collection,
		testAccessToken, "", "application/json", strings.NewReader(`{}`)),
		http.StatusMethodNotAllowed, "INVALID_ARGUMENT")

	missingController := config
	missingController.HostCommandProposalController = nil
	if _, err := New(fixture.store, missingController); err == nil {
		t.Fatal("host command capability accepted a missing controller")
	}
	missingOperator := config
	missingOperator.ExecutionPermissionCapabilities = domain.ExecutionPermissionRuntimeCapabilities{}
	if _, err := New(fixture.store, missingOperator); err == nil {
		t.Fatal("host command capability accepted a missing operator approval gate")
	}
}
