package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

func TestThreadArchiveSafelyConvergesNonterminalRun(t *testing.T) {
	tests := []struct {
		status     domain.RunStatus
		wantStatus domain.RunStatus
		wantActive bool
		versionAdd int64
	}{
		{status: domain.RunCreated, wantStatus: domain.RunCancelled, versionAdd: 2},
		{status: domain.RunPreparing, wantStatus: domain.RunCancelled, versionAdd: 2},
		{status: domain.RunRunning, wantStatus: domain.RunPaused, wantActive: true, versionAdd: 1},
		{status: domain.RunWaitingApproval, wantStatus: domain.RunWaitingApproval, wantActive: true, versionAdd: 1},
		{status: domain.RunPaused, wantStatus: domain.RunPaused, wantActive: true, versionAdd: 1},
	}
	for _, test := range tests {
		t.Run(string(test.status), func(t *testing.T) {
			ctx := context.Background()
			state, run, threadRecord := threadLifecycleFixture(t, ctx, test.status)
			defer state.Close()
			queued, err := state.EnqueueOperatorSteering(ctx,
				domain.EnqueueOperatorSteeringRequest{RunID: run.ID,
					SessionID: run.SessionID, Content: "retain this archived instruction",
					OperationKey: "archive-pending-" + string(test.status),
					RequestedBy:  "test_operator"})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC()
			archived, err := state.TransitionThreadWithOperationKey(ctx, threadRecord.ID,
				domain.ThreadArchive, threadRecord.Version, "test_operator",
				"archive-converge-"+string(test.status), at)
			if err != nil {
				t.Fatal(err)
			}
			storedRun, err := state.GetRun(ctx, run.ID)
			if err != nil || storedRun.Status != test.wantStatus {
				t.Fatalf("Run=%+v want status=%s err=%v", storedRun, test.wantStatus, err)
			}
			wantActiveRunID := ""
			if test.wantActive {
				wantActiveRunID = run.ID
			}
			if archived.Status != domain.ThreadArchived ||
				archived.ActiveRunID != wantActiveRunID ||
				archived.Version != threadRecord.Version+test.versionAdd {
				t.Fatalf("archived Thread=%+v before=%+v", archived, threadRecord)
			}
			linked, err := state.GetSession(ctx, run.SessionID)
			if err != nil || linked.Status != session.StatusArchived {
				t.Fatalf("archived Session=%+v err=%v", linked, err)
			}
			messages, err := state.ListThreadMessagesPage(ctx, threadRecord.ID, true, 0, 0)
			if err != nil || len(messages) != 1 || messages[0].Content != queued.Message.Content {
				t.Fatalf("archived Thread messages=%+v err=%v", messages, err)
			}
			wantMessageStatus := string(domain.OperatorSteeringPending)
			if test.wantStatus == domain.RunCancelled {
				wantMessageStatus = string(domain.OperatorSteeringCancelled)
			}
			if messages[0].Status != wantMessageStatus {
				t.Fatalf("archived message status=%s want=%s", messages[0].Status,
					wantMessageStatus)
			}
			replayed, err := state.TransitionThreadWithOperationKey(ctx, threadRecord.ID,
				domain.ThreadArchive, threadRecord.Version, "test_operator",
				"archive-converge-"+string(test.status), at.Add(time.Hour))
			if err != nil || replayed.Version != archived.Version ||
				!replayed.UpdatedAt.Equal(archived.UpdatedAt) {
				t.Fatalf("archive replay=%+v original=%+v err=%v", replayed, archived, err)
			}
			restored, err := state.TransitionThread(ctx, archived.ID, domain.ThreadRestore,
				archived.Version, "test_operator", at.Add(time.Millisecond))
			if err != nil || restored.Status != domain.ThreadActive {
				t.Fatalf("restore=%+v err=%v", restored, err)
			}
			storedRun, err = state.GetRun(ctx, run.ID)
			if err != nil || storedRun.Status != test.wantStatus {
				t.Fatalf("restore changed Run=%+v err=%v", storedRun, err)
			}
			linked, err = state.GetSession(ctx, run.SessionID)
			wantSessionStatus := session.StatusArchived
			if test.wantActive {
				wantSessionStatus = session.StatusActive
			}
			if err != nil || linked.Status != wantSessionStatus {
				t.Fatalf("restored Session=%+v want=%s err=%v",
					linked, wantSessionStatus, err)
			}
		})
	}
}

