package executionauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolcontract"
)

// BoundApproval is evidence resolved by the host from its existing approval
// ledger, after checking the native proposal's immutable inputs. It is not an
// approval boolean accepted from a tool, plugin, or request body.
type BoundApproval struct {
	Ref                  string
	Subject              SubjectRef
	OperationFingerprint string
	Status               string // pending | approved | denied
}

// OperationAuthority is produced only by a trusted host resolver. Its binding
// fingerprints the current policy, registered roots, actor and runtime fences.
// EffectsVerified means the host knows that the actual adapter enforces ALL
// declared effects and targets. A cwd, tool annotation or server promise cannot
// establish this fact. RuntimeAvailable also includes authentication and native
// access checks; choosing full does not provide unavailable capabilities.
type OperationAuthority struct {
	Mode               domain.ExecutionApprovalMode
	BindingFingerprint string
	RuntimeAvailable   bool
	EffectsVerified    bool
	FullActivated      bool
	Approval           *BoundApproval
}

type AuthorityResolver func(context.Context, SubjectRef, toolcontract.Operation, string) (OperationAuthority, error)

// PolicyAuthorizer owns the common ask/auto/full decision. The resolver owns
// native identity, isolation and ledger validation, not a second policy table.
// It must observe cancellation and re-read current authority on EVERY call.
type PolicyAuthorizer struct{ resolve AuthorityResolver }

func NewPolicyAuthorizer(resolve AuthorityResolver) *PolicyAuthorizer {
	return &PolicyAuthorizer{resolve: resolve}
}

var _ Authorizer = (*PolicyAuthorizer)(nil)

func (a *PolicyAuthorizer) Authorize(ctx context.Context, subject SubjectRef,
	operation toolcontract.Operation, approvalRef string,
) (Decision, error) {
	operation = cloneOperation(operation)
	decision, fingerprint, err := a.decide(ctx, subject, operation, approvalRef)
	if err != nil || decision.Outcome != "allow" {
		return decision, err
	}
	var mu sync.Mutex
	consumed := false
	authorizationRef := decision.AuthorizationRef
	decision.BeforeDispatch = func(ctx context.Context, actualFingerprint string) error {
		mu.Lock()
		defer mu.Unlock()
		if consumed {
			return errors.New("operation dispatch authority was already consumed")
		}
		consumed = true
		if actualFingerprint != fingerprint {
			return errors.New("operation changed before dispatch")
		}
		return a.Recheck(ctx, subject, operation, approvalRef, authorizationRef)
	}
	return decision, nil
}

// Recheck does not start another operation or issue a replacement grant. A
// multi-step native operation uses it before later mutations after consuming
// BeforeDispatch once. The caller must supply the original authorization ref.
// This is a live check, not a lock held across an OS syscall: revocation after a
// successful check may meet an in-flight effect. Adapters must cancel where
// supported and must never describe an unobserved remote outcome as rolled back.
func (a *PolicyAuthorizer) Recheck(ctx context.Context, subject SubjectRef,
	operation toolcontract.Operation, approvalRef, authorizationRef string,
) error {
	decision, _, err := a.decide(ctx, subject, cloneOperation(operation), approvalRef)
	if err != nil {
		return err
	}
	if decision.Outcome != "allow" || authorizationRef == "" ||
		decision.AuthorizationRef != authorizationRef {
		return errors.New("operation authority changed before execution")
	}
	return nil
}

