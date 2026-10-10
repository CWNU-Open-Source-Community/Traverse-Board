package store

import (
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV188PreservesHistoryAndBindsDistinctSBXProfile(t *testing.T) {
	ctx := t.Context()
	file := filepath.Join(t.TempDir(), "v187.db")
	state := openHistoricalTestDatabase(t, file, 187)
	_, run, err := newMigrationFixtureRunService(t, state).Create(ctx,
		application.CreateRunRequest{Goal: "preserve selected Local environment", Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := application.NewRunExecutionProfileService(state).Change(ctx,
		application.ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "local", OperationKey: "v188-local-choice-001", RequestedBy: "operator", Reason: "select Local"})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := application.NewRunExecutionInteractionService(state).Change(ctx,
		application.ChangeRunExecutionInteractionRequest{RunID: run.ID, Mode: "controlled", Trust: "trusted", ConfirmWorkspaceTrust: true,
			OperationKey: "v188-local-interaction-001", RequestedBy: "operator", Reason: "configure coding"})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	oldProfile, err := upgraded.GetRunExecutionProfile(ctx, run.ID)
	if err != nil || oldProfile != profile.Profile {
		t.Fatalf("profile changed: %+v err=%v", oldProfile, err)
	}
	oldInteraction, err := upgraded.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil || oldInteraction != interaction.Interaction {
		t.Fatalf("interaction changed: %+v err=%v", oldInteraction, err)
	}
	selected, err := application.NewRunExecutionProfileService(upgraded).Change(ctx,
		application.ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "sbx", OperationKey: "v188-sbx-choice-001", RequestedBy: "operator", Reason: "select Docker Sandboxes"})
	if err != nil || selected.Profile.Backend != domain.ExecutionBackendSBX || selected.Profile.RequiredGate != domain.ExecutionGateSBXMicroVM {
		t.Fatalf("SBX profile: %+v err=%v", selected, err)
	}
	controlled, err := application.NewRunExecutionInteractionService(upgraded).Change(ctx,
		application.ChangeRunExecutionInteractionRequest{RunID: run.ID, Mode: "controlled", Trust: "trusted", ConfirmWorkspaceTrust: true,
			OperationKey: "v188-sbx-interaction-001", RequestedBy: "operator", Reason: "configure microVM coding"})
	if err != nil || controlled.Interaction.RequiredGate != domain.ExecutionInteractionGateSBXMicroVM {
		t.Fatalf("SBX interaction: %+v err=%v", controlled, err)
	}
	var rewritten int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='trigger' AND sql LIKE '%_v188%'`).Scan(&rewritten); err != nil || rewritten != 0 {
		t.Fatalf("temporary table leaked into triggers: %d %v", rewritten, err)
	}
	var definition string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name='trg_standard_code_preset_operation_update'`).Scan(&definition); err != nil || !strings.Contains(definition, "thread_continuation") {
		t.Fatalf("lost later continuation trigger: %v", err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}
