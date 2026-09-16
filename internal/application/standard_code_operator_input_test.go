package application

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

// The plan, file tools, message queue and Supervisor ledger use real SQLite and
// Git. Command outcomes are explicitly synthetic: this is an admission/replay
// regression, not a claim that an OS verification command ran successfully.
func TestStandardCodeSupervisorExplicitInputRechecksSameCommand(t *testing.T) {
	f, supervisor := newStandardCodeOperatorInputFixture(t)
	ctx := t.Context()
	prepare := func(key string) (*standardCodeSupervisorTurn, domain.RunExecutionLease, domain.OperatorSteeringMessage) {
		t.Helper()
		enqueued, err := f.state.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
			RunID: f.run.ID, SessionID: f.run.SessionID, Content: "Run the original repository check again", OperationKey: key, RequestedBy: "operator"})
		if err != nil {
			t.Fatal(err)
		}
		run, err := f.state.GetRun(ctx, f.run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == domain.RunPaused {
			if _, err = NewRunService(f.state).Resume(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
		}
		claim, err := f.state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
			RunID: run.ID, OwnerID: "explicit-command-check", TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		turn, err := f.state.BeginSupervisorSteeringTurnForMessage(ctx, claim.Lease, enqueued.Message.ID)
		if err != nil {
			t.Fatal(err)
		}
		permission, err := f.state.GetRunExecutionPermission(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		caps, authority, err := supervisor.supervisorAgentCodeCapabilities(ctx, turn, permission)
		if err != nil {
			t.Fatal(err)
		}
		machine, err := supervisor.prepareStandardCodeSupervisor(ctx, turn, permission, caps.Generation, authority)
		if err != nil {
			t.Fatal(err)
		}
		return machine, claim.Lease, enqueued.Message
	}
	command := func(machine *standardCodeSupervisorTurn, round int) domain.SupervisorToolCall {
		t.Helper()
		call := standardCodeCommandCall(t, "temporary", toolgateway.CommandRuntimeActionRun, "& ./check.ps1", "", 0)
		call.RunID, call.AttemptID, call.Turn = f.run.ID, machine.turn.Checkpoint.AttemptID, machine.turn.Checkpoint.NextTurn
		payload, err := toolgateway.NormalizeSupervisorToolPayload(toolgateway.CommandRuntimeTool, json.RawMessage(call.PayloadJSON))
		if err != nil {
			t.Fatal(err)
		}
		call.PayloadJSON = string(payload)
		call.CallID, err = runmutation.SupervisorToolCallID(runmutation.SupervisorToolOperationKey(call.RunID, call.Turn, call.ToolName, call.PayloadJSON), round)
		if err != nil {
			t.Fatal(err)
		}
		attempt := llm.ModelAttempt{Number: round, TransportAttempt: 1, MaxAttempts: 1, ToolRound: round - 1, Provider: "continuation-code", Model: "model"}
		if _, err = f.state.RecordSupervisorModelStarted(ctx, machine.turn.Checkpoint, attempt); err != nil {
			t.Fatal(err)
		}
		attempt.Outcome = llm.OutcomeSuccess
		authority, err := commandruntimeadapter.EncodeAuthority(commandruntimeadapter.NewAuthority(f.run.ID, standardCodeThreadTestRuntime().CommandRuntimeAdapters[0]))
		if err != nil {
			t.Fatal(err)
		}
		checkpoint, err := f.state.RecordSupervisorModelCompleted(ctx, machine.turn.Checkpoint, attempt, llm.ChatResponse{
			Provider: attempt.Provider, Model: attempt.Model, ToolCalls: []llm.ToolCall{{ID: call.CallID, Name: call.ToolName, Arguments: payload, Authority: authority}}})
		if err != nil {
			t.Fatal(err)
		}
		machine.turn.Checkpoint = checkpoint
		rounds, err := f.state.ListSupervisorToolRounds(ctx, checkpoint)
		if err != nil || len(rounds) != round {
			t.Fatalf("round=%+v err=%v", rounds, err)
		}
		return rounds[round-1].Calls[0]
	}
	finish := func(machine *standardCodeSupervisorTurn, lease domain.RunExecutionLease) {
		t.Helper()
		action := domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionWait, Message: "Review the recorded result", Reason: "operator review"}
		raw, _ := json.Marshal(action)
		response := llm.ChatResponse{Provider: "continuation-code", Model: "model", Text: string(raw)}
		rounds, err := f.state.ListSupervisorToolRounds(ctx, machine.turn.Checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		attempt := llm.ModelAttempt{Number: len(rounds) + 1, TransportAttempt: 1, MaxAttempts: 1, ToolRound: len(rounds), Provider: response.Provider, Model: response.Model}
		if _, err := f.state.RecordSupervisorModelStarted(ctx, machine.turn.Checkpoint, attempt); err != nil {
			t.Fatal(err)
		}
		attempt.Outcome = llm.OutcomeSuccess
		checkpoint, err := f.state.RecordSupervisorModelCompleted(ctx, machine.turn.Checkpoint, attempt, response)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err = f.state.CompleteSupervisorTurn(ctx, checkpoint, response, action, policy.Decision{Allowed: true, Reason: "bounded fixture wait"}, 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err = f.state.ReleaseRunExecutionLease(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
	observe := func(machine *standardCodeSupervisorTurn, call domain.SupervisorToolCall, exit int) {
		t.Helper()
		projection := standardCodeCommandOutput(t, "run", []runner.CommandRuntimeJobSnapshot{{
			ID: fmt.Sprintf("synthetic-job-%d", call.Turn), State: runner.CommandRuntimeJobCompleted, ExitCode: &exit, TreeReaped: true}}, nil)
		completed := standardCodeCompleteCall(t, call, domain.SupervisorToolCompleted, nil, projection, "")
		if _, err := f.state.RecordSupervisorToolExecutionStarted(ctx, machine.turn.Checkpoint, call.CallID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.state.RecordSupervisorToolResult(ctx, machine.turn.Checkpoint, domain.SupervisorToolResult{
			CallID: call.CallID, Status: completed.Status, ResultJSON: completed.ResultJSON, CompletedAt: *completed.CompletedAt}); err != nil {
			t.Fatal(err)
		}
		if err := machine.ObserveCall(ctx, completed); err != nil {
			t.Fatal(err)
		}
	}
	first, lease, original := prepare("explicit-check-first")
	firstCall := command(first, 1)
	if decision, err := first.Authorize(ctx, firstCall); err != nil || !decision.Allowed {
		t.Fatalf("first command=%+v %v", decision, err)
	}
	observe(first, firstCall, 1)
	if first.snapshot.State != domain.StandardCodeSupervisorDiagnose {
		t.Fatalf("failure state=%s", first.snapshot.State)
	}
	finish(first, lease)
	sealed := first.snapshot
	// A real user edit does not manufacture a Supervisor mutation epoch.
	owned, _, err := f.state.GetDrydockByRun(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeDrydockTestFile(t, filepath.Join(owned.Path, "user-note.txt"), "User changed this between checks\n")
	second, lease, next := prepare("explicit-check-second")
	if next.ID == original.ID || second.snapshot.MutationEpoch != sealed.MutationEpoch || second.snapshot.CommandsUsed != sealed.CommandsUsed ||
		second.snapshot.NoProgressCount != sealed.NoProgressCount || second.snapshot.RepeatedFailureCount != sealed.RepeatedFailureCount || second.snapshot.State != domain.StandardCodeSupervisorDiagnose {
		t.Fatalf("new input lost state or budgets: old=%+v current=%+v", sealed, second.snapshot)
	}
	if allowed, _, _ := second.authorizeDescriptor(standardCodeCallDescriptor{Kind: domain.StandardCodeToolWorkspaceProposal}); !allowed {
		t.Fatal("new input can no longer propose a correction before rerunning")
	}
	descriptor, err := describeStandardCodeCall(firstCall, sealed.MutationEpoch)
	if err != nil {
		t.Fatal(err)
	}
	autonomous := *second
	autonomous.operatorMessageID = ""
	if handled, err := autonomous.commandIntentAlreadyHandled(ctx, descriptor, "another-kernel-call"); err != nil || !handled {
		t.Fatalf("another kernel turn was mistaken for a new input: handled=%t %v", handled, err)
	}
	sameMessage := *second
	sameMessage.operatorMessageID = original.ID
	if handled, err := sameMessage.commandIntentAlreadyHandled(ctx, descriptor, "recovered-message-call"); err != nil || !handled {
		t.Fatalf("same message under a later attempt lost duplicate protection: handled=%t %v", handled, err)
	}
	exhausted := *second
	exhausted.snapshot.CommandsUsed = exhausted.snapshot.Limits.MaximumCommands
	if allowed, reason, _ := exhausted.authorizeDescriptor(descriptor); allowed || reason != "command_budget_exhausted" {
		t.Fatalf("new input bypassed the command budget: allowed=%t reason=%s", allowed, reason)
	}
	if id, err := f.state.SupervisorOperatorMessageID(ctx, "different-run", second.turn.Checkpoint.AttemptID, second.turn.Checkpoint.NextTurn); err != nil || id != "" {
		t.Fatalf("input lookup crossed Run ownership: %s %v", id, err)
	}
	// Reconstruct the coordinator from durable records before any launch.
	caps, authority, err := supervisor.supervisorAgentCodeCapabilities(ctx, second.turn, second.permission)
	if err != nil {
		t.Fatal(err)
	}
	second, err = supervisor.prepareStandardCodeSupervisor(ctx, second.turn, second.permission, caps.Generation, authority)
	if err != nil {
		t.Fatal(err)
	}
	secondCall := command(second, 1)
	decision, err := second.Authorize(ctx, secondCall)
	if err != nil || !decision.Allowed {
		t.Fatalf("new explicit message cannot rerun identical command: %+v %v", decision, err)
	}
	if secondCall.PayloadJSON != firstCall.PayloadJSON || secondCall.CallID == firstCall.CallID {
		t.Fatal("fixture changed command text or reused call identity")
	}
	commands := second.snapshot.CommandsUsed
	decision, err = second.Authorize(ctx, secondCall)
	if err != nil || !decision.Allowed || second.snapshot.CommandsUsed != commands {
		t.Fatalf("same call replay consumed budget: %+v %v", decision, err)
	}
	observe(second, secondCall, 1)
	// A failed retry cannot start another side effect in the same input, even
	// after rebuilding the coordinator. Other fresh command text also stays
	// in Diagnose until a real mutation or another explicit input.
	second, err = supervisor.prepareStandardCodeSupervisor(ctx, second.turn, second.permission, caps.Generation, authority)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := command(second, 2)
	decision, err = second.Authorize(ctx, duplicate)
	if err != nil || !decision.Replayed || decision.Allowed {
		t.Fatalf("same message repeated launch: %+v %v", decision, err)
	}
	if allowed, _, _ := second.authorizeDescriptor(standardCodeCallDescriptor{Kind: domain.StandardCodeToolCommandRun, CommandCount: 1}); allowed {
		t.Fatal("failed input opened unbounded command retries")
	}
	if _, err = f.state.RecordSupervisorToolExecutionStarted(ctx, second.turn.Checkpoint, duplicate.CallID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.state.RecordSupervisorToolResult(ctx, second.turn.Checkpoint, *decision.Result); err != nil {
		t.Fatal(err)
	}
	finish(second, lease)
	replayed, err := f.state.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{RunID: f.run.ID, SessionID: f.run.SessionID,
		Content: original.Content, OperationKey: "explicit-check-first", RequestedBy: "operator"})
	if err != nil || !replayed.Replayed || replayed.Message.ID != original.ID || replayed.Message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("original key became a new message: %+v %v", replayed, err)
	}
	after, _, err := f.state.GetStandardCodeSupervisorSnapshot(ctx, f.run.ID)
	if err != nil || after.CommandsUsed != sealed.CommandsUsed+1 || after.CanDeliver() || after.MutationEpoch != sealed.MutationEpoch {
		t.Fatalf("retry forged verification or mutation: %+v %v", after, err)
	}
	ledger, err := f.state.ListStandardCodeSupervisorLedger(ctx, f.run.ID, domain.StandardCodeSupervisorMaximumLedgerEntries)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range ledger {
		if entry.Snapshot.Version == sealed.Version {
			found = reflect.DeepEqual(entry.Snapshot, sealed)
		}
	}
	if !found {
		t.Fatal("old failure snapshot was rewritten")
	}
	if got := readDrydockTestFile(t, filepath.Join(owned.Path, "user-note.txt")); got != "User changed this between checks\n" {
		t.Fatalf("user note changed: %q", got)
	}
}

func newStandardCodeOperatorInputFixture(t *testing.T) (drydockApplicationFixture, *RunSupervisor) {
	f, owned := newFileEditDrydockFixture(t)
	ctx := t.Context()
	checkpoints, err := NewWorkspaceCheckpointService(f.state, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	f.service.WithCheckpointService(checkpoints)
	p := &continuationCodeProvider{}
	ref := llm.ModelRef{Provider: p.Name(), Model: "model"}
	router := llm.NewRouter(ref)
	router.RegisterProvider(p)
	router.SetRoute("code", ref)
	supervisor := NewRunSupervisor(f.state, router, policy.NewDefaultChecker()).WithDrydock(f.service).
		WithExecutionPermissionCapabilities(standardCodeThreadTestRuntime().ExecutionPermissionCapabilities)
	wait := func() *llm.ChatResponse {
		raw, _ := json.Marshal(domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionWait,
			Message: "Ready for the next user decision", Reason: "operator review"})
		return &llm.ChatResponse{Provider: p.Name(), Model: "model", Text: string(raw)}
	}
	tool := func(name string, payload any) *llm.ChatResponse {
		var raw json.RawMessage
		if text, ok := payload.(string); ok {
			raw = json.RawMessage(text)
		} else {
			raw = mustAgentCodeDrydockJSON(t, payload)
		}
		return &llm.ChatResponse{Provider: p.Name(), Model: "model", ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("call-%d", p.calls+len(p.responses)+1), Name: name, Arguments: raw}}}
	}
	step := func(runID string, responses ...*llm.ChatResponse) {
		t.Helper()
		p.responses = responses
		current, err := f.state.GetRun(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == domain.RunPaused {
			_, err = NewRunService(f.state).Resume(ctx, runID)
		}
		if current.Status == domain.RunCreated {
			_, err = NewRunService(f.state).Start(ctx, runID)
		}
		if err != nil {
			t.Fatal(err)
		}
		result, err := supervisor.StepWithInput(ctx, runID, "Continue this bounded coding task")
		if err != nil || (result.RunStatus != domain.RunPaused && result.RunStatus != domain.RunRunning) {
			t.Fatalf("step=%+v err=%v remaining=%d", result, err, len(p.responses))
		}
	}
	plan := `{"version":"plan_delivery.v1","directions":[
 {"title":"Small change","summary":"Change the tracked line","tradeoffs":["One scope"],"modules":[{"title":"Update line","objective":"Update the tracked text","acceptance_criteria":["Text updated"],"dependencies":[]}]},
 {"title":"Broader review","summary":"Review surrounding text","tradeoffs":["More review"],"modules":[{"title":"Review text","objective":"Review all text","acceptance_criteria":["Text reviewed"],"dependencies":[]}]},
 {"title":"Defer change","summary":"Document the limitation","tradeoffs":["Later delivery"],"modules":[{"title":"Document limit","objective":"Record the boundary","acceptance_criteria":["Boundary recorded"],"dependencies":[]}]}]}`
	step(f.run.ID,
		tool("workspace_read", `{"version":"agent-code-tools.v1","path":"tracked.txt","start_line":1,"end_line":1}`),
		tool("workspace_read", `{"version":"agent-code-tools.v1","path":"tracked.txt","start_line":1,"end_line":2}`),
		tool("plan_delivery_propose", plan), wait())
	proposals, err := f.state.ListPlanDeliveryProposals(ctx, f.run.ID, 10)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("plan=%+v err=%v", proposals, err)
	}
	control := NewPlanDeliveryControlService(f.state)
	if _, err = control.SelectDirection(ctx, ControlPlanDirectionRequest{Version: PlanDeliveryControlProtocolVersion, RunID: f.run.ID, ProposalID: proposals[0].ID,
		Direction: 1, OperationKey: "continuation-select", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	if _, err = control.EnterDelivery(ctx, ControlPlanDeliveryTransitionRequest{Version: PlanDeliveryControlProtocolVersion, RunID: f.run.ID,
		OperationKey: "continuation-deliver", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	step(f.run.ID, tool("workspace_change", toolgateway.WorkspaceChangePayload{Version: toolgateway.AgentCodeRegistryVersion,
		Action: "propose_patch", Path: "tracked.txt", ExpectedSHA256: fileedit.HashText("base\n"),
		Replacements: []toolgateway.WorkspaceReplacement{{OldText: "base", NewText: "retained reviewed change", ExpectedOccurrences: 1}}}), wait())
	edits, err := f.state.ListFileEdits(ctx, fileedit.ListFilter{SessionID: f.run.SessionID})
	if err != nil || len(edits) != 1 {
		t.Fatalf("edits=%+v err=%v", edits, err)
	}
	edit := edits[0]
	if _, err = NewFileEditReviewService(f.state).WithDrydock(f.service).Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion,
		RunID: f.run.ID, EditID: edit.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	step(f.run.ID, tool("workspace_apply", toolgateway.WorkspaceApplyPayload{Version: toolgateway.AgentCodeRegistryVersion,
		EditID: edit.ID, ExpectedAction: "propose_patch", ExpectedOriginalSHA256: edit.OriginalHash, ExpectedProposedSHA256: edit.ProposedHash}), wait())
	original, found, err := f.state.GetStandardCodeSupervisorSnapshot(ctx, f.run.ID)
	if err != nil || !found || original.MutationEpoch != 1 || !original.InspectionComplete {
		t.Fatalf("actual mutation=%+v err=%v", original, err)
	}
	if got := readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")); got != "retained reviewed change\n" {
		t.Fatalf("real apply missing: %q", got)
	}
	return f, supervisor
}
