package application

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadEmbeddedAnalyzerWorkspaceFileUsesConfinedRoot(t *testing.T) {
	root := t.TempDir()
	wanted := []byte("embedded analyzer input\n")
	if err := os.WriteFile(filepath.Join(root, "input.txt"), wanted, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readEmbeddedAnalyzerWorkspaceFile(root, "input.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wanted) {
		t.Fatalf("content=%q want=%q", got, wanted)
	}

	outside := filepath.Join(filepath.Dir(root), "outside-analyzer-input.txt")
	if err := os.WriteFile(outside, []byte("must not be read"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	if _, err := readEmbeddedAnalyzerWorkspaceFile(root,
		filepath.Join("..", filepath.Base(outside))); err == nil ||
		!strings.Contains(err.Error(), "escapes the workspace") {
		t.Fatalf("lexical escape err=%v", err)
	}

	t.Run("rejects symbolic link outside root", func(t *testing.T) {
		link := filepath.Join(root, "outside-link.txt")
		if err := os.Symlink(outside, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symbolic link creation is unavailable: %v", err)
			}
			t.Fatal(err)
		}
		if _, err := readEmbeddedAnalyzerWorkspaceFile(root, filepath.Base(link)); err == nil {
			t.Fatal("workspace-confined read followed a symbolic link outside the root")
		}
	})
}
