package application

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
)

// Real SQLite/Git; the inherited fixture's Job is explicitly seeded durable
// test data, not a claim that an OS command ran in this test.
func TestThreadStandardCodeContinuationPreservesCurrentPermissionAndRequiresProvenance(t *testing.T) {
	f := newStandardCodeDeliveryReplayFixture(t, 0)
	ctx := t.Context()
	st := f.base.state
	if _, err := st.FailSupervisorTurn(ctx, f.machine.turn.Checkpoint, "end fixture attempt before next epoch", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ReleaseRunExecutionLease(ctx, f.lease); err != nil {
		t.Fatal(err)
	}
	proposalService := NewFileEditProposalService(st, policy.NewDefaultChecker()).WithDrydock(f.base.service)
	source, err := proposalService.IssueSource(ctx, f.base.run.ID, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := proposalService.Propose(ctx, CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion, RunID: f.base.run.ID, SourceHandle: source.Handle, ProposedText: "unapplied previous proposal\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileEditReviewService(st).WithDrydock(f.base.service).Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion, RunID: f.base.run.ID, EditID: proposal.Edit.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	oldApproval, err := st.GetApprovalByProposal(ctx, proposal.Edit.ID)
	if err != nil || oldApproval.Status != approval.StatusApproved {
		t.Fatalf("source approval missing: %#v %v", oldApproval, err)
	}
	thread, err := st.GetThreadByRun(ctx, f.base.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewThreadExecutionPermissionService(st, f.capabilities).Change(ctx, ChangeThreadExecutionPermissionRequest{ThreadID: thread.ID, Mode: "conservative", OperationKey: "carry-current-conservative", RequestedBy: "operator", Reason: "Current Thread permission must remain conservative"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(st).Fail(ctx, f.base.run.ID, "retire fixture execution"); err != nil {
		t.Fatal(err)
	}
	request := SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID, Content: "Continue in the current coding workspace", OperationKey: "next-conservative-code-turn", RequestedBy: "operator"}
	result, err := NewThreadServiceWithExecutionCapabilities(st, f.capabilities).WithDrydock(f.base.service).Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	preset, found, err := st.GetConfiguredStandardCodePresetOperation(ctx, result.Run.ID)
	if err != nil || !found || preset.SelectedBackend != domain.StandardCodeSelectedLocal || preset.RequestedBy != "thread_continuation" || preset.CapabilityGrant {
		t.Fatalf("new preset lost exact source/backend: %#v %v", preset, err)
	}
	permission, err := st.GetRunExecutionPermission(ctx, result.Run.ID)
	if err != nil || permission.Mode != domain.RunExecutionPermissionConservative || permission.CapabilityGrant || permission.ProcessEnabled || permission.ExecutionAuthorized {
		t.Fatalf("continuation raised permission: %#v %v", permission, err)
	}
	profile, err := st.GetRunExecutionProfile(ctx, result.Run.ID)
	if err != nil || profile.Profile != domain.RunExecutionProfileLocal {
		t.Fatalf("wrong backend: %#v %v", profile, err)
	}
	interaction, err := st.GetRunExecutionInteraction(ctx, result.Run.ID)
	if err != nil || interaction.Mode != domain.RunExecutionInteractionControlled || interaction.CapabilityGrant || interaction.ProcessEnabled || interaction.ExecutionAuthorized {
		t.Fatalf("interaction granted execution: %#v %v", interaction, err)
	}
	mode, err := st.GetRunMode(ctx, result.Run.ID)
	if err != nil || mode.Phase != domain.ExecutionPhaseDeliver {
		t.Fatalf("continuation lost Deliver phase: %#v %v", mode, err)
	}
	jobs, err := st.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: result.Run.ID, Limit: 20})
	if err != nil || len(jobs) != 0 {
		t.Fatalf("old verification Jobs copied: %#v %v", jobs, err)
	}
	approvals, err := st.ListApprovals(ctx, approval.ListFilter{RunID: result.Run.ID, Limit: 20})
	if err != nil || len(approvals) != 0 {
		t.Fatalf("old approvals copied: %#v %v", approvals, err)
	}
	oldJob, err := st.GetCommandRuntimeJob(ctx, f.job.ID)
	if err != nil || !reflect.DeepEqual(oldJob, f.job) {
		t.Fatalf("old verification Job changed: %#v %v", oldJob, err)
	}
	unchangedApproval, err := st.GetApprovalByProposal(ctx, proposal.Edit.ID)
	if err != nil || !reflect.DeepEqual(unchangedApproval, oldApproval) {
		t.Fatalf("old approval changed: %#v %v", unchangedApproval, err)
	}
	if _, found, err := st.GetStandardCodeSupervisorSnapshot(ctx, result.Run.ID); err != nil || found {
		t.Fatalf("old verification Supervisor ledger copied: found=%v %v", found, err)
	}
	if _, err := f.service.Record(ctx, StandardCodeDeliveryRecordRequest{RunID: result.Run.ID, OperationKey: "unverified-next-report", RequestedBy: "operator"}); err == nil {
		t.Fatal("new unverified epoch issued a delivery report from old verification")
	}
	// The exact same real Run/Drydock/snapshot tuple cannot be fabricated by an
	// ordinary preset intent. Only its existing proven Thread continuation may
	// carry Deliver/conservative instead of the original Plan/workspace_access.
	db, err := sql.Open("sqlite3", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fakeKey := strings.Repeat("a", 64)
	_, err = tx.Exec(`INSERT INTO standard_code_preset_operations (operation_key_digest,request_fingerprint,protocol_version,requested_run_id,run_id,mission_id,workspace_id,action,backend_intent,selected_backend,selection_reason,status,event_sequence_start,event_sequence_end,requested_by,capability_grant,created_at,updated_at)
		SELECT ?,request_fingerprint,protocol_version,requested_run_id,run_id,mission_id,workspace_id,action,backend_intent,selected_backend,selection_reason,'preparing',event_sequence_start,event_sequence_start,'ordinary_operator',0,created_at,updated_at FROM standard_code_preset_operations WHERE operation_key_digest=?`, fakeKey, preset.KeyDigest)
	if err != nil {
		t.Fatalf("negative fixture could not prepare an otherwise valid intent: %v", err)
	}
	_, err = tx.Exec(`UPDATE standard_code_preset_operations SET status='configured',drydock_id=?,drydock_generation=?,drydock_checkpoint_id=?,mode_snapshot_id=?,profile_snapshot_id=?,interaction_snapshot_id=?,permission_snapshot_id=?,browser_cdp_snapshot_id=?,event_sequence_end=? WHERE operation_key_digest=?`, preset.DrydockID, preset.DrydockGeneration, preset.DrydockCheckpointID, preset.ModeSnapshotID, preset.ProfileSnapshotID, preset.InteractionSnapshotID, preset.PermissionSnapshotID, preset.BrowserCDPSnapshotID, preset.EventSequenceEnd, fakeKey)
	if err == nil || !strings.Contains(err.Error(), "Standard Code preset completion binding is invalid") {
		t.Fatalf("ordinary intent bypassed precise continuation provenance: %v", err)
	}
}
