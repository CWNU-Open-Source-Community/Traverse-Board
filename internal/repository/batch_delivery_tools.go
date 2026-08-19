package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const MaxBatchWorkspaceDiffPreviewBytes int64 = 256 * 1024

type BatchWorkspaceChanges struct {
	Branch       string
	HeadCommit   string
	ChangedFiles []string
	DeletedFiles []string
	Diff         string
	DiffSHA256   string
	DiffBytes    int64
	Clean        bool
}

// InspectBatchWorkspaceChanges provides the narrowed child with a bounded
// working-tree status/diff. It never reads config, remotes, credentials, or
// paths outside the exact worktree.
func InspectBatchWorkspaceChanges(ctx context.Context, root, expectedBranch,
	baseCommit string, maxChangedFiles int, maxDiffBytes int64,
) (BatchWorkspaceChanges, error) {
	var result BatchWorkspaceChanges
	if ctx == nil || ctx.Err() != nil || ValidateBranchName(expectedBranch) != nil ||
		!validWorktreeCommit(baseCommit) || maxChangedFiles <= 0 ||
		maxDiffBytes <= 0 || maxDiffBytes > MaxBatchWorkspaceDiffPreviewBytes {
		return result, errors.New("batch workspace inspection identity or limits are invalid")
	}
	root, err := canonicalExistingRepositoryRoot(root)
	if err != nil {
		return result, err
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return result, err
	}
	branch, err := batchGitOutput(ctx, gitPath, root, "branch", "--show-current")
	if err != nil || strings.TrimSpace(string(branch)) != expectedBranch {
		return result, errors.New("batch workspace branch drifted")
	}
	result.Branch = expectedBranch
	head, err := batchGitOutput(ctx, gitPath, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return result, err
	}
	result.HeadCommit = strings.TrimSpace(string(head))
	if !validWorktreeCommit(result.HeadCommit) {
		return result, errors.New("batch workspace HEAD is invalid")
	}
	ancestor, err := IsAncestor(ctx, root, baseCommit, result.HeadCommit)
	if err != nil || !ancestor {
		return result, errors.New("batch workspace no longer descends from its assigned base")
	}
	nameStatus, err := batchGitOutput(ctx, gitPath, root, "diff", "--name-status", "-z",
		"--find-renames", "HEAD", "--")
	if err != nil {
		return result, err
	}
	changed, deleted, err := parseBatchWorkingChanges(nameStatus, maxChangedFiles)
	if err != nil {
		return result, err
	}
	untracked, err := batchGitOutput(ctx, gitPath, root, "ls-files", "--others", "-z",
		"--exclude-standard", "--")
	if err != nil {
		return result, err
	}
	for _, raw := range bytes.Split(untracked, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		value, pathErr := normalizeBatchGitPath(string(raw))
		if pathErr != nil {
			return result, pathErr
		}
		changed[value] = struct{}{}
		if len(changed) > maxChangedFiles {
			return result, fmt.Errorf("batch workspace changed files exceed %d", maxChangedFiles)
		}
	}
	for value := range changed {
		result.ChangedFiles = append(result.ChangedFiles, value)
	}
	for value := range deleted {
		result.DeletedFiles = append(result.DeletedFiles, value)
	}
	sort.Strings(result.ChangedFiles)
	sort.Strings(result.DeletedFiles)
	status, err := batchGitOutput(ctx, gitPath, root, "status", "--porcelain=v1", "-z",
		"--untracked-files=all")
	if err != nil {
		return result, err
	}
	result.Clean = len(status) == 0
	diff, err := batchGitOutputLimit(ctx, gitPath, root, maxDiffBytes, "diff",
		"--full-index", "--no-ext-diff", "--no-color", "HEAD", "--")
	if err != nil {
		return result, err
	}
	result.Diff = strings.ToValidUTF8(string(diff), "?")
	result.DiffBytes = int64(len(diff))
	digest := sha256.Sum256(diff)
	result.DiffSHA256 = hex.EncodeToString(digest[:])
	return result, nil
}

