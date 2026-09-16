//go:build windows

package runner

import (
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func resolveCommandRuntimeShell(profile CommandRuntimeProfile) (string, error) {
	candidates := make([]string, 0, 12)
	switch profile {
	case CommandRuntimePowerShell:
		// Runtime selection belongs to the trusted host process, never the
		// per-command environment supplied by a model or project.
		if configured := os.Getenv("CYBERAGENT_POWERSHELL_PATH"); configured != "" {
			return resolveCommandRuntimeConfiguredPowerShell(configured)
		}
		for _, root := range controlledKnownFolders(windows.FOLDERID_ProgramFiles,
			windows.FOLDERID_ProgramFilesX64, windows.FOLDERID_ProgramFilesX86) {
			candidates = append(candidates, filepath.Join(root, "PowerShell", "7", "pwsh.exe"))
		}
		if root, err := controlledWindowsDirectory(); err == nil {
			candidates = append(candidates, filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
		}
	case CommandRuntimeBash:
		if gitPath, err := exec.LookPath("git.exe"); err == nil {
			if root, ok := commandRuntimeGitDistributionRoot(gitPath); ok {
				candidates = append(candidates, filepath.Join(root, "bin", "bash.exe"))
			}
		}
		roots := controlledKnownFolders(windows.FOLDERID_ProgramFiles,
			windows.FOLDERID_ProgramFilesX64, windows.FOLDERID_ProgramFilesX86,
			windows.FOLDERID_LocalAppData)
		for _, root := range roots {
			candidates = append(candidates, filepath.Join(root, "Git", "bin", "bash.exe"),
				filepath.Join(root, "Programs", "Git", "bin", "bash.exe"))
		}
	default:
		return "", ErrCommandRuntimeBoundary
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			continue
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
			// Resolve directory aliases before applying the common Workspace
			// exclusion and pinning the executable identity.
			return resolveCommandRuntimeProcess(path)
		}
	}
	return "", ErrCommandRuntimeUnavailable
}

func resolveCommandRuntimeConfiguredPowerShell(value string) (string, error) {
	value = strings.TrimSpace(value)
	volume := filepath.VolumeName(value)
	name := filepath.Base(value)
	if !validCommandRuntimeText(value, false) || len(value) > MaxCommandRuntimePathBytes ||
		!filepath.IsAbs(value) || len(volume) != 2 || volume[1] != ':' ||
		(!strings.EqualFold(name, "pwsh.exe") && !strings.EqualFold(name, "powershell.exe")) {
		return "", fmt.Errorf("%w: CYBERAGENT_POWERSHELL_PATH must name an absolute local pwsh.exe or powershell.exe", ErrCommandRuntimeBoundary)
	}
	path, err := resolveCommandRuntimeProcess(value)
	if err != nil {
		return "", fmt.Errorf("CYBERAGENT_POWERSHELL_PATH: %w", err)
	}
	if !commandRuntimePathEqual(value, path) {
		return "", fmt.Errorf("%w: CYBERAGENT_POWERSHELL_PATH cannot use a filesystem alias", ErrCommandRuntimeBoundary)
	}
	// NormalizeCommandRuntimeSpec performs the shared native-image, bounded
	// regular-file, Workspace exclusion, and SHA-256 checks on this exact path.
	return path, nil
}

func commandRuntimeGitDistributionRoot(gitPath string) (string, bool) {
	path, err := filepath.Abs(strings.TrimSpace(gitPath))
	if err != nil || !strings.EqualFold(filepath.Base(path), "git.exe") {
		return "", false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	directory := filepath.Dir(path)
	if !strings.EqualFold(filepath.Base(directory), "cmd") &&
		!strings.EqualFold(filepath.Base(directory), "bin") {
		return "", false
	}
	root := filepath.Clean(filepath.Join(directory, ".."))
	if root == filepath.Clean(filepath.VolumeName(root)+string(filepath.Separator)) {
		return "", false
	}
	return root, true
}

func commandRuntimeNativeExecutableAllowed(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if filepath.Ext(base) != ".exe" && filepath.Ext(base) != ".com" {
		return false
	}
	// Native development runtimes use the same pinned process boundary as Go
	// and other compiled programs. Shells and system/privilege brokers retain
	// their dedicated profile or remain unavailable through this path.
	blocked := map[string]struct{}{
		"cmd.exe": {}, "powershell.exe": {}, "pwsh.exe": {}, "bash.exe": {},
		"sh.exe": {}, "wscript.exe": {}, "cscript.exe": {}, "mshta.exe": {},
		"rundll32.exe": {}, "regsvr32.exe": {}, "py.exe": {},
		"busybox.exe": {}, "wsl.exe": {}, "runas.exe": {},
	}
	_, found := blocked[base]
	return !found
}

func commandRuntimeInheritedEnvironmentNames() []string {
	return []string{
		"SystemRoot", "WINDIR", "SystemDrive", "ComSpec", "Path", "PATHEXT",
		"TEMP", "TMP", "ProgramData", "ProgramFiles", "ProgramW6432",
		"CommonProgramFiles", "CommonProgramW6432", "LOCALAPPDATA",
		"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS", "GOROOT",
		"GOPATH", "GOCACHE", "GOMODCACHE", "RUSTUP_HOME",
	}
}

func commandRuntimeFixedEnvironment() []string {
	return []string{
		"CI=1", "NO_COLOR=1", "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=NUL", "GIT_CONFIG_COUNT=5",
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=NUL",
		"GIT_CONFIG_KEY_2=core.fsmonitor", "GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_KEY_3=diff.external", "GIT_CONFIG_VALUE_3=",
		"GIT_CONFIG_KEY_4=core.pager", "GIT_CONFIG_VALUE_4=cat",
		"GIT_LFS_SKIP_SMUDGE=1", "GIT_OPTIONAL_LOCKS=0",
		"GIT_ALLOW_PROTOCOL=file", "GIT_ASKPASS=false", "SSH_ASKPASS=false",
		"GIT_EDITOR=false", "GIT_SEQUENCE_EDITOR=false", "GIT_EXTERNAL_DIFF=",
		"GOPROXY=off", "GOSUMDB=off", "CARGO_NET_OFFLINE=true",
		"NPM_CONFIG_OFFLINE=true", "PIP_NO_INDEX=1", "UV_OFFLINE=1",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1", "POWERSHELL_TELEMETRY_OPTOUT=1",
		"HOME=", "USERPROFILE=", "SSH_AUTH_SOCK=",
	}
}

func commandRuntimeExecutableAttributes(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("runtime executable is a reparse point")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	parsed, err := pe.NewFile(file)
	if err != nil {
		return errors.New("runtime executable is not a PE image")
	}
	return parsed.Close()
}

func commandRuntimePathEqual(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
