package app

import (
	"strings"
	"testing"
)

func TestStandardCodeDockerCLIHasNoImageOrDockerArgumentEscapeHatch(t *testing.T) {
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
