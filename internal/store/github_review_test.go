package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/githubreview"
)

func TestGitHubReviewListJSONBound(t *testing.T) {
	remaining, err := addGitHubReviewListJSONBytes(maxGitHubReviewListJSONBytes-1, 1)
	if err != nil || remaining != maxGitHubReviewListJSONBytes {
		t.Fatalf("exact aggregate bound: total=%d err=%v", remaining, err)
	}
	if _, err := addGitHubReviewListJSONBytes(remaining, 1); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("overflow code=%s want=%s err=%v", apperror.CodeOf(err),
			apperror.CodeResourceExhausted, err)
	}
}

// removeSchemaV124ForTestStatements restores a v123 database. Historical
// downgrade fixtures form one cumulative chain, so it first removes the newer
// v125 compatibility migration.
func removeSchemaV124ForTestStatements() []string {
	return append(removeSchemaV125ForTestStatements(), []string{
		`DROP TABLE github_review_write_operations`,
		`DROP TABLE github_review_evidence_graphs`,
		`DROP TABLE github_review_snapshots`,
		`DROP TABLE github_review_connections`,
		`DELETE FROM run_supervisor_tool_calls
			WHERE tool_name IN ('github_review_evidence_list', 'github_review_evidence_read')`,
		`DROP TRIGGER trg_supervisor_tool_call_model_attempt`,
		`DROP TRIGGER trg_supervisor_tool_round_completion`,
		`DROP INDEX idx_run_supervisor_tool_calls_pending`,
		`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v124`,
		`CREATE TABLE run_supervisor_tool_calls (
			run_id TEXT NOT NULL,
			turn INTEGER NOT NULL,
			attempt_id TEXT NOT NULL,
			round INTEGER NOT NULL,
			position INTEGER NOT NULL,
			model_attempt INTEGER NOT NULL,
			call_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			authority_json TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			result_json TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			completed_at TEXT,
			PRIMARY KEY(run_id, turn, attempt_id, round, position),
			UNIQUE(run_id, turn, attempt_id, call_id),
			FOREIGN KEY(run_id, turn, attempt_id, round)
				REFERENCES run_supervisor_tool_rounds(run_id, turn, attempt_id, round) ON DELETE CASCADE,
			CHECK(position BETWEEN 1 AND 4),
			CHECK(model_attempt > 0),
			CHECK(tool_name IN ('work_item_create', 'note_create',
				'specialist_delegation_propose', 'child_task_propose',
				'plan_delivery_propose', 'controlled_command_propose',
				'one_shot_command_propose', 'host_command_propose',
				'sandbox_docker_run_propose', 'skill_candidate_propose', 'debug_terminal',
				'workspace_list', 'workspace_read', 'workspace_glob', 'workspace_grep',
				'workspace_change', 'workspace_apply', 'workspace_delete', 'command_runtime',
				'mcp_tool_call')),
			CHECK((tool_name IN ('workspace_list', 'workspace_read', 'workspace_glob',
				'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
				AND length(authority_json) BETWEEN 2 AND 4096 AND json_valid(authority_json) = 1)
				OR (tool_name NOT IN ('workspace_list', 'workspace_read', 'workspace_glob',
					'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
					AND authority_json = '')),
			CHECK(status IN ('pending', 'completed', 'denied', 'failed')),
			CHECK((status = 'pending' AND result_json = '' AND error_code = '' AND completed_at IS NULL)
				OR (status = 'completed' AND length(result_json) > 0 AND error_code = '' AND completed_at IS NOT NULL)
				OR (status IN ('denied', 'failed') AND length(result_json) > 0 AND length(error_code) > 0
					AND completed_at IS NOT NULL))
		)`,
		`INSERT INTO run_supervisor_tool_calls
			(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
			payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
			SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
			payload_json, authority_json, status, result_json, error_code, created_at, completed_at
			FROM run_supervisor_tool_calls_v124`,
		`DROP TABLE run_supervisor_tool_calls_v124`,
		`CREATE INDEX idx_run_supervisor_tool_calls_pending
			ON run_supervisor_tool_calls(run_id, turn, attempt_id, status, round, position)`,
		`CREATE TRIGGER trg_supervisor_tool_call_model_attempt
			BEFORE INSERT ON run_supervisor_tool_calls
			WHEN NOT EXISTS (
				SELECT 1 FROM run_supervisor_tool_rounds
				WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
					AND round = NEW.round AND model_attempt = NEW.model_attempt
			)
			BEGIN SELECT RAISE(ABORT, 'supervisor tool call model attempt mismatch'); END`,
		`CREATE TRIGGER trg_supervisor_tool_round_completion
			BEFORE UPDATE OF completed_at ON run_supervisor_tool_rounds
			WHEN NEW.completed_at IS NOT NULL AND EXISTS (
				SELECT 1 FROM run_supervisor_tool_calls
				WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
					AND round = NEW.round AND status = 'pending'
			)
			BEGIN SELECT RAISE(ABORT, 'supervisor tool round still has pending calls'); END`,
		`DELETE FROM schema_migrations WHERE version = 124`,
	}...)
}

