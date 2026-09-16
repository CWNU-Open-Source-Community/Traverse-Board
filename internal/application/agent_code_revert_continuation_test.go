package application

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

// Real SQLite, Git, source approval/apply, Thread continuation and gateway
// execution. The scripted model emits only file tools; no command Job or
// automatic verification is manufactured by this fixture.
func TestAgentCodeRevertContinuesAppliedSourceWithFreshApproval(t *testing.T) {
	f, supervisor := newStandardCodeOperatorInputFixture(t)
	ctx := t.Context()
	edits, err := f.state.ListFileEdits(ctx, fileedit.ListFilter{SessionID: f.run.SessionID})
	if err != nil || len(edits) != 1 || edits[0].Status != fileedit.StatusApplied {
		t.Fatalf("source edits=%+v err=%v", edits, err)
	}
	source := edits[0]
	sourceApproval, err := f.state.GetApprovalByProposal(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	physical, found, err := f.state.GetRunFileDrydock(ctx, f.run.ID)
	if err != nil || !found {
		t.Fatalf("physical=%+v found=%t err=%v", physical, found, err)
	}
	proposals := NewFileEditProposalService(f.state, policy.NewDefaultChecker()).WithDrydock(f.service)
	request := CreateFileEditRevertProposalRequest{Version: FileEditProposalProtocolVersion,
		RunID: f.run.ID, SourceRunID: f.run.ID, SourceEditID: source.ID, Path: source.Path,
		ExpectedSHA256: source.ProposedHash, OperationKey: "paused-revert-stays-read-only"}
	if _, err := proposals.ProposeRevert(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("paused proposal unexpectedly changed its gate: %v", err)
	}
	if _, err := NewRunService(f.state).Fail(ctx, f.run.ID, "end previous execution after its reviewed change"); err != nil {
		t.Fatal(err)
	}
	if _, err := proposals.ProposeRevert(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("terminal source became writable: %v", err)
	}
	thread, err := f.state.GetThreadByRun(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	messageRequest := SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID,
		Content: "Propose reversing the exact earlier edit; wait for a new approval before applying it", OperationKey: "revert-in-conversation", RequestedBy: "operator"}
	queued, err := NewThreadServiceWithExecutionCapabilities(f.state, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities).
		WithDrydock(f.service).Submit(ctx, messageRequest)
	if err != nil || !queued.SuccessorCreated || queued.Run.ID == f.run.ID {
		t.Fatalf("continuation=%+v err=%v", queued, err)
	}
	if _, err := NewRunService(f.state).Start(ctx, queued.Run.ID); err != nil {
		t.Fatal(err)
	}
	claim, err := f.state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
		RunID: queued.Run.ID, OwnerID: "revert-current-operator-input", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _, _ = f.state.ReleaseRunExecutionLease(ctx, claim.Lease) })
	turn, err := f.state.BeginSupervisorSteeringTurnForMessage(ctx, claim.Lease, queued.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := f.state.GetRunExecutionPermission(ctx, queued.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, authority, err := supervisor.supervisorAgentCodeCapabilities(ctx, turn, permission)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := supervisor.prepareStandardCodeSupervisor(ctx, turn, permission, capabilities.Generation, authority)
	if err != nil || machine.snapshot.State != domain.StandardCodeSupervisorExecute {
		t.Fatalf("successor is not ready to review its existing work: machine=%+v err=%v", machine, err)
	}
	if allowed, _, _ := machine.authorizeDescriptor(standardCodeCallDescriptor{Kind: domain.StandardCodeToolWorkspaceProposal, Action: "propose_patch"}); allowed {
		t.Fatal("ordinary patch gate was widened in Execute")
	}
	observe := *machine
	observe.snapshot.State = domain.StandardCodeSupervisorObserve
	if allowed, _, _ := observe.authorizeDescriptor(standardCodeCallDescriptor{Kind: domain.StandardCodeToolWorkspaceProposal, Action: "propose_revert"}); allowed {
		t.Fatal("revert displaced an ongoing observation")
	}
	var revertCall domain.SupervisorToolCall
	round := 0
	invoke := func(name toolgateway.ToolName, payload any) domain.SupervisorToolCall {
		t.Helper()
		round++
		raw, normalizeErr := toolgateway.NormalizeSupervisorToolPayload(name, mustAgentCodeDrydockJSON(t, payload))
		if normalizeErr != nil {
			t.Fatal(normalizeErr)
		}
		operationKey := runmutation.SupervisorToolOperationKey(turn.Run.ID, turn.Checkpoint.NextTurn, string(name), string(raw))
		callID, identityErr := runmutation.SupervisorToolCallID(operationKey, round)
		if identityErr != nil {
			t.Fatal(identityErr)
		}
		attempt := llm.ModelAttempt{Number: round, ToolRound: round - 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "continuation-code", Model: "model"}
		if _, err := f.state.RecordSupervisorModelStarted(ctx, machine.turn.Checkpoint, attempt); err != nil {
			t.Fatal(err)
		}
		attempt.Outcome = llm.OutcomeSuccess
		checkpoint, err := f.state.RecordSupervisorModelCompleted(ctx, machine.turn.Checkpoint, attempt,
			llm.ChatResponse{Provider: attempt.Provider, Model: attempt.Model, ToolCalls: []llm.ToolCall{{ID: callID, Name: string(name), Arguments: raw, Authority: authority}}})
		if err != nil {
			t.Fatal(err)
		}
		machine.turn.Checkpoint = checkpoint
		rounds, err := f.state.ListSupervisorToolRounds(ctx, checkpoint)
		if err != nil || len(rounds) != round {
			t.Fatalf("rounds=%+v err=%v", rounds, err)
		}
		call := rounds[round-1].Calls[0]
		decision, err := machine.Authorize(ctx, call)
		if err != nil || !decision.Allowed {
			t.Fatalf("tool decision=%+v err=%v", decision, err)
		}
		if _, err := f.state.RecordSupervisorToolExecutionStarted(ctx, checkpoint, call.CallID); err != nil {
			t.Fatal(err)
		}
		result, err := supervisor.invokeSupervisorTool(ctx, machine.turn, call)
		if err != nil || result.Status != domain.SupervisorToolCompleted {
			t.Fatalf("gateway result=%+v err=%v", result, err)
		}
		stored, _, err := f.state.RecordSupervisorToolResult(ctx, checkpoint, result)
		if err != nil {
			t.Fatal(err)
		}
		if err := machine.ObserveCall(ctx, stored); err != nil {
			t.Fatal(err)
		}
		return stored
	}
	input := toolgateway.WorkspaceChangePayload{Version: toolgateway.AgentCodeRegistryVersion,
		Action: "propose_revert", SourceRunID: f.run.ID, SourceEditID: source.ID,
		Path: source.Path, ExpectedSHA256: source.ProposedHash}
	revertCall = invoke(toolgateway.WorkspaceChangeTool, input)
	currentEdits, err := f.state.ListFileEdits(ctx, fileedit.ListFilter{SessionID: queued.Run.SessionID})
	if err != nil || len(currentEdits) != 1 {
		t.Fatalf("new edits=%+v err=%v", currentEdits, err)
	}
	inverse := currentEdits[0]
	if inverse.SessionID == source.SessionID || inverse.WorkspaceID != physical.WorkspaceID ||
		inverse.Status != fileedit.StatusProposed || inverse.OriginalHash != source.ProposedHash || inverse.ProposedHash != source.OriginalHash {
		t.Fatalf("inverse did not retain exact content in a new Session: %+v", inverse)
	}
	freshApproval, err := f.state.GetApprovalByProposal(ctx, inverse.ID)
	if err != nil || freshApproval.RunID != queued.Run.ID || freshApproval.Status != approval.StatusPending || freshApproval.ID == sourceApproval.ID {
		t.Fatalf("old approval became new authority: %+v %v", freshApproval, err)
	}
	applyRequest := ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion, RunID: queued.Run.ID, EditID: inverse.ID,
		OperationKey: "unapproved-inverse-must-not-write", AppliedBy: "operator", LeaseID: claim.Lease.LeaseID, LeaseGeneration: claim.Lease.Generation}
	if _, err := NewFileEditApplyService(f.state, policy.NewDefaultChecker()).WithDrydock(f.service).Apply(ctx, applyRequest); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("unapproved inverse could apply: %v", err)
	}
	if got := readDrydockTestFile(t, filepath.Join(physical.Path, source.Path)); fileedit.HashText(got) != source.ProposedHash {
		t.Fatalf("proposal or rejected apply changed current content: %q", got)
	}
	if _, err := NewFileEditReviewService(f.state).WithDrydock(f.service).Review(ctx, ReviewFileEditRequest{
		Version: FileEditReviewProtocolVersion, RunID: queued.Run.ID, EditID: inverse.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	invoke(toolgateway.WorkspaceApplyTool, toolgateway.WorkspaceApplyPayload{Version: toolgateway.AgentCodeRegistryVersion,
		EditID: inverse.ID, ExpectedAction: "propose_patch", ExpectedOriginalSHA256: inverse.OriginalHash, ExpectedProposedSHA256: inverse.ProposedHash})
	if got := readDrydockTestFile(t, filepath.Join(physical.Path, source.Path)); got != source.OriginalText {
		t.Fatalf("approved inverse did not restore exact bytes: %q", got)
	}
	if got := readDrydockTestFile(t, filepath.Join(f.sourceRoot, source.Path)); got != "user source\r\n" {
		t.Fatalf("source user file changed: %q", got)
	}
	unchanged, _ := f.state.GetFileEdit(ctx, source.ID)
	unchangedApproval, _ := f.state.GetApprovalByProposal(ctx, source.ID)
	if !reflect.DeepEqual(unchanged, source) || !reflect.DeepEqual(unchangedApproval, sourceApproval) {
		t.Fatal("historical edit or approval was rewritten")
	}
	if machine.snapshot.CanDeliver() || len(machine.snapshot.VerificationJobIDs) != 0 {
		t.Fatal("reverting a file manufactured verification")
	}
	// The gateway's operation identity is stable, and the shared service must
	// replay its inserted proposal without re-reading a later external edit.
	request.RunID = queued.Run.ID
	request.OperationKey = runmutation.SupervisorToolOperationKey(revertCall.RunID, revertCall.Turn, revertCall.ToolName, revertCall.PayloadJSON)
	writeDrydockTestFile(t, filepath.Join(physical.Path, source.Path), "later user edit\n")
	reopened, err := store.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replay, err := NewFileEditProposalService(reopened, policy.NewDefaultChecker()).ProposeRevert(ctx, request)
	if err != nil || !replay.Replayed || replay.Edit.ID != inverse.ID || replay.Edit.Status != fileedit.StatusApplied {
		t.Fatalf("exact inserted inverse failed read-only recovery: %+v %v", replay, err)
	}
	request.OperationKey = "fresh-inverse-rejects-later-user-edit"
	if _, err := proposals.ProposeRevert(ctx, request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("later target change accepted: %v", err)
	}
	request.OperationKey = runmutation.SupervisorToolOperationKey(revertCall.RunID, revertCall.Turn, revertCall.ToolName, revertCall.PayloadJSON)
	request.ExpectedSHA256 = source.OriginalHash
	if _, err := proposals.ProposeRevert(ctx, request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed source intent accepted: %v", err)
	}
	request.ExpectedSHA256, request.SourceRunID = source.ProposedHash, queued.Run.ID
	if _, err := proposals.ProposeRevert(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("source approval was attributed to the successor: %v", err)
	}
	request.SourceRunID = f.run.ID
	// A separate Thread using the same imported source cannot inherit an edit.
	_, foreign, err := NewRunService(f.state).Create(ctx, CreateRunRequest{Goal: "another task", Profile: "code", WorkspaceID: f.workspace.ID, Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	request.RunID, request.ExpectedSHA256 = foreign.ID, source.ProposedHash
	if _, err := proposals.ProposeRevert(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("cross-Thread source accepted: %v", err)
	}
}
