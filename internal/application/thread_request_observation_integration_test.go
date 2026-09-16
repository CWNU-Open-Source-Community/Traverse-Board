package application_test

import (
	"errors"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestThreadRequestObservationSealedFailureDoesNotRepeatCompletedTool(t *testing.T) {
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{toolResponse("observe-before-failure", "note_create", `{"title":"Observed exactly once","content":"Do not repeat this tool on reopening."}`)}}
	st, turns, request := threadControlFixture(t, provider)
	first, err := turns.Execute(t.Context(), request)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) || failed.Failure == nil {
		t.Fatalf("expected sealed failure: %#v %v", first, err)
	}
	before, err := st.ExportThread(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		got, err := st.InspectThreadTurnRequest(t.Context(), request.ThreadID, request.OperationKey, request.RequestedBy)
		if err != nil || got.State != "failed" || !got.Settled || got.Failure == nil || got.Failure.EventSequence != failed.Failure.EventSequence || got.MessageID != failed.Failure.MessageID {
			t.Fatalf("failure=%#v %v", got, err)
		}
	}
	after, err := st.ExportThread(t.Context(), request.ThreadID)
	after.ExportedAt = before.ExportedAt
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed history: %v", err)
	}
	notes, err := st.ListNotes(t.Context(), domain.NoteFilter{RunID: first.Submission.Run.ID})
	if err != nil || len(notes) != 1 {
		t.Fatalf("tool repeated: %#v %v", notes, err)
	}
}
