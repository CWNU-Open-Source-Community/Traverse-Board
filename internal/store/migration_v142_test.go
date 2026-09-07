package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestSchemaV142MigratesPopulatedHostExecutionChildrenAndAcceptsDebug(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "schema-v141.db"))
	defer state.Close()
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 141); err != nil {
		t.Fatal(err)
	}
	intent, _ := hostExecutionStoreIntent(t, ctx, state)
	if replayed, err := state.PrepareHostExecutionIntent(ctx, intent); err != nil || replayed {
		t.Fatalf("prepare v141 host intent replayed=%t err=%v", replayed, err)
	}
	emptyDigest := sha256.Sum256(nil)
	startedAt := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	result := runner.HostExecutionResult{
		ProtocolVersion: runner.HostExecutionProtocolVersion,
		PolicyVersion:   runner.HostExecutionPolicyVersion,
		RequestID:       intent.RequestID, OperationKeyDigest: intent.OperationKeyDigest,
		RunID: intent.RunID, MissionID: intent.MissionID,
		SessionID: intent.SessionID, WorkspaceID: intent.WorkspaceID,
		InteractionSnapshotID:    intent.InteractionSnapshotID,
		InteractionRevision:      intent.InteractionRevision,
		ExecutionProfileRevision: intent.ExecutionProfileRevision,
		PermissionSnapshotID:     intent.PermissionSnapshotID,
		PermissionRevision:       intent.PermissionRevision,
		PermissionMode:           intent.PermissionMode,
		SpecFingerprint:          intent.Spec.Fingerprint,
		Backend:                  "migration-v142-test",
		Stdout: runner.ControlledOutput{
			CapturedPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
		},
		Stderr: runner.ControlledOutput{
			CapturedPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
		},
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Millisecond),
		TreeReaped: true, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: runner.MaxHostActiveProcesses,
		JobMemoryLimit:     runner.MaxHostProcessMemoryBytes,
		StdinClosed:        true, NetworkRequested: true, ProductExecutionEnabled: true,
	}
	if _, replayed, err := state.RecordHostExecutionResult(ctx, result); err != nil || replayed {
		t.Fatalf("record v141 host receipt replayed=%t err=%v", replayed, err)
	}

	if err := state.applyMigration(ctx, migrationPlan()[141]); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{
		"host_command_execution_intents":    1,
		"host_command_execution_operations": 1,
		"host_command_execution_receipts":   1,
	} {
		var count int
		if err := state.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("migrated %s rows=%d want=%d err=%v", table, count, want, err)
		}
	}
	assertNoForeignKeyViolations(t, state.db)

	permission, err := application.NewRunExecutionPermissionService(state,
		domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
			DebugMaximumAccessEnabled: true,
		}).Change(ctx, application.ChangeRunExecutionPermissionRequest{
		RunID: intent.RunID, Mode: string(domain.RunExecutionPermissionDebug),
		OperationKey: "migration-v142-debug-permission-0001",
		RequestedBy:  "test_operator", Reason: "prove Debug inherits stateless host execution",
		ConfirmDebugAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := state.GetRunExecutionInteraction(ctx, intent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := state.GetRunExecutionProfile(ctx, intent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	debugIntent, err := runner.NewHostExecutionIntent(runner.HostExecutionIntentRequest{
		OperationKeyDigest: strings.Repeat("d", 64),
		RunID:              intent.RunID, MissionID: intent.MissionID,
		SessionID: intent.SessionID, WorkspaceID: intent.WorkspaceID,
		Interaction: interaction, Profile: profile,
		Permission: permission.Permission, Spec: intent.Spec,
		RequestedBy: "test_operator", CreatedAt: startedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := state.PrepareHostExecutionIntent(ctx, debugIntent); err != nil || replayed {
		t.Fatalf("schema v142 rejected Debug host intent: replayed=%t err=%v", replayed, err)
	}
}
