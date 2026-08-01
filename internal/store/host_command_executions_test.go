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

func TestHostCommandExecutionAuditIsImmutableIdempotentAndMetadataOnly(
	t *testing.T,
) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "host-execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	intent, environment := hostExecutionStoreIntent(t, ctx, st)
	replayed, err := st.PrepareHostExecutionIntent(ctx, intent)
	if err != nil || replayed {
		t.Fatalf("prepare replayed=%v err=%v", replayed, err)
	}
	retry := intent
	retry.CreatedAt = retry.CreatedAt.Add(time.Minute)
	replayed, err = st.PrepareHostExecutionIntent(ctx, retry)
	if err != nil || !replayed {
		t.Fatalf("prepare replay replayed=%v err=%v", replayed, err)
	}
	conflict := intent
	conflict.RequestedBy = "another_operator"
	if _, err := st.PrepareHostExecutionIntent(
		ctx, conflict); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("conflicting intent error=%v", err)
	}
	reused := hostExecutionStoreIntentWithPurpose(
		t, intent, environment, "different exact command purpose")
	if _, err := st.PrepareHostExecutionIntent(
		ctx, reused); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("reused operation key error=%v", err)
	}

	var storedIntentText string
	err = st.db.QueryRowContext(ctx, `SELECT executable_path || argv_json ||
		working_directory || environment_keys_json || purpose
		FROM host_command_execution_intents WHERE request_id = ?`,
		intent.RequestID).Scan(&storedIntentText)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range environment {
		if strings.Contains(storedIntentText, entry) {
			t.Fatalf("environment value was persisted in host intent: %q", entry)
		}
	}

	rawOutput := []byte("host-output-must-remain-transient")
	rawDigest := sha256.Sum256(rawOutput)
	emptyDigest := sha256.Sum256(nil)
	startedAt := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	result := runner.HostExecutionResult{
		ProtocolVersion:    runner.HostExecutionProtocolVersion,
		PolicyVersion:      runner.HostExecutionPolicyVersion,
		RequestID:          intent.RequestID,
		OperationKeyDigest: intent.OperationKeyDigest,
		RunID:              intent.RunID, MissionID: intent.MissionID,
		SessionID: intent.SessionID, WorkspaceID: intent.WorkspaceID,
		InteractionSnapshotID:    intent.InteractionSnapshotID,
		InteractionRevision:      intent.InteractionRevision,
		ExecutionProfileRevision: intent.ExecutionProfileRevision,
		PermissionSnapshotID:     intent.PermissionSnapshotID,
		PermissionRevision:       intent.PermissionRevision,
		PermissionMode:           intent.PermissionMode,
		SpecFingerprint:          intent.Spec.Fingerprint,
		Backend:                  "test-host-backend",
		Stdout: runner.ControlledOutput{
			Data: rawOutput, ObservedBytes: int64(len(rawOutput)),
			CapturedBytes:        len(rawOutput),
			CapturedPrefixSHA256: hex.EncodeToString(rawDigest[:]),
		},
		Stderr: runner.ControlledOutput{
			CapturedPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
		},
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
		TreeReaped: true, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: runner.MaxHostActiveProcesses,
		JobMemoryLimit:     runner.MaxHostProcessMemoryBytes,
		StdinClosed:        true, NetworkRequested: true,
		ProductExecutionEnabled: true,
	}
	receipt, replayed, err := st.RecordHostExecutionResult(ctx, result)
	if err != nil || replayed {
		t.Fatalf("record replayed=%v receipt=%+v err=%v",
			replayed, receipt, err)
	}
	loaded, found, err := st.GetHostExecutionReceipt(ctx, intent.RequestID)
	if err != nil || !found || loaded != receipt {
		t.Fatalf("loaded found=%v receipt=%+v err=%v", found, loaded, err)
	}
	_, replayed, err = st.RecordHostExecutionResult(ctx, result)
	if err != nil || !replayed {
		t.Fatalf("result replay replayed=%v err=%v", replayed, err)
	}
	var storedReceiptText string
	err = st.db.QueryRowContext(ctx, `SELECT request_id || backend ||
		stdout_prefix_sha256 || stderr_prefix_sha256
		FROM host_command_execution_receipts WHERE request_id = ?`,
		intent.RequestID).Scan(&storedReceiptText)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedReceiptText, string(rawOutput)) {
		t.Fatal("raw host command output was persisted")
	}
	for _, statement := range []string{
		`UPDATE host_command_execution_intents
			SET requested_by = 'tampered' WHERE request_id = ?`,
		`DELETE FROM host_command_execution_intents WHERE request_id = ?`,
		`UPDATE host_command_execution_operations
			SET requested_by = 'tampered' WHERE request_id = ?`,
		`DELETE FROM host_command_execution_operations WHERE request_id = ?`,
		`UPDATE host_command_execution_receipts
			SET exit_code = 99 WHERE request_id = ?`,
		`DELETE FROM host_command_execution_receipts WHERE request_id = ?`,
	} {
		if _, err := st.db.ExecContext(
			ctx, statement, intent.RequestID); err == nil {
			t.Fatalf("immutable host execution statement succeeded: %s",
				statement)
		}
	}
	runEvents, err := st.ListRunEvents(ctx, intent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var prepared, completed int
	for _, event := range runEvents {
		switch event.Type {
		case events.HostCommandExecutionPreparedEvent:
			prepared++
		case events.HostCommandExecutionCompletedEvent:
			completed++
		}
	}
	if prepared != 1 || completed != 1 {
		t.Fatalf("host execution events prepared=%d completed=%d",
			prepared, completed)
	}
}

