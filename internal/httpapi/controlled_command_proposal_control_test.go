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

type controlledCommandProposalControllerStub struct {
	view        application.ControlledCommandProposalView
	reviewCalls int
}

func (s *controlledCommandProposalControllerStub) List(
	_ context.Context,
	_ string,
	_ int,
) ([]application.ControlledCommandProposalView, error) {
	return []application.ControlledCommandProposalView{s.view}, nil
}

func (s *controlledCommandProposalControllerStub) Get(
	_ context.Context,
	_ string,
) (application.ControlledCommandProposalView, error) {
	return s.view, nil
}

func (s *controlledCommandProposalControllerStub) Review(
	_ context.Context,
	request application.ReviewControlledCommandProposalRequest,
) (application.ReviewControlledCommandProposalResult, error) {
	s.reviewCalls++
	view := s.view
	view.Review = &runner.ControlledCommandProposalReview{
		ID:         "controlled-command-review-http-test",
		Decision:   runner.ControlledCommandReviewDecision(request.Decision),
		ReviewedBy: request.ReviewedBy, Reason: request.Reason,
		SingleUseExecutionAuthorized: request.Decision == "approve",
		CreatedAt:                    time.Now().UTC(),
	}
	return application.ReviewControlledCommandProposalResult{
		View: view, EvidenceContent: "UNTRUSTED GO COMMAND RESULT\nok",
	}, nil
}

func TestControlledCommandProposalHTTPUsesSplitAuthorizationAndClosedViews(
	t *testing.T,
) {
	fixture := newAPIFixture(t)
	controller := &controlledCommandProposalControllerStub{
		view: application.ControlledCommandProposalView{
			Proposal: runner.ControlledCommandProposal{
				ID:              "controlled-command-proposal-http-test",
				ProtocolVersion: runner.ControlledCommandProposalProtocolVersion,
				PolicyVersion:   runner.ControlledCommandProposalPolicyVersion,
				RunID:           fixture.run.ID, MissionID: fixture.run.MissionID,
				SessionID:           fixture.run.SessionID,
				WorkspaceID:         fixture.workspace.ID,
				Kind:                runner.ControlledCommandGitStatus,
				TimeoutMilliseconds: 5000,
				Purpose:             "inspect Git state",
				PermissionMode:      domain.RunExecutionPermissionConservative,
				PermissionRevision:  1,
				Fingerprint:         strings.Repeat("a", 64),
				CreatedAt:           time.Now().UTC(),
			},
		},
	}
	api, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ControlledCommandProposalControlEnabled: true,
		ControlledCommandProposalController:     controller,
		AppVersion:                              "command-proposal-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	collection := strings.ReplaceAll(
		ControlledCommandProposalCollectionPathTemplate,
		"{run_id}", fixture.run.ID)
	list := performSessionMessageRequest(
		t, api, http.MethodGet, collection+"?limit=10",
		testAccessToken, "", "", nil)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `"kind":"git-status"`) ||
		!strings.Contains(list.Body.String(),
			`"operator_review_required":true`) ||
		strings.Contains(list.Body.String(), "executable") ||
		strings.Contains(list.Body.String(), "argv") ||
		strings.Contains(list.Body.String(), "shell") {
		t.Fatalf("unsafe command proposal list: status=%d body=%s",
			list.Code, list.Body.String())
	}
	detail := strings.ReplaceAll(
		ControlledCommandProposalDetailPathTemplate,
		"{run_id}", fixture.run.ID)
	detail = strings.ReplaceAll(detail, "{proposal_id}",
		controller.view.Proposal.ID)
	readWithControlToken := performSessionMessageRequest(
		t, api, http.MethodGet, detail, testControlToken, "", "", nil)
	assertAPIError(t, readWithControlToken, http.StatusUnauthorized,
		"POLICY_DENIED")

	reviewPath := strings.ReplaceAll(
		ControlledCommandProposalReviewPathTemplate,
		"{run_id}", fixture.run.ID)
	reviewPath = strings.ReplaceAll(reviewPath, "{proposal_id}",
		controller.view.Proposal.ID)
	reviewWithoutControl := performSessionMessageRequest(
		t, api, http.MethodPost, reviewPath, testAccessToken,
		"command-proposal-http-review-0001", "application/json",
		strings.NewReader(
			`{"version":"controlled_command_proposal_review.v1",`+
				`"decision":"deny"}`))
	assertAPIError(t, reviewWithoutControl, http.StatusUnauthorized,
		"POLICY_DENIED")
	if controller.reviewCalls != 0 {
		t.Fatal("unauthorized proposal review reached the controller")
	}
	approved := performSessionMessageRequest(
		t, api, http.MethodPost, reviewPath, testControlToken,
		"command-proposal-http-review-0002", "application/json",
		strings.NewReader(
			`{"version":"controlled_command_proposal_review.v1",`+
				`"decision":"approve","reason":"reviewed",`+
				`"confirm_execution":true}`))
	if approved.Code != http.StatusAccepted ||
		!strings.Contains(approved.Body.String(),
			`"untrusted_evidence":"UNTRUSTED GO COMMAND RESULT\nok"`) ||
		!strings.Contains(approved.Body.String(),
			`"evidence_instruction_authorized":false`) ||
		strings.Contains(approved.Body.String(), "executable") ||
		strings.Contains(approved.Body.String(), "argv") {
		t.Fatalf("unsafe command proposal review: status=%d body=%s",
			approved.Code, approved.Body.String())
	}
	if controller.reviewCalls != 1 {
		t.Fatalf("review calls = %d, want 1", controller.reviewCalls)
	}
}

