package domain

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const SessionSteeringPromotionProtocolVersion = "session_steering_promotion.v1"

// Promotion preserves the old queue item and its revision as history, and
// creates one attempt-bound correction in the same durable transaction.
type PromoteOperatorSteeringRequest struct {
	SessionID             string
	MessageID             string
	ExpectedRevision      int64
	ExpectedContentSHA256 string
	ExpectedAttemptID     string
	ExpectedExecutionID   string
	OperationKey          string
	RequestedBy           string
}

func (r PromoteOperatorSteeringRequest) Normalize() (PromoteOperatorSteeringRequest, error) {
	r.SessionID, r.MessageID, r.RequestedBy = strings.TrimSpace(r.SessionID), strings.TrimSpace(r.MessageID), strings.TrimSpace(r.RequestedBy)
	for _, value := range []string{r.SessionID, r.MessageID, r.RequestedBy, r.ExpectedAttemptID, r.ExpectedExecutionID} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return r, err
		}
	}
	if r.ExpectedRevision < 0 || !validPromotionDigest(r.ExpectedContentSHA256) {
		return r, errors.New("promotion requires an exact revision and content digest")
	}
	key, err := NormalizeAgentOperationKey(r.OperationKey)
	r.OperationKey = key
	return r, err
}

func validPromotionDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

type OperatorSteeringPromotionReceipt struct {
	ID                   string
	MessageID            string
	ReplacementMessageID string
	RunID                string
	SessionID            string
	ExpectedRevision     int64
	ContentSHA256        string
	TargetAttemptID      string
	ExecutionID          string
	CancellationID       string
	RequestedBy          string
	CreatedAt            time.Time
	OperationKeyDigest   string
	RequestFingerprint   string
}

func (r OperatorSteeringPromotionReceipt) Validate() error {
	for _, value := range []string{r.ID, r.MessageID, r.ReplacementMessageID, r.RunID, r.SessionID, r.TargetAttemptID, r.ExecutionID, r.CancellationID, r.RequestedBy} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return err
		}
	}
	if r.MessageID == r.ReplacementMessageID || r.ExpectedRevision < 0 || r.CreatedAt.IsZero() {
		return errors.New("promotion receipt identity or revision is invalid")
	}
	for _, value := range []string{r.ContentSHA256, r.OperationKeyDigest, r.RequestFingerprint} {
		if !validPromotionDigest(value) {
			return errors.New("promotion receipt digest is invalid")
		}
	}
	return nil
}

type PromoteOperatorSteeringResult struct {
	Receipt   OperatorSteeringPromotionReceipt
	Rejection *OperatorSteeringPromotionRejection
	Replayed  bool
}

// A rejected original key is a durable no-admission fence. Another explicit
// operation key can still promote the unchanged pending message.
type OperatorSteeringPromotionRejection struct {
	ID, MessageID, RunID, SessionID                          string
	ExpectedRevision                                         int64
	ContentSHA256, TargetAttemptID, ExecutionID, RequestedBy string
	CreatedAt                                                time.Time
	OperationKeyDigest, RequestFingerprint                   string
}

func (r OperatorSteeringPromotionRejection) Validate() error {
	for _, value := range []string{r.ID, r.MessageID, r.RunID, r.SessionID, r.TargetAttemptID, r.ExecutionID, r.RequestedBy} {
		if err := validateOperatorSteeringIdentity(value); err != nil {
			return err
		}
	}
	if r.ExpectedRevision < 0 || r.CreatedAt.IsZero() || !validPromotionDigest(r.ContentSHA256) || !validPromotionDigest(r.OperationKeyDigest) || !validPromotionDigest(r.RequestFingerprint) {
		return errors.New("promotion rejection binding is invalid")
	}
	return nil
}

type OperatorSteeringPromotionInspection struct {
	State             OperatorSteeringRevisionInspectionState
	Message           *OperatorSteeringObservationMessage
	Receipt           *OperatorSteeringPromotionReceipt
	Rejection         *OperatorSteeringPromotionRejection
	ExecutionObserved bool
	ExecutionID       string
}
