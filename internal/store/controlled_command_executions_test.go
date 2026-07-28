package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runner"
)

func TestControlledCommandExecutionAuditIsImmutableAndMetadataOnly(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "controlled-execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	plan := controlledExecutionStorePlan(t, ctx, st)
	intent, err := runner.NewControlledExecutionIntent(
		plan, "test_operator", time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := st.PrepareControlledExecutionIntent(ctx, intent)
	if err != nil || replayed {
		t.Fatalf("prepare replayed=%v err=%v", replayed, err)
	}
	retryIntent := intent
	retryIntent.CreatedAt = retryIntent.CreatedAt.Add(time.Minute)
	replayed, err = st.PrepareControlledExecutionIntent(ctx, retryIntent)
	if err != nil || !replayed {
		t.Fatalf("prepare replay replayed=%v err=%v", replayed, err)
	}
	conflict := intent
	conflict.RequestedBy = "another_operator"
	if _, err := st.PrepareControlledExecutionIntent(ctx, conflict); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("conflicting intent error=%v", err)
	}
	reusedOperation := intent
	reusedOperation.PlanFingerprint = strings.Repeat("b", 64)
	reusedOperation.RequestID = "controlled-exec-" +
		reusedOperation.PlanFingerprint[:24]
	if _, err := st.PrepareControlledExecutionIntent(ctx, reusedOperation); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("reused operation identity error=%v", err)
	}
	emptyDigest := sha256.Sum256(nil)
	if _, err := st.db.ExecContext(ctx, `INSERT INTO
		controlled_command_execution_receipts
		(request_id, protocol_version, policy_version, backend, exit_code,
		stdout_observed_bytes, stdout_captured_bytes, stdout_prefix_sha256,
		stdout_truncated, stderr_observed_bytes, stderr_captured_bytes,
		stderr_prefix_sha256, stderr_truncated, started_at, completed_at,
		timed_out, cancelled, output_limit_exceeded, tree_reaped,
		restricted_token, low_integrity_token, job_assigned_at_creation,
		kill_on_job_close, active_process_limit, process_memory_limit,
		stdin_closed, environment_inherited, network_requested,
		persistent_process, product_execution_enabled)
		VALUES (?, ?, ?, 'invalid-direct-write', 0,
			2, 1, ?, 1, 0, 0, ?, 0, ?, ?, 0, 0, 0, 1, 1, 1, 1, 1, 1,
			?, 1, 0, 0, 0, 1)`,
		intent.RequestID, runner.ControlledExecutionProtocolVersion,
		runner.ControlledExecutionPolicyVersion, strings.Repeat("0", 64),
		hex.EncodeToString(emptyDigest[:]),
		"2026-07-26T09:00:00Z", "2026-07-26T09:00:01Z",
		runner.MaxControlledProcessMemoryBytes); err == nil {
		t.Fatal("SQLite accepted an impossible controlled execution receipt")
	}

	rawOutput := []byte("sensitive-output-must-not-be-persisted")
	rawDigest := sha256.Sum256(rawOutput)
	startedAt := time.Date(2026, 7, 26, 9, 1, 0, 0, time.UTC)
	result := runner.ControlledExecutionResult{
		ProtocolVersion: runner.ControlledExecutionProtocolVersion,
		PolicyVersion:   runner.ControlledExecutionPolicyVersion,
		RequestID:       intent.RequestID,
		PlanID:          plan.ID, PlanFingerprint: plan.Fingerprint,
		RunID: plan.RunID, WorkspaceID: plan.WorkspaceID,
		InteractionSnapshotID:    plan.InteractionSnapshotID,
		InteractionRevision:      plan.InteractionRevision,
		ExecutionProfileRevision: plan.ExecutionProfileRevision,
		Kind:                     plan.Kind, Backend: "test-controlled-backend",
		Stdout: runner.ControlledOutput{
			Data: rawOutput, ObservedBytes: int64(len(rawOutput)),
			CapturedBytes:        len(rawOutput),
			CapturedPrefixSHA256: hex.EncodeToString(rawDigest[:]),
		},
		Stderr: runner.ControlledOutput{
			CapturedPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
		},
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
		TreeReaped: true, RestrictedToken: true, LowIntegrityToken: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: 1,
		ProcessMemoryLimit: runner.MaxControlledProcessMemoryBytes,
		StdinClosed:        true, ProductExecutionEnabled: true,
	}
	receipt, replayed, err := st.RecordControlledExecutionResult(ctx, result)
	if err != nil || replayed {
		t.Fatalf("record replayed=%v receipt=%+v err=%v", replayed, receipt, err)
	}
	if receipt.StdoutCapturedBytes != len(rawOutput) ||
		receipt.StdoutPrefixSHA256 != hex.EncodeToString(rawDigest[:]) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	loaded, found, err := st.GetControlledExecutionReceipt(ctx, intent.RequestID)
	if err != nil || !found || loaded != receipt {
		t.Fatalf("loaded found=%v receipt=%+v err=%v", found, loaded, err)
	}
	_, replayed, err = st.RecordControlledExecutionResult(ctx, result)
	if err != nil || !replayed {
		t.Fatalf("result replay replayed=%v err=%v", replayed, err)
	}

	var storedColumns string
	err = st.db.QueryRowContext(ctx, `SELECT
		request_id || protocol_version || policy_version || backend ||
		stdout_prefix_sha256 || stderr_prefix_sha256
		FROM controlled_command_execution_receipts WHERE request_id = ?`,
		intent.RequestID).Scan(&storedColumns)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedColumns, string(rawOutput)) {
		t.Fatal("raw command output was persisted in the receipt")
	}
	for _, statement := range []string{
		`UPDATE controlled_command_execution_intents
			SET requested_by = 'tampered' WHERE request_id = ?`,
		`DELETE FROM controlled_command_execution_intents WHERE request_id = ?`,
		`UPDATE controlled_command_execution_receipts
			SET exit_code = 99 WHERE request_id = ?`,
		`DELETE FROM controlled_command_execution_receipts WHERE request_id = ?`,
	} {
		if _, err := st.db.ExecContext(ctx, statement, intent.RequestID); err == nil {
			t.Fatalf("immutable controlled execution statement succeeded: %s", statement)
		}
	}
	runEvents, err := st.ListRunEvents(ctx, plan.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var prepared, completed int
	for _, event := range runEvents {
		switch event.Type {
		case events.ControlledCommandExecutionPreparedEvent:
			prepared++
		case events.ControlledCommandExecutionCompletedEvent:
			completed++
		}
	}
	if prepared != 1 || completed != 1 {
		t.Fatalf("controlled execution events prepared=%d completed=%d",
			prepared, completed)
	}
}

