package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
)

func TestFileEditRevertProposalHTTPUsesReviewGateAndStrictExactControl(t *testing.T) {
	f := newAPIFixture(t)
	path := filepath.Join(f.workspace.RootPath, "inverse-http.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := fileedit.NewManager(f.store).Propose(t.Context(), fileedit.Proposal{
		SessionID: f.run.SessionID, WorkspaceID: f.workspace.ID, WorkspaceRoot: f.workspace.RootPath,
		Path: "inverse-http.txt", ProposedText: "after\n", ExpectedOriginalHash: fileedit.HashText("before\n")})
	if err != nil {
		t.Fatal(err)
	}
	review := application.NewFileEditReviewService(f.store)
	if _, err := review.Review(t.Context(), application.ReviewFileEditRequest{Version: application.FileEditReviewProtocolVersion,
		RunID: f.run.ID, EditID: source.ID, Action: application.FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	lease, found, err := f.store.GetRunExecutionLease(t.Context(), f.run.ID)
	if err != nil || !found {
		t.Fatalf("fixture lease: %#v %v", lease, err)
	}
	if _, err := application.NewFileEditApplyService(f.store, policy.NewDefaultChecker()).Apply(t.Context(), application.ApplyFileEditRequest{
		Version: fileedit.FileEditApplyProtocolVersion, RunID: f.run.ID, EditID: source.ID,
		OperationKey: "source-http-apply-operation-0001", AppliedBy: "test_operator",
		LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation}); err != nil {
		t.Fatal(err)
	}
	api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
		FileEditReviewEnabled: true, FileEditReviewController: review,
		FileEditProposalController: application.NewFileEditProposalService(f.store, policy.NewDefaultChecker())})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/v1/runs/" + f.run.ID + "/file-edits/" + source.ID + "/revert-proposal"
	const body = `{"version":"file_edit_proposal.v1"}`
	const key = "http-inverse-proposal-operation-0001"
	assertAPIError(t, performSessionMessageRequest(t, f.api, http.MethodPost, endpoint,
		testControlToken, key, "application/json", strings.NewReader(body)), http.StatusNotFound, "NOT_FOUND")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, endpoint,
		testAccessToken, key, "application/json", strings.NewReader(body)), http.StatusUnauthorized, "POLICY_DENIED")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, endpoint,
		testControlToken, "", "application/json", strings.NewReader(body)), http.StatusBadRequest, "INVALID_ARGUMENT")
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost, endpoint,
		testControlToken, key, "application/json", strings.NewReader(`{"version":"file_edit_proposal.v1","path":"other.txt"}`)), http.StatusBadRequest, "INVALID_ARGUMENT")
	remote := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+endpoint, strings.NewReader(body))
	remote.RemoteAddr = "203.0.113.2:5000"
	remote.Header.Set("Authorization", "Bearer "+testControlToken)
	remote.Header.Set("Content-Type", "application/json")
	remote.Header.Set("Idempotency-Key", key)
	remoteResponse := httptest.NewRecorder()
	api.ServeHTTP(remoteResponse, remote)
	assertAPIError(t, remoteResponse, http.StatusForbidden, "POLICY_DENIED")
	var first FileEditRevertProposalView
	decodeDataStatus(t, performSessionMessageRequest(t, api, http.MethodPost, endpoint,
		testControlToken, key, "application/json", strings.NewReader(body)), http.StatusAccepted, &first)
	if first.SourceEditID != source.ID || first.RunID != f.run.ID || first.FileWritten || first.Replayed ||
		first.Edit.ID == source.ID || first.Edit.Status != fileedit.StatusProposed || len(first.Edit.AllowedActions) == 0 ||
		!strings.Contains(first.Edit.Diff, "+before") || !strings.Contains(first.Edit.Diff, "-after") {
		t.Fatalf("inverse response=%#v", first)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "after\n" {
		t.Fatalf("proposal wrote file: %q %v", content, err)
	}
	var replay FileEditRevertProposalView
	decodeDataStatus(t, performSessionMessageRequest(t, api, http.MethodPost, endpoint,
		testControlToken, key, "application/json", strings.NewReader(body)), http.StatusAccepted, &replay)
	if !replay.Replayed || replay.Edit.ID != first.Edit.ID || replay.FileWritten {
		t.Fatalf("HTTP replay=%#v", replay)
	}
	// Review capability never opens arbitrary renderer text proposals.
	assertAPIError(t, performSessionMessageRequest(t, api, http.MethodPost,
		"/api/v1/runs/"+f.run.ID+"/file-edit-proposals", testControlToken, "", "application/json",
		strings.NewReader(`{}`)), http.StatusNotFound, "NOT_FOUND")
}

