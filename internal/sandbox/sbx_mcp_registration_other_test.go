//go:build !windows

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSBXMCPRegistrationPathRefusesUnverifiedPlatformStore(t *testing.T) {
	if _, err := sbxMCPRegistrationPath(sbxTestMCPHelperName); !errors.Is(err, ErrSBXBoundary) {
		t.Fatal("unverified platform registration path was admitted")
	}
}

func TestSBXMCPStoredMetadataReaderRejectsUnixSymlinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "owned-registration.json")
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "redirected-registration.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := sbxReadMCPRegistration(t.Context(), alias); !errors.Is(err, ErrSBXBoundary) {
		t.Fatal("symlink metadata was read")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "unchanged" {
		t.Fatal("symlink target changed")
	}
}
