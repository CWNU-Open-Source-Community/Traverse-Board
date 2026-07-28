package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestRunExecutionInteractionControlRequiresExplicitTrustAndRemainsNonAuthorizing(
	t *testing.T,
) {
	fixture := newAPIFixture(t)
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{
			Goal: "select controlled interaction through HTTP", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionProfileService(fixture.store).Change(t.Context(),
		application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: string(domain.RunExecutionProfileLocal),
			OperationKey: "http-interaction-profile-0001",
			RequestedBy:  "test_operator", Reason: "prepare local controlled profile",
		}); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + run.ID + "/execution-interaction"
	body := `{"mode":"controlled","trust":"trusted","confirm_workspace_trust":true,` +
		`"reason":"operator trusts this workspace"}`
	first := performControlPathRequest(t, fixture.api, path,
		"http-execution-interaction-0001", strings.NewReader(body))
	if raw := first.Body.String(); strings.Contains(raw, "requested_by") ||
		strings.Contains(raw, "operator trusts this workspace") {
		t.Fatalf("interaction response exposed private audit text: %s", raw)
	}
	var selected RunExecutionInteractionControlView
	decodeDataStatus(t, first, http.StatusAccepted, &selected)
	interaction := selected.ExecutionInteraction
	if selected.Replayed ||
		interaction.Mode != string(domain.RunExecutionInteractionControlled) ||
		interaction.WorkspaceTrust != string(domain.WorkspaceTrustTrusted) ||
		interaction.CommandForm != string(domain.ExecutionCommandStructuredArgv) ||
		interaction.PersistentTerminal || interaction.UserInputAvailable ||
		interaction.AgentInputDefault || interaction.ProcessEnabled ||
		interaction.ExecutionAuthorized || interaction.CapabilityGrant {
		t.Fatalf("interaction selection escaped its boundary: %#v", selected)
	}
	replay := performControlPathRequest(t, fixture.api, path,
		"http-execution-interaction-0001", strings.NewReader(body))
	var replayed RunExecutionInteractionControlView
	decodeDataStatus(t, replay, http.StatusAccepted, &replayed)
	if !replayed.Replayed || replayed.ExecutionInteraction.Revision != interaction.Revision {
		t.Fatalf("interaction replay changed result: %#v", replayed)
	}
}

func TestRunExecutionInteractionControlRejectsImplicitTrustAndUnknownFields(t *testing.T) {
	fixture := newAPIFixture(t)
	_, run, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{
			Goal: "reject implicit interaction trust", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + run.ID + "/execution-interaction"
	implicit := performControlPathRequest(t, fixture.api, path,
		"http-execution-interaction-implicit",
		strings.NewReader(`{"mode":"controlled","trust":"trusted"}`))
	assertAPIError(t, implicit, http.StatusBadRequest, "INVALID_ARGUMENT")
	unknown := performControlPathRequest(t, fixture.api, path,
		"http-execution-interaction-unknown",
		strings.NewReader(`{"mode":"preview","trust":"untrusted","process_enabled":true}`))
	assertAPIError(t, unknown, http.StatusBadRequest, "INVALID_ARGUMENT")
}
