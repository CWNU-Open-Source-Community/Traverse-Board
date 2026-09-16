package httpapi

import "cyberagent-workbench/internal/domain"

// ThreadTurnFailureReferenceView identifies an existing sealed outcome. It is
// display provenance, not permission to retry a tool or reopen the failed turn.
type ThreadTurnFailureReferenceView struct {
	ThreadID      string `json:"thread_id"`
	RunID         string `json:"run_id"`
	MessageID     string `json:"message_id"`
	EventSequence int64  `json:"event_sequence"`
}

func threadTurnFailureReference(failure *domain.ThreadTurnFailure) *ThreadTurnFailureReferenceView {
	if failure == nil || !domain.ValidAgentID(failure.ThreadID) || !domain.ValidAgentID(failure.RunID) ||
		!domain.ValidAgentID(failure.MessageID) || failure.EventSequence <= 0 {
		return nil
	}
	return &ThreadTurnFailureReferenceView{ThreadID: failure.ThreadID, RunID: failure.RunID,
		MessageID: failure.MessageID, EventSequence: failure.EventSequence}
}
