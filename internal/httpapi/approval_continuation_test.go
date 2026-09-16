package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/fileedit"
)

type reviewedProposalContinuationStub struct {
	threadTurnControllerStub
	request application.ApprovalContinuationRequest
}

func (s *reviewedProposalContinuationStub) ResumeApproval(ctx context.Context, r application.ApprovalContinuationRequest) application.ApprovalContinuationResult {
	s.request = r
	return application.ApprovalContinuationResult{State: "failed", HandoffID: "run-handoff-test", ErrorCode: "UNAVAILABLE", Message: "审批已保存，后续执行未完成。"}
}

func TestFileReviewHTTPRetainsDurableDecisionWhenContinuationFails(t *testing.T) {
	f := newAPIFixture(t)
	edit, err := fileedit.NewManager(f.store).Propose(t.Context(), fileedit.Proposal{SessionID: f.run.SessionID, WorkspaceID: f.workspace.ID, WorkspaceRoot: f.workspace.RootPath, Path: "reviewed.txt", ProposedText: "approved intent only\n"})
	if err != nil {
		t.Fatal(err)
	}
	controller := &reviewedProposalContinuationStub{}
	f.api.fileEditReviewEnabled = true
	f.api.fileEditReviewController = application.NewFileEditReviewService(f.store)
	f.api.runExecutionEnabled = true
	f.api.threadTurnController = controller
	path := "/api/v1/runs/" + f.run.ID + "/file-edits/" + edit.ID + "/review"
	for _, replay := range []bool{false, true} {
		response := performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, "", "application/json", strings.NewReader(`{"version":"file_edit_review.v1","action":"approve_intent"}`))
		var envelope struct {
			Data FileEditReviewView `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		v := envelope.Data
		if response.Code != http.StatusAccepted || v.Edit.Status != "approved" || v.FileWritten || v.Replayed != replay || v.Continuation == nil || v.Continuation.State != "failed" {
			t.Fatalf("review %d: %s", response.Code, response.Body.String())
		}
		if controller.request.RunID != f.run.ID || controller.request.Kind != "file_edit" || controller.request.ProposalID != edit.ID {
			t.Fatalf("wrong continuation identity: %#v", controller.request)
		}
	}
	if _, err := os.Stat(filepath.Join(f.workspace.RootPath, "reviewed.txt")); !os.IsNotExist(err) {
		t.Fatalf("review wrote file: %v", err)
	}
	stored, err := f.store.GetFileEdit(t.Context(), edit.ID)
	if err != nil || stored.Status != fileedit.StatusApproved {
		t.Fatalf("decision not preserved: %#v %v", stored, err)
	}
}
