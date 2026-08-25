//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/httpapi"
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

func TestWindowsControlPlaneConsumesValidatedLocalSandboxReadiness(t *testing.T) {
	base, err := sandboxtest.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend, err := sandbox.NewPlatformLocalBackend(sandbox.WithLocalOwnerRoot(
		filepath.Clean(filepath.Join(base, "owners"))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	readiness, err := backend.Readiness(context.Background(),
		sandbox.LocalRuntimeCapabilities{Enabled: true})
	if err != nil || !readiness.Ready {
		t.Fatalf("Local Sandbox readiness=%#v err=%v", readiness, err)
	}
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(base, "desktop-local-readiness.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunControlEnabled: true, ExecutionPermissionControlEnabled: true,
		RunExecutionEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		},
		LocalSandboxReadiness: &readiness, LocalSandboxBackend: backend,
		AppVersion: "desktop-local-readiness-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plane.Close() })
	_, run, err := application.NewRunService(plane.stateStore).Create(t.Context(),
		application.CreateRunRequest{Goal: "project verified Local readiness", Profile: "code",
			Surface: "code", Phase: "deliver", Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	response := desktopAPIRequest(plane.Handler(), "/api/v1/runs/"+run.ID+
		"/capability-readiness")
	if response.Code != http.StatusOK {
		t.Fatalf("Desktop readiness status=%d body=%s", response.Code,
			response.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var projection httpapi.RunCapabilityReadinessView
	if err := json.Unmarshal(envelope.Data, &projection); err != nil {
		t.Fatal(err)
	}
	workspace := projection.Permissions[1]
	if workspace.Value != string(domain.RunExecutionPermissionWorkspaceAccess) ||
		!workspace.Selectable || !workspace.RuntimeAvailable || projection.CapabilityGrant {
		t.Fatalf("Desktop did not consume Local readiness: %#v", workspace)
	}
	if !plane.StandardCodePresetEnabled() {
		t.Fatal("Desktop did not expose the Go-owned Standard Code preset")
	}
	capabilityResponse := desktopAPIRequest(plane.Handler(), "/api/v1/capabilities")
	if capabilityResponse.Code != http.StatusOK {
		t.Fatalf("Desktop runtime capabilities status=%d body=%s",
			capabilityResponse.Code, capabilityResponse.Body.String())
	}
	var capabilityEnvelope desktopAPIEnvelope
	if err := json.Unmarshal(capabilityResponse.Body.Bytes(), &capabilityEnvelope); err != nil {
		t.Fatal(err)
	}
	var capabilities httpapi.RuntimeCapabilitiesView
	if err := json.Unmarshal(capabilityEnvelope.Data, &capabilities); err != nil {
		t.Fatal(err)
	}
	if !capabilities.StandardCodePresetEnabled {
		t.Fatalf("Desktop omitted Standard Code from runtime capabilities: %#v", capabilities)
	}
}
