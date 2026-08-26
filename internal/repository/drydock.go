package repository

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/workspaceidentity"
)

// DrydockExecutor composes the closed advanced-Git Worktree templates with
// source/root identity checks and review-only delivery rendering. It never
// accepts a destination path, raw Git argv, or a force-removal option.
type DrydockExecutor struct {
	advanced *AdvancedExecutor
}

type DrydockSourceObservation struct {
	Identity drydock.SourceIdentity
	State    drydock.SourceState
	Binding  gitadvanced.RepositoryBinding
}

type DrydockCreatePlan struct {
	Name    string
	Branch  string
	Path    string
	Preview gitadvanced.Preview
}

type DrydockObservation struct {
	Path            string
	Found           bool
	Present         bool
	Prunable        bool
	Locked          bool
	Clean           bool
	Head            string
	Branch          string
	RootFingerprint string
	Binding         gitadvanced.RepositoryBinding
}

type DrydockDeliveryEvidence struct {
	Binding         gitadvanced.RepositoryBinding
	HeadCommit      string
	MergeBaseCommit string
	Patch           string
	DiffStat        string
	ChangedPaths    []string
	PathStates      []DrydockDeliveryPathState
}

// DrydockDeliveryPathState is an exact, read-only projection of the Git
// locations contributing to the combined delivery Diff. Paths remain private
// application data until a public projector applies repository.PublicPath.
type DrydockDeliveryPathState struct {
	Path            string
	Tracked         bool
	Committed       bool
	IndexChanged    bool
	WorktreeChanged bool
	Untracked       bool
	Conflicted      bool
}

func NewDrydockExecutor(managedRoot string) (*DrydockExecutor, error) {
	advanced, err := NewAdvancedExecutor(managedRoot, true)
	if err != nil {
		return nil, err
	}
	return &DrydockExecutor{advanced: advanced}, nil
}

func (e *DrydockExecutor) Available() bool {
	return e != nil && e.advanced != nil && e.advanced.Available()
}

func (e *DrydockExecutor) ManagedRoot() string {
	if !e.Available() {
		return ""
	}
	return e.advanced.ManagedRoot()
}

func (e *DrydockExecutor) InspectSource(ctx context.Context, workspaceID,
	root string,
) (DrydockSourceObservation, error) {
	if !e.Available() || strings.TrimSpace(workspaceID) == "" {
		return DrydockSourceObservation{}, errors.New("Drydock source inspection is unavailable")
	}
	canonical, err := canonicalExistingRepositoryRoot(root)
	if err != nil {
		return DrydockSourceObservation{}, err
	}
	if err := e.requireDisjointSourceRoot(canonical); err != nil {
		return DrydockSourceObservation{}, err
	}
	binding, err := e.advanced.CaptureAdvancedBinding(ctx, canonical)
	if err != nil {
		return DrydockSourceObservation{}, err
	}
	if err := e.requireDisjointGitCommonDir(ctx, canonical); err != nil {
		return DrydockSourceObservation{}, err
	}
	if binding.Detached || binding.Branch == "" || binding.Head == "unborn" {
		return DrydockSourceObservation{}, errors.New("Drydock source requires an attached branch and exact base commit")
	}
	sequence, err := e.advanced.InspectAdvancedSequence(ctx, canonical)
	if err != nil {
		return DrydockSourceObservation{}, err
	}
	if sequence.Active || sequence.Conflict.Active {
		return DrydockSourceObservation{}, errors.New("Drydock source has an active Git sequence or unresolved conflict")
	}
	rootFingerprint, err := workspaceidentity.Fingerprint(canonical)
	if err != nil {
		return DrydockSourceObservation{}, err
	}
	status, stderr, code, err := e.advanced.git(ctx, canonical, nil, "status",
		"--porcelain=v2", "-z", "--untracked-files=all", "--ignored=matching")
	if err != nil || code != 0 {
		return DrydockSourceObservation{}, fmt.Errorf("inspect Drydock source status: %w: %s",
			err, strings.TrimSpace(stderr))
	}
	dirtyTracked, dirtyUntracked, dirtyIgnored := classifyDrydockStatus(status)
	symlinks, submodules, err := e.drydockSpecialEntries(ctx, canonical)
	if err != nil {
		return DrydockSourceObservation{}, err
	}
	pathDigest := drydock.FingerprintBytes([]byte(filepath.ToSlash(canonical)))
	return DrydockSourceObservation{
		Identity: drydock.SourceIdentity{WorkspaceID: strings.TrimSpace(workspaceID),
			RootPath: canonical, RootPathSHA256: pathDigest,
			RootFingerprint: rootFingerprint, RepositorySHA256: binding.RepositorySHA256,
			CommonDirSHA256: binding.CommonDirSHA256, Branch: binding.Branch,
			BaseCommit: binding.Head, ObjectFormat: binding.ObjectFormat},
		State: drydock.SourceState{IndexSHA256: binding.IndexSHA256,
			WorktreeSHA256: binding.WorktreeSHA256,
			StatusSHA256:   drydock.FingerprintBytes([]byte(status)),
			DirtyTracked:   dirtyTracked, DirtyUntracked: dirtyUntracked,
			DirtyIgnored: dirtyIgnored, SymlinkEntries: symlinks,
			SubmoduleEntries: submodules, CapturedAt: time.Now().UTC()},
		Binding: binding,
	}, nil
}

