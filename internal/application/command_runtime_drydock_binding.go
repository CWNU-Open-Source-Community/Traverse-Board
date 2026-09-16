package application

import (
	"context"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
)

// Physical ownership is historical. Runtime authority additionally requires
// the exact current logical Run/Session and the Thread's current holder.
func commandRuntimeDrydockBound(ctx context.Context, store any, workspace drydock.Workspace,
	runID, missionID, sessionID, sourceID string,
) (bool, error) {
	if workspace.MissionID != missionID || workspace.SourceWorkspaceID != sourceID ||
		(workspace.State != drydock.StateReady && workspace.State != drydock.StateDelivered) {
		return false, nil
	}
	if reader, ok := store.(interface {
		RunOwnsCurrentDrydock(context.Context, string, string) (bool, error)
		GetRun(context.Context, string) (domain.Run, error)
	}); ok {
		run, err := reader.GetRun(ctx, runID)
		if err != nil {
			return false, err
		}
		if run.ID != runID || run.MissionID != missionID || run.SessionID != sessionID {
			return false, nil
		}
		return reader.RunOwnsCurrentDrydock(ctx, runID, workspace.ID)
	}
	// Legacy embedders without the continuation contract can only use their
	// original physical owner, never infer a successor from matching paths.
	return workspace.RunID == runID && workspace.SessionID == sessionID, nil
}
