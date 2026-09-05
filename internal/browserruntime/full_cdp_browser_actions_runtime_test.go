package browserruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func openBrowserActionFullCDPSession(t *testing.T) (*FullCDPSession,
	*scriptedCDPServer, domain.ExecutionPermissionRuntimeCapabilities, uint64,
) {
	t.Helper()
	session, identity, acceptance, ownership, attempt, launchLease, review,
		permission := fullCDPLaunchFacts(t)
	executionPermission, executionCapabilities, executionFence :=
		fullCDPExecutionFacts(t, session)
	runtimeCapabilities := FullCDPRuntimeCapabilities{
		StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true}
	issuedAt := review.CreatedAt.Add(time.Millisecond)
	startAuthorization, err := AuthorizeFullCDPStart(session, identity, acceptance,
		ownership, attempt, launchLease, review, permission, executionPermission,
		runtimeCapabilities, permissionCapabilities, executionCapabilities,
		executionFence, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	profileLease, err := MaterializeFullCDPProfile(startAuthorization, session,
		identity, acceptance, ownership, attempt, launchLease, review, permission,
		executionPermission, executionCapabilities, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := newBrowserProcessController(&fakeBrowserProcessStarter{},
		func(BrowserExecutableIdentity, BrowserAcceptanceCandidate) error { return nil },
		&fakeBrowserNetworkContainmentFactory{available: true})
	if err != nil {
		t.Fatal(err)
	}
	process, err := controller.StartFullCDP(t.Context(), startAuthorization,
		session, identity, acceptance, ownership, attempt, launchLease, review,
		permission, executionPermission, executionCapabilities, profileLease, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	server := newScriptedCDPServer(t, session.Scope.Origins[0].String())
	writeDevToolsActivePort(t, profileLease.DirectoryPath, server.port, server.path)
	authorization, err := AuthorizeFullCDP(session, identity, acceptance,
		permission, executionPermission, runtimeCapabilities, permissionCapabilities,
		executionCapabilities, executionFence, true, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenFullCDPSession(t.Context(), authorization, session,
		identity, acceptance, ownership, permission, executionPermission,
		executionCapabilities, executionFence, profileLease, process)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Close(closeContext)
		_ = process.Stop(closeContext)
		server.Close(t)
	})
	return runtime, server, executionCapabilities, executionFence
}

func setBrowserActionDocument(server *scriptedCDPServer, outer string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.outerHTML = outer
	server.outerSequence = nil
	server.outerCalls = 0
	server.loaderID = "loader-test"
	server.elementNodeName = "INPUT"
	server.elementAttrs = []string{"id", "search", "type", "text"}
	server.backendNodeID = 70
	server.mutateLoaderOnInput = false
}

func TestFullCDPSelectorRequiresLatestStableSnapshotProvenance(t *testing.T) {
	runtime, server, _, _ := openBrowserActionFullCDPSession(t)
	const document = `<html><body><input id="search" type="text" aria-label="Search"></body></html>`
	setBrowserActionDocument(server, document)

	before := server.MethodCount()
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "first"); err == nil {
		t.Fatal("selector without a snapshot provenance record was accepted")
	}
	if containsString(server.Methods()[before:], "Input.insertText") {
		t.Fatal("unprovenanced selector reached Input.insertText")
	}
	snapshot, err := runtime.SnapshotFullCDP(t.Context())
	if err != nil || len(snapshot.Elements) != 1 || snapshot.Elements[0].Selector != "#search" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "second"); err != nil {
		t.Fatalf("snapshotted input was rejected: %v", err)
	}
	before = server.MethodCount()
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "third"); err == nil {
		t.Fatal("selector provenance survived a mutating action")
	}
	if containsString(server.Methods()[before:], "Input.insertText") {
		t.Fatal("expired selector provenance reached Input.insertText")
	}

	setBrowserActionDocument(server, document)
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.outerHTML = strings.Replace(document, "Search", "Changed", 1)
	server.mu.Unlock()
	before = server.MethodCount()
	if _, err := runtime.ClickFullCDP(t.Context(), "#search"); err == nil {
		t.Fatal("selector provenance survived same-URL DOM drift")
	}
	if containsString(server.Methods()[before:], "Input.dispatchMouseEvent") {
		t.Fatal("same-URL DOM drift reached mouse dispatch")
	}

	setBrowserActionDocument(server, document)
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.backendNodeID++
	server.mu.Unlock()
	before = server.MethodCount()
	if _, err := runtime.ClickFullCDP(t.Context(), "#search"); err == nil {
		t.Fatal("selector provenance survived an identical-DOM node replacement")
	}
	if containsString(server.Methods()[before:], "Input.dispatchMouseEvent") {
		t.Fatal("replaced selector target reached mouse dispatch")
	}

	setBrowserActionDocument(server, document)
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.elementAttrs = []string{"id", "search", "type", "text", "readonly", ""}
	server.mu.Unlock()
	before = server.MethodCount()
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "readonly"); err == nil {
		t.Fatal("readonly input target was accepted")
	}
	if containsString(server.Methods()[before:], "Input.insertText") {
		t.Fatal("readonly target reached Input.insertText")
	}
}

func TestFullCDPObservationsRejectDocumentTOCTOU(t *testing.T) {
	runtime, server, _, _ := openBrowserActionFullCDPSession(t)
	const first = `<html><body><input id="search" type="text"></body></html>`
	const second = `<html><body><input id="changed" type="text"></body></html>`
	setBrowserActionDocument(server, first)
	server.mu.Lock()
	server.outerSequence = []string{first, second}
	server.mu.Unlock()
	if _, err := runtime.SnapshotFullCDP(t.Context()); err == nil {
		t.Fatal("snapshot published across same-URL DOM drift")
	}

	setBrowserActionDocument(server, first)
	server.mu.Lock()
	server.outerSequence = []string{first, second}
	server.mu.Unlock()
	if _, err := runtime.ScreenshotFullCDP(t.Context()); err == nil {
		t.Fatal("screenshot published across same-URL DOM drift")
	}

	setBrowserActionDocument(server, first)
	if _, err := runtime.SnapshotFullCDP(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.mutateLoaderOnInput = true
	server.mu.Unlock()
	if _, err := runtime.TypeFullCDP(t.Context(), "#search", "navigate"); err == nil {
		t.Fatal("type result published after its document loader changed")
	}
}

func TestFullCDPInFlightActionIsInterruptedWhenFenceRotates(t *testing.T) {
	runtime, server, capabilities, _ := openBrowserActionFullCDPSession(t)
	setBrowserActionDocument(server,
		`<html><body><input id="search" type="text"></body></html>`)
	entered := make(chan struct{})
	release := make(chan struct{})
	server.mu.Lock()
	server.blockMethod = "Page.captureScreenshot"
	server.blockEntered = entered
	server.blockRelease = release
	server.mu.Unlock()
	result := make(chan error, 1)
	go func() {
		_, err := runtime.ScreenshotFullCDP(context.Background())
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("screenshot did not reach the blocked CDP call")
	}
	if _, err := capabilities.RuntimeAuthority.RotateRunAuthorizationFence(
		runtime.session.RunID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("in-flight action succeeded after its fence rotated")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("in-flight CDP read was not actively interrupted after revocation")
	}
	close(release)
}
