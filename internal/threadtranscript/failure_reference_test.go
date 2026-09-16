package threadtranscript

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/events"
)

func TestFailureNoticeReferencesExactDurableSubjectWithoutChangingEventIdentity(t *testing.T) {
	for _, tc := range []struct{ kind, source, payload string }{
		{events.ThreadTurnFailedEvent, "thread_turn", `{"error_code":"FAILED_PRECONDITION","failure_stage":"empty_model_response"}`},
		{events.RunExecutionHandoffCompletedEvent, "run_execution_handoff", `{"requested_by":"approval_continuation","status":"failed","error_code":"failed_precondition"}`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			record := eventSource("run-1", 1, 24, tc.kind, tc.payload, time.Now().UTC())
			record.Event.Source = tc.source
			record.Event.SubjectID = "exact-failure-subject"
			original := *record.Event
			items, err := Build("thread-1", []Source{record})
			if err != nil || len(items) != 1 || items[0].SourceRef != original.SubjectID ||
				items[0].Sequence != original.Sequence || items[0].ID != original.EventID ||
				items[0].CanonicalID != original.EventID || items[0].Stage != StageBlocked {
				t.Fatalf("failure identity changed or disappeared: %#v %v", items, err)
			}
			if *record.Event != original {
				t.Fatal("read projection changed the original event")
			}
			record.Event.Source = "unrelated_source"
			items, err = Build("thread-1", []Source{record})
			if err != nil || (len(items) > 0 && items[0].SourceRef != "") {
				t.Fatalf("unrelated event source borrowed a failure reference: %#v %v", items, err)
			}
		})
	}
}