func TestControlledCommandProposalHTTPRejectsUnknownFieldsRunMismatchAndCreate(
	t *testing.T,
) {
	fixture := newAPIFixture(t)
	controller := &controlledCommandProposalControllerStub{
		view: application.ControlledCommandProposalView{
			Proposal: runner.ControlledCommandProposal{
				ID:    "controlled-command-proposal-http-boundary",
				RunID: fixture.run.ID,
			},
		},
	}
	api, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ControlledCommandProposalControlEnabled: true,
		ControlledCommandProposalController:     controller,
		AppVersion:                              "command-proposal-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewPath := "/api/v1/runs/another-run/command-proposals/" +
		controller.view.Proposal.ID + "/review"
	mismatch := performSessionMessageRequest(
		t, api, http.MethodPost, reviewPath, testControlToken,
		"command-proposal-http-review-0003", "application/json",
		strings.NewReader(
			`{"version":"controlled_command_proposal_review.v1",`+
				`"decision":"deny"}`))
	assertAPIError(t, mismatch, http.StatusNotFound, "NOT_FOUND")
	if controller.reviewCalls != 0 {
		t.Fatal("Run-mismatched proposal reached review")
	}

	validPath := strings.ReplaceAll(reviewPath, "another-run", fixture.run.ID)
	unknown := performSessionMessageRequest(
		t, api, http.MethodPost, validPath, testControlToken,
		"command-proposal-http-review-0004", "application/json",
		strings.NewReader(
			`{"version":"controlled_command_proposal_review.v1",`+
				`"decision":"deny","shell":"whoami"}`))
	assertAPIError(t, unknown, http.StatusBadRequest, "INVALID_ARGUMENT")
	if controller.reviewCalls != 0 {
		t.Fatal("unknown review field reached controller")
	}

	collection := strings.ReplaceAll(
		ControlledCommandProposalCollectionPathTemplate,
		"{run_id}", fixture.run.ID)
	create := performSessionMessageRequest(
		t, api, http.MethodPost, collection, testAccessToken,
		"", "application/json",
		strings.NewReader(`{"kind":"git-status"}`))
	assertAPIError(t, create, http.StatusMethodNotAllowed,
		"INVALID_ARGUMENT")

	if _, err := New(fixture.store, Config{
		AccessToken: testAccessToken, ControlToken: testControlToken,
		ControlledCommandProposalControlEnabled: true,
		AppVersion:                              "command-proposal-test",
	}); err == nil {
		t.Fatal("command proposal capability accepted a missing controller")
	}
}