func (a *PolicyAuthorizer) decide(ctx context.Context, subject SubjectRef,
	operation toolcontract.Operation, approvalRef string,
) (Decision, string, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, "", err
	}
	if a == nil || a.resolve == nil {
		return Decision{}, "", errors.New("host operation authority resolver is unavailable")
	}
	if err := subject.Validate(); err != nil {
		return Decision{}, "", err
	}
	if approvalRef != "" && !operationContractID(approvalRef) {
		return Decision{}, "", errors.New("operation approval reference is invalid")
	}
	fingerprint, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		return Decision{}, "", err
	}
	authority, err := a.resolve(ctx, subject, cloneOperation(operation), approvalRef)
	if err != nil {
		return Decision{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, "", err
	}
	if _, err := domain.ParseExecutionApprovalMode(string(authority.Mode)); err != nil ||
		!authorityDigest(authority.BindingFingerprint) {
		return Decision{}, "", errors.New("host operation authority binding is invalid")
	}
	if authority.Approval != nil {
		proof := *authority.Approval
		authority.Approval = &proof
		if !operationContractID(proof.Ref) || proof.Subject != subject ||
			proof.OperationFingerprint != fingerprint ||
			(approvalRef != "" && proof.Ref != approvalRef) ||
			(proof.Status != "pending" && proof.Status != "approved" && proof.Status != "denied") {
			return Decision{}, "", errors.New("approval does not bind the exact operation and subject")
		}
	} else if approvalRef != "" {
		return Decision{}, "", errors.New("operation approval was not found")
	}
	decision := classifyOperation(authority, operation)
	if decision.Outcome == "allow" {
		raw, err := json.Marshal(struct {
			Subject   SubjectRef
			Operation string
			Authority OperationAuthority
		}{subject, fingerprint, authority})
		if err != nil {
			return Decision{}, "", err
		}
		sum := sha256.Sum256(append([]byte("executionauth.authorization.v1\x00"), raw...))
		decision.AuthorizationRef = hex.EncodeToString(sum[:])
	}
	return decision, fingerprint, nil
}

func classifyOperation(authority OperationAuthority, operation toolcontract.Operation) Decision {
	deny := func(reason string) Decision { return Decision{Outcome: "deny", ReasonCode: reason} }
	allow := func(reason string) Decision { return Decision{Outcome: "allow", ReasonCode: reason} }
	if !authority.RuntimeAvailable {
		return deny("runtime_unavailable")
	}
	if authority.Mode == domain.ExecutionApprovalFull && !authority.FullActivated {
		return deny("full_activation_required")
	}
	// An existing review is never silently upgraded by a mode change, even for
	// an ordinary workspace edit or an activated full Run.
	if proof := authority.Approval; proof != nil {
		switch proof.Status {
		case "pending":
			return Decision{Outcome: "require_approval", ReasonCode: "existing_approval_pending", ApprovalRef: proof.Ref}
		case "denied":
			return deny("existing_approval_denied")
		case "approved":
			decision := allow("exact_operation_approved")
			decision.ApprovalRef = proof.Ref
			return decision
		}
	}
	if authority.Mode == domain.ExecutionApprovalFull {
		return allow("active_full_authority")
	}
	if authority.EffectsVerified {
		routine := true
		for _, effect := range operation.Effects {
			switch effect {
			case toolcontract.EffectWorkspaceRead, toolcontract.EffectReversibleWrite:
			case toolcontract.EffectProcess:
				// Bounded process effects need a verified workspace boundary.
				routine = routine && (slices.Contains(operation.Effects, toolcontract.EffectWorkspaceRead) ||
					slices.Contains(operation.Effects, toolcontract.EffectReversibleWrite))
			case toolcontract.EffectPublicNetwork:
				routine = routine && authority.Mode == domain.ExecutionApprovalAuto
			default:
				routine = false
			}
		}
		if routine {
			return allow("verified_routine_operation")
		}
	}
	// The resolver must prepare a native pending proposal before exposing a
	// require_approval response. This layer never invents an unpersisted ref.
	return deny("approval_proposal_required")
}

func cloneOperation(operation toolcontract.Operation) toolcontract.Operation {
	operation.Targets = slices.Clone(operation.Targets)
	operation.Effects = slices.Clone(operation.Effects)
	return operation
}

func authorityDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil && len(decoded) == sha256.Size
}
