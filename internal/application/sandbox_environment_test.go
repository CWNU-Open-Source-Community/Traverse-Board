package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func sandboxEnvironmentTestStore(t *testing.T) *FileSandboxEnvironmentSettingsStore {
	t.Helper()
	store, err := NewFileSandboxEnvironmentSettingsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func sandboxEnvironmentDockerSettings() SandboxEnvironmentSettings {
	return SandboxEnvironmentSettings{DefaultBackend: "docker", DockerEnabled: true,
		DockerImageDigest: "sha256:" + strings.Repeat("a", 64)}
}

func TestSandboxEnvironmentFileStoreCASReplayAndRestartRecovery(t *testing.T) {
	ctx := context.Background()
	store := sandboxEnvironmentTestStore(t)
	initial, err := store.Load(ctx)
	if err != nil || initial.Revision != 1 || initial.Settings != DefaultSandboxEnvironmentSettings() {
		t.Fatalf("initial = %#v, %v", initial, err)
	}
	settings := sandboxEnvironmentDockerSettings()
	saved, replayed, err := store.Save(ctx, 1, settings)
	if err != nil || replayed || saved.Revision != 2 || saved.Settings != settings {
		t.Fatalf("save = %#v replay=%v err=%v", saved, replayed, err)
	}
	// Simulate a lost response followed by process restart and exact request retry.
	reopened, err := NewFileSandboxEnvironmentSettingsStore(store.home)
	if err != nil {
		t.Fatal(err)
	}
	recovery, replayed, err := reopened.Save(ctx, 1, settings)
	if err != nil || !replayed || recovery != saved {
		t.Fatalf("recovery = %#v replay=%v err=%v", recovery, replayed, err)
	}
	if _, _, err := reopened.Save(ctx, 1, DefaultSandboxEnvironmentSettings()); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale different request = %v", err)
	}
	noOp, replayed, err := reopened.Save(ctx, 2, settings)
	if err != nil || replayed || noOp != saved {
		t.Fatalf("no-op = %#v replay=%v err=%v", noOp, replayed, err)
	}
	changed := settings
	changed.DefaultBackend = "local"
	changed.SBXEnabled = true
	changed.SBXTemplate = "registry.example/sandbox/go@sha256:" + strings.Repeat("b", 64)
	third, _, err := reopened.Save(ctx, 2, changed)
	if err != nil || third.Revision != 3 {
		t.Fatalf("second atomic publication = %#v, %v", third, err)
	}
	if _, _, err := reopened.Save(ctx, 1, settings); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("older replay should conflict: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil || loaded != third {
		t.Fatalf("durable current = %#v, %v", loaded, err)
	}
	entries, err := os.ReadDir(store.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sandbox-environment-") {
			t.Fatalf("temporary publication leaked: %s", entry.Name())
		}
	}
}

func TestSandboxEnvironmentIndependentStoresCannotOverwriteConcurrentSave(t *testing.T) {
	first := sandboxEnvironmentTestStore(t)
	second, err := NewFileSandboxEnvironmentSettingsStore(first.home)
	if err != nil {
		t.Fatal(err)
	}
	root, err := first.openHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockSandboxEnvironmentSettings(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.Save(context.Background(), 1, sandboxEnvironmentDockerSettings()); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("another process lock was ignored: %v", err)
	}
	_ = lock.Close()
	_ = root.Close()
	start := make(chan struct{})
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for index, current := range []*FileSandboxEnvironmentSettingsStore{first, second} {
		wait.Add(1)
		go func(index int, current *FileSandboxEnvironmentSettingsStore) {
			defer wait.Done()
			<-start
			settings := sandboxEnvironmentDockerSettings()
			settings.DockerImageDigest = "sha256:" + strings.Repeat(string(rune('a'+index)), 64)
			_, _, err := current.Save(context.Background(), 1, settings)
			errors <- err
		}(index, current)
	}
	close(start)
	wait.Wait()
	close(errors)
	successes, conflicts := 0, 0
	for err := range errors {
		switch apperror.CodeOf(err) {
		case "":
			successes++
		case apperror.CodeConflict:
			conflicts++
		default:
			t.Fatalf("concurrent save = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	loaded, err := first.Load(context.Background())
	if err != nil || loaded.Revision != 2 {
		t.Fatalf("current = %#v, %v", loaded, err)
	}
}

func TestSandboxEnvironmentCorruptStorageIsNeverSilentlyOverwritten(t *testing.T) {
	valid, err := json.Marshal(sandboxEnvironmentSettingsRecord{Version: SandboxEnvironmentVersion, Revision: 2, Settings: sandboxEnvironmentDockerSettings()})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"duplicate top field": strings.Replace(string(valid), `"revision":2`, `"revision":1,"revision":2`, 1),
		"duplicate settings":  strings.Replace(string(valid), `"docker_enabled":true`, `"docker_enabled":false,"docker_enabled":true`, 1),
		"missing boolean":     strings.Replace(string(valid), `"sbx_enabled":false,`, "", 1),
		"null boolean":        strings.Replace(string(valid), `"sbx_enabled":false`, `"sbx_enabled":null`, 1),
		"case variant":        strings.Replace(string(valid), `"revision":`, `"Revision":`, 1),
		"unknown":             strings.Replace(string(valid), `"settings":`, `"owner_token":"private","settings":`, 1),
		"trailing object":     string(valid) + `{}`,
		"oversized":           strings.Repeat(" ", maxSandboxEnvironmentSettingsBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			store := sandboxEnvironmentTestStore(t)
			path := filepath.Join(store.home, sandboxEnvironmentSettingsFile)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(context.Background()); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("load accepted corrupt storage: %v", err)
			}
			if _, _, err := store.Save(context.Background(), 2, DefaultSandboxEnvironmentSettings()); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("save overwrote corrupt storage: %v", err)
			}
			unchanged, err := os.ReadFile(path)
			if err != nil || string(unchanged) != body {
				t.Fatalf("corrupt evidence was changed: %v", err)
			}
		})
	}
}

