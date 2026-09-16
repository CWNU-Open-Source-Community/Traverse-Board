//go:build windows

package fileedit

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestManagerNestedCreateRejectsWindowsParentJunction(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	manager := NewManager(newMemoryStore())
	edit, err := manager.Propose(t.Context(), Proposal{WorkspaceID: "ws-junction", WorkspaceRoot: root,
		Path: "test/file.txt", Operation: OperationCreate, ExpectedOriginalHash: missingHash, ProposedText: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ApproveIntent(t.Context(), edit.ID); err != nil {
		t.Fatal(err)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(system, "cmd.exe"), "/d", "/c", "mklink", "/J", filepath.Join(root, "test"), filepath.Clean(outside))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, output)
	}
	failed, err := manager.Approve(t.Context(), edit.ID, root)
	if err == nil || failed.Status != StatusFailed {
		t.Fatalf("junction accepted: %#v %v", failed, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "test")); err != nil {
		t.Fatalf("user redirect removed: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside scope changed: %v %v", entries, err)
	}
}
