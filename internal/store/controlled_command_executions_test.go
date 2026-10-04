package store

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestSchemaV87AddsControlledCommandExecutionAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v86-controlled-execution.db")
	st, err := openHistoricalMigrationFixture(t, path, 86)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The immutable historical prefix above is the upgrade input.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{
		"controlled_command_execution_intents",
		"controlled_command_execution_receipts",
	} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}

func controlledExecutionStorePlan(t *testing.T, ctx context.Context,
	st *SQLiteStore,
) runner.ControlledCommandPlan {
	t.Helper()
	workspaceRoot := filepath.Clean(t.TempDir())
	workspace := WorkspaceRecord{
		ID: "workspace-controlled-execution", Name: "controlled-execution",
		RootPath: workspaceRoot,
	}
	if err := st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	mission, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "audit one controlled command", Profile: "code",
			WorkspaceID: workspace.ID,
			Budget:      domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	profileResult, err := application.NewRunExecutionProfileService(st).Change(
		ctx, application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "local",
			OperationKey: "controlled-execution-profile-0001",
			RequestedBy:  "test_operator", Reason: "prepare controlled command",
		})
	if err != nil {
		t.Fatal(err)
	}
	interactionResult, err :=
		application.NewRunExecutionInteractionService(st).Change(ctx,
			application.ChangeRunExecutionInteractionRequest{
				RunID: run.ID, Mode: "controlled", Trust: "trusted",
				OperationKey: "controlled-execution-interaction-0001",
				RequestedBy:  "test_operator", Reason: "trusted test workspace",
				ConfirmWorkspaceTrust: true,
			})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runner.PlanControlledCommand(runner.ControlledCommandPlanRequest{
		ID: "controlled-command-plan-0001", WorkspaceID: mission.WorkspaceID,
		WorkspaceRoot:  workspaceRoot,
		Interaction:    interactionResult.Interaction,
		CurrentProfile: profileResult.Profile,
		CurrentSurface: domain.ExecutionSurfaceCode,
		Kind:           runner.ControlledCommandGoVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func removeSchemaV87ForTestStatements() []string {
	return append(removeSchemaV88ForTestStatements(), []string{
		`DROP TRIGGER trg_controlled_execution_receipt_delete_immutable`,
		`DROP TRIGGER trg_controlled_execution_receipt_update_immutable`,
		`DROP TRIGGER trg_controlled_execution_intent_delete_immutable`,
		`DROP TRIGGER trg_controlled_execution_intent_update_immutable`,
		`DROP TABLE controlled_command_execution_receipts`,
		`DROP INDEX idx_controlled_execution_intents_run_created`,
		`DROP TABLE controlled_command_execution_intents`,
		`DELETE FROM schema_migrations WHERE version = 87`,
	}...)
}
