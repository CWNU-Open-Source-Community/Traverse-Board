package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/modelregistry"
)

func TestControlPlaneWiresOrdinaryAgentBrowserIntoModelRequests(t *testing.T) {
	const model = "agent-browser-wiring-model"
	const providerSecret = "desktop-agent-browser-wiring-secret"
	qualificationNonce := regexp.MustCompile(`Call prayu_harness_echo exactly once with nonce ([0-9a-f]{32})\.`)

	var requestMu sync.Mutex
	modelRequests := make([]desktopSourceWiringRequest, 0, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" || request.Method != http.MethodPost {
			t.Errorf("unexpected Provider request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Header.Get("x-api-key") != providerSecret ||
			request.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Provider request omitted its credential or streaming contract")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(request.Body, 256*1024))
		if err != nil {
			t.Errorf("read Provider request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var captured desktopSourceWiringRequest
		if err := json.Unmarshal(raw, &captured); err != nil || !captured.Stream || captured.Model != model {
			t.Errorf("invalid Provider request body: %s err=%v", string(raw), err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		body := string(raw)
		switch {
		case strings.Contains(body, "Return exactly one JSON object with version model_harness_probe.v1"):
			nonce := regexp.MustCompile(`[0-9a-f]{32}`).FindString(body)
			if nonce == "" {
				t.Errorf("qualification result nonce was not found in %s", body)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			writeAnthropicTextSSE(t, writer, model, fmt.Sprintf(
				`{"version":"model_harness_probe.v1","status":"ok","nonce":"%s"}`, nonce))
		case strings.Contains(body, "Call prayu_harness_echo exactly once"):
			match := qualificationNonce.FindStringSubmatch(body)
			if len(match) != 2 {
				t.Errorf("qualification tool nonce was not found in %s", body)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			writeAnthropicToolSSE(t, writer, model, match[1])
		default:
			requestMu.Lock()
			modelRequests = append(modelRequests, captured)
			requestMu.Unlock()
			writeAnthropicTextSSE(t, writer, model,
				`{"version":"root_lifecycle.v1","action":"wait","message":"browser wiring observed","reason":"operator turn boundary"}`)
		}
	}))
	defer provider.Close()

	t.Setenv("CYBERAGENT_ANTHROPIC_API_KEY", providerSecret)
	t.Setenv("CYBERAGENT_ANTHROPIC_BASE_URL", provider.URL)
	t.Setenv("CYBERAGENT_ANTHROPIC_MODEL", model)
	permissionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:        true,
		DangerFullAccessEnabled:        true,
		FullAccessRequiresRuntimeGrant: true,
		RuntimeAuthority:               domain.NewExecutionPermissionRuntimeAuthority(),
	}
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "agent-browser-wiring.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunCreationEnabled: true, SessionMessageEnabled: true,
		RunLifecycleEnabled: true, RunExecutionEnabled: true, ModelControlEnabled: true,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities:   permissionCapabilities,
		CredentialStore:                   credential.NewMemoryStore(),
		AppVersion:                        "desktop-agent-browser-wiring-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()

	workspace, err := plane.RegisterWorkspaceDirectory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	harness := desktopControlRequest(plane.Handler(), http.MethodPost,
		httpapi.ModelHarnessQualificationPath, "desktop-agent-browser-harness-0001",
		fmt.Sprintf(`{"version":%q,"provider":"anthropic","model":%q,"confirm_qualification":true}`,
			modelregistry.HarnessQualificationProtocolVersion, model))
	if harness.Code != http.StatusAccepted {
		t.Fatalf("Harness qualification status=%d body=%s", harness.Code, harness.Body.String())
	}
	route := desktopControlRequest(plane.Handler(), http.MethodPost,
		"/api/v1/models/routes/code", "desktop-agent-browser-route-0001",
		fmt.Sprintf(`{"version":%q,"provider":"anthropic","model":%q}`,
			modelregistry.RouteControlProtocolVersion, model))
	if route.Code != http.StatusAccepted {
		t.Fatalf("route selection status=%d body=%s", route.Code, route.Body.String())
	}

	fullThread := createDesktopSourceWiringThread(t, plane, workspace.ID,
		"Full Access ordinary browser wiring", "desktop-agent-browser-full-thread-0001")
	permission := desktopControlRequest(plane.Handler(), http.MethodPost,
		"/api/v1/threads/"+fullThread.Thread.ID+"/execution-permission",
		"desktop-agent-browser-full-permission-0001",
		`{"mode":"full_access","reason":"inspect ordinary browser tools","confirm_danger_full_access":true}`)
	if permission.Code != http.StatusAccepted {
		t.Fatalf("Full Access selection status=%d body=%s", permission.Code, permission.Body.String())
	}
	completeDesktopSourceWiringTurn(t, plane, fullThread,
		"Inspect the ordinary browser tools available to this Run",
		"desktop-agent-browser-full-turn-0001")

	restrictedThread := createDesktopSourceWiringThread(t, plane, workspace.ID,
		"Conservative ordinary browser boundary", "desktop-agent-browser-restricted-thread-0001")
	completeDesktopSourceWiringTurn(t, plane, restrictedThread,
		"Inspect tools without elevated execution permission",
		"desktop-agent-browser-restricted-turn-0001")

	requestMu.Lock()
	captured := append([]desktopSourceWiringRequest(nil), modelRequests...)
	requestMu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("Supervisor model request count=%d, want one Full Access and one conservative request", len(captured))
	}
	fullTools := desktopSourceWiringToolsByName(captured[0].Tools)
	restrictedTools := desktopSourceWiringToolsByName(captured[1].Tools)
	for _, name := range []string{"browser_navigate", "browser_scroll", "browser_key"} {
		_, fullFound := fullTools[name]
		_, restrictedFound := restrictedTools[name]
		if runtime.GOOS == "windows" && !fullFound {
			t.Fatalf("Windows Full Access Provider tools omitted %s: %v", name,
				desktopSourceWiringToolNames(captured[0].Tools))
		}
		if runtime.GOOS != "windows" && fullFound {
			t.Fatalf("non-Windows host advertised unavailable ordinary browser tool %s", name)
		}
		if restrictedFound {
			t.Fatalf("conservative Run received ordinary browser tool %s: %v", name,
				desktopSourceWiringToolNames(captured[1].Tools))
		}
	}
	if runtime.GOOS != "windows" {
		return
	}

	statusResponse := desktopAPIRequest(plane.Handler(),
		"/api/v1/runs/"+fullThread.Run.ID+"/agent-browser")
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("Agent browser status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	var status httpapi.AgentBrowserStatusView
	decodeDesktopControlData(t, statusResponse, &status)
	if status.SessionID == "" || status.State != "idle" {
		t.Fatalf("model request did not create an observable idle Agent browser session: %#v", status)
	}
	closedResponse := desktopControlRequest(plane.Handler(), http.MethodPost,
		"/api/v1/runs/"+fullThread.Run.ID+"/agent-browser/close",
		"desktop-agent-browser-close-0001",
		fmt.Sprintf(`{"version":"agent_browser_close.v1","session_id":%q}`, status.SessionID))
	if closedResponse.Code != http.StatusOK {
		t.Fatalf("Agent browser close status=%d body=%s", closedResponse.Code, closedResponse.Body.String())
	}
	var closed httpapi.AgentBrowserStatusView
	decodeDesktopControlData(t, closedResponse, &closed)
	if closed.State != "closed" || !closed.TreeReaped || !closed.ProfileRemoved || closed.CleanupPending {
		t.Fatalf("idle Agent browser session did not close cleanly: %#v", closed)
	}
}
