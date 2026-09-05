package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const WebFetchAuthorizationProtocolVersion = "web_fetch_authorization.v1"

type WebFetchAuthorizationStatus string

const (
	WebFetchAuthorizationPending  WebFetchAuthorizationStatus = "pending"
	WebFetchAuthorizationApproved WebFetchAuthorizationStatus = "approved"
	WebFetchAuthorizationDenied   WebFetchAuthorizationStatus = "denied"
	WebFetchAuthorizationConsumed WebFetchAuthorizationStatus = "consumed"
)

func (s WebFetchAuthorizationStatus) Valid() bool {
	switch s {
	case WebFetchAuthorizationPending, WebFetchAuthorizationApproved,
		WebFetchAuthorizationDenied, WebFetchAuthorizationConsumed:
		return true
	default:
		return false
	}
}

type WebFetchAuthorizationScope string

const (
	WebFetchAuthorizationOnce   WebFetchAuthorizationScope = "once"
	WebFetchAuthorizationThread WebFetchAuthorizationScope = "thread"
)

func (s WebFetchAuthorizationScope) Valid() bool {
	return s == WebFetchAuthorizationOnce || s == WebFetchAuthorizationThread
}

// WebFetchAuthorization is the safe, public projection of an inline domain
// approval. It deliberately contains neither the raw tool payload nor response
// data. CanonicalURL has already passed the public-HTTPS parser; ExactTarget is
// the single normalized DNS host that may be added as subordinate authority.
type WebFetchAuthorization struct {
	ID                   string
	ApprovalID           string
	ThreadID             string
	RunID                string
	MissionID            string
	SessionID            string
	WorkspaceID          string
	SupervisorTurn       int
	SupervisorToolCallID string
	CanonicalURL         string
	ExactTarget          string
	RequestFingerprint   string
	Scope                WebFetchAuthorizationScope
	Status               WebFetchAuthorizationStatus
	RequestedBy          string
	ReviewedBy           string
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DecidedAt            *time.Time
}

type WebFetchAuthorizationRequest struct {
	ID                   string
	RunID                string
	MissionID            string
	SessionID            string
	WorkspaceID          string
	SupervisorTurn       int
	SupervisorToolCallID string
	CanonicalURL         string
	ExactTarget          string
	RequestFingerprint   string
	RequestedBy          string
}

func (a WebFetchAuthorization) Validate() error {
	for _, value := range []string{a.ID, a.ApprovalID, a.ThreadID, a.RunID,
		a.MissionID, a.SessionID, a.WorkspaceID, a.SupervisorToolCallID,
		a.CanonicalURL, a.ExactTarget, a.RequestFingerprint, a.RequestedBy,
		a.ReviewedBy} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
			return errors.New("web fetch authorization text is invalid")
		}
	}
	if a.ID == "" || a.ApprovalID == "" || a.ThreadID == "" || a.RunID == "" ||
		a.MissionID == "" || a.SessionID == "" || a.SupervisorToolCallID == "" ||
		a.CanonicalURL == "" || a.ExactTarget == "" || len(a.RequestFingerprint) != 64 ||
		a.RequestedBy == "" || a.SupervisorTurn < 1 || a.Version < 1 ||
		!a.Scope.Valid() || !a.Status.Valid() || a.CreatedAt.IsZero() ||
		a.UpdatedAt.Before(a.CreatedAt) {
		return errors.New("web fetch authorization identity or state is invalid")
	}
	if a.Status == WebFetchAuthorizationPending {
		if a.ReviewedBy != "" || a.DecidedAt != nil {
			return errors.New("pending web fetch authorization cannot be decided")
		}
	} else if a.ReviewedBy == "" || a.DecidedAt == nil ||
		a.DecidedAt.Before(a.CreatedAt) {
		return errors.New("decided web fetch authorization requires review metadata")
	}
	if a.Status == WebFetchAuthorizationConsumed && a.Scope != WebFetchAuthorizationOnce {
		return errors.New("only an allow-once web fetch authorization can be consumed")
	}
	return nil
}
