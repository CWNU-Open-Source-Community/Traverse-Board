package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentCodeCreateMissingParentsKeepsReadMoveAndPolicyBoundaries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("generated/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target, fingerprint, err := AgentCodeResolveCreatePath(root, "test/nested/file.mjs")
	if err != nil || target != filepath.Join(root, "test/nested/file.mjs") || fingerprint == "" {
		t.Fatalf("create target %q %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(root, "test")); !os.IsNotExist(err) {
		t.Fatalf("resolution created a directory: %v", err)
	}
	if _, _, err := AgentCodeResolveWritePath(root, "test/nested/file.mjs", true); err == nil {
		t.Fatal("ordinary write/move parent requirement widened")
	}
	if _, err := AgentCodeReadFile(root, "ws", "test/nested/file.mjs", 1, 10, false); err == nil {
		t.Fatal("read unexpectedly succeeded")
	}
	if err := os.Mkdir(filepath.Join(root, "Case"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside/new.txt", ".git/config", "test/.gitkeep", "generated/missing/file.txt", "case/new/file.txt"} {
		if _, _, err := AgentCodeResolveCreatePath(root, path); err == nil {
			t.Fatalf("unsafe create allowed: %s", path)
		}
	}
}
