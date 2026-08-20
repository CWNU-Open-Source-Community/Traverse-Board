package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestBatchValidationUsesContainedProcessTree(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.invalid/batchvalidation\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "validation_test.go"), []byte(`package batchvalidation

import "testing"

func TestContained(t *testing.T) {}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RunBatchValidation(t.Context(), root, strings.Repeat("a", 40),
		domain.BatchDeliveryValidationRequirement{ID: "go-contained",
			Kind: domain.BatchValidationGoTest, Scope: "."})
	if err != nil {
		// Obtain bounded output from the same tree primitive for a useful test
		// failure without changing the product error contract.
		goPath, lookupErr := exec.LookPath("go")
		if lookupErr == nil {
			goPath, lookupErr = canonicalBatchValidationExecutable(root, goPath)
		}
		if lookupErr == nil {
			started, startErr := runner.NewPlatformOnceProcessStarter().Start(t.Context(),
				runner.OnceStartSpec{RequestFingerprint: strings.Repeat("b", 64),
					ExecutablePath: goPath, Argv: []string{"test", "./..."},
					WorkingDirectory: root,
					Environment:      batchValidationEnvironment(filepath.Join(t.TempDir(), "cache"))})
			t.Fatalf("RunBatchValidation: %v; diagnostic start=%v exit=%d stdout=%q stderr=%q",
				err, startErr, started.ExitCode, started.Stdout.CapturedPrefix,
				started.Stderr.CapturedPrefix)
		}
		t.Fatal(err)
	}
	if result.ExitCode != 0 || len(result.OutputSHA256) != 64 ||
		result.CompletedAt.IsZero() {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}

func TestBatchValidationFailurePersistsOnlyOutputDigest(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.invalid/batchvalidationfailure\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const canary = "raw-child-output-must-not-cross-the-receipt-boundary"
	if err := os.WriteFile(filepath.Join(root, "validation_test.go"), []byte(`package batchvalidationfailure

import "testing"

func TestFailure(t *testing.T) { t.Fatal("`+canary+`") }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RunBatchValidation(t.Context(), root, strings.Repeat("a", 40),
		domain.BatchDeliveryValidationRequirement{ID: "go-failure-digest",
			Kind: domain.BatchValidationGoTest, Scope: "."})
	if err == nil {
		t.Fatal("failing validation reported success")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("raw validation output escaped in error: %v", err)
	}
	if len(result.OutputSHA256) != 64 || !strings.Contains(err.Error(), result.OutputSHA256) {
		t.Fatalf("digest evidence missing: result=%#v err=%v", result, err)
	}
}

func TestBatchValidationRejectsLinkedWorkingScope(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked-scope")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("directory symlink requires host privileges: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := canonicalBatchValidationWorkingRoot(root, "linked-scope"); err == nil {
		t.Fatal("linked validation scope was accepted")
	}
}

func TestHardenedGitEnvironmentDisablesHooksAndAttributeDrivers(t *testing.T) {
	values := make(map[string]string)
	for _, entry := range hardenedGitEnvironment() {
		key, value, found := strings.Cut(entry, "=")
		if found {
			values[key] = value
		}
	}
	if values["GIT_ATTR_NOSYSTEM"] != "1" || values["GIT_CONFIG_COUNT"] != "8" ||
		values["GIT_CONFIG_KEY_0"] != "core.hooksPath" ||
		values["GIT_CONFIG_VALUE_0"] != os.DevNull ||
		values["GIT_CONFIG_KEY_6"] != "core.attributesFile" ||
		values["GIT_CONFIG_VALUE_6"] != os.DevNull ||
		values["GIT_CONFIG_KEY_7"] != "commit.gpgSign" ||
		values["GIT_CONFIG_VALUE_7"] != "false" {
		t.Fatalf("hardened Git environment is incomplete: %#v", values)
	}
}

func TestCreateWorktreeRejectsRepositoryLocalExecutableDriver(t *testing.T) {
	root := newMutationRepo(t)
	base := fixtureGit(t, "-C", root, "rev-parse", "HEAD")
	fixtureGit(t, "-C", root, "config", "filter.batchattack.clean", "malicious-driver")
	destination := filepath.Join(t.TempDir(), "child")
	_, err := CreateWorktree(t.Context(), root, destination,
		"codex/test-local-driver", base)
	if err == nil || !strings.Contains(err.Error(), "executable Git drivers") {
		t.Fatalf("repository-local executable driver was not rejected: %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("rejected worktree destination was materialized: %v", statErr)
	}
}

func TestVerifyBatchMergeCommitRejectsExtraDescendant(t *testing.T) {
	root := newMutationRepo(t)
	base := fixtureGit(t, "-C", root, "rev-parse", "HEAD")
	parent := t.TempDir()
	childRoot := filepath.Join(parent, "child")
	childBranch := "codex/test-exact-child"
	if _, err := CreateWorktree(t.Context(), root, childRoot, childBranch, base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childRoot, "child.txt"), []byte("child\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, "-C", childRoot, "add", "child.txt")
	fixtureGit(t, "-C", childRoot, "commit", "--quiet", "-m", "child change")
	childHead := fixtureGit(t, "-C", childRoot, "rev-parse", "HEAD")

	integrationRoot := filepath.Join(parent, "integration")
	integrationBranch := "codex/test-exact-integration"
	if _, err := CreateWorktree(t.Context(), root, integrationRoot,
		integrationBranch, base); err != nil {
		t.Fatal(err)
	}
	mergedHead, conflicted, err := MergeBatchDeliveryStep(t.Context(), integrationRoot,
		integrationBranch, base, childHead, 1)
	if err != nil || conflicted {
		t.Fatalf("merge head=%s conflicted=%t err=%v", mergedHead, conflicted, err)
	}
	if err := VerifyBatchMergeCommit(t.Context(), integrationRoot, integrationBranch,
		base, childHead, mergedHead, 1); err != nil {
		t.Fatalf("exact merge was rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(integrationRoot, "extra.txt"),
		[]byte("unreviewed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, "-C", integrationRoot, "add", "extra.txt")
	fixtureGit(t, "-C", integrationRoot, "commit", "--quiet", "-m", "extra descendant")
	extraHead := fixtureGit(t, "-C", integrationRoot, "rev-parse", "HEAD")
	if err := VerifyBatchMergeCommit(t.Context(), integrationRoot, integrationBranch,
		base, childHead, extraHead, 1); err == nil {
		t.Fatal("arbitrary clean descendant was accepted as recovered merge evidence")
	}
}
