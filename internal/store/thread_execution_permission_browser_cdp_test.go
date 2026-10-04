package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
)

func threadFullCDPTestFixture(t *testing.T) (context.Context, *SQLiteStore,
	domain.Run, domain.Thread, *application.ThreadExecutionPermissionService,
) {
	t.Helper()
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "thread-full-cdp.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "test Thread Full CDP semantics", Profile: "code",
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
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
			DangerFullAccessEnabled: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
		})
	return ctx, state, run, threadRecord, service
}

// Seed genuine v1 tuples under the immutable v177 schema, then use Open for
// the real upgrade. Current writers cannot manufacture these records. No
// trigger, schema constraint or existing row is weakened for the fixture.
func legacyDebugThreadFullCDPTestFixture(t *testing.T) (context.Context, *SQLiteStore,
	domain.Run, domain.Thread, *application.ThreadExecutionPermissionService,
) {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "retained-debug-cdp.db")
	state := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 177); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if state != nil {
			_ = state.Close()
		}
	})
	const workspaceID = "workspace-retained-debug-cdp"
	if err := state.SaveWorkspace(ctx, WorkspaceRecord{ID: workspaceID,
		Name: "retained Debug", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	run := seedLegacyStructuredToolRun(t, state, workspaceID,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionDebug)
	mission, err := state.GetMission(ctx, run.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	threadID := domain.InitialThreadID(run.ID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO threads
		(id, protocol_version, workspace_id, mission_id, title, status,
		active_run_id, last_run_id, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, 0, ?, ?)`, threadID,
		domain.ThreadProtocolVersion, workspaceID, mission.ID, mission.Goal,
		domain.ThreadActive, ts(run.CreatedAt), ts(run.UpdatedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_runs
		(thread_id, run_id, session_id, ordinal, predecessor_run_id, created_at)
		VALUES (?, ?, ?, 1, NULL, ?)`, threadID, run.ID, run.SessionID, ts(run.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	threadRecord, err := scanThread(tx.QueryRowContext(ctx, threadSelect+` WHERE id = ?`, threadID))
	if err != nil {
		t.Fatal(err)
	}
	template, err := domain.NewInitialThreadExecutionPermissionSnapshot(
		"unused-current-thread-template", threadRecord, "retained_fixture", run.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := template.Next("retained-thread-conservative", domain.RunExecutionPermissionConservative,
		false, "retained_fixture", "retained v1 initial preference", run.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	initial.Revision = 1
	if err := insertThreadExecutionPermissionSnapshotTx(ctx, tx, initial); err != nil {
		t.Fatal(err)
	}
	debug, err := initial.Next("retained-thread-debug", domain.RunExecutionPermissionDebug,
		true, "retained_fixture", "retained v1 Debug selection", run.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertThreadExecutionPermissionSnapshotTx(ctx, tx, debug); err != nil {
		t.Fatal(err)
	}
	browser, err := getCurrentRunBrowserCDPPermissionSnapshot(ctx, tx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := appendThreadManagedRunBrowserCDPTransitionTx(ctx, tx, browser,
		domain.RunBrowserCDPPermissionFullDebug, "retained_fixture",
		"retained Debug browser preference", run.CreatedAt,
		runmutation.Fingerprint("retained-debug-browser-preference", run.ID)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || after != before || after.Mode != domain.RunExecutionPermissionDebug ||
		after.ProtocolVersion != domain.RunExecutionPermissionProtocolVersion ||
		after.ProcessEnabled || after.ExecutionAuthorized || after.CapabilityGrant {
		t.Fatalf("retained Run changed across reopen: before=%+v after=%+v err=%v", before, after, err)
	}
	retained, err := state.GetThreadExecutionPermission(ctx, threadID)
	if err != nil || retained != debug {
		t.Fatalf("retained Thread changed across reopen: before=%+v after=%+v err=%v", debug, retained, err)
	}
	upgradedLedger, err := state.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for version, entry := range ledger {
		if upgradedLedger[version] != entry {
			t.Fatalf("retained migration %d changed", version)
		}
	}
	assertNoForeignKeyViolations(t, state.db)
	assertLatestMigrationLedger(t, state, migrationPlan())
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
	}
	if capabilities.AllowsSnapshot(after) {
		t.Fatal("retained Debug data acquired current process authority")
	}
	service := application.NewThreadExecutionPermissionService(state, capabilities)
	if _, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadID, Mode: "debug", ConfirmDebugAccess: true,
		OperationKey: "retained-debug-thread-write-rejected", RequestedBy: "test_operator",
		Reason: "retained reads do not reopen retired writers",
	}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("retired Thread Debug writer was accepted: %v", err)
	}
	if _, err := application.NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: "debug", ConfirmDebugAccess: true,
			OperationKey: "retained-debug-run-write-rejected", RequestedBy: "test_operator",
			Reason: "retained reads do not reopen retired writers",
		}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("retired Run Debug writer was accepted: %v", err)
	}
	return ctx, state, run, threadRecord, service
}

func selectThreadPermissionForFullCDPTest(t *testing.T, ctx context.Context,
	service *application.ThreadExecutionPermissionService, threadID string,
	mode domain.RunExecutionPermissionMode, operationKey string,
) application.ChangeThreadExecutionPermissionResult {
	t.Helper()
	request := application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadID, Mode: string(mode), OperationKey: operationKey,
		RequestedBy: "test_operator", Reason: "verify nested Full CDP policy",
	}
	if mode == domain.RunExecutionPermissionFull {
		request.ConfirmFull = true
	}
	selected, err := service.Change(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

func transitionRunBrowserCDPForStoreTest(t *testing.T, ctx context.Context,
	state *SQLiteStore, runID string, mode domain.RunBrowserCDPPermissionMode,
	operationLabel string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	t.Helper()
	current, err := state.GetRunBrowserCDPPermission(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if at.Before(current.CreatedAt) {
		at = current.CreatedAt
	}
	next, err := current.Next(idgen.New("run-browser-cdp-permission"), mode,
		mode == domain.RunBrowserCDPPermissionFullDebug, "test_operator",
		"exercise the running Run Full CDP safety boundary", at)
	if err != nil {
		t.Fatal(err)
	}
	operation := domain.RunBrowserCDPPermissionOperation{
		KeyDigest: runmutation.Fingerprint(
			"thread-full-cdp-store-test-operation", runID, operationLabel),
		RequestFingerprint: runBrowserCDPPermissionRequestFingerprint(next),
		SnapshotID:         next.ID, RunID: next.RunID, RequestedBy: next.RequestedBy,
		CreatedAt: next.CreatedAt,
	}
	event, err := newThreadManagedRunBrowserCDPPermissionSelectedEvent(current, next)
	if err != nil {
		t.Fatal(err)
	}
	executionPermission, executionErr := state.GetRunExecutionPermission(ctx, runID)
	if executionErr != nil {
		t.Fatal(executionErr)
	}
	stored, _, err := state.TransitionRunBrowserCDPPermission(
		ctx, next, operation, event, executionPermission)
	return stored, err
}

func TestThreadPermissionDefaultsFullCDPOnAndForcesItOffOnDowngrade(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()

	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-full-cdp-default-on-0001")
	fullCDP, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fullCDP.Mode != domain.RunBrowserCDPPermissionFullDebug || fullCDP.Revision != 2 ||
		fullCDP.TransportEnabled || fullCDP.BrowserStartAuthorized ||
		fullCDP.RuntimeAuthorized || fullCDP.CapabilityGrant {
		t.Fatalf("Full Access did not default its Full CDP sub-switch on safely: %+v", fullCDP)
	}
	eventList, err := state.ListRunEvents(ctx, run.ID)
	if err != nil || countRunEventType(eventList,
		events.RunBrowserCDPPermissionSelectedEvent) != 1 {
		t.Fatalf("Full CDP enable audit event missing: events=%#v err=%v", eventList, err)
	}
	var operationCount int
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM run_browser_cdp_permission_operations WHERE run_id = ?`, run.ID).
		Scan(&operationCount); err != nil || operationCount != 1 {
		t.Fatalf("Full CDP enable operation count=%d err=%v", operationCount, err)
	}

	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionAsk, "thread-full-cdp-forced-off-0001")
	restricted, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restricted.Mode != domain.RunBrowserCDPPermissionRestricted ||
		restricted.Revision != 3 {
		t.Fatalf("execution downgrade did not force Full CDP off: %+v", restricted)
	}
	preference, err := state.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if err != nil || preference.Mode != domain.RunExecutionPermissionAsk {
		t.Fatalf("Thread downgrade missing: permission=%+v err=%v", preference, err)
	}
	runPermission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || runPermission.Mode != domain.RunExecutionPermissionAsk {
		t.Fatalf("Run downgrade missing: permission=%+v err=%v", runPermission, err)
	}
}

func TestThreadFullPreferenceKeepsBrowserGateAndRuntimeBindingIndependent(t *testing.T) {
	ctx, state, run, threadRecord, _ := threadFullCDPTestFixture(t)
	defer state.Close()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
	}
	selectThreadPermissionForFullCDPTest(t, ctx,
		application.NewThreadExecutionPermissionService(state, capabilities), threadRecord.ID,
		domain.RunExecutionPermissionFull, "independent-browser-full-selection")
	restricted, err := transitionRunBrowserCDPForStoreTest(t, ctx, state, run.ID,
		domain.RunBrowserCDPPermissionRestricted, "independent-browser-disabled")
	if err != nil {
		t.Fatal(err)
	}
	request := application.ChangeRunBrowserCDPPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunBrowserCDPPermissionFullDebug),
		OperationKey: "independent-browser-enable", RequestedBy: "test_operator",
		Reason: "separately confirm the exact browser child permission", ConfirmFullCDPDebug: true,
	}
	fresh := capabilities
	fresh.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
	for _, test := range []struct {
		name      string
		browser   domain.BrowserCDPPermissionRuntimeCapabilities
		execution domain.ExecutionPermissionRuntimeCapabilities
	}{
		{"browser_gate_closed", domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true}, capabilities},
		{"fresh_process_without_current_grant", domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true, FullDebugEnabled: true}, fresh},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := application.NewRunBrowserCDPPermissionServiceWithExecutionCapabilities(state, test.browser, test.execution)
			if _, err := service.Change(ctx, request); apperror.CodeOf(err) != apperror.CodePolicyDenied {
				t.Fatalf("Full preference bypassed independent browser authority: %v", err)
			}
			after, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
			if err != nil || after != restricted {
				t.Fatalf("denied browser upgrade changed its snapshot: before=%+v after=%+v err=%v", restricted, after, err)
			}
		})
	}
	service := application.NewRunBrowserCDPPermissionServiceWithExecutionCapabilities(state,
		domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true, FullDebugEnabled: true}, capabilities)
	missingConfirmation := request
	missingConfirmation.ConfirmFullCDPDebug = false
	if _, err := service.Change(ctx, missingConfirmation); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("parent Full confirmation replaced exact browser confirmation: %v", err)
	}
	selected, err := service.Change(ctx, request)
	if err != nil || selected.Permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		selected.Permission.RuntimeAuthorized || selected.Permission.CapabilityGrant ||
		selected.Permission.BrowserStartAuthorized || selected.Permission.TransportEnabled {
		t.Fatalf("explicit browser selection widened its durable receipt: %+v err=%v", selected, err)
	}
	_, otherRun, err := application.NewRunService(state).Create(ctx, application.CreateRunRequest{
		Goal: "another browser target", Profile: "code", Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	request.RunID = otherRun.ID
	request.OperationKey = "independent-browser-other-run"
	if _, err := service.Change(ctx, request); apperror.CodeOf(err) != apperror.CodePolicyDenied {
		t.Fatalf("another Run borrowed the live Full grant: %v", err)
	}
}

func TestThreadPermissionPreservesLegacyDisabledFullCDPAcrossFullAndSuccessor(t *testing.T) {
	ctx, state, run, threadRecord, service := legacyDebugThreadFullCDPTestFixture(t)
	defer state.Close()

	browserService := application.NewRunBrowserCDPPermissionService(state,
		domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true,
		})
	if _, err := browserService.Change(ctx,
		application.ChangeRunBrowserCDPPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunBrowserCDPPermissionRestricted),
			OperationKey: "thread-full-cdp-disable-0001", RequestedBy: "test_operator",
			Reason: "turn off the Full CDP sub-switch",
		}); err != nil {
		t.Fatal(err)
	}
	disabled, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || disabled.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("Full CDP sub-switch did not turn off: %+v err=%v", disabled, err)
	}

	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-debug-preserves-cdp-off-0001")
	preserved, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || preserved.ID != disabled.ID ||
		preserved.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("retained Debug to Full overwrote the CDP sub-switch: before=%+v after=%+v err=%v",
			disabled, preserved, err)
	}

	runService := application.NewRunService(state)
	if _, err := runService.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.Fail(ctx, run.ID,
		"create Full CDP inheritance successor"); err != nil {
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue with Full CDP disabled",
			OperationKey: "thread-full-cdp-disabled-successor-0001",
			RequestedBy:  "test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	if !continued.SuccessorCreated {
		t.Fatalf("successor was not created: %+v", continued)
	}
	inherited, err := state.GetRunBrowserCDPPermission(ctx, continued.Run.ID)
	if err != nil || inherited.Mode != domain.RunBrowserCDPPermissionRestricted ||
		inherited.Revision != 1 {
		t.Fatalf("successor did not inherit disabled Full CDP: %+v err=%v", inherited, err)
	}
	successorPermission, err := state.GetRunExecutionPermission(ctx, continued.Run.ID)
	if err != nil || successorPermission.Mode != domain.RunExecutionPermissionFull {
		t.Fatalf("successor did not inherit current Full ceiling: %+v err=%v",
			successorPermission, err)
	}
}

func TestThreadPermissionSuccessorInheritsEnabledFullCDPWithoutAuthority(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-full-cdp-on-successor-0001")
	runService := application.NewRunService(state)
	if _, err := runService.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.Fail(ctx, run.ID,
		"create enabled Full CDP successor"); err != nil {
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue with Full CDP enabled",
			OperationKey: "thread-full-cdp-enabled-successor-0001",
			RequestedBy:  "test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := state.GetRunBrowserCDPPermission(ctx, continued.Run.ID)
	if err != nil || inherited.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		inherited.Revision != 2 || inherited.TransportEnabled ||
		inherited.BrowserStartAuthorized || inherited.RuntimeAuthorized ||
		inherited.CapabilityGrant {
		t.Fatalf("successor Full CDP policy/authority mismatch: %+v err=%v", inherited, err)
	}
	eventList, err := state.ListRunEvents(ctx, continued.Run.ID)
	if err != nil || countRunEventType(eventList,
		events.RunBrowserCDPPermissionSelectedEvent) != 1 {
		t.Fatalf("successor Full CDP audit event missing: events=%#v err=%v", eventList, err)
	}
}

func TestThreadPermissionFullAccessAfterLowPredecessorDefaultsSuccessorFullCDPOn(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	runService := application.NewRunService(state)
	if _, err := runService.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.Fail(ctx, run.ID, "finish low-permission predecessor"); err != nil {
		t.Fatal(err)
	}
	selected := selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-full-after-low-predecessor-0001")
	if selected.CurrentRunID != "" ||
		selected.CurrentRunEffect != domain.ThreadExecutionPermissionNoActiveRun {
		t.Fatalf("terminal predecessor was treated as active: %+v", selected)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue after selecting Full Access",
			OperationKey: "thread-full-after-low-successor-0001",
			RequestedBy:  "test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunBrowserCDPPermission(ctx, continued.Run.ID)
	if err != nil || permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		permission.Revision != 2 {
		t.Fatalf("Full Access successor did not use default-on Full CDP: %+v err=%v",
			permission, err)
	}
}

func TestThreadPermissionLowToFullWithoutActiveRunResetsOldDisabledCDPPreference(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-full-cdp-old-generation-0001")
	browserService := application.NewRunBrowserCDPPermissionService(state,
		domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled: true, FullDebugEnabled: true,
		})
	if _, err := browserService.Change(ctx,
		application.ChangeRunBrowserCDPPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunBrowserCDPPermissionRestricted),
			OperationKey: "thread-full-cdp-old-generation-off-0001",
			RequestedBy:  "test_operator", Reason: "disable Full CDP in the old generation",
		}); err != nil {
		t.Fatal(err)
	}
	runService := application.NewRunService(state)
	if _, err := runService.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runService.Fail(ctx, run.ID,
		"finish predecessor with disabled Full CDP"); err != nil {
		t.Fatal(err)
	}
	low := selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionAsk, "thread-cdp-new-low-generation-0001")
	if low.CurrentRunEffect != domain.ThreadExecutionPermissionNoActiveRun {
		t.Fatalf("terminal predecessor remained active during low selection: %+v", low)
	}
	high := selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-cdp-new-full-generation-0001")
	if high.CurrentRunEffect != domain.ThreadExecutionPermissionNoActiveRun {
		t.Fatalf("terminal predecessor remained active during Full selection: %+v", high)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue in the newly confirmed Full generation",
			OperationKey: "thread-cdp-new-full-successor-0001",
			RequestedBy:  "test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunBrowserCDPPermission(ctx, continued.Run.ID)
	if err != nil || permission.Mode != domain.RunBrowserCDPPermissionFullDebug ||
		permission.Revision != 2 {
		t.Fatalf("new low-to-Full generation inherited stale disabled CDP: %+v err=%v",
			permission, err)
	}
}

func TestDirectRunExecutionDowngradeAtomicallyRestrictsFullCDP(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "direct-run-cdp-full-0001")
	before, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || before.Mode != domain.RunBrowserCDPPermissionFullDebug {
		t.Fatalf("test Full CDP setup failed: %+v err=%v", before, err)
	}
	result, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
		}).Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionAsk),
		OperationKey: "direct-run-cdp-downgrade-0001", RequestedBy: "test_operator",
		Reason: "leave Full Access through the direct Run selector",
	})
	if err != nil || result.Permission.Mode != domain.RunExecutionPermissionAsk {
		t.Fatalf("direct Run downgrade failed: %+v err=%v", result, err)
	}
	after, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || after.Mode != domain.RunBrowserCDPPermissionRestricted ||
		after.Revision <= before.Revision {
		t.Fatalf("direct Run downgrade left Full CDP enabled: before=%+v after=%+v err=%v",
			before, after, err)
	}
}

func TestDirectRunFullReconfirmationPreservesIndependentFullCDPChoice(t *testing.T) {
	ctx, state, run, _, _ := threadFullCDPTestFixture(t)
	defer state.Close()
	permissions := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
		})
	full, err := permissions.Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFull),
		OperationKey: "direct-run-cdp-default-on-0001", RequestedBy: "test_operator",
		Reason:      "enter Full Access through the direct Run selector",
		ConfirmFull: true,
	})
	if err != nil || full.Permission.Mode != domain.RunExecutionPermissionFull {
		t.Fatalf("direct Run Full Access failed: %+v err=%v", full, err)
	}
	browserPermission, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || browserPermission.Mode != domain.RunBrowserCDPPermissionFullDebug {
		t.Fatalf("direct low-to-high transition did not default Full CDP on: %+v err=%v",
			browserPermission, err)
	}
	if _, err := transitionRunBrowserCDPForStoreTest(t, ctx, state, run.ID,
		domain.RunBrowserCDPPermissionRestricted,
		"direct-run-disable-before-high-to-high"); err != nil {
		t.Fatal(err)
	}
	disabled, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	reconfirmed, err := permissions.Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFull),
		OperationKey: "direct-run-cdp-debug-preserve-off-0001", RequestedBy: "test_operator",
		Reason:      "re-confirm Full without changing the CDP sub-switch",
		ConfirmFull: true,
	})
	if err != nil || reconfirmed.Permission.Mode != domain.RunExecutionPermissionFull || reconfirmed.Permission.ID == full.Permission.ID || reconfirmed.Permission.Revision != full.Permission.Revision+1 {
		t.Fatalf("direct Run Full re-confirmation failed: %+v err=%v", reconfirmed, err)
	}
	afterReconfirmation, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || afterReconfirmation.ID != disabled.ID ||
		afterReconfirmation.Revision != disabled.Revision ||
		afterReconfirmation.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("Full re-confirmation changed the explicit Full CDP switch: before=%+v after=%+v err=%v",
			disabled, afterReconfirmation, err)
	}
	fullAgain, err := permissions.Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionFull),
			OperationKey: "direct-run-cdp-full-preserve-off-0002",
			RequestedBy:  "test_operator",
			Reason:       "return to Full Access without changing the CDP sub-switch",
			ConfirmFull:  true,
		})
	if err != nil || fullAgain.Permission.Mode != domain.RunExecutionPermissionFull || fullAgain.Permission.ID == reconfirmed.Permission.ID || fullAgain.Permission.Revision != reconfirmed.Permission.Revision+1 {
		t.Fatalf("direct Run Full transition failed: %+v err=%v", fullAgain, err)
	}
	afterFull, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || afterFull.ID != disabled.ID ||
		afterFull.Revision != disabled.Revision ||
		afterFull.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("Repeated Full re-confirmation changed the explicit Full CDP switch: before=%+v after=%+v err=%v",
			disabled, afterFull, err)
	}
}

func TestFullCDPUpgradeRejectsStaleExpectedExecutionSnapshot(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "stale-execution-cdp-full-0001")
	if _, err := transitionRunBrowserCDPForStoreTest(t, ctx, state, run.ID,
		domain.RunBrowserCDPPermissionRestricted,
		"stale-execution-disable-before-upgrade"); err != nil {
		t.Fatal(err)
	}
	expectedExecution, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if at.Before(current.CreatedAt) {
		at = current.CreatedAt
	}
	next, err := current.Next(idgen.New("run-browser-cdp-permission"),
		domain.RunBrowserCDPPermissionFullDebug, true, "test_operator",
		"attempt Full CDP with a stale execution snapshot", at)
	if err != nil {
		t.Fatal(err)
	}
	operation := domain.RunBrowserCDPPermissionOperation{
		KeyDigest: runmutation.Fingerprint(
			"thread-full-cdp-store-test-operation", run.ID,
			"stale-execution-upgrade"),
		RequestFingerprint: runBrowserCDPPermissionRequestFingerprint(next),
		SnapshotID:         next.ID, RunID: next.RunID, RequestedBy: next.RequestedBy,
		CreatedAt: next.CreatedAt,
	}
	event, err := newThreadManagedRunBrowserCDPPermissionSelectedEvent(current, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
		}).Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionAsk),
		OperationKey: "stale-execution-cdp-downgrade-0001", RequestedBy: "test_operator",
		Reason: "interleave an execution downgrade before Full CDP commits",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.TransitionRunBrowserCDPPermission(ctx, next, operation,
		event, expectedExecution); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale execution snapshot enabled Full CDP: %v", err)
	}
	after, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || after.Mode != domain.RunBrowserCDPPermissionRestricted ||
		after.ID != current.ID {
		t.Fatalf("stale Full CDP upgrade changed durable state: before=%+v after=%+v err=%v",
			current, after, err)
	}
}

func TestThreadPermissionFullCDPFailureRollsBackWholeTransition(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	if _, err := state.db.ExecContext(ctx, `CREATE TRIGGER test_reject_thread_full_cdp
		BEFORE INSERT ON run_browser_cdp_permission_snapshots
		WHEN NEW.revision > 1 BEGIN
			SELECT RAISE(ABORT, 'injected Full CDP persistence failure');
		END;`); err != nil {
		t.Fatal(err)
	}
	request := application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFull),
		OperationKey: "thread-full-cdp-atomic-failure-0001",
		RequestedBy:  "test_operator", Reason: "inject atomic failure",
		ConfirmFull: true,
	}
	if _, err := service.Change(ctx, request); err == nil {
		t.Fatal("Thread Full Access transition succeeded despite injected CDP failure")
	}
	preference, err := state.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if err != nil || preference.Mode != domain.RunExecutionPermissionAsk ||
		preference.Revision != 1 {
		t.Fatalf("failed transition partially changed Thread permission: %+v err=%v",
			preference, err)
	}
	runPermission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || runPermission.Mode != domain.RunExecutionPermissionAsk ||
		runPermission.Revision != 1 {
		t.Fatalf("failed transition partially changed Run permission: %+v err=%v",
			runPermission, err)
	}
	browserPermission, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || browserPermission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		browserPermission.Revision != 1 {
		t.Fatalf("failed transition partially changed Full CDP: %+v err=%v",
			browserPermission, err)
	}
	var threadOperationCount, runPermissionCount, browserPermissionCount int
	queries := []struct {
		query string
		value *int
	}{
		{`SELECT COUNT(*) FROM thread_execution_permission_operations
			WHERE thread_id = ?`, &threadOperationCount},
		{`SELECT COUNT(*) FROM run_execution_permission_snapshots
			WHERE run_id = ?`, &runPermissionCount},
		{`SELECT COUNT(*) FROM run_browser_cdp_permission_snapshots
			WHERE run_id = ?`, &browserPermissionCount},
	}
	for index, item := range queries {
		identity := run.ID
		if index == 0 {
			identity = threadRecord.ID
		}
		if err := state.db.QueryRowContext(ctx, item.query, identity).Scan(item.value); err != nil {
			t.Fatal(err)
		}
	}
	if threadOperationCount != 0 || runPermissionCount != 1 || browserPermissionCount != 1 {
		t.Fatalf("failed transition left half-state: thread_ops=%d run_permissions=%d browser_permissions=%d",
			threadOperationCount, runPermissionCount, browserPermissionCount)
	}
}

func TestRunBrowserCDPDowngradeDoesNotPauseRunningRun(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-full-cdp-running-toggle-0001")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	selected, err := transitionRunBrowserCDPForStoreTest(t, ctx, state, run.ID,
		domain.RunBrowserCDPPermissionRestricted, "safe-running-downgrade")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("running Full CDP downgrade was not stored: %+v", selected)
	}
	storedRun, err := state.GetRun(ctx, run.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("Full CDP downgrade changed Run lifecycle: run=%+v err=%v", storedRun, err)
	}
}

func TestRunBrowserCDPDowngradeBypassesActiveLeaseAndSurfaceWithoutPausing(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-full-cdp-running-lease-0001")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	_ = acquireTestRunExecutionLease(t, ctx, state, run.ID)
	now := time.Now().UTC()
	if err := state.CreateTerminalSession(ctx, TerminalSessionRecord{
		ID:              "terminal-full-cdp-immediate-downgrade",
		ProtocolVersion: "user_terminal_session.v1", RunID: run.ID,
		WorkspaceID: "workspace-full-cdp-immediate-downgrade", State: "running",
		Cwd: ".", Columns: 120, Rows: 30, CreatedAt: now, LastActivityAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	selected, err := transitionRunBrowserCDPForStoreTest(t, ctx, state, run.ID,
		domain.RunBrowserCDPPermissionRestricted, "leased-running-downgrade")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("downgrade did not persist through live surfaces: %+v", selected)
	}
	storedRun, getErr := state.GetRun(ctx, run.ID)
	if getErr != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("downgrade paused live Run: run=%+v err=%v", storedRun, getErr)
	}
	permission, getErr := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if getErr != nil || permission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		permission.Revision != 3 {
		t.Fatalf("downgrade did not become the current ceiling: %+v err=%v", permission, getErr)
	}
}

func TestThreadPermissionDowngradePersistsOnRunningRunAndReleasesLease(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-debug-running-downgrade-0001")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	now := time.Now().UTC()
	if err := state.CreateTerminalSession(ctx, TerminalSessionRecord{
		ID:              "terminal-thread-permission-immediate-downgrade",
		ProtocolVersion: "user_terminal_session.v1", RunID: run.ID,
		WorkspaceID: "workspace-thread-permission-immediate-downgrade", State: "running",
		Cwd: ".", Columns: 120, Rows: 30, CreatedAt: now, LastActivityAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	selected := selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionAsk, "thread-debug-running-downgrade-0002")
	if selected.Permission.Mode != domain.RunExecutionPermissionAsk {
		t.Fatalf("Thread downgrade=%+v", selected)
	}
	storedRun, err := state.GetRun(ctx, run.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("downgrade changed Run lifecycle: run=%+v err=%v", storedRun, err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || permission.Mode != domain.RunExecutionPermissionAsk {
		t.Fatalf("Run permission downgrade=%+v err=%v", permission, err)
	}
	released, found, err := state.GetRunExecutionLease(ctx, run.ID)
	if err != nil || !found || released.LeaseID != lease.LeaseID ||
		released.Status != domain.RunExecutionLeaseReleased {
		t.Fatalf("downgrade lease=%+v found=%t err=%v", released, found, err)
	}
	browserPermission, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || browserPermission.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("downgrade left Full CDP enabled: %+v err=%v", browserPermission, err)
	}
}

func TestRetainedThreadDebugToFullPersistsOnRunningRunAndReleasesLease(t *testing.T) {
	ctx, state, run, threadRecord, service := legacyDebugThreadFullCDPTestFixture(t)
	defer state.Close()
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	selected := selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-debug-to-full-running-0002")
	if selected.Permission.Mode != domain.RunExecutionPermissionFull {
		t.Fatalf("Thread Debug-to-Full=%+v", selected)
	}
	storedRun, err := state.GetRun(ctx, run.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("Debug-to-Full changed Run lifecycle: run=%+v err=%v", storedRun, err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || permission.Mode != domain.RunExecutionPermissionFull {
		t.Fatalf("Run Debug-to-Full permission=%+v err=%v", permission, err)
	}
	released, found, err := state.GetRunExecutionLease(ctx, run.ID)
	if err != nil || !found || released.LeaseID != lease.LeaseID ||
		released.Status != domain.RunExecutionLeaseReleased {
		t.Fatalf("Debug-to-Full lease=%+v found=%t err=%v", released, found, err)
	}
}

func TestRetainedRunDebugDowngradePersistsOnRunningRunAndReleasesLease(t *testing.T) {
	ctx, state, run, _, _ := legacyDebugThreadFullCDPTestFixture(t)
	defer state.Close()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
	}
	permissions := application.NewRunExecutionPermissionService(state, capabilities)
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	selected, err := permissions.Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionAsk),
		OperationKey: "direct-debug-running-downgrade-0002",
		RequestedBy:  "test_operator", Reason: "immediately revoke Debug for this Run",
	})
	if err != nil || selected.Permission.Mode != domain.RunExecutionPermissionAsk {
		t.Fatalf("direct Debug downgrade=%+v err=%v", selected, err)
	}
	storedRun, err := state.GetRun(ctx, run.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("direct downgrade changed Run lifecycle: run=%+v err=%v", storedRun, err)
	}
	released, found, err := state.GetRunExecutionLease(ctx, run.ID)
	if err != nil || !found || released.LeaseID != lease.LeaseID ||
		released.Status != domain.RunExecutionLeaseReleased {
		t.Fatalf("direct downgrade lease=%+v found=%t err=%v", released, found, err)
	}
	browserPermission, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || browserPermission.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("direct downgrade left Full CDP enabled: %+v err=%v", browserPermission, err)
	}
}

func TestRunBrowserCDPToggleRejectsRunningUpgradeUntilQuiescent(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFull, "thread-full-cdp-running-upgrade-0001")
	if _, err := transitionRunBrowserCDPForStoreTest(t, ctx, state, run.ID,
		domain.RunBrowserCDPPermissionRestricted, "disable-before-running-upgrade"); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	_, err := transitionRunBrowserCDPForStoreTest(t, ctx, state, run.ID,
		domain.RunBrowserCDPPermissionFullDebug, "reject-running-upgrade")
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("running upgrade error code=%s err=%v", apperror.CodeOf(err), err)
	}
	storedRun, getErr := state.GetRun(ctx, run.ID)
	if getErr != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("rejected upgrade changed Run state: run=%+v err=%v", storedRun, getErr)
	}
	permission, getErr := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if getErr != nil || permission.Mode != domain.RunBrowserCDPPermissionRestricted ||
		permission.Revision != 3 {
		t.Fatalf("rejected upgrade changed Full CDP: %+v err=%v", permission, getErr)
	}
}
