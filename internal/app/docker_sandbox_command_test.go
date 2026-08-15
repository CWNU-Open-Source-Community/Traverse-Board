package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/store"
)

func TestDockerSandboxCLIRequiresExplicitProcessCapabilities(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	encoded, err := json.Marshal(dockerSandboxCLITestManifest())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := executeTestCommand(t, "run", "sandbox", "docker-admit",
		"sandbox-docker-plan-cli", "--manifest-file", manifestPath,
		"--operation-key", "docker-admit-cli-key")
	if code == 0 || !strings.Contains(stderr, "require --enable-docker-execution") {
		t.Fatalf("admission did not require explicit Docker capability: code=%d stderr=%s",
			code, stderr)
	}

	_, stderr, code = executeTestCommand(t, "run", "sandbox", "docker-admit",
		"sandbox-docker-plan-cli", "--manifest-file", manifestPath,
		"--operation-key", "docker-admit-cli-key", "--enable-docker-execution")
	if code == 0 || !strings.Contains(stderr, "requires --enable-permission-control") {
		t.Fatalf("admission did not require permission capability: code=%d stderr=%s",
			code, stderr)
	}

	_, stderr, code = executeTestCommand(t, "run", "sandbox", "docker-readiness",
		"sandbox-docker-plan-cli", "--manifest-file", manifestPath,
		"--docker-host", "tcp://127.0.0.1:2375")
	if code == 0 || (!strings.Contains(stderr, "flag provided but not defined") &&
		!strings.Contains(stderr, "usage: cyberagent run sandbox docker-readiness")) {
		t.Fatalf("CLI accepted a caller-supplied Docker endpoint: code=%d stderr=%s",
			code, stderr)
	}
}

func TestDockerSandboxAppCompositionKeepsCapabilityProcessLocal(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := &App{home: home, store: st, checker: policy.NewDefaultChecker()}
	disabled, err := app.newDockerSandboxService(false,
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	disabledCapabilities, disabledEpoch, err := disabled.RuntimeCapabilities()
	if err != nil || disabledCapabilities.Enabled || disabledEpoch == "" {
		t.Fatalf("disabled capability was not process-local and zero-closed: %#v %q err=%v",
			disabledCapabilities, disabledEpoch, err)
	}
	if info, err := os.Stat(filepath.Join(home, dockerSandboxStagingDirectory)); err != nil ||
		!info.IsDir() {
		t.Fatalf("trusted staging root was not created: info=%v err=%v", info, err)
	}

	enabled, err := app.newDockerSandboxService(true,
		domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	enabledCapabilities, enabledEpoch, err := enabled.RuntimeCapabilities()
	if err != nil || !enabledCapabilities.Enabled || enabledEpoch == "" ||
		enabledEpoch == disabledEpoch {
		t.Fatalf("enabled capability did not receive a fresh process epoch: %#v %q err=%v",
			enabledCapabilities, enabledEpoch, err)
	}
	if app.dockerSandbox != enabled || app.newDockerSandboxProposalExecutor() == nil {
		t.Fatal("App did not inject the process-owned Docker Sandbox service into model proposals")
	}
}

func TestAPIDockerExecutionFlagRequiresPermissionControl(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	_, stderr, code := executeTestCommand(t, "api", "serve", "--enable-docker-execution")
	if code == 0 || !strings.Contains(stderr,
		"--enable-docker-execution requires --enable-permission-control") {
		t.Fatalf("API accepted Docker execution without permission control: code=%d stderr=%s",
			code, stderr)
	}
}

func dockerSandboxCLITestManifest() sandbox.Manifest {
	return sandbox.Manifest{
		ProtocolVersion: sandbox.ManifestProtocolVersion, Backend: sandbox.BackendDocker,
		Command: sandbox.CommandSpec{Executable: "/bin/true",
			WorkingDirectory: "/workspace"},
		Mounts: []sandbox.Mount{{Source: ".", Target: "/workspace",
			Access: sandbox.MountReadOnly}},
		Network: sandbox.NetworkScope{Mode: "disabled"},
		Resources: sandbox.ResourceLimits{CPUQuotaMillis: 1000,
			MemoryBytes: 256 * 1024 * 1024, PIDs: 64,
			MaxOutputBytes: 4 * 1024 * 1024},
		Output:         sandbox.OutputSpec{CaptureStdout: true, CaptureStderr: true},
		TimeoutSeconds: 300,
		Cancellation:   sandbox.CancellationSpec{GracePeriodMillis: 2000},
	}
}
