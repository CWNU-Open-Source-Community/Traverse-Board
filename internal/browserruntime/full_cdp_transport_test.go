package browserruntime

import (
	"context"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestFullCDPQueuedOperationRechecksRevokeRunBeforeClose(t *testing.T) {
	session, identity, acceptance, ownership, attempt, launchLease, review,
		permission := fullCDPLaunchFacts(t)
	executionPermission, executionCapabilities, executionFence :=
		fullCDPExecutionFacts(t, session)
	runtimeCapabilities := FullCDPRuntimeCapabilities{
		StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true,
	}
	permissionCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: true, FullDebugEnabled: true,
	}
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
		permission, executionPermission, executionCapabilities, profileLease,
		issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := process.Stop(stopContext); err != nil {
			t.Errorf("stop fake Full CDP browser: %v", err)
		}
	})

	server := newScriptedCDPServer(t, session.Scope.Origins[0].String())
	t.Cleanup(func() { server.Close(t) })
	writeDevToolsActivePort(t, profileLease.DirectoryPath, server.port, server.path)
	authorization, err := AuthorizeFullCDP(session, identity, acceptance,
		permission, executionPermission, runtimeCapabilities, permissionCapabilities,
		executionCapabilities, executionFence, true, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	fullSession, err := OpenFullCDPSession(t.Context(), authorization, session,
		identity, acceptance, ownership, permission, executionPermission,
		executionCapabilities, executionFence, profileLease, process)
	if err != nil {
		t.Fatal(err)
	}

	// Hold the transport token so both the sensitive operation and Close must
	// queue. Revocation happens after the operation's fast admission check but
	// before it can acquire the token.
	<-fullSession.operation
	operationStarted := make(chan struct{})
	operationResult := make(chan error, 1)
	go func() {
		close(operationStarted)
		_, operationErr := fullSession.CookieAccess(context.Background())
		operationResult <- operationErr
	}()
	<-operationStarted
	select {
	case operationErr := <-operationResult:
		t.Fatalf("queued operation returned before token release: %v", operationErr)
	case <-time.After(20 * time.Millisecond):
	}

	executionCapabilities.RuntimeAuthority.RevokeRun(session.RunID)
	closeStarted := make(chan struct{})
	closeResult := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closeResult <- fullSession.Close(closeContext)
	}()
	<-closeStarted
	select {
	case closeErr := <-closeResult:
		t.Fatalf("Close returned before token release: %v", closeErr)
	case <-time.After(20 * time.Millisecond):
	}

	methodCountBeforeRelease := server.MethodCount()
	fullSession.operation <- struct{}{}
	select {
	case operationErr := <-operationResult:
		if operationErr == nil {
			t.Fatalf("queued operation did not observe revocation/Close: %v", operationErr)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation did not terminate after revocation")
	}
	select {
	case closeErr := <-closeResult:
		if closeErr != nil {
			t.Fatalf("Close after queued revocation: %v", closeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not terminate after queued operation was rejected")
	}
	if got := server.MethodCount(); got != methodCountBeforeRelease+2 {
		// Close emits only Target.closeTarget and Target.disposeBrowserContext.
		// A third method means the stale Network.getCookies operation crossed the
		// post-queue revocation boundary.
		t.Fatalf("queued revoked operation reached CDP transport: methods before=%d after=%d",
			methodCountBeforeRelease, got)
	}
}
