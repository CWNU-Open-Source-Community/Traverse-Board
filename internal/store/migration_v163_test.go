package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestSchemaV163PreservesDisabledLegacyJobRowidAndAgent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "command-runtime-v163-old-job.db")
	state := openUnmigratedSQLiteStore(t, path)
	defer state.Close()
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 162); err != nil {
		t.Fatal(err)
	}
	job := commandRuntimeMigrationJob(t, state,
		domain.RunExecutionPermissionFullAccess,
		commandruntimeadapter.HostUnsandboxed(strings.Repeat("a", 64)))
	insertV162CommandRuntimeJob(t, state, job)
	var beforeRowid int64
	var beforeAgent string
	if err := state.db.QueryRowContext(ctx,
		`SELECT rowid FROM command_runtime_jobs WHERE id = ?`, job.ID).
		Scan(&beforeRowid); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRowContext(ctx,
		`SELECT attribution_source FROM command_runtime_job_agents WHERE job_id = ?`,
		job.ID).Scan(&beforeAgent); err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(ctx, migrationPlan()[162]); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.GetCommandRuntimeJob(ctx, job.ID)
	if err != nil || loaded.Network != runner.CommandRuntimeNetworkDisabled ||
		loaded.PermissionRuntimeEpoch != "" || loaded.PermissionGeneration != 0 {
		t.Fatalf("legacy disabled job=%#v err=%v", loaded, err)
	}
	var afterRowid int64
	var afterAgent string
	if err := state.db.QueryRowContext(ctx,
		`SELECT rowid FROM command_runtime_jobs WHERE id = ?`, job.ID).
		Scan(&afterRowid); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRowContext(ctx,
		`SELECT attribution_source FROM command_runtime_job_agents WHERE job_id = ?`,
		job.ID).Scan(&afterAgent); err != nil {
		t.Fatal(err)
	}
	if beforeRowid != afterRowid || beforeAgent != afterAgent {
		t.Fatalf("legacy job rowid/actor changed: %d/%s -> %d/%s",
			beforeRowid, beforeAgent, afterRowid, afterAgent)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func insertV162CommandRuntimeJob(t *testing.T, state *SQLiteStore,
	job runner.CommandRuntimeJob,
) {
	t.Helper()
	columns := strings.TrimSuffix(commandRuntimeJobColumns,
		",\n\tpermission_runtime_epoch, permission_generation")
	if columns == commandRuntimeJobColumns {
		t.Fatal("v163 command Job columns changed unexpectedly")
	}
	values := []any{
		runner.CommandRuntimeProtocolVersion, job.ID, job.OperationDigest,
		job.RequestFingerprint, job.InvocationID, job.RunID, job.MissionID,
		job.SessionID, job.WorkspaceID, job.RootAgentID, job.WorkspaceRootSHA256,
		job.ModeSnapshotID, job.ModeRevision, job.ProfileSnapshotID,
		job.ProfileRevision, job.PermissionSnapshotID, job.PermissionRevision,
		job.PermissionMode, job.LeaseID, job.LeaseGeneration, job.LeaseOwnerID,
		job.OwnerID, job.OwnerGeneration, ts(job.OwnerRenewedAt),
		ts(job.OwnerExpiresAt), job.IntentJSON, job.SpecFingerprint, job.Profile,
		job.ExecutablePath, job.ExecutableSHA256, job.EnvironmentSHA256,
		job.WorkingDirectory, job.StdinPolicy, job.Network, job.Credentials,
		job.TimeoutMilliseconds, job.InlineLimitBytes, job.ArtifactLimitBytes,
		job.State, job.PID, job.ProcessGroup, job.Stdout, job.Stderr,
		job.StdoutObservedBytes, job.StderrObservedBytes, job.OutputCursor,
		job.OutputBaseCursor, job.OutputFramesJSON, job.StdoutSHA256,
		job.StderrSHA256, job.TruncationReason, nullableInt(job.ExitCode),
		boolInt(job.TimedOut), boolInt(job.Cancelled), boolInt(job.Killed),
		boolInt(job.TreeReaped), boolInt(job.JobAssignedAtCreation),
		boolInt(job.StdinClosed), job.StdinWriteCount, job.Version,
		ts(job.CreatedAt), nullableTS(job.StartedAt), nullableTS(job.CompletedAt),
		ts(job.UpdatedAt), job.Adapter.Kind, job.Adapter.Backend,
		job.Adapter.BackendIdentity, job.Adapter.Generation,
		job.Adapter.IsolationGrade, job.Adapter.NetworkPolicy,
		job.Adapter.CredentialPolicy,
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	if _, err := state.db.ExecContext(context.Background(),
		`INSERT INTO command_runtime_jobs (protocol_version, `+columns+
			`) VALUES (`+placeholders+`)`, values...); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(context.Background(),
		`INSERT INTO command_runtime_job_agents
		(job_id, run_id, agent_id, agent_attempt_id, attribution_source, created_at)
		VALUES (?, ?, ?, NULL, 'legacy_root', ?)`, job.ID, job.RunID,
		job.RootAgentID, ts(job.CreatedAt)); err != nil {
		t.Fatal(err)
	}
}

func cloneV163JobWithChanges(ctx context.Context, state *SQLiteStore,
	sourceID string, changes map[string]any,
) error {
	rows, err := state.db.QueryContext(ctx, `PRAGMA table_info(command_runtime_jobs)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var columns, expressions []string
	var arguments []any
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull,
			&defaultValue, &primaryKey); err != nil {
			return err
		}
		columns = append(columns, name)
		if value, changed := changes[name]; changed {
			expressions = append(expressions, "?")
			arguments = append(arguments, value)
		} else {
			expressions = append(expressions, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	arguments = append(arguments, sourceID)
	_, err = state.db.ExecContext(ctx, `INSERT INTO command_runtime_jobs (`+
		strings.Join(columns, ", ")+`) SELECT `+strings.Join(expressions, ", ")+
		` FROM command_runtime_jobs WHERE id = ?`, arguments...)
	return err
}

func TestSchemaV163HostNetworkCleanInstallAndLegacyUpgrade(t *testing.T) {
	for _, version := range []int{0, 161, 162} {
		t.Run(fmt.Sprintf("from-v%d", version), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "command-runtime-v163.db")
			if version != 0 {
				legacy := openUnmigratedSQLiteStore(t, path)
				if err := applyMigrationPrefixForTest(ctx, legacy, migrationPlan(), version); err != nil {
					t.Fatal(err)
				}
				if err := legacy.Close(); err != nil {
					t.Fatal(err)
				}
			}
			state, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			var schemaVersion int
			if err := state.db.QueryRowContext(ctx,
				`SELECT MAX(version) FROM schema_migrations`).Scan(&schemaVersion); err != nil ||
				schemaVersion != LatestSchemaVersion {
				t.Fatalf("schema version=%d err=%v", schemaVersion, err)
			}
			job := commandRuntimeMigrationJob(t, state,
				domain.RunExecutionPermissionFullAccess,
				commandruntimeadapter.HostUnsandboxed(strings.Repeat("a", 64)))
			job.Network = runner.CommandRuntimeNetworkHost
			job.PermissionRuntimeEpoch = "permission-runtime-v163"
			job.PermissionGeneration = 2
			prepared, replayed, err := state.PrepareCommandRuntimeJob(ctx, job)
			if err != nil || replayed || prepared.Network != runner.CommandRuntimeNetworkHost ||
				prepared.PermissionRuntimeEpoch != job.PermissionRuntimeEpoch ||
				prepared.PermissionGeneration != job.PermissionGeneration {
				t.Fatalf("host job=%#v replayed=%t err=%v", prepared, replayed, err)
			}
			if replay, replayed, err := state.PrepareCommandRuntimeJob(ctx, job); err != nil || !replayed || replay.ID != job.ID {
				t.Fatalf("host replay=%#v replayed=%t err=%v", replay, replayed, err)
			}
			stale := job
			stale.PermissionGeneration++
			if _, _, err := state.PrepareCommandRuntimeJob(ctx, stale); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("changed grant replay error=%v", err)
			}
			var actorCount int
			if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*)
				FROM command_runtime_job_agents WHERE job_id = ?`, job.ID).
				Scan(&actorCount); err != nil || actorCount != 1 {
				t.Fatalf("actor rows=%d err=%v", actorCount, err)
			}
			if _, err := state.db.ExecContext(ctx,
				`UPDATE command_runtime_jobs SET permission_generation = 3 WHERE id = ?`,
				job.ID); err == nil {
				t.Fatal("grant provenance could be changed after insert")
			}
			for name, adapterChanges := range map[string]map[string]any{
				"legacy": {
					"adapter_kind":              "legacy_unbound",
					"adapter_backend":           "legacy_unbound",
					"adapter_backend_identity":  "legacy_unbound",
					"adapter_generation":        "legacy_unbound",
					"adapter_isolation_grade":   "legacy_unknown",
					"adapter_network_policy":    "legacy_unknown",
					"adapter_credential_policy": "legacy_unknown",
				},
				"workspace": {
					"permission_mode":           "workspace_access",
					"adapter_kind":              "sandboxed_workspace",
					"adapter_backend":           "local_windows_lpac",
					"adapter_backend_identity":  "local-windows-lpac.v1",
					"adapter_isolation_grade":   "workspace_sandbox",
					"adapter_network_policy":    "denied",
					"adapter_credential_policy": "none",
				},
			} {
				changes := map[string]any{
					"id":                  "command-job-v163-" + name,
					"operation_digest":    testCommandRuntimeDigest("v163-" + name),
					"request_fingerprint": testCommandRuntimeDigest("v163-request-" + name),
					"invocation_id":       "invocation-v163-" + name,
				}
				for key, value := range adapterChanges {
					changes[key] = value
				}
				if err := cloneV163JobWithChanges(ctx, state, job.ID, changes); err == nil {
					t.Fatalf("%s adapter inserted host network intent", name)
				}
			}
			assertNoForeignKeyViolations(t, state.db)
		})
	}
}
