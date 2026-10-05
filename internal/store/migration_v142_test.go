package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestSchemaV142MigratesPopulatedHostExecutionChildrenAndAcceptsDebug(t *testing.T) {
	ctx := context.Background()
	state := openHistoricalTestDatabase(t, filepath.Join(t.TempDir(), "schema-v141.db"), 141)
	intent, _ := hostExecutionStoreIntent(t, ctx, state)
	if replayed, err := seedHistoricalHostExecutionIntent(ctx, state, intent); err != nil || replayed {
		t.Fatalf("prepare v141 host intent replayed=%t err=%v", replayed, err)
	}
	emptyDigest := sha256.Sum256(nil)
	startedAt := time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC)
	receipt := runner.HostExecutionReceipt{
		ProtocolVersion: runner.HostCommandReceiptProtocolVersion,
		PolicyVersion:   runner.HostExecutionPolicyVersion,
		RequestID:       intent.RequestID, Backend: "migration-v142-test",
		StdoutPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
		StderrPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
		StartedAt:          startedAt, CompletedAt: startedAt.Add(time.Millisecond),
		TreeReaped: true, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: runner.MaxHostActiveProcesses,
		JobMemoryLimit:     runner.MaxHostProcessMemoryBytes,
		StdinClosed:        true, NetworkRequested: true, ProductExecutionEnabled: true,
	}
	if _, replayed, err := seedHistoricalHostExecutionReceipt(ctx, state, intent, receipt); err != nil || replayed {
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

	permission, err := seedHistoricalHostPermission(ctx, state, intent.RunID, domain.RunExecutionPermissionDebug)
	if err != nil {
		t.Fatal(err)
	}
	debugIntent := intent
	debugIntent.OperationKeyDigest = strings.Repeat("d", 64)
	debugIntent.PermissionSnapshotID = permission.ID
	debugIntent.PermissionRevision = permission.Revision
	debugIntent.PermissionMode = permission.Mode
	debugIntent.CreatedAt = startedAt.Add(time.Second)
	debugIntent.RequestID = runner.HostExecutionRequestID(debugIntent.RunID, debugIntent.OperationKeyDigest, debugIntent.Spec.Fingerprint)
	if err := debugIntent.Validate(); err != nil {
		t.Fatal(err)
	}
	if replayed, err := seedHistoricalHostExecutionIntent(ctx, state, debugIntent); err != nil || replayed {
		t.Fatalf("schema v142 rejected Debug host intent: replayed=%t err=%v", replayed, err)
	}
}
