package application

import (
	"context"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/hooks"
)

type ThreadRunRecoveryStore interface {
	GetThreadRunRecovery(context.Context, string) (domain.ThreadRunRecovery, bool, error)
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	RecoverThreadRunFromFailedHandoff(context.Context, string, string, string, string, string) (
		domain.Thread, domain.Run, bool, error)
}

type threadRunAutomaticContinuationStore interface {
	ContinueThreadRunFromFailedHandoff(context.Context, string, string, string, string, string) (
		domain.Thread, domain.Run, bool, error)
}

type threadRunRecoveryMonetaryReleaser interface {
	ReleaseOpenMonetaryReservations(context.Context, string) (int, error)
}

type threadRunRecoveryDependencyReconciler interface {
	ReconcileDependencyEdges(context.Context, string) ([]domain.DependencyWake, error)
}

type ThreadRunRecoveryService struct {
	store            ThreadRunRecoveryStore
	lifecycleHooks   *hooks.Engine
	runtimeAuthority *domain.ExecutionPermissionRuntimeAuthority
}

type RecoverThreadRunRequest struct {
	Version            string
	ThreadID           string
	RunID              string
	HandoffOperationID string
	OperationKey       string
	RequestedBy        string
}

type RecoverThreadRunResult struct {
	Thread    domain.Thread
	FailedRun domain.Run
	Replayed  bool
}

func NewThreadRunRecoveryService(store ThreadRunRecoveryStore) *ThreadRunRecoveryService {
	return &ThreadRunRecoveryService{store: store}
}

func (s *ThreadRunRecoveryService) WithLifecycleHooks(engine *hooks.Engine) *ThreadRunRecoveryService {
	if s != nil {
		s.lifecycleHooks = engine
	}
	return s
}

func (s *ThreadRunRecoveryService) WithExecutionPermissionRuntimeAuthority(
	authority *domain.ExecutionPermissionRuntimeAuthority,
) *ThreadRunRecoveryService {
	if s != nil {
		s.runtimeAuthority = authority
	}
	return s
}

func (s *ThreadRunRecoveryService) Get(ctx context.Context,
	threadID string,
) (domain.ThreadRunRecovery, bool, error) {
	if s == nil || s.store == nil {
		return domain.ThreadRunRecovery{}, false, apperror.New(
			apperror.CodeFailedPrecondition, "Thread recovery store is required")
	}
	value, found, err := s.store.GetThreadRunRecovery(ctx, strings.TrimSpace(threadID))
	if err != nil || !found {
		return value, found, apperror.Normalize(err)
	}
	// A failed handoff can leave the same Supervisor turn retryable. Do not
	// surface terminal recovery UI for a transient turn boundary.
	if !value.Disposition.AllowsRunRecovery() {
		return domain.ThreadRunRecovery{}, false, nil
	}
	return value, true, nil
}

func (s *ThreadRunRecoveryService) Recover(ctx context.Context,
	request RecoverThreadRunRequest,
) (RecoverThreadRunResult, error) {
	return s.recover(ctx, request, false)
}

// RecoverForNextTurn advances a Thread past its latest failed durable handoff
// when the user has explicitly submitted another turn. Unlike the standalone
// recovery control, this may abandon a retryable failed turn: the old pending
// input is cancelled and is never copied or replayed into the successor Run.
func (s *ThreadRunRecoveryService) RecoverForNextTurn(ctx context.Context,
	request RecoverThreadRunRequest,
) (RecoverThreadRunResult, error) {
	return s.recover(ctx, request, true)
}

