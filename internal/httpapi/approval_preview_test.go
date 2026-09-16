package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/toolrun"
)

type approvalPreviewSource struct {
	Store
	proposal toolrun.ToolRun
}

func (s approvalPreviewSource) GetToolRun(context.Context, string) (toolrun.ToolRun, error) {
	return s.proposal, nil
}

func TestApprovalPreviewRefusesScopeAndSourceDrift(t *testing.T) {
	fixture := newAPIFixture(t)
	outcome, err := toolgateway.New(fixture.store, policy.NewDefaultChecker()).Invoke(t.Context(), toolgateway.ToolCall{
		Name: toolgateway.ShellTool, RunID: fixture.run.ID, SessionID: fixture.run.SessionID,
		WorkspaceID: fixture.workspace.ID, RequestedBy: "preview_test",
		Arguments: map[string]string{"command": "echo preview binding"},
	})
	if err != nil || outcome.Proposal == nil {
		t.Fatalf("proposal=%#v err=%v", outcome, err)
	}
	record, err := fixture.store.GetApprovalByProposal(t.Context(), outcome.Proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + fixture.run.ID + "/approvals/" + record.ID + "/preview"
	value, err := fixture.store.GetToolRun(t.Context(), record.ProposalID)
	if err != nil {
		t.Fatal(err)
	}
	for _, drift := range []string{"command", "workspace", "session", "identity"} {
		t.Run(drift, func(t *testing.T) {
			changed := value
			switch drift {
			case "command":
				changed.Command = "echo another command"
			case "workspace":
				changed.WorkspaceID = "other-workspace"
			case "session":
				changed.SessionID = "other-session"
			case "identity":
				changed.ID = "other-proposal"
			}
			fixture.api.store = approvalPreviewSource{Store: fixture.store, proposal: changed}
			response := fixture.get(t, path)
			assertAPIError(t, response, http.StatusPreconditionFailed, "FAILED_PRECONDITION")
		})
	}
	fixture.api.store = fixture.store
	unchanged, err := fixture.store.GetApproval(t.Context(), record.ID)
	if err != nil || unchanged.Status != approval.StatusPending {
		t.Fatalf("preview mutated approval: %#v %v", unchanged, err)
	}
	response := performSessionMessageRequest(t, fixture.api, http.MethodGet, path, "", "", "", nil)
	assertAPIError(t, response, http.StatusUnauthorized, "POLICY_DENIED")
	if strings.Contains(response.Body.String(), value.Command) {
		t.Fatal("unauthorized preview exposed command")
	}
}
