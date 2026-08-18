//go:build !windows

package runner

import (
	"os"
	"path/filepath"
	"strings"
)

func resolveCommandRuntimeShell(profile CommandRuntimeProfile) (string, error) {
	var candidates []string
	switch profile {
	case CommandRuntimeBash:
		candidates = []string{"/bin/bash", "/usr/bin/bash", "/opt/homebrew/bin/bash"}
	case CommandRuntimePowerShell:
		candidates = []string{"/usr/bin/pwsh", "/usr/local/bin/pwsh", "/opt/homebrew/bin/pwsh"}
	default:
		return "", ErrCommandRuntimeBoundary
	}
	for _, path := range candidates {
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return filepath.Clean(path), nil
		}
	}
	return "", ErrCommandRuntimeUnavailable
}

func commandRuntimeNativeExecutableAllowed(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	blocked := map[string]struct{}{
		"sh": {}, "bash": {}, "dash": {}, "zsh": {}, "fish": {}, "ksh": {},
		"csh": {}, "tcsh": {}, "pwsh": {}, "powershell": {}, "env": {},
	}
	_, found := blocked[base]
	return !found
}

func commandRuntimeInheritedEnvironmentNames() []string {
	return []string{
		"PATH", "TMPDIR", "LANG", "LC_ALL", "GOROOT", "GOPATH", "GOCACHE",
		"GOMODCACHE", "RUSTUP_HOME", "CARGO_HOME",
	}
}

func commandRuntimeFixedEnvironment() []string {
	return []string{
		"CI=1", "NO_COLOR=1", "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=/dev/null",
		"GIT_LFS_SKIP_SMUDGE=1", "GIT_OPTIONAL_LOCKS=0", "HOME=", "SSH_AUTH_SOCK=",
	}
}
