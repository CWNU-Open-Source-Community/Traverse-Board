package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSchemaV151BackfillsProvableLegacyAgentAttribution(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v150-agent-attribution.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 150); err != nil {
		t.Fatal(err)
	}
	v150Schema := legacyFixtureSchema(t, state)
	v150Ledger, err := state.loadAppliedMigrations(ctx)
	if err != nil || len(v150Ledger) != 150 {
		t.Fatalf("v150 migration ledger: count=%d err=%v", len(v150Ledger), err)
	}
	restoreLegacyInputs := addCurrentInputColumnsForLegacySeed(t, state)

	_, run, err := newMigrationFixtureRunService(t, state).Create(ctx, application.CreateRunRequest{
		Goal: "preserve historical Supervisor actor", Profile: "code", WorkspaceID: "ws-structured",
		Budget: domain.Budget{MaxTurns: 5, MaxToolCalls: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newMigrationFixtureRunService(t, state).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, state, run.ID), "persist before v151")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := toolgateway.NormalizeStructuredMemoryPayload(
		toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"v150","content":"preserve actor"}`))
	if err != nil {
		t.Fatal(err)
	}
	operationKey := runmutation.SupervisorToolOperationKey(run.ID,
		turn.Checkpoint.NextTurn, string(toolgateway.NoteCreateTool), string(payload))
	callID, err := runmutation.SupervisorToolCallID(operationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	unknownPayload, err := toolgateway.NormalizeStructuredMemoryPayload(
		toolgateway.NoteCreateTool,
		json.RawMessage(`{"title":"v150-unresolved","content":"no known result"}`))
	if err != nil {
		t.Fatal(err)
	}
	unknownOperationKey := runmutation.SupervisorToolOperationKey(run.ID,
		turn.Checkpoint.NextTurn, string(toolgateway.NoteCreateTool), string(unknownPayload))
	unknownCallID, err := runmutation.SupervisorToolCallID(unknownOperationKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint,
		attempt); err != nil || !inserted {
		t.Fatalf("record model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint,
		attempt, llm.ChatResponse{Provider: "test", Model: "model",
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID,
				Name: string(toolgateway.NoteCreateTool), Arguments: payload},
				{ID: unknownCallID, Name: string(toolgateway.NoteCreateTool), Arguments: unknownPayload}}})
	if err != nil {
		t.Fatal(err)
	}
	const legacyResult = `{"legacy_receipt":"v150-completed"}`
	if _, replayed, err := state.RecordSupervisorToolResult(ctx, checkpoint,
		domain.SupervisorToolResult{CallID: callID, Status: domain.SupervisorToolCompleted,
			ResultJSON: legacyResult, CompletedAt: time.Now().UTC()}); err != nil || replayed {
		t.Fatalf("record legacy result replayed=%t err=%v", replayed, err)
	}
	if inserted, err := state.RecordSupervisorToolExecutionStarted(ctx, checkpoint,
		unknownCallID); err != nil || !inserted {
		t.Fatalf("record unresolved legacy start inserted=%t err=%v", inserted, err)
	}

	job := commandRuntimeMigrationJob(t, state,
		domain.RunExecutionPermissionFullAccess,
		commandruntimeadapter.HostUnsandboxed(testCommandRuntimeDigest("v151-adapter")))
	threadRecord, err := state.GetThreadByRun(ctx, job.RunID)
	if err != nil {
		t.Fatal(err)
	}

	restoreLegacyInputs()
	if !reflect.DeepEqual(v150Schema, legacyFixtureSchema(t, state)) {
		t.Fatal("legacy seeding changed the exact v150 schema")
	}
	// The v150 Job writer had neither runtime-grant columns nor an operator
	// discriminator. Insert its exact SQL columns with all historical guards on.
	insertV150CommandRuntimeJob(t, state, job, 73)
	completedJob := job
	completedJob.ID = "command-job-v150-completed"
	completedJob.OperationDigest = testCommandRuntimeDigest("v150-completed-operation")
	completedJob.RequestFingerprint = testCommandRuntimeDigest("v150-completed-request")
	completedAt, exitCode := job.CreatedAt.Add(time.Millisecond), 0
	completedJob.State, completedJob.Version = runner.CommandRuntimeJobCompleted, 3
	completedJob.PID, completedJob.ProcessGroup = 4242, 4242
	completedJob.StartedAt, completedJob.CompletedAt = &job.CreatedAt, &completedAt
	completedJob.UpdatedAt, completedJob.ExitCode = completedAt, &exitCode
	completedJob.JobAssignedAtCreation, completedJob.TreeReaped = true, true
	completedJob.Stdout, completedJob.StdoutObservedBytes, completedJob.OutputCursor = "legacy\n", 7, 7
	completedJob.StdoutSHA256 = testCommandRuntimeDigest(completedJob.Stdout)
	completedJob.StderrSHA256 = testCommandRuntimeDigest("")
	insertV150CommandRuntimeJob(t, state, completedJob, 91)
	if job.PermissionMode != domain.RunExecutionPermissionFullAccess ||
		job.PermissionRuntimeEpoch != "" || job.PermissionGeneration != 0 || job.RunAuthorizationFence != 0 {
		t.Fatal("historical Job acquired current permission/runtime bindings")
	}
	before := map[string][][]any{}
	for _, table := range []string{"command_runtime_jobs", "run_supervisor_tool_calls",
		"run_supervisor_tool_rounds", "agent_nodes", "run_mode_snapshots",
		"run_execution_profile_snapshots", "run_execution_permission_snapshots"} {
		before[table] = legacyFixtureRows(t, state, table)
	}
	if len(before["command_runtime_jobs"]) != 2 || len(before["run_supervisor_tool_calls"]) != 2 {
		t.Fatal("missing completed/unresolved historical fixture rows")
	}
	assertNoForeignKeyViolations(t, state.db)
	if err := state.applyMigration(ctx, plan[150]); err != nil {
		t.Fatal(err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 151 {
		t.Fatalf("schema version=%d want=151 err=%v", version, err)
	}
	for table, rows := range before {
		if !reflect.DeepEqual(rows, legacyFixtureRows(t, state, table)) {
			t.Fatalf("v151 changed historical %s rows/rowids", table)
		}
	}
	afterSchema := legacyFixtureSchema(t, state)
	for name, statement := range v150Schema {
		if afterSchema[name] != statement {
			t.Fatalf("v151 changed historical schema object %s", name)
		}
	}
	afterLedger, err := state.loadAppliedMigrations(ctx)
	if err != nil || len(afterLedger) != 151 {
		t.Fatalf("v151 changed its historical migration ledger: %v", err)
	}
	for version, applied := range v150Ledger {
		if !reflect.DeepEqual(applied, afterLedger[version]) {
			t.Fatalf("v151 changed historical migration %d", version)
		}
	}
	if err := validateMigrationPlan(plan, afterLedger); err != nil {
		t.Fatal(err)
	}
	rounds, err := state.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 2 {
		t.Fatalf("legacy Supervisor round=%#v err=%v", rounds, err)
	}
	for _, call := range rounds[0].Calls {
		if call.AgentID != turn.Agent.ID ||
			call.AgentAttemptID != turn.Checkpoint.AttemptID ||
			call.AgentAttribution != domain.AgentAttributionLegacyRoot {
			t.Fatalf("legacy Supervisor attribution=%#v", call)
		}
		if call.CallID == callID && (call.Status != domain.SupervisorToolCompleted || call.ResultJSON != legacyResult) {
			t.Fatalf("legacy receipt changed: %#v", call)
		}
		if call.CallID == unknownCallID && (call.Status != domain.SupervisorToolPending || call.ResultJSON != "" || call.CompletedAt != nil) {
			t.Fatalf("v151 invented a result for an unresolved started call: %#v", call)
		}
	}
	for _, id := range []string{job.ID, completedJob.ID} {
		jobAttribution, err := state.GetThreadCommandRuntimeJobAgentAttribution(ctx,
			threadRecord.ID, id)
		if err != nil || jobAttribution.AgentID != job.RootAgentID ||
			jobAttribution.AgentAttemptID != "" ||
			jobAttribution.Source != domain.AgentAttributionLegacyRoot {
			t.Fatalf("legacy Command attribution=%#v err=%v", jobAttribution, err)
		}
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE command_runtime_job_agents
		SET attribution_source = 'recorded' WHERE job_id = ?`, job.ID); err == nil {
		t.Fatal("v151 allowed historical Command attribution mutation")
	}
	assertNoForeignKeyViolations(t, state.db)
}

