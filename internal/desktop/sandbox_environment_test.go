package desktop

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/httpapi"
)

func TestDesktopSBXAssemblyCanonicalizesApplicationHome(t *testing.T) {
	t.Chdir(t.TempDir())
	backend, err := NewDesktopSBXBackend("relative-home", application.DefaultSandboxEnvironmentSettings())
	if err != nil {
		t.Fatalf("relative application home: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("relative-home", "sbx-owned")); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopSBXAssemblyAcceptsHomeAliasButRejectsJournalAlias(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "actual-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	backend, err := NewDesktopSBXBackend(alias, application.DefaultSandboxEnvironmentSettings())
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "other-home")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "sbx-owned"), filepath.Join(other, "sbx-owned")); err != nil {
		t.Fatal(err)
	}
	if backend, err := NewDesktopSBXBackend(other, application.DefaultSandboxEnvironmentSettings()); err == nil {
		_ = backend.Close()
		t.Fatal("aliased managed journal was accepted")
	}
}

func TestDesktopSandboxSettingsSaveLeavesActiveLaunchUnchanged(t *testing.T) {
	home := t.TempDir()
	active := application.DefaultSandboxEnvironmentSettings()
	plane, err := OpenControlPlane(ControlPlaneConfig{DatabasePath: filepath.Join(home, "test.db"), HomePath: home,
		ReadToken: desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		SandboxEnvironmentControlEnabled: true, SandboxEnvironmentSettings: &active})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plane.Close() })
	body := `{"version":"sandbox_environment.v1","expected_revision":1,"settings":{"default_backend":"docker","docker_enabled":true,"docker_image_digest":"","sbx_enabled":false,"sbx_template":""}}`
	response := desktopControlRequest(plane.Handler(), http.MethodPut, httpapi.SandboxEnvironmentPath, "", body)
	if response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var view httpapi.SandboxEnvironmentView
	if err := json.Unmarshal(envelope.Data, &view); err != nil {
		t.Fatal(err)
	}
	if !view.RestartRequired || view.CapabilityGrant || view.ActiveSettings.DockerEnabled ||
		!view.Settings.DockerEnabled || view.Settings.DefaultBackend != "docker" {
		t.Fatalf("save changed active authority: %+v", view)
	}
	if enabled, err := plane.DockerExecutionEnabled(); err != nil || enabled {
		t.Fatalf("live Docker authority changed: %t %v", enabled, err)
	}
	store, err := application.NewFileSandboxEnvironmentSettingsStore(home)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load(t.Context())
	if err != nil || saved.Settings.DefaultBackend != "docker" || saved.Revision != 2 {
		t.Fatalf("durable preference: %+v %v", saved, err)
	}
}