func TestSchemaV90AddsHostCommandExecutionAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v89-host-execution.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, statement := range removeSchemaV90ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v90 fixture with %q: %v", statement, err)
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
		"host_command_execution_intents",
		"host_command_execution_operations",
		"host_command_execution_receipts",
	} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master
				WHERE type = 'table' AND name = ?`,
			table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}

func hostExecutionStoreIntent(
	t *testing.T,
	ctx context.Context,
	st *SQLiteStore,
) (runner.HostExecutionIntent, []string) {
	t.Helper()
	workspaceRoot := filepath.Clean(t.TempDir())
	workspace := WorkspaceRecord{
		ID: "workspace-host-execution", Name: "host-execution",
		RootPath: workspaceRoot,
	}
	if err := st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	mission, runRecord, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "audit one non-sandboxed host command", Profile: "code",
			WorkspaceID: workspace.ID,
			Budget:      domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := application.NewRunExecutionProfileService(st).Change(
		ctx, application.ChangeRunExecutionProfileRequest{
			RunID: runRecord.ID, Profile: "local",
			OperationKey: "host-execution-profile-0001",
			RequestedBy:  "test_operator", Reason: "prepare host command",
		})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := application.NewRunExecutionInteractionService(st).
		Change(ctx, application.ChangeRunExecutionInteractionRequest{
			RunID: runRecord.ID, Mode: "controlled", Trust: "trusted",
			OperationKey: "host-execution-interaction-0001",
			RequestedBy:  "test_operator", Reason: "trusted test workspace",
			ConfirmWorkspaceTrust: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := application.NewRunExecutionPermissionService(st,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		}).Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: runRecord.ID, Mode: "full_access",
		OperationKey: "host-execution-permission-0001",
		RequestedBy:  "test_operator", Reason: "test non-sandboxed command",
		ConfirmDangerFullAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"PATH=" + filepath.Join(workspaceRoot, "bin"),
		"HOME=" + filepath.Join(workspaceRoot, "home"),
	}
	spec, err := runner.NewHostCommandSpec(runner.HostCommandSpecRequest{
		ExecutablePath:      filepath.Join(workspaceRoot, "tool.exe"),
		ExecutableSHA256:    strings.Repeat("a", 64),
		Argv:                []string{"test", "./..."},
		WorkingDirectory:    workspaceRoot,
		Environment:         environment,
		NetworkIntent:       runner.HostNetworkIntentHost,
		TimeoutMilliseconds: (30 * time.Second).Milliseconds(),
		Purpose:             "run an explicitly confirmed host test",
	})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := runner.NewHostExecutionIntent(
		runner.HostExecutionIntentRequest{
			OperationKeyDigest: strings.Repeat("b", 64),
			RunID:              runRecord.ID, MissionID: mission.ID,
			SessionID: runRecord.SessionID, WorkspaceID: workspace.ID,
			Interaction: interaction.Interaction, Profile: profile.Profile,
			Permission: permission.Permission, Spec: spec,
			RequestedBy: "test_operator",
			CreatedAt:   time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC),
		})
	if err != nil {
		t.Fatal(err)
	}
	return intent, environment
}

func hostExecutionStoreIntentWithPurpose(
	t *testing.T,
	base runner.HostExecutionIntent,
	environment []string,
	purpose string,
) runner.HostExecutionIntent {
	t.Helper()
	spec, err := runner.NewHostCommandSpec(runner.HostCommandSpecRequest{
		ExecutablePath:   base.Spec.ExecutablePath,
		ExecutableSHA256: base.Spec.ExecutableSHA256,
		Argv:             base.Spec.Argv, WorkingDirectory: base.Spec.WorkingDirectory,
		Environment: environment, NetworkIntent: base.Spec.NetworkIntent,
		TimeoutMilliseconds: base.Spec.TimeoutMilliseconds, Purpose: purpose,
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.Spec = spec
	changed.RequestID = runner.HostExecutionRequestID(
		changed.RunID, changed.OperationKeyDigest, spec.Fingerprint)
	if err := changed.Validate(); err != nil {
		t.Fatal(err)
	}
	return changed
}

func removeSchemaV90ForTestStatements() []string {
	return append(removeSchemaV91ForTestStatements(), []string{
		`DROP TRIGGER trg_host_command_execution_receipt_delete_immutable`,
		`DROP TRIGGER trg_host_command_execution_receipt_update_immutable`,
		`DROP TRIGGER trg_host_command_execution_operation_delete_immutable`,
		`DROP TRIGGER trg_host_command_execution_operation_update_immutable`,
		`DROP TRIGGER trg_host_command_execution_intent_delete_immutable`,
		`DROP TRIGGER trg_host_command_execution_intent_update_immutable`,
		`DROP TRIGGER trg_host_command_execution_receipt_insert_binding`,
		`DROP TRIGGER trg_host_command_execution_operation_insert_binding`,
		`DROP TRIGGER trg_host_command_execution_intent_insert_binding`,
		`DROP TABLE host_command_execution_receipts`,
		`DROP TABLE host_command_execution_operations`,
		`DROP INDEX idx_host_command_execution_intents_run_created`,
		`DROP TABLE host_command_execution_intents`,
		`DELETE FROM schema_migrations WHERE version = 90`,
	}...)
}
