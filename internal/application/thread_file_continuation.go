package application

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
	"errors"
)

type threadFileContinuationStore interface {
	GetRunFileDrydock(context.Context, string) (drydock.Workspace, bool, error)
	GetConfiguredStandardCodePresetOperation(context.Context, string) (domain.StandardCodePresetOperation, bool, error)
	GetThreadModelRoutePreference(context.Context, string) (domain.ThreadModelRoutePreference, bool, error)
	GetThreadExecutionPermission(context.Context, string) (domain.ThreadExecutionPermissionSnapshot, error)
	RunOwnsCurrentDrydock(context.Context, string, string) (bool, error)
	EnsureThreadSuccessorWithFiles(context.Context, domain.ThreadMessageIntentRequest, string, domain.Mission, domain.Run, domain.RunModeSnapshot, session.Session, []events.Event, domain.ThreadFileContinuation) (domain.Thread, domain.Run, bool, error)
}

func (s *ThreadService) prepareFileContinuation(ctx context.Context, request SubmitThreadMessageRequest, thread domain.Thread, predecessor, candidate domain.Run, mode domain.RunModeSnapshot) (*domain.ThreadFileContinuation, error) {
	store, ok := s.store.(threadFileContinuationStore)
	if !ok {
		return nil, nil
	}
	physical, found, err := store.GetRunFileDrydock(ctx, predecessor.ID)
	if err != nil {
		return nil, err
	}
	preset, configured, err := store.GetConfiguredStandardCodePresetOperation(ctx, predecessor.ID)
	if err != nil {
		return nil, err
	}
	if !found && !configured {
		return nil, nil
	}
	if !found || s.drydocks == nil || !s.drydocks.executor.Available() {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "The current Thread working directory is unavailable; the source directory cannot be substituted")
	}
	holder, err := store.RunOwnsCurrentDrydock(ctx, predecessor.ID, physical.ID)
	if err != nil {
		return nil, err
	}
	if !holder {
		return nil, apperror.New(apperror.CodeConflict, "The previous execution no longer holds this Thread working directory")
	}
	if physical.State != drydock.StateReady && physical.State != drydock.StateDelivered {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "The current Thread working directory needs recovery before execution can continue")
	}
	checked, _, _, err := s.drydocks.loadExactDrydock(ctx, predecessor.ID, physical.Generation, false)
	if err != nil {
		return nil, err
	}
	if checked.ID != physical.ID || checked.WorkspaceID != physical.WorkspaceID {
		return nil, errors.New("Thread physical working directory binding changed")
	}
	permission, err := store.GetThreadExecutionPermission(ctx, thread.ID)
	if err != nil {
		return nil, err
	}
	p := &domain.ThreadFileContinuation{Binding: domain.ThreadDrydockBinding{RunID: candidate.ID, ThreadID: thread.ID, DrydockID: physical.ID, PredecessorRunID: predecessor.ID, CreatedAt: candidate.CreatedAt}, ThreadVersion: thread.Version, PermissionSnapshotID: permission.ID, PermissionRevision: permission.Revision, Workspace: physical, Preset: preset}
	preference, found, err := store.GetThreadModelRoutePreference(ctx, thread.ID)
	if err != nil {
		return nil, err
	}
	if found {
		p.ModelPreference = &preference
		if preference.Selected && candidate.Config.ModelRoute != preference.Provider+"/"+preference.Model {
			return nil, apperror.New(apperror.CodeConflict, "Thread model changed before continuation")
		}
	}
	return p, nil
}
