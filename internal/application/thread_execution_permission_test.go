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

func TestThreadExecutionPermissionRequiresExactCurrentConfirmation(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull,
	} {
		t.Run(string(mode), func(t *testing.T) {
			base := ChangeThreadExecutionPermissionRequest{ThreadID: "thread-current-permission",
				Mode: string(mode), OperationKey: "thread-current-confirmation-0001",
				RequestedBy: "operator", Reason: "select the current approval preference",
				ConfirmFull: mode == domain.RunExecutionPermissionFull}
			normalized, actual, confirmed, err := normalizeChangeThreadExecutionPermissionRequest(base)
			if err != nil || actual != mode || confirmed != base.ConfirmFull || normalized.Mode != string(mode) {
				t.Fatalf("normalized=%+v mode=%s confirmed=%t err=%v", normalized, actual, confirmed, err)
			}
			invalid := base
			invalid.ConfirmFull = !base.ConfirmFull
			if _, _, _, err := normalizeChangeThreadExecutionPermissionRequest(invalid); err == nil {
				t.Fatal("mode accepted an inexact Full confirmation")
			}
			for _, legacyFlag := range []func(*ChangeThreadExecutionPermissionRequest){
				func(r *ChangeThreadExecutionPermissionRequest) { r.ConfirmWorkspaceAccess = true },
				func(r *ChangeThreadExecutionPermissionRequest) { r.ConfirmUserApproval = true },
				func(r *ChangeThreadExecutionPermissionRequest) { r.ConfirmDangerFullAccess = true },
				func(r *ChangeThreadExecutionPermissionRequest) { r.ConfirmDebugAccess = true },
			} {
				invalid = base
				legacyFlag(&invalid)
				if _, _, _, err := normalizeChangeThreadExecutionPermissionRequest(invalid); err == nil {
					t.Fatal("current mode accepted a retired confirmation flag")
				}
			}
		})
	}
}

func TestThreadExecutionPermissionRejectsNonOperatorAuthoritySources(t *testing.T) {
	for _, requester := range []string{"model", "agent", "skill", "repository",
		"project_config", "recovery_data", "mcp", "plugin", "hook"} {
		request := ChangeThreadExecutionPermissionRequest{
			ThreadID:     "thread-authority-source",
			Mode:         string(domain.RunExecutionPermissionAuto),
			OperationKey: "thread-permission-source-" + requester + "-0001",
			RequestedBy:  requester, Reason: "attempt unauthorized selection"}
		if _, _, _, err := normalizeChangeThreadExecutionPermissionRequest(request); err == nil ||
			!strings.Contains(err.Error(), "cannot select execution permission modes") {
			t.Fatalf("requester %q authority rejection=%v", requester, err)
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
	autoThread, err := initialThread.Next("thread-inspect-permission-2",
		domain.RunExecutionPermissionAuto, false, "operator",
		"select Auto for the Thread", at.Add(time.Second))
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
		threadPermission: autoThread, runPermission: initialRun}
	service := NewThreadExecutionPermissionService(store,
		domain.ExecutionPermissionRuntimeCapabilities{})

	drifted, err := service.Inspect(t.Context(), threadRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.CurrentRunID != threadRecord.ActiveRunID ||
		drifted.CurrentRunMode != domain.RunExecutionPermissionAsk ||
		drifted.CurrentRunSynchronized {
		t.Fatalf("current Run drift was hidden: %+v", drifted)
	}

	store.runPermission, err = initialRun.Next("run-inspect-permission-2",
		domain.RunExecutionPermissionAuto, false, "operator",
		"apply Thread Auto preference", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	synchronized, err := service.Inspect(t.Context(), threadRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if synchronized.CurrentRunMode != domain.RunExecutionPermissionAuto ||
		!synchronized.CurrentRunSynchronized {
		t.Fatalf("matching current Run was reported as pending: %+v", synchronized)
	}
}
