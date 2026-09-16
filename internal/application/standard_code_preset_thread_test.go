package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
)

func standardCodeThreadTestRuntime() CapabilityReadinessRuntime {
	return CapabilityReadinessRuntime{RunControlEnabled: true, RunExecutionEnabled: true,
		ExecutionPermissionControlEnabled: true, StandardCodePresetEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true},
		LocalSandboxInstalled: true, LocalSandboxProven: true, LocalBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{commandruntimeadapter.SandboxedWorkspace(
			CommandRuntimeLocalSandboxBackend, "preset-thread-test-adapter", "preset-thread-test-generation")}}
}

func TestStandardCodePresetKeepsConfiguredThreadRunForNextSubmission(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunCreated, domain.RunPaused, domain.RunRunning} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newDrydockApplicationFixture(t, "preset thread "+string(status))
			runs := NewRunService(fixture.state)
			if status != domain.RunCreated {
				if _, err := runs.Start(t.Context(), fixture.run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if status == domain.RunPaused {
				if _, err := runs.Pause(t.Context(), fixture.run.ID); err != nil {
					t.Fatal(err)
				}
			}
			runtime := standardCodeThreadTestRuntime()
			preset, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
			if err != nil {
				t.Fatal(err)
			}
			action := "configure"
			if status == domain.RunRunning {
				action = "pause_and_configure"
			}
			request := ConfigureStandardCodeRequest{Version: domain.StandardCodePresetProtocolVersion,
				RunID: fixture.run.ID, Action: action, BackendIntent: "local",
				OperationKey: "preset-thread-configure-0001", RequestedBy: "operator"}
			preview, err := preset.Configure(t.Context(), request)
			if err != nil || !preview.TrustRequired {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			request.ConfirmWorkspaceTrust, request.ExpectedTrustDigest = true, preview.TrustDigest
			configured, err := preset.Configure(t.Context(), request)
			if err != nil || configured.Status != StandardCodeResultConfigured {
				t.Fatalf("configured=%+v err=%v", configured, err)
			}
			thread, err := fixture.state.GetThreadByRun(t.Context(), fixture.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			preference, err := fixture.state.GetThreadExecutionPermission(t.Context(), thread.ID)
			if err != nil || preference.Mode != domain.RunExecutionPermissionWorkspaceAccess || preference.Revision != 2 ||
				preference.ProcessEnabled || preference.ExecutionAuthorized || preference.CapabilityGrant {
				t.Fatalf("Thread preference=%+v err=%v", preference, err)
			}
			turns := NewThreadTurnServiceWithExecutionCapabilities(fixture.state,
				NewRunLifecycleControlService(fixture.state), nil, runtime.ExecutionPermissionCapabilities)
			submissionRequest := SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion,
				ThreadID: thread.ID, Content: "Inspect this configured task", OperationKey: "preset-thread-message-0001", RequestedBy: "http_thread_operator"}
			// Exercise the actual /turns configuration, enqueue, and start/resume
			// path on SQLite. No model or sandbox execution is claimed here.
			if err := turns.advanceForPendingConfiguration(t.Context(), submissionRequest); err != nil {
				t.Fatal(err)
			}
			submission, err := turns.threads.Submit(t.Context(), submissionRequest)
			if err != nil || submission.SuccessorCreated || submission.Run.ID != fixture.run.ID {
				t.Fatalf("submission=%+v err=%v", submission, err)
			}
			ready, err := turns.prepareSubmissionLifecycle(t.Context(), submissionRequest, ExecuteThreadTurnResult{Submission: submission})
			if err != nil || ready.Submission.Run.ID != fixture.run.ID || ready.Submission.Run.Status != domain.RunRunning {
				t.Fatalf("ready=%+v err=%v", ready, err)
			}
			bindings, err := fixture.state.ListThreadRuns(t.Context(), thread.ID)
			if err != nil || len(bindings) != 1 {
				t.Fatalf("bindings=%v err=%v", bindings, err)
			}
			changed, err := NewThreadExecutionPermissionService(fixture.state, runtime.ExecutionPermissionCapabilities).
				Change(t.Context(), ChangeThreadExecutionPermissionRequest{ThreadID: thread.ID,
					Mode: "conservative", OperationKey: "preset-thread-later-setting-0001",
					RequestedBy: "operator", Reason: "explicit later preference"})
			if err != nil {
				t.Fatal(err)
			}
			beforeRunPermission, err := fixture.state.GetRunExecutionPermission(t.Context(), fixture.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := preset.Configure(t.Context(), request)
			if err != nil || !replay.Replayed {
				t.Fatalf("old preset replay=%+v err=%v", replay, err)
			}
			operation, found, err := fixture.state.GetConfiguredStandardCodePresetOperation(t.Context(), fixture.run.ID)
			if err != nil || !found {
				t.Fatalf("configured operation found=%t err=%v", found, err)
			}
			if _, _, replayed, err := fixture.state.CommitStandardCodePreset(t.Context(), domain.StandardCodePresetCommit{
				Operation: operation, DrydockID: operation.DrydockID, DrydockGeneration: operation.DrydockGeneration,
				DrydockCheckpointID: operation.DrydockCheckpointID, CommittedAt: time.Now().UTC(),
			}); err != nil || !replayed {
				t.Fatalf("old store commit replayed=%t err=%v", replayed, err)
			}
			afterPreference, err := fixture.state.GetThreadExecutionPermission(t.Context(), thread.ID)
			if err != nil || !reflect.DeepEqual(afterPreference, changed.Permission) {
				t.Fatalf("old key rewrote later Thread preference: err=%v", err)
			}
			afterRunPermission, err := fixture.state.GetRunExecutionPermission(t.Context(), fixture.run.ID)
			if err != nil || !reflect.DeepEqual(afterRunPermission, beforeRunPermission) {
				t.Fatalf("old key rewrote later Run permission: err=%v", err)
			}
			if changed.CurrentRunEffect == domain.ThreadExecutionPermissionDeferred {
				active, _ := fixture.state.GetRun(t.Context(), fixture.run.ID)
				pending, err := turns.pendingEpochConfiguration(t.Context(), fixture.state, thread, active)
				if err != nil || !pending {
					t.Fatalf("explicit later preference was ignored: pending=%t err=%v", pending, err)
				}
			}
		})
	}
}

