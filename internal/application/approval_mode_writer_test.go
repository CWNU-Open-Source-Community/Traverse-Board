package application

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

func TestApprovalModeWritersPersistNewValuesAndRequireLiveFullActivation(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "approval-writer.db")
	state, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, run, err := NewRunService(state).Create(ctx, CreateRunRequest{Goal: "three-mode writer", Profile: "code", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || initial.Mode != domain.RunExecutionPermissionAsk || initial.ProtocolVersion != domain.RunApprovalPermissionProtocolVersion || initial.OperatorConfirmed {
		t.Fatal("new Run did not store actual ask", initial, err)
	}
	preference, err := state.GetThreadExecutionPermission(ctx, thread.ID)
	if err != nil || preference.Mode != domain.RunExecutionPermissionAsk || preference.ProtocolVersion != domain.ThreadApprovalPermissionProtocolVersion {
		t.Fatal("new Thread did not store actual ask", preference, err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}
	service := NewRunExecutionPermissionService(state, capabilities)
	for _, mode := range []string{"conservative", "workspace_access", "approval", "full_access", "debug"} {
		if _, err := service.Change(ctx, ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: mode,
			OperationKey: "reject-legacy-writer-" + mode, RequestedBy: "operator"}); err == nil {
			t.Fatal("retired legacy writer accepted", mode)
		}
	}
	for index, mode := range []string{"auto", "ask", "full"} {
		request := ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: mode, OperationKey: fmt.Sprintf("three-mode-writer-run-%d", index),
			RequestedBy: "operator", ConfirmFull: mode == "full"}
		if mode == "full" {
			unconfirmed := request
			unconfirmed.ConfirmFull = false
			if _, err := service.Change(ctx, unconfirmed); err == nil {
				t.Fatal("Full selection did not require explicit confirmation")
			}
		}
		result, err := service.Change(ctx, request)
		if err != nil || string(result.Permission.Mode) != mode || result.Permission.ProtocolVersion != domain.RunApprovalPermissionProtocolVersion ||
			result.Permission.ProcessEnabled || result.Permission.ExecutionAuthorized || result.Permission.CapabilityGrant {
			t.Fatal("new preference did not reach the existing immutable ledger", mode, result, err)
		}
		replay, err := service.Change(ctx, request)
		if err != nil || !replay.Replayed || replay.Permission.ID != result.Permission.ID {
			t.Fatal("exact new-mode replay changed its snapshot", replay, err)
		}
		if mode == "full" && !capabilities.AllowsSnapshot(result.Permission) {
			t.Fatal("explicit current-process Full was not activated")
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cold := capabilities
	cold.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
	stored, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil || stored.Mode != domain.RunExecutionPermissionFull || cold.AllowsSnapshot(stored) {
		t.Fatal("restart restored Full authority from stored preference", stored, err)
	}
	current := NewRunExecutionPermissionService(state, cold)
	reactivated, err := current.Change(ctx, ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: "full", ConfirmFull: true,
		OperationKey: "explicit-reactivation", RequestedBy: "operator"})
	if err != nil || reactivated.Permission.ID == stored.ID || !cold.AllowsSnapshot(reactivated.Permission) {
		t.Fatal("explicit reactivation failed to rotate and activate the current snapshot", reactivated, err)
	}
	threadService := NewThreadExecutionPermissionService(state, cold)
	for index, mode := range []string{"auto", "full", "ask"} {
		result, err := threadService.Change(ctx, ChangeThreadExecutionPermissionRequest{ThreadID: thread.ID, Mode: mode,
			ConfirmFull: mode == "full", RequestedBy: "operator", OperationKey: fmt.Sprintf("thread-new-mode-%d", index)})
		if err != nil || string(result.Permission.Mode) != mode || result.Permission.ProtocolVersion != domain.ThreadApprovalPermissionProtocolVersion {
			t.Fatal("Thread writer did not store the new mode", result, err)
		}
		child, err := state.GetRunExecutionPermission(ctx, run.ID)
		if err != nil || string(child.Mode) != mode || child.ProtocolVersion != domain.RunApprovalPermissionProtocolVersion {
			t.Fatal("Thread preference did not atomically update the current Run", child, err)
		}
	}
	if _, err := current.Change(ctx, ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: "auto",
		OperationKey: "auto-before-live-revocation", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "approval-writer-live-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	fence, err := cold.RuntimeAuthority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.Change(ctx, ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: "ask",
		OperationKey: "ask-revokes-live-auto-lease", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	if cold.RuntimeAuthority.AllowsRunAuthorizationFence(run.ID, fence) {
		t.Fatal("Auto -> Ask retained the old operation fence")
	}
	live, found, err := state.GetRunExecutionLease(ctx, run.ID)
	if err != nil || !found || live.ActiveAt(time.Now().UTC()) || live.LeaseID != lease.Lease.LeaseID {
		t.Fatal("Auto -> Ask did not release the active lease", live, err)
	}
}