func TestSchemaV87AddsControlledCommandExecutionAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v86-controlled-execution.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, statement := range removeSchemaV87ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v87 fixture with %q: %v", statement, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{
		"controlled_command_execution_intents",
		"controlled_command_execution_receipts",
	} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}

func controlledExecutionStorePlan(t *testing.T, ctx context.Context,
	st *SQLiteStore,
) runner.ControlledCommandPlan {
	t.Helper()
	workspaceRoot := filepath.Clean(t.TempDir())
	workspace := WorkspaceRecord{
		ID: "workspace-controlled-execution", Name: "controlled-execution",
		RootPath: workspaceRoot,
	}
	if err := st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	mission, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "audit one controlled command", Profile: "code",
			WorkspaceID: workspace.ID,
			Budget:      domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	profileResult, err := application.NewRunExecutionProfileService(st).Change(
		ctx, application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "local",
			OperationKey: "controlled-execution-profile-0001",
			RequestedBy:  "test_operator", Reason: "prepare controlled command",
		})
	if err != nil {
		t.Fatal(err)
	}
	interactionResult, err :=
		application.NewRunExecutionInteractionService(st).Change(ctx,
			application.ChangeRunExecutionInteractionRequest{
				RunID: run.ID, Mode: "controlled", Trust: "trusted",
				OperationKey: "controlled-execution-interaction-0001",
				RequestedBy:  "test_operator", Reason: "trusted test workspace",
				ConfirmWorkspaceTrust: true,
			})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runner.PlanControlledCommand(runner.ControlledCommandPlanRequest{
		ID: "controlled-command-plan-0001", WorkspaceID: mission.WorkspaceID,
		WorkspaceRoot:  workspaceRoot,
		Interaction:    interactionResult.Interaction,
		CurrentProfile: profileResult.Profile,
		CurrentSurface: domain.ExecutionSurfaceCode,
		Kind:           runner.ControlledCommandGoVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func removeSchemaV87ForTestStatements() []string {
	return append(removeSchemaV88ForTestStatements(), []string{
		`DROP TRIGGER trg_controlled_execution_receipt_delete_immutable`,
		`DROP TRIGGER trg_controlled_execution_receipt_update_immutable`,
		`DROP TRIGGER trg_controlled_execution_intent_delete_immutable`,
		`DROP TRIGGER trg_controlled_execution_intent_update_immutable`,
		`DROP TABLE controlled_command_execution_receipts`,
		`DROP INDEX idx_controlled_execution_intents_run_created`,
		`DROP TABLE controlled_command_execution_intents`,
		`DELETE FROM schema_migrations WHERE version = 87`,
	}...)
}
