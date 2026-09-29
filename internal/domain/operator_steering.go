package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SessionMessageSubmissionProtocolVersion    = "session_message_submission.v1"
	SessionSteeringCancellationProtocolVersion = "session_steering_cancellation.v1"
	SessionSteeringRevisionProtocolVersion     = "session_steering_revision.v1"
	ThreadQueuedMessagesProtocolVersion        = "thread_queued_messages.v1"

	MaxOperatorSteeringContentBytes  = 16 * 1024
	MaxOperatorSteeringReasonBytes   = 2 * 1024
	MaxOperatorSteeringIdentityRunes = 256
	MaxPendingOperatorSteering       = 64
	MaxPendingOperatorSteeringBytes  = 256 * 1024
	MaxOperatorSteeringListLimit     = 200
)

type OperatorSteeringStatus string

type OperatorSteeringDeliveryMode string

const (
	OperatorSteeringNextTurn    OperatorSteeringDeliveryMode = "next_turn"
	OperatorSteeringCurrentTurn OperatorSteeringDeliveryMode = "steer"
)

func (m OperatorSteeringDeliveryMode) Valid() bool {
	return m == OperatorSteeringNextTurn || m == OperatorSteeringCurrentTurn
}

const (
	OperatorSteeringPending   OperatorSteeringStatus = "pending"
	OperatorSteeringCommitted OperatorSteeringStatus = "committed"
	OperatorSteeringCancelled OperatorSteeringStatus = "cancelled"
)

func (s OperatorSteeringStatus) Valid() bool {
	return s == OperatorSteeringPending || s == OperatorSteeringCommitted ||
		s == OperatorSteeringCancelled
}

type OperatorSteeringDeliveryStatus string

const (
	OperatorSteeringDeliveryPrepared   OperatorSteeringDeliveryStatus = "prepared"
	OperatorSteeringDeliveryCommitted  OperatorSteeringDeliveryStatus = "committed"
	OperatorSteeringDeliverySuperseded OperatorSteeringDeliveryStatus = "superseded"
	OperatorSteeringDeliveryCancelled  OperatorSteeringDeliveryStatus = "cancelled"
)

func (s OperatorSteeringDeliveryStatus) Valid() bool {
	return s == OperatorSteeringDeliveryPrepared || s == OperatorSteeringDeliveryCommitted ||
		s == OperatorSteeringDeliverySuperseded || s == OperatorSteeringDeliveryCancelled
}

type EnqueueOperatorSteeringRequest struct {
	RunID        string
	SessionID    string
	Content      string
	OperationKey string
	RequestedBy  string
	DeliveryMode OperatorSteeringDeliveryMode
	Images       []ImageReference
	Attachments  []FileAttachmentReference
}

type OperatorSteeringCancellationKind string

const (
	OperatorSteeringCancellationOperator    OperatorSteeringCancellationKind = "operator"
	OperatorSteeringCancellationRunTerminal OperatorSteeringCancellationKind = "run_terminal"
)

func (k OperatorSteeringCancellationKind) Valid() bool {
	return k == OperatorSteeringCancellationOperator ||
		k == OperatorSteeringCancellationRunTerminal
}

type CancelOperatorSteeringRequest struct {
	MessageID    string
	OperationKey string
	RequestedBy  string
	Reason       string
}

func (r CancelOperatorSteeringRequest) Normalize() (CancelOperatorSteeringRequest, error) {
	r.MessageID = strings.TrimSpace(r.MessageID)
	r.RequestedBy = strings.TrimSpace(r.RequestedBy)
	for label, value := range map[string]string{
		"message id": r.MessageID, "requester": r.RequestedBy,
	} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return CancelOperatorSteeringRequest{},
				fmt.Errorf("operator steering cancellation %s is invalid: %w", label, err)
		}
	}
	operationKey, err := NormalizeAgentOperationKey(r.OperationKey)
	if err != nil {
		return CancelOperatorSteeringRequest{},
			fmt.Errorf("operator steering cancellation operation key is invalid: %w", err)
	}
	r.OperationKey = operationKey
	reason, err := NormalizeOperatorSteeringCancellationReason(r.Reason)
	if err != nil {
		return CancelOperatorSteeringRequest{}, err
	}
	r.Reason = reason
	return r, nil
}

