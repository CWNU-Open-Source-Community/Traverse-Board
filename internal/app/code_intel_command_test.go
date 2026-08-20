package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/codeintel"
)

func TestCodeIntelCLIReportsReviewedMetadataAndQualificationWithoutLaunchDetails(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	if stdout, stderr, code := executeTestCommand(t, "workspace", "init", "demo"); code != 0 || stderr != "" || !strings.Contains(stdout, "workspace demo initialized") {
		t.Fatalf("initialize workspace: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	shown, stderr, code := executeTestCommand(t, "workspace", "show", "demo")
	if code != 0 || stderr != "" {
		t.Fatalf("show workspace: code=%d stdout=%s stderr=%s", code, shown, stderr)
	}
	workspaceID := fieldLine(shown, "id")
	if workspaceID == "" {
		t.Fatalf("workspace output lacks id: %s", shown)
	}

	configPath := writeCodeIntelCLIConfig(t, workspaceID)
	status, stderr, code := executeTestCommand(t, "code-intel", "status",
		"--config", configPath, "--json")
	if code != 0 || stderr != "" || !json.Valid([]byte(status)) ||
		!strings.Contains(status, `"protocol_version": "code-intel-lsp.v1"`) ||
		!strings.Contains(status, `"health": "configured"`) {
		t.Fatalf("unexpected code-intel status: code=%d stdout=%s stderr=%s",
			code, status, stderr)
	}
	if containsCodeIntelLaunchDetail(status) {
		t.Fatalf("code-intel status exposed a launch detail: %s", status)
	}

	qualified, stderr, code := executeTestCommand(t, "code-intel", "qualify",
		"--config", configPath, "--workspace", "demo", "--start=false", "--json")
	if code != 0 || stderr != "" || !strings.Contains(qualified, `"eligible": true`) ||
		!strings.Contains(qualified, `"executable_hash_matched": true`) ||
		!strings.Contains(qualified, `"minimal_environment": true`) ||
		!strings.Contains(qualified, `"network_access_granted": false`) {
		t.Fatalf("unexpected code-intel qualification: code=%d stdout=%s stderr=%s",
			code, qualified, stderr)
	}
	if containsCodeIntelLaunchDetail(qualified) {
		t.Fatalf("code-intel qualification exposed a launch detail: %s", qualified)
	}
}

func TestCodeIntelCLIRequiresAnExplicitConfiguration(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	t.Setenv("CYBERAGENT_CODE_INTEL_CONFIG", "")
	stdout, stderr, code := executeTestCommand(t, "code-intel", "status")
	if code == 0 || stdout != "" || !strings.Contains(stderr,
		"no explicit code-intel config is selected") {
		t.Fatalf("unexpected missing-config result: code=%d stdout=%s stderr=%s",
			code, stdout, stderr)
	}
}

func writeCodeIntelCLIConfig(t *testing.T, workspaceID string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	executable = filepath.Clean(executable)
	rawExecutable, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(rawExecutable)
	config := codeintel.Config{ProtocolVersion: codeintel.ConfigProtocolVersion,
		Servers: []codeintel.ServerDescriptor{{ProtocolVersion: codeintel.ProtocolVersion,
			ID: "cli-test-lsp", Name: "CLI test LSP", WorkspaceID: workspaceID,
			Languages:  []codeintel.Language{{ID: "go", Extensions: []string{".go"}}},
			Executable: executable, ExecutableSHA256: hex.EncodeToString(digest[:]),
			RequestTimeoutMillis: 1000, ReviewedBy: "cli-test-operator",
			ReviewedAt: time.Unix(1, 0).UTC()}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "code-intel.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fieldLine(output, name string) string {
	prefix := name + ": "
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func containsCodeIntelLaunchDetail(output string) bool {
	for _, field := range []string{`"executable":`, `"arguments":`, `"argv":`,
		`"environment":`, `"credential":`, `"token":`} {
		if strings.Contains(output, field) {
			return true
		}
	}
	return false
}