func parseBatchWorkingChanges(raw []byte, limit int) (map[string]struct{},
	map[string]struct{}, error,
) {
	changed := make(map[string]struct{}, limit)
	deleted := make(map[string]struct{})
	parts := bytes.Split(raw, []byte{0})
	for index := 0; index < len(parts); {
		if len(parts[index]) == 0 {
			index++
			continue
		}
		status := string(parts[index])
		index++
		pathCount := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			pathCount = 2
		}
		for pathIndex := 0; pathIndex < pathCount; pathIndex++ {
			if index >= len(parts) || len(parts[index]) == 0 {
				return nil, nil, errors.New("batch workspace changed-file stream is malformed")
			}
			value, err := normalizeBatchGitPath(string(parts[index]))
			index++
			if err != nil {
				return nil, nil, err
			}
			changed[value] = struct{}{}
			if strings.HasPrefix(status, "D") ||
				(strings.HasPrefix(status, "R") && pathIndex == 0) {
				deleted[value] = struct{}{}
			}
			if len(changed) > limit {
				return nil, nil, fmt.Errorf("batch workspace changed files exceed %d", limit)
			}
		}
	}
	return changed, deleted, nil
}

func normalizeBatchGitPath(value string) (string, error) {
	value = filepath.ToSlash(value)
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
		filepath.IsAbs(value) || value == ".." || strings.HasPrefix(value, "../") {
		return "", errors.New("batch workspace changed path escapes the repository")
	}
	return value, nil
}

func CommitBatchWorkspace(ctx context.Context, root, expectedBranch, baseCommit,
	message string, maxChangedFiles int, allowPath func(string) bool,
) (string, BatchWorkspaceChanges, error) {
	var empty BatchWorkspaceChanges
	message = strings.TrimSpace(message)
	if allowPath == nil || message == "" || len([]rune(message)) > 256 ||
		strings.ContainsAny(message, "\r\n\x00") {
		return "", empty, errors.New("batch workspace commit request is invalid")
	}
	inspection, err := InspectBatchWorkspaceChanges(ctx, root, expectedBranch,
		baseCommit, maxChangedFiles, MaxBatchWorkspaceDiffPreviewBytes)
	if err != nil {
		return "", empty, err
	}
	if inspection.Clean || len(inspection.ChangedFiles) == 0 {
		return "", inspection, errors.New("batch workspace has no changes to commit")
	}
	if len(inspection.DeletedFiles) != 0 {
		return "", inspection, errors.New("batch child profile does not allow file deletion or rename")
	}
	for _, changed := range inspection.ChangedFiles {
		if !allowPath(changed) {
			return "", inspection, fmt.Errorf("batch workspace changed unowned path %q", changed)
		}
	}
	root, err = canonicalExistingRepositoryRoot(root)
	if err != nil {
		return "", inspection, err
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return "", inspection, err
	}
	args := append([]string{"add", "--"}, inspection.ChangedFiles...)
	if err := runBatchGitMutation(ctx, gitPath, root, args...); err != nil {
		return "", inspection, err
	}
	rollbackIndex := func() {
		_ = runBatchGitMutation(context.WithoutCancel(ctx), gitPath, root,
			"reset", "--mixed", "HEAD", "--")
	}
	if err := runBatchGitMutation(ctx, gitPath, root, "diff", "--cached", "--check"); err != nil {
		rollbackIndex()
		return "", inspection, err
	}
	if err := runBatchGitMutation(ctx, gitPath, root,
		"-c", "user.name=CyberAgent Delivery Child",
		"-c", "user.email=delivery-child@cyberagent.invalid",
		"commit", "--no-gpg-sign", "-m", message); err != nil {
		rollbackIndex()
		return "", inspection, err
	}
	head, err := CurrentFullHead(ctx, root)
	if err != nil || head == inspection.HeadCommit {
		return "", inspection, errors.New("batch workspace commit did not advance HEAD")
	}
	status, err := batchGitOutput(ctx, gitPath, root, "status", "--porcelain=v1", "-z",
		"--untracked-files=all")
	if err != nil || len(status) != 0 {
		return head, inspection, errors.New("batch workspace changed concurrently during commit")
	}
	return head, inspection, nil
}

