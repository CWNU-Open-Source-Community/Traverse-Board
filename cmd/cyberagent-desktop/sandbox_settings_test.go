//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/desktop"
)

func TestDesktopSandboxSettingsLoadOnlyForProductAndFixedRestart(t *testing.T) {
	home := t.TempDir()
	store, err := application.NewFileSandboxEnvironmentSettingsStore(home)
	if err != nil {
		t.Fatal(err)
	}
	settings := application.SandboxEnvironmentSettings{DefaultBackend: "sbx", DockerEnabled: true,
		DockerImageDigest: "sha256:" + strings.Repeat("a", 64), SBXEnabled: true,
		SBXTemplate: "example.test/code@sha256:" + strings.Repeat("b", 64)}
	if _, _, err := store.Save(t.Context(), 1, settings); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"--safe-view"}, {"--operator-preview"}, {"--enable-profile-control"}} {
		before, err := parseDesktopOptions(args)
		if err != nil {
			t.Fatal(err)
		}
		after, loaded, err := loadDesktopSandboxSettings(t.Context(), before, home)
		if err != nil {
			t.Fatal(err)
		}
		if len(args) == 0 {
			if loaded == nil || *loaded != settings || !after.dockerExecution {
				t.Fatalf("saved settings missing: %+v", loaded)
			}
			before.dockerExecution = true
		} else if loaded != nil {
			t.Fatalf("explicit launch inherited preferences: %v", args)
		}
		if before != after {
			t.Fatalf("settings changed unrelated launch authority for %v", args)
		}
	}
	for _, profile := range []desktop.DesktopRiskProfile{desktop.DesktopRiskProfileSandbox, desktop.DesktopRiskProfileSandboxDebug, desktop.DesktopRiskProfileDebug} {
		before, err := desktopOptionsForRiskProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		after, loaded, err := loadDesktopSandboxSettings(t.Context(), before, home)
		if err != nil || loaded == nil || *loaded != settings {
			t.Fatalf("restart lost preferences: %+v %v", loaded, err)
		}
		before.dockerExecution = true
		if before != after {
			t.Fatal("settings changed the fixed restart profile")
		}
	}
}

func TestDesktopSandboxSettingsDefaultLocalAndRejectCorruptSavedChoice(t *testing.T) {
	home := t.TempDir()
	config, err := parseDesktopOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	loadedConfig, settings, err := loadDesktopSandboxSettings(t.Context(), config, home)
	if err != nil || settings == nil || *settings != application.DefaultSandboxEnvironmentSettings() || loadedConfig.dockerExecution {
		t.Fatalf("fresh launch defaults: %+v %v", settings, err)
	}
	if err := os.WriteFile(filepath.Join(home, "sandbox-environment.json"), []byte(`{"broken":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadDesktopSandboxSettings(t.Context(), config, home); err == nil {
		t.Fatal("corrupt settings silently selected another environment")
	}
}
