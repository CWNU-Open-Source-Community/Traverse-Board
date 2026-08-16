package repository

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/credential"
)

func remoteGit(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func newRemoteFixture(t *testing.T) (remoteURL, workRoot string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}
	base := t.TempDir()
	batch := filepath.Join(base, "remote.git")
	remoteGit(t, "init", "--bare", "--quiet", batch)
	workRoot = filepath.Join(base, "work")
	remoteGit(t, "init", "--quiet", workRoot)
	remoteGit(t, "-C", workRoot, "config", "user.email", "test@example.com")
	remoteGit(t, "-C", workRoot, "config", "user.name", "remote-test")
	if err := os.WriteFile(filepath.Join(workRoot, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remoteGit(t, "-C", workRoot, "add", "base.txt")
	remoteGit(t, "-C", workRoot, "commit", "--quiet", "-m", "baseline")
	remoteGit(t, "-C", workRoot, "branch", "-M", "main")
	remoteURL = "file://" + filepath.ToSlash(batch)
	remoteGit(t, "-C", workRoot, "push", "--quiet", remoteURL, "main")
	remoteGit(t, "-C", workRoot, "fetch", "--quiet", remoteURL, "main")
	return remoteURL, workRoot
}

func newRemoteExecutor(t *testing.T) *RemoteExecutor {
	t.Helper()
	executor, err := NewRemoteExecutor(credential.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	executor.allowLocalRemote = true
	return executor
}

func remoteSpec(op RemoteOperation, remoteURL, branch string) RemoteSpec {
	return RemoteSpec{
		ProtocolVersion: RemoteProtocolVersion, Operation: op,
		RemoteURL: remoteURL, Branch: branch,
		NetworkTTLMillis: 30_000,
	}
}

func TestRemoteSpecValidationFailsClosed(t *testing.T) {
	executor := newRemoteExecutor(t)
	executor.allowLocalRemote = false
	cases := []struct {
		name string
		spec RemoteSpec
	}{

		{name: "http scheme", spec: remoteSpec(RemoteFetch, "http://example.com/repo.git", "main")},

		{name: "loopback", spec: remoteSpec(RemoteFetch, "https://127.0.0.1/repo.git", "main")},

		{name: "credential in url", spec: remoteSpec(RemoteFetch, "https://user:pass@example.com/repo.git", "main")},

		{name: "unknown op", spec: RemoteSpec{ProtocolVersion: RemoteProtocolVersion, Operation: "force_push", RemoteURL: "https://example.com/r.git", Branch: "main", NetworkTTLMillis: 1000}},

		{name: "pr without base", spec: RemoteSpec{ProtocolVersion: RemoteProtocolVersion, Operation: RemoteCreatePR, RemoteURL: "https://example.com/r.git", Branch: "feat", PRTitle: "t", NetworkTTLMillis: 1000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := executor.ValidateSpec(tc.spec); err == nil {
				t.Fatalf("hostile spec accepted: %#v", tc.spec)
			}
		})
	}
}

func TestRemoteFetchAndFastForwardPull(t *testing.T) {
	ctx := context.Background()
	remoteURL, workRoot := newRemoteFixture(t)
	executor := newRemoteExecutor(t)
	// Advance the remote with an unrelated clone.
	other := filepath.Join(t.TempDir(), "other")
	remoteGit(t, "clone", "--quiet", "-b", "main", remoteURL, other)
	remoteGit(t, "-C", other, "config", "user.email", "x@example.com")
	remoteGit(t, "-C", other, "config", "user.name", "x")
	if err := os.WriteFile(filepath.Join(other, "adv.txt"), []byte("adv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remoteGit(t, "-C", other, "add", "adv.txt")
	remoteGit(t, "-C", other, "commit", "--quiet", "-m", "advance")
	remoteGit(t, "-C", other, "push", "--quiet", remoteURL, "main")
	advancedHead := remoteGit(t, "-C", other, "rev-parse", "HEAD")

	preHead := remoteGit(t, "-C", workRoot, "rev-parse", "HEAD")
	binding := RemoteBinding{ProtocolVersion: RemoteProtocolVersion, RunID: "run-1",
		WorkspaceID: "ws-1", LocalHead: preHead, RemoteHost: "local", Protocol: "https", Branch: "main"}
	spec := remoteSpec(RemotePullFF, remoteURL, "main")
	receipt, err := executor.ExecuteGit(ctx, workRoot, spec, binding, "op-key-0001")
	if err != nil {
		t.Fatalf("pull_ff: %v", err)
	}
	if receipt.PostHead != advancedHead || receipt.CommitID != advancedHead {
		t.Fatalf("fast-forward pull did not land on %s: %#v", advancedHead, receipt)
	}
}

func TestRemotePushBranchRejectsExistingBranch(t *testing.T) {
	ctx := context.Background()
	remoteURL, workRoot := newRemoteFixture(t)
	executor := newRemoteExecutor(t)
	preHead := remoteGit(t, "-C", workRoot, "rev-parse", "HEAD")
	binding := RemoteBinding{ProtocolVersion: RemoteProtocolVersion, RunID: "run-1",
		WorkspaceID: "ws-1", LocalHead: preHead, RemoteHost: "local", Protocol: "https", Branch: "main"}
	// main already exists on the remote → default-deny.
	spec := remoteSpec(RemotePushBranch, remoteURL, "main")
	if _, err := executor.ExecuteGit(ctx, workRoot, spec, binding, "op-key-0002"); err == nil ||
		apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("existing branch push was not rejected: %v", err)
	}
	// A brand-new branch pushes cleanly.
	remoteGit(t, "-C", workRoot, "checkout", "--quiet", "-b", "feat")
	if err := os.WriteFile(filepath.Join(workRoot, "feat.txt"), []byte("feat\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	remoteGit(t, "-C", workRoot, "add", "feat.txt")
	remoteGit(t, "-C", workRoot, "commit", "--quiet", "-m", "feat")
	featHead := remoteGit(t, "-C", workRoot, "rev-parse", "HEAD")
	binding.LocalHead = featHead
	binding.Branch = "feat"
	spec = remoteSpec(RemotePushBranch, remoteURL, "feat")
	if _, err := executor.ExecuteGit(ctx, workRoot, spec, binding, "op-key-0003"); err != nil {
		t.Fatalf("new branch push: %v", err)
	}
	remoteBranch := remoteGit(t, "ls-remote", remoteURL, "refs/heads/feat")
	if !strings.Contains(remoteBranch, featHead) {
		t.Fatalf("remote branch missing: %q", remoteBranch)
	}
}

func TestRemoteCredentialRedaction(t *testing.T) {
	if strings.Contains(redactRemoteOutput("error: https://token-abc@host", "token-abc"), "token-abc") {
		t.Fatal("credential leaked through redaction")
	}
	if redactRemoteOutput("plain", "") != "plain" {
		t.Fatal("plain output changed")
	}
}

func TestRemoteAskpassHelperResolvesCredentialByName(t *testing.T) {
	ctx := context.Background()
	store := credential.NewMemoryStore()
	if err := store.Put(ctx, "github-pat", "secret-token-value"); err != nil {
		t.Fatal(err)
	}
	executor, err := NewRemoteExecutor(store)
	if err != nil {
		t.Fatal(err)
	}
	path, secret, err := executor.askpassHelper(ctx, "github-pat")
	if err != nil || secret != "secret-token-value" || path == "" {
		t.Fatalf("askpass helper: path=%q secret=%q err=%v", path, secret, err)
	}
	_ = os.Remove(path)
}

var _ = time.Now
