package application_test

import (
	"context"
	"sync"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
)

type planQueuedCorrectionProvider struct {
	*scriptedToolProvider
	once   sync.Once
	before func()
}

func (p *planQueuedCorrectionProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.once.Do(p.before)
	return p.scriptedToolProvider.Chat(ctx, req)
}
func (p *planQueuedCorrectionProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, req)
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

func TestThreadPlanQueuedCorrectionBeforeProposalStillInvalidatesOldPlan(t *testing.T) {
	st, run, scripted, _ := newThreadPlanFixture(t, toolResponse("queued-correction-plan", "plan_delivery_propose", planDeliveryTestPayload), planWait(), planWait())
	p := &planQueuedCorrectionProvider{scriptedToolProvider: scripted}
	router := llm.NewRouter(llm.ModelRef{Provider: p.Name(), Model: "model"})
	router.RegisterProvider(p)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st), application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()))
	correction := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), Content: "Queued correction: preserve external API compatibility.", OperationKey: "correction-before-proposal-created-at", RequestedBy: "operator"}
	p.before = func() {
		if _, err := turns.Execute(t.Context(), correction); err != nil {
			t.Error(err)
		}
	}
	sendThreadPlanInput(t, turns, run, "initial-long-running-plan", "Prepare the plan")
	proposal := latestThreadPlan(t, st, run.ID)
	observed, err := st.InspectThreadTurnRequest(t.Context(), correction.ThreadID, correction.OperationKey, correction.RequestedBy)
	if err != nil {
		t.Fatal(err)
	}
	message, err := st.GetOperatorSteering(t.Context(), observed.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if !message.CreatedAt.Before(proposal.CreatedAt) || message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("fixture did not prove queued-before/consumed-after: %+v proposal=%s", message, proposal.CreatedAt)
	}
	if _, err := turns.ControlPlan(t.Context(), confirmThreadPlan(run, proposal, "old-queued-proposal")); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("queued correction missed: %v", err)
	}
	if len(scripted.Requests()) != 3 {
		t.Fatal("stale plan called model")
	}
}
