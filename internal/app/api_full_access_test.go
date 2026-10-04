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
	caps := newAPIExecutionPermissionCapabilities(true, true)
	service := application.NewThreadExecutionPermissionService(st, caps)
	request := application.ChangeThreadExecutionPermissionRequest{ThreadID: domain.InitialThreadID(run.ID), Mode: "full", OperationKey: "api-explicit-full-activation", RequestedBy: "test_operator", ConfirmFull: true, Reason: "Explicitly enable Full for this task"}
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
	fresh := newAPIExecutionPermissionCapabilities(true, true)
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
	downgrade.Mode = "ask"
	downgrade.OperationKey = "api-revoke-full-activation"
	downgrade.ConfirmFull = false
	if _, err := cold.Change(t.Context(), downgrade); err != nil {
		t.Fatal(err)
	}
	_, _ = cold.Change(t.Context(), request)
	if fresh.AllowsSnapshot(permission) {
		t.Fatal("stale POST revived revoked authority")
	}
	cli := cliExecutionPermissionCapabilities(true, true)
	if cli.RuntimeAuthority == nil || cli.AllowsSnapshot(permission) {
		t.Fatal("a new CLI process inherited persisted Full authority")
	}
	for _, args := range [][2]bool{{false, false}, {true, false}, {true, true}} {
		c := newAPIExecutionPermissionCapabilities(args[0], args[1])
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		if c.DangerFullAccessEnabled != args[1] {
			t.Fatal("API startup gate broadened")
		}
	}
}