func (r EnqueueOperatorSteeringRequest) Normalize() (EnqueueOperatorSteeringRequest, error) {
	if r.DeliveryMode == "" {
		r.DeliveryMode = OperatorSteeringNextTurn
	}
	if !r.DeliveryMode.Valid() {
		return EnqueueOperatorSteeringRequest{}, errors.New("operator steering delivery mode is invalid")
	}
	r.RunID = strings.TrimSpace(r.RunID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.RequestedBy = strings.TrimSpace(r.RequestedBy)
	content, err := NormalizeThreadMessageContent(r.Content, r.Images, r.Attachments)
	if err != nil {
		return EnqueueOperatorSteeringRequest{}, err
	}
	r.Content = content
	for label, value := range map[string]string{
		"Run id": r.RunID, "Session id": r.SessionID, "requester": r.RequestedBy,
	} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return EnqueueOperatorSteeringRequest{}, fmt.Errorf("operator steering %s is invalid: %w", label, err)
		}
	}
	operationKey, err := NormalizeAgentOperationKey(r.OperationKey)
	if err != nil {
		return EnqueueOperatorSteeringRequest{}, fmt.Errorf("operator steering operation key is invalid: %w", err)
	}
	r.OperationKey = operationKey
	return r, nil
}

type OperatorSteeringMessage struct {
	ID                    string
	RunID                 string
	SessionID             string
	Sequence              int64
	Status                OperatorSteeringStatus
	DeliveryMode          OperatorSteeringDeliveryMode
	TargetAttemptID       string
	Prepared              bool
	Content               string
	ImageCount            int
	AttachmentCount       int
	ContentSHA256         string
	Revision              int64
	OriginalContent       string `json:"-"`
	OriginalContentSHA256 string `json:"-"`
	RequestedBy           string
	SessionMessageID      int64
	CreatedAt             time.Time
	CommittedAt           *time.Time
	CancelledAt           *time.Time
	EditedAt              *time.Time
}

func (m OperatorSteeringMessage) Validate() error {
	if m.DeliveryMode == "" {
		m.DeliveryMode = OperatorSteeringNextTurn
	}
	if !m.DeliveryMode.Valid() || (m.DeliveryMode == OperatorSteeringCurrentTurn && !ValidAgentID(m.TargetAttemptID)) ||
		(m.DeliveryMode == OperatorSteeringNextTurn && m.TargetAttemptID != "") {
		return errors.New("operator steering delivery binding is invalid")
	}
	for label, value := range map[string]string{
		"id": m.ID, "Run id": m.RunID, "Session id": m.SessionID, "requester": m.RequestedBy,
	} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return fmt.Errorf("operator steering %s is invalid: %w", label, err)
		}
	}
	content, err := NormalizeOperatorSteeringContent(m.Content)
	if (err != nil || content != m.Content) && !(m.Content == "" && ((m.ImageCount > 0 && m.ImageCount <= MaxThreadMessageImages) || (m.AttachmentCount > 0 && m.AttachmentCount <= MaxThreadMessageAttachments))) {
		return errors.New("operator steering content is not normalized")
	}
	if m.AttachmentCount < 0 || m.AttachmentCount > MaxThreadMessageAttachments {
		return errors.New("operator steering attachment count is invalid")
	}
	if m.ImageCount < 0 || m.ImageCount > MaxThreadMessageImages {
		return errors.New("operator steering image count is invalid")
	}
	if m.ContentSHA256 != OperatorSteeringContentSHA256(m.Content) {
		return errors.New("operator steering content digest does not match")
	}
	original, originalErr := NormalizeOperatorSteeringContent(m.OriginalContent)
	if (originalErr != nil || original != m.OriginalContent) && !(m.OriginalContent == "" &&
		((m.ImageCount > 0 && m.ImageCount <= MaxThreadMessageImages) ||
			(m.AttachmentCount > 0 && m.AttachmentCount <= MaxThreadMessageAttachments))) {
		return errors.New("operator steering original content is not normalized")
	}
	if m.Revision < 0 || m.OriginalContentSHA256 != OperatorSteeringContentSHA256(m.OriginalContent) {
		return errors.New("operator steering original content or revision is invalid")
	}
	if m.Revision == 0 {
		if m.Content != m.OriginalContent || m.ContentSHA256 != m.OriginalContentSHA256 || m.EditedAt != nil {
			return errors.New("unrevised operator steering must match its original content")
		}
	} else if m.EditedAt == nil || m.EditedAt.IsZero() || m.EditedAt.Before(m.CreatedAt) {
		return errors.New("revised operator steering requires an edit time")
	}
	if m.Sequence <= 0 || !m.Status.Valid() || m.CreatedAt.IsZero() {
		return errors.New("operator steering sequence, status, and creation time are required")
	}
	if m.Prepared && m.Status != OperatorSteeringPending {
		return errors.New("only pending operator steering can have a prepared delivery")
	}
	switch m.Status {
	case OperatorSteeringPending:
		if m.SessionMessageID != 0 || m.CommittedAt != nil || m.CancelledAt != nil {
			return errors.New("pending operator steering cannot have terminal delivery data")
		}
	case OperatorSteeringCommitted:
		if m.SessionMessageID <= 0 || m.CommittedAt == nil || m.CommittedAt.IsZero() ||
			m.CommittedAt.Before(m.CreatedAt) || m.CancelledAt != nil {
			return errors.New("committed operator steering requires one Session message and commit time")
		}
	case OperatorSteeringCancelled:
		if m.SessionMessageID != 0 || m.CommittedAt != nil || m.CancelledAt == nil ||
			m.CancelledAt.IsZero() || m.CancelledAt.Before(m.CreatedAt) {
			return errors.New("cancelled operator steering requires only a cancellation time")
		}
	}
	return nil
}

