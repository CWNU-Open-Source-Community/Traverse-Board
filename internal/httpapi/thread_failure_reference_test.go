package httpapi

import (
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestThreadFailureReferenceProjectsOnlyBoundedSealedIdentity(t *testing.T) {
	failure := domain.ThreadTurnFailure{ThreadID: "thread-1", RunID: "run-1", MessageID: "message-1", EventSequence: 42,
		AttemptID: "private-attempt-not-part-of-contract", ErrorCode: "FAILED_PRECONDITION"}
	view := threadTurnFailureReference(&failure)
	body, err := json.Marshal(view)
	if err != nil || string(body) != `{"thread_id":"thread-1","run_id":"run-1","message_id":"message-1","event_sequence":42}` {
		t.Fatalf("failure reference exposed more than its exact identity: %s %v", body, err)
	}
	for _, invalid := range []*domain.ThreadTurnFailure{nil, {}, {ThreadID: "thread-1", RunID: "run-1", MessageID: "message-1"}} {
		if threadTurnFailureReference(invalid) != nil {
			t.Fatal("unsealed failure produced a reference")
		}
	}
}
