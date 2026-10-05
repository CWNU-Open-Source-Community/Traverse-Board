package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
)

func specialistBriefResponses(t *testing.T, count int) []llm.ChatResponse {
	t.Helper()
	text := specialistResponse(t, domain.SpecialistAction{
		Version: domain.SpecialistLifecycleVersion, Kind: domain.SpecialistActionContinue,
		Message: "continue the evidence analysis",
	})
	responses := make([]llm.ChatResponse, count)
	for i := range responses {
		responses[i] = llm.ChatResponse{Text: text,
			Usage: llm.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}}
	}
	return responses
}

func sendSpecialistBriefInstruction(t *testing.T, st *store.SQLiteStore,
	run domain.Run, child domain.AgentNode, instruction string,
) domain.AgentMessage {
	t.Helper()
	ctx := context.Background()
	root, found, err := st.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root missing: %v", err)
	}
	payload, err := json.Marshal(domain.AgentInstructionPayload{
		Version: domain.SpecialistInstructionVersion, Instruction: instruction,
	})
	if err != nil {
		t.Fatal(err)
	}
	message, replayed, err := st.SendAgentMessage(ctx, domain.AgentMessage{
		ID: idgen.New("agentmsg"), RunID: run.ID,
		SenderAgentID: root.ID, RecipientAgentID: child.ID,
		Kind: domain.AgentMessageInstruction, Semantic: domain.AgentMessageSemanticMessage,
		PayloadJSON: string(payload),
	}, idgen.New("brief-instruction"))
	if err != nil || replayed {
		t.Fatalf("instruction failed: replayed=%t err=%v", replayed, err)
	}
	return message
}

func TestSpecialistTaskBriefPreservesConsumedDelegationOutsideHistoryWindow(t *testing.T) {
	const turns = 8
	const delegation = "Inspect only AUTH_SCOPE_PERSISTENCE_215; keep public interfaces unchanged."
	provider := &specialistTestProvider{responses: specialistBriefResponses(t, turns)}
	st, run, child, runner := newSubagentRunnerFixture(t, provider,
		domain.Budget{MaxTurns: 16}, turns+1, 256)
	instruction := sendSpecialistBriefInstruction(t, st, run, child, delegation)
	ctx := context.Background()
	for i := range turns {
		result, err := runner.Step(ctx, run.ID, child.ID)
		if err != nil || result.AttemptStatus != domain.AgentAttemptContinued {
			t.Fatalf("turn%d failed: result=%#v err=%v", i+1, result, err)
		}
	}
	if len(provider.requests) != turns {
		t.Fatalf("calls=%d want%d", len(provider.requests), turns)
	}
	actual := provider.requests[turns-1]
	for _, req := range provider.requests {
		if len(req.Tools) != 0 {
			t.Fatal("brief persistence granted tools")
		}
	}
	for _, message := range actual.Messages {
		if strings.Contains(message.Content, delegation) {
			goto delegationDelivered
		}
	}
	t.Fatal("actual eighth request lost the consumed delegation after the original user record left the12-message window")
delegationDelivered:
	// The current input itself must carry it; an optional historical repetition is insufficient.
	if !strings.Contains(actual.Messages[len(actual.Messages)-1].Content, delegation) {
		t.Fatal("current child context did not persist the effective delegation")
	}
	messages, err := st.ListAgentMessages(ctx, child.ID, false, 10)
	if err != nil || len(messages) != 1 || messages[0].ID != instruction.ID ||
		messages[0].Status != domain.AgentMessageConsumed || messages[0].ConsumedAt == nil {
		t.Fatalf("parent delivery lost idempotent consumption: messages=%#v err=%v", messages, err)
	}
	attempts, err := st.ListAgentAttempts(ctx, child.ID)
	if err != nil || len(attempts) != turns {
		t.Fatalf("missing attempt evidence: %d %v", len(attempts), err)
	}
	leases := map[string]struct{}{}
	for _, attempt := range attempts {
		key := fmt.Sprintf("%s/%d", attempt.LeaseID, attempt.LeaseGeneration)
		if _, exists := leases[key]; exists {
			t.Fatal("continued child restored an earlier attempt lease")
		}
		leases[key] = struct{}{}
	}
	timeline, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, event := range timeline {
		if event.Type != "model.started" || event.Source != "specialist_model_gateway" {
			continue
		}
		var payload struct {
			AttemptID string                `json:"agent_attempt_id"`
			Context   llm.ModelContextAudit `json:"context"`
		}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		bound := false
		for _, source := range payload.Context.Included {
			bound = bound || source.Kind == "specialist_task_brief" && len(source.SourceID) == 64
		}
		if payload.AttemptID == "" || !bound {
			t.Fatal("current attempt lacks a task brief dispatch receipt")
		}
		starts++
	}
	if starts != turns {
		t.Fatalf("brief dispatch receipts=%d want%d", starts, turns)
	}
}

