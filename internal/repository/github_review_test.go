package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureReviewDiffEvidenceBindsMergeBaseHunksAndDirtyState(t *testing.T) {
	root := newMutationRepo(t)
	base := strings.TrimSpace(fixtureGit(t, "-C", root, "rev-parse", "HEAD"))
	path := filepath.Join(root, "review.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, "-C", root, "add", "review.txt")
	fixtureGit(t, "-C", root, "commit", "--quiet", "-m", "review change")
	head := strings.TrimSpace(fixtureGit(t, "-C", root, "rev-parse", "HEAD"))
	executor := newAdvancedExecutor(t)
	evidence, err := executor.CaptureReviewDiffEvidence(context.Background(), root, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.MergeBaseSHA != base || evidence.HeadSHA != head || !evidence.Complete ||
		evidence.DiffSHA256 == "" || evidence.CallChainSHA256 == "" ||
		len(evidence.ChangedFiles) != 1 || evidence.ChangedFiles[0] != "review.txt" ||
		len(evidence.Hunks) != 1 || evidence.Hunks[0].Path != "review.txt" ||
		evidence.Hunks[0].WorktreeSHA256 == "" {
		t.Fatalf("Git review evidence is incomplete: %#v", evidence)
	}
	if err := evidence.Validate(); err != nil {
		t.Fatalf("Git review evidence contract failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("local drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	drifted, err := executor.CaptureReviewDiffEvidence(context.Background(), root, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Binding.WorktreeSHA256 == evidence.Binding.WorktreeSHA256 ||
		drifted.Hunks[0].WorktreeSHA256 == evidence.Hunks[0].WorktreeSHA256 {
		t.Fatal("dirty worktree drift did not invalidate Git review evidence")
	}
}

func TestCaptureReviewDiffEvidenceRejectsHeadDrift(t *testing.T) {
	root := newMutationRepo(t)
	head := strings.TrimSpace(fixtureGit(t, "-C", root, "rev-parse", "HEAD"))
	other := strings.Repeat("a", 40)
	_, err := newAdvancedExecutor(t).CaptureReviewDiffEvidence(context.Background(), root, head, other)
	if err == nil || !strings.Contains(err.Error(), "local HEAD") {
		t.Fatalf("head drift was not rejected: %v", err)
	}
}
