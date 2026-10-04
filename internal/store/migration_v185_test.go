package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestSchemaV185PreservesRealV184CommandJobs(t *testing.T) {
	if got := migrationPlanDigest(migrationPlan()[:184]); got != "334bb197038c5b66a1ab0d176186fe8dd831be1b4d4fa8ac77e3b1212c2567d3" {
		t.Fatalf("published v1-v184 prefix changed: %s", got)
	}
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "actual-v184.db")
	st := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(ctx, st, migrationPlan(), 177); err != nil {
		t.Fatal(err)
	}
	job := commandRuntimeMigrationJob(t, st, domain.RunExecutionPermissionFullAccess, commandruntimeadapter.HostUnsandboxed(strings.Repeat("a", 64)))
	insertV162CommandRuntimeJob(t, st, job, 73)
	oldIntents := seedV177FixedCommandHistory(t, st, job)
	for _, migration := range migrationPlan()[177:184] {
		if err := st.applyMigration(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	before, err := st.GetCommandRuntimeJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	actors := legacyFixtureRows(t, st, "command_runtime_job_agents")
	permissions := legacyFixtureRows(t, st, "run_execution_permission_snapshots")
	oldReceipts := legacyFixtureRows(t, st, "controlled_command_execution_receipts")
	ledger, err := st.loadAppliedMigrations(ctx)
	if err != nil || len(ledger) != 184 {
		t.Fatal("not a real v184 input", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	after, err := st.GetCommandRuntimeJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("historical Job changed", err)
	}
	var rowid, operator int
	if err := st.db.QueryRowContext(ctx, `SELECT rowid,operator_invocation FROM command_runtime_jobs WHERE id=?`, job.ID).Scan(&rowid, &operator); err != nil || rowid != 73 || operator != 0 {
		t.Fatal("old Job rowid or default provenance changed", rowid, operator, err)
	}
	if !reflect.DeepEqual(actors, legacyFixtureRows(t, st, "command_runtime_job_agents")) || !reflect.DeepEqual(permissions, legacyFixtureRows(t, st, "run_execution_permission_snapshots")) {
		t.Fatal("historical actors or permissions changed")
	}
	if !reflect.DeepEqual(oldReceipts, legacyFixtureRows(t, st, "controlled_command_execution_receipts")) {
		t.Fatal("old fixed command receipt changed")
	}
	for index, intent := range oldIntents {
		got, found, err := st.GetControlledExecutionIntentByPlanID(ctx, intent.PlanID)
		if err != nil || !found || got != intent {
			t.Fatal("old intent read changed", got, found, err)
		}
		_, found, err = st.GetControlledExecutionReceipt(ctx, intent.RequestID)
		if err != nil || found != (index == 0) {
			t.Fatal("unknown old intent acquired a receipt", found, err)
		}
	}
	current, err := st.loadAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for version, entry := range ledger {
		if current[version] != entry {
			t.Fatalf("migration %d changed", version)
		}
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE command_runtime_jobs SET operator_invocation=1 WHERE id=?`, job.ID); err == nil {
		t.Fatal("old Job source changed")
	}
	// Actor input alone cannot produce process-local preparation provenance.
	forged := after
	forged.ID = "forged-operator-job"
	forged.OperationDigest = strings.Repeat("b", 64)
	if _, _, err := st.PrepareCommandRuntimeJobForAgent(ctx, forged, domain.AgentAttribution{AgentID: job.RootAgentID, Source: domain.AgentAttributionOperatorRoot}); err == nil || !strings.Contains(err.Error(), "live host provenance") {
		t.Fatal("public actor input forged operator invocation", err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO command_runtime_job_agents (job_id,run_id,agent_id,agent_attempt_id,attribution_source,created_at) VALUES (?,?,?,NULL,'operator_root',?)`, job.ID, job.RunID, job.RootAgentID, ts(job.CreatedAt)); err == nil || !strings.Contains(err.Error(), "invocation source differs") {
		t.Fatal("actor source disagreed with stored provenance", err)
	}
	assertNoForeignKeyViolations(t, st.db)
	assertLatestMigrationLedger(t, st, migrationPlan())
	t.Log("immutable v1-v184 migration digest", migrationPlanDigest(migrationPlan()[:184]))
}

// Seed through the real legacy writer before upgrading. No current permission
// is relabelled and no new execution is inferred for the unknown second intent.
func seedV177FixedCommandHistory(t *testing.T, st *SQLiteStore, job runner.CommandRuntimeJob) []runner.ControlledExecutionIntent {
	t.Helper()
	ctx := t.Context()
	lease, found, err := st.GetRunExecutionLease(ctx, job.RunID)
	if err != nil || !found {
		t.Fatal("historical lease", err)
	}
	if _, _, err := st.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Pause(ctx, job.RunID); err != nil {
		t.Fatal(err)
	}
	interaction, err := application.NewRunExecutionInteractionService(st).Change(ctx, application.ChangeRunExecutionInteractionRequest{RunID: job.RunID, Mode: "controlled", Trust: "trusted", ConfirmWorkspaceTrust: true, OperationKey: "historical-controlled-interaction", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.GetRunExecutionProfile(ctx, job.RunID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.GetWorkspaceByID(ctx, job.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	intents := []runner.ControlledExecutionIntent{}
	for _, id := range []string{"historical-fixed-complete", "historical-fixed-unknown"} {
		plan, err := runner.PlanControlledCommand(runner.ControlledCommandPlanRequest{ID: id, WorkspaceID: workspace.ID, WorkspaceRoot: workspace.RootPath,
			Interaction: interaction.Interaction, CurrentProfile: profile, CurrentSurface: domain.ExecutionSurfaceCode, Kind: runner.ControlledCommandGoVersion})
		if err != nil {
			t.Fatal(err)
		}
		intent, err := runner.NewControlledExecutionIntent(plan, "operator", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.PrepareControlledExecutionIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
		intents = append(intents, intent)
		if id == "historical-fixed-unknown" {
			continue
		}
		result := runner.ControlledExecutionResult{ProtocolVersion: runner.ControlledExecutionProtocolVersion, PolicyVersion: runner.ControlledExecutionPolicyVersion,
			RequestID: intent.RequestID, PlanID: plan.ID, PlanFingerprint: plan.Fingerprint, RunID: plan.RunID, WorkspaceID: plan.WorkspaceID,
			InteractionSnapshotID: plan.InteractionSnapshotID, InteractionRevision: plan.InteractionRevision, ExecutionProfileRevision: plan.ExecutionProfileRevision,
			Kind: plan.Kind, Backend: "historical-fixture", Stdout: runner.ControlledOutput{CapturedPrefixSHA256: testCommandRuntimeDigest("")}, Stderr: runner.ControlledOutput{CapturedPrefixSHA256: testCommandRuntimeDigest("")},
			StartedAt: intent.CreatedAt, CompletedAt: intent.CreatedAt.Add(time.Second), TreeReaped: true, RestrictedToken: true, LowIntegrityToken: true, JobAssignedAtCreation: true,
			KillOnJobClose: true, ActiveProcessLimit: 1, ProcessMemoryLimit: runner.MaxControlledProcessMemoryBytes, StdinClosed: true, ProductExecutionEnabled: true}
		if _, _, err := st.RecordControlledExecutionResult(ctx, result); err != nil {
			t.Fatal(err)
		}
	}
	return intents
}
