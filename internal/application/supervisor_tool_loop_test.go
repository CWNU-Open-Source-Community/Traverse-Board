package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

func TestRunSupervisorExecutesAllowlistedStructuredToolAndContinuesModel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-tool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 5})
	toolCallResponse := toolResponse("provider-call-1", "work_item_create",
		`{"title":"Inspect parser","priority":"high"}`)
	toolCallResponse.Text = "已完成任务分析，下一步创建工作项。"
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolCallResponse,
		textResponse(rootActionResponse(domain.RootActionContinue, "work board updated", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != application.LifecycleTurnCompleted || result.ToolRounds != 1 || result.ToolCalls != 1 ||
		result.ModelAttempts != 2 || result.Text != "work board updated" || result.Checkpoint.TotalTokens != 8 {
		t.Fatalf("unexpected structured tool lifecycle result: %#v", result)
	}
	items, err := st.ListWorkItems(ctx, domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) != 1 || items[0].Title != "Inspect parser" {
		t.Fatalf("structured WorkItem was not created: %#v err=%v", items, err)
	}
	root, found, err := st.GetRootAgent(ctx, run.ID)
	if err != nil || !found || items[0].OwnerAgentID != root.ID {
		t.Fatalf("structured WorkItem was not bound to the calling root Agent: item=%#v root=%#v found=%t err=%v",
			items[0], root, found, err)
	}
	requests := provider.Requests()
	if len(requests) != 2 ||
		!hasToolSpec(requests[0], "work_item_create") ||
		!hasToolSpec(requests[0], "controlled_command_propose") ||
		hasToolResults(requests[0]) ||
		!hasToolResult(requests[1], "work_item") {
		t.Fatalf("model did not receive the structured tool transcript: %#v", requests)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for eventType, want := range map[string]int{
		events.ModelCompletedEvent: 2, events.SupervisorToolBatchEvent: 1,
		events.ModelPublicCommentaryEvent:            1,
		events.SupervisorToolExecutionStartedEvent:   1,
		events.SupervisorToolExecutionCompletedEvent: 1,
		events.SupervisorToolResultEvent:             1, events.SupervisorToolCompleteEvent: 1,
		events.WorkItemCreatedEvent: 1, events.ToolCompletedEvent: 1,
	} {
		if got := countEventType(eventList, eventType); got != want {
			t.Fatalf("event %s count=%d want=%d events=%#v", eventType, got, want, eventList)
		}
	}
	commentarySequence := int64(0)
	modelCompletedSequence := int64(0)
	toolBatchSequence := int64(0)
	for _, event := range eventList {
		switch event.Type {
		case events.ModelPublicCommentaryEvent:
			commentarySequence = event.Sequence
		case events.ModelCompletedEvent:
			if modelCompletedSequence == 0 {
				modelCompletedSequence = event.Sequence
			}
		case events.SupervisorToolBatchEvent:
			toolBatchSequence = event.Sequence
		}
		if strings.Contains(event.PayloadJSON, "provider-call-1") ||
			(strings.Contains(event.PayloadJSON, "Inspect parser") &&
				(event.Type == events.SupervisorToolBatchEvent || event.Type == events.SupervisorToolResultEvent ||
					event.Type == events.SupervisorToolCompleteEvent || strings.HasPrefix(event.Type, "model."))) {
			t.Fatalf("provider id or tool payload leaked into event %s: %s", event.Type, event.PayloadJSON)
		}
	}
	if commentarySequence == 0 || !(commentarySequence < modelCompletedSequence &&
		modelCompletedSequence < toolBatchSequence) {
		t.Fatalf("public commentary was not sequenced before the verified tool batch: commentary=%d model=%d tool=%d",
			commentarySequence, modelCompletedSequence, toolBatchSequence)
	}
	messages, err := st.ListSessionMessages(ctx, run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "下一步创建工作项") {
			t.Fatalf("display-only commentary leaked into Session history: %#v", messages)
		}
	}
}

func TestRunSupervisorExecutesDurableRunScopedWebFetch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-web-fetch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	runService := application.NewRunService(st)
	_, run, err := runService.Create(ctx, application.CreateRunRequest{
		Goal: "fetch one public evidence source", Profile: "review",
		Surface: "code", Phase: "deliver", ModelRoute: "tool-loop/model",
		NetworkMode: "allowlist", AllowedTargets: []string{"docs.example.com"},
		Budget: domain.Budget{MaxTurns: 3, MaxToolCalls: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runService.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-web-fetch", string(toolgateway.WebFetchTool),
			`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`),
		textResponse(rootActionResponse(domain.RootActionContinue,
			"public evidence fetched", "", "")),
	}}
	backend := &applicationWebFetchBackend{}
	supervisor := newToolLoopSupervisor(st, provider).WithWebEvidence(
		webevidence.NewService(st, nil, backend))
	result, err := supervisor.Step(ctx, run.ID)
	if err != nil || result.ToolRounds != 1 || result.ToolCalls != 1 ||
		result.ModelAttempts != 2 || backend.calls != 1 ||
		result.Text != "public evidence fetched" {
		t.Fatalf("result=%#v fetches=%d err=%v", result, backend.calls, err)
	}
	requests := provider.Requests()
	if len(requests) != 2 ||
		!hasToolSpec(requests[0], string(toolgateway.WebFetchTool)) ||
		!hasToolSpec(requests[0], string(toolgateway.WebCitationTool)) ||
		hasToolSpec(requests[0], string(toolgateway.WebSearchTool)) ||
		!hasToolResult(requests[1], "private bounded evidence body") {
		t.Fatalf("provider Web evidence contract drifted: %#v", requests)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(ctx, run.ID, 0, 2)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 ||
		rounds[0].Calls[0].ToolName != string(toolgateway.WebFetchTool) ||
		rounds[0].Calls[0].AuthorityJSON == "" || !rounds[0].Complete() {
		t.Fatalf("durable Web evidence round=%#v err=%v", rounds, err)
	}
	inventory, err := webevidence.LoadInventory(ctx, st, run.ID, 10, time.Now().UTC())
	if err != nil || len(inventory.Sources) != 1 || len(inventory.Snapshots) != 1 ||
		!inventory.Snapshots[0].Citeable || !inventory.Untrusted ||
		inventory.InstructionAuthorized {
		t.Fatalf("inventory=%#v err=%v", inventory, err)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundPresentation := false
	for _, event := range eventList {
		if strings.Contains(event.PayloadJSON, "private bounded evidence body") {
			t.Fatalf("Web page body leaked into public event %s: %s", event.Type,
				event.PayloadJSON)
		}
		if event.Type == events.SupervisorToolResultEvent &&
			strings.Contains(event.PayloadJSON, `"web_evidence"`) &&
			strings.Contains(event.PayloadJSON, `"instruction_authorized":false`) {
			foundPresentation = true
		}
	}
	if !foundPresentation {
		t.Fatalf("public Web evidence presentation was not recorded: %#v", eventList)
	}
}

func TestRunSupervisorInlineWebFetchApprovalResumesExactTurn(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-web-fetch-inline-approval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	runService := application.NewRunService(st)
	_, run, err := runService.Create(ctx, application.CreateRunRequest{
		Goal: "fetch one newly discovered public source", Profile: "review",
		Surface: "code", Phase: "deliver", ModelRoute: "tool-loop/model",
		NetworkMode: "disabled",
		Budget:      domain.Budget{MaxTurns: 3, MaxToolCalls: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runService.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-inline-web-fetch", string(toolgateway.WebFetchTool),
			`{"version":"web_fetch.v1","url":"https://new.example.net/report"}`),
		textResponse(rootActionResponse(domain.RootActionContinue,
			"approved public evidence fetched", "", "")),
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	checker := policy.NewDefaultChecker()
	backend := &applicationWebFetchBackend{}
	webService := webevidence.NewService(st, nil, backend)
	supervisor := application.NewRunSupervisor(st, router, checker).
		WithWebEvidence(webService).
		WithWebFetchAuthorizationScheduler(true)
	waiting, err := supervisor.Step(ctx, run.ID)
	if err != nil || waiting.RunStatus != domain.RunWaitingApproval ||
		waiting.ModelAttempts != 1 || waiting.ToolCalls != 1 || backend.calls != 0 {
		t.Fatalf("waiting lifecycle=%#v fetches=%d err=%v", waiting, backend.calls, err)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(ctx, run.ID, 0, 2)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 ||
		rounds[0].Calls[0].Status != domain.SupervisorToolPending {
		t.Fatalf("pending exact Web call=%#v err=%v", rounds, err)
	}
	pendingTurn, pendingCallID := rounds[0].Turn, rounds[0].Calls[0].CallID
	records, err := st.ListApprovals(ctx, approval.ListFilter{RunID: run.ID,
		Status: approval.StatusPending, Limit: 10})
	if err != nil || len(records) != 1 || records[0].ToolName != "web_fetch" {
		t.Fatalf("pending Web approval=%#v err=%v", records, err)
	}
	authorization, err := st.GetWebFetchAuthorizationByApproval(ctx, records[0].ID)
	if err != nil || authorization.SupervisorTurn != pendingTurn ||
		authorization.SupervisorToolCallID != pendingCallID {
		t.Fatalf("authorization=%#v err=%v", authorization, err)
	}
	approvalControl := application.NewApprovalControlService(st,
		toolgateway.New(st, checker), checker)
	decision, err := approvalControl.Decide(ctx, application.DecideApprovalControlRequest{
		Version: application.ApprovalControlProtocolVersion, RunID: run.ID,
		ApprovalID: records[0].ID, Action: application.ApprovalControlApproveOnce,
		OperationKey: "supervisor-inline-web-fetch-approve-once-0001",
		ReviewedBy:   "test_operator",
	})
	if err != nil || decision.Approval.Status != approval.StatusApproved || decision.Replayed {
		t.Fatalf("approval decision=%#v err=%v", decision, err)
	}
	handoff := application.NewRunExecutionHandoffService(st, router, checker).
		WithWebEvidence(webService).
		WithWebFetchAuthorizationScheduler(true)
	resumed, replayed, err := handoff.ResumeWebFetchAuthorization(ctx, run.ID,
		authorization.ID)
	if err != nil || replayed || backend.calls != 1 || resumed.Turn != pendingTurn ||
		resumed.ModelAttempts != 2 || resumed.ToolCalls != 1 || resumed.ToolRounds != 1 ||
		resumed.Text != "approved public evidence fetched" ||
		resumed.RunStatus != domain.RunRunning {
		t.Fatalf("resumed lifecycle=%#v replayed=%t fetches=%d err=%v",
			resumed, replayed, backend.calls, err)
	}
	requests := provider.Requests()
	if len(requests) != 2 || !hasToolSpec(requests[0], string(toolgateway.WebFetchTool)) ||
		!hasToolResult(requests[1], "private bounded evidence body") {
		t.Fatalf("provider did not receive the resumed exact result once: %#v", requests)
	}
	completedRounds, err := st.ListRunSupervisorToolRoundsPage(ctx, run.ID, 0, 2)
	if err != nil || len(completedRounds) != 1 || len(completedRounds[0].Calls) != 1 ||
		completedRounds[0].Turn != pendingTurn ||
		completedRounds[0].Calls[0].CallID != pendingCallID ||
		completedRounds[0].Calls[0].Status != domain.SupervisorToolCompleted {
		t.Fatalf("resumed call identity drifted: rounds=%#v err=%v", completedRounds, err)
	}
	storedRun, err := st.GetRun(ctx, run.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("Run did not remain continuable: run=%#v err=%v", storedRun, err)
	}
}

func TestRunSupervisorCompletesTwoRealAgentCodeToolRounds(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-agent-code-tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"),
		[]byte("first line\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkspace(ctx, store.WorkspaceRecord{ID: "ws-agent-code-rounds",
		Name: "agent-code-rounds", RootPath: workspaceRoot,
		CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	service := application.NewRunService(st)
	_, run, err := service.Create(ctx, application.CreateRunRequest{
		Goal: "inspect the workspace in two rounds", Profile: "code", Surface: "code",
		Phase: "deliver", WorkspaceID: "ws-agent-code-rounds", ModelRoute: "tool-loop/model",
		Budget: domain.Budget{MaxTurns: 3, MaxToolCalls: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-list", string(toolgateway.WorkspaceListTool),
			`{"version":"agent-code-tools.v1","path":".","limit":20}`),
		toolResponse("provider-read", string(toolgateway.WorkspaceReadTool),
			`{"version":"agent-code-tools.v1","path":"README.md","start_line":1,"end_line":20}`),
		textResponse(rootActionResponse(domain.RootActionContinue,
			"workspace inspection completed", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil || result.ToolRounds != 2 || result.ToolCalls != 2 ||
		result.ModelAttempts != 3 || result.Text != "workspace inspection completed" {
		t.Fatalf("agent code tool lifecycle=%#v err=%v", result, err)
	}
	requests := provider.Requests()
	if len(requests) != 3 || !hasToolSpec(requests[0], string(toolgateway.WorkspaceListTool)) ||
		!hasToolSpec(requests[0], string(toolgateway.WorkspaceApplyTool)) ||
		!hasToolResult(requests[1], `agent-code-tools.v1`) ||
		!hasToolResult(requests[2], `first line`) {
		t.Fatalf("provider did not receive agent code tools/results: %#v", requests)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(ctx, run.ID, 0, 4)
	if err != nil || len(rounds) != 2 {
		t.Fatalf("durable agent code rounds=%#v err=%v", rounds, err)
	}
	for _, round := range rounds {
		if len(round.Calls) != 1 || round.Calls[0].AuthorityJSON == "" ||
			!strings.Contains(round.Calls[0].AuthorityJSON, `"capability_generation"`) {
			t.Fatalf("agent code authority was not durable: %#v", round)
		}
	}
	usage, err := st.GetToolCallUsage(ctx, run.ID)
	if err != nil || usage.Consumed != 2 {
		t.Fatalf("agent code calls were not budgeted: %#v err=%v", usage, err)
	}
	artifacts, err := st.ListRunArtifacts(ctx, artifact.ListFilter{RunID: run.ID})
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("agent code artifacts=%#v err=%v", artifacts, err)
	}
	artifactTools := map[string]bool{}
	for _, descriptor := range artifacts {
		artifactTools[descriptor.ToolName] = true
		if descriptor.SourceID == "" || descriptor.Stream != artifact.StreamStdout ||
			descriptor.MIME != "application/json" || descriptor.SizeBytes <= 0 {
			t.Fatalf("agent code artifact binding=%#v", descriptor)
		}
	}
	if !artifactTools[string(toolgateway.WorkspaceListTool)] ||
		!artifactTools[string(toolgateway.WorkspaceReadTool)] {
		t.Fatalf("agent code artifact tools=%#v", artifactTools)
	}
}

func TestRunSupervisorRecordsOneShotCommandProposal(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-once-command.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	if err := st.SaveWorkspace(ctx, store.WorkspaceRecord{
		ID: "ws-once-command", Name: "once-command", RootPath: workspaceRoot,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	service := application.NewRunService(st)
	_, run, err := service.Create(ctx, application.CreateRunRequest{
		Goal: "record a one-shot command proposal", Profile: "code", Surface: "code",
		Phase: "deliver", WorkspaceID: "ws-once-command",
		Budget: domain.Budget{MaxTurns: 3, MaxToolCalls: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(toolgateway.OneShotCommandProposalSpec{
		Version: "once_command.v1", ExecutablePath: executable,
		Argv: []string{"-test.run", "^$"}, WorkingDirectory: workspaceRoot,
		Environment: []string{}, TimeoutMS: 30000, Purpose: "verify the test binary",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-once-command", string(toolgateway.OneShotCommandProposeTool),
			string(payload)),
		textResponse(rootActionResponse(domain.RootActionContinue,
			"one-shot command proposal awaits operator review", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil || result.ToolRounds != 1 || result.ToolCalls != 1 ||
		result.ModelAttempts != 2 {
		t.Fatalf("unexpected one-shot command tool lifecycle: %#v err=%v", result, err)
	}
	proposals, err := st.ListOnceCommandProposals(ctx, run.ID, 10)
	if err != nil || len(proposals) != 1 || proposals[0].Status != "proposed" ||
		proposals[0].ExecutablePath != executable || proposals[0].WorkingDirectory != workspaceRoot {
		t.Fatalf("one-shot command proposal was not persisted: %#v err=%v", proposals, err)
	}
	requests := provider.Requests()
	if len(requests) != 2 ||
		!hasToolSpec(requests[0], string(toolgateway.OneShotCommandProposeTool)) ||
		!hasToolResult(requests[1], proposals[0].ID) {
		t.Fatalf("model did not receive the one-shot proposal result: %#v", requests)
	}
}

func TestRunSupervisorPersistsReviewGatedDelegationWithoutSpawningAgents(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-delegation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop",
		domain.Budget{MaxTurns: 8, MaxTokens: 1000, MaxToolCalls: 5})
	token := "s" + "k-" + strings.Repeat("z", 28)
	payload := `{"version":"specialist_delegation.v1","assignments":[` +
		`{"title":"Parser review","goal":"Inspect parser without changing files; token=` + token +
		`","skills":["model.chat","work_item_create"],"turn_limit":2,"token_limit":128},` +
		`{"title":"Test review","goal":"Identify missing focused tests","skills":["note_create"],"turn_limit":1,"token_limit":64}]}`
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-delegation-1", "specialist_delegation_propose", payload),
		toolResponse("provider-delegation-2", "specialist_delegation_propose", payload),
		textResponse(rootActionResponse(domain.RootActionContinue,
			"delegation proposal awaits operator review", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil || result.ToolRounds != 2 || result.ToolCalls != 2 || result.ModelAttempts != 3 {
		t.Fatalf("unexpected delegation tool lifecycle: %#v err=%v", result, err)
	}
	proposals, err := st.ListSpecialistDelegationProposals(ctx, run.ID, 10)
	if err != nil || len(proposals) != 1 || len(proposals[0].Spec.Assignments) != 2 ||
		proposals[0].Status != domain.SpecialistDelegationProposed ||
		strings.Contains(proposals[0].Spec.Assignments[0].Goal, token) ||
		!strings.Contains(proposals[0].Spec.Assignments[0].Goal, "[REDACTED:") {
		t.Fatalf("delegation proposal was not safely persisted: %#v err=%v", proposals, err)
	}
	nodes, err := st.ListAgentNodes(ctx, run.ID)
	if err != nil || len(nodes) != 1 || nodes[0].Role != domain.AgentRoleRoot {
		t.Fatalf("proposal created or admitted a child Agent: %#v err=%v", nodes, err)
	}
	requests := provider.Requests()
	proposalIDs := toolResultMetadataValues(requests[2], "proposal_id")
	if len(proposalIDs) != 2 || proposalIDs[0] == "" || proposalIDs[0] != proposalIDs[1] ||
		!hasToolResult(requests[2], `"admission_authorized":"false"`) {
		t.Fatalf("delegation replay did not converge or claimed admission: %#v", requests[2])
	}
	usage, err := st.GetToolCallUsage(ctx, run.ID)
	if err != nil || usage.Consumed != 2 {
		t.Fatalf("delegation attempts were not budgeted: %#v err=%v", usage, err)
	}
	timeline, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(timeline, events.AgentDelegationProposedEvent) != 1 ||
		countEventType(timeline, events.ToolCompletedEvent) != 1 ||
		countEventType(timeline, events.AgentRegisteredEvent) != 1 {
		t.Fatalf("delegation event stream is inconsistent: %#v", timeline)
	}
	for _, event := range timeline {
		if strings.Contains(event.PayloadJSON, token) || strings.Contains(event.PayloadJSON, "Inspect parser") ||
			strings.Contains(event.PayloadJSON, "provider-delegation") {
			t.Fatalf("delegation text or provider identity leaked into %s: %s", event.Type, event.PayloadJSON)
		}
	}
}

func TestRunSupervisorReturnsRejectedDelegationAsToolResult(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-delegation-rejected.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop",
		domain.Budget{MaxTurns: 5, MaxTokens: 500, MaxToolCalls: 3})
	payload := `{"version":"specialist_delegation.v1","assignments":[` +
		`{"title":"Escalation","goal":"Request unavailable capability","skills":["shell"],"turn_limit":1,"token_limit":32}]}`
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-delegation-escalation", "specialist_delegation_propose", payload),
		textResponse(rootActionResponse(domain.RootActionContinue,
			"delegation rejected by Go capability checks", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil || result.ToolCalls != 1 ||
		!hasErrorToolResult(provider.Requests()[1], "INVALID_ARGUMENT") {
		t.Fatalf("invalid delegation was not returned as a bounded tool result: %#v err=%v requests=%#v",
			result, err, provider.Requests())
	}
	proposals, err := st.ListSpecialistDelegationProposals(ctx, run.ID, 10)
	if err != nil || len(proposals) != 0 {
		t.Fatalf("invalid delegation persisted state: %#v err=%v", proposals, err)
	}
}

func TestRunSupervisorRecoversRootActionWithTrailingCommentaryAfterToolResult(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-root-action-commentary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop",
		domain.Budget{MaxTurns: 3, MaxTokens: 500, MaxToolCalls: 3})
	invalidDelegation := `{"version":"specialist_delegation.v1","assignments":[` +
		`{"title":"Escalation","goal":"Request unavailable capability","skills":["shell"],"turn_limit":1,"token_limit":32}]}`
	const commentary = "The requested search-style tool result was unavailable."
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-root-action-commentary", "specialist_delegation_propose",
			invalidDelegation),
		textResponse(rootActionResponse(domain.RootActionFinish, "SEARCH_UNAVAILABLE",
			"search unavailable", "") + "\n\n" + commentary),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action.Kind != domain.RootActionFinish || result.Text != "SEARCH_UNAVAILABLE" ||
		result.ToolRounds != 1 || result.ToolCalls != 1 || result.ModelAttempts != 2 ||
		result.ProtocolRepairs != 0 || result.ModelOutcome != llm.OutcomeSuccess {
		t.Fatalf("trailing commentary recovery changed the tool-loop lifecycle: %#v", result)
	}
	requests := provider.Requests()
	if len(requests) != 2 || !hasErrorToolResult(requests[1], "INVALID_ARGUMENT") {
		t.Fatalf("recovery test did not exercise a failed durable tool result: %#v", requests)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	compatibilityEvents := 0
	for _, event := range eventList {
		if strings.Contains(event.PayloadJSON, commentary) {
			t.Fatalf("discarded trailing commentary leaked into durable events: %s", event.PayloadJSON)
		}
		if event.Type == events.ModelCompletedEvent &&
			strings.Contains(event.PayloadJSON, `"root_action_compatibility":{`) {
			compatibilityEvents++
			if !strings.Contains(event.PayloadJSON,
				`"recovery":"trailing_non_json_commentary_discarded"`) ||
				!strings.Contains(event.PayloadJSON,
					`"version":"root_action_compatibility.v1"`) {
				t.Fatalf("compatibility audit metadata is incomplete: %s", event.PayloadJSON)
			}
		}
	}
	if compatibilityEvents != 1 ||
		countEventType(eventList, events.ProtocolRepairRequestedEvent) != 0 ||
		countEventType(eventList, events.ModelFailedEvent) != 0 {
		t.Fatalf("unexpected compatibility recovery events: %#v", eventList)
	}
	messages, err := st.ListSessionMessages(ctx, run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content != "SEARCH_UNAVAILABLE" ||
		strings.Contains(messages[1].Content, commentary) {
		t.Fatalf("discarded commentary reached the transcript: %#v", messages)
	}
}

func TestRunSupervisorRecoversPendingToolResultAcrossStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supervisor-tool-restart.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 5})
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-before-restart", "work_item_create", `{"title":"Durable item"}`),
		textResponse(rootActionResponse(domain.RootActionContinue, "recovered tools", "", "")),
	}}
	failing := &failOnceToolResultStore{SQLiteStore: st, fail: true}
	first, err := newToolLoopSupervisor(failing, provider).Step(ctx, run.ID)
	if apperror.CodeOf(err) != apperror.CodeInternal || first.Checkpoint.Phase != domain.SupervisorTurnStarted {
		t.Fatalf("tool result failure did not leave a recoverable turn: %#v err=%v", first, err)
	}
	items, err := st.ListWorkItems(ctx, domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) != 1 {
		t.Fatalf("tool mutation did not commit before the injected result failure: %#v err=%v", items, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	resumed, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Recovered || resumed.ToolRounds != 1 || resumed.ToolCalls != 1 || resumed.ModelAttempts != 2 {
		t.Fatalf("pending tool batch was not recovered: %#v", resumed)
	}
	items, err = st.ListWorkItems(ctx, domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) != 1 || items[0].Title != "Durable item" {
		t.Fatalf("tool replay created duplicates: %#v err=%v", items, err)
	}
	usage, err := st.GetToolCallUsage(ctx, run.ID)
	if err != nil || usage.Consumed != 2 {
		t.Fatalf("initial call and recovery replay were not both budgeted: %#v err=%v", usage, err)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil || countEventType(eventList, events.WorkItemCreatedEvent) != 1 ||
		countEventType(eventList, events.ToolCompletedEvent) != 1 ||
		countEventType(eventList, events.SupervisorToolResultEvent) != 1 {
		t.Fatalf("recovered tool events are inconsistent: %#v err=%v", eventList, err)
	}
}

func TestRunSupervisorRecoversPendingAgentCodeReadAcrossStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supervisor-agent-code-restart.db")
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"),
		[]byte("durable workspace read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.SaveWorkspace(ctx, store.WorkspaceRecord{ID: "ws-agent-code-restart",
		Name: "agent-code-restart", RootPath: workspaceRoot,
		CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	service := application.NewRunService(st)
	_, run, err := service.Create(ctx, application.CreateRunRequest{
		Goal: "recover a durable workspace read", Profile: "code", Surface: "code",
		Phase: "deliver", WorkspaceID: "ws-agent-code-restart", ModelRoute: "tool-loop/model",
		Budget: domain.Budget{MaxTurns: 3, MaxToolCalls: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-agent-code-before-restart", string(toolgateway.WorkspaceReadTool),
			`{"version":"agent-code-tools.v1","path":"README.md","start_line":1,"end_line":20}`),
		textResponse(rootActionResponse(domain.RootActionContinue,
			"recovered workspace read", "", "")),
	}}
	failing := &failOnceToolResultStore{SQLiteStore: st, fail: true}
	first, err := newToolLoopSupervisor(failing, provider).Step(ctx, run.ID)
	if apperror.CodeOf(err) != apperror.CodeInternal ||
		first.Checkpoint.Phase != domain.SupervisorTurnStarted {
		t.Fatalf("agent code result failure was not recoverable: %#v err=%v", first, err)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(ctx, run.ID, 0, 4)
	if err != nil || len(rounds) != 1 || rounds[0].Calls[0].AuthorityJSON == "" ||
		rounds[0].Calls[0].Status != domain.SupervisorToolPending {
		t.Fatalf("pending agent code authority=%#v err=%v", rounds, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	resumed, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil || !resumed.Recovered || resumed.ToolRounds != 1 ||
		resumed.ToolCalls != 1 || resumed.ModelAttempts != 2 ||
		resumed.Text != "recovered workspace read" {
		t.Fatalf("pending agent code read was not recovered: %#v err=%v", resumed, err)
	}
	usage, err := st.GetToolCallUsage(ctx, run.ID)
	if err != nil || usage.Consumed != 2 {
		t.Fatalf("recovered agent code calls were not budgeted: %#v err=%v", usage, err)
	}
	artifacts, err := st.ListRunArtifacts(ctx, artifact.ListFilter{RunID: run.ID})
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("recovered agent code artifacts=%#v err=%v", artifacts, err)
	}
	for _, descriptor := range artifacts {
		if descriptor.ToolName != string(toolgateway.WorkspaceReadTool) {
			t.Fatalf("recovered artifact lost tool binding: %#v", descriptor)
		}
	}
}

func TestRunSupervisorReturnsPolicyDeniedToolResultWithoutCreatingNote(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-tool-denied.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 5})
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-denied", "note_create", `{"title":"Unsafe","content":"masscan 0.0.0.0/0"}`),
		textResponse(rootActionResponse(domain.RootActionContinue, "unsafe note rejected", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil || result.ToolCalls != 1 || !hasErrorToolResult(provider.Requests()[1], "POLICY_DENIED") {
		t.Fatalf("Policy denial was not returned as a tool result: %#v err=%v requests=%#v",
			result, err, provider.Requests())
	}
	notes, err := st.ListNotes(ctx, domain.NoteFilter{RunID: run.ID})
	if err != nil || len(notes) != 0 {
		t.Fatalf("Policy-denied Note was persisted: %#v err=%v", notes, err)
	}
}

func TestRunSupervisorSemanticToolKeySurvivesFailedTurnAndChangedProviderID(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-tool-attempt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 8})
	payload := `{"title":"One semantic item"}`
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-id-first", "work_item_create", payload),
		textResponse(`not lifecycle JSON`),
		textResponse(`still not lifecycle JSON`),
		toolResponse("provider-id-second", "work_item_create", payload),
		textResponse(rootActionResponse(domain.RootActionContinue, "retry converged", "", "")),
	}}
	supervisor := newToolLoopSupervisor(st, provider)
	first, err := supervisor.Step(ctx, run.ID)
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition || first.Checkpoint.Phase != domain.SupervisorTurnFailed {
		t.Fatalf("first turn attempt should fail protocol repair: %#v err=%v", first, err)
	}
	second, err := supervisor.Step(ctx, run.ID)
	if err != nil || second.ToolCalls != 1 || second.Text != "retry converged" {
		t.Fatalf("second turn attempt did not recover: %#v err=%v", second, err)
	}
	items, err := st.ListWorkItems(ctx, domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) != 1 {
		t.Fatalf("changed Provider call id duplicated semantic intent: %#v err=%v", items, err)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil || countEventType(eventList, events.WorkItemCreatedEvent) != 1 ||
		countEventType(eventList, events.ToolCompletedEvent) != 1 {
		t.Fatalf("semantic replay duplicated successful events: %#v err=%v", eventList, err)
	}
}

func TestRunSupervisorReturnsToolBudgetExhaustionAndResetsTransportAttemptsPerRound(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-tool-budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 1})
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-note-1", "note_create", `{"title":"First","content":"saved"}`),
		toolResponse("provider-note-2", "note_create", `{"title":"Second","content":"over budget"}`),
		textResponse(rootActionResponse(domain.RootActionContinue, "budget observed", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if err != nil || result.ToolRounds != 2 || result.ToolCalls != 2 || result.ModelAttempts != 3 ||
		!hasErrorToolResult(provider.Requests()[2], "RESOURCE_EXHAUSTED") {
		t.Fatalf("tool budget exhaustion was not returned to the model: %#v err=%v requests=%#v",
			result, err, provider.Requests())
	}
	notes, err := st.ListNotes(ctx, domain.NoteFilter{RunID: run.ID})
	if err != nil || len(notes) != 1 || notes[0].Title != "First" {
		t.Fatalf("over-budget Note was persisted: %#v err=%v", notes, err)
	}
	usage, err := st.GetToolCallUsage(ctx, run.ID)
	if err != nil || usage.Consumed != 1 || usage.ExhaustedAt == nil {
		t.Fatalf("tool budget ledger is inconsistent: %#v err=%v", usage, err)
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ {
		needle := `"tool_round":` + strconv.Itoa(round) + `,"transport_attempt":1`
		found := false
		for _, event := range eventList {
			if event.Type == events.ModelStartedEvent && strings.Contains(event.PayloadJSON, needle) {
				found = true
			}
		}
		if !found {
			t.Fatalf("tool round %d did not reset the transport attempt: %#v", round, eventList)
		}
	}
}

func TestRunSupervisorBoundsStructuredToolRounds(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-tool-round-limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 10})
	responses := make([]*llm.ChatResponse, 0, domain.MaxSupervisorToolRounds+2)
	for round := 1; round <= domain.MaxSupervisorToolRounds+2; round++ {
		responses = append(responses, toolResponse(fmt.Sprintf("provider-round-%d", round),
			"work_item_create", fmt.Sprintf(`{"title":"Round %d"}`, round)))
	}
	provider := &scriptedToolProvider{responses: responses}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition ||
		result.ToolRounds != domain.MaxSupervisorToolRounds ||
		result.ToolCalls != domain.MaxSupervisorToolRounds || result.ProtocolRepairs != 1 {
		t.Fatalf("structured tool round limit was not enforced: %#v err=%v", result, err)
	}
	items, listErr := st.ListWorkItems(ctx, domain.WorkItemFilter{RunID: run.ID})
	if listErr != nil || len(items) != domain.MaxSupervisorToolRounds {
		t.Fatalf("over-limit tool calls were executed: %#v err=%v", items, listErr)
	}
	eventList, listErr := st.ListRunEvents(ctx, run.ID)
	if listErr != nil || countEventType(eventList, events.SupervisorToolBatchEvent) != domain.MaxSupervisorToolRounds {
		t.Fatalf("tool batch limit event stream is inconsistent: %#v err=%v", eventList, listErr)
	}
}

func TestRunSupervisorReplaysRepeatedSemanticIntentAcrossToolRounds(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "supervisor-tool-repeat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	run := newStartedRunForProvider(t, st, "tool-loop", domain.Budget{MaxTurns: 3, MaxToolCalls: 4})
	payload := `{"title":"Repeat safely"}`
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("provider-repeat-1", "work_item_create", payload),
		toolResponse("provider-repeat-2", "work_item_create", payload),
		textResponse(rootActionResponse(domain.RootActionContinue, "repeat observed", "", "")),
	}}
	result, err := newToolLoopSupervisor(st, provider).Step(ctx, run.ID)
	entityIDs := toolResultEntityIDs(provider.Requests()[2])
	if err != nil || result.ToolRounds != 2 || result.ToolCalls != 2 || len(entityIDs) != 2 ||
		entityIDs[0] == "" || entityIDs[0] != entityIDs[1] {
		t.Fatalf("repeated semantic tool intent did not replay: %#v err=%v requests=%#v",
			result, err, provider.Requests())
	}
	items, err := st.ListWorkItems(ctx, domain.WorkItemFilter{RunID: run.ID})
	if err != nil || len(items) != 1 {
		t.Fatalf("repeated semantic intent created duplicates: %#v err=%v", items, err)
	}
}

func toolResultEntityIDs(request llm.ChatRequest) []string {
	ids := make([]string, 0)
	for _, message := range request.Messages {
		for _, result := range message.ToolResults {
			var envelope struct {
				Metadata map[string]string `json:"metadata"`
			}
			if json.Unmarshal([]byte(result.Content), &envelope) == nil {
				ids = append(ids, envelope.Metadata["entity_id"])
			}
		}
	}
	return ids
}

func toolResultMetadataValues(request llm.ChatRequest, key string) []string {
	values := make([]string, 0)
	for _, message := range request.Messages {
		for _, result := range message.ToolResults {
			var envelope struct {
				Metadata map[string]string `json:"metadata"`
			}
			if json.Unmarshal([]byte(result.Content), &envelope) == nil {
				if value := envelope.Metadata[key]; value != "" {
					values = append(values, value)
				}
			}
		}
	}
	return values
}

type scriptedToolProvider struct {
	mu        sync.Mutex
	responses []*llm.ChatResponse
	requests  []llm.ChatRequest
}

func (*scriptedToolProvider) Name() string { return "tool-loop" }

func (*scriptedToolProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", Provider: "tool-loop", Capabilities: []string{"chat", "tools"}}}, nil
}

func (p *scriptedToolProvider) Chat(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	if len(p.responses) == 0 {
		return nil, errors.New("scripted tool provider response queue is empty")
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	copy := *response
	copy.ToolCalls = append([]llm.ToolCall(nil), response.ToolCalls...)
	return &copy, nil
}

func (p *scriptedToolProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	chunks := make(chan llm.ChatChunk, 2)
	if response.Text != "" {
		chunks <- llm.ChatChunk{Text: response.Text}
	}
	chunks <- llm.FinalChatChunk(response)
	close(chunks)
	return chunks, nil
}

func (*scriptedToolProvider) SupportsTools(string) bool    { return true }
func (*scriptedToolProvider) SupportsVision(string) bool   { return false }
func (*scriptedToolProvider) SupportsJSONMode(string) bool { return true }

func (p *scriptedToolProvider) Requests() []llm.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.ChatRequest(nil), p.requests...)
}

type failOnceToolResultStore struct {
	*store.SQLiteStore
	mu   sync.Mutex
	fail bool
}

func (s *failOnceToolResultStore) RecordSupervisorToolResult(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, result domain.SupervisorToolResult,
) (domain.SupervisorToolCall, bool, error) {
	s.mu.Lock()
	if s.fail {
		s.fail = false
		s.mu.Unlock()
		return domain.SupervisorToolCall{}, false, apperror.New(apperror.CodeInternal,
			"injected supervisor tool result failure")
	}
	s.mu.Unlock()
	return s.SQLiteStore.RecordSupervisorToolResult(ctx, checkpoint, result)
}

func newToolLoopSupervisor(st application.RunSupervisorStore,
	provider *scriptedToolProvider,
) *application.RunSupervisor {
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	return application.NewRunSupervisor(st, router, policy.NewDefaultChecker())
}

func toolResponse(id string, name string, payload string) *llm.ChatResponse {
	return &llm.ChatResponse{
		Provider: "tool-loop", Model: "model", Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4},
		ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(payload)}},
	}
}

func textResponse(text string) *llm.ChatResponse {
	return &llm.ChatResponse{
		Text: text, Provider: "tool-loop", Model: "model",
		Usage: llm.Usage{InputTokens: 2, OutputTokens: 2, TotalTokens: 4},
	}
}

func hasToolResults(request llm.ChatRequest) bool {
	for _, message := range request.Messages {
		if len(message.ToolResults) > 0 {
			return true
		}
	}
	return false
}

func hasToolSpec(request llm.ChatRequest, name string) bool {
	for _, tool := range request.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func hasToolResult(request llm.ChatRequest, value string) bool {
	for _, message := range request.Messages {
		for _, result := range message.ToolResults {
			if strings.Contains(result.Content, value) {
				return true
			}
		}
	}
	return false
}

func hasErrorToolResult(request llm.ChatRequest, code string) bool {
	for _, message := range request.Messages {
		for _, result := range message.ToolResults {
			if result.IsError && strings.Contains(result.Content, code) {
				return true
			}
		}
	}
	return false
}
