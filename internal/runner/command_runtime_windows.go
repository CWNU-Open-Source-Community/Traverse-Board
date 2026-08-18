//go:build windows

package runner

import (
	"debug/pe"
	"errors"
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
			return filepath.Clean(path), nil
		}
	}
	return "", ErrCommandRuntimeUnavailable
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
	stem := strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".com")
	if strings.HasPrefix(stem, "python3.") || strings.HasPrefix(stem, "pypy3.") {
		return false
	}
	blocked := map[string]struct{}{
		"cmd.exe": {}, "powershell.exe": {}, "pwsh.exe": {}, "bash.exe": {},
		"sh.exe": {}, "wscript.exe": {}, "cscript.exe": {}, "mshta.exe": {},
		"rundll32.exe": {}, "regsvr32.exe": {}, "python.exe": {},
		"python2.exe": {}, "python3.exe": {}, "py.exe": {}, "pypy.exe": {},
		"pypy3.exe": {}, "node.exe": {}, "deno.exe": {}, "bun.exe": {},
		"perl.exe": {}, "ruby.exe": {}, "php.exe": {}, "lua.exe": {},
		"java.exe": {}, "javaw.exe": {}, "dotnet.exe": {}, "mono.exe": {},
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
