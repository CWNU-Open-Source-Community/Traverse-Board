package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

func removeSchemaV131ForTestStatements() []string {
	return append([]string{
		`DELETE FROM schema_migrations WHERE version = 131`,
	}, removeSchemaV130ForTestStatements()...)
}

func TestSchemaV131PreservesV130StreamToolIdentities(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "command-runtime-preserves-item-stream.db")
	legacy := openSchemaV130Store(t, path)
	_, runRecord := createStructuredToolTestRun(t, ctx, legacy,
		"preserve item-stream identity through command runtime migration")
	if _, err := application.NewRunService(legacy).Start(ctx, runRecord.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := legacy.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, legacy, runRecord.ID),
		"persist a v130 streamed tool call")
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"title":"Migration","content":"preserve stream identity"}`)
	safePayload, err := toolgateway.NormalizeStructuredMemoryPayload(
		toolgateway.NoteCreateTool, payload)
	if err != nil {
		t.Fatal(err)
	}
	operationKey := runmutation.SupervisorToolOperationKey(runRecord.ID,
		turn.Checkpoint.NextTurn, string(toolgateway.NoteCreateTool), string(safePayload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1,
		Provider: "test", Model: "model"}
	if inserted, err := legacy.RecordSupervisorModelStarted(ctx, turn.Checkpoint,
		attempt); err != nil || !inserted {
		t.Fatalf("record v130 model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	if _, err := legacy.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID,
				Name: string(toolgateway.NoteCreateTool), Arguments: payload}}}); err != nil {
		t.Fatal(err)
	}
	var before [3]string
	if err := legacy.db.QueryRowContext(ctx, `SELECT stream_response_id,
		stream_item_id, stream_call_id FROM run_supervisor_tool_calls
		WHERE run_id = ? AND call_id = ?`, runRecord.ID, callID).
		Scan(&before[0], &before[1], &before[2]); err != nil {
		t.Fatal(err)
	}
	if before[0] == "" || before[1] == "" || before[2] == "" {
		t.Fatalf("v130 stream identity is incomplete: %#v", before)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var after [3]string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT stream_response_id,
		stream_item_id, stream_call_id FROM run_supervisor_tool_calls
		WHERE run_id = ? AND call_id = ?`, runRecord.ID, callID).
		Scan(&after[0], &after[1], &after[2]); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("v131 changed v130 stream identity: before=%#v after=%#v", before, after)
	}
	if _, err := upgraded.db.ExecContext(ctx, `UPDATE run_supervisor_tool_calls
		SET stream_call_id = 'changed' WHERE run_id = ? AND call_id = ?`,
		runRecord.ID, callID); err == nil {
		t.Fatal("v131 did not restore the stream identity immutability trigger")
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func TestSchemaV131ProjectsV130JobsAsReadOnlyLegacyUnbound(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "command-runtime-v130.db")
	legacy := openSchemaV130Store(t, path)
	job := commandRuntimeMigrationJob(t, legacy,
		domain.RunExecutionPermissionFullAccess,
		commandruntimeadapter.HostUnsandboxed(strings.Repeat("a", 64)))
	insertV130CommandRuntimeJob(t, legacy, job)
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	loaded, err := upgraded.GetCommandRuntimeJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Adapter != commandruntimeadapter.LegacyUnbound() ||
		loaded.Adapter.Executable() {
		t.Fatalf("v130 Job adapter=%#v, want non-executable legacy projection",
			loaded.Adapter)
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(upgraded,
		"command-runtime-v131-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	if reconciled, err := manager.ReconcileStartup(ctx); err != nil || reconciled != 0 {
		t.Fatalf("legacy Job reconciliation count=%d err=%v", reconciled, err)
	}
	after, err := upgraded.GetCommandRuntimeJob(ctx, job.ID)
	if err != nil || after.Version != loaded.Version || after.State != loaded.State {
		t.Fatalf("legacy Job was adopted or rewritten: before=%#v after=%#v err=%v",
			loaded, after, err)
	}
	if active, err := upgraded.CommandRuntimeJobOwnershipActive(ctx, after); err != nil || active {
		t.Fatalf("legacy Job restored executable ownership: active=%t err=%v", active, err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}

func TestSchemaV131PersistsSandboxJobsWithoutHostProcessIdentity(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "command-runtime-sandbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	adapter := commandruntimeadapter.SandboxedWorkspace(
		application.CommandRuntimeLocalSandboxBackend,
		"windows_appcontainer.windows_appcontainer_policy.v1",
		strings.Repeat("b", 64))
	prepared := commandRuntimeMigrationJob(t, state,
		domain.RunExecutionPermissionWorkspaceAccess, adapter)
	prepared, replayed, err := state.PrepareCommandRuntimeJob(ctx, prepared)
	if err != nil || replayed {
		t.Fatalf("prepare sandbox Job replayed=%t err=%v", replayed, err)
	}
	startedAt := prepared.CreatedAt.Add(time.Millisecond)
	running := prepared
	running.State = runner.CommandRuntimeJobRunning
	running.JobAssignedAtCreation = true
	running.StartedAt = &startedAt
	running.Version++
	running.UpdatedAt = startedAt
	running, err = state.UpdateCommandRuntimeJob(ctx, running, prepared.Version)
	if err != nil || running.PID != 0 || running.ProcessGroup != 0 {
		t.Fatalf("sandbox running Job=%#v err=%v", running, err)
	}
	drifted := running
	drifted.Adapter.Generation = strings.Repeat("c", 64)
	drifted.Version++
	drifted.UpdatedAt = startedAt.Add(time.Millisecond)
	if _, err := state.UpdateCommandRuntimeJob(ctx, drifted, running.Version); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("adapter generation drift error=%v", err)
	}
}

func openSchemaV130Store(t testing.TB, path string) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		t.Fatal(err)
	}
	state := &SQLiteStore{db: db, home: filepath.Dir(path)}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrationPlan()[:130] {
		if err := state.applyMigration(context.Background(), item); err != nil {
			_ = state.Close()
			t.Fatalf("apply schema v130 fixture migration %d: %v", item.Version, err)
		}
	}
	return state
}

