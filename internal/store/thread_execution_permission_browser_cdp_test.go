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
			DangerFullAccessEnabled: true, DebugMaximumAccessEnabled: true,
		})
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
	switch mode {
	case domain.RunExecutionPermissionWorkspaceAccess:
		request.ConfirmWorkspaceAccess = true
	case domain.RunExecutionPermissionApproval:
		request.ConfirmUserApproval = true
	case domain.RunExecutionPermissionFullAccess:
		request.ConfirmDangerFullAccess = true
	case domain.RunExecutionPermissionDebug:
		request.ConfirmDebugAccess = true
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
		domain.RunExecutionPermissionFullAccess, "thread-full-cdp-default-on-0001")
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
		domain.RunExecutionPermissionApproval, "thread-full-cdp-forced-off-0001")
	restricted, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restricted.Mode != domain.RunBrowserCDPPermissionRestricted ||
		restricted.Revision != 3 {
		t.Fatalf("execution downgrade did not force Full CDP off: %+v", restricted)
	}
	preference, err := state.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if err != nil || preference.Mode != domain.RunExecutionPermissionApproval {
		t.Fatalf("Thread downgrade missing: permission=%+v err=%v", preference, err)
	}
	runPermission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || runPermission.Mode != domain.RunExecutionPermissionApproval {
		t.Fatalf("Run downgrade missing: permission=%+v err=%v", runPermission, err)
	}
}

func TestThreadPermissionPreservesDisabledFullCDPAcrossDebugAndSuccessor(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()

	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFullAccess, "thread-full-cdp-before-disable-0001")
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
		domain.RunExecutionPermissionDebug, "thread-debug-preserves-cdp-off-0001")
	preserved, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || preserved.ID != disabled.ID ||
		preserved.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("Full Access to Debug overwrote the CDP sub-switch: before=%+v after=%+v err=%v",
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
	if err != nil || successorPermission.Mode != domain.RunExecutionPermissionDebug {
		t.Fatalf("successor did not inherit Debug ceiling: %+v err=%v",
			successorPermission, err)
	}
}

func TestThreadPermissionSuccessorInheritsEnabledFullCDPWithoutAuthority(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFullAccess, "thread-full-cdp-on-successor-0001")
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
		domain.RunExecutionPermissionFullAccess, "thread-full-after-low-predecessor-0001")
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
		domain.RunExecutionPermissionFullAccess, "thread-full-cdp-old-generation-0001")
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
		domain.RunExecutionPermissionApproval, "thread-cdp-new-low-generation-0001")
	if low.CurrentRunEffect != domain.ThreadExecutionPermissionNoActiveRun {
		t.Fatalf("terminal predecessor remained active during low selection: %+v", low)
	}
	high := selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFullAccess, "thread-cdp-new-full-generation-0001")
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
		domain.RunExecutionPermissionFullAccess, "direct-run-cdp-full-0001")
	before, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || before.Mode != domain.RunBrowserCDPPermissionFullDebug {
		t.Fatalf("test Full CDP setup failed: %+v err=%v", before, err)
	}
	result, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		}).Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionApproval),
		OperationKey: "direct-run-cdp-downgrade-0001", RequestedBy: "test_operator",
		Reason: "leave Full Access through the direct Run selector", ConfirmUserApproval: true,
	})
	if err != nil || result.Permission.Mode != domain.RunExecutionPermissionApproval {
		t.Fatalf("direct Run downgrade failed: %+v err=%v", result, err)
	}
	after, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || after.Mode != domain.RunBrowserCDPPermissionRestricted ||
		after.Revision <= before.Revision {
		t.Fatalf("direct Run downgrade left Full CDP enabled: before=%+v after=%+v err=%v",
			before, after, err)
	}
}

func TestDirectRunExecutionHighRiskTransitionsDefaultAndPreserveFullCDP(t *testing.T) {
	ctx, state, run, _, _ := threadFullCDPTestFixture(t)
	defer state.Close()
	permissions := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			DebugMaximumAccessEnabled: true,
		})
	full, err := permissions.Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "direct-run-cdp-default-on-0001", RequestedBy: "test_operator",
		Reason:                  "enter Full Access through the direct Run selector",
		ConfirmDangerFullAccess: true,
	})
	if err != nil || full.Permission.Mode != domain.RunExecutionPermissionFullAccess {
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
	debug, err := permissions.Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "direct-run-cdp-debug-preserve-off-0001", RequestedBy: "test_operator",
		Reason:             "move from Full Access to Debug without changing the CDP sub-switch",
		ConfirmDebugAccess: true,
	})
	if err != nil || debug.Permission.Mode != domain.RunExecutionPermissionDebug {
		t.Fatalf("direct Run Debug transition failed: %+v err=%v", debug, err)
	}
	afterDebug, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || afterDebug.ID != disabled.ID ||
		afterDebug.Revision != disabled.Revision ||
		afterDebug.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("Full-to-Debug changed the explicit Full CDP switch: before=%+v after=%+v err=%v",
			disabled, afterDebug, err)
	}
	fullAgain, err := permissions.Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
			OperationKey:            "direct-run-cdp-full-preserve-off-0002",
			RequestedBy:             "test_operator",
			Reason:                  "return to Full Access without changing the CDP sub-switch",
			ConfirmDangerFullAccess: true,
		})
	if err != nil || fullAgain.Permission.Mode != domain.RunExecutionPermissionFullAccess {
		t.Fatalf("direct Run Full transition failed: %+v err=%v", fullAgain, err)
	}
	afterFull, err := state.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil || afterFull.ID != disabled.ID ||
		afterFull.Revision != disabled.Revision ||
		afterFull.Mode != domain.RunBrowserCDPPermissionRestricted {
		t.Fatalf("Debug-to-Full changed the explicit Full CDP switch: before=%+v after=%+v err=%v",
			disabled, afterFull, err)
	}
}

