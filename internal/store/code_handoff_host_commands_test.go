package store

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/runner"
)

// The surrounding fixture uses a scripted model and a constructed process
// result, but real SQLite proposal/review/intent/result transactions and HTTP.
// It is a receipt projection regression, not an OS test execution claim.
func assertHostCommandHandoffHTTP(t *testing.T, state *SQLiteStore, run domain.Run,
	proposal runner.HostCommandProposal, receipt *runner.HostExecutionReceipt, review string,
) {
	t.Helper()
	const access = "host-handoff-access-token-12345678"
	const control = "host-handoff-control-token-12345678"
	api, err := httpapi.New(state, httpapi.Config{AccessToken: access, ControlToken: control,
		AppVersion: "host-handoff-test", HostCommandProposalControlEnabled: true,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities:   domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true},
		HostCommandProposalController: application.NewHostCommandProposalReviewService(state, nil,
			domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
		request.Host = "127.0.0.1"
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set("Authorization", "Bearer "+access)
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	response := get("/runs/" + run.ID + "/code-handoff")
	if response.Code != http.StatusOK {
		t.Fatalf("handoff: %d %s", response.Code, response.Body)
	}
	var envelope struct {
		Data httpapi.CodeHandoffView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	handoff := envelope.Data
	if handoff.HostCommands == nil || len(handoff.HostCommands.Items) != 1 || handoff.HostCommands.Truncated ||
		handoff.StandardCodeDelivery != nil || handoff.Verification.PassCount != 0 ||
		strings.Contains(response.Body.String(), "verified helper output") {
		t.Fatalf("handoff fabricated verification or included output: %s", response.Body)
	}
	item := handoff.HostCommands.Items[0]
	if item.ProposalID != proposal.ID || item.RunID != run.ID || item.SessionID != run.SessionID ||
		item.WorkspaceID != proposal.WorkspaceID || item.WorkingDirectory != proposal.Spec.WorkingDirectory ||
		item.SpecFingerprint != proposal.Spec.Fingerprint || item.ReviewDecision != review {
		t.Fatalf("handoff changed command binding: %#v", item)
	}
	if receipt == nil {
		if item.Receipt != nil || item.ResultID != "" || item.ResultStatus != "" {
			t.Fatalf("unexecuted proposal acquired a result: %#v", item)
		}
		return
	}
	if item.Receipt == nil || item.Receipt.ExitCode != receipt.ExitCode || item.Receipt.RequestID != receipt.RequestID ||
		(item.ResultStatus == "completed") != (receipt.ExitCode == 0) || item.ResultID == "" {
		t.Fatalf("receipt not projected faithfully: %#v", item)
	}
	detail := get("/runs/" + run.ID + "/host-command-proposals/" + proposal.ID)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "verified helper output") ||
		!strings.Contains(detail.Body.String(), `"evidence_instruction_authorized":false`) {
		t.Fatalf("saved output unavailable: %d %s", detail.Code, detail.Body)
	}
	var saved struct {
		Data httpapi.HostCommandProposalView `json:"data"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	output := saved.Data.SavedOutput
	if output == nil || output.ResultID != item.ResultID || output.RequestID != receipt.RequestID ||
		output.Stdout.Text != "verified helper output" || output.Stdout.UTF8Bytes != 22 ||
		output.Stdout.Truncated || !output.Stdout.Redacted || output.Stderr.Text != "" ||
		output.Stderr.UTF8Bytes != 0 || output.Stderr.Truncated || !output.Stderr.Redacted {
		t.Fatalf("saved streams were not projected from the sealed result: %#v", output)
	}
	listed := get("/runs/" + run.ID + "/host-command-proposals")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), `"saved_output"`) {
		t.Fatalf("collection eagerly exposed output bodies: %d %s", listed.Code, listed.Body)
	}
	wrong := get("/runs/another-run/host-command-proposals/" + proposal.ID)
	if wrong.Code != http.StatusNotFound || strings.Contains(wrong.Body.String(), "verified helper output") {
		t.Fatalf("cross-Run output leaked: %d %s", wrong.Code, wrong.Body)
	}
	exported := get("/runs/" + run.ID + "/code-handoff/export?format=markdown")
	if exported.Code != http.StatusOK || !strings.Contains(exported.Body.String(), "historical execution receipts") ||
		!strings.Contains(exported.Body.String(), proposal.ID) {
		t.Fatalf("host receipt missing in export: %d %s", exported.Code, exported.Body)
	}
}
