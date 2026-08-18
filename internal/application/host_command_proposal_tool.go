package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

type HostCommandProposalMutationStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)
	GetRunExecutionInteraction(context.Context, string) (
		domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionProfile(context.Context, string) (
		domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	CreateHostCommandProposal(context.Context,
		runner.HostCommandProposalOperation, runner.HostCommandProposal,
	) (runner.HostCommandProposal, bool, error)
}

type HostCommandProposalToolExecutor struct {
	store        HostCommandProposalMutationStore
	resolveShell func(string) (string, error)
}

func NewHostCommandProposalToolExecutor(
	store HostCommandProposalMutationStore,
) *HostCommandProposalToolExecutor {
	return &HostCommandProposalToolExecutor{
		store: store, resolveShell: resolveProposalShellExecutable,
	}
}

func (e *HostCommandProposalToolExecutor) ProposeHostCommand(ctx context.Context,
	scope toolgateway.HostCommandProposalContext,
	payload toolgateway.HostCommandProposalSpec,
) (toolgateway.HostCommandProposalResult, error) {
	if e == nil || e.store == nil {
		return toolgateway.HostCommandProposalResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"host command proposal mutation store is required")
	}
	if err := scope.Validate(); err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	if err := payload.Validate(); err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	run, err := e.store.GetRun(ctx, scope.RunID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	mission, err := e.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	if mission.WorkspaceID == "" || mission.WorkspaceID != scope.WorkspaceID {
		return toolgateway.HostCommandProposalResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"host command proposal requires the current Workspace")
	}
	workspace, err := e.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	interaction, err := e.store.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	profile, err := e.store.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	permission, err := e.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	mode, err := e.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	if permission.Mode != domain.RunExecutionPermissionApproval ||
		mode.Surface != domain.ExecutionSurfaceCode ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		interaction.Surface != domain.ExecutionSurfaceCode ||
		interaction.ExecutionProfile != domain.RunExecutionProfileLocal ||
		interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted ||
		interaction.CommandForm != domain.ExecutionCommandStructuredArgv ||
		interaction.PersistentTerminal ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		interaction.ExecutionProfileRevision != profile.Revision {
		return toolgateway.HostCommandProposalResult{}, apperror.New(
			apperror.CodePolicyDenied,
			"host command proposals require a trusted Code Run in approval mode with controlled local one-shot execution")
	}
	workingDirectory, err := proposalWorkspaceDirectory(
		payload.WorkingDirectory, workspace.RootPath)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
			apperror.CodePolicyDenied, err.Error(), err)
	}
	executableRequest := payload.ExecutablePath
	argv := append([]string(nil), payload.Argv...)
	if payload.Transport == toolgateway.HostCommandTransportShell {
		if e.resolveShell == nil {
			return toolgateway.HostCommandProposalResult{}, apperror.New(
				apperror.CodeFailedPrecondition, "host shell resolver is unavailable")
		}
		executableRequest, err = e.resolveShell(payload.Shell)
		if err != nil {
			return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
				apperror.CodeUnavailable, "requested host shell is unavailable", err)
		}
		argv, err = runner.CanonicalHostShellArguments(payload.Shell, payload.Command)
		if err != nil {
			return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
				apperror.CodeInvalidArgument, "host shell command is invalid", err)
		}
	}
	executablePath, executableSHA, err := proposalExecutableIdentity(
		executableRequest, workspace.RootPath)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
			apperror.CodePolicyDenied, err.Error(), err)
	}
	environment := sanitizedHostEnvironment()
	spec, err := runner.NewHostCommandSpec(runner.HostCommandSpecRequest{
		ExecutablePath: executablePath, ExecutableSHA256: executableSHA,
		Argv: argv, WorkingDirectory: workingDirectory,
		Environment: environment, NetworkIntent: runner.HostNetworkIntentHost,
		TimeoutMilliseconds: payload.TimeoutMilliseconds,
		Purpose:             payload.Purpose,
	})
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "host command specification is invalid", err)
	}
	validateTransport := runner.ValidateHostCommandProposalTransport
	if payload.Transport == toolgateway.HostCommandTransportProcess {
		validateTransport = runner.ValidateHostCommandProcessProposalTransport
	}
	if err := validateTransport(spec); err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
			apperror.CodePolicyDenied, err.Error(), err)
	}
	operationDigest := runmutation.OperationKeyDigest(
		string(toolgateway.HostCommandProposeTool), scope.RunID,
		scope.OperationKey)
	now := time.Now().UTC()
	proposal, err := runner.NewHostCommandProposal(
		runner.HostCommandProposalRequest{
			ID:    "host-command-proposal-" + operationDigest[:24],
			RunID: run.ID, MissionID: mission.ID, SessionID: scope.SessionID,
			WorkspaceID: workspace.ID, RootAgentID: scope.RootAgentID,
			InteractionSnapshotID:    interaction.ID,
			InteractionRevision:      interaction.Revision,
			ExecutionProfileRevision: profile.Revision,
			Permission:               permission, Spec: spec,
			RequestedBy: scope.RequestedBy, CreatedAt: now,
		})
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "host command proposal is invalid", err)
	}
	operation := runner.HostCommandProposalOperation{
		KeyDigest:          operationDigest,
		RequestFingerprint: runner.HostCommandProposalRequestFingerprint(proposal),
		InvocationID:       scope.InvocationID, ProposalID: proposal.ID,
		RunID: scope.RunID, SessionID: scope.SessionID,
		WorkspaceID: scope.WorkspaceID, RootAgentID: scope.RootAgentID,
		LeaseID: scope.LeaseID, LeaseGeneration: scope.LeaseGeneration,
		RequestedBy: scope.RequestedBy, CreatedAt: now,
	}
	stored, replayed, err := e.store.CreateHostCommandProposal(
		ctx, operation, proposal)
	if err != nil {
		return toolgateway.HostCommandProposalResult{}, apperror.Normalize(err)
	}
	return toolgateway.HostCommandProposalResult{
		ProposalID: stored.ID, SpecFingerprint: stored.Spec.Fingerprint,
		Replayed: replayed,
	}, nil
}

