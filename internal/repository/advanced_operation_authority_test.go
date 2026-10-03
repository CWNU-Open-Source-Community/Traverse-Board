package repository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/toolcontract"
)

func TestAdvancedDispatchGuardChecksActualFrozenPreviewBeforeGit(t *testing.T) {
	root := newMutationRepo(t)
	executor := newAdvancedExecutor(t)
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("unapproved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	discovery, err := executor.ReviewAdvanced(t.Context(), root, advancedSpec(gitadvanced.HunkStage))
	if err != nil || len(discovery.Hunks) != 1 {
		t.Fatalf("discovery: %v %#v", err, discovery)
	}
	spec := advancedSpec(gitadvanced.HunkStage)
	spec.HunkIDs = []string{discovery.Hunks[0].ID}
	preview, err := executor.ReviewAdvanced(t.Context(), root, spec)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := AdvancedOperation(preview)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	guard := func(_ context.Context, actual string) error {
		checks++
		if actual != expected {
			t.Fatalf("adapter changed its final fingerprint: %s != %s", actual, expected)
		}
		return errors.New("revoked exact operation")
	}
	receipt, err := executor.ExecuteAdvanced(t.Context(), root, preview, guard)
	if err == nil || checks != 1 || receipt.Status != gitadvanced.ReceiptFailed ||
		receipt.PostBinding.RepositorySHA256 == "" {
		t.Fatalf("guard refusal lost observed state: checks=%d err=%v receipt=%#v", checks, err, receipt)
	}
	if got := fixtureGit(t, "-C", root, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("guard refusal mutated the index: %s", got)
	}
}

func TestAdvancedDispatchGuardPreservesPartialEffectsAndStopsLaterMutation(t *testing.T) {
	root := newMutationRepo(t)
	fixtureGit(t, "-C", root, "config", "core.autocrlf", "false")
	executor := newAdvancedExecutor(t)
	payload := []byte("approved first step\n")
	if err := os.WriteFile(filepath.Join(root, "base.txt"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, "-C", root, "stash", "push", "--quiet", "--message", "guarded synthetic fixture")
	stashOID := fixtureGit(t, "-C", root, "rev-parse", "refs/stash")
	spec := advancedSpec(gitadvanced.StashPop)
	spec.StashOID = stashOID
	preview, err := executor.ReviewAdvanced(t.Context(), root, spec)
	if err != nil {
		t.Fatal(err)
	}
	deny := false
	executor.commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "stash" && args[i+1] == "drop" {
				deny = true // Revoke after apply, immediately before the drop sink.
			}
		}
		return repositoryCommandContext(ctx, name, args...)
	}
	checks := 0
	guard := func(context.Context, string) error {
		checks++
		if deny {
			return errors.New("revoked before later mutation")
		}
		return nil
	}
	receipt, err := executor.ExecuteAdvanced(t.Context(), root, preview, guard)
	if err == nil || !deny || checks < 2 || receipt.Status != gitadvanced.ReceiptFailed ||
		receipt.PostBinding.RepositorySHA256 == "" || receipt.TargetOID != stashOID {
		t.Fatalf("partial result missing: checks=%d err=%v receipt=%#v", checks, err, receipt)
	}
	content, err := os.ReadFile(filepath.Join(root, "base.txt"))
	if err != nil || string(content) != string(payload) {
		t.Fatalf("first-step bytes were silently rolled back: %q %v", content, err)
	}
	if got := fixtureGit(t, "-C", root, "rev-parse", "refs/stash"); got != stashOID {
		t.Fatalf("revoked second step deleted the original stash: %s", got)
	}
}
