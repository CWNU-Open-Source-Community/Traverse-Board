package domain

// ThreadRequestObservation is a read-only snapshot of an existing request.
// Not received means absent at this read, not that an in-flight HTTP request
// cannot arrive later. A completed creation says nothing about task execution.
type ThreadRequestObservation struct {
	Kind               string
	State              string
	Settled            bool
	WorkspaceID        string
	ThreadID           string
	RunID              string
	SessionID          string
	MessageID          string
	MessageStatus      OperatorSteeringStatus
	RequestFingerprint string
	Failure            *ThreadTurnFailure
}
