package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/workspaceidentity"
)

func TestDrydockManagedRootMustBeDisjointFromSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	inside, err := NewDrydockExecutor(filepath.Join(source, "managed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := inside.requireDisjointSourceRoot(source); err == nil {
		t.Fatal("managed root inside the source Workspace was accepted")
	}

	managedParent := filepath.Join(parent, "managed-parent")
	outside, err := NewDrydockExecutor(managedParent)
	if err != nil {
		t.Fatal(err)
	}
	if err := outside.requireDisjointSourceRoot(filepath.Join(managedParent, "source")); err == nil {
		t.Fatal("source Workspace inside the managed root was accepted")
	}
	if err := outside.requireDisjointSourceRoot(source); err != nil {
		t.Fatalf("disjoint source and managed roots were rejected: %v", err)
	}
}

func TestCleanupDrydockReviewTemporaryRemovesOnlyExpectedOwnedEntries(t *testing.T) {
	t.Run("exact temporary index", func(t *testing.T) {
		root := t.TempDir()
		fingerprint, err := workspaceidentity.Fingerprint(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "index"), []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
		cleanupDrydockReviewTemporary(root, fingerprint)
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			t.Fatalf("exact temporary directory was not removed: %v", err)
		}
	})

	t.Run("unexpected user file is preserved", func(t *testing.T) {
		root := t.TempDir()
		fingerprint, err := workspaceidentity.Fingerprint(root)
		if err != nil {
			t.Fatal(err)
		}
		userPath := filepath.Join(root, "user-file.txt")
		if err := os.WriteFile(userPath, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		cleanupDrydockReviewTemporary(root, fingerprint)
		content, err := os.ReadFile(userPath)
		if err != nil || string(content) != "keep" {
			t.Fatalf("unexpected file was changed or removed: content=%q err=%v", content, err)
		}
	})

	t.Run("directory identity drift is preserved", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "review")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := workspaceidentity.Fingerprint(root)
		if err != nil {
			t.Fatal(err)
		}
		replacement := filepath.Join(parent, "previous-review")
		if err := os.Rename(root, replacement); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		userPath := filepath.Join(root, "index")
		if err := os.WriteFile(userPath, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		cleanupDrydockReviewTemporary(root, fingerprint)
		content, err := os.ReadFile(userPath)
		if err != nil || string(content) != "replacement" {
			t.Fatalf("replacement identity was changed or removed: content=%q err=%v", content, err)
		}
	})
}
