package fileedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerCreateMissingParentsDoesNotWriteUntilApply(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(newMemoryStore())
	edit, err := manager.Propose(t.Context(), Proposal{WorkspaceID: "ws-create-nested", WorkspaceRoot: root,
		Path: "test/nested/log-summary.test.mjs", Operation: OperationCreate, ExpectedOriginalHash: missingHash,
		ProposedText: "import test from 'node:test';\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "test")); !os.IsNotExist(err) {
		t.Fatalf("proposal created a directory: %v", err)
	}
	if _, err := manager.ApproveIntent(t.Context(), edit.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "test")); !os.IsNotExist(err) {
		t.Fatalf("review created a directory: %v", err)
	}
	applied, err := manager.Approve(t.Context(), edit.ID, root)
	if err != nil || applied.Status != StatusApplied {
		t.Fatalf("apply: %#v %v", applied, err)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(edit.Path)))
	if err != nil || string(data) != edit.ProposedText {
		t.Fatalf("file: %q %v", data, err)
	}
	if _, err := manager.Approve(t.Context(), edit.ID, root); err != nil {
		t.Fatal(err)
	}
}

func TestManagerNestedCreateRefusesChangedParentAndConcurrentTarget(t *testing.T) {
	for _, scenario := range []string{"file_parent", "concurrent_target", "symlink_parent"} {
		t.Run(scenario, func(t *testing.T) {
			path := t.TempDir()
			outside := t.TempDir()
			manager := NewManager(newMemoryStore())
			edit, err := manager.Propose(t.Context(), Proposal{WorkspaceID: "ws-nested", WorkspaceRoot: path,
				Path: "test/file.txt", Operation: OperationCreate, ExpectedOriginalHash: missingHash, ProposedText: "proposal"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.ApproveIntent(t.Context(), edit.ID); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "file_parent":
				if err := os.WriteFile(filepath.Join(path, "test"), []byte("user file"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "concurrent_target":
				if err := os.Mkdir(filepath.Join(path, "test"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "test/file.txt"), []byte("concurrent user"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink_parent":
				if err := os.Symlink(outside, filepath.Join(path, "test")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			applied, err := manager.Approve(t.Context(), edit.ID, path)
			if err == nil || applied.Status != StatusFailed {
				t.Fatalf("unsafe apply: %#v %v", applied, err)
			}
			if scenario == "concurrent_target" {
				data, err := os.ReadFile(filepath.Join(path, "test/file.txt"))
				if err != nil || string(data) != "concurrent user" {
					t.Fatalf("user file changed: %q %v", data, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(path, "test")); err != nil {
				t.Fatalf("user parent removed: %v", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("outside scope changed: %v %v", entries, err)
			}
		})
	}
}

func TestManagerCreateFailureRetainsParentsWithoutDeletingUserContent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(newMemoryStore())
	// The first new parent is valid; the following component exceeds the
	// filesystem component limit. This fails during actual rooted Mkdir.
	path := "existing/new/" + strings.Repeat("x", 300) + "/file.txt"
	edit, err := manager.Propose(t.Context(), Proposal{WorkspaceID: "ws-failed-create", WorkspaceRoot: root,
		Path: path, Operation: OperationCreate, ExpectedOriginalHash: missingHash, ProposedText: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ApproveIntent(t.Context(), edit.ID); err != nil {
		t.Fatal(err)
	}
	failed, err := manager.Approve(t.Context(), edit.ID, root)
	if err == nil || failed.Status != StatusFailed {
		t.Fatalf("expected bounded creation failure: %#v %v", failed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "existing/new")); err != nil {
		t.Fatalf("new parent must remain after failed publication: %v", err)
	}
	if !strings.Contains(failed.Reason, "parent directories, if any, are retained") {
		t.Fatalf("failed create did not explain possible directory effects: %s", failed.Reason)
	}
	if _, err := os.Stat(filepath.Join(root, "existing")); err != nil {
		t.Fatalf("preexisting directory was removed: %v", err)
	}
}
