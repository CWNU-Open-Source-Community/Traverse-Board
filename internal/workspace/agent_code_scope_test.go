package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentCodeScopedSearchNeverDiscoversOutsideOwnership(t *testing.T) {
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("owned/visible.txt", "needle visible\n")
	write("outside/private.txt", "needle must-not-leak\n")
	scopes := []AgentCodeScopePath{{Path: "owned", Directory: true}}
	glob, err := AgentCodeScopedGlobFiles(root, "workspace-scope-test", "**", "", 20, scopes)
	if err != nil || len(glob.Paths) != 1 || glob.Paths[0] != "owned/visible.txt" {
		t.Fatalf("scoped glob=%#v err=%v", glob, err)
	}
	grep, err := AgentCodeScopedGrepFiles(root, "workspace-scope-test", "needle", "**", "",
		20, true, scopes)
	if err != nil || len(grep.Matches) != 1 ||
		grep.Matches[0].Path != "owned/visible.txt" || grep.Matches[0].Snippet != "needle visible" {
		t.Fatalf("scoped grep=%#v err=%v", grep, err)
	}

	fileOnly := []AgentCodeScopePath{{Path: "owned/visible.txt"}}
	glob, err = AgentCodeScopedGlobFiles(root, "workspace-scope-test", "**", "", 20, fileOnly)
	if err != nil || len(glob.Paths) != 1 || glob.Paths[0] != "owned/visible.txt" {
		t.Fatalf("file scoped glob=%#v err=%v", glob, err)
	}
}