func TestFullCDPUpgradeRejectsStaleExpectedExecutionSnapshot(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFullAccess, "stale-execution-cdp-full-0001")
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
		}).Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionApproval),
		OperationKey: "stale-execution-cdp-downgrade-0001", RequestedBy: "test_operator",
		Reason:              "interleave an execution downgrade before Full CDP commits",
		ConfirmUserApproval: true,
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
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "thread-full-cdp-atomic-failure-0001",
		RequestedBy:  "test_operator", Reason: "inject atomic failure",
		ConfirmDangerFullAccess: true,
	}
	if _, err := service.Change(ctx, request); err == nil {
		t.Fatal("Thread Full Access transition succeeded despite injected CDP failure")
	}
	preference, err := state.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if err != nil || preference.Mode != domain.RunExecutionPermissionConservative ||
		preference.Revision != 1 {
		t.Fatalf("failed transition partially changed Thread permission: %+v err=%v",
			preference, err)
	}
	runPermission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || runPermission.Mode != domain.RunExecutionPermissionConservative ||
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
		domain.RunExecutionPermissionFullAccess, "thread-full-cdp-running-toggle-0001")
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
		domain.RunExecutionPermissionFullAccess, "thread-full-cdp-running-lease-0001")
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
		domain.RunExecutionPermissionDebug, "thread-debug-running-downgrade-0001")
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
		domain.RunExecutionPermissionApproval, "thread-debug-running-downgrade-0002")
	if selected.Permission.Mode != domain.RunExecutionPermissionApproval {
		t.Fatalf("Thread downgrade=%+v", selected)
	}
	storedRun, err := state.GetRun(ctx, run.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("downgrade changed Run lifecycle: run=%+v err=%v", storedRun, err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || permission.Mode != domain.RunExecutionPermissionApproval {
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

func TestThreadDebugToFullPersistsOnRunningRunAndReleasesLease(t *testing.T) {
	ctx, state, run, threadRecord, service := threadFullCDPTestFixture(t)
	defer state.Close()
	selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionDebug, "thread-debug-to-full-running-0001")
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	selected := selectThreadPermissionForFullCDPTest(t, ctx, service, threadRecord.ID,
		domain.RunExecutionPermissionFullAccess, "thread-debug-to-full-running-0002")
	if selected.Permission.Mode != domain.RunExecutionPermissionFullAccess {
		t.Fatalf("Thread Debug-to-Full=%+v", selected)
	}
	storedRun, err := state.GetRun(ctx, run.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("Debug-to-Full changed Run lifecycle: run=%+v err=%v", storedRun, err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || permission.Mode != domain.RunExecutionPermissionFullAccess {
		t.Fatalf("Run Debug-to-Full permission=%+v err=%v", permission, err)
	}
	released, found, err := state.GetRunExecutionLease(ctx, run.ID)
	if err != nil || !found || released.LeaseID != lease.LeaseID ||
		released.Status != domain.RunExecutionLeaseReleased {
		t.Fatalf("Debug-to-Full lease=%+v found=%t err=%v", released, found, err)
	}
}

func TestDirectRunDebugDowngradePersistsOnRunningRunAndReleasesLease(t *testing.T) {
	ctx, state, run, _, _ := threadFullCDPTestFixture(t)
	defer state.Close()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
	}
	permissions := application.NewRunExecutionPermissionService(state, capabilities)
	if _, err := permissions.Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "direct-debug-running-downgrade-0001",
		RequestedBy:  "test_operator", Reason: "select Debug before direct downgrade",
		ConfirmDebugAccess: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	selected, err := permissions.Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: run.ID, Mode: string(domain.RunExecutionPermissionConservative),
		OperationKey: "direct-debug-running-downgrade-0002",
		RequestedBy:  "test_operator", Reason: "immediately revoke Debug for this Run",
	})
	if err != nil || selected.Permission.Mode != domain.RunExecutionPermissionConservative {
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
		domain.RunExecutionPermissionFullAccess, "thread-full-cdp-running-upgrade-0001")
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
