package application

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/toolgateway"
)

// DockerSandboxProposalExecutor is the model-facing product adapter. It can
// ask the shared DockerSandboxService for an admission, but deliberately does
// not expose or call Start, Cancel, Recover, or any Docker transport.
type DockerSandboxProposalExecutor struct {
	service *DockerSandboxService
}

func NewDockerSandboxProposalExecutor(
	service *DockerSandboxService,
) (*DockerSandboxProposalExecutor, error) {
	if service == nil || service.store == nil {
		return nil, errors.New("Docker Sandbox service is required")
	}
	return &DockerSandboxProposalExecutor{service: service}, nil
}

func (e *DockerSandboxProposalExecutor) ProposeDockerSandbox(ctx context.Context,
	scope toolgateway.DockerSandboxProposalContext,
	spec toolgateway.DockerSandboxProposalSpec,
) (toolgateway.DockerSandboxProposalResult, error) {
	if e == nil || e.service == nil || e.service.store == nil {
		return toolgateway.DockerSandboxProposalResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Docker Sandbox proposal executor is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return toolgateway.DockerSandboxProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument,
			"Docker Sandbox proposal scope is invalid", err)
	}
	if err := spec.Validate(); err != nil {
		return toolgateway.DockerSandboxProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument,
			"Docker Sandbox proposal is invalid", err)
	}

	// Bind the model call to the durable compiler-owned plan before Admit can
	// append anything. The plan owner, not the model-facing transport label, is
	// the requester consumed by the admission service.
	plan, err := e.service.store.GetDockerContainerPlan(ctx, spec.PlanID)
	if err != nil {
		return toolgateway.DockerSandboxProposalResult{}, apperror.Normalize(err)
	}
	if err := plan.Validate(); err != nil {
		return toolgateway.DockerSandboxProposalResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Docker Sandbox compiled plan is invalid")
	}
	if plan.RunID != scope.RunID || plan.WorkspaceID != scope.WorkspaceID {
		return toolgateway.DockerSandboxProposalResult{}, apperror.New(
			apperror.CodeConflict,
			"Docker Sandbox proposal does not belong to the fenced Run and Workspace")
	}

	admitted, err := e.service.Admit(ctx, DockerSandboxAdmissionRequest{
		PlanID: spec.PlanID, Manifest: spec.Manifest,
		OperationKey: scope.OperationKey, RequestedBy: plan.RequestedBy,
	})
	if err != nil {
		return toolgateway.DockerSandboxProposalResult{}, err
	}
	result := toolgateway.DockerSandboxProposalResult{
		Allowed: admitted.Allowed, ReasonCode: admitted.ReasonCode,
		RemediationCode: admitted.RemediationCode, Replayed: admitted.Replayed,
	}
	if admitted.Admission != nil {
		if !admitted.Allowed || admitted.Admission.RunID != scope.RunID ||
			admitted.Admission.WorkspaceID != scope.WorkspaceID ||
			admitted.Admission.PlanID != spec.PlanID {
			return toolgateway.DockerSandboxProposalResult{}, apperror.New(
				apperror.CodeConflict,
				"Docker Sandbox admission escaped the fenced proposal scope")
		}
		result.AdmissionID = admitted.Admission.ID
	} else if admitted.Allowed {
		return toolgateway.DockerSandboxProposalResult{}, apperror.New(
			apperror.CodeInternal,
			"Docker Sandbox admission result is inconsistent")
	}
	if err := result.Validate(); err != nil {
		return toolgateway.DockerSandboxProposalResult{}, apperror.Wrap(
			apperror.CodeInternal,
			"Docker Sandbox proposal result is invalid", err)
	}
	return result, nil
}

var _ toolgateway.DockerSandboxProposalExecutor = (*DockerSandboxProposalExecutor)(nil)
