//go:build windows && desktop && wv2runtime.error

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"cyberagent-workbench/internal/desktop"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func discoverWorkspaceLaunchers() ([]workspaceLauncherCandidate, error) {
	candidates := make(map[string]workspaceLauncherCandidate)
	localAppData, _ := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	windowsRoot, _ := windows.KnownFolderPath(windows.FOLDERID_Windows, windows.KF_FLAG_DEFAULT)
	programFiles, _ := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, windows.KF_FLAG_DEFAULT)
	programFilesX86, _ := windows.KnownFolderPath(windows.FOLDERID_ProgramFilesX86, windows.KF_FLAG_DEFAULT)

	addWorkspaceLauncher(candidates, workspaceLauncher("antigravity", "Antigravity",
		desktop.WorkspaceLauncherEditor, filepath.Join(localAppData,
			"Programs", "antigravity", "Antigravity.exe"), true, false))
	addWorkspaceLauncher(candidates, workspaceLauncher("file-explorer", "File Explorer",
		desktop.WorkspaceLauncherFolder, filepath.Join(windowsRoot, "explorer.exe"), true, false))
	addWorkspaceLauncher(candidates, workspaceLauncher("terminal", "Terminal",
		desktop.WorkspaceLauncherTerminal, filepath.Join(localAppData,
			"Microsoft", "WindowsApps", "wt.exe"), false, true))
	if _, exists := candidates["terminal"]; !exists {
		addWorkspaceLauncher(candidates, workspaceLauncher("terminal", "Terminal",
			desktop.WorkspaceLauncherTerminal, filepath.Join(windowsRoot,
				"System32", "WindowsPowerShell", "v1.0", "powershell.exe"), false, false))
	}
	for _, root := range []string{localAppData, programFiles, programFilesX86} {
		addWorkspaceLauncher(candidates, workspaceLauncher("visual-studio-code", "Visual Studio Code",
			desktop.WorkspaceLauncherEditor, filepath.Join(root,
				"Programs", "Microsoft VS Code", "Code.exe"), true, false))
		addWorkspaceLauncher(candidates, workspaceLauncher("visual-studio-code", "Visual Studio Code",
			desktop.WorkspaceLauncherEditor, filepath.Join(root,
				"Microsoft VS Code", "Code.exe"), true, false))
	}
	for _, candidate := range registryWorkspaceLaunchers() {
		addWorkspaceLauncher(candidates, candidate)
	}

	order := map[string]int{
		"antigravity": 0, "file-explorer": 1, "terminal": 2,
		"pycharm": 3, "webstorm": 4, "visual-studio-code": 5,
	}
	return orderedWorkspaceLaunchers(candidates, order), nil
}

func registryWorkspaceLaunchers() []workspaceLauncherCandidate {
	const uninstallPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	type registryRoot struct {
		key  registry.Key
		view uint32
	}
	roots := []registryRoot{
		{registry.CURRENT_USER, registry.WOW64_64KEY},
		{registry.CURRENT_USER, registry.WOW64_32KEY},
		{registry.LOCAL_MACHINE, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, registry.WOW64_32KEY},
	}
	var out []workspaceLauncherCandidate
	for _, root := range roots {
		key, err := registry.OpenKey(root.key, uninstallPath, registry.READ|root.view)
		if err != nil {
			continue
		}
		names, err := key.ReadSubKeyNames(-1)
		_ = key.Close()
		if err != nil {
			continue
		}
		sort.Strings(names)
		for _, name := range names {
			subkey, openErr := registry.OpenKey(root.key,
				uninstallPath+`\`+name, registry.READ|root.view)
			if openErr != nil {
				continue
			}
			displayName, _, nameErr := subkey.GetStringValue("DisplayName")
			displayIcon, _, iconErr := subkey.GetStringValue("DisplayIcon")
			_ = subkey.Close()
			if nameErr != nil || iconErr != nil {
				continue
			}
			id, label, kind, matched := classifyWorkspaceLauncher(displayName)
			if !matched {
				continue
			}
			executable := parseDisplayIconExecutable(displayIcon)
			if !launcherExecutableMatches(id, executable) {
				continue
			}
			out = append(out, workspaceLauncher(id, label, kind, executable, true, false))
		}
	}
	return out
}

func classifyWorkspaceLauncher(displayName string) (string, string,
	desktop.WorkspaceLauncherKind, bool) {
	value := strings.ToLower(strings.TrimSpace(displayName))
	switch {
	case strings.Contains(value, "antigravity"):
		return "antigravity", "Antigravity", desktop.WorkspaceLauncherEditor, true
	case strings.Contains(value, "pycharm"):
		return "pycharm", "PyCharm", desktop.WorkspaceLauncherEditor, true
	case strings.Contains(value, "webstorm"):
		return "webstorm", "WebStorm", desktop.WorkspaceLauncherEditor, true
	case strings.Contains(value, "visual studio code") || strings.Contains(value, "vscode"):
		return "visual-studio-code", "Visual Studio Code", desktop.WorkspaceLauncherEditor, true
	default:
		return "", "", "", false
	}
}

func parseDisplayIconExecutable(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "%") {
		return ""
	}
	if strings.HasPrefix(value, `"`) {
		if end := strings.Index(value[1:], `"`); end >= 0 {
			return filepath.Clean(value[1 : end+1])
		}
		return ""
	}
	if comma := strings.LastIndex(value, ","); comma >= 0 {
		value = value[:comma]
	}
	return filepath.Clean(strings.TrimSpace(value))
}

func launcherExecutableMatches(id, executable string) bool {
	base := strings.ToLower(filepath.Base(executable))
	switch id {
	case "antigravity":
		return base == "antigravity.exe"
	case "pycharm":
		return base == "pycharm64.exe"
	case "webstorm":
		return base == "webstorm64.exe"
	case "visual-studio-code":
		return base == "code.exe"
	default:
		return false
	}
}

func validateLauncherExecutable(candidate workspaceLauncherCandidate) error {
	path := candidate.executable
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) ||
		!strings.EqualFold(filepath.Ext(path), ".exe") {
		return errors.New("native workspace launcher executable is invalid")
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errors.New("native workspace launcher executable is invalid")
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 ||
		(!candidate.allowReparse && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0) {
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
	if candidate.passRoot {
		arguments = []string{target.RootPath}
	}
	command := exec.Command(candidate.executable, arguments...)
	command.Dir = target.RootPath
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	return command, nil
}
