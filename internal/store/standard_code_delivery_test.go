package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/standardcodedelivery"
)

func TestStandardCodeDeliveryLedgerSealsReplaysAndRejectsMutation(t *testing.T) {
	ctx := context.Background()
	state, runRecord, mission, _ := newWorkspaceCheckpointStoreFixture(t)
	defer state.Close()
	executor, err := repository.NewDrydockExecutor(filepath.Join(t.TempDir(), "managed"))
	if err != nil {
		t.Fatal(err)
	}
	drydocks, err := application.NewDrydockService(state, executor)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := application.NewWorkspaceCheckpointService(state,
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	drydocks.WithCheckpointService(checkpoints)
	preview, err := drydocks.Create(ctx, application.DrydockCreateRequest{
		RunID: runRecord.ID, OperationKey: "delivery-ledger-preview", RequestedBy: "operator"})
	if err != nil || !preview.TrustRequired {
		t.Fatalf("Drydock preview=%+v err=%v", preview, err)
	}
	created, err := drydocks.Create(ctx, application.DrydockCreateRequest{
		RunID: runRecord.ID, OperationKey: "delivery-ledger-create", RequestedBy: "operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest})
	if err != nil || created.Workspace == nil || created.Checkpoint == nil {
		t.Fatalf("Drydock create=%+v err=%v", created, err)
	}
	workspace := *created.Workspace
	checkpoint := *created.Checkpoint
	evidence, err := executor.CaptureDelivery(ctx, workspace.Path, workspace.BaseCommit)
	if err != nil {
		t.Fatal(err)
	}
	revision := standardcodedelivery.RevisionSHA256(checkpoint.ManifestSHA256,
		checkpoint.IndexSHA256, checkpoint.RootFingerprint, checkpoint.RootPathSHA256,
		checkpoint.BaseCommit, checkpoint.Branch)
	now := time.Now().UTC()
	exit := 0
	report := standardcodedelivery.Report{ID: "standard-code-delivery-ledger",
		ProtocolVersion:    standardcodedelivery.ProtocolVersion,
		OperationKeySHA256: standardcodedelivery.Hash("ledger-operation"),
		RequestFingerprint: standardcodedelivery.Hash("ledger-request"),
		Status:             standardcodedelivery.StatusPassed, ReceiptStatus: standardcodedelivery.StatusPassed,
		Verified: true, Binding: standardcodedelivery.Binding{RunID: runRecord.ID,
			MissionID: mission.ID, SessionID: runRecord.SessionID,
			SourceWorkspaceID: mission.WorkspaceID, DrydockWorkspaceID: workspace.WorkspaceID,
			DrydockID: workspace.ID, DrydockGeneration: workspace.Generation,
			PresetOperationSHA256: standardcodedelivery.Hash("preset"),
			PermissionSnapshotID:  "permission-ledger", PermissionRevision: 1,
			Backend: "local", BackendGenerationSHA256: standardcodedelivery.Hash("backend"),
			CapabilityGenerationSHA256: standardcodedelivery.Hash("capability")},
		BaseCommit: workspace.BaseCommit, HeadCommit: evidence.HeadCommit,
		Diff: standardcodedelivery.Diff{SHA256: standardcodedelivery.HashBytes([]byte(evidence.Patch)),
			Bytes: len([]byte(evidence.Patch)), Files: []standardcodedelivery.ChangedFile{}},
		FinalCheckpoint: standardcodedelivery.Checkpoint{ID: checkpoint.ID,
			ManifestSHA256: checkpoint.ManifestSHA256, IndexSHA256: checkpoint.IndexSHA256,
			RootFingerprint: checkpoint.RootFingerprint, RootPathSHA256: checkpoint.RootPathSHA256,
			HeadCommit: checkpoint.BaseCommit, BranchSHA256: standardcodedelivery.Hash(checkpoint.Branch),
			RevisionSHA256: revision, RecoveryLevel: string(checkpoint.RecoveryLevel),
			IncompleteReasonSHA256: []string{}, CreatedAt: checkpoint.CreatedAt},
		Verifications: []standardcodedelivery.Verification{{JobID: "verification-ledger",
			Conclusion: standardcodedelivery.StatusPassed,
			ReasonCode: standardcodedelivery.ReasonPassed, State: "completed", ExitCode: &exit,
			SpecSHA256:        standardcodedelivery.Hash("spec"),
			ExecutableSHA256:  standardcodedelivery.Hash("executable"),
			EnvironmentSHA256: standardcodedelivery.Hash("environment"), PermissionRevision: 1,
			Backend: "local", BackendGenerationSHA256: standardcodedelivery.Hash("backend"),
			CheckpointID: checkpoint.ID, RevisionSHA256: revision, CurrentRevision: true,
			StdoutSHA256: standardcodedelivery.Hash(""), StderrSHA256: standardcodedelivery.Hash(""),
			TreeReaped: true, StartedAt: &now, CompletedAt: &now,
			Artifacts: []standardcodedelivery.Artifact{}}},
		UncoveredItems: []standardcodedelivery.UncoveredItem{},
		Links: standardcodedelivery.Links{
			Self:               "/api/v1/runs/" + runRecord.ID + "/standard-code-delivery",
			Checkpoint:         "/api/v1/runs/" + runRecord.ID + "/workspace-checkpoints?checkpoint_id=" + checkpoint.ID,
			CheckpointTimeline: "/api/v1/runs/" + runRecord.ID + "/workspace-checkpoints",
			Undo:               "/api/v1/runs/" + runRecord.ID + "/workspace-checkpoints/undo",
			Rewind:             "/api/v1/runs/" + runRecord.ID + "/workspace-checkpoints/rewind",
			Fork:               "/api/v1/runs/" + runRecord.ID + "/workspace-checkpoints/fork"},
		Safeguards: standardcodedelivery.Safeguards{}, CreatedAt: now}
	report.Reasons = []standardcodedelivery.Reason{standardcodedelivery.ReasonFact(
		standardcodedelivery.ReasonPassed, revision, report.Diff.SHA256,
		report.RequestFingerprint)}

	stored, replayed, err := state.CreateStandardCodeDelivery(ctx, report)
	if err != nil || replayed || stored.EventSequence <= 0 || stored.ReceiptSHA256 == "" ||
		stored.FinalCheckpoint.RevisionSHA256 != revision ||
		stored.Diff.SHA256 != standardcodedelivery.HashBytes([]byte(evidence.Patch)) {
		t.Fatalf("stored=%+v replayed=%t err=%v", stored, replayed, err)
	}
	replayedReport, replayed, err := state.CreateStandardCodeDelivery(ctx, report)
	if err != nil || !replayed || replayedReport.ReceiptSHA256 != stored.ReceiptSHA256 ||
		replayedReport.EventSequence != stored.EventSequence {
		t.Fatalf("replay=%+v replayed=%t err=%v", replayedReport, replayed, err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE standard_code_deliveries
		SET receipt_status = 'failed' WHERE id = ?`, stored.ID); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("delivery update error=%v", err)
	}
	if _, err := state.db.ExecContext(ctx,
		`DELETE FROM standard_code_deliveries WHERE id = ?`, stored.ID); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("delivery delete error=%v", err)
	}
	latest, found, err := state.GetLatestStandardCodeDelivery(ctx, runRecord.ID)
	if err != nil || !found || latest.ReceiptSHA256 != stored.ReceiptSHA256 {
		t.Fatalf("latest=%+v found=%t err=%v", latest, found, err)
	}
}
