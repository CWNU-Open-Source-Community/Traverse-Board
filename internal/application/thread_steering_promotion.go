package application

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type currentSteeringPromotionStore interface {
	GetThreadBySession(context.Context, string) (domain.Thread, error)
	PromoteOperatorSteering(context.Context, domain.PromoteOperatorSteeringRequest) (domain.PromoteOperatorSteeringResult, error)
	RejectOperatorSteeringPromotion(context.Context, domain.PromoteOperatorSteeringRequest) (domain.PromoteOperatorSteeringResult, error)
	InspectOperatorSteeringPromotion(context.Context, string, string, string, string) (domain.OperatorSteeringPromotionInspection, error)
}

// Absence never proves that another controller's in-flight POST cannot commit.
// Only a sealed success/rejection or monotonic source change ends uncertainty.
func (s *ThreadTurnService) InspectCurrentSteeringPromotion(ctx context.Context, sessionID, messageID, key, actor string) (domain.OperatorSteeringPromotionInspection, error) {
	if s == nil || s.threads == nil {
		return domain.OperatorSteeringPromotionInspection{}, apperror.New(apperror.CodeFailedPrecondition, "current task control is unavailable")
	}
	store, ok := s.threads.store.(currentSteeringPromotionStore)
	if !ok {
		return domain.OperatorSteeringPromotionInspection{}, apperror.New(apperror.CodeFailedPrecondition, "promotion store is unavailable")
	}
	return store.InspectOperatorSteeringPromotion(ctx, sessionID, messageID, key, actor)
}

// PromoteCurrentSteering requires this process's exact live request owner.
// A checkpoint attempt may survive process/lease recovery; executionID may not.
func (s *ThreadTurnService) PromoteCurrentSteering(ctx context.Context, request domain.PromoteOperatorSteeringRequest) (domain.PromoteOperatorSteeringResult, error) {
	if s == nil || s.threads == nil {
		return domain.PromoteOperatorSteeringResult{}, apperror.New(apperror.CodeFailedPrecondition, "current task control is unavailable")
	}
	store, ok := s.threads.store.(currentSteeringPromotionStore)
	if !ok {
		return domain.PromoteOperatorSteeringResult{}, apperror.New(apperror.CodeFailedPrecondition, "promotion store is unavailable")
	}
	normalized, err := request.Normalize()
	if err != nil {
		return domain.PromoteOperatorSteeringResult{}, apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	observation, err := store.InspectOperatorSteeringPromotion(ctx, normalized.SessionID, normalized.MessageID, normalized.OperationKey, normalized.RequestedBy)
	if err != nil {
		return domain.PromoteOperatorSteeringResult{}, err
	}
	if observation.State == domain.OperatorSteeringRevisionSealed || observation.State == "rejected" {
		// The store verifies the original full request fingerprint before replay.
		return store.PromoteOperatorSteering(ctx, normalized)
	}
	thread, err := store.GetThreadBySession(ctx, normalized.SessionID)
	if err != nil {
		return domain.PromoteOperatorSteeringResult{}, err
	}
	active := s.activeTurns[thread.ID]
	if active == nil || active.stopping || active.preparingPlan || active.id != normalized.ExpectedExecutionID {
		return store.RejectOperatorSteeringPromotion(ctx, normalized)
	}
	return store.PromoteOperatorSteering(ctx, normalized)
}
