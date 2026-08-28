package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

type threadPermissionInspectStore struct {
	thread           domain.Thread
	threadPermission domain.ThreadExecutionPermissionSnapshot
	runPermission    domain.RunExecutionPermissionSnapshot
}

func (s *threadPermissionInspectStore) GetThread(_ context.Context,
	_ string,
) (domain.Thread, error) {
	return s.thread, nil
}

func (s *threadPermissionInspectStore) GetThreadExecutionPermission(_ context.Context,
	_ string,
) (domain.ThreadExecutionPermissionSnapshot, error) {
	return s.threadPermission, nil
}

func (s *threadPermissionInspectStore) GetRunExecutionPermission(_ context.Context,
	_ string,
) (domain.RunExecutionPermissionSnapshot, error) {
	return s.runPermission, nil
}

func (s *threadPermissionInspectStore) GetThreadExecutionPermissionSnapshot(context.Context,
	string,
) (domain.ThreadExecutionPermissionSnapshot, error) {
	return domain.ThreadExecutionPermissionSnapshot{}, errors.New("unused")
}

func (s *threadPermissionInspectStore) GetThreadExecutionPermissionOperation(context.Context,
	string,
) (domain.ThreadExecutionPermissionOperation, bool, error) {
	return domain.ThreadExecutionPermissionOperation{}, false, errors.New("unused")
}

func (s *threadPermissionInspectStore) TransitionThreadExecutionPermission(context.Context,
	domain.ThreadExecutionPermissionSnapshot, domain.ThreadExecutionPermissionOperation,
) (domain.ThreadExecutionPermissionSnapshot, domain.ThreadExecutionPermissionOperation,
	bool, error,
) {
	return domain.ThreadExecutionPermissionSnapshot{},
		domain.ThreadExecutionPermissionOperation{}, false, errors.New("unused")
}

func TestThreadWorkspaceAccessPermissionRequiresExactOperatorConfirmation(t *testing.T) {
	base := ChangeThreadExecutionPermissionRequest{ThreadID: "thread-workspace-access",
		Mode:         string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "thread-workspace-access-confirmation-0001",
		RequestedBy:  "operator", Reason: "select the bounded Workspace permission"}
	if _, _, _, err := normalizeChangeThreadExecutionPermissionRequest(base); err == nil ||
		!strings.Contains(err.Error(), "exact sandbox-boundary confirmation") {
		t.Fatalf("missing Workspace confirmation error=%v", err)
	}
	base.ConfirmWorkspaceAccess = true
	normalized, mode, confirmed, err := normalizeChangeThreadExecutionPermissionRequest(base)
	if err != nil || mode != domain.RunExecutionPermissionWorkspaceAccess ||
		!confirmed || normalized.Mode != string(mode) {
		t.Fatalf("normalized=%+v mode=%s confirmed=%t err=%v",
			normalized, mode, confirmed, err)
	}
	base.ConfirmUserApproval = true
	if _, _, _, err := normalizeChangeThreadExecutionPermissionRequest(base); err == nil {
		t.Fatal("Workspace confirmation accepted an unrelated approval flag")
	}
}

func TestThreadExecutionPermissionRejectsNonOperatorAuthoritySources(t *testing.T) {
	for _, requester := range []string{"model", "agent", "skill", "repository",
		"project_config", "recovery_data", "mcp", "plugin", "hook"} {
		request := ChangeThreadExecutionPermissionRequest{
			ThreadID:     "thread-authority-source",
			Mode:         string(domain.RunExecutionPermissionWorkspaceAccess),
			OperationKey: "thread-permission-source-" + requester + "-0001",
			RequestedBy:  requester, Reason: "attempt unauthorized selection",
			ConfirmWorkspaceAccess: true}
		if _, _, _, err := normalizeChangeThreadExecutionPermissionRequest(request); err == nil {
			t.Fatalf("requester %q selected a Thread permission mode", requester)
		}
	}
}

func TestInspectThreadExecutionPermissionReportsCurrentRunDriftAndSynchronization(t *testing.T) {
	at := time.Date(2026, 8, 28, 2, 3, 4, 0, time.UTC)
	threadRecord := domain.Thread{ID: "thread-inspect-permission", MissionID: "mission-inspect",
		ProtocolVersion: domain.ThreadProtocolVersion, Title: "inspect permission",
		Status: domain.ThreadActive, ActiveRunID: "run-inspect-permission",
		LastRunID: "run-inspect-permission", Version: 1, CreatedAt: at, UpdatedAt: at}
	initialThread, err := domain.NewInitialThreadExecutionPermissionSnapshot(
		"thread-inspect-permission-1", threadRecord, "operator", at)
	if err != nil {
		t.Fatal(err)
	}
	workspaceThread, err := initialThread.Next("thread-inspect-permission-2",
		domain.RunExecutionPermissionWorkspaceAccess, true, "operator",
		"use bounded Workspace Access", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	initialRun, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"run-inspect-permission-1",
		domain.Run{ID: threadRecord.ActiveRunID, MissionID: threadRecord.MissionID},
		domain.Mission{ID: threadRecord.MissionID}, "operator", at)
	if err != nil {
		t.Fatal(err)
	}
	store := &threadPermissionInspectStore{thread: threadRecord,
		threadPermission: workspaceThread, runPermission: initialRun}
	service := NewThreadExecutionPermissionService(store,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})

	drifted, err := service.Inspect(t.Context(), threadRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.CurrentRunID != threadRecord.ActiveRunID ||
		drifted.CurrentRunMode != domain.RunExecutionPermissionConservative ||
		drifted.CurrentRunSynchronized {
		t.Fatalf("current Run drift was hidden: %+v", drifted)
	}

	store.runPermission, err = initialRun.Next("run-inspect-permission-2",
		domain.RunExecutionPermissionWorkspaceAccess, true, "operator",
		"apply Thread Workspace Access", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	synchronized, err := service.Inspect(t.Context(), threadRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if synchronized.CurrentRunMode != domain.RunExecutionPermissionWorkspaceAccess ||
		!synchronized.CurrentRunSynchronized {
		t.Fatalf("matching current Run was reported as pending: %+v", synchronized)
	}
}