func commandRuntimeMigrationJob(t testing.TB, state *SQLiteStore,
	permissionMode domain.RunExecutionPermissionMode,
	adapter commandruntimeadapter.Identity,
) runner.CommandRuntimeJob {
	t.Helper()
	ctx := context.Background()
	rootPath := t.TempDir()
	workspace := WorkspaceRecord{ID: "workspace-command-runtime-v131",
		Name: "command-runtime-v131", RootPath: rootPath}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	mission, runRecord, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "migrate a fenced Command Runtime Job", Profile: "code",
		WorkspaceID: workspace.ID,
		Budget:      domain.Budget{MaxTurns: 4, MaxTokens: 1000, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	profileName := "local"
	if adapter.Backend == application.CommandRuntimeDockerSandboxBackend {
		profileName = "docker"
	}
	profile, err := application.NewRunExecutionProfileService(state).Change(ctx,
		application.ChangeRunExecutionProfileRequest{RunID: runRecord.ID,
			Profile: profileName, OperationKey: "command-runtime-v131-profile",
			RequestedBy: "test_operator", Reason: "bind adapter profile"})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		DangerFullAccessEnabled: true}
	permissionRequest := application.ChangeRunExecutionPermissionRequest{
		RunID: runRecord.ID, Mode: string(permissionMode),
		OperationKey: "command-runtime-v131-permission", RequestedBy: "test_operator",
		Reason: "bind adapter permission"}
	if permissionMode == domain.RunExecutionPermissionWorkspaceAccess {
		permissionRequest.ConfirmWorkspaceAccess = true
	} else {
		permissionRequest.ConfirmDangerFullAccess = true
	}
	permission, err := application.NewRunExecutionPermissionService(state,
		capabilities).Change(ctx, permissionRequest)
	if err != nil {
		t.Fatal(err)
	}
	started, err := runs.Start(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, started.ID)
	mode, err := state.GetRunMode(ctx, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := state.GetRootAgent(ctx, started.ID)
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
		Output: runner.CommandRuntimeOutputPolicy{
			InlineBytes:   runner.MinCommandRuntimeInlineBytes,
			ArtifactBytes: runner.MinCommandRuntimeInlineBytes},
		Network:     runner.CommandRuntimeNetworkDisabled,
		Credentials: runner.CommandRuntimeCredentialsNone,
		Purpose:     "test v131 migration"}, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return runner.CommandRuntimeJob{ID: "command-job-v131",
		OperationDigest:    testCommandRuntimeDigest("v131-operation"),
		RequestFingerprint: testCommandRuntimeDigest("v131-request"),
		InvocationID:       "invocation-v131", RunID: started.ID, MissionID: mission.ID,
		SessionID: started.SessionID, WorkspaceID: workspace.ID, RootAgentID: root.ID,
		WorkspaceRootSHA256: resolved.WorkspaceRootSHA256,
		ModeSnapshotID:      mode.ID, ModeRevision: mode.Revision,
		ProfileSnapshotID: profile.Profile.ID, ProfileRevision: profile.Profile.Revision,
		PermissionSnapshotID: permission.Permission.ID,
		PermissionRevision:   permission.Permission.Revision,
		PermissionMode:       permission.Permission.Mode,
		LeaseID:              lease.LeaseID, LeaseGeneration: lease.Generation,
		LeaseOwnerID: lease.OwnerID, Adapter: adapter,
		OwnerID: "command-runtime-v131-owner", OwnerGeneration: 1,
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
		StdinClosed: true, Version: 1, CreatedAt: now, UpdatedAt: now}
}

