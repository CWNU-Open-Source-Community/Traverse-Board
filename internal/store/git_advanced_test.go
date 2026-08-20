package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/repository"
)

// removeSchemaV123ForTestStatements restores a v122 database. Historical
// downgrade fixtures form one cumulative chain, so every older migration test
// must remove the newest Git advanced objects before deleting its target row.
func removeSchemaV123ForTestStatements() []string {
	return []string{
		`DROP TABLE git_managed_worktrees`,
		`DROP TABLE git_advanced_sequences`,
		`DROP TABLE git_advanced_operations`,
		`DELETE FROM schema_migrations WHERE version = 123`,
	}
}

func TestSchemaV123UpgradesV122Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "git-advanced-v122.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV123ForTestStatements() {
		if _, err := state.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("downgrade v123 with %q: %v", statement, err)
		}
	}
	if version, err := state.SchemaVersion(t.Context()); err != nil || version != 122 {
		t.Fatalf("downgraded schema version=%d want=122 err=%v", version, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(t.Context()); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	for _, table := range []string{
		"git_advanced_operations", "git_advanced_sequences", "git_managed_worktrees",
	} {
		assertTableCount(t, upgraded, table, 0)
	}
}

func TestGitAdvancedStoreKeepsAuditImmutableAndFencesDurableState(t *testing.T) {
	state, runRecord, mission, root := newWorkspaceCheckpointStoreFixture(t)
	t.Cleanup(func() { _ = state.Close() })
	ctx := context.Background()
	executor, err := repository.NewAdvancedExecutor(filepath.Join(t.TempDir(), "managed"), true)
	if err != nil {
		t.Fatal(err)
	}
	spec := gitadvanced.Spec{ProtocolVersion: gitadvanced.ProtocolVersion,
		Operation: gitadvanced.StashCreate, Message: "store fixture stash"}
	preview, err := executor.ReviewAdvanced(ctx, root, spec)
	if err != nil {
		t.Fatal(err)
	}
	preview.PermissionSnapshotID = "permission-store-1"
	preview.PermissionRevision = 1
	preview.LeaseGeneration = 1
	specJSON, _ := json.Marshal(spec)
	previewJSON, _ := json.Marshal(preview)
	now := time.Now().UTC().Truncate(time.Microsecond)
	record := gitadvanced.OperationRecord{ID: "git-advanced-store-1",
		ProtocolVersion:    gitadvanced.ProtocolVersion,
		OperationKeySHA256: gitadvanced.Fingerprint("store-operation-key"),
		RequestFingerprint: gitadvanced.Fingerprint("store-request"),
		PreviewID:          preview.ID, ApprovalFingerprint: preview.ApprovalFingerprint,
		RunID: runRecord.ID, SessionID: runRecord.SessionID, WorkspaceID: mission.WorkspaceID,
		Operation: spec.Operation, SpecJSON: string(specJSON), PreviewJSON: string(previewJSON),
		RepositorySHA256:     preview.Binding.RepositorySHA256,
		CommonDirSHA256:      preview.Binding.CommonDirSHA256,
		PermissionSnapshotID: "permission-store-1", PermissionRevision: 1,
		CapabilityGeneration: preview.Capability.Generation,
		LeaseID:              "lease-store-1", LeaseGeneration: 1,
		Status: gitadvanced.OperationProposed, ReceiptJSON: "{}", CreatedAt: now}
	tamperedRecord := record
	tamperedPreview := preview
	tamperedPreview.Spec.Message = "different unaudited stash intent"
	tamperedJSON, err := json.Marshal(tamperedPreview)
	if err != nil {
		t.Fatal(err)
	}
	tamperedRecord.PreviewJSON = string(tamperedJSON)
	if _, _, err := state.CreateGitAdvancedOperation(ctx, tamperedRecord); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeInvalidArgument {
		t.Fatalf("mismatched stored spec was accepted: %v", err)
	}
	stored, replayed, err := state.CreateGitAdvancedOperation(ctx, record)
	if err != nil || replayed || stored.ID != record.ID {
		t.Fatalf("create=%#v replayed=%t err=%v", stored, replayed, err)
	}
	if _, err := state.db.ExecContext(ctx,
		`UPDATE git_advanced_operations SET preview_json = '{}' WHERE id = ?`, record.ID); err == nil {
		t.Fatal("immutable Git advanced preview was rewritten")
	}
	if _, err := state.db.ExecContext(ctx,
		`DELETE FROM git_advanced_operations WHERE id = ?`, record.ID); err == nil {
		t.Fatal("immutable Git advanced operation was deleted")
	}
	var eventCount int
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events
		WHERE run_id = ? AND type = ? AND subject_id = ?`, runRecord.ID,
		events.GitAdvancedProposedEvent, record.ID).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("proposed audit events=%d err=%v", eventCount, err)
	}

	secondRecord := record
	secondRecord.ID = "git-advanced-store-2"
	secondRecord.OperationKeySHA256 = gitadvanced.Fingerprint("store-operation-key-2")
	secondRecord.RequestFingerprint = gitadvanced.Fingerprint("store-request-2")
	if _, replayed, err := state.CreateGitAdvancedOperation(ctx, secondRecord); err != nil || replayed {
		t.Fatalf("second operation create replayed=%t err=%v", replayed, err)
	}
	approve := func(operation gitadvanced.OperationRecord, key string) approval.Record {
		t.Helper()
		value, err := state.EnsureApproval(ctx, approval.Proposal{
			IdempotencyKey: key, ProposalID: operation.ID,
			SessionID: operation.SessionID, WorkspaceID: operation.WorkspaceID,
			ToolName:    gitadvanced.ApprovalToolName,
			ActionClass: gitadvanced.ApprovalActionClass, Mode: "per_call",
			Status: approval.StatusPending, RequestFingerprint: operation.ApprovalFingerprint,
			RequestedBy: "store-test", CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		decision, err := state.DecideApproval(ctx, approval.DecisionRequest{
			ProposalID: operation.ID, IdempotencyKey: key + "-decision",
			Action: approval.ActionApprove, ReviewedBy: "store-test"})
		if err != nil || decision.Approval.ID != value.ID {
			t.Fatalf("approve Git operation: %v %#v", err, decision)
		}
		return decision.Approval
	}
	firstApproval := approve(record, "git-advanced-store-approval-1")
	secondApproval := approve(secondRecord, "git-advanced-store-approval-2")
	if _, replayed, err := state.StartGitAdvancedOperation(ctx, record.ID,
		firstApproval.ID, record.ApprovalFingerprint, now.Add(time.Second)); err != nil || replayed {
		t.Fatalf("first common-dir owner start replayed=%t err=%v", replayed, err)
	}
	if _, _, err := state.StartGitAdvancedOperation(ctx, secondRecord.ID,
		secondApproval.ID, secondRecord.ApprovalFingerprint,
		now.Add(2*time.Second)); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatalf("second common-dir owner was not fenced: %v", err)
	}

	sequence := gitadvanced.Sequence{ID: "git-sequence-store-1",
		ProtocolVersion: gitadvanced.SequenceProtocolVersion,
		RunID:           runRecord.ID, WorkspaceID: mission.WorkspaceID,
		Kind: gitadvanced.SequenceRebase, Status: gitadvanced.SequenceActive,
		RepositorySHA256: preview.Binding.RepositorySHA256,
		OriginalHead:     preview.Binding.Head, OriginalBranch: preview.Binding.Branch,
		TargetJSON: string(specJSON), SequencerSHA256: preview.Binding.SequenceSHA256,
		CurrentHead: preview.Binding.Head, ConflictJSON: "{}", Generation: 1,
		StartedOperationID: record.ID, LastOperationID: record.ID,
		CreatedAt: now, UpdatedAt: now}
	if _, replayed, err := state.CreateGitAdvancedSequence(ctx, sequence); err != nil || replayed {
		t.Fatalf("sequence create replayed=%t err=%v", replayed, err)
	}
	sequence.Generation = 10
	sequence.LastOperationID = record.ID
	sequence.UpdatedAt = now.Add(time.Second)
	if _, _, err := state.AdvanceGitAdvancedSequence(ctx, sequence, 9); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatalf("stale sequence CAS error=%v", err)
	}
	sequence.Generation = 2
	advanced, replayed, err := state.AdvanceGitAdvancedSequence(ctx, sequence, 1)
	if err != nil || replayed || advanced.Generation != 2 {
		t.Fatalf("sequence advance=%#v replayed=%t err=%v", advanced, replayed, err)
	}

	managedPath := filepath.Join(t.TempDir(), "managed", "review")
	worktree := gitadvanced.ManagedWorktree{ID: "git-worktree-store-1",
		ProtocolVersion: gitadvanced.WorktreeProtocolVersion,
		RunID:           runRecord.ID, WorkspaceID: mission.WorkspaceID,
		RepositorySHA256: preview.Binding.RepositorySHA256,
		CommonDirSHA256:  preview.Binding.CommonDirSHA256, Name: "review",
		Path: managedPath, PathSHA256: gitadvanced.Fingerprint("worktree-path", managedPath),
		Branch: "review/branch", Head: preview.Binding.Head, Present: true, Generation: 1,
		CreatedOperationID: record.ID, LastOperationID: record.ID,
		CreatedAt: now, UpdatedAt: now}
	if _, replayed, err := state.CreateManagedGitWorktree(ctx, worktree); err != nil || replayed {
		t.Fatalf("worktree create replayed=%t err=%v", replayed, err)
	}
	worktree.ID = "git-worktree-store-2"
	worktree.Path += "-other"
	worktree.PathSHA256 = gitadvanced.Fingerprint("worktree-path", worktree.Path)
	if _, _, err := state.CreateManagedGitWorktree(ctx, worktree); err == nil {
		t.Fatal("duplicate managed worktree name was not fenced")
	}
}
