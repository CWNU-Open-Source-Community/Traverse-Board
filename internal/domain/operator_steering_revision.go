package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const QueueRevisionUnchangedProtocolVersion = "queue_revision_unchanged.v1"

type ReviseOperatorSteeringRequest struct {
	SessionID        string
	MessageID        string
	ExpectedRevision int64
	Content          string
	OperationKey     string
	RequestedBy      string
}

func (r ReviseOperatorSteeringRequest) Normalize() (ReviseOperatorSteeringRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.MessageID = strings.TrimSpace(r.MessageID)
	r.RequestedBy = strings.TrimSpace(r.RequestedBy)
	for label, value := range map[string]string{"Session id": r.SessionID, "message id": r.MessageID, "requester": r.RequestedBy} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return ReviseOperatorSteeringRequest{}, fmt.Errorf("operator steering revision %s is invalid: %w", label, err)
		}
	}
	if r.ExpectedRevision < 0 {
		return ReviseOperatorSteeringRequest{}, errors.New("operator steering expected revision cannot be negative")
	}
	key, err := NormalizeAgentOperationKey(r.OperationKey)
	if err != nil {
		return ReviseOperatorSteeringRequest{}, fmt.Errorf("operator steering revision operation key is invalid: %w", err)
	}
	r.OperationKey = key
	return r, nil
}

type OperatorSteeringRevisionReceipt struct {
	ID                 string
	MessageID          string
	RunID              string
	SessionID          string
	FromRevision       int64
	ToRevision         int64
	OldContentSHA256   string
	NewContentSHA256   string
	RequestedBy        string
	CreatedAt          time.Time
	OperationKeyDigest string
	RequestFingerprint string
}

func (r OperatorSteeringRevisionReceipt) Validate() error {
	for label, value := range map[string]string{"id": r.ID, "message id": r.MessageID, "Run id": r.RunID, "Session id": r.SessionID, "requester": r.RequestedBy} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return fmt.Errorf("operator steering revision receipt %s is invalid: %w", label, err)
		}
	}
	if r.FromRevision < 0 || r.ToRevision != r.FromRevision+1 || r.CreatedAt.IsZero() {
		return errors.New("operator steering revision receipt version or creation time is invalid")
	}
	for _, digest := range []string{r.OldContentSHA256, r.NewContentSHA256, r.OperationKeyDigest, r.RequestFingerprint} {
		if len(digest) != 64 {
			return errors.New("operator steering revision receipt digest is invalid")
		}
	}
	return nil
}

type ReviseOperatorSteeringResult struct {
	Message  OperatorSteeringMessage
	Receipt  OperatorSteeringRevisionReceipt
	Replayed bool
}

type OperatorSteeringRevisionUnchangedError struct {
	RunID                   string
	SessionID               string
	MessageID               string
	ExpectedRevision        int64
	OperationKeySHA256      string
	RequestContentSHA256    string
	NormalizedContentSHA256 string
	CurrentContentSHA256    string
}

func (e *OperatorSteeringRevisionUnchangedError) Error() string {
	return "operator steering revision leaves the normalized content unchanged"
}

type OperatorSteeringRevisionInspectionState string

const (
	OperatorSteeringRevisionAbsent OperatorSteeringRevisionInspectionState = "absent"
	OperatorSteeringRevisionSealed OperatorSteeringRevisionInspectionState = "sealed"
)

type OperatorSteeringRevisionInspection struct {
	State   OperatorSteeringRevisionInspectionState
	Message *OperatorSteeringObservationMessage
	Receipt *OperatorSteeringRevisionReceipt
}

type OperatorSteeringObservationMessage struct {
	ID        string
	RunID     string
	SessionID string
	Revision  int64
	Status    OperatorSteeringStatus
}

func (m OperatorSteeringObservationMessage) Validate() error {
	for label, value := range map[string]string{"id": m.ID, "Run id": m.RunID, "Session id": m.SessionID} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return fmt.Errorf("operator steering observation %s is invalid: %w", label, err)
		}
	}
	if m.Revision < 0 || !m.Status.Valid() {
		return errors.New("operator steering observation revision or status is invalid")
	}
	return nil
}

type OperatorSteeringCancellationReceipt struct {
	CancellationID     string
	MessageID          string
	RunID              string
	SessionID          string
	Kind               OperatorSteeringCancellationKind
	RequestedBy        string
	ReasonSHA256       string
	CreatedAt          time.Time
	OperationKeyDigest string
	RequestFingerprint string
}

func (r OperatorSteeringCancellationReceipt) Validate() error {
	for label, value := range map[string]string{"cancellation id": r.CancellationID,
		"message id": r.MessageID, "Run id": r.RunID, "Session id": r.SessionID,
		"requester": r.RequestedBy} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return fmt.Errorf("operator steering cancellation receipt %s is invalid: %w", label, err)
		}
	}
	if r.Kind != OperatorSteeringCancellationOperator || r.CreatedAt.IsZero() {
		return errors.New("operator steering cancellation receipt kind or creation time is invalid")
	}
	for _, digest := range []string{r.ReasonSHA256, r.OperationKeyDigest, r.RequestFingerprint} {
		if len(digest) != 64 {
			return errors.New("operator steering cancellation receipt digest is invalid")
		}
	}
	return nil
}

type OperatorSteeringCancellationInspectionState string

const (
	OperatorSteeringCancellationAbsent OperatorSteeringCancellationInspectionState = "absent"
	OperatorSteeringCancellationSealed OperatorSteeringCancellationInspectionState = "sealed"
)

type OperatorSteeringCancellationInspection struct {
	State   OperatorSteeringCancellationInspectionState
	Message *OperatorSteeringObservationMessage
	Receipt *OperatorSteeringCancellationReceipt
}

type QueuedOperatorSteeringMessage struct {
	Message     OperatorSteeringMessage
	Images      []WorkspaceImage
	Attachments []WorkspaceFileAttachment
}

type ThreadQueuedMessagesSnapshot struct {
	ProtocolVersion string
	ThreadID        string
	RunID           string
	SessionID       string
	RunStatus       RunStatus
	Messages        []QueuedOperatorSteeringMessage
}