// Frozen v150 INSERT: no current Job column constant, current writer, schema
// extension, or pre-created v151 actor row participates in this fixture.
func insertV150CommandRuntimeJob(t *testing.T, state *SQLiteStore, job runner.CommandRuntimeJob, rowid int64) {
	t.Helper()
	const columns = `rowid, protocol_version, id, operation_digest, request_fingerprint,
		invocation_id, run_id, mission_id, session_id, workspace_id, root_agent_id,
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
		updated_at, adapter_kind, adapter_backend, adapter_backend_identity,
		adapter_generation, adapter_isolation_grade, adapter_network_policy, adapter_credential_policy`
	values := []any{rowid, "command-runtime.v2", job.ID, job.OperationDigest,
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
		nullableTS(job.CompletedAt), ts(job.UpdatedAt), job.Adapter.Kind, job.Adapter.Backend,
		job.Adapter.BackendIdentity, job.Adapter.Generation, job.Adapter.IsolationGrade,
		job.Adapter.NetworkPolicy, job.Adapter.CredentialPolicy}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	if _, err := state.db.ExecContext(t.Context(), `INSERT INTO command_runtime_jobs (`+columns+
		`) VALUES (`+placeholders+`)`, values...); err != nil {
		t.Fatal(err)
	}
}

func TestCleanInstallV151IncludesAgentAttributionLedgers(t *testing.T) {
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "clean-v151-agent-attribution.db"))
	defer state.Close()
	used, err := state.tryCleanInstallBaseline(t.Context(), migrationPlan())
	if err != nil || !used {
		t.Fatalf("v151 clean-install baseline used=%t err=%v", used, err)
	}
	for _, name := range []string{"run_supervisor_tool_call_agents",
		"command_runtime_job_agents"} {
		var count int
		if err := state.db.QueryRowContext(t.Context(), `SELECT COUNT(*)
			FROM sqlite_master WHERE type = 'table' AND name = ?`, name).
			Scan(&count); err != nil || count != 1 {
			t.Fatalf("clean-install table %s count=%d err=%v", name, count, err)
		}
	}
	assertNoForeignKeyViolations(t, state.db)
}
