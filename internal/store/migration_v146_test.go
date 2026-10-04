package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV146DefersRunningThreadPermissionAndMaterializesSuccessor(t *testing.T) {
	ctx := context.Background()
	state := openHistoricalTestDatabase(t,
		filepath.Join(t.TempDir(), "schema-v145-deferred-thread-permission.db"), 145)
	plan := migrationPlan()
	restoreLegacyInputs := addCurrentInputColumnsForLegacySeed(t, state)
	runs := newMigrationFixtureRunService(t, state)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal:    "defer a permission preference until the successor Run",
		Profile: "code", Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	restoreLegacyInputs()
	if err := state.applyMigration(ctx, plan[145]); err != nil {
		t.Fatal(err)
	}

	preference, selected, err := seedHistoricalThreadWorkspacePreference(ctx, state, threadRecord.ID,
		"migration-v146-deferred-thread-permission-0001", "test_operator",
		"apply bounded Workspace Access to the next Run")
	if err != nil || selected.CurrentRunID != run.ID ||
		selected.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("deferred selection=%+v err=%v", selected, err)
	}
	currentPermission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || currentPermission.Mode != domain.RunExecutionPermissionConservative ||
		currentPermission.Revision != 1 {
		t.Fatalf("current Run permission changed: %+v err=%v", currentPermission, err)
	}
	currentRun, err := state.GetRun(ctx, run.ID)
	if err != nil || currentRun.Status != domain.RunRunning {
		t.Fatalf("current Run lifecycle changed: %+v err=%v", currentRun, err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 146 {
		t.Fatalf("schema version=%d want=146 err=%v", version, err)
	}
	// Verify v146's deferred preference before upgrading the later schema used
	// by current lifecycle and Thread message writers.
	if err := state.applyMigrations(ctx, plan); err != nil {
		t.Fatal(err)
	}

	if retained, err := state.GetThreadExecutionPermission(ctx, threadRecord.ID); err != nil || !reflect.DeepEqual(retained, preference) {
		t.Fatalf("upgrade rewrote the historical Thread preference: %+v err=%v", retained, err)
	}
	if _, err := runs.Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue with the deferred permission preference",
			OperationKey: "migration-v146-successor-message-0001",
			RequestedBy:  "test_operator",
		})
	if err != nil || !continued.SuccessorCreated {
		t.Fatalf("successor=%+v err=%v", continued, err)
	}
	successorPermission, err := state.GetRunExecutionPermission(ctx, continued.Run.ID)
	if err != nil || successorPermission.Mode != domain.RunExecutionPermissionAsk ||
		successorPermission.ProtocolVersion != domain.RunApprovalPermissionProtocolVersion ||
		successorPermission.Revision != 1 || successorPermission.ProcessEnabled ||
		successorPermission.ExecutionAuthorized || successorPermission.CapabilityGrant {
		t.Fatalf("successor did not materialize deferred preference: %+v err=%v",
			successorPermission, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}
