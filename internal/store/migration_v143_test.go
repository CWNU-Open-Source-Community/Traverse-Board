package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV143UpgradesPopulatedRunningPermissionForImmediateDowngrade(
	t *testing.T,
) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v142-running-permission.db"))
	defer state.Close()
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 142); err != nil {
		t.Fatal(err)
	}

	runs := application.NewRunService(state)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "prove v143 running high-risk downgrade", Profile: "code",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	permissions := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			DebugMaximumAccessEnabled: true,
		})
	debug, err := permissions.Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionDebug),
			OperationKey: "migration-v143-debug-selection-0001",
			RequestedBy:  "test_operator", Reason: "prepare populated v142 state",
			ConfirmDebugAccess: true,
		})
	if err != nil || debug.Permission.Mode != domain.RunExecutionPermissionDebug {
		t.Fatalf("prepare v142 Debug permission=%+v err=%v", debug, err)
	}
	if _, err := runs.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: run.ID,
			OwnerID: "migration-v143-old-owner", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	if err := state.applyMigration(ctx, migrationPlan()[142]); err != nil {
		t.Fatal(err)
	}
	selected, err := permissions.Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionApproval),
			OperationKey: "migration-v143-running-downgrade-0001",
			RequestedBy:  "test_operator", Reason: "revoke live high-risk authority",
			ConfirmUserApproval: true,
		})
	if err != nil || selected.Permission.Mode != domain.RunExecutionPermissionApproval {
		t.Fatalf("v143 running downgrade=%+v err=%v", selected, err)
	}
	currentLease, found, err := state.GetRunExecutionLease(ctx, run.ID)
	if err != nil || !found || currentLease.LeaseID != lease.Lease.LeaseID ||
		currentLease.Status != domain.RunExecutionLeaseReleased {
		t.Fatalf("v143 did not release the stale execution lease: lease=%+v found=%t err=%v",
			currentLease, found, err)
	}
	browserPermission, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || browserPermission.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("v143 downgrade left Full CDP enabled: %+v err=%v",
			browserPermission, err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 143 {
		t.Fatalf("schema version=%d want=143 err=%v", version, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}
