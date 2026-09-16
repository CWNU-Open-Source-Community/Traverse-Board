package application_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runactivity"
)

func TestThreadRejectedResponseDoesNotInventSuccessThroughFormatRepair(t *testing.T) {
	for _, tc := range []struct {
		name, stage, notice string
		response            *llm.ChatResponse
		wantCalls, repairs  int
	}{
		{"invalid_tool", "tool_request_rejected", "该批请求尚未执行", toolResponse("bad-note", "note_create", `{"title":123}`), 2, 1},
		{"empty_reply", "empty_model_response", "模型没有返回有效答复", textResponse(" \r\n\t "), 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
				tc.response,
				textResponse(rootActionResponse(domain.RootActionFinish, "invented-success-never-executed", "done", "")),
			}}
			st, turns, request := threadControlFixture(t, provider)
			first, err := turns.Execute(t.Context(), request)
			var failed *application.ThreadTurnFailedError
			if !errors.As(err, &failed) || len(provider.Requests()) != tc.wantCalls || first.Execution == nil {
				t.Fatalf("invalid response must end honestly without a tool-less replacement answer: calls=%d err=%v", len(provider.Requests()), err)
			}
			runID := first.Submission.Run.ID
			if failed.Failure == nil || failed.Failure.ThreadID != request.ThreadID || failed.Failure.RunID != runID ||
				failed.Failure.MessageID != first.Submission.Message.ID || failed.Failure.EventSequence <= 0 {
				t.Fatalf("sealed failure lost its exact public reference: %#v", failed.Failure)
			}
			sealedFailure := *failed.Failure
			eventList, err := st.ListRunEvents(t.Context(), runID)
			if err != nil {
				t.Fatal(err)
			}
			if countEventType(eventList, events.ProtocolRepairRequestedEvent) != tc.repairs ||
				countEventType(eventList, events.ProtocolRepairFailedEvent) != tc.repairs ||
				countEventType(eventList, events.SupervisorToolBatchEvent) != 0 {
				t.Fatal("correction exceeded its bound or a rejected request was recorded as tool execution")
			}
			foundStage := false
			for _, event := range eventList {
				if event.Type == events.ThreadTurnFailedEvent {
					if event.Sequence != sealedFailure.EventSequence || event.SubjectID != sealedFailure.MessageID {
						t.Fatal("failure reference differs from the persisted outcome")
					}
					var payload map[string]any
					if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
						t.Fatal(err)
					}
					foundStage = payload["failure_stage"] == tc.stage
				}
				if strings.Contains(event.PayloadJSON, "invented-success-never-executed") {
					t.Fatal("the unused repair answer leaked into durable history")
				}
			}
			projection, err := runactivity.Build(runID, eventList, false)
			if err != nil {
				t.Fatal(err)
			}
			foundNotice := false
			for _, item := range projection.Items {
				foundNotice = foundNotice || strings.Contains(item.Detail, tc.notice)
			}
			if !foundStage || !foundNotice {
				t.Fatalf("missing factual failure stage or notice: stage=%v notice=%v", foundStage, foundNotice)
			}
			checkpoint, _, err := st.GetSupervisorCheckpoint(t.Context(), runID)
			if err != nil || checkpoint.TotalTokens != int64(4*tc.wantCalls) || checkpoint.Phase != domain.SupervisorIdle {
				t.Fatalf("rejected response usage or turn closure lost: %#v %v", checkpoint, err)
			}
			if _, err = turns.Execute(t.Context(), request); !errors.As(err, &failed) || len(provider.Requests()) != tc.wantCalls {
				t.Fatal("confirmation of the original request started another model call")
			}
			if failed.Failure == nil || *failed.Failure != sealedFailure {
				t.Fatal("original-key confirmation changed the sealed failure reference")
			}
			provider.mu.Lock()
			provider.responses = []*llm.ChatResponse{textResponse(rootActionResponse(domain.RootActionFinish, "new request completed", "done", ""))}
			provider.mu.Unlock()
			request.OperationKey += "-next"
			request.Content = "Continue with the corrected request"
			next, err := turns.Execute(t.Context(), request)
			if err != nil || next.Submission.Run.ID != runID || len(provider.Requests()) != tc.wantCalls+1 {
				t.Fatalf("ordinary new message did not continue the same conversation: %#v %v", next, err)
			}
		})
	}
}

func TestThreadEmptyResponsePreservesEarlierToolEffectsAndTheirEvidence(t *testing.T) {
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("real-note", "note_create", `{"title":"Observed once","content":"Preserve this completed effect."}`),
		textResponse("\n \t"),
		textResponse(rootActionResponse(domain.RootActionFinish, "invented-success-never-executed", "done", "")),
	}}
	st, turns, request := threadControlFixture(t, provider)
	first, err := turns.Execute(t.Context(), request)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) || len(provider.Requests()) != 2 {
		t.Fatalf("empty post-tool response was converted into success: %v", err)
	}
	notes, err := st.ListNotes(t.Context(), domain.NoteFilter{RunID: first.Submission.Run.ID})
	if err != nil || len(notes) != 1 {
		t.Fatalf("completed tool effect was lost or repeated: %#v %v", notes, err)
	}
	provider.mu.Lock()
	provider.responses = []*llm.ChatResponse{textResponse(rootActionResponse(domain.RootActionFinish, "using the saved result", "done", ""))}
	provider.mu.Unlock()
	request.OperationKey += "-next"
	request.Content = "Continue without creating the note again"
	if _, err := turns.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	requests := provider.Requests()
	sawEvidence := false
	for _, message := range requests[len(requests)-1].Messages {
		sawEvidence = sawEvidence || strings.Contains(message.Content, "note_create") && strings.Contains(message.Content, "result SHA256")
	}
	if !sawEvidence {
		t.Fatal("ordinary continuation lost the earlier completed tool result")
	}
	notes, err = st.ListNotes(t.Context(), domain.NoteFilter{RunID: first.Submission.Run.ID})
	if err != nil || len(notes) != 1 {
		t.Fatal("continuation repeated the completed side effect")
	}
}
