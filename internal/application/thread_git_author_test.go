package application

import (
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func TestThreadGitAuthorPreviewDriftMissingAndSealedReplay(t *testing.T) {
	svc, st, threadID, runID, root := threadGitApplicationFixture(t)
	ctx := t.Context()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, nil, 0600); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, "-C", root, "config", "--local", "--unset-all", "user.name")
	runFixtureGit(t, "-C", root, "config", "--local", "--unset-all", "user.email")
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("author-bound content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: ThreadGitSpec{Operation: "commit", Paths: []string{"selected.txt"}, Message: "exact reviewed author"}}
	missing, err := svc.Preview(ctx, threadID, input)
	if err != nil || missing.CanExecute || missing.BlockedReason == "" || missing.CommitAuthor != nil {
		t.Fatalf("missing identity preview=%#v %v", missing, err)
	}
	if err := os.WriteFile(global, []byte("[user]\nname = Original Global\nemail = original@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Preview(ctx, threadID, input)
	if err != nil || !first.CanExecute || first.CommitAuthor == nil || first.CommitAuthor.Name != "Original Global" {
		t.Fatalf("first=%#v %v", first, err)
	}
	execute := ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: input.Spec, OperationKey: "author-preview-original", ExpectedPreviewFingerprint: first.PreviewFingerprint, RequestedBy: "desktop-ui"}
	oldHead := runFixtureGit(t, "-C", root, "rev-parse", "HEAD")
	if err := os.WriteFile(global, []byte("[user]\nname = Current Global\nemail = current@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(ctx, threadID, execute); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed preview author accepted: %v", err)
	}
	if _, found, err := st.GetGitMutationByKey(ctx, threadGitKey(threadID, execute.OperationKey)); err != nil || found {
		t.Fatalf("author drift created intent: %v %v", found, err)
	}
	if runFixtureGit(t, "-C", root, "rev-parse", "HEAD") != oldHead {
		t.Fatal("author drift published a commit")
	}
	fresh, err := svc.Preview(ctx, threadID, input)
	if err != nil || fresh.CommitAuthor == nil || fresh.CommitAuthor.Email != "current@example.invalid" || fresh.PreviewFingerprint == first.PreviewFingerprint {
		t.Fatalf("fresh author not fingerprinted: %#v %v", fresh, err)
	}
	execute.ExpectedPreviewFingerprint = fresh.PreviewFingerprint
	result, err := svc.Execute(ctx, threadID, execute)
	if err != nil || result.State != "completed" {
		t.Fatalf("global-only commit=%#v %v", result, err)
	}
	if got := runFixtureGit(t, "-C", root, "show", "--no-patch", "--format=%an|%ae|%cn|%ce", result.CommitOID); got != "Current Global|current@example.invalid|Current Global|current@example.invalid" {
		t.Fatalf("commit guessed identity: %q", got)
	}
	before, err := st.ListRunEvents(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("[user]\nname = Later Global\nemail = later@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	replay, err := svc.Execute(ctx, threadID, execute)
	if err != nil || !replay.Replayed || replay.CommitOID != result.CommitOID {
		t.Fatalf("sealed replay recomputed author: %#v %v", replay, err)
	}
	after, err := st.ListRunEvents(ctx, runID)
	if err != nil || len(before) != len(after) {
		t.Fatalf("sealed replay mutated history: %v", err)
	}
}