// VerifyBatchWorkspaceCommittedIntent proves that the current clean HEAD can
// only be the result of the narrowed commit operation that was durably
// recorded before Git was mutated. This closes the process-crash window
// between git commit and the database completion receipt.
func VerifyBatchWorkspaceCommittedIntent(ctx context.Context, root, expectedBranch,
	baseCommit, priorHead, message string, maxChangedFiles int, maxDiffBytes int64,
	allowPath func(string) bool,
) (BatchDeliveryInspection, error) {
	var empty BatchDeliveryInspection
	message = strings.TrimSpace(message)
	if allowPath == nil || !validWorktreeCommit(priorHead) || message == "" ||
		len([]rune(message)) > 256 || strings.ContainsAny(message, "\r\n\x00") {
		return empty, errors.New("batch workspace commit recovery request is invalid")
	}
	inspection, err := InspectBatchDelivery(ctx, root, expectedBranch, baseCommit,
		maxChangedFiles, maxDiffBytes)
	if err != nil {
		return empty, err
	}
	if inspection.HeadCommit == priorHead {
		return empty, errors.New("batch workspace commit intent did not advance HEAD")
	}
	for _, changed := range inspection.ChangedFiles {
		if !allowPath(changed) {
			return empty, fmt.Errorf("batch workspace committed unowned path %q", changed)
		}
	}
	root, err = canonicalExistingRepositoryRoot(root)
	if err != nil {
		return empty, err
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return empty, err
	}
	ancestry, err := batchGitOutput(ctx, gitPath, root, "rev-list", "--parents", "-n", "1",
		inspection.HeadCommit)
	if err != nil {
		return empty, err
	}
	commitAndParents := strings.Fields(string(ancestry))
	if len(commitAndParents) != 2 || commitAndParents[0] != inspection.HeadCommit ||
		commitAndParents[1] != priorHead {
		return empty, errors.New("batch workspace recovered HEAD is not the single direct commit recorded by its intent")
	}
	forbidden, err := batchGitOutput(ctx, gitPath, root, "diff", "--name-only", "-z",
		"--diff-filter=DRC", priorHead+"..."+inspection.HeadCommit, "--")
	if err != nil || len(forbidden) != 0 {
		return empty, errors.New("batch workspace recovered commit deletes, renames, or copies files")
	}
	metadata, err := batchGitOutput(ctx, gitPath, root, "show", "-s",
		"--format=%an%x00%ae%x00%B", inspection.HeadCommit)
	if err != nil {
		return empty, err
	}
	parts := bytes.SplitN(metadata, []byte{0}, 3)
	if len(parts) != 3 || string(parts[0]) != "CyberAgent Delivery Child" ||
		string(parts[1]) != "delivery-child@cyberagent.invalid" ||
		strings.TrimSpace(string(parts[2])) != message {
		return empty, errors.New("batch workspace recovered commit metadata does not match its intent")
	}
	return inspection, nil
}

type boundedBatchOutput struct {
	buffer bytes.Buffer
	limit  int64
}

func (w *boundedBatchOutput) Write(value []byte) (int, error) {
	if int64(w.buffer.Len())+int64(len(value)) > w.limit {
		return 0, fmt.Errorf("batch Git output exceeds %d bytes", w.limit)
	}
	return w.buffer.Write(value)
}

func batchGitOutputLimit(ctx context.Context, gitPath, root string, limit int64,
	args ...string,
) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, MaxGitDuration)
	defer cancel()
	command := exec.CommandContext(commandCtx, gitPath,
		append([]string{"-C", root, "--no-optional-locks"}, args...)...)
	command.Dir, command.Env = root, hardenedGitEnvironment()
	stdout := &boundedBatchOutput{limit: limit}
	var stderr boundedBuffer
	command.Stdout, command.Stderr = stdout, &stderr
	if err := command.Run(); err != nil {
		if commandCtx.Err() != nil {
			return nil, commandCtx.Err()
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err,
			strings.TrimSpace(stderr.String()))
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), nil
}