func TestThreadDeleteSafelyCancelsEveryNonterminalRunAndPendingInput(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunCreated, domain.RunPreparing,
		domain.RunRunning, domain.RunWaitingApproval, domain.RunPaused} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			state, run, threadRecord := threadLifecycleFixture(t, ctx, status)
			defer state.Close()
			queued, err := state.EnqueueOperatorSteering(ctx,
				domain.EnqueueOperatorSteeringRequest{RunID: run.ID,
					SessionID: run.SessionID, Content: "do not execute after deletion",
					OperationKey: "delete-pending-" + string(status),
					RequestedBy:  "test_operator"})
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := state.TransitionThread(ctx, threadRecord.ID,
				domain.ThreadDelete, threadRecord.Version, "test_operator", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			storedRun, err := state.GetRun(ctx, run.ID)
			if err != nil || storedRun.Status != domain.RunCancelled {
				t.Fatalf("deleted Run=%+v err=%v", storedRun, err)
			}
			storedMessage, err := state.GetOperatorSteering(ctx, queued.Message.ID)
			if err != nil || storedMessage.Status != domain.OperatorSteeringCancelled {
				t.Fatalf("pending input=%+v err=%v", storedMessage, err)
			}
			if deleted.Status != domain.ThreadDeleted || deleted.ActiveRunID != "" ||
				deleted.Version != threadRecord.Version+2 {
				t.Fatalf("deleted Thread=%+v before=%+v", deleted, threadRecord)
			}
			linked, err := state.GetSession(ctx, run.SessionID)
			if err != nil || linked.Status != session.StatusArchived {
				t.Fatalf("deleted Session=%+v err=%v", linked, err)
			}
		})
	}
}

func TestThreadLifecycleFailsClosedForActiveLeaseAndNonquiescentExecution(t *testing.T) {
	t.Run("effective execution lease", func(t *testing.T) {
		ctx := context.Background()
		state, run, threadRecord := threadLifecycleFixture(t, ctx, domain.RunRunning)
		defer state.Close()
		lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
		_, err := state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
			threadRecord.Version, "test_operator", time.Now().UTC())
		if apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatalf("active lease error code=%s err=%v", apperror.CodeOf(err), err)
		}
		assertThreadLifecycleUnchanged(t, ctx, state, threadRecord, run)
		expireTestRunExecutionLease(t, ctx, state, lease)
		archived, err := state.TransitionThread(ctx, threadRecord.ID,
			domain.ThreadArchive, threadRecord.Version, "test_operator", time.Now().UTC())
		if err != nil || archived.Status != domain.ThreadArchived {
			t.Fatalf("archive after lease expiry=%+v err=%v", archived, err)
		}
	})

	t.Run("active agent attempt", func(t *testing.T) {
		ctx := context.Background()
		state, run, threadRecord := threadLifecycleFixture(t, ctx, domain.RunRunning)
		defer state.Close()
		root, found, err := state.GetRootAgent(ctx, run.ID)
		if err != nil || !found {
			t.Fatalf("root found=%t err=%v", found, err)
		}
		if _, err := state.db.ExecContext(ctx, `UPDATE agent_nodes SET status = 'running',
			active_attempt_id = ?, version = version + 1, updated_at = ? WHERE id = ?`,
			"attempt-thread-lifecycle", ts(time.Now().UTC()), root.ID); err != nil {
			t.Fatal(err)
		}
		_, err = state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
			threadRecord.Version, "test_operator", time.Now().UTC())
		if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("active attempt error code=%s err=%v", apperror.CodeOf(err), err)
		}
		assertThreadLifecycleUnchanged(t, ctx, state, threadRecord, run)
	})

	t.Run("running terminal Session", func(t *testing.T) {
		ctx := context.Background()
		state, run, threadRecord := threadLifecycleFixture(t, ctx, domain.RunRunning)
		defer state.Close()
		now := time.Now().UTC()
		if err := state.CreateTerminalSession(ctx, TerminalSessionRecord{
			ID: "terminal-thread-lifecycle", ProtocolVersion: "user_terminal_session.v1",
			RunID: run.ID, WorkspaceID: "workspace-thread-lifecycle", State: "running",
			Cwd: ".", Columns: 120, Rows: 30, CreatedAt: now, LastActivityAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		_, err := state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
			threadRecord.Version, "test_operator", time.Now().UTC())
		if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("active terminal error code=%s err=%v", apperror.CodeOf(err), err)
		}
		assertThreadLifecycleUnchanged(t, ctx, state, threadRecord, run)
	})
}

