package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/gitmutation"
)

func TestRemoteOperationStartedClaimAndV159MigrationPreserveHistory(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "v159.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	workspace := WorkspaceRecord{ID: "ws-pr-started", Name: "fixture", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err = st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{Goal: "started migration fixture", Profile: "code", WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TRIGGER trg_git_remote_started_once`, `DROP TRIGGER trg_git_mutation_started_once`, `ALTER TABLE git_remote_operations DROP COLUMN started_at`, `ALTER TABLE git_mutation_operations DROP COLUMN started_at`, `DELETE FROM schema_migrations WHERE version=159`} {
		if _, err = st.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	created := time.Now().UTC().Add(-time.Minute)
	completed := created.Add(time.Second)
	_, err = st.db.ExecContext(ctx, `INSERT INTO git_remote_operations(id,protocol_version,operation_key_digest,request_fingerprint,run_id,workspace_id,operation,spec_json,remote_host,protocol,branch,pre_head,pull_request_number,pull_request_url,completed_at,created_at) VALUES(?,'repository_remote.v1',?,?,?,?,'create_pr','{}','github.com','https','feature',?,17,'https://github.com/acme/widget/pull/17',?,?)`, "old-pr", strings.Repeat("a", 64), strings.Repeat("b", 64), run.ID, workspace.ID, strings.Repeat("1", 40), ts(completed), ts(created))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old, found, err := st.GetRemoteOperation(ctx, "old-pr")
	if err != nil || !found || old.StartedAt != nil || old.CompletedAt == nil || old.PullRequestNumber != 17 || old.SpecJSON != "{}" {
		t.Fatalf("historical receipt changed: %#v %v", old, err)
	}
	if _, claimed, err := st.StartRemoteOperation(ctx, old.ID, old.RequestFingerprint, time.Now().UTC()); err != nil || claimed {
		t.Fatalf("completed legacy operation reclaimed: %v %v", claimed, err)
	}
	row := gitmutation.RemoteRecord{ID: "new-pr", ProtocolVersion: "repository_remote.v1", OperationKeyDigest: strings.Repeat("c", 64), RequestFingerprint: strings.Repeat("d", 64), RunID: run.ID, WorkspaceID: workspace.ID, Operation: gitmutation.RemoteCreatePR, SpecJSON: `{"explicit_fixture":true}`, RemoteHost: "github.com", RemotePort: "443", Protocol: "https", Branch: "feature", PreHead: strings.Repeat("1", 40), CreatedAt: time.Now().UTC()}
	if _, _, err = st.CreateRemoteOperation(ctx, row); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := st.StartRemoteOperation(ctx, row.ID, strings.Repeat("e", 64), time.Now().UTC()); err == nil || claimed {
		t.Fatal("different intent claimed")
	}
	started, claimed, err := st.StartRemoteOperation(ctx, row.ID, row.RequestFingerprint, time.Now().UTC())
	if err != nil || !claimed || started.StartedAt == nil {
		t.Fatalf("first claim %#v %v", started, err)
	}
	if _, again, err := st.StartRemoteOperation(ctx, row.ID, row.RequestFingerprint, time.Now().UTC()); err != nil || again {
		t.Fatalf("second claim %v %v", again, err)
	}
	for _, q := range []string{`UPDATE git_remote_operations SET started_at=NULL WHERE id='new-pr'`, `UPDATE git_remote_operations SET started_at='2026-09-11T00:00:00Z' WHERE id='new-pr'`} {
		if _, err = st.db.ExecContext(ctx, q); err == nil {
			t.Fatal("immutable execution claim changed")
		}
	}
	wrong := row
	wrong.ID = "wrong-thread-pr"
	wrong.OperationKeyDigest = strings.Repeat("f", 64)
	intent, _ := json.Marshal(map[string]any{"version": "thread_pull_request.v1", "operation_id": wrong.ID, "thread_id": "different-thread", "run_id": run.ID, "session_id": run.SessionID, "workspace_id": workspace.ID, "source_workspace_id": workspace.ID, "approval_fingerprint": wrong.RequestFingerprint, "draft_only": true})
	wrong.SpecJSON = string(intent)
	if _, _, err = st.CreateRemoteOperation(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	_, err = st.EnsureApproval(ctx, approval.Proposal{IdempotencyKey: "wrong-thread-approval", ProposalID: wrong.ID, SessionID: run.SessionID, WorkspaceID: workspace.ID, ToolName: "github.pull_request", ActionClass: "github_pull_request_create", Mode: "per_call", Status: approval.StatusPending, RequestFingerprint: wrong.RequestFingerprint})
	if err == nil || !strings.Contains(err.Error(), "exact task origin") {
		t.Fatalf("cross-task source accepted or rejected for the wrong reason: %v", err)
	}
	if version, err := st.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("migration %d %v", version, err)
	}
	var failures int
	if err = st.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&failures); err != nil || failures != 0 {
		t.Fatalf("FK %d %v", failures, err)
	}
}
