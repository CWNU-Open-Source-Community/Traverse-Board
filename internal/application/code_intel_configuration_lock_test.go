package application

import (
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func TestCodeIntelConfigurationPublicationLockIsExclusiveAndReusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "code-intel.json.lock")
	first, err := acquireCodeIntelConfigurationLock(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireCodeIntelConfigurationLock(path)
	if second != nil {
		_ = second.Close()
		t.Fatal("second publisher acquired an active exclusive lock")
	}
	if apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("contention error=%v", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		t.Fatalf("persistent lock sidecar=%#v err=%v", info, err)
	}
	third, err := acquireCodeIntelConfigurationLock(path)
	if err != nil {
		t.Fatalf("unheld sidecar blocked recovery: %v", err)
	}
	_ = third.Close()
	redirected := filepath.Join(t.TempDir(), "directory.lock")
	if err = os.Mkdir(redirected, 0o700); err != nil {
		t.Fatal(err)
	}
	if file, err := acquireCodeIntelConfigurationLock(redirected); err == nil {
		_ = file.Close()
		t.Fatal("publication accepted a nonregular lock")
	}
}