func TestSandboxEnvironmentHomeRotationDoesNotRedirectSettings(t *testing.T) {
	parent := t.TempDir()
	store, err := NewFileSandboxEnvironmentSettingsStore(filepath.Join(parent, "settings"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(store.home, filepath.Join(parent, "previous")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.home, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(context.Background(), 1, sandboxEnvironmentDockerSettings()); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("rotated home accepted: %v", err)
	}
}

func TestSandboxEnvironmentSymlinkDoesNotRedirectSettings(t *testing.T) {
	store := sandboxEnvironmentTestStore(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store.home, sandboxEnvironmentSettingsFile)); err != nil {
		t.Skipf("OS does not permit test symlinks: %v", err)
	}
	if _, _, err := store.Save(context.Background(), 1, sandboxEnvironmentDockerSettings()); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("symlink accepted: %v", err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil || string(raw) != "preserve" {
		t.Fatalf("outside file changed: %v", err)
	}
}

func TestSandboxEnvironmentSavePreservesActiveGatesAndNeverProbes(t *testing.T) {
	store := sandboxEnvironmentTestStore(t)
	calls := 0
	probe := func(_ context.Context, active SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
		calls++
		if active != DefaultSandboxEnvironmentSettings() {
			t.Fatalf("probe consumed saved settings before restart: %#v", active)
		}
		return SandboxEnvironmentObservation{Installed: true, Ready: true}, nil
	}
	service, err := NewSandboxEnvironmentService(store, DefaultSandboxEnvironmentSettings(), SandboxEnvironmentProbes{Local: probe, Docker: probe, SBX: probe})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.SaveSandboxEnvironment(context.Background(), SaveSandboxEnvironmentRequest{
		Version: SandboxEnvironmentVersion, ExpectedRevision: 1, Settings: sandboxEnvironmentDockerSettings(),
	})
	if err != nil || calls != 0 || saved.ProbeStatus != "not_checked" || !saved.RestartRequired || saved.CapabilityGrant || saved.ActiveSettings != DefaultSandboxEnvironmentSettings() {
		t.Fatalf("save = %#v calls=%d err=%v", saved, calls, err)
	}
	for _, row := range saved.Backends {
		if row.Status != "not_checked" || row.Installed || row.Ready || len(row.Blockers) != 1 || row.Blockers[0].Code != "ENVIRONMENT_RECHECK_REQUIRED" {
			t.Fatalf("save claimed readiness: %#v", row)
		}
	}
	checked, err := service.SandboxEnvironment(context.Background())
	if err != nil || calls != 3 || checked.ProbeStatus != "checked" || !checked.RestartRequired || checked.Backends[0].Status != "ready" {
		t.Fatalf("get = %#v calls=%d err=%v", checked, calls, err)
	}
	for _, row := range checked.Backends[1:] {
		if !row.Installed || row.Enabled || row.Ready || row.Status != "disabled" {
			t.Fatalf("saved preference escaped active gate: %#v", row)
		}
	}
	restarted, err := NewSandboxEnvironmentService(store, saved.Settings, SandboxEnvironmentProbes{Local: probe})
	if err != nil {
		t.Fatal(err)
	}
	// The explicit startup setting change is the only way to clear this flag.
	if restarted.view(SandboxEnvironmentSettingsSnapshot{Revision: saved.Revision, Settings: saved.Settings}, false).RestartRequired {
		t.Fatal("restarted settings still require restart")
	}
}

func TestSandboxEnvironmentReadinessErrorsAndInvalidEvidenceAreBounded(t *testing.T) {
	secret := "sk-" + strings.Repeat("x", 40)
	for name, probe := range map[string]SandboxEnvironmentProbe{
		"provider error": func(context.Context, SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
			return SandboxEnvironmentObservation{}, errors.New("stderr " + secret)
		},
		"secret blocker": func(context.Context, SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
			return SandboxEnvironmentObservation{Blockers: []SandboxEnvironmentBlocker{{Code: "PRIVATE", Message: secret}}}, nil
		},
		"secret code": func(context.Context, SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
			return SandboxEnvironmentObservation{Blockers: []SandboxEnvironmentBlocker{{Code: secret, Message: "重新检测当前环境。"}}}, nil
		},
		"control text": func(context.Context, SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
			return SandboxEnvironmentObservation{Blockers: []SandboxEnvironmentBlocker{{Code: "UNAVAILABLE", Message: "诊断\x1b"}}}, nil
		},
		"contradictory ready": func(context.Context, SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
			return SandboxEnvironmentObservation{Ready: true}, nil
		},
		"overlarge blockers": func(context.Context, SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
			return SandboxEnvironmentObservation{Blockers: make([]SandboxEnvironmentBlocker, 9)}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, err := NewSandboxEnvironmentService(sandboxEnvironmentTestStore(t), DefaultSandboxEnvironmentSettings(), SandboxEnvironmentProbes{Local: probe})
			if err != nil {
				t.Fatal(err)
			}
			view, err := service.SandboxEnvironment(context.Background())
			if err != nil || view.Backends[0].Ready || view.Backends[0].Status != "unavailable" || view.Backends[0].Blockers[0].Code != "PROBE_FAILED" {
				t.Fatalf("invalid observation accepted: %#v %v", view, err)
			}
			raw, _ := json.Marshal(view)
			if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "stderr") {
				t.Fatalf("private diagnostic escaped: %s", raw)
			}
		})
	}
}

