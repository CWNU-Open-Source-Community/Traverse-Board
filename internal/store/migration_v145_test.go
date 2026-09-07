package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	eventpkg "cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
)

func TestSchemaV145UpgradesExistingRunForExactNetworkAuthorityExpansion(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v144-network-authority.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 144); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "preserve a v144 Run",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(ctx, plan[144]); err != nil {
		t.Fatal(err)
	}
	result, err := application.NewRunNetworkAuthorityService(state).WithRuntimeAuthority(
		domain.NewExecutionPermissionRuntimeAuthority()).Expand(ctx,
		application.ExpandRunNetworkAuthorityRequest{
			Version: application.RunNetworkAuthorityControlProtocolVersion,
			RunID:   run.ID, ExpectedModeRevision: 1,
			AddAllowedTargets: []string{"search.example.org"},
			OperationKey:      "migration-v145-network-authority-0001",
			RequestedBy:       "test_operator",
		})
	if err != nil || result.Mode.Revision != 2 ||
		result.Mode.Scope.NetworkMode != "allowlist" {
		t.Fatalf("v145 expansion=%#v err=%v", result, err)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 145 {
		t.Fatalf("schema version=%d want=145 err=%v", version, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestSchemaV145RejectsUnboundOrUnsafeNetworkAuthorityRows(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "schema-v145-network-guards.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "guard exact network rows",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := state.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_network_authority_operations
		(operation_key_digest, request_fingerprint, snapshot_id, run_id,
		expected_mode_revision, requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		strings.Repeat("a", 64), strings.Repeat("b", 64), "missing-network-snapshot",
		run.ID, current.Revision, "test_operator", ts(time.Now().UTC())); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("orphan network operation commit error=%v", err)
	}

	unbound := current
	unbound.ID = "run-mode-unbound-network"
	unbound.Revision++
	unbound.Scope.NetworkMode = "allowlist"
	unbound.Scope.AllowedTargets = []string{"search.example.org"}
	unbound.RequestedBy = "test_operator"
	unbound.Reason = "unbound direct snapshot"
	unbound.CreatedAt = current.CreatedAt.Add(time.Second)
	tx, err = state.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertRunModeSnapshotTx(ctx, tx, unbound); err == nil ||
		!strings.Contains(err.Error(), "Run mode snapshot binding") {
		_ = tx.Rollback()
		t.Fatalf("unbound network snapshot error=%v", err)
	}
	_ = tx.Rollback()

	for index, target := range []string{"*.example.org", "127.0.0.1", "::1", "a..example.org"} {
		next := unbound
		next.ID = fmt.Sprintf("run-mode-unsafe-network-%d", index)
		next.Scope.AllowedTargets = []string{target}
		next.CreatedAt = current.CreatedAt.Add(time.Duration(index+2) * time.Second)
		tx, err = state.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_network_authority_operations
			(operation_key_digest, request_fingerprint, snapshot_id, run_id,
			expected_mode_revision, requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("%064x", index+1), fmt.Sprintf("%064x", index+101), next.ID,
			run.ID, current.Revision, next.RequestedBy, ts(next.CreatedAt)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("target=%q operation insert error=%v", target, err)
		}
		if err := insertRunModeSnapshotTx(ctx, tx, next); err == nil ||
			!strings.Contains(err.Error(), "Run mode snapshot binding") {
			_ = tx.Rollback()
			t.Fatalf("unsafe target=%q snapshot error=%v", target, err)
		}
		_ = tx.Rollback()
	}
}

func TestSchemaV145ResetsLegacyBroadThreadNetworkPreference(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v144-legacy-network-successor.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 144); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	linkedSession := session.New("", "migrate a legacy broad network preference", "code")
	linkedSession.ID = "session-v144-legacy-broad-network"
	linkedSession.CreatedAt, linkedSession.UpdatedAt = now, now
	mission := domain.Mission{ID: "mission-v144-legacy-broad-network",
		Goal: "migrate a legacy broad network preference", Profile: domain.ProfileCode,
		Scope: domain.Scope{NetworkMode: "allowlist",
			AllowedTargets: []string{"public_https"}}, CreatedAt: now, UpdatedAt: now}
	predecessor := domain.Run{ID: "run-v144-legacy-broad-network", MissionID: mission.ID,
		SessionID: linkedSession.ID, Status: domain.RunCreated,
		Config: domain.RunConfig{ModelRoute: "code"}, Budget: domain.Budget{MaxTurns: 2},
		CreatedAt: now, UpdatedAt: now}
	mode, err := domain.NewInitialRunModeSnapshot("run-mode-v144-legacy-broad-network",
		predecessor, mission, domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		"legacy_fixture", "legacy broad authority", now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := eventpkg.New(predecessor.ID, mission.ID, eventpkg.RunCreatedEvent,
		"migration_fixture", predecessor.ID, map[string]any{"status": predecessor.Status})
	if err != nil {
		t.Fatal(err)
	}
	created.CreatedAt = now
	if err := state.CreateMissionRun(ctx, mission, predecessor, mode, linkedSession, true,
		[]eventpkg.Event{created}); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	if _, err := runs.Cancel(ctx, predecessor.ID); err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(ctx, plan[144]); err != nil {
		t.Fatal(err)
	}
	continued, err := application.NewThreadService(state).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue without inheriting legacy broad authority",
			OperationKey: "legacy-network-reset-message-0001", RequestedBy: "operator",
		})
	if err != nil || !continued.SuccessorCreated {
		t.Fatalf("successor=%#v err=%v", continued, err)
	}
	mode, err = state.GetRunMode(ctx, continued.Run.ID)
	if err != nil || mode.Scope.NetworkMode != "disabled" ||
		len(mode.Scope.AllowedTargets) != 0 {
		t.Fatalf("legacy successor mode=%#v err=%v", mode, err)
	}
	if lease, found, err := state.GetRunExecutionLease(ctx, continued.Run.ID); err != nil || found {
		t.Fatalf("legacy successor inherited lease: found=%v lease=%#v err=%v",
			found, lease, err)
	}
	events, err := state.ListThreadEvents(ctx, threadRecord.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundReset := false
	for _, event := range events {
		if event.Type == "thread.run_successor_created" &&
			strings.Contains(event.PayloadJSON, `"legacy_network_authority_reset":true`) {
			foundReset = true
		}
	}
	if !foundReset {
		t.Fatalf("legacy network reset audit event missing: %#v", events)
	}
}