func TestGitHubReviewLedgerPersistsMetadataWithoutCredentialAndFencesWrites(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "github-review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if version, err := state.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	workspace := WorkspaceRecord{ID: "ws-github-review", Name: "github-review",
		RootPath: t.TempDir(), CreatedAt: now}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(ctx, application.CreateRunRequest{
		Goal: "review PR", Profile: "code", WorkspaceID: workspace.ID,
		Budget: domain.Budget{MaxTurns: 5, MaxToolCalls: 20}})
	if err != nil {
		t.Fatal(err)
	}
	repository, _ := githubreview.ParseRepository("acme/widget")
	connection := githubreview.Connection{ProtocolVersion: githubreview.ConnectionProtocolVersion,
		ID: "github-acme-widget", Repository: repository,
		Credential: githubreview.CredentialReference{Name: "github-review-token",
			Kind: githubreview.AuthFineGrainedPAT}, Network: githubreview.DefaultNetworkScope(),
		Enabled: true, Generation: 1, CreatedAt: now, UpdatedAt: now}
	storedConnection, replayed, err := state.PutGitHubReviewConnection(ctx, connection, 0)
	if err != nil || replayed || storedConnection.Generation != 1 {
		t.Fatalf("create GitHub review connection: %v replayed=%t %#v", err, replayed, storedConnection)
	}
	if _, replayed, err := state.PutGitHubReviewConnection(ctx, connection, 0); err != nil || !replayed {
		t.Fatalf("idempotent GitHub review connection: %v replayed=%t", err, replayed)
	}
	connection.Enabled = false
	connection.Generation = 2
	connection.UpdatedAt = now.Add(time.Minute)
	if _, replayed, err := state.PutGitHubReviewConnection(ctx, connection, 1); err != nil || replayed {
		t.Fatalf("update GitHub review connection: %v replayed=%t", err, replayed)
	}

	base, head, merge := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	capability := githubreview.CapabilitySnapshot{ProtocolVersion: githubreview.CapabilityProtocolVersion,
		Generation: strings.Repeat("a", 64), APIHost: "api.github.com",
		APIVersion: githubreview.RESTAPIVersion, AccountLogin: "octocat",
		Repository: repository, Credential: connection.Credential,
		Permissions: map[string]string{"metadata": "read", "pull_requests": "write"},
		Read:        true, Reply: true, Resolve: true, Review: true,
		RequestReviewer: true, CapturedAt: now}
	snapshot := githubreview.Snapshot{ProtocolVersion: githubreview.SnapshotProtocolVersion,
		Identity: githubreview.PullRequestIdentity{Repository: repository, Number: 7,
			NodeID: "PR_7", State: "open", BaseRef: "main", BaseSHA: base,
			HeadRef: "feature", HeadSHA: head, MergeBaseSHA: merge, UpdatedAt: now},
		Capability: capability, Title: githubreview.SanitizeRemoteText("title", 1024),
		Body: githubreview.SanitizeRemoteText("body", 1024), Author: "octocat",
		RequestedReviewers: []string{}, Files: []githubreview.ChangedFile{},
		Reviews: []githubreview.Review{}, Threads: []githubreview.ReviewThread{},
		LooseComments: []githubreview.Comment{}, CheckSuites: []githubreview.CheckSuite{},
		CheckRuns: []githubreview.CheckRun{}, Jobs: []githubreview.WorkflowJob{},
		Artifacts: []githubreview.ArtifactMetadata{}, Pagination: []githubreview.PageEvidence{},
		State: githubreview.EvidenceVerified, Omissions: []string{}, FetchedAt: now}
	snapshot.Finalize()
	if _, replayed, err := state.SaveGitHubReviewSnapshot(ctx, connection.ID, snapshot); err != nil || replayed {
		t.Fatalf("save GitHub review snapshot: %v replayed=%t", err, replayed)
	}
	if _, replayed, err := state.SaveGitHubReviewSnapshot(ctx, connection.ID, snapshot); err != nil || !replayed {
		t.Fatalf("replay GitHub review snapshot: %v replayed=%t", err, replayed)
	}
	graph := githubreview.EvidenceGraph{ProtocolVersion: githubreview.EvidenceProtocolVersion,
		SnapshotID: snapshot.ID, SnapshotFingerprint: snapshot.Fingerprint,
		Local: githubreview.LocalBinding{RepositorySHA256: strings.Repeat("4", 64),
			HeadSHA: head, MergeBaseSHA: merge, IndexSHA256: strings.Repeat("5", 64),
			WorktreeSHA256: strings.Repeat("6", 64), StatusSHA256: strings.Repeat("7", 64),
			FileSHA256: map[string]string{}, CapturedAt: now},
		Git: githubreview.GitEvidence{ProtocolVersion: "git-review-diff-evidence.v1",
			DiffSHA256: strings.Repeat("8", 64), CallChainSHA256: strings.Repeat("9", 64),
			ChangedFiles: []string{}, HunkIDs: []string{}, Omissions: []string{}, Complete: true},
		State: githubreview.EvidenceVerified, Mappings: []githubreview.PositionMapping{},
		Omissions: []string{}, Fingerprint: strings.Repeat("a", 64), CreatedAt: now}
	recordID := func(runID string) string {
		return "ghg-" + githubreview.Fingerprint("github-review-evidence-record",
			runID, workspace.ID, graph.Fingerprint)[:32]
	}
	firstEvidence := githubreview.EvidenceRecord{ID: recordID(run.ID), RunID: run.ID,
		WorkspaceID: workspace.ID, Graph: graph}
	if _, replayed, err := state.SaveGitHubReviewEvidence(ctx, firstEvidence); err != nil || replayed {
		t.Fatalf("save first Run evidence: %v replayed=%t", err, replayed)
	}
	_, secondRun, err := application.NewRunService(state).Create(ctx, application.CreateRunRequest{
		Goal: "review the same PR again", Profile: "code", WorkspaceID: workspace.ID,
		Budget: domain.Budget{MaxTurns: 5, MaxToolCalls: 20}})
	if err != nil {
		t.Fatal(err)
	}
	secondEvidence := githubreview.EvidenceRecord{ID: recordID(secondRun.ID), RunID: secondRun.ID,
		WorkspaceID: workspace.ID, Graph: graph}
	if _, replayed, err := state.SaveGitHubReviewEvidence(ctx, secondEvidence); err != nil || replayed {
		t.Fatalf("save second Run evidence: %v replayed=%t", err, replayed)
	}
	if listed, err := state.ListGitHubReviewEvidence(ctx, secondRun.ID, 10); err != nil ||
		len(listed) != 1 || listed[0].RunID != secondRun.ID || listed[0].ID == firstEvidence.ID {
		t.Fatalf("evidence was not isolated by Run: %v %#v", err, listed)
	}

	spec := githubreview.WriteSpec{ProtocolVersion: githubreview.WriteProtocolVersion,
		Operation: githubreview.WriteReply, Identity: snapshot.Identity,
		Credential: connection.Credential, CapabilityGeneration: capability.Generation,
		TargetID: "thread_7", Body: "fixed", Reviewers: []string{},
		LocalChangeSummary: "one hunk", ValidationSummary: "focused checks passed"}
	preview, err := githubreview.NewWritePreview(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	record := githubreview.WriteRecord{ID: preview.ID,
		ProtocolVersion:     githubreview.WriteProtocolVersion,
		OperationKeySHA256:  githubreview.Fingerprint("operation-key", "write-1"),
		RequestFingerprint:  githubreview.Fingerprint("request", preview.ID),
		ApprovalFingerprint: preview.ApprovalFingerprint,
		RunID:               run.ID, SessionID: run.SessionID, WorkspaceID: workspace.ID,
		ConnectionID: connection.ID, Preview: preview, Spec: spec,
		Status: githubreview.OperationProposed, CreatedAt: now}
	created, replayed, err := state.CreateGitHubReviewWrite(ctx, record)
	if err != nil || replayed || created.Status != githubreview.OperationProposed {
		t.Fatalf("create GitHub review write: %v replayed=%t %#v", err, replayed, created)
	}
	driftedSpec := spec
	driftedSpec.ValidationSummary = "different reviewed validation"
	driftedPreview, err := githubreview.NewWritePreview(driftedSpec, now)
	if err != nil {
		t.Fatal(err)
	}
	driftedRecord := record
	driftedRecord.Spec = driftedSpec
	driftedRecord.Preview = driftedPreview
	driftedRecord.ApprovalFingerprint = driftedPreview.ApprovalFingerprint
	if _, _, err := state.CreateGitHubReviewWrite(ctx, driftedRecord); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operation key accepted a changed approval summary: %v", err)
	}
	approvalRecord, err := state.EnsureApproval(ctx, approval.Proposal{
		IdempotencyKey: approval.ProposalIdempotencyKey("github.review", record.ID),
		ProposalID:     record.ID, SessionID: run.SessionID, WorkspaceID: workspace.ID,
		ToolName: githubreview.ApprovalToolName, ActionClass: githubreview.ApprovalActionClass, Mode: "per_call",
		Status: approval.StatusPending, RequestFingerprint: preview.ApprovalFingerprint,
		RequestedBy: "operator", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := state.DecideApproval(ctx, approval.DecisionRequest{
		ProposalID:     record.ID,
		IdempotencyKey: approval.ReviewIdempotencyKey("github.review", record.ID, approval.ActionApprove),
		Action:         approval.ActionApprove, ReviewedBy: "operator"})
	if err != nil || decision.Approval.Status != approval.StatusApproved {
		t.Fatalf("approve GitHub review write: %v %#v", err, decision)
	}
	started, replayed, err := state.StartGitHubReviewWrite(ctx, record.ID,
		approvalRecord.ID, preview.ApprovalFingerprint, now.Add(time.Minute))
	if err != nil || replayed || started.Status != githubreview.OperationRunning {
		t.Fatalf("start GitHub review write: %v replayed=%t %#v", err, replayed, started)
	}
	if _, replayed, err := state.StartGitHubReviewWrite(ctx, record.ID,
		approvalRecord.ID, preview.ApprovalFingerprint, now.Add(time.Minute)); err != nil || !replayed {
		t.Fatalf("concurrent GitHub review start was not fenced: %v replayed=%t", err, replayed)
	}
	receipt := githubreview.WriteReceipt{ProtocolVersion: githubreview.ReceiptProtocolVersion,
		ID:        "ghr-" + githubreview.Fingerprint("receipt", preview.ID)[:32],
		PreviewID: preview.ID, Operation: preview.Operation,
		Status: githubreview.ReceiptSucceeded, Identity: preview.Identity,
		TargetID: preview.TargetID, ResultID: "comment_8",
		IdempotencyMarker: preview.IdempotencyMarker,
		StartedAt:         started.StartedAt, CompletedAt: now.Add(2 * time.Minute)}
	completed, replayed, err := state.CompleteGitHubReviewWrite(ctx, record.ID,
		receipt, receipt.CompletedAt)
	if err != nil || replayed || completed.Status != githubreview.OperationSucceeded {
		t.Fatalf("complete GitHub review write: %v replayed=%t %#v", err, replayed, completed)
	}
	if _, replayed, err := state.CompleteGitHubReviewWrite(ctx, record.ID,
		receipt, receipt.CompletedAt); err != nil || !replayed {
		t.Fatalf("terminal GitHub review receipt was not replayed: %v replayed=%t", err, replayed)
	}

	var connectionJSON, snapshotJSON, specJSON string
	if err := state.db.QueryRow(`SELECT network_json FROM github_review_connections WHERE id = ?`,
		connection.ID).Scan(&connectionJSON); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRow(`SELECT snapshot_json FROM github_review_snapshots WHERE id = ?`,
		snapshot.ID).Scan(&snapshotJSON); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRow(`SELECT spec_json FROM github_review_write_operations WHERE id = ?`,
		record.ID).Scan(&specJSON); err != nil {
		t.Fatal(err)
	}
	for _, stored := range []string{connectionJSON, snapshotJSON, specJSON} {
		if strings.Contains(stored, "github_pat_") || strings.Contains(stored, "ghu_") {
			t.Fatal("GitHub review ledger persisted a credential value")
		}
	}
}
