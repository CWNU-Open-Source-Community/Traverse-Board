package application

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
)

type threadPlanSuccessorStore interface {
	GetThreadPlanSuccessor(context.Context, string, string, string, string, domain.ExecutionPhase) (domain.Run, bool, error)
	EnsureThreadPlanSuccessor(context.Context, string, string, string, string, domain.Mission, domain.Run, domain.RunModeSnapshot, session.Session, []events.Event, *domain.ThreadFileContinuation) (domain.Thread, domain.Run, bool, error)
}

func threadPlanTargetPhase(action string) domain.ExecutionPhase {
	if action == "enter_plan" {
		return domain.ExecutionPhasePlan
	}
	return domain.ExecutionPhaseDeliver
}

func (s *ThreadTurnService) enterThreadPlanMode(ctx context.Context, request ThreadPlanControlRequest) error {
	st, err := s.planControlStore()
	if err != nil {
		return err
	}
	successorStore, ok := s.threads.store.(threadPlanSuccessorStore)
	if !ok {
		return apperror.New(apperror.CodeFailedPrecondition, "Thread mode continuation is unavailable")
	}
	phase := threadPlanTargetPhase(request.Action)
	modeKey := threadPlanStepKey(request.ThreadID, request.OperationKey, request.Action)
	if _, found, err := successorStore.GetThreadPlanSuccessor(ctx, request.ThreadID, request.RunID, modeKey, request.RequestedBy, phase); err != nil || found {
		return apperror.Normalize(err)
	}
	// An original in-place mode receipt remains replayable after later changes.
	if _, found, err := st.GetRunModeOperation(ctx, threadPlanOperationDigest(request.ThreadID, modeKey)); err != nil {
		return apperror.Normalize(err)
	} else if found {
		_, err := NewRunService(st).ChangePhase(ctx, ChangeRunPhaseRequest{ThreadID: request.ThreadID, RunID: request.RunID, Phase: string(phase), OperationKey: modeKey, RequestedBy: request.RequestedBy, Reason: "operator explicitly selected Thread planning mode"})
		return err
	}
	thread, err := st.GetThread(ctx, request.ThreadID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if thread.LastRunID != request.RunID || (thread.ActiveRunID != "" && thread.ActiveRunID != request.RunID) {
		return apperror.New(apperror.CodeConflict, "Thread mode target is no longer current")
	}
	run, err := st.GetRun(ctx, request.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	_, selected, err := st.GetPlanDeliverySelectionByRun(ctx, request.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	if !run.Terminal() && !selected {
		_, err := NewRunService(st).ChangePhase(ctx, ChangeRunPhaseRequest{ThreadID: request.ThreadID, RunID: request.RunID, Phase: string(phase), OperationKey: modeKey, RequestedBy: request.RequestedBy, Reason: "operator explicitly selected Thread planning mode"})
		return err
	}
	mission, err := s.threads.store.GetMission(ctx, thread.MissionID)
	if err != nil {
		return apperror.Normalize(err)
	}
	previous, err := st.GetRunMode(ctx, run.ID)
	if err != nil {
		return apperror.Normalize(err)
	}
	mission.Scope, _ = successorRunNetworkScope(previous.Scope)
	target := previous
	target.Phase = phase
	candidate, linked, mode, initial, err := s.threads.prepareSuccessor(ctx, thread, mission, run, target, false, request.RequestedBy)
	if err != nil {
		return err
	}
	files, err := s.threads.prepareFileContinuation(ctx, SubmitThreadMessageRequest{}, thread, run, candidate, mode)
	if err != nil {
		return err
	}
	_, _, _, err = successorStore.EnsureThreadPlanSuccessor(ctx, thread.ID, run.ID, modeKey, request.RequestedBy, mission, candidate, mode, linked, initial, files)
	return apperror.Normalize(err)
}
