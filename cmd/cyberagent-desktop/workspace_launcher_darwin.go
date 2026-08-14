//go:build darwin && desktop

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"cyberagent-workbench/internal/desktop"
)

// darwinOpenExecutable is the fixed LaunchServices gateway. The Desktop never
// invokes app binaries directly or through a shell; the registered directory
// is the only argument a candidate receives.
const darwinOpenExecutable = "/usr/bin/open"

func discoverWorkspaceLaunchers() ([]workspaceLauncherCandidate, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil, errors.New("native workspace launcher home directory is unavailable")
	}
	return discoverWorkspaceLaunchersIn([]string{
		"/Applications", "/System/Applications/Utilities", "/Applications/Utilities",
	}, filepath.Join(home, "Applications")), nil
}

// discoverWorkspaceLaunchersIn resolves the fixed Prayu launcher set against
// the supplied application roots. Only absolute, existing .app bundles are
// kept; duplicates collapse to the first valid candidate, and the registered
// directory is the only launch argument. Finder is always available and opens
// the directory itself through /usr/bin/open.
func discoverWorkspaceLaunchersIn(systemRoots []string,
	userApplications string) []workspaceLauncherCandidate {
	candidates := make(map[string]workspaceLauncherCandidate)
	roots := append([]string{userApplications}, systemRoots...)
	for _, root := range roots {
		addWorkspaceLauncher(candidates, workspaceLauncher("antigravity", "Antigravity",
			desktop.WorkspaceLauncherEditor, filepath.Join(root, "Antigravity.app"), true, false))
		addWorkspaceLauncher(candidates, workspaceLauncher("pycharm", "PyCharm",
			desktop.WorkspaceLauncherEditor, filepath.Join(root, "PyCharm.app"), true, false))
		addWorkspaceLauncher(candidates, workspaceLauncher("pycharm", "PyCharm",
			desktop.WorkspaceLauncherEditor, filepath.Join(root, "PyCharm CE.app"), true, false))
		addWorkspaceLauncher(candidates, workspaceLauncher("webstorm", "WebStorm",
			desktop.WorkspaceLauncherEditor, filepath.Join(root, "WebStorm.app"), true, false))
		addWorkspaceLauncher(candidates, workspaceLauncher("visual-studio-code", "Visual Studio Code",
			desktop.WorkspaceLauncherEditor, filepath.Join(root, "Visual Studio Code.app"), true, false))
		addWorkspaceLauncher(candidates, workspaceLauncher("terminal", "Terminal",
			desktop.WorkspaceLauncherTerminal, filepath.Join(root, "Terminal.app"), false, false))
	}
	addWorkspaceLauncher(candidates, workspaceLauncher("finder", "Finder",
		desktop.WorkspaceLauncherFolder, "", true, false))

	order := map[string]int{
		"antigravity": 0, "finder": 1, "terminal": 2,
		"pycharm": 3, "webstorm": 4, "visual-studio-code": 5,
	}
	return orderedWorkspaceLaunchers(candidates, order)
}

// validateLauncherExecutable accepts only absolute, existing .app bundles.
// The finder candidate carries no bundle path and is validated through the
// fixed /usr/bin/open gateway instead.
func validateLauncherExecutable(candidate workspaceLauncherCandidate) error {
	if candidate.descriptor.ID == "finder" && candidate.executable == "" {
		info, err := os.Stat(darwinOpenExecutable)
		if err != nil || info.IsDir() {
			return errors.New("native workspace launcher executable is unavailable")
		}
		return nil
	}
	path := candidate.executable
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) ||
		!strings.EqualFold(filepath.Ext(path), ".app") {
		return errors.New("native workspace launcher executable is invalid")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return errors.New("native workspace launcher executable is unavailable")
	}
	return nil
}

func workspaceLauncherCommand(candidate workspaceLauncherCandidate,
	target desktop.WorkspaceOpenTarget) (*exec.Cmd, error) {
	if err := validateLauncherExecutable(candidate); err != nil {
		return nil, err
	}
	if err := validateWorkspaceDirectory(target.RootPath); err != nil {
		return nil, err
	}
	arguments := []string(nil)
	if candidate.descriptor.ID == "finder" {
		// Finder opens the registered directory itself.
		arguments = []string{target.RootPath}
	} else {
		arguments = []string{"-a", candidate.executable}
		if candidate.passRoot {
			arguments = append(arguments, target.RootPath)
		}
	}
	command := exec.Command(darwinOpenExecutable, arguments...)
	command.Dir = target.RootPath
	// Own process session so the external application is not tied to the
	// Desktop process group after launch.
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return command, nil
}
