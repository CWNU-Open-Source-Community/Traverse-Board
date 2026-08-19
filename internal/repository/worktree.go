package repository

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ValidateBranchName exposes the same closed branch-name contract used by
// typed Git mutations so Workspace forks cannot smuggle arbitrary argv.
func ValidateBranchName(branch string) error {
	return validateBranchName(strings.TrimSpace(branch))
}

// NormalizeWorktreeDestination returns one absolute, symlink-resolved
// destination identity whether the final directory exists yet or not. Callers
// can persist it before materialization and later use the same identity for
// crash recovery.
func NormalizeWorktreeDestination(sourceRoot, destinationRoot string) (string, error) {
	source, err := canonicalExistingRepositoryRoot(sourceRoot)
	if err != nil {
		return "", err
	}
	destination, err := canonicalWorktreeRoot(destinationRoot)
	if err != nil {
		return "", err
	}
	if pathsOverlap(source, destination) {
		return "", errors.New("git worktree destination overlaps the source repository")
	}
	return destination, nil
}

// CreateWorktree creates one new branch-backed Git worktree at an exact full
// commit. The destination must not exist and must be outside the source root.
func CreateWorktree(ctx context.Context, sourceRoot, destinationRoot, branch,
	commit string,
) (string, error) {
	if ctx == nil || ctx.Err() != nil {
		return "", errors.New("git worktree context is unavailable")
	}
	branch, commit = strings.TrimSpace(branch), strings.TrimSpace(commit)
	if err := ValidateBranchName(branch); err != nil || !validWorktreeCommit(commit) {
		return "", errors.New("git worktree branch or commit is invalid")
	}
	source, err := canonicalExistingRepositoryRoot(sourceRoot)
	if err != nil {
		return "", err
	}
	destination, err := canonicalAbsentWorktreeRoot(destinationRoot)
	if err != nil {
		return "", err
	}
	if pathsOverlap(source, destination) {
		return "", errors.New("git worktree destination overlaps the source repository")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("git binary is unavailable: %w", err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, MaxGitDuration)
	defer cancel()
	command := exec.CommandContext(commandCtx, gitPath, "-C", source,
		"--no-optional-locks", "worktree", "add", "--no-track", "-b", branch,
		destination, commit)
	command.Env = hardenedGitEnvironment()
	command.Dir = source
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("create Git worktree: %w: %s", err,
			strings.TrimSpace(stderr.String()))
	}
	if err := verifyCreatedWorktree(commandCtx, gitPath, destination, branch, commit); err != nil {
		cleanupErr := RemoveCreatedWorktree(context.WithoutCancel(ctx), source,
			destination, branch)
		return "", errors.Join(err, cleanupErr)
	}
	return destination, nil
}

// RemoveCreatedWorktree removes only an exact registered worktree and its
// newly-created branch. It never falls back to recursive filesystem deletion.
func RemoveCreatedWorktree(ctx context.Context, sourceRoot, destinationRoot,
	branch string,
) error {
	if ctx == nil {
		return errors.New("git worktree cleanup context is unavailable")
	}
	branch = strings.TrimSpace(branch)
	if err := ValidateBranchName(branch); err != nil {
		return err
	}
	source, err := canonicalExistingRepositoryRoot(sourceRoot)
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(strings.TrimSpace(destinationRoot))
	if err != nil || pathsOverlap(source, destination) {
		return errors.New("git worktree cleanup destination is invalid")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	commandCtx, cancel := context.WithTimeout(ctx, MaxGitDuration)
	defer cancel()
	remove := exec.CommandContext(commandCtx, gitPath, "-C", source,
		"--no-optional-locks", "worktree", "remove", "--force", destination)
	remove.Env, remove.Dir = hardenedGitEnvironment(), source
	var removeOutput boundedBuffer
	remove.Stderr = &removeOutput
	if err := remove.Run(); err != nil {
		return fmt.Errorf("remove Git worktree: %w: %s", err,
			strings.TrimSpace(removeOutput.String()))
	}
	deleteBranch := exec.CommandContext(commandCtx, gitPath, "-C", source,
		"--no-optional-locks", "branch", "-D", "--", branch)
	deleteBranch.Env, deleteBranch.Dir = hardenedGitEnvironment(), source
	var branchOutput boundedBuffer
	deleteBranch.Stderr = &branchOutput
	if err := deleteBranch.Run(); err != nil {
		return fmt.Errorf("remove Git worktree branch: %w: %s", err,
			strings.TrimSpace(branchOutput.String()))
	}
	return nil
}

// CleanupInterruptedWorktree removes only the exact branch-backed worktree
// recorded before a fork was materialized. Every observable identity is
// checked first; unexpected filesystem, branch, or commit drift fails closed.
func CleanupInterruptedWorktree(ctx context.Context, sourceRoot, destinationRoot,
	branch, expectedCommit string,
) error {
	if ctx == nil || ctx.Err() != nil {
		return errors.New("git worktree recovery context is unavailable")
	}
	branch, expectedCommit = strings.TrimSpace(branch), strings.TrimSpace(expectedCommit)
	if err := ValidateBranchName(branch); err != nil || !validWorktreeCommit(expectedCommit) {
		return errors.New("git worktree recovery identity is invalid")
	}
	source, err := canonicalExistingRepositoryRoot(sourceRoot)
	if err != nil {
		return err
	}
	requested, err := filepath.Abs(strings.TrimSpace(destinationRoot))
	if err != nil || strings.ContainsRune(destinationRoot, 0) {
		return errors.New("git worktree recovery destination is invalid")
	}
	destination, err := canonicalWorktreeRoot(destinationRoot)
	if err != nil {
		return err
	}
	if !sameFilesystemPath(requested, destination) || pathsOverlap(source, destination) {
		return errors.New("git worktree recovery destination identity changed")
	}
	info, statErr := os.Lstat(destination)
	if statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("git worktree recovery destination is not a real directory")
		}
		gitPath, lookupErr := exec.LookPath("git")
		if lookupErr != nil {
			return fmt.Errorf("git binary is unavailable: %w", lookupErr)
		}
		verifyCtx, cancel := context.WithTimeout(ctx, MaxGitDuration)
		verifyErr := verifyCreatedWorktree(verifyCtx, gitPath, destination, branch,
			expectedCommit)
		cancel()
		if verifyErr != nil {
			return verifyErr
		}
		return RemoveCreatedWorktree(ctx, source, destination, branch)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect Git worktree recovery destination: %w", statErr)
	}
	return removeInterruptedWorktreeBranch(ctx, source, branch, expectedCommit)
}

func canonicalExistingRepositoryRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	abs, err := filepath.Abs(root)
	if err != nil || root == "" || strings.ContainsRune(root, 0) {
		return "", errors.New("repository root is invalid")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, ".git")); err != nil {
		return "", errors.New("repository root does not contain Git metadata")
	}
	return filepath.Clean(resolved), nil
}

func canonicalAbsentWorktreeRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" || strings.ContainsRune(root, 0) {
		return "", errors.New("git worktree destination is invalid")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("git worktree destination already exists or is inaccessible")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("resolve Git worktree parent: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", errors.New("git worktree parent is not a directory")
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func canonicalWorktreeRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" || strings.ContainsRune(root, 0) {
		return "", errors.New("git worktree destination is invalid")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("git worktree destination is not a real directory")
		}
		resolved, resolveErr := filepath.EvalSymlinks(abs)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve Git worktree destination: %w", resolveErr)
		}
		return filepath.Clean(resolved), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect Git worktree destination: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("resolve Git worktree parent: %w", err)
	}
	info, err = os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", errors.New("git worktree parent is not a directory")
	}
	return filepath.Join(filepath.Clean(parent), filepath.Base(abs)), nil
}

func sameFilesystemPath(left, right string) bool {
	relative, err := filepath.Rel(filepath.Clean(left), filepath.Clean(right))
	return err == nil && relative == "."
}

func removeInterruptedWorktreeBranch(ctx context.Context, source, branch,
	expectedCommit string,
) error {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git binary is unavailable: %w", err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, MaxGitDuration)
	defer cancel()
	ref := "refs/heads/" + branch
	show := exec.CommandContext(commandCtx, gitPath, "-C", source,
		"--no-optional-locks", "show-ref", "--verify", "--hash", ref)
	show.Env, show.Dir = hardenedGitEnvironment(), source
	var stdout, stderr boundedBuffer
	show.Stdout, show.Stderr = &stdout, &stderr
	if err := show.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 &&
			strings.TrimSpace(stdout.String()) == "" {
			return nil
		}
		return fmt.Errorf("inspect interrupted Git worktree branch: %w: %s", err,
			strings.TrimSpace(stderr.String()))
	}
	if observed := strings.TrimSpace(stdout.String()); observed != expectedCommit {
		return fmt.Errorf("interrupted Git worktree branch drifted: got %q, want %q",
			observed, expectedCommit)
	}
	remove := exec.CommandContext(commandCtx, gitPath, "-C", source,
		"--no-optional-locks", "branch", "-D", "--", branch)
	remove.Env, remove.Dir = hardenedGitEnvironment(), source
	var removeOutput boundedBuffer
	remove.Stderr = &removeOutput
	if err := remove.Run(); err != nil {
		return fmt.Errorf("remove interrupted Git worktree branch: %w: %s", err,
			strings.TrimSpace(removeOutput.String()))
	}
	return nil
}

func pathsOverlap(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative,
			".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func validWorktreeCommit(value string) bool {
	if (len(value) != 40 && len(value) != 64) || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == len(value)
}

func verifyCreatedWorktree(ctx context.Context, gitPath, root, branch, commit string) error {
	for _, check := range []struct {
		args []string
		want string
	}{
		{args: []string{"rev-parse", "--verify", "HEAD"}, want: commit},
		{args: []string{"branch", "--show-current"}, want: branch},
	} {
		command := exec.CommandContext(ctx, gitPath, append([]string{"-C", root,
			"--no-optional-locks"}, check.args...)...)
		command.Env, command.Dir = hardenedGitEnvironment(), root
		var stdout, stderr boundedBuffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("verify Git worktree %s: %w: %s", check.args[0], err,
				strings.TrimSpace(stderr.String()))
		}
		if got := strings.TrimSpace(stdout.String()); got != check.want {
			return fmt.Errorf("verify Git worktree %s: got %q, want %q",
				check.args[0], got, check.want)
		}
	}
	return nil
}
