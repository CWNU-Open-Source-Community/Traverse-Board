package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

func removeSchemaV139ForTestStatements() []string {
	return append(removeSchemaV140ForTestStatements(), []string{
		`DROP TRIGGER trg_thread_execution_permission_operation_delete_immutable`,
		`DROP TRIGGER trg_thread_execution_permission_operation_update_immutable`,
		`DROP TRIGGER trg_thread_execution_permission_snapshot_delete_immutable`,
		`DROP TRIGGER trg_thread_execution_permission_snapshot_update_immutable`,
		`DROP TRIGGER trg_thread_execution_permission_operation_insert`,
		`DROP TRIGGER trg_thread_execution_permission_snapshot_insert`,
		`DROP TABLE thread_execution_permission_operations`,
		`DROP INDEX idx_thread_execution_permission_snapshots_thread_revision`,
		`DROP TABLE thread_execution_permission_snapshots`,
		`DELETE FROM schema_migrations WHERE version = 139`,
	}...)
}

func TestSchemaV139BackfillsConservativeThreadPermission(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "thread-permission-v138.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "legacy Thread permission", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV139ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v138 with %q: %v", statement, err)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 138 {
		state.Close()
		t.Fatalf("restored schema version=%d want=138 err=%v", version, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	preference, err := upgraded.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preference.ThreadID != threadRecord.ID || preference.MissionID != run.MissionID ||
		preference.Revision != 1 ||
		preference.Mode != domain.RunExecutionPermissionConservative ||
		preference.RequestedBy != "schema_v139" || preference.ProcessEnabled ||
		preference.ExecutionAuthorized || preference.CapabilityGrant {
		t.Fatalf("unexpected v139 Thread permission: %+v", preference)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("upgraded version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func TestThreadPermissionPreferenceUpdatesPausedRunThenMaterializesSuccessor(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "thread-permission-successor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "continue with Thread permission", Profile: "code",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	initialRunPermission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	runs := application.NewRunService(state)
	running, err := runs.Start(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != domain.RunRunning {
		t.Fatalf("Run did not start: %+v", running)
	}
	if _, err := runs.Pause(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	request := application.ChangeThreadExecutionPermissionRequest{
		ThreadID:     threadRecord.ID,
		Mode:         string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "thread-permission-successor-0001", RequestedBy: "test_operator",
		Reason:                 "use bounded Workspace Access for future Runs",
		ConfirmWorkspaceAccess: true,
	}
	selected, err := service.Change(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Replayed || selected.Permission.Revision != 2 ||
		selected.Permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		selected.CurrentRunID != run.ID ||
		selected.CurrentRunEffect != domain.ThreadExecutionPermissionApplied ||
		selected.Permission.ProcessEnabled || selected.Permission.ExecutionAuthorized ||
		selected.Permission.CapabilityGrant {
		t.Fatalf("unexpected Thread preference: %+v", selected)
	}
	replayed, err := service.Change(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Permission.ID != selected.Permission.ID {
		t.Fatalf("Thread preference replay=%+v err=%v", replayed, err)
	}
	applied, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || applied.ID == initialRunPermission.ID ||
		applied.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		applied.ProcessEnabled || applied.ExecutionAuthorized || applied.CapabilityGrant {
		t.Fatalf("current Run permission was not safely applied: before=%+v after=%+v err=%v",
			initialRunPermission, applied, err)
	}
	paused, err := state.GetRun(ctx, run.ID)
	if err != nil || paused.Status != domain.RunPaused {
		t.Fatalf("current Run was not paused: run=%+v err=%v", paused, err)
	}
	if _, err := runs.Fail(ctx, paused.ID, "create successor fixture"); err != nil {
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion,
			ThreadID: threadRecord.ID, Content: "continue safely",
			OperationKey: "thread-permission-successor-message-0001",
			RequestedBy:  "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	if !continued.SuccessorCreated || continued.Run.ID == run.ID {
		t.Fatalf("successor was not created: %+v", continued)
	}
	materialized, err := state.GetRunExecutionPermission(ctx, continued.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		materialized.Revision != 2 || materialized.ProcessEnabled ||
		materialized.ExecutionAuthorized || materialized.CapabilityGrant {
		t.Fatalf("Thread preference was not safely materialized: %+v", materialized)
	}
	if lease, found, err := state.GetRunExecutionLease(ctx, continued.Run.ID); err != nil || found {
		t.Fatalf("successor inherited an execution lease: lease=%+v found=%t err=%v",
			lease, found, err)
	}
	for _, statement := range []string{
		`UPDATE thread_execution_permission_snapshots SET process_enabled = 1 WHERE id = ?`,
		`DELETE FROM thread_execution_permission_snapshots WHERE id = ?`,
	} {
		if _, err := state.db.ExecContext(ctx, statement, selected.Permission.ID); err == nil {
			t.Fatalf("immutable Thread preference statement succeeded: %s", statement)
		}
	}

	request.Mode = string(domain.RunExecutionPermissionConservative)
	request.ConfirmWorkspaceAccess = false
	if _, err := service.Change(ctx, request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("reused operation key error=%v", err)
	}
}

func TestThreadPermissionDefersWhileCurrentRunHasActiveLease(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "thread-permission-active-lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "reject permission drift during execution",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	_ = acquireTestRunExecutionLease(t, ctx, state, run.ID)
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	selected, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID:     threadRecord.ID,
		Mode:         string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "thread-permission-active-lease-0001",
		RequestedBy:  "test_operator", Reason: "must not drift a leased Run",
		ConfirmWorkspaceAccess: true,
	})
	if err != nil || selected.CurrentRunID != run.ID ||
		selected.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("active lease preference was not deferred: selected=%+v err=%v",
			selected, err)
	}
	preference, getErr := state.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if getErr != nil || preference.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		preference.Revision != 2 {
		t.Fatalf("deferred Thread preference was not durable: %+v err=%v", preference, getErr)
	}
	permission, getErr := state.GetRunExecutionPermission(ctx, run.ID)
	if getErr != nil || permission.Mode != domain.RunExecutionPermissionConservative ||
		permission.Revision != 1 {
		t.Fatalf("deferred request changed Run permission: %+v err=%v", permission, getErr)
	}
	storedRun, getErr := state.GetRun(ctx, run.ID)
	if getErr != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("deferred request changed current Run: %+v err=%v", storedRun, getErr)
	}
}

func TestThreadPermissionDefersWithoutTouchingPersistentExecutionSurface(t *testing.T) {
	ctx := context.Background()
	state, run, threadRecord := threadLifecycleFixture(t, ctx, domain.RunRunning)
	defer state.Close()
	now := time.Now().UTC()
	if err := state.CreateTerminalSession(ctx, TerminalSessionRecord{
		ID: "terminal-thread-permission", ProtocolVersion: "user_terminal_session.v1",
		RunID: run.ID, WorkspaceID: "workspace-thread-permission", State: "running",
		Cwd: ".", Columns: 120, Rows: 30, CreatedAt: now, LastActivityAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	selected, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "thread-permission-terminal-0001", RequestedBy: "test_operator",
		Reason: "must not drift a persistent terminal", ConfirmWorkspaceAccess: true,
	})
	if err != nil || selected.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("persistent terminal preference was not deferred: selected=%+v err=%v",
			selected, err)
	}
	preference, getErr := state.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if getErr != nil || preference.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		preference.Revision != 2 {
		t.Fatalf("deferred Thread preference was not durable: %+v err=%v", preference, getErr)
	}
	storedRun, getErr := state.GetRun(ctx, run.ID)
	if getErr != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("deferred request changed persistent Run: %+v err=%v", storedRun, getErr)
	}
}

func TestThreadPermissionPreservesPendingApprovalGate(t *testing.T) {
	ctx := context.Background()
	state, run, threadRecord := threadLifecycleFixture(t, ctx, domain.RunWaitingApproval)
	defer state.Close()
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	selected, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "thread-permission-waiting-approval-0001", RequestedBy: "test_operator",
		Reason: "must preserve the pending approval gate", ConfirmWorkspaceAccess: true,
	})
	if err != nil || selected.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("pending approval preference was not deferred: selected=%+v err=%v",
			selected, err)
	}
	storedRun, getErr := state.GetRun(ctx, run.ID)
	if getErr != nil || storedRun.Status != domain.RunWaitingApproval {
		t.Fatalf("deferred request changed approval gate: %+v err=%v", storedRun, getErr)
	}
	linked, getErr := state.GetSession(ctx, run.SessionID)
	if getErr != nil || linked.Status != session.StatusActive {
		t.Fatalf("failed request changed linked Session: %+v err=%v", linked, getErr)
	}
}
