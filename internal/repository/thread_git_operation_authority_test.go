package repository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/toolcontract"
)

func TestThreadGitGuardBindsActualRootBeforeStartingGit(t *testing.T) {
	root, other := threadGitFixture(t), threadGitFixture(t)
	executor, _ := NewMutationExecutor()
	if err := os.WriteFile(filepath.Join(root, "chosen.txt"), []byte("reviewed root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := executor.ReviewSelected(t.Context(), root, []string{"chosen.txt"})
	if err != nil {
		t.Fatal(err)
	}
	author, err := executor.ReadCommitAuthor(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	marker := strings.Repeat("c", 64)
	operation, err := SelectedCommitOperation(root, review, "exact root", marker, author)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	guard := func(_ context.Context, actual string) error {
		checked = true
		if actual == expected {
			t.Fatal("different root retained the approved operation fingerprint")
		}
		return errors.New("actual root differs from approved native inputs")
	}
	if _, err := executor.PrepareSelectedCommitAuthorized(t.Context(), other, review, "exact root", marker, author, guard); err == nil || !checked {
		t.Fatalf("root drift escaped dispatch guard: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, ".git", "index.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root drift created index lock: %v", err)
	}
	if got := threadGitTestCommand(t, other, "show", "HEAD:chosen.txt"); got != "original" {
		t.Fatalf("unapproved root changed: %s", got)
	}
}

func TestThreadGitGuardFreezesSelectedBytesAndChecksEveryPublication(t *testing.T) {
	root := threadGitFixture(t)
	executor, _ := NewMutationExecutor()
	if err := os.WriteFile(filepath.Join(root, "chosen.txt"), []byte("reviewed bytes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := executor.ReviewSelected(t.Context(), root, []string{"chosen.txt"})
	if err != nil {
		t.Fatal(err)
	}
	author, err := executor.ReadCommitAuthor(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	marker := strings.Repeat("a", 64)
	operation, err := SelectedCommitOperation(root, review, "frozen", marker, author)
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
			t.Fatalf("actual operation drifted: %s != %s", actual, expected)
		}
		if checks == 1 {
			review.Files[0].Content[0] = 'X'
			review.Files[0].Path = "other.txt"
			review.Files[0].SHA256 = strings.Repeat("f", 64)
		}
		return nil
	}
	prepared, err := executor.PrepareSelectedCommitAuthorized(t.Context(), root, review, "frozen", marker, author, guard)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	beforePublish := checks
	if err := prepared.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if checks <= beforePublish {
		t.Fatal("publication reused preparation authority without checking")
	}
	if got := threadGitTestCommand(t, root, "show", "HEAD:chosen.txt"); got != "reviewed bytes" {
		t.Fatalf("caller mutation changed commit: %q", got)
	}
	if got := threadGitTestCommand(t, root, "show", "HEAD:other.txt"); got != "original" {
		t.Fatalf("caller changed selected path: %q", got)
	}
}

func TestThreadGitGuardStopsRefAndIndexPublicationWithoutRollingBackEffects(t *testing.T) {
	for _, boundary := range []string{"before_ref", "before_index"} {
		t.Run(boundary, func(t *testing.T) {
			root := threadGitFixture(t)
			executor, _ := NewMutationExecutor()
			if err := os.WriteFile(filepath.Join(root, "chosen.txt"), []byte("pending\n"), 0600); err != nil {
				t.Fatal(err)
			}
			review, err := executor.ReviewSelected(t.Context(), root, []string{"chosen.txt"})
			if err != nil {
				t.Fatal(err)
			}
			author, err := executor.ReadCommitAuthor(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			publishing, denied := false, false
			executor.commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
				if publishing && boundary == "before_ref" {
					for _, arg := range args {
						if arg == "update-ref" {
							denied = true
						}
					}
				}
				return repositoryCommandContext(ctx, name, args...)
			}
			guard := func(context.Context, string) error {
				if publishing && boundary == "before_index" && threadGitTestCommand(t, root, "rev-parse", "HEAD") != review.Binding.Head {
					denied = true
				}
				if denied {
					return errors.New("revoked at actual native publication")
				}
				return nil
			}
			prepared, err := executor.PrepareSelectedCommitAuthorized(t.Context(), root, review, "publication boundary", strings.Repeat("b", 64), author, guard)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			publishing = true
			if err := prepared.Publish(t.Context()); err == nil || !denied {
				t.Fatalf("publication passed revoked guard: %v", err)
			}
			head := threadGitTestCommand(t, root, "rev-parse", "HEAD")
			if boundary == "before_ref" && head != review.Binding.Head {
				t.Fatal("denied ref update was executed")
			}
			if boundary == "before_index" && head != prepared.CommitOID {
				t.Fatal("partial ref effect was silently rolled back")
			}
			after, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil || string(after) != string(index) {
				t.Fatalf("index published after denial: %v", err)
			}
			if observed, err := executor.ObserveSelectedCommit(t.Context(), root, *prepared); err != nil || observed {
				t.Fatalf("partial operation was reported complete: %v %v", observed, err)
			}
		})
	}
}

type threadGitRevokingCredential struct {
	credential.Store
	afterGet func()
}

func (s threadGitRevokingCredential) Get(ctx context.Context, name string) (string, bool, error) {
	value, found, err := s.Store.Get(ctx, name)
	s.afterGet()
	return value, found, err
}

func TestThreadGitRemoteGuardRejectsAfterCredentialsBeforeAnyPush(t *testing.T) {
	root := threadGitFixture(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	threadGitTestCommand(t, root, "init", "--bare", "-q", bare)
	credentials := credential.NewMemoryStore()
	if err := credentials.Put(t.Context(), "thread-git-test", "fixture-not-real"); err != nil {
		t.Fatal(err)
	}
	revoked := false
	remote, _ := NewRemoteExecutor(threadGitRevokingCredential{Store: credentials, afterGet: func() { revoked = true }})
	remote.AllowLocalRemotesForTest()
	spec := RemoteSpec{ProtocolVersion: RemoteProtocolVersion, Operation: RemotePushBranch, RemoteURL: "file:///" + filepath.ToSlash(bare), Branch: "never-created",
		NetworkTTLMillis: 30000, CommitOID: threadGitTestCommand(t, root, "rev-parse", "HEAD"), ExpectedRemoteOID: "missing", CredentialName: "thread-git-test"}
	binding := RemoteBinding{LocalHead: spec.CommitOID}
	operation, err := ThreadRemoteOperation(root, spec, binding, "exact-key")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	guard := func(_ context.Context, actual string) error {
		if actual != expected {
			t.Fatalf("remote native inputs changed: %s != %s", actual, expected)
		}
		if revoked {
			return errors.New("credential resolution revoked native authority")
		}
		return nil
	}
	if _, err := remote.ExecuteGit(t.Context(), root, spec, binding, "exact-key", guard); err == nil || !revoked {
		t.Fatalf("credential revocation ignored: %v", err)
	}
	if got := threadGitTestCommand(t, bare, "for-each-ref", "refs/heads"); got != "" {
		t.Fatalf("push ran after credential revocation: %s", got)
	}
}