func TestThreadLifecycleFailsClosedForRunningCommandRuntimeJob(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "thread-command-runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	workspaceRoot := t.TempDir()
	workspace := WorkspaceRecord{ID: "workspace-thread-command-runtime",
		Name: "thread-command-runtime", RootPath: workspaceRoot}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	mission, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "guard persistent command runtime", Profile: "code",
		WorkspaceID: workspace.ID, Surface: "code", Phase: "deliver",
		Budget: domain.Budget{MaxTurns: 3, MaxTokens: 1000, MaxToolCalls: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := application.NewRunExecutionProfileService(state).Change(ctx,
		application.ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "local",
			OperationKey: "thread-command-profile-0001", RequestedBy: "test_operator",
			Reason: "prepare a persistent command fixture"})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true,
			DangerFullAccessEnabled: true}).Change(ctx,
		application.ChangeRunExecutionPermissionRequest{RunID: run.ID,
			Mode:         string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "thread-command-permission-0001", RequestedBy: "test_operator",
			Reason: "prepare a persistent command fixture", ConfirmDangerFullAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runs.Start(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	mode, err := state.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root found=%t err=%v", found, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := runner.NormalizeCommandRuntimeSpec(runner.CommandRuntimeSpec{
		Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimeProcess,
		Executable: executable, Arguments: []string{"--help"}, WorkingDirectory: ".",
		Environment: []runner.CommandRuntimeEnvironment{},
		StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
		TimeoutMilliseconds: 1000,
		Output: runner.CommandRuntimeOutputPolicy{InlineBytes: runner.MinCommandRuntimeInlineBytes,
			ArtifactBytes: runner.MinCommandRuntimeInlineBytes},
		Network:     runner.CommandRuntimeNetworkDisabled,
		Credentials: runner.CommandRuntimeCredentialsNone, Purpose: "Thread lifecycle guard",
	}, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := runner.CommandRuntimeJob{
		ID:                 "thread-lifecycle-command-job",
		OperationDigest:    testCommandRuntimeDigest("thread-lifecycle-operation"),
		RequestFingerprint: testCommandRuntimeDigest("thread-lifecycle-request"),
		InvocationID:       "thread-lifecycle-invocation", RunID: run.ID, MissionID: mission.ID,
		SessionID: run.SessionID, WorkspaceID: workspace.ID, RootAgentID: root.ID,
		WorkspaceRootSHA256: resolved.WorkspaceRootSHA256,
		ModeSnapshotID:      mode.ID, ModeRevision: mode.Revision,
		ProfileSnapshotID: profile.Profile.ID, ProfileRevision: profile.Profile.Revision,
		PermissionSnapshotID: permission.Permission.ID,
		PermissionRevision:   permission.Permission.Revision,
		PermissionMode:       permission.Permission.Mode,
		LeaseID:              lease.LeaseID, LeaseGeneration: lease.Generation, LeaseOwnerID: lease.OwnerID,
		Adapter: commandruntimeadapter.HostUnsandboxed(strings.Repeat("e", 64)),
		OwnerID: "thread-lifecycle-command-owner", OwnerGeneration: 1,
		OwnerRenewedAt: now, OwnerExpiresAt: now.Add(time.Minute), IntentJSON: `{}`,
		SpecFingerprint: runner.CommandRuntimeSpecFingerprint(resolved),
		Profile:         resolved.Spec.Profile, ExecutablePath: resolved.ExecutablePath,
		ExecutableSHA256:  resolved.ExecutableSHA256,
		EnvironmentSHA256: resolved.EnvironmentSHA256,
		WorkingDirectory:  resolved.Spec.WorkingDirectory,
		StdinPolicy:       resolved.Spec.StdinPolicy, Network: resolved.Spec.Network,
		Credentials:         resolved.Spec.Credentials,
		TimeoutMilliseconds: resolved.Spec.TimeoutMilliseconds,
		InlineLimitBytes:    resolved.Spec.Output.InlineBytes,
		ArtifactLimitBytes:  resolved.Spec.Output.ArtifactBytes,
		State:               runner.CommandRuntimeJobPrepared, OutputFramesJSON: "[]",
		StdinClosed: true, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	prepared, _, err := state.PrepareCommandRuntimeJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	expireTestRunExecutionLease(t, ctx, state, lease)
	_, err = state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
		threadRecord.Version, "test_operator", time.Now().UTC())
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("prepared command Job error code=%s err=%v", apperror.CodeOf(err), err)
	}
	assertThreadLifecycleUnchanged(t, ctx, state, threadRecord, run)

	startedAt := now.Add(time.Millisecond)
	runningJob := prepared
	runningJob.State = runner.CommandRuntimeJobRunning
	runningJob.PID, runningJob.ProcessGroup = 4242, 4242
	runningJob.JobAssignedAtCreation = true
	runningJob.StartedAt = &startedAt
	runningJob.Version++
	runningJob.UpdatedAt = startedAt
	runningJob, err = state.UpdateCommandRuntimeJob(ctx, runningJob, prepared.Version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
		threadRecord.Version, "test_operator", time.Now().UTC())
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("running command Job error code=%s err=%v", apperror.CodeOf(err), err)
	}
	assertThreadLifecycleUnchanged(t, ctx, state, threadRecord, run)

	completedAt := startedAt.Add(time.Millisecond)
	exitCode := 0
	terminalJob := runningJob
	terminalJob.State = runner.CommandRuntimeJobCompleted
	terminalJob.ExitCode = &exitCode
	terminalJob.TreeReaped = true
	terminalJob.CompletedAt = &completedAt
	terminalJob.StdoutSHA256 = testCommandRuntimeDigest("thread-command-stdout")
	terminalJob.StderrSHA256 = testCommandRuntimeDigest("thread-command-stderr")
	terminalJob.Version++
	terminalJob.UpdatedAt = completedAt
	if _, err := state.UpdateCommandRuntimeJob(ctx, terminalJob, runningJob.Version); err != nil {
		t.Fatal(err)
	}
	archived, err := state.TransitionThread(ctx, threadRecord.ID, domain.ThreadArchive,
		threadRecord.Version, "test_operator", time.Now().UTC().Add(time.Millisecond))
	if err != nil || archived.Status != domain.ThreadArchived {
		t.Fatalf("archive after command completion=%+v err=%v", archived, err)
	}
}

