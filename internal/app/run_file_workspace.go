package app

import (
	"context"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/repository"
)

// Ordinary source Runs do not acquire a Git dependency. An owned/configured
// Run uses the same product-managed root as Drydock and command execution.
func (a *App) newRunFileDrydockService(ctx context.Context, runID string) (*application.DrydockService, error) {
	runID = strings.TrimSpace(runID)
	_, owned, err := a.store.GetDrydockByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	_, configured, err := a.store.GetConfiguredStandardCodePresetOperation(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !owned && !configured {
		return nil, nil
	}
	executor, err := repository.NewDrydockExecutor(filepath.Join(a.home, "drydocks"))
	if err != nil {
		return nil, err
	}
	drydocks, err := application.NewDrydockService(a.store, executor)
	if err != nil {
		return nil, err
	}
	checkpoints, err := application.NewWorkspaceCheckpointService(a.store,
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		return nil, err
	}
	return drydocks.WithCheckpointService(checkpoints), nil
}
