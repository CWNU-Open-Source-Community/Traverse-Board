package app

import (
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
	"path/filepath"
	"testing"
)

func TestAPIFullAccessNeedsExplicitCurrentActivationAndPreservesCLI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "api-full.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "API current Full authority", Profile: "code", Interactive: true, Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	caps := newAPIExecutionPermissionCapabilities(true, true, false)
	service := application.NewThreadExecutionPermissionService(st, caps)
	request := application.ChangeThreadExecutionPermissionRequest{ThreadID: domain.InitialThreadID(run.ID), Mode: "full_access", OperationKey: "api-explicit-full-activation", RequestedBy: "test_operator", ConfirmDangerFullAccess: true, Reason: "Explicitly enable Full for this task"}
	if _, err := service.Change(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	permission, err := st.GetRunExecutionPermission(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	generation, live := caps.FullAccessGeneration(permission)
	if !live || generation == 0 {
		t.Fatal("API Full did not receive a bounded runtime activation")
	}
	if _, err := service.Change(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if after, ok := caps.FullAccessGeneration(permission); !ok || after != generation {
		t.Fatal("hot idempotent POST rotated authority")
	}
	fresh := newAPIExecutionPermissionCapabilities(true, true, false)
	cold := application.NewThreadExecutionPermissionService(st, fresh)
	if _, err := cold.Inspect(t.Context(), request.ThreadID); err != nil {
		t.Fatal(err)
	}
	if fresh.AllowsSnapshot(permission) {
		t.Fatal("read-only cold inspection reactivated old Full")
	}
	if fresh.RuntimeAuthority.RuntimeEpoch() == caps.RuntimeAuthority.RuntimeEpoch() {
		t.Fatal("restart reused runtime epoch")
	}
	if _, err := cold.Change(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if !fresh.AllowsSnapshot(permission) {
		t.Fatal("explicit cold exact POST could not reactivate current Full")
	}
	downgrade := request
	downgrade.Mode = "conservative"
	downgrade.OperationKey = "api-revoke-full-activation"
	downgrade.ConfirmDangerFullAccess = false
	if _, err := cold.Change(t.Context(), downgrade); err != nil {
		t.Fatal(err)
	}
	_, _ = cold.Change(t.Context(), request)
	if fresh.AllowsSnapshot(permission) {
		t.Fatal("stale POST revived revoked authority")
	}
	if cliExecutionPermissionCapabilities(true, true, false).FullAccessRequiresRuntimeGrant {
		t.Fatal("API change altered CLI startup contract")
	}
	for _, args := range [][3]bool{{false, false, false}, {true, false, false}, {true, true, true}} {
		c := newAPIExecutionPermissionCapabilities(args[0], args[1], args[2])
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		if c.FullAccessRequiresRuntimeGrant != args[1] || c.DebugMaximumAccessEnabled != args[2] {
			t.Fatal("API startup gate broadened")
		}
	}
}