func (e *DrydockExecutor) PlanCreate(ctx context.Context, root, name, branch,
	baseCommit string,
) (DrydockCreatePlan, error) {
	if !e.Available() {
		return DrydockCreatePlan{}, errors.New("Drydock create capability is unavailable")
	}
	canonical, err := canonicalExistingRepositoryRoot(root)
	if err != nil {
		return DrydockCreatePlan{}, err
	}
	if err := e.requireDisjointSourceRoot(canonical); err != nil {
		return DrydockCreatePlan{}, err
	}
	if err := e.requireDisjointGitCommonDir(ctx, canonical); err != nil {
		return DrydockCreatePlan{}, err
	}
	preview, err := e.advanced.ReviewAdvanced(ctx, canonical, gitadvanced.Spec{
		ProtocolVersion: gitadvanced.ProtocolVersion,
		Operation:       gitadvanced.WorktreeCreate,
		WorktreeName:    name,
		Branch:          branch,
		Commit:          baseCommit,
	})
	if err != nil {
		return DrydockCreatePlan{}, err
	}
	path, err := e.advanced.PlannedManagedWorktreePath(preview.Binding, name)
	if err != nil {
		return DrydockCreatePlan{}, err
	}
	return DrydockCreatePlan{Name: name, Branch: branch, Path: path, Preview: preview}, nil
}

func (e *DrydockExecutor) requireDisjointSourceRoot(sourceRoot string) error {
	managedRoot := e.ManagedRoot()
	if managedRoot == "" || pathInsideRoot(sourceRoot, managedRoot) ||
		pathInsideRoot(managedRoot, sourceRoot) {
		return errors.New("Drydock managed root and source Workspace must be disjoint")
	}
	return nil
}

