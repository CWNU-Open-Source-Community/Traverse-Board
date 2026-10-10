//go:build desktop

package main

import (
	"context"

	"cyberagent-workbench/internal/application"
)

// Only the normal product launch and fixed native restart profiles consume
// saved backend preferences. Explicit command-line launch gates retain their
// original meaning, including operator-preview and security test workers.
func loadDesktopSandboxSettings(ctx context.Context, config desktopOptions, home string) (
	desktopOptions, *application.SandboxEnvironmentSettings, error,
) {
	if !config.sandboxSettings {
		return config, nil, nil
	}
	store, err := application.NewFileSandboxEnvironmentSettingsStore(home)
	if err != nil {
		return config, nil, err
	}
	snapshot, err := store.Load(ctx)
	if err != nil {
		return config, nil, err
	}
	config.dockerExecution = snapshot.Settings.DockerEnabled
	return config, &snapshot.Settings, nil
}
