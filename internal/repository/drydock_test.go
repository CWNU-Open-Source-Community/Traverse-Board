package repository

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCaptureDrydockDeliveryAggregatesCommittedIndexWorktreeAndUntrackedState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := filepath.Join(t.TempDir(), "delivery source")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runDrydockDeliveryGit(t, root, "init", "-q", "-b", "main")
	runDrydockDeliveryGit(t, root, "config", "user.email", "delivery@example.invalid")
	runDrydockDeliveryGit(t, root, "config", "user.name", "Delivery Test")
	for _, name := range []string{"committed.txt", "index.txt", "worktree.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runDrydockDeliveryGit(t, root, "add", ".")
	runDrydockDeliveryGit(t, root, "commit", "-q", "-m", "baseline")
	base := runDrydockDeliveryGit(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "committed.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDrydockDeliveryGit(t, root, "add", "committed.txt")
	runDrydockDeliveryGit(t, root, "commit", "-q", "-m", "committed change")
	if err := os.WriteFile(filepath.Join(root, "index.txt"), []byte("index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDrydockDeliveryGit(t, root, "add", "index.txt")
	if err := os.WriteFile(filepath.Join(root, "worktree.txt"), []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statusBefore := runDrydockDeliveryGit(t, root, "status", "--porcelain=v1")
	executor, err := NewDrydockExecutor(filepath.Join(t.TempDir(), "managed"))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := executor.CaptureDelivery(context.Background(), root, base)
	if err != nil {
		t.Fatal(err)
	}
	statusAfter := runDrydockDeliveryGit(t, root, "status", "--porcelain=v1")
	if statusAfter != statusBefore {
		t.Fatalf("read-only delivery capture changed Git state:\nbefore=%q\nafter=%q",
			statusBefore, statusAfter)
	}
	states := make(map[string]DrydockDeliveryPathState, len(evidence.PathStates))
	for _, state := range evidence.PathStates {
		states[state.Path] = state
	}
	assert := func(path string, committed, index, worktree, untracked bool) {
		t.Helper()
		state, found := states[path]
		if !found || state.Committed != committed || state.IndexChanged != index ||
			state.WorktreeChanged != worktree || state.Untracked != untracked ||
			state.Tracked == untracked {
			t.Fatalf("path %s state=%+v found=%t", path, state, found)
		}
	}
	assert("committed.txt", true, false, false, false)
	assert("index.txt", false, true, false, false)
	assert("worktree.txt", false, false, true, false)
	assert("untracked.txt", false, false, false, true)
	if len(evidence.ChangedPaths) != 4 || len(evidence.PathStates) != 4 ||
		!strings.Contains(evidence.Patch, "untracked.txt") {
		t.Fatalf("combined delivery evidence is incomplete: paths=%v states=%+v",
			evidence.ChangedPaths, evidence.PathStates)
	}
}

func runDrydockDeliveryGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
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
