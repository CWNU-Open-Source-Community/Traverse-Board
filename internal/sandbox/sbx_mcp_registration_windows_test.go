//go:build windows

package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSBXMCPRegistrationPathUsesTheActualAccountProfile(t *testing.T) {
	profile, err := windows.GetCurrentProcessToken().GetUserProfileDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path, err := sbxMCPRegistrationPath(sbxTestMCPHelperName)
	want := filepath.Join(profile, "AppData", "Local", "DockerSandboxes", "sandboxes-"+SBXAppName,
		"state", "sandboxd", "mcp", "servers", sbxTestMCPHelperName+".json")
	if err != nil || path != want {
		t.Fatalf("registration path was redirected: %v", err)
	}
}

func TestSBXMCPStoredMetadataReaderRejectsWindowsJunctionParents(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "owned-server-store")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "owned-registration.json")
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "store-junction")
	if _, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, directory).CombinedOutput(); err != nil {
		t.Fatalf("create owned metadata junction: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(alias) })
	for _, candidate := range []string{
		filepath.Join(alias, "owned-registration.json"),
		filepath.Join(alias, "absent-registration.json"),
		filepath.Join(alias, "absent-servers", "absent-registration.json"),
	} {
		if _, err := sbxReadMCPRegistration(t.Context(), candidate); !errors.Is(err, ErrSBXBoundary) || errors.Is(err, os.ErrNotExist) {
			t.Fatal("redirected metadata store was read or authorized a new registration")
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "unchanged" {
		t.Fatal("junction target changed")
	}
}