func threadLifecycleFixture(t testing.TB, ctx context.Context,
	status domain.RunStatus,
) (*SQLiteStore, domain.Run, domain.Thread) {
	t.Helper()
	state, err := Open(filepath.Join(t.TempDir(), "thread-lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	mission, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "Thread lifecycle " + string(status), Profile: "review",
		Budget: domain.Budget{MaxTurns: 3},
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	switch status {
	case domain.RunCreated:
	case domain.RunPreparing:
		run = transitionThreadLifecycleFixtureRun(t, ctx, state, mission, run,
			domain.RunPreparing)
	case domain.RunRunning:
		run, err = runs.Start(ctx, run.ID)
	case domain.RunWaitingApproval:
		run, err = runs.Start(ctx, run.ID)
		if err == nil {
			run = transitionThreadLifecycleFixtureRun(t, ctx, state, mission, run,
				domain.RunWaitingApproval)
		}
	case domain.RunPaused:
		run, err = runs.Start(ctx, run.ID)
		if err == nil {
			run = transitionThreadLifecycleFixtureRun(t, ctx, state, mission, run,
				domain.RunPaused)
		}
	default:
		state.Close()
		t.Fatalf("unsupported fixture status %s", status)
	}
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, run.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state, run, threadRecord
}

func transitionThreadLifecycleFixtureRun(t testing.TB, ctx context.Context,
	state *SQLiteStore, mission domain.Mission, run domain.Run, target domain.RunStatus,
) domain.Run {
	t.Helper()
	expected := run.Status
	at := time.Now().UTC()
	if err := run.Transition(target, at); err != nil {
		t.Fatal(err)
	}
	event, err := events.New(run.ID, mission.ID, events.RunStatusChangedEvent,
		"thread_lifecycle_test", run.ID, map[string]any{
			"from": expected, "to": target,
		})
	if err != nil {
		t.Fatal(err)
	}
	event.CreatedAt = at
	if err := state.TransitionRun(ctx, run, expected, event); err != nil {
		t.Fatal(err)
	}
	return run
}

func assertThreadLifecycleUnchanged(t testing.TB, ctx context.Context,
	state *SQLiteStore, wantThread domain.Thread, wantRun domain.Run,
) {
	t.Helper()
	threadRecord, err := state.GetThread(ctx, wantThread.ID)
	if err != nil || threadRecord.Status != wantThread.Status ||
		threadRecord.Version != wantThread.Version ||
		threadRecord.ActiveRunID != wantThread.ActiveRunID {
		t.Fatalf("Thread changed after rejected lifecycle: before=%+v after=%+v err=%v",
			wantThread, threadRecord, err)
	}
	run, err := state.GetRun(ctx, wantRun.ID)
	if err != nil || run.Status != wantRun.Status {
		t.Fatalf("Run changed after rejected lifecycle: before=%+v after=%+v err=%v",
			wantRun, run, err)
	}
	linked, err := state.GetSession(ctx, wantRun.SessionID)
	if err != nil || linked.Status != session.StatusActive {
		t.Fatalf("Session changed after rejected lifecycle: Session=%+v err=%v", linked, err)
	}
	var lifecycleEvents int
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_events
		WHERE thread_id = ? AND type IN ('thread.archive', 'thread.delete')`, wantThread.ID).
		Scan(&lifecycleEvents); err != nil || lifecycleEvents != 0 {
		t.Fatalf("rejected lifecycle events=%d err=%v", lifecycleEvents, err)
	}
}
