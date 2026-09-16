package application

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/workspace"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestAgentCodeWorkspaceAuthorityUsesOwnedDrydock(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "agent code owned files")
	checkpoints, err := NewWorkspaceCheckpointService(fixture.state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.WithCheckpointService(checkpoints)
	owned := mustCreateDrydock(t, fixture)
	if _, err := NewRunExecutionPermissionService(fixture.state,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true}).Change(t.Context(),
		ChangeRunExecutionPermissionRequest{RunID: fixture.run.ID, Mode: "workspace_access",
			OperationKey: "owned-agent-code-permission", RequestedBy: "operator", Reason: "exercise reviewed owned edit",
			ConfirmWorkspaceAccess: true}); err != nil {
		t.Fatal(err)
	}
	writeDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt"), "user source\r\n")
	writeDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt"), "owned target\n")
	run, err := NewRunService(fixture.state).Start(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := fixture.state.GetMission(t.Context(), run.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := fixture.state.GetRunMode(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := fixture.state.GetRunExecutionPermission(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := fixture.state.GetRootAgent(t.Context(), run.ID)
	if err != nil || !found {
		t.Fatalf("root found=%t err=%v", found, err)
	}
	supervisor := NewRunSupervisor(fixture.state, nil, policy.NewDefaultChecker())
	supervisor.WithDrydock(fixture.service)
	_, raw, err := supervisor.supervisorAgentCodeCapabilities(t.Context(), domain.SupervisorTurn{
		Run: run, Mission: mission, Agent: root, Mode: mode}, permission)
	if err != nil {
		t.Fatal(err)
	}
	var authority toolgateway.AgentCodeCallAuthority
	if err := json.Unmarshal(raw, &authority); err != nil {
		t.Fatal(err)
	}
	want, err := workspace.AgentCodeRootFingerprint(owned.Path)
	if err != nil {
		t.Fatal(err)
	}
	if authority.WorkspaceID != fixture.workspace.ID || authority.RootFingerprint != want {
		t.Fatalf("Agent Code advertised source files instead of the owned Drydock: workspace=%s root=%s want=%s", authority.WorkspaceID, authority.RootFingerprint, want)
	}
	lease, err := fixture.state.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "owned-agent-code-test", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	call := toolgateway.ToolCall{Name: toolgateway.WorkspaceReadTool,
		Payload:      json.RawMessage(`{"version":"agent-code-tools.v1","path":"tracked.txt","start_line":1,"end_line":10}`),
		OperationKey: "owned-agent-code-read-0001", RunID: run.ID, MissionID: mission.ID,
		AgentID: root.ID, SessionID: run.SessionID, WorkspaceID: mission.WorkspaceID,
		RootFingerprint: authority.RootFingerprint, Surface: authority.Surface, Phase: authority.Phase,
		Role: authority.Role, Profile: authority.Profile, PermissionMode: authority.PermissionMode,
		ModeRevision: authority.ModeRevision, PermissionRevision: authority.PermissionRevision,
		CapabilityGeneration: authority.CapabilityGeneration, LeaseID: lease.Lease.LeaseID,
		LeaseGeneration: lease.Lease.Generation, RequestedBy: "run_supervisor"}
	read, err := supervisor.tools.Invoke(t.Context(), call)
	if err != nil || read.Result == nil {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	var content struct {
		WorkspaceID string `json:"workspace_id"`
		Content     string `json:"content"`
		SHA256      string `json:"content_sha256"`
	}
	if err := json.Unmarshal([]byte(read.Result.Stdout), &content); err != nil {
		t.Fatal(err)
	}
	if content.WorkspaceID != owned.WorkspaceID || content.Content != "owned target" || content.SHA256 != fileedit.HashText("owned target\n") {
		t.Fatalf("read crossed into source workspace: %+v", content)
	}
	wrongRoot := call
	wrongRoot.OperationKey = "owned-agent-code-wrong-root-0001"
	wrongRoot.WorkspaceRoot = fixture.sourceRoot
	if _, err := supervisor.tools.Invoke(t.Context(), wrongRoot); err == nil {
		t.Fatal("stale source root was accepted")
	}
	change := call
	change.Name, change.OperationKey = toolgateway.WorkspaceChangeTool, "owned-agent-code-patch-0001"
	change.Payload, err = json.Marshal(toolgateway.WorkspaceChangePayload{Version: toolgateway.AgentCodeRegistryVersion,
		Action: "propose_patch", Path: "tracked.txt", ExpectedSHA256: content.SHA256,
		Replacements: []toolgateway.WorkspaceReplacement{{OldText: "owned target", NewText: "reviewed target", ExpectedOccurrences: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := supervisor.tools.Invoke(t.Context(), change)
	if err != nil || proposal.Result == nil {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}
	edit, err := fixture.state.GetFileEdit(t.Context(), proposal.Result.Metadata["edit_id"])
	if err != nil || edit.WorkspaceID != owned.WorkspaceID || edit.SessionID != run.SessionID ||
		edit.OriginalHash != content.SHA256 || edit.Status != fileedit.StatusProposed {
		t.Fatalf("persistent proposal target=%+v err=%v", edit, err)
	}
	if readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")) != "user source\r\n" ||
		readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")) != "owned target\n" {
		t.Fatal("proposal wrote workspace files")
	}
	stale := call
	stale.RootFingerprint, _ = workspace.AgentCodeRootFingerprint(fixture.sourceRoot)
	stale.CapabilityGeneration = toolgateway.AgentCodeCapabilities(toolgateway.AgentCodeCapabilityContext{
		RunID: run.ID, MissionID: mission.ID, RootAgentID: root.ID, WorkspaceID: mission.WorkspaceID,
		RootFingerprint: stale.RootFingerprint, Surface: mode.Surface, Phase: mode.Phase,
		Role: root.Role, Profile: mode.Profile, PermissionMode: permission.Mode,
		ModeRevision: mode.Revision, PermissionRevision: permission.Revision}).Generation
	stale.OperationKey = "owned-agent-code-old-authority-0001"
	if _, err := supervisor.tools.Invoke(t.Context(), stale); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeFailedPrecondition {
		t.Fatalf("old source authority was reinterpreted: %v", err)
	}
	if _, err := NewFileEditReviewService(fixture.state).WithDrydock(fixture.service).Review(t.Context(),
		ReviewFileEditRequest{Version: FileEditReviewProtocolVersion, RunID: run.ID, EditID: edit.ID,
			Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	turn, err := fixture.state.BeginSupervisorTurn(t.Context(), lease.Lease, "apply the reviewed owned edit")
	if err != nil {
		t.Fatal(err)
	}
	applyPayload, err := toolgateway.NormalizeAgentCodePayload(toolgateway.WorkspaceApplyTool,
		mustAgentCodeDrydockJSON(t, toolgateway.WorkspaceApplyPayload{Version: toolgateway.AgentCodeRegistryVersion,
			EditID: edit.ID, ExpectedAction: "propose_patch", ExpectedOriginalSHA256: edit.OriginalHash,
			ExpectedProposedSHA256: edit.ProposedHash}))
	if err != nil {
		t.Fatal(err)
	}
	opKey := runmutation.SupervisorToolOperationKey(run.ID, turn.Checkpoint.NextTurn,
		string(toolgateway.WorkspaceApplyTool), string(applyPayload))
	callID, err := runmutation.SupervisorToolCallID(opKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model"}
	if _, err := fixture.state.RecordSupervisorModelStarted(t.Context(), turn.Checkpoint, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := fixture.state.RecordSupervisorModelCompleted(t.Context(), turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: string(toolgateway.WorkspaceApplyTool), Arguments: applyPayload, Authority: raw}}})
	if err != nil {
		t.Fatal(err)
	}
	turn.Checkpoint = checkpoint
	rounds, err := fixture.state.ListSupervisorToolRounds(t.Context(), checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatalf("rounds=%+v err=%v", rounds, err)
	}
	durableCall := rounds[0].Calls[0]
	if _, err := fixture.state.RecordSupervisorToolExecutionStarted(t.Context(), checkpoint, durableCall.CallID); err != nil {
		t.Fatal(err)
	}
	// Invoke the real gateway: its budget invocation differs from this durable
	// Supervisor call ID. The checkpoint must resolve the latter's exact attempt.
	applied, err := supervisor.invokeSupervisorTool(t.Context(), turn, durableCall)
	if err != nil || applied.Status != domain.SupervisorToolCompleted {
		t.Fatalf("apply=%+v err=%v", applied, err)
	}
	storedCall, _, err := fixture.state.RecordSupervisorToolResult(t.Context(), checkpoint, applied)
	if err != nil {
		t.Fatal(err)
	}
	transactions, err := fixture.state.ListWorkspaceCheckpointTransactions(t.Context(), run.ID, 20)
	if err != nil || len(transactions) != 1 {
		t.Fatalf("transactions=%+v err=%v", transactions, err)
	}
	for _, checkpointID := range []string{transactions[0].BeforeCheckpointID, transactions[0].AfterCheckpointID} {
		actual, err := fixture.state.GetWorkspaceCheckpoint(t.Context(), checkpointID)
		if err != nil || actual.AttemptID != checkpoint.AttemptID || actual.CapabilityGeneration != authority.CapabilityGeneration ||
			actual.WorkspaceID != owned.WorkspaceID || actual.TriggerReceiptID != edit.ID || actual.RecoveryLevel != workspacecheckpoint.RecoveryComplete {
			t.Fatalf("gateway mutation lost exact Supervisor attempt: checkpoint=%+v expected_attempt=%s err=%v", actual, checkpoint.AttemptID, err)
		}
	}
	var envelope supervisorToolResultEnvelope
	if err := json.Unmarshal([]byte(storedCall.ResultJSON), &envelope); err != nil {
		t.Fatal(err)
	}
	machine := &standardCodeSupervisorTurn{store: fixture.state, turn: turn, permission: permission, fileWorkspaceID: owned.WorkspaceID,
		snapshot: domain.StandardCodeSupervisorSnapshot{RunID: run.ID, MissionID: mission.ID, WorkspaceID: mission.WorkspaceID,
			RootAgentID: root.ID, AttemptID: checkpoint.AttemptID, CapabilityGeneration: authority.CapabilityGeneration,
			ModeRevision: mode.Revision, PermissionRevision: permission.Revision, State: domain.StandardCodeSupervisorEdit}}
	if reason, err := machine.observeWorkspaceMutation(t.Context(), storedCall, standardCodeCallDescriptor{EditID: edit.ID}, envelope); err != nil || machine.snapshot.MutationEpoch != 1 || machine.snapshot.State != domain.StandardCodeSupervisorExecute {
		t.Fatalf("real checkpoint was rejected by mutation observer: reason=%s snapshot=%+v err=%v", reason, machine.snapshot, err)
	}
	if readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")) != "user source\r\n" ||
		readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")) != "reviewed target\n" {
		t.Fatal("gateway apply did not preserve the source and update the Drydock")
	}
}

func mustAgentCodeDrydockJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAgentCodeOwnedDrydockRejectsApprovalHostProposal(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "owned host proposal boundary")
	mustCreateDrydock(t, fixture)
	if _, err := NewRunExecutionPermissionService(fixture.state,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).Change(t.Context(),
		ChangeRunExecutionPermissionRequest{RunID: fixture.run.ID, Mode: "approval",
			OperationKey: "owned-host-approval-0001", RequestedBy: "operator",
			Reason: "explicit approval mode", ConfirmUserApproval: true}); err != nil {
		t.Fatal(err)
	}
	run, err := NewRunService(fixture.state).Start(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := fixture.state.GetRootAgent(t.Context(), run.ID)
	if err != nil || !found {
		t.Fatalf("root found=%t err=%v", found, err)
	}
	lease, err := fixture.state.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "owned-host-test", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewHostCommandProposalToolExecutor(fixture.state).ProposeHostCommand(t.Context(),
		toolgateway.HostCommandProposalContext{InvocationID: "owned-host-invocation", OperationKey: "owned-host-propose-0001",
			RunID: run.ID, RootAgentID: root.ID, SessionID: run.SessionID, WorkspaceID: fixture.workspace.ID,
			LeaseID: lease.Lease.LeaseID, LeaseGeneration: lease.Lease.Generation, RequestedBy: "run_supervisor",
			PolicyDecision: toolgateway.Decision{Allowed: true, Approval: toolgateway.ApprovalAutomatic,
				Risk: "low", Reason: "record proposal only"}},
		toolgateway.HostCommandProposalSpec{Version: runner.HostCommandProposalProtocolVersion,
			Transport: toolgateway.HostCommandTransportProcess, ExecutablePath: "git", Argv: []string{"status"},
			WorkingDirectory: ".", TimeoutMilliseconds: 1000, Purpose: "inspect workspace status"})
	if apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeFailedPrecondition ||
		err.Error() != "host command proposals do not support the Run's owned Drydock; use its configured Command Runtime" {
		t.Fatalf("owned approval mode could propose a source command: %v", err)
	}
	for _, definition := range supervisorStructuredToolSpecs(domain.ExecutionSurfaceCode,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionApproval, false, false,
		supervisorToolOptions{OwnedFileWorkspace: true}) {
		if definition.Name == string(toolgateway.HostCommandProposeTool) {
			t.Fatal("unsupported source host command was advertised for an owned Run")
		}
	}
}
