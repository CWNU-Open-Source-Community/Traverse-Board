package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableSkillCLIImportsThroughExistingCommandAndPluginLifecycle(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	directory, err := filepath.Abs(filepath.Join("..", "agentpackages", "testdata", "upstream", "agent-plugins-example"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"skill", "import-dir", directory, "--surface", "code", "--operation-key", "portable-cli-import"}
	if _, stderr, code := executeTestCommand(t, args...); code == 0 || !strings.Contains(stderr, "untrusted confirmation") {
		t.Fatalf("unconfirmed native import code=%d stderr=%q", code, stderr)
	}
	args = append(args, "--confirm-untrusted-skill")
	output, stderr, code := executeTestCommand(t, args...)
	if code != 0 || stderr != "" || !strings.Contains(output, "format: agent-plugins") || !strings.Contains(output, "state: enabled") || !strings.Contains(output, "skills: 1") || strings.Contains(output, "publisher: 0") {
		t.Fatalf("native CLI result code=%d stderr=%q output=%q", code, stderr, output)
	}
	listed, stderr, code := executeTestCommand(t, "plugin", "list")
	if code != 0 || stderr != "" || !strings.Contains(listed, `"plugin-installation.v2"`) || !strings.Contains(listed, `"agent-plugins-example"`) {
		t.Fatalf("existing plugin list lost native import code=%d stderr=%q output=%q", code, stderr, listed)
	}
}