func insertV130CommandRuntimeJob(t testing.TB, state *SQLiteStore,
	job runner.CommandRuntimeJob,
) {
	t.Helper()
	_, err := state.db.ExecContext(context.Background(), `INSERT INTO command_runtime_jobs (
		id, protocol_version, operation_digest, request_fingerprint, invocation_id,
		run_id, mission_id, session_id, workspace_id, root_agent_id,
		workspace_root_sha256, mode_snapshot_id, mode_revision, profile_snapshot_id,
		profile_revision, permission_snapshot_id, permission_revision, permission_mode,
		lease_id, lease_generation, lease_owner_id, owner_id, owner_generation,
		owner_renewed_at, owner_expires_at, intent_json, spec_fingerprint, profile,
		executable_path, executable_sha256, environment_sha256, working_directory,
		stdin_policy, network, credentials, timeout_milliseconds, inline_limit_bytes,
		artifact_limit_bytes, state, pid, process_group, stdout, stderr,
		stdout_observed_bytes, stderr_observed_bytes, output_cursor, output_base_cursor,
		output_frames_json, stdout_sha256, stderr_sha256, truncation_reason, exit_code,
		timed_out, cancelled, killed, tree_reaped, job_assigned_at_creation,
		stdin_closed, stdin_write_count, version, created_at, started_at, completed_at,
		updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, runner.CommandRuntimeProtocolVersion, job.OperationDigest,
		job.RequestFingerprint, job.InvocationID, job.RunID, job.MissionID,
		job.SessionID, job.WorkspaceID, job.RootAgentID, job.WorkspaceRootSHA256,
		job.ModeSnapshotID, job.ModeRevision, job.ProfileSnapshotID,
		job.ProfileRevision, job.PermissionSnapshotID, job.PermissionRevision,
		job.PermissionMode, job.LeaseID, job.LeaseGeneration, job.LeaseOwnerID,
		job.OwnerID, job.OwnerGeneration, ts(job.OwnerRenewedAt), ts(job.OwnerExpiresAt),
		job.IntentJSON, job.SpecFingerprint, job.Profile, job.ExecutablePath,
		job.ExecutableSHA256, job.EnvironmentSHA256, job.WorkingDirectory,
		job.StdinPolicy, job.Network, job.Credentials, job.TimeoutMilliseconds,
		job.InlineLimitBytes, job.ArtifactLimitBytes, job.State, job.PID,
		job.ProcessGroup, job.Stdout, job.Stderr, job.StdoutObservedBytes,
		job.StderrObservedBytes, job.OutputCursor, job.OutputBaseCursor,
		job.OutputFramesJSON, job.StdoutSHA256, job.StderrSHA256,
		job.TruncationReason, nullableInt(job.ExitCode), boolInt(job.TimedOut),
		boolInt(job.Cancelled), boolInt(job.Killed), boolInt(job.TreeReaped),
		boolInt(job.JobAssignedAtCreation), boolInt(job.StdinClosed),
		job.StdinWriteCount, job.Version, ts(job.CreatedAt), nullableTS(job.StartedAt),
		nullableTS(job.CompletedAt), ts(job.UpdatedAt))
	if err != nil {
		t.Fatal(err)
	}
}
