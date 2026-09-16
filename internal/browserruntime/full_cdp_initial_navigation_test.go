package browserruntime

import (
	"context"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

type initialNavigationStarter struct {
	*fakeBrowserProcessStarter
	t      *testing.T
	server *scriptedCDPServer
}

func (s *initialNavigationStarter) Start(ctx context.Context, spec BrowserStartSpec) (browserPlatformProcess, error) {
	process, err := s.fakeBrowserProcessStarter.Start(ctx, spec)
	if err == nil {
		writeDevToolsActivePort(s.t, spec.ProfilePath, s.server.port, s.server.path)
	}
	return process, err
}

func TestManagedFullCDPInitialNavigationUsesScopeBeforeReady(t *testing.T) {
	for _, mode := range []string{"target_path", "outside_scope", "transport_only"} {
		t.Run(mode, func(t *testing.T) {
			session, identity, acceptance, ownership, attempt, lease, review, permission := fullCDPLaunchFacts(t)
			execution, capabilities, fence := fullCDPExecutionFacts(t, session)
			initialURL := session.Scope.Origins[0].String() + "/nested/app?view=preview"
			server := newScriptedCDPServer(t, initialURL)
			t.Cleanup(func() { server.Close(t) })
			// The shared authorization fixture places its review one second in
			// the future for pure validation tests. A real launch compares the
			// authorization against the wall clock, so seal this review now.
			var err error
			review, err = BuildBrowserLaunchReview(session, identity, acceptance, ownership,
				attempt, lease, "initial-navigation-review", BrowserLaunchReviewAcceptCandidate,
				"independent-runtime-operator", "initial-navigation-review", "", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "outside_scope" {
				initialURL = "https://example.com/not-authorized"
			}
			if mode == "transport_only" {
				initialURL = ""
			}
			starter := &initialNavigationStarter{fakeBrowserProcessStarter: &fakeBrowserProcessStarter{}, t: t, server: server}
			controller, err := newBrowserProcessController(starter,
				func(BrowserExecutableIdentity, BrowserAcceptanceCandidate) error { return nil },
				&fakeBrowserNetworkContainmentFactory{available: true})
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := LaunchManagedFullCDP(t.Context(), controller, FullCDPManagedLaunchRequest{
				RuntimeID: "full-cdp-initial-navigation", InitialURL: initialURL, Session: session, Identity: identity,
				Acceptance: acceptance, Ownership: ownership, Attempt: attempt, LaunchLease: lease, Review: review,
				Permission: permission, ExecutionPermission: execution, ExecutionCapabilities: capabilities, ExecutionFence: fence,
				RuntimeCapabilities:    FullCDPRuntimeCapabilities{StartEnabled: true, DisposableProfileEnabled: true, TransportEnabled: true},
				PermissionCapabilities: domain.BrowserCDPPermissionRuntimeCapabilities{ControlEnabled: true, FullDebugEnabled: true},
				Confirmed:              true, Now: time.Now().UTC(),
			})
			if runtime == nil {
				t.Fatalf("managed owner missing: %v", err)
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			defer runtime.Close(closeCtx, "test completed")
			server.mu.Lock()
			urls := append([]string(nil), server.navigationURLs...)
			server.mu.Unlock()
			if mode == "outside_scope" {
				if err == nil || len(urls) != 0 {
					t.Fatalf("out-of-scope navigation reached CDP: urls=%v err=%v", urls, err)
				}
				receipt, closeErr := runtime.Close(closeCtx, "test failed navigation")
				if closeErr != nil || !receipt.CDPClosed || !receipt.ProcessTreeQuiescent || !receipt.ProfileCleaned ||
					receipt.Succeeded || receipt.FailureCode != "navigation_failed" {
					t.Fatalf("navigation failure did not retain cleanup evidence: receipt=%+v err=%v", receipt, closeErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "transport_only" {
				if len(urls) != 0 {
					t.Fatalf("legacy transport-only launch navigated: %v", urls)
				}
				return
			}
			if len(urls) != 1 || urls[0] != initialURL {
				t.Fatalf("explicit target was not navigated before launch returned: %v", urls)
			}
			snapshot, err := runtime.BrowserSnapshot(t.Context())
			if err != nil || snapshot.CanonicalURL != initialURL {
				t.Fatalf("initial target snapshot=%+v err=%v", snapshot, err)
			}
		})
	}
}
