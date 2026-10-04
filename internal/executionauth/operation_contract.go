package executionauth

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/toolcontract"
)

// SubjectRef is host-issued for this migration's native backend. The binder
// resolves Thread, policy, registered roots and internal lease/epoch/fences;
// plugin authors do not supply those internals or choose the current policy.
type SubjectRef struct {
	RunID   string
	ActorID string
}

func (s SubjectRef) Validate() error {
	if !operationContractID(s.RunID) || !operationContractID(s.ActorID) {
		return errors.New("operation subject requires host Run and actor identities")
	}
	return nil
}

type Decision struct {
	Outcome          string // allow | require_approval | deny
	ReasonCode       string
	ApprovalRef      string
	AuthorizationRef string
	BeforeDispatch   toolcontract.DispatchGuard `json:"-"`
}

func (d Decision) Validate() error {
	if !operationContractID(d.ReasonCode) ||
		(d.ApprovalRef != "" && !operationContractID(d.ApprovalRef)) ||
		(d.AuthorizationRef != "" && !operationContractID(d.AuthorizationRef)) {
		return errors.New("operation decision requires a reason code")
	}
	switch d.Outcome {
	case "allow":
		if d.AuthorizationRef == "" || d.BeforeDispatch == nil {
			return errors.New("allowed operation requires host authorization and dispatch guard")
		}
	case "require_approval":
		if d.ApprovalRef == "" || d.AuthorizationRef != "" || d.BeforeDispatch != nil {
			return errors.New("pending approval cannot carry an executable grant")
		}
	case "deny":
		if d.ApprovalRef != "" || d.AuthorizationRef != "" || d.BeforeDispatch != nil {
			return errors.New("denied operation cannot carry approval or authority")
		}
	default:
		return errors.New("operation decision outcome is invalid")
	}
	return nil
}

func operationContractID(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

// Authorizer is implemented by the host, not by plugins/transports. References
// are looked up and bound to the exact final operation and live policy; passing
// a string is not approval. This contract does not yet switch production policy.
type Authorizer interface {
	Authorize(ctx context.Context, subject SubjectRef, operation toolcontract.Operation,
		approvalRef string) (Decision, error)
}
