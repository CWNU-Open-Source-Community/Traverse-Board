package domain

import (
	"errors"
	"strings"
	"time"
)

const ThreadRunRecoveryProtocolVersion = "thread_run_recovery.v1"

// ThreadRunFailureDisposition records what may safely happen after a durable
// handoff failure. A failed handoff is not, by itself, proof that its Run is
// dead: transient model and scheduling failures keep the same logical turn
// retryable. Only successor/recovery dispositions may terminalize the Run.
type ThreadRunFailureDisposition string

const (
	ThreadRunFailureRetrySameTurn     ThreadRunFailureDisposition = "retry_same_turn"
	ThreadRunFailureRequiresSuccessor ThreadRunFailureDisposition = "requires_successor"
	ThreadRunFailureRecoveryRequired  ThreadRunFailureDisposition = "recovery_required"
)

func (d ThreadRunFailureDisposition) Valid() bool {
	switch d {
	case ThreadRunFailureRetrySameTurn, ThreadRunFailureRequiresSuccessor,
		ThreadRunFailureRecoveryRequired:
		return true
	default:
		return false
	}
}

func (d ThreadRunFailureDisposition) AllowsRunRecovery() bool {
	return d == ThreadRunFailureRequiresSuccessor ||
		d == ThreadRunFailureRecoveryRequired
}

// ThreadRunRecovery classifies the latest durable failed execution handoff of
// an active Run. Application callers expose terminal recovery only when the
// disposition allows it. Recovery never copies pending operator steering into
// the successor Run.
type ThreadRunRecovery struct {
	ProtocolVersion    string
	ThreadID           string
	RunID              string
	HandoffOperationID string
	Disposition        ThreadRunFailureDisposition
	ErrorCode          string
	StopReason         string
	Detail             string
	Quiescent          bool
	FailedAt           time.Time
}

func (r ThreadRunRecovery) Validate() error {
	if r.ProtocolVersion != ThreadRunRecoveryProtocolVersion ||
		!ValidAgentID(r.ThreadID) || !ValidAgentID(r.RunID) ||
		!ValidAgentID(r.HandoffOperationID) || !r.Disposition.Valid() {
		return errors.New("thread run recovery identity is invalid")
	}
	if r.ErrorCode != strings.TrimSpace(r.ErrorCode) || r.ErrorCode == "" ||
		len(r.ErrorCode) > 64 || strings.ContainsRune(r.ErrorCode, 0) ||
		r.StopReason != strings.TrimSpace(r.StopReason) || r.StopReason == "" ||
		len(r.StopReason) > 64 || strings.ContainsRune(r.StopReason, 0) {
		return errors.New("thread run recovery failure is invalid")
	}
	if r.Detail != strings.TrimSpace(r.Detail) || len([]rune(r.Detail)) > 4096 ||
		strings.ContainsRune(r.Detail, 0) || r.FailedAt.IsZero() {
		return errors.New("thread run recovery detail is invalid")
	}
	return nil
}
