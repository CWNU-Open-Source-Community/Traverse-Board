package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV133PreservesControlledInteractionAndDependentTriggers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "standard-code-v132.db")
	state := openHistoricalTestDatabase(t, filepath.Join(t.TempDir(), "seed.db"), 177)
	_, run, err := newMigrationFixtureRunService(t, state).Create(ctx,
		application.CreateRunRequest{Goal: "v133 controlled interaction",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionProfileService(state).Change(ctx,
		application.ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "local",
			OperationKey: "v133-local-profile-0001", RequestedBy: "test_operator",
			Reason: "preserve Local profile"}); err != nil {
		t.Fatal(err)
	}
	selected, err := application.NewRunExecutionInteractionService(state).Change(ctx,
		application.ChangeRunExecutionInteractionRequest{RunID: run.ID,
			Mode: "controlled", Trust: "trusted",
			OperationKey: "v133-controlled-interaction-0001",
			RequestedBy:  "test_operator", Reason: "preserve controlled interaction",
			ConfirmWorkspaceTrust: true})
	if err != nil {
		t.Fatal(err)
	}
	state = historicalTestDatabaseFromSeed(t, state, path, 132)
	if version, err := state.SchemaVersion(ctx); err != nil || version != 132 {
		state.Close()
		t.Fatalf("restored schema version=%d want=132 err=%v", version, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	interaction, err := upgraded.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil || interaction.ID != selected.Interaction.ID ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.RequiredGate != domain.ExecutionInteractionGateLocalOSSandbox {
		t.Fatalf("upgraded interaction=%+v err=%v", interaction, err)
	}
	var triggerSQL string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'trigger' AND name = 'trg_controlled_command_proposal_insert_binding'`).
		Scan(&triggerSQL); err != nil ||
		strings.Contains(triggerSQL, "run_execution_interaction_snapshots_v") ||
		!strings.Contains(triggerSQL, "run_execution_interaction_snapshots") {
		t.Fatalf("dependent trigger was rewritten to a temporary table: %q err=%v",
			triggerSQL, err)
	}
	var tableSQL string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_execution_interaction_snapshots'`).
		Scan(&tableSQL); err != nil || !strings.Contains(tableSQL, "docker_sandbox_gate") {
		t.Fatalf("v133 Docker controlled constraint missing: %q err=%v", tableSQL, err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}