func TestSandboxEnvironmentEnabledSetupWithoutPinsStaysUnconfigured(t *testing.T) {
	settings := SandboxEnvironmentSettings{DefaultBackend: "sbx", DockerEnabled: true, SBXEnabled: true}
	probe := func(ctx context.Context, active SandboxEnvironmentSettings) (SandboxEnvironmentObservation, error) {
		if _, bounded := ctx.Deadline(); !bounded || active != settings {
			t.Fatal("probe did not receive bounded active startup settings")
		}
		return SandboxEnvironmentObservation{Installed: true}, nil
	}
	store := sandboxEnvironmentTestStore(t)
	if _, _, err := store.Save(context.Background(), 1, settings); err != nil {
		t.Fatal(err)
	}
	service, err := NewSandboxEnvironmentService(store, settings, SandboxEnvironmentProbes{Docker: probe, SBX: probe})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.SandboxEnvironment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Backends[1:] {
		if !row.Enabled || !row.Installed || row.Configured || row.Ready || row.Status != "configuration_required" || row.Blockers[0].Code != "PINNED_REFERENCE_REQUIRED" {
			t.Fatalf("setup granted unpinned readiness: %#v", row)
		}
	}
}

func TestSandboxEnvironmentSettingsRejectUnpinnedAndAmbiguousInput(t *testing.T) {
	for name, settings := range map[string]SandboxEnvironmentSettings{
		"unknown default":  {DefaultBackend: "auto"},
		"disabled default": {DefaultBackend: "docker"},
		"tagged Docker":    {DefaultBackend: "local", DockerImageDigest: "ubuntu:latest"},
		"uppercase digest": {DefaultBackend: "local", DockerImageDigest: "sha256:" + strings.Repeat("A", 64)},
		"tagged SBX":       {DefaultBackend: "local", SBXTemplate: "registry.example/template:latest"},
		"SBX flags":        {DefaultBackend: "local", SBXTemplate: "--cloud@sha256:" + strings.Repeat("a", 64)},
		"bare SBX digest":  {DefaultBackend: "local", SBXTemplate: "sha256:" + strings.Repeat("a", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			store := sandboxEnvironmentTestStore(t)
			if _, _, err := store.Save(context.Background(), 1, settings); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
				t.Fatalf("invalid settings accepted: %v", err)
			}
			current, err := store.Load(context.Background())
			if err != nil || current.Settings != DefaultSandboxEnvironmentSettings() || current.Revision != 1 {
				t.Fatalf("invalid write changed state: %#v %v", current, err)
			}
		})
	}
	store := sandboxEnvironmentTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.Save(ctx, 1, sandboxEnvironmentDockerSettings()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save = %v", err)
	}
	current, err := store.Load(context.Background())
	if err != nil || current.Revision != 1 {
		t.Fatalf("cancelled save changed state: %#v %v", current, err)
	}
}
