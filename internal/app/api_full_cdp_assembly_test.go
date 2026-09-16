package app

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

func TestAPIFullCDPAssemblyProvidesSharedRevocationAuthority(t *testing.T) {
	capabilities := newAPIExecutionPermissionCapabilities(true, true, true)
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	if capabilities.RuntimeAuthority == nil || capabilities.FullAccessRequiresRuntimeGrant {
		t.Fatal("API lost its shared fence or changed the explicit startup permission gates")
	}
	// Copies supplied to sibling services must fence the same Run. Creating a
	// separate API process must neither reuse this authority nor activate grants.
	browserCapabilities, permissionCapabilities := capabilities, capabilities
	fence, err := browserCapabilities.RuntimeAuthority.IssueRunAuthorizationFence("run-api-preview")
	if err != nil {
		t.Fatal(err)
	}
	permissionCapabilities.RuntimeAuthority.RevokeRun("run-api-preview")
	if browserCapabilities.RuntimeAuthority.AllowsRunAuthorizationFence("run-api-preview", fence) {
		t.Fatal("permission revocation did not invalidate the browser fence")
	}
	fresh := newAPIExecutionPermissionCapabilities(true, true, true)
	if fresh.RuntimeAuthority == capabilities.RuntimeAuthority ||
		fresh.RuntimeAuthority.AllowsRunAuthorizationFence("run-api-preview", fence) {
		t.Fatal("an API restart inherited a runtime authority")
	}
	if _, granted := capabilities.RuntimeAuthority.AllowsFullAccess(domain.RunExecutionPermissionSnapshot{
		RunID: "run-api-preview", Mode: domain.RunExecutionPermissionFullAccess, OperatorConfirmed: true,
	}); granted {
		t.Fatal("creating the authority activated a Full Access grant")
	}

	controller, err := browserruntime.NewPlatformBrowserProcessController()
	if err != nil {
		t.Fatal(err)
	}
	if !controller.FullCDPAvailable() {
		t.Skip("this platform does not provide the Full CDP process adapter")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Clean(home)
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runtime := browserruntime.FullCDPRuntimeCapabilities{
		StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true,
	}
	browser := domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true, FullDebugEnabled: true}
	// This is the CLI's production constructor. It prepares only its private
	// profile root; no API listener, browser process, or Run is started here.
	service, err := application.NewHomeFullCDPProductionService(state, controller, runtime,
		browser, capabilities, home)
	if err != nil {
		t.Fatalf("enabled CLI Full CDP assembly failed before startup: %v", err)
	}
	defer service.Close(context.Background())
	for _, denied := range []domain.ExecutionPermissionRuntimeCapabilities{
		newAPIExecutionPermissionCapabilities(false, false, false),
		newAPIExecutionPermissionCapabilities(true, false, false),
		{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, DebugMaximumAccessEnabled: true},
	} {
		if _, err := application.NewHomeFullCDPProductionService(state, controller, runtime,
			browser, denied, home); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("missing startup gate or runtime authority was accepted: %v", err)
		}
	}
}
