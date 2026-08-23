//go:build windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/sandboxtest"
)

func TestMain(m *testing.M) {
	restore, err := sandboxtest.PrepareHost()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "prepare Windows Local Sandbox host ACLs: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := restore(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "restore Windows Local Sandbox host ACLs: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func TestWindowsLocalSandboxCLIProbeOpensOnlyWorkspaceGate(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	readinessJSON, stderr, code := executeTestCommand(t, "sandbox", "local-readiness",
		"--enable-workspace-sandbox", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("local readiness stdout=%s stderr=%s code=%d",
			readinessJSON, stderr, code)
	}
	var readiness sandbox.LocalReadiness
	if err := json.Unmarshal([]byte(readinessJSON), &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.Validate() != nil || !readiness.Ready || readiness.CapabilityGrant {
		t.Fatalf("real Local Sandbox probe was not ready: %#v", readiness)
	}

	created, stderr, code := executeTestCommand(t, "run", "create",
		"project verified local readiness", "--profile", "code", "--surface", "code",
		"--phase", "deliver", "--max-turns", "2")
	if code != 0 || stderr != "" {
		t.Fatalf("create stdout=%s stderr=%s code=%d", created, stderr, code)
	}
	runID := runIDPattern.FindString(created)
	projectionJSON, stderr, code := executeTestCommand(t, "run", "capability-readiness",
		runID, "--json", "--enable-permission-control", "--enable-workspace-sandbox")
	if code != 0 || stderr != "" {
		t.Fatalf("capability readiness stdout=%s stderr=%s code=%d",
			projectionJSON, stderr, code)
	}
	var projection application.RunCapabilityReadiness
	if err := json.Unmarshal([]byte(projectionJSON), &projection); err != nil {
		t.Fatal(err)
	}
	workspace := projection.Permissions[1]
	fullAccess := projection.Permissions[3]
	if workspace.Value != "workspace_access" || !workspace.Selectable ||
		!workspace.RuntimeAvailable || fullAccess.RuntimeAvailable ||
		fullAccess.Selectable || projection.CapabilityGrant {
		t.Fatalf("Local readiness widened the wrong gate: workspace=%#v full=%#v",
			workspace, fullAccess)
	}

	selected, stderr, code := executeTestCommand(t, "run", "execution-permission", "set",
		runID, "workspace_access", "--operation-key", "local-sandbox-select-0001",
		"--confirm-workspace-access", "--enable-permission-control",
		"--enable-workspace-sandbox")
	if code != 0 || stderr != "" || !strings.Contains(selected, "mode: workspace_access") ||
		!strings.Contains(selected, "runtime_gate_available: true") ||
		!strings.Contains(selected, "capability_grant: false") {
		t.Fatalf("Workspace Access selection stdout=%s stderr=%s code=%d",
			selected, stderr, code)
	}
}

func TestWindowsAPIServePublishesWorkspaceGateOnlyAfterLocalProbe(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	t.Setenv(apiTokenEnvironment, "local-readiness-api-token-0123456789")
	t.Setenv(apiControlTokenEnvironment, "local-readiness-control-token-012345")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout synchronizedBuffer
	var stderr synchronizedBuffer
	done := make(chan int, 1)
	go func() {
		done <- ExecuteContext(ctx, []string{"api", "serve", "--listen", "127.0.0.1:0",
			"--enable-permission-control", "--enable-workspace-sandbox"},
			&stdout, &stderr)
	}()
	output := waitForAPIProcessOutput(t, &stdout, &stderr, done, func(value string) bool {
		return outputField(value, "api_url") != "" &&
			strings.Contains(value, "workspace_sandbox_enabled: true")
	})
	if strings.Contains(output, "danger_full_access_enabled: true") ||
		strings.Contains(output, "debug_maximum_access_enabled: true") ||
		strings.Contains(output, "command_runtime_enabled: true") {
		t.Fatalf("Workspace Sandbox probe widened host execution: %s", output)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 || stderr.String() != "" {
			t.Fatalf("API shutdown code=%d stderr=%s", code, stderr.String())
		}
	case <-time.After(4 * time.Second):
		t.Fatal("API did not stop after Local Sandbox test cancellation")
	}
}