func (s *ThreadRunRecoveryService) recover(ctx context.Context,
	request RecoverThreadRunRequest, allowRetryableAbandonment bool,
) (RecoverThreadRunResult, error) {
	if s == nil || s.store == nil {
		return RecoverThreadRunResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Thread recovery store is required")
	}
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	request.RunID = strings.TrimSpace(request.RunID)
	request.HandoffOperationID = strings.TrimSpace(request.HandoffOperationID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	operationKey, operationKeyErr := domain.NormalizeAgentOperationKey(request.OperationKey)
	if request.Version != domain.ThreadRunRecoveryProtocolVersion ||
		!domain.ValidAgentID(request.ThreadID) || !domain.ValidAgentID(request.RunID) ||
		!domain.ValidAgentID(request.HandoffOperationID) ||
		!domain.ValidAgentID(request.RequestedBy) || operationKeyErr != nil ||
		operationKey != request.OperationKey {
		return RecoverThreadRunResult{}, apperror.New(
			apperror.CodeInvalidArgument, "Thread recovery request is invalid")
	}
	run, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return RecoverThreadRunResult{}, apperror.Normalize(err)
	}
	if !run.Terminal() {
		recovery, found, lookupErr := s.store.GetThreadRunRecovery(ctx, request.ThreadID)
		if lookupErr != nil {
			return RecoverThreadRunResult{}, apperror.Normalize(lookupErr)
		}
		if !found || recovery.RunID != request.RunID ||
			recovery.HandoffOperationID != request.HandoffOperationID {
			return RecoverThreadRunResult{}, apperror.New(apperror.CodeConflict,
				"The failed Thread boundary changed; refresh before recovering")
		}
		if !recovery.Quiescent {
			return RecoverThreadRunResult{}, apperror.New(apperror.CodeFailedPrecondition,
				"The old Run is still executing; wait for it to stop before recovering")
		}
		if !allowRetryableAbandonment && !recovery.Disposition.AllowsRunRecovery() {
			return RecoverThreadRunResult{}, apperror.New(
				apperror.CodeFailedPrecondition,
				"The failed Thread turn remains retryable on the current Run")
		}
	}
	mission, missionErr := s.store.GetMission(ctx, run.MissionID)
	if missionErr != nil {
		return RecoverThreadRunResult{}, apperror.Normalize(missionErr)
	}
	var threadRecord domain.Thread
	var failed domain.Run
	var replayed bool
	if allowRetryableAbandonment {
		continuationStore, ok := s.store.(threadRunAutomaticContinuationStore)
		if !ok {
			return RecoverThreadRunResult{}, apperror.New(
				apperror.CodeFailedPrecondition,
				"Thread automatic continuation store is required")
		}
		threadRecord, failed, replayed, err =
			continuationStore.ContinueThreadRunFromFailedHandoff(
				ctx, request.ThreadID, request.RunID, request.HandoffOperationID,
				request.RequestedBy, request.OperationKey)
	} else {
		threadRecord, failed, replayed, err = s.store.RecoverThreadRunFromFailedHandoff(
			ctx, request.ThreadID, request.RunID, request.HandoffOperationID,
			request.RequestedBy, request.OperationKey)
	}
	if err != nil {
		return RecoverThreadRunResult{Thread: threadRecord, FailedRun: failed,
			Replayed: replayed}, apperror.Normalize(err)
	}
	// The SQLite transaction above is the terminal fence: it rechecks the
	// latest failed handoff and lease while holding the writer lock, then makes
	// the Run terminal before releasing that lock. Process-local revocation and
	// extension hooks must not run before that proof, otherwise a concurrently
	// acquired lease could retain durable ownership after external authority was
	// already revoked. Revoke on replays as well; it is intentionally idempotent.
	if s.runtimeAuthority != nil {
		s.runtimeAuthority.RevokeRun(failed.ID)
	}
	if !replayed {
		// A terminal safety recovery cannot be rolled back after commit. Hooks at
		// this point are post-boundary notifications, so denial/unavailability is
		// best-effort rather than being reported as if recovery had not happened.
		_ = executeLifecycleBoundary(ctx, s.lifecycleHooks, hooks.RunCompleted,
			failed.ID, mission.WorkspaceID, map[string]any{
				"session_id": failed.SessionID, "from": run.Status,
				"to": domain.RunFailed, "source": "thread_recovery",
			})
	}
	if releaser, ok := s.store.(threadRunRecoveryMonetaryReleaser); ok {
		_, _ = releaser.ReleaseOpenMonetaryReservations(ctx, failed.ID)
	}
	if reconciler, ok := s.store.(threadRunRecoveryDependencyReconciler); ok {
		_, _ = reconciler.ReconcileDependencyEdges(ctx, failed.ID)
	}
	return RecoverThreadRunResult{Thread: threadRecord, FailedRun: failed,
		Replayed: replayed}, nil
}
