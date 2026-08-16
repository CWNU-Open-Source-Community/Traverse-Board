package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/runmutation"
)

const (
	OnceCommandProtocolVersion     = "once_command.v1"
	OnceCommandPolicyVersion       = "once_command_policy.v1"
	OnceExecutionProtocolVersion   = "once_execution.v1"
	MaxOnceCommandArguments        = 64
	MaxOnceCommandArgumentBytes    = 16 * 1024
	MaxOnceCommandArgumentsBytes   = 64 * 1024
	MaxOnceCommandEnvironment      = 16
	MaxOnceCommandEnvironmentBytes = 16 * 1024
	MaxOnceCommandPurposeRunes     = 1200
	MaxOnceCommandTimeout          = 10 * time.Minute
	MaxOnceOutputBytes             = 64 * 1024
)

var ErrOnceCommandBoundary = errors.New("once command boundary is invalid")

// onceShellInterpreters are shell binaries that could reinterpret argv as
// script text. One-shot commands execute directly without a shell, so any of
// these as the executable is rejected outright.
var onceShellInterpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "csh": true, "tcsh": true,
	"cmd": true, "cmd.exe": true, "powershell": true, "powershell.exe": true,
	"pwsh": true, "pwsh.exe": true, "wscript": true, "wscript.exe": true,
	"cscript": true, "cscript.exe": true,
}

// OnceEnvironmentAllowlist is the only environment the one-shot runner ever
// passes to a child. It never loads PowerShell/Bash profiles or inherits the
// agent process environment.
var OnceEnvironmentAllowlist = map[string]bool{
	"SystemRoot": true, "WINDIR": true, "TEMP": true, "TMP": true,
}

// OnceCommandSpec is the exact, structured request: executable, argv, cwd and
// environment. There is no shell string anywhere in the protocol; each argv
// item is a literal argument to the spawned executable.
type OnceCommandSpec struct {
	ProtocolVersion     string   `json:"protocol_version"`
	ExecutablePath      string   `json:"executable_path"`
	Argv                []string `json:"argv"`
	WorkingDirectory    string   `json:"working_directory"`
	Environment         []string `json:"environment"`
	TimeoutMilliseconds int64    `json:"timeout_milliseconds"`
	Purpose             string   `json:"purpose"`
}

// OnceCommandRequest binds a spec to one Run and Workspace. The workspace
// root is a trusted, already-resolved path supplied by the application layer.
type OnceCommandRequest struct {
	Spec             OnceCommandSpec
	RunID            string
	MissionID        string
	WorkspaceID      string
	WorkspaceRoot    string
	RequestedBy      string
	OperatorApproved bool
}

// ValidateOnceCommandSpec enforces the full boundary: shell interpreters,
// workspace escape via cwd symlinks/junctions, hostile argv/env, bounds, and
// purpose limits. It never executes anything.
func ValidateOnceCommandSpec(spec OnceCommandSpec, workspaceRoot string) error {
	if spec.ProtocolVersion != OnceCommandProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol %q", ErrOnceCommandBoundary, spec.ProtocolVersion)
	}
	if strings.TrimSpace(workspaceRoot) == "" || strings.ContainsRune(workspaceRoot, 0) {
		return fmt.Errorf("%w: workspace root is required", ErrOnceCommandBoundary)
	}
	resolvedRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return fmt.Errorf("%w: workspace root does not resolve: %v", ErrOnceCommandBoundary, err)
	}
	if err := validateOnceExecutable(spec.ExecutablePath, resolvedRoot); err != nil {
		return err
	}
	if len(spec.Argv) > MaxOnceCommandArguments {
		return fmt.Errorf("%w: argv exceeds %d entries", ErrOnceCommandBoundary, MaxOnceCommandArguments)
	}
	totalArgs := 0
	for _, argument := range spec.Argv {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) {
			return fmt.Errorf("%w: argv entry is not valid UTF-8 without NUL", ErrOnceCommandBoundary)
		}
		if len(argument) > MaxOnceCommandArgumentBytes {
			return fmt.Errorf("%w: argv entry exceeds %d bytes", ErrOnceCommandBoundary, MaxOnceCommandArgumentBytes)
		}
		totalArgs += len(argument)
	}
	if totalArgs > MaxOnceCommandArgumentsBytes {
		return fmt.Errorf("%w: argv exceeds %d bytes", ErrOnceCommandBoundary, MaxOnceCommandArgumentsBytes)
	}
	if err := validateOnceWorkingDirectory(spec.WorkingDirectory, resolvedRoot); err != nil {
		return err
	}
	if err := validateOnceEnvironment(spec.Environment); err != nil {
		return err
	}
	if spec.TimeoutMilliseconds < 1 || spec.TimeoutMilliseconds > MaxOnceCommandTimeout.Milliseconds() {
		return fmt.Errorf("%w: timeout must be between 1ms and %s", ErrOnceCommandBoundary, MaxOnceCommandTimeout)
	}
	if strings.TrimSpace(spec.Purpose) == "" || utf8.RuneCountInString(spec.Purpose) > MaxOnceCommandPurposeRunes ||
		!utf8.ValidString(spec.Purpose) || strings.ContainsRune(spec.Purpose, 0) {
		return fmt.Errorf("%w: purpose must be bounded valid UTF-8", ErrOnceCommandBoundary)
	}
	return nil
}