func proposalWorkspaceDirectory(value string, workspaceRoot string) (string, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return "", errors.New("workspace root is invalid")
	}
	path, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil || !pathWithinRoot(path, root) {
		return "", errors.New("host command working directory must stay inside the Workspace")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("host command working directory must be an existing non-link directory")
	}
	return filepath.Clean(path), nil
}

func proposalExecutableIdentity(value string, workspaceRoot string) (
	string, string, error,
) {
	path, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil {
		return "", "", errors.New("host executable path is invalid")
	}
	path = filepath.Clean(path)
	if !proposalExecutableRootAllowed(path, workspaceRoot) {
		return "", "", errors.New("host executable is outside the Workspace and trusted program roots")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&os.ModeSymlink != 0 || info.Size() < 512 || info.Size() > 1<<30 {
		return "", "", errors.New("host executable must be a regular non-link file between 512 bytes and 1 GiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", errors.New("host executable cannot be opened")
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", "", errors.New("host executable cannot be hashed")
	}
	return path, hex.EncodeToString(digest.Sum(nil)), nil
}

func proposalExecutableRootAllowed(path string, workspaceRoot string) bool {
	roots := []string{workspaceRoot, os.Getenv("SystemRoot"),
		os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramW6432")}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "Programs"))
	}
	trustedGitBash := ""
	if runtime.GOOS == "windows" {
		if gitPath, err := exec.LookPath("git.exe"); err == nil {
			if gitRoot, ok := gitForWindowsDistributionRoot(gitPath); ok {
				// Git for Windows is commonly installed outside Program Files. Trust
				// only Bash itself from the exact distribution already selected by
				// PATH, not the distribution root. The executable is still hashed and
				// reviewed before any proposal can execute.
				trustedGitBash = filepath.Clean(filepath.Join(gitRoot, "bin", "bash.exe"))
			}
		}
	}
	if trustedGitBash != "" && strings.EqualFold(path, trustedGitBash) {
		return true
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err == nil && pathWithinRoot(path, absolute) {
			return true
		}
	}
	return false
}

func resolveProposalShellExecutable(shell string) (string, error) {
	shell = strings.ToLower(strings.TrimSpace(shell))
	candidates := make([]string, 0, 8)
	if runtime.GOOS != "windows" {
		return "", runner.ErrHostCommandPlatform
	}
	switch shell {
	case toolgateway.HostCommandShellPowerShell:
		if root := os.Getenv("ProgramFiles"); root != "" {
			candidates = append(candidates, filepath.Join(root, "PowerShell", "7", "pwsh.exe"))
		}
		if root := os.Getenv("SystemRoot"); root != "" {
			candidates = append(candidates, filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
		}
	case toolgateway.HostCommandShellBash:
		// Prefer Bash from the exact Git for Windows distribution already
		// selected by PATH. Fixed trusted installs remain fallback candidates.
		if gitPath, err := exec.LookPath("git.exe"); err == nil {
			if gitRoot, ok := gitForWindowsDistributionRoot(gitPath); ok {
				candidates = append(candidates,
					filepath.Join(gitRoot, "bin", "bash.exe"))
			}
		}
		roots := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			roots = append(roots, filepath.Join(local, "Programs"))
		}
		for _, root := range roots {
			if strings.TrimSpace(root) != "" {
				candidates = append(candidates, filepath.Join(root, "Git", "bin", "bash.exe"))
			}
		}
	default:
		return "", errors.New("unsupported host shell")
	}
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, err := os.Lstat(absolute)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return filepath.Clean(absolute), nil
		}
	}
	return "", errors.New("requested shell executable was not found in trusted candidates")
}

