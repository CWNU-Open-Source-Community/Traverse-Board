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
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFull),
		OperationKey: "thread-full-first-confirmation-0001", RequestedBy: "test_operator",
		Reason: "confirm Full Access for the current task", ConfirmFull: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || !authority.AllowsThreadFullAccess(first.Permission, &firstRun) {
		t.Fatalf("first Thread Full activation failed: run=%+v err=%v", firstRun, err)
	}
	firstGeneration, active := capabilities.FullAccessGeneration(firstRun)
	if !active {
		t.Fatal("first Thread Full activation has no Run generation")
	}
	// A cold exact replay can bind the same snapshot to the same numeric
	// generation in a fresh process; its runtime epoch must still differ.
	freshAuthority := domain.NewExecutionPermissionRuntimeAuthority()
	// A separate startup can have one prior runtime revocation before replay,
	// making its next numeric generation equal to the old process's value.
	freshAuthority.RevokeRun("run-unrelated-prior-revocation")
	freshCapabilities := capabilities
	freshCapabilities.RuntimeAuthority = freshAuthority
	freshService := application.NewThreadExecutionPermissionService(state, freshCapabilities)
	coldReplay, err := freshService.Change(ctx,
		application.ChangeThreadExecutionPermissionRequest{
			ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFull),
			OperationKey: "thread-full-first-confirmation-0001", RequestedBy: "test_operator",
			Reason: "confirm Full Access for the current task", ConfirmFull: true,
		})
	if err != nil || !coldReplay.Replayed || coldReplay.Permission.ID != first.Permission.ID {
		t.Fatalf("fresh process exact replay=%+v err=%v", coldReplay, err)
	}
	if after, allowed := freshCapabilities.FullAccessGeneration(firstRun); !allowed ||
		after != firstGeneration || freshAuthority.RuntimeEpoch() == authority.RuntimeEpoch() {
		t.Fatalf("fresh process replay did not establish a distinct epoch: first_generation=%d fresh_generation=%d first_epoch=%s fresh_epoch=%s allowed=%t",
			firstGeneration, after, authority.RuntimeEpoch(), freshAuthority.RuntimeEpoch(), allowed)
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
			ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFull),
			OperationKey: "thread-full-second-confirmation-0001", RequestedBy: "test_operator",
			Reason:      "explicitly reactivate Full Access for the current task",
			ConfirmFull: true,
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
			ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFull),
			OperationKey: "thread-full-second-confirmation-0001", RequestedBy: "test_operator",
			Reason:      "explicitly reactivate Full Access for the current task",
			ConfirmFull: true,
		})
	if err != nil || !replayed.Replayed {
		t.Fatalf("same-mode Full retry was not replayed: result=%+v err=%v", replayed, err)
	}
	if after, allowed := capabilities.FullAccessGeneration(reconfirmedRun); !allowed || after != generation {
		t.Fatalf("idempotent Full replay rotated its live generation: before=%d after=%d allowed=%t",
			generation, after, allowed)
	}
}

func TestThreadExecutionPermissionExactAutoReplayPreservesAuthorizationFence(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "thread-auto-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "verify Auto replay fencing",
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
			FullAccessRequiresRuntimeGrant: true,
			RuntimeAuthority:               authority,
		})
	request := application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionAuto),
		OperationKey: "thread-auto-replay-operation-0001", RequestedBy: "test_operator",
		Reason: "select Auto for the current task",
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
		t.Fatalf("exact Thread Auto replay=%+v err=%v", replayed, err)
	}
	if !authority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("exact Thread Auto replay rotated its current Run fence")
	}
}

func TestThreadExecutionPermissionFreshSameAutoPreservesAuthorizationFence(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "thread-auto-fresh-reaffirmation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "verify fresh Auto reaffirmation fencing",
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
			FullAccessRequiresRuntimeGrant: true,
			RuntimeAuthority:               authority,
		})
	first, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionAuto),
		OperationKey: "thread-auto-fresh-first-0001", RequestedBy: "test_operator",
		Reason: "select Auto for this task",
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
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionAuto),
		OperationKey: "thread-auto-fresh-second-0001", RequestedBy: "test_operator",
		Reason: "reaffirm Auto for this task",
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || reaffirmed.Replayed ||
		reaffirmed.Permission.Revision <= first.Permission.Revision ||
		reaffirmed.CurrentRunEffect != domain.ThreadExecutionPermissionApplied ||
		after.ID != before.ID || after.Revision != before.Revision {
		t.Fatalf("fresh Auto reaffirmation changed the Run snapshot: result=%+v before=%+v after=%+v err=%v",
			reaffirmed, before, after, err)
	}
	if !authority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("fresh same-mode Auto operation revoked the unchanged Run fence")
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
		application.CreateRunRequest{Goal: "preserve current authority while deferring Full",
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
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	service := application.NewThreadExecutionPermissionService(state, capabilities)
	auto, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionAuto),
		OperationKey: "thread-deferred-auto-operation-0001", RequestedBy: "test_operator",
		Reason: "establish the current Auto preference",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	autoRun, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || autoRun.Mode != domain.RunExecutionPermissionAuto ||
		authority.AllowsThreadFullAccess(auto.Permission, &autoRun) {
		t.Fatalf("Auto preference unexpectedly granted Full authority: permission=%+v err=%v", autoRun, err)
	}
	fence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}

	full, err := service.Change(ctx, application.ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFull),
		OperationKey: "thread-deferred-full-operation-0001", RequestedBy: "test_operator",
		Reason: "use Full on the next Run", ConfirmFull: true,
	})
	if err != nil || full.CurrentRunEffect != domain.ThreadExecutionPermissionDeferred {
		t.Fatalf("Full preference was not deferred: result=%+v err=%v", full, err)
	}
	if !authority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("deferred preference rotated the current Run authorization fence")
	}
	current, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || current.ID != autoRun.ID || current.Mode != domain.RunExecutionPermissionAuto {
		t.Fatalf("deferred preference changed current Run permission: %+v err=%v", current, err)
	}
	if authority.AllowsThreadFullAccess(full.Permission, &current) {
		t.Fatal("deferred Full preference activated authority for the current Auto Run")
	}
}
