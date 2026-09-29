package application_test

import (
	"context"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/threadtranscript"
)

func TestPromotionReachesProviderOnceAndPreservesPairedHistory(t *testing.T) {
	provider := &gatedThreadProvider{started: make(chan struct{}), release: make(chan struct{}),
		lifecycleProvider: lifecycleProvider{responses: []string{
			rootActionResponse(domain.RootActionFinish, "obsolete answer", "complete", ""),
			rootActionResponse(domain.RootActionFinish, "guided answer", "complete", ""),
		}}}
	st, turns, request := threadControlFixture(t, provider)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(ctx, request); done <- err }()
	awaitTurnSignal(t, provider.started)
	queuedRequest := request
	queuedRequest.Content, queuedRequest.OperationKey = "only inspect frontend; preserve backend", "promotion-queued-input-0001"
	queued, err := turns.Execute(t.Context(), queuedRequest)
	if err != nil || queued.ExecutionStarted {
		t.Fatalf("queue=%#v %v", queued, err)
	}
	state, err := turns.ExecutionState(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ListThreadQueuedMessages(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	message := queued.Submission.Message
	input := domain.PromoteOperatorSteeringRequest{SessionID: message.SessionID, MessageID: message.ID, ExpectedRevision: message.Revision,
		ExpectedContentSHA256: message.ContentSHA256, ExpectedAttemptID: snapshot.CurrentAttemptID, ExpectedExecutionID: state.ExecutionID,
		OperationKey: "promotion-live-execution-0001", RequestedBy: request.RequestedBy}
	// The same durable attempt/lease without this process's owner is insufficient.
	orphan := application.NewThreadTurnService(st, nil, nil)
	observed, err := orphan.InspectCurrentSteeringPromotion(t.Context(), input.SessionID, input.MessageID, input.OperationKey, input.RequestedBy)
	if err != nil || observed.ExecutionObserved || observed.ExecutionID != "" || observed.State != domain.OperatorSteeringRevisionAbsent {
		t.Fatalf("orphan observation=%#v %v", observed, err)
	}
	refusedInput := input
	refusedInput.OperationKey = "promotion-cross-instance-rejected-0001"
	refused, err := orphan.PromoteCurrentSteering(t.Context(), refusedInput)
	if err != nil || refused.Rejection == nil {
		t.Fatalf("orphan rejection=%#v %v", refused, err)
	}
	late, err := turns.PromoteCurrentSteering(t.Context(), refusedInput)
	if err != nil || late.Rejection == nil || !late.Replayed || late.Rejection.ID != refused.Rejection.ID {
		t.Fatalf("delayed live POST escaped rejection=%#v %v", late, err)
	}
	stale := input
	stale.ExpectedExecutionID = "old-process-same-attempt"
	stale.OperationKey = "promotion-stale-execution-0001"
	if rejected, err := turns.PromoteCurrentSteering(t.Context(), stale); err != nil || rejected.Rejection == nil {
		t.Fatalf("stale execution rejection=%#v %v", rejected, err)
	}
	observed, err = turns.InspectCurrentSteeringPromotion(t.Context(), input.SessionID, input.MessageID, input.OperationKey, input.RequestedBy)
	if err != nil || observed.ExecutionObserved || observed.ExecutionID != "" {
		t.Fatalf("live observation=%#v %v", observed, err)
	}
	promoted, err := turns.PromoteCurrentSteering(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	if err := awaitTurnResult(t, done); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 {
		t.Fatalf("queue was executed again, provider calls=%d", provider.calls)
	}
	for i, req := range provider.requests {
		count := 0
		for _, m := range req.Messages {
			if strings.Contains(m.Content, queuedRequest.Content) {
				count++
			}
		}
		if count != i {
			t.Fatalf("request %d contains correction %d times", i, count)
		}
	}
	history, err := st.ListSessionMessages(t.Context(), message.SessionID, true)
	if err != nil || len(history) != 3 || history[0].Content != request.Content || history[1].Content != queuedRequest.Content || history[2].Content != "guided answer" {
		t.Fatalf("history=%#v %v", history, err)
	}
	old, err := st.GetOperatorSteering(t.Context(), message.ID)
	if err != nil || old.Status != domain.OperatorSteeringCancelled || old.SessionMessageID != 0 {
		t.Fatalf("old=%#v %v", old, err)
	}
	replacement, err := st.GetOperatorSteering(t.Context(), promoted.Receipt.ReplacementMessageID)
	if err != nil || replacement.Status != domain.OperatorSteeringCommitted || replacement.Prepared {
		t.Fatalf("replacement=%#v %v", replacement, err)
	}
	// Each one-source page must retain provenance even without the paired row.
	source, err := st.ListThreadTranscriptSourceBefore(t.Context(), request.ThreadID, 0, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundOld, foundNew := false, false
	for _, s := range source {
		items, err := threadtranscript.Build(request.ThreadID, []threadtranscript.Source{s})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.PromotedToMessageID != "" {
				foundOld = true
				if item.SourceRef != message.ID || item.PromotedToMessageID != replacement.ID || item.InstructionAuthorized || item.Status != "cancelled" || !strings.Contains(item.Title, "转为") {
					t.Fatalf("old projection=%#v", item)
				}
			}
			if item.PromotedFromMessageID != "" {
				foundNew = true
				if item.SourceRef != replacement.ID || item.PromotedFromMessageID != message.ID || !item.InstructionAuthorized || item.DeliveryMode != "steer" {
					t.Fatalf("new projection=%#v", item)
				}
			}
		}
	}
	if !foundOld || !foundNew {
		t.Fatal("promotion pair missing from paged history")
	}
	// Exact retry and receipt inspection still work after the live owner ends.
	replay, err := orphan.PromoteCurrentSteering(t.Context(), input)
	if err != nil || !replay.Replayed || replay.Receipt.ID != promoted.Receipt.ID {
		t.Fatalf("terminal replay=%#v %v", replay, err)
	}
	observed, err = orphan.InspectCurrentSteeringPromotion(t.Context(), input.SessionID, input.MessageID, input.OperationKey, input.RequestedBy)
	if err != nil || observed.State != domain.OperatorSteeringRevisionSealed || observed.Receipt.ID != promoted.Receipt.ID {
		t.Fatalf("sealed=%#v %v", observed, err)
	}
}

func TestPromotionStopWinsAndUnknownIntentBecomesObsolete(t *testing.T) {
	provider := blockingProvider{started: make(chan struct{}, 2)}
	st, turns, request := threadControlFixture(t, provider)
	done := make(chan error, 1)
	go func() { _, err := turns.Execute(t.Context(), request); done <- err }()
	awaitTurnSignal(t, provider.started)
	queuedRequest := request
	queuedRequest.Content = "keep this queued"
	queuedRequest.OperationKey = "promotion-stop-queued-0001"
	queued, err := turns.Execute(t.Context(), queuedRequest)
	if err != nil {
		t.Fatal(err)
	}
	state, err := turns.ExecutionState(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ListThreadQueuedMessages(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	m := queued.Submission.Message
	input := domain.PromoteOperatorSteeringRequest{SessionID: m.SessionID, MessageID: m.ID, ExpectedContentSHA256: m.ContentSHA256,
		ExpectedAttemptID: snapshot.CurrentAttemptID, ExpectedExecutionID: state.ExecutionID, OperationKey: "promotion-stop-delayed-0001", RequestedBy: request.RequestedBy}
	if _, err := turns.Interrupt(t.Context(), request.ThreadID, state.ExecutionID); err != nil {
		t.Fatal(err)
	}
	observed, err := turns.InspectCurrentSteeringPromotion(t.Context(), m.SessionID, m.ID, input.OperationKey, input.RequestedBy)
	if err != nil || observed.ExecutionObserved || observed.ExecutionID != "" || observed.State != domain.OperatorSteeringRevisionAbsent {
		t.Fatalf("stop observation=%#v %v", observed, err)
	}
	if rejected, err := turns.PromoteCurrentSteering(t.Context(), input); err != nil || rejected.Rejection == nil {
		t.Fatalf("stop rejection=%#v %v", rejected, err)
	}
	_ = awaitTurnResult(t, done)
	old, err := st.GetOperatorSteering(t.Context(), m.ID)
	if err != nil || old.Status != domain.OperatorSteeringPending {
		t.Fatalf("stop lost queue=%#v %v", old, err)
	}
}
