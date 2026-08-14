//go:build desktop

package main

import (
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/desktop"
)

func TestValidateWorkspaceDirectoryRejectsInvalidRoots(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]string{
		"empty":         "",
		"relative":      "relative/path",
		"nul":           string([]byte{'a', 0}),
		"non canonical": root + string(filepath.Separator) + ".",
		"missing":       filepath.Join(root, "missing"),
		"not directory": file,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateWorkspaceDirectory(candidate); err == nil {
				t.Fatalf("root %q was accepted", candidate)
			}
		})
	}
	if err := validateWorkspaceDirectory(root); err != nil {
		t.Fatalf("canonical directory was rejected: %v", err)
	}
}

func TestFindWorkspaceLauncherIsExact(t *testing.T) {
	editor := workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
		"/apps/editor", true, false)
	terminal := workspaceLauncher("terminal", "Terminal", desktop.WorkspaceLauncherTerminal,
		"/apps/terminal", false, false)
	candidates := []workspaceLauncherCandidate{editor, terminal}
	if _, found := findWorkspaceLauncher(candidates, "missing"); found {
		t.Fatal("unknown launcher was found")
	}
	got, found := findWorkspaceLauncher(candidates, "terminal")
	if !found || got != terminal {
		t.Fatalf("exact launcher lookup = %#v found=%t", got, found)
	}
}