func TestFileEditDeleteHTTPProjectsOnlyCompleteVerifiedDeletionContent(t *testing.T) {
	for _, kind := range []string{"inverse", "ordinary", "empty", "redacted", "hash-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			f := newAPIFixture(t)
			const relative = "delete-preview.txt"
			content := "first deleted line\nsecond deleted line\n"
			if kind == "empty" {
				content = ""
			}
			if kind == "redacted" {
				content = "private token=" + f.secret + "\n"
			}
			manager := fileedit.NewManager(f.store)
			var edit fileedit.Edit
			var err error
			var inverseResponse FileEditRevertProposalView
			if kind == "inverse" {
				source, sourceErr := manager.Propose(t.Context(), fileedit.Proposal{SessionID: f.run.SessionID,
					WorkspaceID: f.workspace.ID, WorkspaceRoot: f.workspace.RootPath, Path: relative,
					Operation: fileedit.OperationCreate, ExpectedOriginalHash: "missing", ProposedText: content})
				if sourceErr != nil {
					t.Fatal(sourceErr)
				}
				review := application.NewFileEditReviewService(f.store)
				if _, err := review.Review(t.Context(), application.ReviewFileEditRequest{Version: application.FileEditReviewProtocolVersion,
					RunID: f.run.ID, EditID: source.ID, Action: application.FileEditApproveIntent}); err != nil {
					t.Fatal(err)
				}
				if _, err := manager.Approve(t.Context(), source.ID, f.workspace.RootPath); err != nil {
					t.Fatal(err)
				}
				api, err := New(f.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken,
					FileEditReviewEnabled: true, FileEditReviewController: review,
					FileEditProposalController: application.NewFileEditProposalService(f.store, policy.NewDefaultChecker())})
				if err != nil {
					t.Fatal(err)
				}
				decodeDataStatus(t, performSessionMessageRequest(t, api, http.MethodPost,
					"/api/v1/runs/"+f.run.ID+"/file-edits/"+source.ID+"/revert-proposal", testControlToken,
					"delete-preview-revert-operation-0001", "application/json",
					strings.NewReader(`{"version":"file_edit_proposal.v1"}`)), http.StatusAccepted, &inverseResponse)
				edit, err = f.store.GetFileEdit(t.Context(), inverseResponse.Edit.ID)
			} else {
				if err := os.WriteFile(filepath.Join(f.workspace.RootPath, relative), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				edit, err = manager.Propose(t.Context(), fileedit.Proposal{SessionID: f.run.SessionID,
					WorkspaceID: f.workspace.ID, WorkspaceRoot: f.workspace.RootPath, Path: relative,
					Operation: fileedit.OperationDelete, ExpectedOriginalHash: fileedit.HashText(content)})
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "hash-mismatch" {
				edit.OriginalText = "fabricated content must not be displayed\n"
				edit, err = f.store.SaveFileEdit(t.Context(), edit)
				if err != nil {
					t.Fatal(err)
				}
			}
			response := performSessionMessageRequest(t, f.api, http.MethodGet,
				"/api/v1/runs/"+f.run.ID+"/file-edits/"+edit.ID, testAccessToken, "", "", nil)
			var view FileEditPreviewView
			decodeDataStatus(t, response, http.StatusOK, &view)
			if !strings.Contains(view.Diff, "delete "+relative) {
				t.Fatalf("missing delete metadata: %#v", view)
			}
			switch kind {
			case "inverse", "ordinary":
				if !strings.Contains(view.Diff, "@@ -") || !strings.Contains(view.Diff, "-first deleted line\n") ||
					!strings.Contains(view.Diff, "-second deleted line\n") {
					t.Fatalf("deletion content missing: %#v", view)
				}
				if kind == "inverse" && inverseResponse.Edit.Diff != view.Diff {
					t.Fatalf("POST/GET deletion views diverge: %#v / %#v", inverseResponse.Edit, view)
				}
			case "empty":
				if !strings.Contains(view.Diff, "size_bytes 0") || strings.Contains(view.Diff, "@@") {
					t.Fatalf("empty delete misrepresented: %#v", view)
				}
			case "redacted", "hash-mismatch":
				if strings.Contains(view.Diff, "@@") || view.Diff != edit.Diff ||
					strings.Contains(response.Body.String(), f.secret) || strings.Contains(response.Body.String(), "fabricated content") {
					t.Fatalf("unverified delete body exposed: %s", response.Body.String())
				}
			}
			if strings.Contains(response.Body.String(), `"original_text"`) || strings.Contains(response.Body.String(), `"proposed_text"`) {
				t.Fatal("read-only preview exposed raw body fields")
			}
			stored, err := f.store.GetFileEdit(t.Context(), edit.ID)
			if err != nil || stored != edit {
				t.Fatalf("projection rewrote durable edit: %#v %v", stored, err)
			}
		})
	}
}