func gitForWindowsDistributionRoot(gitPath string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	absolute, err := filepath.Abs(strings.TrimSpace(gitPath))
	if err != nil {
		return "", false
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!strings.EqualFold(filepath.Base(absolute), "git.exe") {
		return "", false
	}
	programDirectory := filepath.Dir(absolute)
	switch strings.ToLower(filepath.Base(programDirectory)) {
	case "cmd", "bin":
	default:
		return "", false
	}
	root := filepath.Clean(filepath.Join(programDirectory, ".."))
	volumeRoot := filepath.Clean(filepath.VolumeName(root) + string(filepath.Separator))
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 ||
		strings.EqualFold(root, volumeRoot) {
		return "", false
	}
	return root, true
}

func pathWithinRoot(path string, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sanitizedHostEnvironment() []string {
	allowed := []string{
		"SystemRoot", "WINDIR", "SystemDrive", "ComSpec", "Path", "PATHEXT",
		"TEMP", "TMP", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
		"LOCALAPPDATA", "APPDATA", "ProgramData", "ProgramFiles",
		"ProgramW6432", "CommonProgramFiles", "CommonProgramW6432",
		"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS",
	}
	environment := make([]string, 0, len(allowed)+1)
	pathFound := false
	for _, key := range allowed {
		value, ok := os.LookupEnv(key)
		if !ok || value == "" {
			continue
		}
		entry := key + "=" + value
		if redact.String(entry) != entry {
			continue
		}
		if strings.EqualFold(key, "Path") {
			pathFound = true
		}
		environment = append(environment, entry)
	}
	if !pathFound {
		environment = append(environment, "Path=")
	}
	environment = append(environment, "NO_COLOR=1")
	sort.Slice(environment, func(left int, right int) bool {
		return strings.ToLower(environment[left]) <
			strings.ToLower(environment[right])
	})
	return environment
}