func (e *DrydockExecutor) requireDisjointGitCommonDir(ctx context.Context,
	sourceRoot string,
) error {
	commonDir, err := e.advanced.requiredPathOutput(ctx, sourceRoot, "rev-parse",
		"--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	commonDir, err = canonicalGitMetadataPath(commonDir)
	if err != nil {
		return err
	}
	managedRoot := e.ManagedRoot()
	if pathInsideRoot(commonDir, managedRoot) || pathInsideRoot(managedRoot, commonDir) {
		return errors.New("Drydock managed root and Git common directory must be disjoint")
	}
	return nil
}

func (e *DrydockExecutor) ExecuteCreate(ctx context.Context, root string,
	plan DrydockCreatePlan,
) (gitadvanced.Receipt, error) {
	if !e.Available() || plan.Preview.Operation != gitadvanced.WorktreeCreate ||
		plan.Preview.Spec.WorktreeName != plan.Name || plan.Preview.Spec.Branch != plan.Branch {
		return gitadvanced.Receipt{}, errors.New("Drydock create plan is invalid")
	}
	return e.advanced.ExecuteAdvanced(ctx, root, plan.Preview)
}

func (e *DrydockExecutor) Inspect(ctx context.Context, sourceRoot string,
	sourceBinding gitadvanced.RepositoryBinding, name string,
) (DrydockObservation, error) {
	if !e.Available() {
		return DrydockObservation{}, errors.New("Drydock inspection is unavailable")
	}
	observed, err := e.advanced.InspectManagedWorktree(ctx, sourceRoot, sourceBinding, name)
	if err != nil {
		return DrydockObservation{}, err
	}
	value := DrydockObservation{Path: observed.Path, Found: observed.Found,
		Present: observed.Present, Prunable: observed.Prunable, Locked: observed.Locked,
		Clean: observed.Clean, Head: observed.Head, Branch: observed.Branch,
		Binding: observed.Binding}
	if observed.Present {
		value.RootFingerprint, err = workspaceidentity.Fingerprint(observed.Path)
		if err != nil {
			return value, err
		}
	}
	return value, nil
}

func (e *DrydockExecutor) PlanRemove(ctx context.Context, sourceRoot string,
	name, worktreeID string,
) (gitadvanced.Preview, error) {
	if !e.Available() {
		return gitadvanced.Preview{}, errors.New("Drydock cleanup capability is unavailable")
	}
	return e.advanced.ReviewAdvanced(ctx, sourceRoot, gitadvanced.Spec{
		ProtocolVersion: gitadvanced.ProtocolVersion,
		Operation:       gitadvanced.WorktreeRemove,
		WorktreeName:    name,
		WorktreeID:      worktreeID,
	})
}

func (e *DrydockExecutor) ExecuteRemove(ctx context.Context, sourceRoot string,
	preview gitadvanced.Preview,
) (gitadvanced.Receipt, error) {
	if !e.Available() || preview.Operation != gitadvanced.WorktreeRemove {
		return gitadvanced.Receipt{}, errors.New("Drydock cleanup preview is invalid")
	}
	return e.advanced.ExecuteAdvanced(ctx, sourceRoot, preview)
}

func (e *DrydockExecutor) VerifyBaseAncestry(ctx context.Context, root,
	baseCommit, headCommit string,
) error {
	if !e.Available() || !gitadvanced.ValidObjectID(baseCommit) ||
		!gitadvanced.ValidObjectID(headCommit) {
		return errors.New("Drydock ancestry identity is invalid")
	}
	mergeBase, err := e.drydockGit(ctx, root, nil, "merge-base", baseCommit, headCommit)
	if err != nil || strings.TrimSpace(mergeBase) != baseCommit {
		return errors.New("Drydock branch no longer descends from its exact base commit")
	}
	return nil
}

// CaptureDelivery renders a combined worktree/index/untracked diff through a
// temporary Git index. The live index and source Workspace are never mutated.
func (e *DrydockExecutor) CaptureDelivery(ctx context.Context, root,
	baseCommit string,
) (DrydockDeliveryEvidence, error) {
	if !e.Available() || !gitadvanced.ValidObjectID(baseCommit) {
		return DrydockDeliveryEvidence{}, errors.New("Drydock delivery request is invalid")
	}
	before, err := e.advanced.CaptureAdvancedBinding(ctx, root)
	if err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	if before.Head == "unborn" || before.Detached || before.Branch == "" {
		return DrydockDeliveryEvidence{}, errors.New("Drydock delivery requires an attached branch")
	}
	mergeBase, err := e.drydockGit(ctx, root, nil, "merge-base", baseCommit, before.Head)
	if err != nil || strings.TrimSpace(mergeBase) != baseCommit {
		return DrydockDeliveryEvidence{}, errors.New("Drydock branch no longer descends from its exact base commit")
	}
	temporary, err := os.MkdirTemp(e.advanced.ManagedRoot(), ".drydock-review-")
	if err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	temporaryFingerprint, err := workspaceidentity.Fingerprint(temporary)
	if err != nil {
		// The invocation just created an empty directory. A non-recursive remove
		// cannot delete later or unattributed descendants.
		_ = os.Remove(temporary)
		return DrydockDeliveryEvidence{}, err
	}
	defer cleanupDrydockReviewTemporary(temporary, temporaryFingerprint)
	indexPath := filepath.Join(temporary, "index")
	extraEnv := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err := e.drydockGit(ctx, root, extraEnv, "read-tree", baseCommit); err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	if _, err := e.drydockGit(ctx, root, extraEnv, "add", "-A", "--", "."); err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	patch, err := e.drydockGit(ctx, root, extraEnv, "diff", "--cached", "--binary",
		"--full-index", "--no-ext-diff", "--no-renames", baseCommit, "--")
	if err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	if len([]byte(patch)) > drydock.MaxPatchBytes {
		return DrydockDeliveryEvidence{}, errors.New("Drydock delivery diff exceeds its byte bound")
	}
	diffStat, err := e.drydockGit(ctx, root, extraEnv, "diff", "--cached",
		"--stat=120,80", "--no-renames", baseCommit, "--")
	if err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	pathOutput, err := e.drydockGit(ctx, root, extraEnv, "diff", "--cached",
		"--name-only", "-z", "--no-renames", baseCommit, "--")
	if err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	paths := splitDrydockPaths(pathOutput)
	if len(paths) > drydock.MaxChangedPaths {
		return DrydockDeliveryEvidence{}, errors.New("Drydock delivery exceeds its changed-path bound")
	}
	pathStates, err := e.captureDeliveryPathStates(ctx, root, baseCommit, before.Head, paths)
	if err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	after, err := e.advanced.CaptureAdvancedBinding(ctx, root)
	if err != nil {
		return DrydockDeliveryEvidence{}, err
	}
	if !before.SameState(after) {
		return DrydockDeliveryEvidence{}, errors.New("Drydock changed while delivery evidence was rendered")
	}
	return DrydockDeliveryEvidence{Binding: after, HeadCommit: after.Head,
		MergeBaseCommit: baseCommit, Patch: patch, DiffStat: strings.TrimSpace(diffStat),
		ChangedPaths: paths, PathStates: pathStates}, nil
}

func (e *DrydockExecutor) captureDeliveryPathStates(ctx context.Context, root,
	baseCommit, headCommit string, combined []string,
) ([]DrydockDeliveryPathState, error) {
	type pathSet map[string]struct{}
	read := func(args ...string) (pathSet, error) {
		value, err := e.drydockGit(ctx, root, nil, args...)
		if err != nil {
			return nil, err
		}
		result := make(pathSet)
		for _, current := range splitDrydockPaths(value) {
			result[current] = struct{}{}
		}
		return result, nil
	}
	committed, err := read("diff", "--name-only", "-z", "--no-renames",
		baseCommit, headCommit, "--")
	if err != nil {
		return nil, err
	}
	index, err := read("diff", "--cached", "--name-only", "-z", "--no-renames",
		headCommit, "--")
	if err != nil {
		return nil, err
	}
	worktree, err := read("diff", "--name-only", "-z", "--no-renames", "--")
	if err != nil {
		return nil, err
	}
	untracked, err := read("ls-files", "--others", "--exclude-standard", "-z", "--")
	if err != nil {
		return nil, err
	}
	conflicted, err := read("diff", "--name-only", "-z", "--diff-filter=U", "--")
	if err != nil {
		return nil, err
	}
	states := make([]DrydockDeliveryPathState, 0, len(combined))
	for _, current := range combined {
		_, isCommitted := committed[current]
		_, isIndex := index[current]
		_, isWorktree := worktree[current]
		_, isUntracked := untracked[current]
		_, isConflict := conflicted[current]
		states = append(states, DrydockDeliveryPathState{Path: current,
			Tracked:   isCommitted || isIndex || isWorktree || isConflict,
			Committed: isCommitted, IndexChanged: isIndex,
			WorktreeChanged: isWorktree, Untracked: isUntracked,
			Conflicted: isConflict})
	}
	return states, nil
}

// cleanupDrydockReviewTemporary never recurses. If the directory identity
// changes, a link/reparse point appears, or any unexpected entry is present,
// the complete directory is preserved for operator inspection.
func cleanupDrydockReviewTemporary(root, expectedFingerprint string) {
	observedFingerprint, err := workspaceidentity.Fingerprint(root)
	if err != nil || observedFingerprint != expectedFingerprint {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() != "index" && entry.Name() != "index.lock" {
			return
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || isReparsePoint(info) {
			return
		}
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
			return
		}
	}
	_ = os.Remove(root)
}

func (e *DrydockExecutor) drydockSpecialEntries(ctx context.Context,
	root string,
) (int, int, error) {
	index, stderr, code, err := e.advanced.git(ctx, root, nil, "ls-files", "--stage", "-z")
	if err != nil || code != 0 {
		return 0, 0, fmt.Errorf("inspect Drydock source index modes: %w: %s", err,
			strings.TrimSpace(stderr))
	}
	symlinks, submodules := 0, 0
	for _, value := range strings.Split(index, "\x00") {
		if strings.HasPrefix(value, "120000 ") {
			symlinks++
		}
		if strings.HasPrefix(value, "160000 ") {
			submodules++
		}
	}
	visited := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if strings.Split(relative, string(filepath.Separator))[0] == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > workspaceCheckpointWalkBound {
			return errors.New("Drydock source exceeds its filesystem inspection bound")
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info) {
			symlinks++
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return symlinks, submodules, err
}

const workspaceCheckpointWalkBound = 50_000

func classifyDrydockStatus(status string) (tracked, untracked, ignored bool) {
	for _, value := range strings.Split(status, "\x00") {
		switch {
		case strings.HasPrefix(value, "1 "), strings.HasPrefix(value, "2 "),
			strings.HasPrefix(value, "u "):
			tracked = true
		case strings.HasPrefix(value, "? "):
			untracked = true
		case strings.HasPrefix(value, "! "):
			ignored = true
		}
	}
	return tracked, untracked, ignored
}

func splitDrydockPaths(value string) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	for _, current := range strings.Split(value, "\x00") {
		// A Git path is literal data. Do not trim a leading or trailing space into
		// a different identity; the domain validator will reject unsupported names.
		current = filepath.ToSlash(current)
		if current == "" {
			continue
		}
		if _, exists := seen[current]; exists {
			continue
		}
		seen[current] = struct{}{}
		paths = append(paths, current)
	}
	sort.Strings(paths)
	return paths
}

func (e *DrydockExecutor) drydockGit(ctx context.Context, root string,
	extraEnv []string, args ...string,
) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, MaxAdvancedGitDuration)
	defer cancel()
	base := []string{"-C", root, "--no-optional-locks", "-c", "core.autocrlf=false",
		"-c", "submodule.recurse=false", "--literal-pathspecs"}
	command := exec.CommandContext(commandCtx, e.advanced.gitPath, append(base, args...)...)
	command.Dir = root
	command.Env = append(hardenedGitEnvironment(), extraEnv...)
	stdout := advancedBoundedBuffer{max: drydock.MaxPatchBytes + 1}
	stderr := advancedBoundedBuffer{max: MaxGitOutputBytes}
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if commandCtx.Err() != nil {
		return "", commandCtx.Err()
	}
	if stdout.truncated || stderr.truncated {
		return "", errors.New("Drydock Git output exceeded its protocol bound")
	}
	if err != nil {
		return "", fmt.Errorf("Drydock Git %s failed: %w: %s", args[0], err,
			strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
