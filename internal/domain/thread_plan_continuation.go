package domain

import "time"

// ThreadPlanCompletionSource references an original manual attestation or an
// on-demand work-item completion event. Neither is a current automated result.
type ThreadPlanCompletionSource struct {
	WorkItemID        string
	SourceRunID       string
	SourceWorkItemID  string
	CheckpointID      string
	HandoffNoteID     string
	CompletionEventID string
	CompletedAt       time.Time
}