type preparedThreadPermissionStore struct {
	*store.SQLiteStore
	prepare func(domain.ThreadExecutionPermissionSnapshot, domain.ThreadExecutionPermissionOperation) error
}

func (s *preparedThreadPermissionStore) TransitionThreadExecutionPermission(ctx context.Context,
	snapshot domain.ThreadExecutionPermissionSnapshot, operation domain.ThreadExecutionPermissionOperation,
) (domain.ThreadExecutionPermissionSnapshot, domain.ThreadExecutionPermissionOperation, bool, error) {
	if err := s.prepare(snapshot, operation); err != nil {
		return domain.ThreadExecutionPermissionSnapshot{}, domain.ThreadExecutionPermissionOperation{}, false, err
	}
	return s.SQLiteStore.TransitionThreadExecutionPermission(ctx, snapshot, operation)
}

func TestStandardCodePresetRejectsPermissionPreparedEarlierButCommittedAfterIntent(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunCreated, domain.RunRunning} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newDrydockApplicationFixture(t, "preset permission commit order")
			actionName := "configure"
			pendingStatus := domain.StandardCodePresetPreparing
			if status == domain.RunRunning {
				if _, err := NewRunService(fixture.state).Start(t.Context(), fixture.run.ID); err != nil {
					t.Fatal(err)
				}
				actionName = "pause_and_configure"
				pendingStatus = domain.StandardCodePresetWaitingForPause
			}
			second, err := store.Open(fixture.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			thread, err := fixture.state.GetThreadByRun(t.Context(), fixture.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			runtime := standardCodeThreadTestRuntime()
			service, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
			if err != nil {
				t.Fatal(err)
			}
			request := ConfigureStandardCodeRequest{Version: domain.StandardCodePresetProtocolVersion,
				RunID: fixture.run.ID, Action: actionName, BackendIntent: "local",
				OperationKey: "preset-thread-race-operation-0001", RequestedBy: "operator"}
			preview, err := service.Configure(t.Context(), request)
			if err != nil || !preview.TrustRequired {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			request.ConfirmWorkspaceTrust, request.ExpectedTrustDigest = true, preview.TrustDigest
			normalized, intent, action, err := normalizeStandardCodePresetRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			var operation domain.StandardCodePresetOperation
			permissionStore := &preparedThreadPermissionStore{SQLiteStore: second}
			permissionStore.prepare = func(snapshot domain.ThreadExecutionPermissionSnapshot, _ domain.ThreadExecutionPermissionOperation) error {
				// Make the timestamp ordering explicit without depending on host clock
				// resolution; the permission still commits after this intent transaction.
				now := snapshot.CreatedAt.Add(time.Millisecond)
				operation = domain.StandardCodePresetOperation{ProtocolVersion: domain.StandardCodePresetProtocolVersion,
					KeyDigest:          runmutation.Fingerprint("standard_code_preset_operation.v1", request.OperationKey),
					RequestFingerprint: standardCodePresetRequestFingerprint(normalized, intent, action),
					RequestedRunID:     fixture.run.ID, RunID: fixture.run.ID, MissionID: fixture.run.MissionID,
					WorkspaceID: fixture.workspace.ID, Action: action, BackendIntent: intent,
					SelectedBackend: domain.StandardCodeSelectedLocal, SelectionReason: domain.StandardCodeReasonExplicitLocal,
					Status: pendingStatus, RequestedBy: request.RequestedBy, CreatedAt: now, UpdatedAt: now}
				var err error
				operation, _, err = fixture.state.BeginStandardCodePreset(t.Context(), operation)
				if err == nil && !snapshot.CreatedAt.Before(operation.CreatedAt) {
					t.Fatal("fixture did not prepare the permission before preset intent")
				}
				return err
			}
			changed, err := NewThreadExecutionPermissionService(permissionStore, runtime.ExecutionPermissionCapabilities).
				Change(t.Context(), ChangeThreadExecutionPermissionRequest{ThreadID: thread.ID,
					Mode: "approval", ConfirmUserApproval: true, OperationKey: "permission-prepared-before-preset-0001",
					RequestedBy: "operator", Reason: "later commit must remain authoritative"})
			if err != nil {
				t.Fatal(err)
			}
			beforeMode, _ := second.GetRunMode(t.Context(), fixture.run.ID)
			beforePermission, _ := second.GetRunExecutionPermission(t.Context(), fixture.run.ID)
			beforeRun, _ := second.GetRun(t.Context(), fixture.run.ID)
			if _, err := service.Configure(t.Context(), request); apperror.CodeOf(err) != apperror.CodeConflict ||
				!errors.Is(err, domain.ErrStandardCodePresetThreadPreferenceChanged) {
				t.Fatalf("late permission was overwritten: %v", err)
			}
			afterPreference, _ := second.GetThreadExecutionPermission(t.Context(), thread.ID)
			afterMode, _ := second.GetRunMode(t.Context(), fixture.run.ID)
			afterPermission, _ := second.GetRunExecutionPermission(t.Context(), fixture.run.ID)
			afterRun, _ := second.GetRun(t.Context(), fixture.run.ID)
			if !reflect.DeepEqual(afterPreference, changed.Permission) || !reflect.DeepEqual(beforeMode, afterMode) || !reflect.DeepEqual(beforePermission, afterPermission) || beforeRun.Status != afterRun.Status {
				t.Fatal("conflicting preset partially changed Thread or Run settings")
			}
			stored, _, err := second.GetStandardCodePresetOperation(t.Context(), operation.KeyDigest)
			if err != nil || stored.Status != pendingStatus {
				t.Fatalf("preset status=%s err=%v", stored.Status, err)
			}
			// A newly explicit attempt binds the current preference and may apply.
			request.OperationKey = "preset-thread-race-new-operation-0002"
			configured, err := service.Configure(t.Context(), request)
			if err != nil || configured.Status != StandardCodeResultConfigured {
				t.Fatalf("new attempt=%+v err=%v", configured, err)
			}
		})
	}
}
