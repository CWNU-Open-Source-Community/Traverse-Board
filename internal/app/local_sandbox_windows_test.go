//go:build windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func setCanonicalLocalSandboxTestHome(t *testing.T) {
	t.Helper()
	home, err := sandboxtest.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CYBERAGENT_HOME", home)
}

func TestWindowsLocalSandboxCLIProbeOpensOnlyWorkspaceGate(t *testing.T) {
	setCanonicalLocalSandboxTestHome(t)
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
	setCanonicalLocalSandboxTestHome(t)
	t.Setenv(apiTokenEnvironment, "local-readiness-api-token-0123456789")
	t.Setenv(apiControlTokenEnvironment, "local-readiness-control-token-012345")
	ctx, cancel := context.WithCancel(context.Background())
	var stdout synchronizedBuffer
	var stderr synchronizedBuffer
	done := make(chan int, 1)
	go func() {
		defer close(done)
		done <- ExecuteContext(ctx, []string{"api", "serve", "--listen", "127.0.0.1:0",
			"--enable-permission-control", "--enable-workspace-sandbox"},
			&stdout, &stderr)
	}()
	// Stop and join the in-process server before t.TempDir cleanup even when the
	// startup assertion fails. Otherwise Windows can observe the SQLite handle
	// while it is still owned by the canceled control plane.
	t.Cleanup(func() {
		cancel()
		select {
		case code, ok := <-done:
			if ok && (code != 0 || stderr.String() != "") {
				t.Errorf("API cleanup code=%d stderr=%s", code, stderr.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("API did not stop during Local Sandbox test cleanup")
		}
	})
	output := waitForAPIProcessOutput(t, &stdout, &stderr, done, func(value string) bool {
		return outputField(value, "api_url") != "" &&
			strings.Contains(value, "workspace_sandbox_enabled: true")
	})
	if strings.Contains(output, "danger_full_access_enabled: true") ||
		strings.Contains(output, "debug_maximum_access_enabled: true") {
		t.Fatalf("Workspace Sandbox probe widened host execution: %s", output)
	}
	if !strings.Contains(output, "command_runtime_enabled: true") {
		t.Fatalf("Workspace Sandbox probe did not publish Command Runtime: %s", output)
	}
	request, err := http.NewRequest(http.MethodGet,
		outputField(output, "api_url")+"/capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer local-readiness-api-token-0123456789")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data struct {
			CommandRuntimeEnabled           bool `json:"command_runtime_enabled"`
			CommandRuntimeProtocolAvailable bool `json:"command_runtime_protocol_available"`
			CommandRuntimeAdapterInstalled  bool `json:"command_runtime_adapter_installed"`
			CommandRuntimeAdapterReady      bool `json:"command_runtime_adapter_ready"`
			CommandRuntimeAdapters          []struct {
				Kind             string `json:"kind"`
				Backend          string `json:"backend"`
				IsolationGrade   string `json:"isolation_grade"`
				NetworkPolicy    string `json:"network_policy"`
				CredentialPolicy string `json:"credential_policy"`
				Ready            bool   `json:"ready"`
			} `json:"command_runtime_adapters"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	adapters := envelope.Data.CommandRuntimeAdapters
	if response.StatusCode != http.StatusOK || !envelope.Data.CommandRuntimeEnabled ||
		!envelope.Data.CommandRuntimeProtocolAvailable ||
		!envelope.Data.CommandRuntimeAdapterInstalled ||
		!envelope.Data.CommandRuntimeAdapterReady || len(adapters) != 1 ||
		adapters[0].Kind != "sandboxed_workspace" ||
		adapters[0].Backend != application.CommandRuntimeLocalSandboxBackend ||
		adapters[0].IsolationGrade != "workspace_sandbox" ||
		adapters[0].NetworkPolicy != "denied" ||
		adapters[0].CredentialPolicy != "none" || !adapters[0].Ready {
		t.Fatalf("Workspace Sandbox Command Runtime receipt is not exact: status=%d data=%+v",
			response.StatusCode, envelope.Data)
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