func validateOnceExecutable(executablePath, resolvedRoot string) error {
	if !filepath.IsAbs(executablePath) || strings.ContainsRune(executablePath, 0) {
		return fmt.Errorf("%w: executable must be an absolute path", ErrOnceCommandBoundary)
	}
	info, err := os.Lstat(executablePath)
	if err != nil {
		return fmt.Errorf("%w: executable stat: %v", ErrOnceCommandBoundary, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: executable must be a regular file without symlink or junction", ErrOnceCommandBoundary)
	}
	base := strings.ToLower(filepath.Base(executablePath))
	if onceShellInterpreters[base] {
		return fmt.Errorf("%w: shell interpreter %q cannot wrap a one-shot command", ErrOnceCommandBoundary, base)
	}
	if !onceExecutableExtensionAllowed(base) {
		return fmt.Errorf("%w: executable must be a native binary, not a script interpreter target", ErrOnceCommandBoundary)
	}
	resolvedExecutable, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		return fmt.Errorf("%w: executable does not resolve: %v", ErrOnceCommandBoundary, err)
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(resolvedExecutable))
	if err != nil {
		return fmt.Errorf("%w: executable directory does not resolve: %v", ErrOnceCommandBoundary, err)
	}
	if within, err := pathWithin(resolvedDir, resolvedRoot); err != nil {
		return fmt.Errorf("%w: %v", ErrOnceCommandBoundary, err)
	} else if within {
		return fmt.Errorf("%w: workspace files cannot be executed as one-shot commands", ErrOnceCommandBoundary)
	}
	return nil
}

func validateOnceWorkingDirectory(workingDirectory, resolvedRoot string) error {
	if strings.TrimSpace(workingDirectory) == "" || strings.ContainsRune(workingDirectory, 0) {
		return fmt.Errorf("%w: working directory is required", ErrOnceCommandBoundary)
	}
	joined := workingDirectory
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(resolvedRoot, joined)
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return fmt.Errorf("%w: working directory does not resolve: %v", ErrOnceCommandBoundary, err)
	}
	within, err := pathWithin(resolved, resolvedRoot)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOnceCommandBoundary, err)
	}
	if !within {
		return fmt.Errorf("%w: working directory escapes the workspace root", ErrOnceCommandBoundary)
	}
	return nil
}

func pathWithin(candidate, root string) (bool, error) {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, err
	}
	if relative == "." {
		return true, nil
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..", nil
}

func validateOnceEnvironment(environment []string) error {
	if len(environment) > MaxOnceCommandEnvironment {
		return fmt.Errorf("%w: environment exceeds %d entries", ErrOnceCommandBoundary, MaxOnceCommandEnvironment)
	}
	seen := make(map[string]bool, len(environment))
	for _, entry := range environment {
		if len(entry) > MaxOnceCommandEnvironmentBytes || strings.ContainsRune(entry, 0) || !utf8.ValidString(entry) {
			return fmt.Errorf("%w: environment entry is invalid", ErrOnceCommandBoundary)
		}
		key, _, found := strings.Cut(entry, "=")
		if !found || !OnceEnvironmentAllowlist[key] {
			return fmt.Errorf("%w: environment key %q is not in the allowlist", ErrOnceCommandBoundary, key)
		}
		if seen[key] {
			return fmt.Errorf("%w: environment key %q is duplicated", ErrOnceCommandBoundary, key)
		}
		seen[key] = true
	}
	return nil
}

// OnceCommandSpecFingerprint is the immutable digest of the exact command.
func OnceCommandSpecFingerprint(spec OnceCommandSpec) string {
	return runmutation.Fingerprint("once_command_spec.v1",
		spec.ExecutablePath, strings.Join(spec.Argv, "\x00"),
		spec.WorkingDirectory, strings.Join(sortOnceEnvironment(spec.Environment), "\x00"),
		strconv.FormatInt(spec.TimeoutMilliseconds, 10), spec.Purpose)
}

// OnceCommandRequestFingerprint binds the spec to the Run and Workspace.
func OnceCommandRequestFingerprint(runID, workspaceID string, spec OnceCommandSpec) string {
	return runmutation.Fingerprint("once_command_request.v1", runID, workspaceID, OnceCommandSpecFingerprint(spec))
}

// OnceCommandApprovalFingerprint binds the approved request to the approval
// identity, so parameters cannot change after approval.
func OnceCommandApprovalFingerprint(requestFingerprint, approvalID string) string {
	return runmutation.Fingerprint("once_command_approval.v1", requestFingerprint, approvalID)
}

func sortOnceEnvironment(values []string) []string {
	cloned := append([]string(nil), values...)
	sort.Strings(cloned)
	return cloned
}
