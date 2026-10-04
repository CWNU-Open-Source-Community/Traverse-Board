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
	view application.ControlledCommandProposalView
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
		ControlledCommandProposalController: controller,
		AppVersion:                          "command-proposal-test",
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

	for _, token := range []string{testAccessToken, testControlToken} {
		status, code := http.StatusMethodNotAllowed, "INVALID_ARGUMENT"
		if token == testControlToken {
			status, code = http.StatusUnauthorized, "POLICY_DENIED"
		}
		assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, detail+"/review", token, "retired-review-operation", "application/json", strings.NewReader(`{"decision":"approve","confirm_execution":true}`)), status, code)
	}
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodGet, strings.Replace(detail, fixture.run.ID, "other-run", 1), testAccessToken, "", "", nil), http.StatusNotFound, "NOT_FOUND")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, collection, testAccessToken, "", "application/json", strings.NewReader(`{}`)), http.StatusMethodNotAllowed, "INVALID_ARGUMENT")
}
