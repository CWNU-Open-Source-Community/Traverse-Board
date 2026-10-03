package application

import (
	"context"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/toolcontract"
)

// Native sources validate their immutable intent and current host authority
// first. This shared decision step uses the existing approval ledger; it never
// creates a parallel policy or invents a non-durable approval reference.
func decidePendingOperation(ctx context.Context, permission domain.RunExecutionPermissionSnapshot,
	subject executionauth.SubjectRef, operation toolcontract.Operation, binding string,
	record **approval.Record, ensure func() error, forceReview, verified bool,
) (waiting, allowed bool, err error) {
	if forceReview && *record == nil {
		if err := ensure(); err != nil {
			return false, false, err
		}
	}
	fingerprint, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		return false, false, err
	}
	projection, err := domain.ExecutionPermissionApproval(permission)
	if err != nil {
		return false, false, err
	}
	authorizer := executionauth.NewPolicyAuthorizer(func(context.Context, executionauth.SubjectRef, toolcontract.Operation, string) (executionauth.OperationAuthority, error) {
		value := executionauth.OperationAuthority{Mode: projection.Mode, BindingFingerprint: binding,
			RuntimeAvailable: true, FullActivated: projection.Mode == domain.ExecutionApprovalFull, EffectsVerified: verified}
		if proof := *record; proof != nil {
			value.Approval = &executionauth.BoundApproval{Ref: proof.ID, Subject: subject, OperationFingerprint: fingerprint, Status: string(proof.Status)}
		}
		return value, nil
	})
	decision, err := authorizer.Authorize(ctx, subject, operation, "")
	if err != nil {
		return false, false, err
	}
	if decision.ReasonCode == "approval_proposal_required" {
		if err := ensure(); err != nil {
			return false, false, err
		}
		return true, false, nil
	}
	return decision.Outcome == "require_approval", decision.Outcome == "allow", nil
}
