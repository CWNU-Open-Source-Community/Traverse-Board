package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandardCodeDockerCLIHasNoImageOrDockerArgumentEscapeHatch(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	t.Setenv(standardCodeDockerImageEnvironment, "")
	base := []string{"run", "standard-code", "docker-readiness",
		"standard-code-run-1", "--generation", "1", "--checkpoint",
		"standard-code-checkpoint-1", "--toolchain", "go", "--purpose",
		"offline test", "--enable-permission-control", "--enable-workspace-sandbox"}
	_, stderr, code := executeTestCommand(t, base...)
	if code != 1 || !strings.Contains(stderr, standardCodeDockerImageEnvironment) {
		t.Fatalf("missing fixed image identity was not fail-closed: code=%d stderr=%s",
			code, stderr)
	}
	for _, escape := range []string{"--image-digest", "--docker-endpoint",
		"--mount", "--network", "--env", "--docker-flag"} {
		args := append(append([]string(nil), base...), escape, "attacker-controlled")
		_, stderr, code = executeTestCommand(t, args...)
		if code != 2 || !strings.Contains(stderr, "usage: cyberagent run standard-code") {
			t.Fatalf("CLI accepted backend escape %s: code=%d stderr=%s",
				escape, code, stderr)
		}
	}
}

func TestStandardCodeDockerExecuteRequiresAllProcessGates(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	args := []string{"run", "standard-code", "docker-execute",
		"standard-code-run-1", "--generation", "1", "--checkpoint",
		"standard-code-checkpoint-1", "--toolchain", "rust", "--purpose",
		"offline test", "--preparation", "standard-code-preparation-1",
		"--approval", "standard-code-approval-1", "--operation-key",
		"standard-code-operation-1"}
	_, stderr, code := executeTestCommand(t, args...)
	if code != 2 || !strings.Contains(stderr, "--enable-permission-control") {
		t.Fatalf("execution gates were not fail-closed: code=%d stderr=%s", code, stderr)
	}
}

func TestStandardCodePresetCLIUsesOneStrictNonAuthorizingResult(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	if _, stderr, code := executeTestCommand(t, "workspace", "init", "standard-code-cli"); code != 0 {
		t.Fatalf("workspace init failed: %s", stderr)
	}
	root := filepath.Join(home, "workspaces", "standard-code-cli")
	runDrydockCLIGit(t, root, "init", "-q", "-b", "main")
	runDrydockCLIGit(t, root, "config", "user.email", "standard-code@example.invalid")
	runDrydockCLIGit(t, root, "config", "user.name", "Standard Code CLI Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runDrydockCLIGit(t, root, "add", "tracked.txt")
	runDrydockCLIGit(t, root, "commit", "-q", "-m", "base")

	output, stderr, code := executeTestCommand(t, "run", "standard-code", "preset",
		"--workspace", "standard-code-cli", "--goal", "verify atomic CLI",
		"--backend", "auto", "--operation-key", "standard-code-cli-preview-0001",
		"--enable-permission-control", "--enable-workspace-sandbox", "--json")
	var view struct {
		ProtocolVersion string `json:"protocol_version"`
		Status          string `json:"status"`
		Network         string `json:"network"`
		Credentials     string `json:"credentials"`
		CapabilityGrant bool   `json:"capability_grant"`
		TrustRequired   bool   `json:"trust_required"`
		TrustDigest     string `json:"trust_digest"`
		LocalReadiness  struct {
			Available bool `json:"available"`
		} `json:"local_readiness"`
	}
	if code != 0 || stderr != "" || json.Unmarshal([]byte(output), &view) != nil ||
		view.ProtocolVersion != "standard_code_preset.v1" || view.Status != "blocked" ||
		view.Network != "disabled" || view.Credentials != "none" ||
		view.CapabilityGrant || strings.Contains(strings.ToLower(output), "bearer") {
		t.Fatalf("preset output=%q stderr=%q code=%d parsed=%+v",
			output, stderr, code, view)
	}
	if view.LocalReadiness.Available && (!view.TrustRequired || len(view.TrustDigest) != 64) {
		t.Fatalf("ready Local backend omitted exact trust review: %+v", view)
	}

	_, stderr, code = executeTestCommand(t, "run", "standard-code", "preset",
		"--workspace", "standard-code-cli", "--goal", "model must fail",
		"--operation-key", "standard-code-cli-model-0001", "--operator", "model",
		"--enable-permission-control", "--enable-workspace-sandbox", "--json")
	if code == 0 || !strings.Contains(stderr, "cannot invoke Standard Code") {
		t.Fatalf("model-class CLI invocation was not rejected: code=%d stderr=%s",
			code, stderr)
	}
}

func TestStandardCodePauseAndConfigureCLIRequiresRunIdentity(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	_, stderr, code := executeTestCommand(t, "run", "standard-code",
		"pause-and-configure", "--operation-key", "standard-code-pause-missing-run-0001",
		"--enable-permission-control", "--enable-workspace-sandbox")
	if code != 2 || !strings.Contains(stderr, "usage: cyberagent run standard-code") {
		t.Fatalf("missing pause Run was accepted: code=%d stderr=%s", code, stderr)
	}
}
