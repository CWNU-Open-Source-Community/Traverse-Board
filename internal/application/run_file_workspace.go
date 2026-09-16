package application

import (
	"context"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

// RunFileWorkspace keeps the source control identity separate from the exact
// filesystem target. It is application data, never an execution grant.
type RunFileWorkspace struct {
	Source    session.WorkspaceInfo
	Workspace session.WorkspaceInfo
	Drydock   *drydock.Workspace
}

// NewAgentCodeWorkspaceResolver keeps Run ownership in the resolution key;
// several Runs may share one source while owning different working directories.
func NewAgentCodeWorkspaceResolver(store AgentCodeToolStore, drydocks *DrydockService) toolgateway.AgentCodeWorkspaceResolver {
	return func(ctx context.Context, runID, sourceWorkspaceID string) (string, string, error) {
		run, err := store.GetRun(ctx, runID)
		if err != nil {
			return "", "", apperror.Normalize(err)
		}
		mission, err := store.GetMission(ctx, run.MissionID)
		if err != nil {
			return "", "", apperror.Normalize(err)
		}
		if mission.WorkspaceID != sourceWorkspaceID {
			return "", "", apperror.New(apperror.CodeConflict, "Agent Code source workspace does not belong to this Run")
		}
		files, err := ResolveRunFileWorkspace(ctx, store, run, mission, drydocks)
		if err != nil {
			return "", "", err
		}
		if files.Drydock != nil {
			if err := requireCurrentRunFileDrydock(ctx, store, runID, *files.Drydock); err != nil {
				return "", "", err
			}
		}
		return files.Workspace.ID, files.Workspace.RootPath, nil
	}
}

type RunFileWorkspaceStore interface {
	GetSession(context.Context, string) (session.Session, error)
	GetWorkspaceInfo(context.Context, string) (session.WorkspaceInfo, error)
}

type runFileDrydockStore interface {
	GetDrydockByRun(context.Context, string) (drydock.Workspace, bool, error)
}

type runFileDrydockBindingStore interface {
	GetRunFileDrydock(context.Context, string) (drydock.Workspace, bool, error)
}

// Return the actual physical owner object. A Thread epoch relation never
// rewrites the creator Run/Session in historical Drydock records.
func readRunFileDrydock(ctx context.Context, store any, runID string) (drydock.Workspace, bool, error) {
	if reader, ok := store.(runFileDrydockBindingStore); ok {
		return reader.GetRunFileDrydock(ctx, runID)
	}
	if reader, ok := store.(runFileDrydockStore); ok {
		return reader.GetDrydockByRun(ctx, runID)
	}
	return drydock.Workspace{}, false, nil
}

type runFilePresetStore interface {
	GetConfiguredStandardCodePresetOperation(context.Context, string) (domain.StandardCodePresetOperation, bool, error)
}

func runHasOwnedFileWorkspace(ctx context.Context, store any, runID string) (bool, error) {
	if _, found, err := readRunFileDrydock(ctx, store, runID); err != nil || found {
		return found, apperror.Normalize(err)
	}
	if reader, ok := store.(runFilePresetStore); ok {
		_, found, err := reader.GetConfiguredStandardCodePresetOperation(ctx, runID)
		return found, apperror.Normalize(err)
	}
	return false, nil
}

// ResolveRunFileWorkspace is read-only. Ordinary Runs retain their registered
// source directory. A configured Run, or any Run that owns a Drydock, may never
// fall back to that source when the owned target is unavailable or has drifted.
func ResolveRunFileWorkspace(ctx context.Context, store RunFileWorkspaceStore,
	run domain.Run, mission domain.Mission, drydocks *DrydockService,
) (RunFileWorkspace, error) {
	if store == nil || run.ID == "" || run.MissionID != mission.ID ||
		run.SessionID == "" || strings.TrimSpace(mission.WorkspaceID) == "" {
		return RunFileWorkspace{}, apperror.New(apperror.CodeFailedPrecondition,
			"Run file workspace control binding is unavailable")
	}
	linked, err := store.GetSession(ctx, run.SessionID)
	if err != nil {
		return RunFileWorkspace{}, apperror.Normalize(err)
	}
	source, err := store.GetWorkspaceInfo(ctx, mission.WorkspaceID)
	if err != nil {
		return RunFileWorkspace{}, apperror.Normalize(err)
	}
	if linked.ID != run.SessionID || linked.WorkspaceID != mission.WorkspaceID ||
		source.ID != mission.WorkspaceID || strings.TrimSpace(source.RootPath) == "" {
		return RunFileWorkspace{}, apperror.New(apperror.CodeConflict,
			"Run, Session, and source workspace binding changed")
	}
	var owned drydock.Workspace
	var found bool
	owned, found, err = readRunFileDrydock(ctx, store, run.ID)
	if err != nil {
		return RunFileWorkspace{}, apperror.Normalize(err)
	}
	var preset domain.StandardCodePresetOperation
	var configured bool
	if reader, ok := store.(runFilePresetStore); ok {
		preset, configured, err = reader.GetConfiguredStandardCodePresetOperation(ctx, run.ID)
		if err != nil {
			return RunFileWorkspace{}, apperror.Normalize(err)
		}
	}
	if !found && !configured {
		return RunFileWorkspace{Source: source, Workspace: source}, nil
	}
	if !found || drydocks == nil {
		return RunFileWorkspace{}, apperror.New(apperror.CodeFailedPrecondition,
			"Run file workspace requires its exact Drydock; source fallback is unavailable")
	}

	if owned.RunID != run.ID {
		reader, ok := store.(interface {
			GetThreadDrydockBinding(context.Context, string) (domain.ThreadDrydockBinding, bool, error)
		})
		if !ok {
			return RunFileWorkspace{}, apperror.New(apperror.CodeConflict, "Run has no explicit Thread working directory binding")
		}
		binding, present, err := reader.GetThreadDrydockBinding(ctx, run.ID)
		if err != nil {
			return RunFileWorkspace{}, apperror.Normalize(err)
		}
		if !present || binding.RunID != run.ID || binding.DrydockID != owned.ID || binding.ThreadID == "" {
			return RunFileWorkspace{}, apperror.New(apperror.CodeConflict, "Run working directory continuation binding changed")
		}
	} else if owned.SessionID != run.SessionID {
		return RunFileWorkspace{}, apperror.New(apperror.CodeConflict, "Run working directory Session binding changed")
	}
	if owned.MissionID != mission.ID || owned.SourceWorkspaceID != source.ID ||
		owned.WorkspaceID == source.ID || owned.WorkspaceID == "" ||
		(owned.State != drydock.StateReady && owned.State != drydock.StateDelivered) ||
		(configured && (preset.RunID != run.ID || preset.MissionID != mission.ID ||
			preset.WorkspaceID != source.ID || preset.DrydockID != owned.ID)) {
		return RunFileWorkspace{}, apperror.New(apperror.CodeConflict,
			"Run file workspace Drydock binding changed")
	}
	exact, _, _, err := drydocks.loadExactDrydock(ctx, run.ID, owned.Generation, false)
	if err != nil {
		return RunFileWorkspace{}, err
	}
	if exact.ID != owned.ID || exact.WorkspaceID != owned.WorkspaceID ||
		exact.RunID != owned.RunID || exact.MissionID != mission.ID ||
		exact.SessionID != owned.SessionID || exact.SourceWorkspaceID != source.ID ||
		exact.Generation != owned.Generation ||
		(exact.State != drydock.StateReady && exact.State != drydock.StateDelivered) {
		return RunFileWorkspace{}, apperror.New(apperror.CodeConflict,
			"Run file workspace Drydock ownership changed during inspection")
	}
	return RunFileWorkspace{Source: source,
		Workspace: session.WorkspaceInfo{ID: exact.WorkspaceID, Name: exact.Name, RootPath: exact.Path},
		Drydock:   &exact}, nil
}

func requireCurrentRunFileDrydock(ctx context.Context, store any, runID string, physical drydock.Workspace) error {
	if reader, ok := store.(interface {
		RunOwnsCurrentDrydock(context.Context, string, string) (bool, error)
	}); ok {
		held, err := reader.RunOwnsCurrentDrydock(ctx, runID, physical.ID)
		if err != nil {
			return apperror.Normalize(err)
		}
		if !held {
			return apperror.New(apperror.CodeFailedPrecondition, "This execution no longer holds the Thread working directory")
		}
		return nil
	}
	if physical.RunID != runID {
		return apperror.New(apperror.CodeConflict, "Run working directory holder is unavailable")
	}
	return nil
}