type OperatorSteeringDelivery struct {
	ID         string
	MessageID  string
	RunID      string
	AttemptID  string
	Turn       int
	Status     OperatorSteeringDeliveryStatus
	PreparedAt time.Time
	TerminalAt *time.Time
}

func (d OperatorSteeringDelivery) Validate() error {
	for label, value := range map[string]string{
		"id": d.ID, "message id": d.MessageID, "Run id": d.RunID, "attempt id": d.AttemptID,
	} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return fmt.Errorf("operator steering delivery %s is invalid: %w", label, err)
		}
	}
	if d.Turn <= 0 || !d.Status.Valid() || d.PreparedAt.IsZero() {
		return errors.New("operator steering delivery turn, status, and preparation time are required")
	}
	if d.Status == OperatorSteeringDeliveryPrepared {
		if d.TerminalAt != nil {
			return errors.New("prepared operator steering delivery cannot be terminal")
		}
	} else if d.TerminalAt == nil || d.TerminalAt.IsZero() || d.TerminalAt.Before(d.PreparedAt) {
		return errors.New("terminal operator steering delivery requires a valid terminal time")
	}
	return nil
}

type OperatorSteeringEnqueueResult struct {
	Message  OperatorSteeringMessage
	Replayed bool
}

type OperatorSteeringCancellation struct {
	ID           string
	MessageID    string
	RunID        string
	Kind         OperatorSteeringCancellationKind
	RequestedBy  string
	Reason       string
	ReasonSHA256 string
	CreatedAt    time.Time
}

func (c OperatorSteeringCancellation) Validate() error {
	for label, value := range map[string]string{
		"id": c.ID, "message id": c.MessageID, "Run id": c.RunID,
		"requester": c.RequestedBy,
	} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return fmt.Errorf("operator steering cancellation %s is invalid: %w", label, err)
		}
	}
	reason, err := NormalizeOperatorSteeringCancellationReason(c.Reason)
	if err != nil || reason != c.Reason {
		return errors.New("operator steering cancellation reason is not normalized")
	}
	if !c.Kind.Valid() || c.CreatedAt.IsZero() {
		return errors.New("operator steering cancellation kind and creation time are required")
	}
	if c.ReasonSHA256 != OperatorSteeringContentSHA256(c.Reason) {
		return errors.New("operator steering cancellation reason digest does not match")
	}
	return nil
}

type OperatorSteeringCancellationResult struct {
	Cancellation OperatorSteeringCancellation
	Message      OperatorSteeringMessage
	Replayed     bool
}

type OperatorSteeringQueueSummary struct {
	RunID     string
	Pending   int
	Prepared  int
	Committed int
	Cancelled int
	Next      *OperatorSteeringMessage
}

func NormalizeOperatorSteeringContent(value string) (string, error) {
	return normalizeOperatorSteeringText(value, MaxOperatorSteeringContentBytes,
		"operator steering content")
}

func NormalizeOperatorSteeringCancellationReason(value string) (string, error) {
	return normalizeOperatorSteeringText(value, MaxOperatorSteeringReasonBytes,
		"operator steering cancellation reason")
}

func normalizeOperatorSteeringText(value string, maxBytes int, label string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be valid UTF-8", label)
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimSpace(value)
	if value == "" || len([]byte(value)) > maxBytes {
		return "", fmt.Errorf("%s must be between 1 and %d bytes", label, maxBytes)
	}
	for _, current := range value {
		if current == 0 || (unicode.IsControl(current) && current != '\n' && current != '\t') {
			return "", fmt.Errorf("%s contains an unsupported control character", label)
		}
	}
	return value, nil
}

func OperatorSteeringContentSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func validateOperatorSteeringIdentity(value string) error {
	if value == "" || !utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		utf8.RuneCountInString(value) > MaxOperatorSteeringIdentityRunes {
		return errors.New("value must be normalized and bounded UTF-8")
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return errors.New("value cannot contain control characters")
		}
	}
	return nil
}
