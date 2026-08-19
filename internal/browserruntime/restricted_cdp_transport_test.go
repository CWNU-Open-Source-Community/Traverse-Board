package browserruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRestrictedCDPNavigatesInspectsAndScreenshotsWithoutArbitraryMethods(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	profileLease := facts.materialize(t)
	allowedURL := "http://127.0.0.1:18080/page"
	server := newScriptedCDPServer(t, allowedURL)
	defer server.Close(t)
	writeDevToolsActivePort(t, profileLease.DirectoryPath, server.port,
		server.path)
	process := startFakeBrowserProcess(t, facts, profileLease)
	defer func() { _ = process.Stop(context.Background()) }()
	authorization, err := AuthorizeRestrictedCDP(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission,
		ProductionRuntimeCapabilities{SafeWebStartEnabled: true,
			DisposableProfileEnabled: true, NetworkContainmentEnabled: true,
			RestrictedCDPEnabled: true}, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenRestrictedBrowserSession(t.Context(), authorization,
		facts.authorization, facts.session, facts.identity, facts.acceptance,
		facts.ownership, facts.attempt, facts.launchLease, facts.review,
		facts.networkEvidence, facts.networkReview, facts.networkPlan,
		facts.permission, profileLease, process)
	if err != nil {
		t.Fatal(err)
	}

	navigation, err := runtime.Navigate(t.Context(), allowedURL)
	if err != nil {
		t.Fatal(err)
	}
	if navigation.CanonicalURL != allowedURL || navigation.AllowedRequests != 1 ||
		navigation.BlockedRequests != 1 || !navigation.ScopeValidated ||
		!navigation.RedirectsValidated || !navigation.UntrustedEvidence ||
		navigation.Fingerprint != browserRuntimeFingerprint(navigation) {
		t.Fatalf("unexpected restricted navigation: %#v", navigation)
	}
	before := server.MethodCount()
	if _, err := runtime.Navigate(t.Context(), "https://example.com/"); err == nil {
		t.Fatal("out-of-scope navigation unexpectedly reached CDP")
	}
	if server.MethodCount() != before {
		t.Fatal("out-of-scope navigation emitted a CDP method")
	}

	metadata, err := runtime.DOMMetadata(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.CanonicalURL != allowedURL || metadata.RootNodeName != "#document" ||
		metadata.RootChildCount != 2 || metadata.ElementCount != 42 ||
		metadata.FormCount != 2 || metadata.ScriptCount != 3 ||
		metadata.ViewportWidth != 1280 || metadata.ViewportHeight != 720 ||
		metadata.TextIncluded || metadata.InstructionsUsed || !metadata.UntrustedEvidence ||
		metadata.Fingerprint != browserRuntimeFingerprint(metadata) {
		t.Fatalf("unexpected restricted DOM metadata: %#v", metadata)
	}
	screenshot, err := runtime.Screenshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if screenshot.CanonicalURL != allowedURL || screenshot.MediaType != "image/png" ||
		screenshot.Bytes != len(server.png) || string(screenshot.PNG) != string(server.png) ||
		!screenshot.UntrustedEvidence || screenshot.Fingerprint != browserRuntimeFingerprint(screenshot) {
		t.Fatalf("unexpected restricted screenshot: %#v", screenshot)
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	methods := server.Methods()
	for _, required := range []string{"Fetch.continueRequest", "Fetch.failRequest",
		"Network.setBypassServiceWorker", "Browser.setDownloadBehavior",
		"DOM.performSearch", "Page.captureScreenshot"} {
		if !containsString(methods, required) {
			t.Fatalf("required fixed CDP method %s was not used: %v", required, methods)
		}
	}
	for _, forbidden := range []string{"Runtime.evaluate", "Runtime.callFunctionOn",
		"Network.getAllCookies", "Network.getResponseBody", "Fetch.getResponseBody",
		"Storage.getCookies"} {
		if containsString(methods, forbidden) {
			t.Fatalf("forbidden CDP method %s was used: %v", forbidden, methods)
		}
	}
}

func TestRestrictedCDPRejectsMalformedEndpointFile(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	profileLease := facts.materialize(t)
	process := startFakeBrowserProcess(t, facts, profileLease)
	defer func() { _ = process.Stop(context.Background()) }()
	authorization, err := AuthorizeRestrictedCDP(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission,
		ProductionRuntimeCapabilities{SafeWebStartEnabled: true,
			DisposableProfileEnabled: true, NetworkContainmentEnabled: true,
			RestrictedCDPEnabled: true}, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profileLease.DirectoryPath, DevToolsActivePortFileName)
	if err := os.WriteFile(path,
		[]byte("9222\n/devtools/browser/test\nextra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRestrictedBrowserSession(t.Context(), authorization,
		facts.authorization, facts.session, facts.identity, facts.acceptance,
		facts.ownership, facts.attempt, facts.launchLease, facts.review,
		facts.networkEvidence, facts.networkReview, facts.networkPlan,
		facts.permission, profileLease, process); err == nil {
		t.Fatal("malformed DevTools endpoint unexpectedly opened a CDP session")
	}
}

func TestRestrictedCDPRejectsIndirectEndpointFile(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	profileLease := facts.materialize(t)
	process := startFakeBrowserProcess(t, facts, profileLease)
	defer func() { _ = process.Stop(context.Background()) }()
	authorization, err := AuthorizeRestrictedCDP(facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission,
		ProductionRuntimeCapabilities{SafeWebStartEnabled: true,
			DisposableProfileEnabled: true, NetworkContainmentEnabled: true,
			RestrictedCDPEnabled: true}, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profileLease.DirectoryPath, DevToolsActivePortFileName)
	if err := os.Remove(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "foreign-devtools-port")
	if err := os.WriteFile(target, []byte("9222\n/devtools/browser/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symbolic-link creation is unavailable for this test process: %v", err)
	}
	if _, err := OpenRestrictedBrowserSession(t.Context(), authorization,
		facts.authorization, facts.session, facts.identity, facts.acceptance,
		facts.ownership, facts.attempt, facts.launchLease, facts.review,
		facts.networkEvidence, facts.networkReview, facts.networkPlan,
		facts.permission, profileLease, process); err == nil {
		t.Fatal("indirect DevTools endpoint unexpectedly opened a CDP session")
	}
}

func TestRestrictedCDPMethodSetRemainsClosed(t *testing.T) {
	want := map[string]restrictedCDPMethodScope{
		"Browser.setDownloadBehavior":    restrictedCDPBrowserMethod,
		"Target.attachToTarget":          restrictedCDPBrowserMethod,
		"Target.closeTarget":             restrictedCDPBrowserMethod,
		"Target.createBrowserContext":    restrictedCDPBrowserMethod,
		"Target.createTarget":            restrictedCDPBrowserMethod,
		"Target.disposeBrowserContext":   restrictedCDPBrowserMethod,
		"DOM.discardSearchResults":       restrictedCDPTargetMethod,
		"DOM.enable":                     restrictedCDPTargetMethod,
		"DOM.getDocument":                restrictedCDPTargetMethod,
		"DOM.performSearch":              restrictedCDPTargetMethod,
		"Fetch.continueRequest":          restrictedCDPTargetMethod,
		"Fetch.enable":                   restrictedCDPTargetMethod,
		"Fetch.failRequest":              restrictedCDPTargetMethod,
		"Network.enable":                 restrictedCDPTargetMethod,
		"Network.setBlockedURLs":         restrictedCDPTargetMethod,
		"Network.setBypassServiceWorker": restrictedCDPTargetMethod,
		"Network.setCacheDisabled":       restrictedCDPTargetMethod,
		"Page.captureScreenshot":         restrictedCDPTargetMethod,
		"Page.enable":                    restrictedCDPTargetMethod,
		"Page.getLayoutMetrics":          restrictedCDPTargetMethod,
		"Page.navigate":                  restrictedCDPTargetMethod,
	}
	if len(restrictedCDPMethods) != len(want) {
		t.Fatalf("restricted CDP method set widened: %#v", restrictedCDPMethods)
	}
	for method, wantScope := range want {
		if scope, ok := restrictedCDPMethods[method]; !ok || scope != wantScope {
			t.Fatalf("restricted CDP method %q scope = %v, want %v", method, scope, wantScope)
		}
	}
}

func startFakeBrowserProcess(t *testing.T, facts browserRuntimeFacts,
	profileLease ProfileRuntimeLease,
) *BrowserProcess {
	t.Helper()
	controller, err := newBrowserProcessController(&fakeBrowserProcessStarter{},
		func(BrowserExecutableIdentity, BrowserAcceptanceCandidate) error { return nil },
		&fakeBrowserNetworkContainmentFactory{available: true})
	if err != nil {
		t.Fatal(err)
	}
	process, err := controller.Start(t.Context(), facts.authorization, facts.session,
		facts.identity, facts.acceptance, facts.ownership, facts.attempt,
		facts.launchLease, facts.review, facts.networkEvidence, facts.networkReview,
		facts.networkPlan, facts.permission, profileLease, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	return process
}

func writeDevToolsActivePort(t *testing.T, profilePath string, port int,
	path string,
) {
	t.Helper()
	raw := strconv.Itoa(port) + "\n" + path + "\n"
	if err := os.WriteFile(filepath.Join(profilePath, DevToolsActivePortFileName),
		[]byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

type scriptedCDPServer struct {
	listener        net.Listener
	server          *http.Server
	port            int
	path            string
	url             string
	png             []byte
	mu              sync.Mutex
	methods         []string
	selectorQueries map[string]int
}

func newScriptedCDPServer(t *testing.T, allowedURL string) *scriptedCDPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &scriptedCDPServer{listener: listener, path: "/devtools/browser/test",
		url: allowedURL, png: restrictedTestPNG(), selectorQueries: make(map[string]int)}
	server.port = listener.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc(server.path, server.serveWebSocket)
	server.server = &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = server.server.Serve(listener) }()
	return server
}

func (server *scriptedCDPServer) Close(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.server.Shutdown(ctx); err != nil {
		t.Errorf("shutdown fake CDP server: %v", err)
	}
}

func (server *scriptedCDPServer) Methods() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.methods...)
}

func (server *scriptedCDPServer) MethodCount() int { return len(server.Methods()) }

func (server *scriptedCDPServer) serveWebSocket(writer http.ResponseWriter,
	request *http.Request,
) {
	connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool {
		return true
	}}).Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	var pendingNavigationID int64
	navigationStage := 0
	performanceCalls := 0
	for {
		_, raw, err := connection.ReadMessage()
		if err != nil {
			return
		}
		var command struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Params    json.RawMessage `json:"params"`
		}
		if json.Unmarshal(raw, &command) != nil {
			return
		}
		server.mu.Lock()
		server.methods = append(server.methods, command.Method)
		server.mu.Unlock()
		switch command.Method {
		case "Target.createBrowserContext":
			writeCDPResult(connection, command.ID,
				map[string]any{"browserContextId": "context-test"})
		case "Target.createTarget":
			writeCDPResult(connection, command.ID,
				map[string]any{"targetId": "target-test"})
		case "Target.attachToTarget":
			writeCDPResult(connection, command.ID,
				map[string]any{"sessionId": "session-test"})
		case "Page.navigate":
			pendingNavigationID = command.ID
			navigationStage = 1
			writeCDPEvent(connection, "session-test", "Fetch.requestPaused", map[string]any{
				"requestId": "request-allowed", "resourceType": "Document",
				"request": map[string]any{"url": server.url},
			})
		case "Fetch.continueRequest":
			writeCDPResult(connection, command.ID, map[string]any{})
			if pendingNavigationID != 0 && navigationStage == 1 {
				navigationStage = 2
				writeCDPEvent(connection, "session-test", "Fetch.requestPaused", map[string]any{
					"requestId": "request-blocked", "resourceType": "Script",
					"request": map[string]any{"url": "https://203.0.113.10/payload.js"},
				})
			}
		case "Fetch.failRequest":
			writeCDPResult(connection, command.ID, map[string]any{})
			if pendingNavigationID != 0 && navigationStage == 2 {
				writeCDPResult(connection, pendingNavigationID,
					map[string]any{"frameId": "frame-test"})
				writeCDPEvent(connection, "session-test", "Page.loadEventFired",
					map[string]any{"timestamp": 1})
				writeCDPEvent(connection, "session-test", "Runtime.consoleAPICalled",
					map[string]any{"type": "warning", "timestamp": 1,
						"args": []map[string]any{{"type": "string", "value": "fixture warning"}}})
				writeCDPEvent(connection, "session-test", "Network.requestWillBeSent",
					map[string]any{"requestId": "network-test", "type": "XHR",
						"request": map[string]any{"url": server.url + "?token=hidden", "method": "GET"}})
				writeCDPEvent(connection, "session-test", "Network.responseReceived",
					map[string]any{"requestId": "network-test", "response": map[string]any{
						"url": server.url + "?token=hidden", "status": 500, "mimeType": "application/json"}})
				writeCDPEvent(connection, "session-test", "Network.loadingFinished",
					map[string]any{"requestId": "network-test"})
				pendingNavigationID = 0
			}
		case "DOM.getDocument":
			writeCDPResult(connection, command.ID, map[string]any{"root": map[string]any{
				"nodeId": 1, "nodeName": "#document", "childNodeCount": 2,
				"documentURL": server.url,
			}})
		case "DOM.performSearch":
			var params struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal(command.Params, &params)
			counts := map[string]int{"*": 42, "form": 2, "script": 3}
			writeCDPResult(connection, command.ID, map[string]any{
				"searchId":    "search-" + strings.ReplaceAll(params.Query, "*", "all"),
				"resultCount": counts[params.Query],
			})
		case "Page.getLayoutMetrics":
			writeCDPResult(connection, command.ID, map[string]any{
				"cssLayoutViewport": map[string]any{
					"clientWidth": 1280, "clientHeight": 720,
				},
			})
		case "Page.captureScreenshot":
			writeCDPResult(connection, command.ID, map[string]any{
				"data": base64.StdEncoding.EncodeToString(server.png),
			})
		case "DOM.querySelector":
			var params struct {
				Selector string `json:"selector"`
			}
			_ = json.Unmarshal(command.Params, &params)
			server.mu.Lock()
			server.selectorQueries[params.Selector]++
			queryCount := server.selectorQueries[params.Selector]
			server.mu.Unlock()
			nodeID := 0
			if params.Selector != "#absent" &&
				(params.Selector != "#eventual" || queryCount >= 3) {
				nodeID = 7
			}
			writeCDPResult(connection, command.ID, map[string]any{"nodeId": nodeID})
		case "DOM.getBoxModel":
			writeCDPResult(connection, command.ID, map[string]any{"model": map[string]any{
				"border": []float64{2, 2, 6, 2, 6, 6, 2, 6}}})
		case "DOM.getOuterHTML":
			writeCDPResult(connection, command.ID, map[string]any{
				"outerHTML": `<html><main>token=abcdefghijklmnopqrstuvwxyz1234567890</main></html>`,
			})
		case "Accessibility.getFullAXTree":
			writeCDPResult(connection, command.ID, map[string]any{"nodes": []map[string]any{{
				"nodeId": "ax-1", "name": map[string]any{"value": "fixture"}}}})
		case "Performance.getMetrics":
			performanceCalls++
			if performanceCalls == 3 {
				writeCDPEvent(connection, "session-test", "Runtime.consoleAPICalled",
					map[string]any{"type": "warning", "timestamp": 2,
						"args": []map[string]any{{"type": "string", "value": "delayed warning"}}})
			}
			writeCDPResult(connection, command.ID, map[string]any{"metrics": []map[string]any{{
				"name": "LayoutCount", "value": 2}}})
		case "Target.closeTarget":
			writeCDPResult(connection, command.ID, map[string]any{"success": true})
		default:
			writeCDPResult(connection, command.ID, map[string]any{})
		}
	}
}

func restrictedTestPNG() []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, 10, 10))
	drawColor := color.RGBA{R: 240, G: 240, B: 240, A: 255}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			canvas.SetRGBA(x, y, drawColor)
		}
	}
	var output bytes.Buffer
	_ = png.Encode(&output, canvas)
	return output.Bytes()
}

func writeCDPResult(connection *websocket.Conn, id int64, result map[string]any) {
	_ = connection.WriteJSON(map[string]any{"id": id, "result": result})
}

func writeCDPEvent(connection *websocket.Conn, sessionID string, method string,
	params map[string]any,
) {
	_ = connection.WriteJSON(map[string]any{"method": method,
		"sessionId": sessionID, "params": params})
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
