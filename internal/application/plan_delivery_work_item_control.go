package application

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type ControlPlanDeliveryWorkItemRequest struct {
	Version string
	domain.PlanDeliveryWorkItemTransition
}

type ControlPlanDeliveryWorkItemResult struct {
	CurrentWorkItem domain.WorkItem
	AppliedStatus   domain.WorkItemStatus
	AppliedVersion  int64
	Replayed        bool
}

type ControlPlanDeliveryCheckpointRequest struct {
	Version string
	RecordDeliveryCheckpointRequest
}

type ControlPlanDeliveryCheckpointResult struct {
	RecordDeliveryCheckpointResult
	CurrentWorkItem domain.WorkItem
	CurrentMode     domain.RunModeSnapshot
}

func (s *PlanDeliveryControlService) TransitionWorkItem(ctx context.Context, request ControlPlanDeliveryWorkItemRequest) (ControlPlanDeliveryWorkItemResult, error) {
	if s == nil || s.store == nil {
		return ControlPlanDeliveryWorkItemResult{}, apperror.New(apperror.CodeFailedPrecondition, "Plan Delivery control store is required")
	}
	if err := validatePlanDeliveryControlIdentity(request.Version, request.RunID); err != nil {
		return ControlPlanDeliveryWorkItemResult{}, err
	}
	item, replayed, err := s.store.TransitionPlanDeliveryWorkItem(ctx, request.PlanDeliveryWorkItemTransition)
	if err != nil {
		return ControlPlanDeliveryWorkItemResult{}, apperror.Normalize(err)
	}
	return ControlPlanDeliveryWorkItemResult{CurrentWorkItem: item, AppliedStatus: request.Target, AppliedVersion: request.ExpectedVersion + 1, Replayed: replayed}, nil
}

func (s *PlanDeliveryControlService) RecordCheckpoint(ctx context.Context, request ControlPlanDeliveryCheckpointRequest) (ControlPlanDeliveryCheckpointResult, error) {
	if s == nil || s.store == nil {
		return ControlPlanDeliveryCheckpointResult{}, apperror.New(apperror.CodeFailedPrecondition, "Plan Delivery control store is required")
	}
	if err := validatePlanDeliveryControlIdentity(request.Version, request.RunID); err != nil {
		return ControlPlanDeliveryCheckpointResult{}, err
	}
	if request.ExpectedWorkItemVersion <= 0 {
		return ControlPlanDeliveryCheckpointResult{}, apperror.New(apperror.CodeInvalidArgument, "Plan Delivery checkpoint requires the expected WorkItem version")
	}
	result, err := NewDeliveryCheckpointService(s.store).Record(ctx, request.RecordDeliveryCheckpointRequest)
	if err != nil {
		return ControlPlanDeliveryCheckpointResult{}, err
	}
	item, err := s.store.GetWorkItem(ctx, result.Checkpoint.WorkItemID)
	if err != nil {
		return ControlPlanDeliveryCheckpointResult{}, apperror.Normalize(err)
	}
	mode, err := s.store.GetRunMode(ctx, request.RunID)
	if err != nil {
		return ControlPlanDeliveryCheckpointResult{}, apperror.Normalize(err)
	}
	return ControlPlanDeliveryCheckpointResult{RecordDeliveryCheckpointResult: result, CurrentWorkItem: item, CurrentMode: mode}, nil
}
