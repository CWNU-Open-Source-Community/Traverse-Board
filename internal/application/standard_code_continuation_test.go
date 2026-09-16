package application

import (
	"context"
	"encoding/json"
	"errors"
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

// SQLite, Git and the registered workspace tools are real. Model responses are
// deterministic; this test asserts the command gate, not OS command execution.
func TestStandardCodeContinuationRechecksRetainedMutationWithoutAnotherEdit(t *testing.T) {
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
	thread, err := f.state.GetThreadByRun(ctx, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	prior := f.run.ID
	var source *domain.StandardCodeContinuation
	for generation := 0; generation < 2; generation++ {
		if _, err = NewRunService(f.state).Cancel(ctx, prior); err != nil {
			t.Fatal(err)
		}
		next, err := NewThreadServiceWithExecutionCapabilities(f.state, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities).
			WithDrydock(f.service).Submit(ctx, SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID,
			Content: "Check the already applied change using the current configuration", OperationKey: fmt.Sprintf("continuation-new-input-%d", generation), RequestedBy: "operator"})
		if err != nil {
			t.Fatal(err)
		}
		step(next.Run.ID, wait())
		current, found, err := f.state.GetStandardCodeSupervisorSnapshot(ctx, next.Run.ID)
		if err != nil || !found || current.State != domain.StandardCodeSupervisorExecute || current.MutationEpoch != 1 || current.CanDeliver() ||
			current.VerifiedMutationEpoch != 0 || len(current.Jobs) != 0 || len(current.VerificationJobIDs) != 0 || current.DeliveryID != "" || current.CommandsUsed != 0 {
			t.Fatalf("continued current check state=%+v err=%v", current, err)
		}
		if current.Continuation == nil || current.Continuation.MutationRunID != f.run.ID || current.Continuation.MutationCheckpointID == "" ||
			current.Continuation.PredecessorRunID != prior {
			t.Fatalf("missing exact original observation: %+v", current.Continuation)
		}
		if source != nil && (source.MutationCheckpointID != current.Continuation.MutationCheckpointID || source.MutationSnapshotVersion != current.Continuation.MutationSnapshotVersion) {
			t.Fatal("second successor lost original mutation proof")
		}
		source = current.Continuation
		latestRun, err := f.state.GetRun(ctx, next.Run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if latestRun.Status == domain.RunPaused {
			if _, err = NewRunService(f.state).Resume(ctx, next.Run.ID); err != nil {
				t.Fatal(err)
			}
		}
		claim, err := f.state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: next.Run.ID, OwnerID: "continuation-command-gate", TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		turn, err := f.state.BeginSupervisorTurn(ctx, claim.Lease, "Run the current verification")
		if err != nil {
			t.Fatal(err)
		}
		permission, err := f.state.GetRunExecutionPermission(ctx, next.Run.ID)
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
		call := standardCodeCommandCall(t, "recheck-existing-change", toolgateway.CommandRuntimeActionRun, "echo verify", "", 0)
		call.RunID, call.AttemptID, call.Turn = next.Run.ID, turn.Checkpoint.AttemptID, turn.Checkpoint.NextTurn
		payload, err := toolgateway.NormalizeSupervisorToolPayload(toolgateway.CommandRuntimeTool, json.RawMessage(call.PayloadJSON))
		if err != nil {
			t.Fatal(err)
		}
		call.PayloadJSON = string(payload)
		call.CallID, err = runmutation.SupervisorToolCallID(runmutation.SupervisorToolOperationKey(call.RunID, call.Turn, call.ToolName, call.PayloadJSON), 1)
		if err != nil {
			t.Fatal(err)
		}
		attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: p.Name(), Model: "model"}
		if _, err = f.state.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil {
			t.Fatal(err)
		}
		attempt.Outcome = llm.OutcomeSuccess
		commandAuthority, err := commandruntimeadapter.EncodeAuthority(commandruntimeadapter.NewAuthority(next.Run.ID, standardCodeThreadTestRuntime().CommandRuntimeAdapters[0]))
		if err != nil {
			t.Fatal(err)
		}
		checkpoint, err := f.state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt, llm.ChatResponse{Provider: p.Name(), Model: "model",
			ToolCalls: []llm.ToolCall{{ID: call.CallID, Name: call.ToolName, Arguments: json.RawMessage(call.PayloadJSON), Authority: commandAuthority}}})
		if err != nil {
			t.Fatal(err)
		}
		turn.Checkpoint = checkpoint
		machine.turn.Checkpoint = checkpoint
		rounds, err := f.state.ListSupervisorToolRounds(ctx, checkpoint)
		if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
			t.Fatalf("current command round=%+v %v", rounds, err)
		}
		call = rounds[0].Calls[0]
		decision, err := machine.Authorize(ctx, call)
		if err != nil || !decision.Allowed {
			t.Fatalf("real command gate still requires a redundant edit: %+v %v", decision, err)
		}
		if machine.snapshot.CanDeliver() {
			t.Fatal("authorization was mistaken for verification")
		}
		// The command outcome below is deliberately synthetic. It verifies
		// that a previous passed projection cannot cross an execution epoch;
		// the real OS check is covered by the separate product acceptance run.
		zero := 0
		projection := standardCodeCommandOutput(t, "run", []runner.CommandRuntimeJobSnapshot{{ID: "synthetic-current-job", State: runner.CommandRuntimeJobCompleted, ExitCode: &zero, TreeReaped: true}}, nil)
		completed := standardCodeCompleteCall(t, call, domain.SupervisorToolCompleted, nil, projection, "")
		if _, err = f.state.RecordSupervisorToolExecutionStarted(ctx, checkpoint, call.CallID); err != nil {
			t.Fatal(err)
		}
		if _, _, err = f.state.RecordSupervisorToolResult(ctx, checkpoint, domain.SupervisorToolResult{CallID: call.CallID, Status: completed.Status, ResultJSON: completed.ResultJSON, CompletedAt: *completed.CompletedAt}); err != nil {
			t.Fatal(err)
		}
		if err = machine.ObserveCall(ctx, completed); err != nil {
			t.Fatal(err)
		}
		if !machine.snapshot.CanDeliver() || len(machine.snapshot.VerificationJobIDs) != 1 {
			t.Fatalf("current verification did not work: %+v", machine.snapshot)
		}
		valid := *machine.snapshot.Continuation
		forged := valid
		forged.MutationCheckpointID = "checkpoint-from-another-task"
		machine.snapshot.Continuation = &forged
		if err = machine.append(ctx, domain.StandardCodeSupervisorTurnPrepared, domain.StandardCodeSupervisorRecorded, "", "", "forged-source", domain.StandardCodeToolOther, "", "", "", "", "", "forged_continuation_test"); err == nil {
			t.Fatal("changed historical mutation source was accepted")
		}
		machine.snapshot.Continuation = &valid
		if _, err = f.state.FailSupervisorTurn(ctx, turn.Checkpoint, "stop gate-only test before launching OS process", 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err = f.state.ReleaseRunExecutionLease(ctx, claim.Lease); err != nil {
			t.Fatal(err)
		}
		prior = next.Run.ID
	}
	after, _, err := f.state.GetStandardCodeSupervisorSnapshot(ctx, f.run.ID)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatal("old mutation ledger was rewritten")
	}
	if got := readDrydockTestFile(t, filepath.Join(f.sourceRoot, "tracked.txt")); got != "user source\r\n" {
		t.Fatalf("source changed: %q", got)
	}
}

type continuationCodeProvider struct {
	responses []*llm.ChatResponse
	calls     int
}

func (*continuationCodeProvider) Name() string { return "continuation-code" }
func (*continuationCodeProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", Provider: "continuation-code", Capabilities: []string{"chat", "tools"}}}, nil
}
func (p *continuationCodeProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	if len(p.responses) == 0 {
		return nil, errors.New("bounded continuation response queue empty")
	}
	r := p.responses[0]
	p.responses = p.responses[1:]
	return r, nil
}
func (p *continuationCodeProvider) StreamChat(ctx context.Context, r llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, r)
	if err != nil {
		return nil, err
	}
	out := make(chan llm.ChatChunk, 2)
	if response.Text != "" {
		out <- llm.ChatChunk{Text: response.Text}
	}
	out <- llm.FinalChatChunk(response)
	close(out)
	return out, nil
}
func (*continuationCodeProvider) SupportsTools(string) bool    { return true }
func (*continuationCodeProvider) SupportsVision(string) bool   { return false }
func (*continuationCodeProvider) SupportsJSONMode(string) bool { return true }