func TestSpecialistTaskBriefDeliversCompleteOwnedWorkDescriptionAndAcceptance(t *testing.T) {
	provider := &specialistTestProvider{responses: specialistBriefResponses(t, 1)}
	st, run, child, runner := newSubagentRunnerFixture(t, provider,
		domain.Budget{MaxTurns: 10}, 2, 256)
	description := strings.Repeat("bounded evidence detail ", 45) + "REQUIRED_DESCRIPTION_TAIL_215"
	criteria := []string{"first", "second", "third", "fourth", "fifth", "sixth", "zz_REQUIRED_LAST_CRITERION_215"}
	work, err := application.NewWorkItemService(st).Create(context.Background(),
		application.CreateWorkItemRequest{
			RunID: run.ID, OwnerAgentID: child.ID, Title: "child authentication review",
			Description: description, AcceptanceCriteria: criteria, Priority: "high",
		})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 16 {
		if _, err := application.NewNoteService(st).Create(t.Context(), application.CreateNoteRequest{
			RunID: run.ID, OwnerAgentID: child.ID, Visibility: "owner", Title: fmt.Sprintf("optional note %d", i),
			Category: "observation", Content: strings.Repeat("optional evidence background ", 90),
		}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := runner.Step(context.Background(), run.ID, child.ID)
	if err != nil || result.AttemptStatus != domain.AgentAttemptContinued {
		t.Fatalf("child turn failed: result=%#v err=%v", result, err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("calls=%d", len(provider.requests))
	}
	if result.ContextOmitted == 0 {
		t.Fatal("fixture did not create optional-note competition")
	}
	input := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Content
	for _, required := range []string{work.ID, "REQUIRED_DESCRIPTION_TAIL_215", "zz_REQUIRED_LAST_CRITERION_215"} {
		if !strings.Contains(input, required) {
			t.Errorf("actual model input silently lost required owned-work content %q", required)
		}
	}
}

func specialistBriefRunnerWithWindow(t *testing.T, st *store.SQLiteStore, provider llm.Provider, tokens int) *application.SubagentRunner {
	t.Helper()
	ref := llm.ModelRef{Provider: provider.Name(), Model: "model"}
	router := llm.NewRouter(ref)
	router.RegisterProvider(provider)
	window := llm.DefaultContextWindow()
	window.WindowTokens = tokens
	window.SafetyMarginTokens = 128
	window.DefaultOutputTokens = 128
	window.MaxOutputTokens = 256
	window.Source = "brief_test"
	if err := router.SetContextWindow(ref, window); err != nil {
		t.Fatal(err)
	}
	return application.NewSubagentRunner(st, router, policy.NewDefaultChecker())
}

func TestSpecialistTaskBriefSurvivesIndependentHistoryBytePressure(t *testing.T) {
	provider := &specialistTestProvider{responses: specialistBriefResponses(t, 2)}
	st, run, child, _ := newSubagentRunnerFixture(t, provider, domain.Budget{MaxTurns: 10}, 4, 256)
	runner := specialistBriefRunnerWithWindow(t, st, provider, 128*1024)
	const required = "CURRENT_SCOPE_BYTE_PRESSURE_215"
	sendSpecialistBriefInstruction(t, st, run, child, required)
	if _, err := runner.Step(t.Context(), run.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := st.SaveSessionMessage(t.Context(), session.Message{SessionID: child.SessionID, Role: "assistant",
			Content: fmt.Sprintf("LARGE_RECENT_%d_215 ", i) + strings.Repeat("bounded history background ", 1300)}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := st.ListRecentSessionMessages(t.Context(), child.SessionID, false, 12)
	if err != nil || len(records) >= 12 {
		t.Fatalf("fixture exceeded count bound instead of bytes: %d %v", len(records), err)
	}
	rawBytes := 0
	for _, m := range records {
		rawBytes += len(m.Content)
	}
	if rawBytes <= 64*1024 {
		t.Fatal("fixture did not exceed history byte bound")
	}
	if _, err := runner.Step(t.Context(), run.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	actual := provider.requests[1]
	if !strings.Contains(actual.Messages[len(actual.Messages)-1].Content, required) {
		t.Fatal("byte pressure lost mandatory constraints")
	}
	historyBytes := 0
	for i, m := range actual.Messages {
		if strings.Contains(m.Content, "LARGE_RECENT_0_215") {
			t.Fatal("old byte-pressure background was not omitted")
		}
		if i < len(actual.Messages)-1 && m.Role == "assistant" {
			historyBytes += len(m.Content)
		}
	}
	if historyBytes > 64*1024 || actual.Metadata["context_history_omitted"] != "0" {
		t.Fatalf("wrong pressure boundary: bytes=%d metadata=%#v", historyBytes, actual.Metadata)
	}
}

func TestSpecialistTaskBriefSurvivesFinalWindowFittingAndStopsIfRequiredCannotFit(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("refuse_%t", refuse), func(t *testing.T) {
			provider := &specialistTestProvider{responses: specialistBriefResponses(t, 1)}
			st, run, child, _ := newSubagentRunnerFixture(t, provider, domain.Budget{MaxTurns: 10}, 3, 256)
			runner := specialistBriefRunnerWithWindow(t, st, provider, 4096)
			required := "FINAL_WINDOW_REQUIRED_SCOPE_215"
			instruction := sendSpecialistBriefInstruction(t, st, run, child, required)
			if refuse {
				if _, err := application.NewWorkItemService(st).Create(t.Context(), application.CreateWorkItemRequest{
					RunID: run.ID, OwnerAgentID: child.ID, Title: "complete required work", Description: strings.Repeat("界", 1050) + "FINAL_WINDOW_WORK_TAIL_215",
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				for range 3 {
					if _, err := st.SaveSessionMessage(t.Context(), session.Message{SessionID: child.SessionID, Role: "assistant", Content: strings.Repeat("optional historical evidence ", 450)}); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := runner.Step(t.Context(), run.ID, child.ID)
			if refuse {
				if apperror.CodeOf(err) != apperror.CodeResourceExhausted || !strings.Contains(err.Error(), "mandatory model context") || result.AttemptStatus != domain.AgentAttemptCrashed || len(provider.requests) != 0 {
					t.Fatalf("required final-window overflow did not stop before dispatch: %#v %v calls%d", result, err, len(provider.requests))
				}
				pending, err := st.ListAgentMessages(t.Context(), child.ID, true, 10)
				if err != nil || len(pending) != 1 || pending[0].ID != instruction.ID {
					t.Fatal("undelivered instruction was consumed")
				}
				return
			}
			if err != nil || len(provider.requests) != 1 {
				t.Fatalf("fitting failed: %#v %v", result, err)
			}
			actual := provider.requests[0]
			omitted, _ := strconv.Atoi(actual.Metadata["context_history_omitted"])
			if omitted == 0 || !strings.Contains(actual.Messages[len(actual.Messages)-1].Content, required) {
				t.Fatal("final fitting did not omit history while retaining constraints")
			}
		})
	}
}

func sendSpecialistBriefOperation(t *testing.T, st *store.SQLiteStore, run domain.Run, child domain.AgentNode, op, text string, target domain.AgentMessage) domain.AgentMessage {
	t.Helper()
	root, _, err := st.GetRootAgent(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.AgentInstructionPayload{Version: domain.SpecialistInstructionOperationVersion, Operation: op, Instruction: text}
	if target.ID != "" {
		p.TargetMessageID = target.ID
		p.TargetPayloadSHA256 = domain.SpecialistInstructionPayloadSHA256(target.PayloadJSON)
	}
	raw, _ := json.Marshal(p)
	message, _, err := st.SendAgentMessage(t.Context(), domain.AgentMessage{ID: idgen.New("agentmsg"), RunID: run.ID,
		SenderAgentID: root.ID, RecipientAgentID: child.ID, Kind: domain.AgentMessageInstruction,
		Semantic: domain.AgentMessageSemanticMessage, PayloadJSON: string(raw)}, idgen.New("brief-op"))
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestSpecialistTaskBriefActualRequestsApplyCorrectionsReplacementAndWithdrawal(t *testing.T) {
	provider := &specialistTestProvider{responses: specialistBriefResponses(t, 4)}
	st, run, child, runner := newSubagentRunnerFixture(t, provider, domain.Budget{MaxTurns: 10}, 5, 256)
	original := sendSpecialistBriefInstruction(t, st, run, child, "RETIRED_ORIGINAL_SCOPE_215")
	if _, err := runner.Step(t.Context(), run.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	correction := sendSpecialistBriefOperation(t, st, run, child, "append", "WITHDRAWN_INTERFACE_CORRECTION_215", domain.AgentMessage{})
	if _, err := runner.Step(t.Context(), run.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	replacement := sendSpecialistBriefOperation(t, st, run, child, "replace", "CURRENT_REPLACEMENT_SCOPE_215", original)
	latest := sendSpecialistBriefOperation(t, st, run, child, "append", "CURRENT_SECOND_CORRECTION_215", domain.AgentMessage{})
	if _, err := runner.Step(t.Context(), run.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	withdrawal := sendSpecialistBriefOperation(t, st, run, child, "withdraw", "", correction)
	if _, err := runner.Step(t.Context(), run.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	actual := provider.requests[3]
	for _, m := range actual.Messages {
		if strings.Contains(m.Content, "RETIRED_ORIGINAL_SCOPE_215") || strings.Contains(m.Content, "WITHDRAWN_INTERFACE_CORRECTION_215") {
			t.Fatal("retired instruction leaked from historical context")
		}
	}
	input := actual.Messages[len(actual.Messages)-1].Content
	for _, text := range []string{"CURRENT_REPLACEMENT_SCOPE_215", "CURRENT_SECOND_CORRECTION_215"} {
		if !strings.Contains(input, text) {
			t.Fatal("current correction was omitted")
		}
	}
	for _, message := range []domain.AgentMessage{original, correction, replacement, latest, withdrawal} {
		if strings.Contains(input, message.ID) {
			t.Fatal("parent source routing ID entered model text")
		}
	}
	messages, err := st.ListAgentMessages(t.Context(), child.ID, false, 10)
	if err != nil || len(messages) != 5 {
		t.Fatal("instruction ledger changed")
	}
	for _, message := range messages {
		if message.Status != domain.AgentMessageConsumed {
			t.Fatal("instruction consumption was not committed")
		}
	}
}

func TestSpecialistTaskBriefRejectsTwentyFirstActiveOwnedWorkItem(t *testing.T) {
	provider := &specialistTestProvider{responses: specialistBriefResponses(t, 1)}
	st, run, child, runner := newSubagentRunnerFixture(t, provider, domain.Budget{MaxTurns: 10}, 3, 256)
	for i := range 21 {
		if _, err := application.NewWorkItemService(st).Create(t.Context(), application.CreateWorkItemRequest{RunID: run.ID, OwnerAgentID: child.ID, Title: fmt.Sprintf("required unfinished work %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := runner.Step(t.Context(), run.ID, child.ID)
	if apperror.CodeOf(err) != apperror.CodeResourceExhausted || result.AttemptStatus != domain.AgentAttemptCrashed || len(provider.requests) != 0 {
		t.Fatalf("count overflow silently omitted work: %#v %v", result, err)
	}
}

func TestSpecialistTaskBriefRemainsCompleteDuringProtocolRepair(t *testing.T) {
	responses := specialistBriefResponses(t, 2)
	responses[0].Text = "invalid lifecycle response"
	provider := &specialistTestProvider{responses: responses}
	st, run, child, runner := newSubagentRunnerFixture(t, provider, domain.Budget{MaxTurns: 10}, 3, 256)
	const required = "PROTOCOL_REPAIR_REQUIRED_SCOPE_215"
	sendSpecialistBriefInstruction(t, st, run, child, required)
	result, err := runner.Step(t.Context(), run.ID, child.ID)
	if err != nil || result.ProtocolRepairs != 1 || len(provider.requests) != 2 {
		t.Fatalf("repair failed: %#v %v", result, err)
	}
	for _, request := range provider.requests {
		if len(request.Tools) != 0 || !strings.Contains(request.Messages[len(request.Messages)-1].Content, required) {
			t.Fatal("repair dropped task brief or granted tools")
		}
	}
}

func TestSpecialistTaskBriefRemainsCompleteDuringTransportRetry(t *testing.T) {
	provider := &specialistTestProvider{responses: specialistBriefResponses(t, 2), failures: []error{llm.NewProviderError(llm.OutcomeRetryable, "specialist-test", "bounded transient failure", nil)}}
	st, run, child, runner := newSubagentRunnerFixture(t, provider, domain.Budget{MaxTurns: 10}, 3, 256)
	runner.WithModelRetryPolicy(application.ModelRetryPolicy{MaxAttempts: 2})
	const required = "TRANSPORT_RETRY_REQUIRED_SCOPE_215"
	sendSpecialistBriefInstruction(t, st, run, child, required)
	result, err := runner.Step(t.Context(), run.ID, child.ID)
	if err != nil || result.ModelAttempts != 2 || len(provider.requests) != 2 {
		t.Fatalf("transport retry failed: %#v %v", result, err)
	}
	first := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Content
	second := provider.requests[1].Messages[len(provider.requests[1].Messages)-1].Content
	if first != second || !strings.Contains(second, required) {
		t.Fatal("transport retry changed persistent task constraints")
	}
}

func TestSpecialistTaskBriefRedactedSourcesCompleteAndSettleExactlyOnce(t *testing.T) {
	const raw = "TOKEN=synthetic-secret-215\nREQUIRED_REDACTION_TAIL_215"
	safe := redact.String(raw)
	if safe == raw || !strings.Contains(safe, "REQUIRED_REDACTION_TAIL_215") {
		t.Fatal("fixture must trigger redaction without losing the following constraint")
	}
	for _, source := range []string{"instruction", "owned_work"} {
		t.Run(source, func(t *testing.T) {
			provider := &specialistTestProvider{responses: specialistBriefResponses(t, 2)}
			st, run, child, runner := newSubagentRunnerFixture(t, provider, domain.Budget{MaxTurns: 10}, 3, 256)
			instructionText, workText := "keep the current scope", "complete the assigned review"
			if source == "instruction" {
				instructionText = raw
			} else {
				workText = raw
			}
			instruction := sendSpecialistBriefInstruction(t, st, run, child, instructionText)
			payload, err := domain.DecodeAgentInstructionPayload(instruction.PayloadJSON)
			if err != nil || payload.Instruction != redact.String(instructionText) {
				t.Fatalf("real instruction write did not redact its string value: %v", err)
			}
			workService := application.NewWorkItemService(st)
			work, err := workService.Create(t.Context(), application.CreateWorkItemRequest{
				RunID: run.ID, OwnerAgentID: child.ID, Title: workText, Description: workText,
				AcceptanceCriteria: []string{workText}, Priority: "high",
			})
			if err != nil {
				t.Fatal(err)
			}
			work, err = workService.Transition(t.Context(), work.ID, work.Version, domain.WorkItemBlocked, workText)
			if err != nil {
				t.Fatal(err)
			}
			if work.Title != redact.String(workText) || work.Description != redact.String(workText) ||
				len(work.AcceptanceCriteria) != 1 || work.AcceptanceCriteria[0] != redact.String(workText) ||
				work.BlockedReason != redact.String(workText) {
				t.Fatal("real owned-work writes did not redact their text")
			}
			provider.responses[1].Text = specialistResponse(t, domain.SpecialistAction{
				Version: domain.SpecialistLifecycleVersion, Kind: domain.SpecialistActionFinish,
				Message: "safe source delivery verified", Report: &domain.CompletionReport{
					Version: domain.CompletionReportVersion, Outcome: domain.CompletionPartial,
					Summary:     "required constraints and redaction preserved; assigned work remains blocked",
					WorkItemIDs: []string{work.ID},
				},
			})
			brief, err := domain.BuildSpecialistTaskBrief(run.ID, child.ID, child.ParentID,
				[]domain.AgentMessage{instruction}, []domain.WorkItem{work})
			if err != nil {
				t.Fatal(err)
			}
			completedIDs := []string{}
			for i := range 2 {
				result, err := runner.Step(t.Context(), run.ID, child.ID)
				wantStatus := domain.AgentAttemptContinued
				if i == 1 {
					wantStatus = domain.AgentAttemptFinished
				}
				if err != nil || result.AttemptStatus != wantStatus ||
					result.ModelOutcome != llm.OutcomeSuccess || result.Usage.TotalTokens != 5 {
					attempts, _ := st.ListAgentAttempts(t.Context(), child.ID)
					var savedUsage int64
					for _, attempt := range attempts {
						savedUsage += attempt.Usage.TotalTokens
					}
					t.Fatalf("successful provider response failed completion: calls=%d saved_usage=%d result=%#v err=%v",
						len(provider.requests), savedUsage, result, err)
				}
				completedIDs = append(completedIDs, result.AttemptID)
			}
			if len(provider.requests) != 2 {
				t.Fatalf("provider calls=%d want2", len(provider.requests))
			}
			assertInput := func(input string) {
				t.Helper()
				var envelope struct {
					Fingerprint  string `json:"task_brief_fingerprint"`
					Instructions []struct {
						Instruction string `json:"instruction"`
					} `json:"parent_instructions"`
					Work []domain.SpecialistTaskWorkContext `json:"work_items"`
				}
				if err := json.Unmarshal([]byte(input), &envelope); err != nil ||
					envelope.Fingerprint != brief.Fingerprint || len(envelope.Instructions) != 1 || len(envelope.Work) != 1 {
					t.Fatalf("delivered safe context lost its source binding: %v", err)
				}
				if envelope.Instructions[0].Instruction != payload.Instruction || envelope.Work[0].Title != work.Title ||
					envelope.Work[0].Description != work.Description || len(envelope.Work[0].AcceptanceCriteria) != 1 ||
					envelope.Work[0].AcceptanceCriteria[0] != work.AcceptanceCriteria[0] ||
					envelope.Work[0].BlockedReason != work.BlockedReason {
					t.Fatal("safe delivery or persistence changed a required task constraint")
				}
			}
			for _, request := range provider.requests {
				assertInput(request.Messages[len(request.Messages)-1].Content)
				encoded, _ := json.Marshal(request)
				if strings.Contains(string(encoded), "synthetic-secret-215") {
					t.Fatal("sensitive source text leaked into the provider request")
				}
			}
			messages, err := st.ListSessionMessages(t.Context(), child.SessionID, true)
			if err != nil || len(messages) != 4 {
				t.Fatalf("successful model input/output not persisted: count%d err%v", len(messages), err)
			}
			for _, message := range messages {
				if strings.Contains(message.Content, "synthetic-secret-215") {
					t.Fatal("sensitive source text leaked into session history")
				}
				if message.Role == "user" {
					assertInput(message.Content)
				}
			}
			attempts, err := st.ListAgentAttempts(t.Context(), child.ID)
			if err != nil || len(attempts) != 2 || attempts[0].ID == attempts[1].ID {
				t.Fatalf("expected two independent settled attempts: %v", err)
			}
			for _, attempt := range attempts {
				if (attempt.Status != domain.AgentAttemptContinued && attempt.Status != domain.AgentAttemptFinished) ||
					attempt.UsageRecordedAt == nil || attempt.Usage.TotalTokens != 5 {
					t.Fatal("successful attempt usage was not settled exactly once")
				}
			}
			updated, err := st.GetAgentNode(t.Context(), child.ID)
			if err != nil || updated.TokensUsed != 10 || updated.TurnsUsed != 2 || updated.Status != domain.AgentCompleted {
				t.Fatal("child usage totals lost or duplicated a successful call")
			}
			inbox, err := st.ListAgentMessages(t.Context(), child.ID, false, 10)
			if err != nil || len(inbox) != 1 || inbox[0].Status != domain.AgentMessageConsumed ||
				inbox[0].PayloadJSON != instruction.PayloadJSON {
				t.Fatal("instruction source was altered or not consumed once")
			}
			assertSpecialistEventCounts(t, st, run.ID, map[string]int{
				events.ModelStartedEvent: 2, events.ModelCompletedEvent: 2,
				events.AgentAttemptUsageRecordedEvent: 2, events.AgentMessageConsumedEvent: 1,
			})
			timeline, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range timeline {
				if strings.Contains(event.PayloadJSON, "synthetic-secret-215") {
					t.Fatal("sensitive source text leaked into event evidence")
				}
				if event.Type != events.ModelStartedEvent && event.Type != events.AgentMessageConsumedEvent {
					continue
				}
				var payload struct {
					AttemptID string                `json:"agent_attempt_id"`
					Context   llm.ModelContextAudit `json:"context"`
				}
				if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
					t.Fatal(err)
				}
				if event.Type == events.AgentMessageConsumedEvent && payload.AttemptID != completedIDs[0] {
					t.Fatal("later attempt consumed an already-delivered instruction")
				}
				if event.Type == events.ModelStartedEvent {
					bound := false
					for _, included := range payload.Context.Included {
						bound = bound || included.Kind == "specialist_task_brief" && included.SourceID == brief.Fingerprint
					}
					if !bound || (payload.AttemptID != completedIDs[0] && payload.AttemptID != completedIDs[1]) {
						t.Fatal("safe projection lost its original brief fingerprint or current attempt binding")
					}
				}
			}
		})
	}
}
