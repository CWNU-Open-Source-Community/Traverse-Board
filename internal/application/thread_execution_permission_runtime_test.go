package application_test

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

func TestThreadFullAccessColdStartRequiresExplicitSameModeReconfirmation(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "thread-full-reactivation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "verify current Thread Full reactivation",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		DangerFullAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
		RuntimeAuthority: authority,
	}
	service := application.NewThreadExecutionPermissionService(state, capabilities)
	first, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "thread-full-first-confirmation-0001", RequestedBy: "test_operator",
		Reason: "confirm Full Access for the current task", ConfirmDangerFullAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || !authority.AllowsThreadFullAccess(first.Permission, &firstRun) {
		t.Fatalf("first Thread Full activation failed: run=%+v err=%v", firstRun, err)
	}

	authority.RevokeThread(threadRecord.ID)
	inspected, err := service.Inspect(ctx, threadRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if authority.AllowsThreadFullAccess(inspected.Permission,
		&inspected.CurrentRunPermission) {
		t.Fatal("reading a historical Full preference recreated runtime authority")
	}

	reconfirmed, err := service.Change(ctx,
		application.ChangeThreadExecutionPermissionRequest{
			ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "thread-full-second-confirmation-0001", RequestedBy: "test_operator",
			Reason:                  "explicitly reactivate Full Access for the current task",
			ConfirmDangerFullAccess: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	reconfirmedRun, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || reconfirmed.Permission.Revision <= first.Permission.Revision ||
		reconfirmedRun.ID == firstRun.ID || reconfirmedRun.Revision <= firstRun.Revision ||
		!authority.AllowsThreadFullAccess(reconfirmed.Permission, &reconfirmedRun) ||
		!capabilities.AllowsSnapshot(reconfirmedRun) {
		t.Fatalf("same-mode Full reconfirmation did not bind the current snapshots: result=%+v run=%+v err=%v",
			reconfirmed, reconfirmedRun, err)
	}
	generation, active := capabilities.FullAccessGeneration(reconfirmedRun)
	if !active || generation == 0 {
		t.Fatal("same-mode Full reconfirmation did not expose its live generation")
	}
	replayed, err := service.Change(ctx,
		application.ChangeThreadExecutionPermissionRequest{
			ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "thread-full-second-confirmation-0001", RequestedBy: "test_operator",
			Reason:                  "explicitly reactivate Full Access for the current task",
			ConfirmDangerFullAccess: true,
		})
	if err != nil || !replayed.Replayed {
		t.Fatalf("same-mode Full retry was not replayed: result=%+v err=%v", replayed, err)
	}
	if after, allowed := capabilities.FullAccessGeneration(reconfirmedRun); !allowed || after != generation {
		t.Fatalf("idempotent Full replay rotated its live generation: before=%d after=%d allowed=%t",
			generation, after, allowed)
	}
}

func TestThreadExecutionPermissionExactDebugReplayPreservesAuthorizationFence(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "thread-debug-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "verify Debug replay fencing",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			DebugMaximumAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
			RuntimeAuthority: authority,
		})
	request := application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "thread-debug-replay-operation-0001", RequestedBy: "test_operator",
		Reason: "select Debug for the current task", ConfirmDebugAccess: true,
	}
	first, err := service.Change(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Change(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Permission.ID != first.Permission.ID {
		t.Fatalf("exact Thread Debug replay=%+v err=%v", replayed, err)
	}
	if !authority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("exact Thread Debug replay rotated its current Run fence")
	}
}

func TestThreadExecutionPermissionFreshSameDebugPreservesAuthorizationFence(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "thread-debug-fresh-reaffirmation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "verify fresh Debug reaffirmation fencing",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	service := application.NewThreadExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			DebugMaximumAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
			RuntimeAuthority: authority,
		})
	first, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "thread-debug-fresh-first-0001", RequestedBy: "test_operator",
		Reason: "select Debug for this task", ConfirmDebugAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	reaffirmed, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "thread-debug-fresh-second-0001", RequestedBy: "test_operator",
		Reason: "reaffirm Debug for this task", ConfirmDebugAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || reaffirmed.Replayed ||
		reaffirmed.Permission.Revision <= first.Permission.Revision ||
		reaffirmed.CurrentRunEffect != domain.ThreadExecutionPermissionApplied ||
		after.ID != before.ID || after.Revision != before.Revision {
		t.Fatalf("fresh Debug reaffirmation changed the Run snapshot: result=%+v before=%+v after=%+v err=%v",
			reaffirmed, before, after, err)
	}
	if !authority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("fresh same-mode Debug operation revoked the unchanged Run fence")
	}
}

func TestDeferredThreadEscalationPreservesCurrentRunAuthorizationFence(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "thread-deferred-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "preserve current authority while deferring Debug",
			Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
		RuntimeAuthority: authority,
	}
	service := application.NewThreadExecutionPermissionService(state, capabilities)
	full, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "thread-deferred-full-operation-0001", RequestedBy: "test_operator",
		Reason: "establish current Full authority", ConfirmDangerFullAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	fullRun, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || !authority.AllowsThreadFullAccess(full.Permission, &fullRun) {
		t.Fatalf("Full authority was not active: permission=%+v err=%v", fullRun, err)
	}
	fence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}

	debug, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "thread-deferred-debug-operation-0001", RequestedBy: "test_operator",
		Reason: "use Debug on the next Run", ConfirmDebugAccess: true,
	})
	if err != nil || debug.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("Debug preference was not deferred: result=%+v err=%v", debug, err)
	}
	if !authority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("deferred preference rotated the current Run authorization fence")
	}
	current, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || current.ID != fullRun.ID ||
		current.Mode != domain.RunExecutionPermissionFullAccess {
		t.Fatalf("deferred preference changed current Run permission: %+v err=%v", current, err)
	}
}
