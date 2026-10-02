package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/projectconfig"
)

func TestProjectInstructionHTTPDeliveryRequiresExplicitControlConfirmation(t *testing.T) {
	fixture := newAPIFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.workspace.RootPath, "AGENTS.md"), []byte("EXACT_HTTP_REQUIRED_RULE"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := projectconfig.DiscoverInstructions(t.Context(), fixture.workspace.RootPath, ".")
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(), application.CreateRunRequest{
		Goal: "classify exact project rules", Profile: "review", WorkspaceID: fixture.workspace.ID,
		Budget: domain.DefaultBudget(), ProjectInstructions: &snapshot, RequestedBy: "http_test_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + run.ID + "/project-instructions/refresh"
	view := projectInstructionRefreshRequestView{TargetPath: ".", ExpectedFingerprint: snapshot.Fingerprint,
		ExpectedLiveFingerprint: snapshot.Fingerprint, Classifications: []projectconfig.InstructionSourceDelivery{{
			Path: snapshot.Sources[0].Path, ContentSHA256: snapshot.Sources[0].ContentSHA256, Requirement: projectconfig.InstructionMandatory,
		}}}
	encode := func() string {
		t.Helper()
		body, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	var state application.ProjectInstructionState
	decodeData(t, continuityHTTPMutation(t, fixture.api, http.MethodPost, path, testControlToken, encode()), &state)
	if state.Pinned.Snapshot.Delivery != nil || state.Pinned.Revision != 1 {
		t.Fatal("preview persisted classification without confirmation")
	}
	view.Confirm = true
	assertAPIError(t, continuityHTTPMutation(t, fixture.api, http.MethodPost, path, testAccessToken, encode()),
		http.StatusUnauthorized, "POLICY_DENIED")
	view.Classifications[0].ContentSHA256 = strings.Repeat("0", 64)
	assertAPIError(t, continuityHTTPMutation(t, fixture.api, http.MethodPost, path, testControlToken, encode()),
		http.StatusBadRequest, "INVALID_ARGUMENT")
	view.Classifications[0].ContentSHA256 = snapshot.Sources[0].ContentSHA256
	decodeData(t, continuityHTTPMutation(t, fixture.api, http.MethodPost, path, testControlToken, encode()), &state)
	if !state.RefreshConfirmed || state.Stale || state.CapabilityGrant || state.Pinned.Revision != 2 ||
		state.Pinned.ConfirmedBy != "http_control" || state.Pinned.Snapshot.Delivery.Sources[0].Requirement != projectconfig.InstructionMandatory ||
		len(state.Pinned.Diff.Changed) != 1 {
		t.Fatalf("confirmed delivery lacks exact source/history/actor binding: %+v", state)
	}
	if _, err := application.NewRunService(fixture.store).Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	view.ExpectedFingerprint, view.ExpectedLiveFingerprint = state.Pinned.Snapshot.Fingerprint, state.Live.Fingerprint
	view.Classifications[0].Requirement = projectconfig.InstructionOptional
	assertAPIError(t, continuityHTTPMutation(t, fixture.api, http.MethodPost, path, testControlToken, encode()),
		http.StatusPreconditionFailed, "FAILED_PRECONDITION")
	if _, err := application.NewRunService(fixture.store).Pause(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	decodeData(t, continuityHTTPMutation(t, fixture.api, http.MethodPost, path, testControlToken, encode()), &state)
	if state.Pinned.Revision != 3 || state.Pinned.Snapshot.Delivery.Sources[0].Requirement != projectconfig.InstructionOptional || state.Stale {
		t.Fatal("paused operator could not explicitly renew classification")
	}
}
