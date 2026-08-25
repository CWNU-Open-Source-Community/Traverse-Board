package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func removeSchemaV133ForTestStatements() []string {
	createSnapshots := requireMigrationStatement(
		"CREATE TABLE run_execution_interaction_snapshots (",
		runExecutionInteractionStatements)
	createSnapshots = strings.Replace(createSnapshots,
		"CREATE TABLE run_execution_interaction_snapshots (",
		"CREATE TABLE run_execution_interaction_snapshots_v132 (", 1)
	createOperations := requireMigrationStatement(
		"CREATE TABLE run_execution_interaction_operations (",
		runExecutionInteractionStatements)
	createOperations = strings.Replace(createOperations,
		"CREATE TABLE run_execution_interaction_operations (",
		"CREATE TABLE run_execution_interaction_operations_v132 (", 1)
	statements := []string{
		`PRAGMA foreign_keys = OFF`,
		`PRAGMA legacy_alter_table = ON`,
		`DROP TRIGGER trg_standard_code_preset_operation_insert`,
		`DROP TRIGGER trg_standard_code_preset_operation_update`,
		`DROP TRIGGER trg_standard_code_preset_operation_delete_immutable`,
		`DROP INDEX idx_standard_code_preset_operations_run_created`,
		`DROP TABLE standard_code_preset_operations`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_insert`,
		`DROP TRIGGER trg_run_execution_interaction_operation_insert`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_update_immutable`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_delete_immutable`,
		`DROP TRIGGER trg_run_execution_interaction_operation_update_immutable`,
		`DROP TRIGGER trg_run_execution_interaction_operation_delete_immutable`,
		`DROP INDEX idx_run_execution_interaction_snapshots_run_revision`,
		createSnapshots,
		`INSERT INTO run_execution_interaction_snapshots_v132
			SELECT * FROM run_execution_interaction_snapshots`,
		createOperations,
		`INSERT INTO run_execution_interaction_operations_v132
			SELECT * FROM run_execution_interaction_operations`,
		`DROP TABLE run_execution_interaction_operations`,
		`DROP TABLE run_execution_interaction_snapshots`,
		`ALTER TABLE run_execution_interaction_snapshots_v132
			RENAME TO run_execution_interaction_snapshots`,
		`ALTER TABLE run_execution_interaction_operations_v132
			RENAME TO run_execution_interaction_operations`,
		requireMigrationStatement(
			"CREATE INDEX idx_run_execution_interaction_snapshots_run_revision",
			runExecutionInteractionStatements),
	}
	for _, name := range []string{
		"trg_run_execution_interaction_snapshot_insert",
		"trg_run_execution_interaction_operation_insert",
		"trg_run_execution_interaction_snapshot_update_immutable",
		"trg_run_execution_interaction_snapshot_delete_immutable",
		"trg_run_execution_interaction_operation_update_immutable",
		"trg_run_execution_interaction_operation_delete_immutable",
	} {
		statements = append(statements,
			requireMigrationTrigger(name, runExecutionInteractionStatements))
	}
	return append(statements,
		`DELETE FROM schema_migrations WHERE version = 133`,
		`PRAGMA legacy_alter_table = OFF`,
		`PRAGMA foreign_keys = ON`)
}

func TestSchemaV133PreservesControlledInteractionAndDependentTriggers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "standard-code-v132.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(ctx,
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
	for _, statement := range removeSchemaV133ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v132 with %q: %v", statement, err)
		}
	}
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
