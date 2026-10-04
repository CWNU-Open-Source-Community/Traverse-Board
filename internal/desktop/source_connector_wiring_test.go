package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/modelregistry"
)

type desktopSourceWiringTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type desktopSourceWiringRequest struct {
	Model    string                    `json:"model"`
	Stream   bool                      `json:"stream"`
	Messages []json.RawMessage         `json:"messages"`
	Tools    []desktopSourceWiringTool `json:"tools"`
}

func TestControlPlaneWiresDefaultSourceConnectorsIntoCurrentRunTools(t *testing.T) {
	const model = "source-wiring-model"
	const providerSecret = "desktop-source-wiring-secret"
	qualificationFixture := &anthropicHarnessFixture{}

	var requestMu sync.Mutex
	modelRequests := make([]desktopSourceWiringRequest, 0, 3)
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
		if err := json.Unmarshal(raw, &captured); err != nil || !captured.Stream ||
			captured.Model != model {
			t.Errorf("invalid Provider request body: %s err=%v", string(raw), err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}

		phase, nonce, err := qualificationFixture.phase(raw)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		switch phase {
		case "tool_result":
			writeAnthropicTextSSE(t, writer, model, fmt.Sprintf(
				`{"version":"model_harness_probe.v1","status":"ok","nonce":"%s"}`, nonce))
		case "tool_call":
			writeAnthropicToolSSE(t, writer, model, nonce)
		default:
			requestMu.Lock()
			modelRequests = append(modelRequests, captured)
			requestMu.Unlock()
			writeAnthropicTextSSE(t, writer, model,
				`{"version":"root_lifecycle.v1","action":"wait","message":"source wiring observed","reason":"operator turn boundary"}`)
		}
	}))
	defer provider.Close()

	t.Setenv("CYBERAGENT_ANTHROPIC_API_KEY", providerSecret)
	t.Setenv("CYBERAGENT_ANTHROPIC_BASE_URL", provider.URL)
	t.Setenv("CYBERAGENT_ANTHROPIC_MODEL", model)
	permissionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true,
		DangerFullAccessEnabled: true,
		RuntimeAuthority:        domain.NewExecutionPermissionRuntimeAuthority(),
	}
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "source-wiring.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunCreationEnabled: true, SessionMessageEnabled: true,
		RunLifecycleEnabled: true, RunExecutionEnabled: true, ModelControlEnabled: true,
		ApprovalControlEnabled:            true,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities:   permissionCapabilities,
		CredentialStore:                   credential.NewMemoryStore(),
		AppVersion:                        "desktop-source-wiring-test",
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
		httpapi.ModelHarnessQualificationPath, "desktop-source-wiring-harness-0001",
		fmt.Sprintf(`{"version":%q,"provider":"anthropic","model":%q,"confirm_qualification":true}`,
			modelregistry.HarnessQualificationProtocolVersion, model))
	if harness.Code != http.StatusAccepted {
		t.Fatalf("Harness qualification status=%d body=%s", harness.Code, harness.Body.String())
	}
	var qualification httpapi.ModelHarnessQualificationView
	decodeDesktopControlData(t, harness, &qualification)
	if qualification.Status != modelregistry.HarnessDiagnosticQualified || qualification.ModelCalls != 2 || qualification.SyntheticToolCalls != 1 ||
		!qualification.Harness.RootEligible {
		t.Fatalf("Harness qualification did not become root eligible: %#v", qualification)
	}
	route := desktopControlRequest(plane.Handler(), http.MethodPost,
		"/api/v1/models/routes/code", "desktop-source-wiring-route-0001",
		fmt.Sprintf(`{"version":%q,"provider":"anthropic","model":%q}`,
			modelregistry.RouteControlProtocolVersion, model))
	if route.Code != http.StatusAccepted {
		t.Fatalf("route selection status=%d body=%s", route.Code, route.Body.String())
	}

	modes := []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull,
	}
	for _, mode := range modes {
		thread := createDesktopSourceWiringThread(t, plane, workspace.ID,
			string(mode)+" source connector wiring", "desktop-source-wiring-"+string(mode)+"-thread-0001")
		if mode != domain.RunExecutionPermissionAsk {
			permission := desktopControlRequest(plane.Handler(), http.MethodPost,
				"/api/v1/threads/"+thread.Thread.ID+"/execution-permission",
				"desktop-source-wiring-"+string(mode)+"-permission-0001",
				fmt.Sprintf(`{"mode":%q,"reason":"exercise configured web tools","confirm_full":%t}`,
					mode, mode == domain.RunExecutionPermissionFull))
			if permission.Code != http.StatusAccepted {
				t.Fatalf("%s selection status=%d body=%s", mode, permission.Code, permission.Body.String())
			}
			var selected httpapi.ThreadExecutionPermissionControlView
			decodeDesktopControlData(t, permission, &selected)
			if selected.CurrentRunID != thread.Run.ID ||
				selected.CurrentRunEffect != string(domain.ThreadExecutionPermissionApplied) ||
				selected.CurrentRunMode != string(mode) ||
				!selected.CurrentRunSynchronized || !selected.ExecutionPermission.AppliesToCurrentRun {
				t.Fatalf("%s did not bind to the current Run: %#v", mode, selected)
			}
		}
		assertDesktopCurrentApproval(t, plane, permissionCapabilities, thread.Run.ID, mode)
		completeDesktopSourceWiringTurn(t, plane, thread,
			"Inspect configured web tools with exact fetch approval",
			"desktop-source-wiring-"+string(mode)+"-turn-0001")
	}

	requestMu.Lock()
	capturedRequests := append([]desktopSourceWiringRequest(nil), modelRequests...)
	requestMu.Unlock()
	if len(capturedRequests) != len(modes) {
		t.Fatalf("Supervisor model request count=%d, want one Ask, Auto, and Full request",
			len(capturedRequests))
	}
	for index, mode := range modes {
		tools := desktopSourceWiringToolsByName(capturedRequests[index].Tools)
		sourceSearch, found := tools["source_search"]
		if !found {
			t.Fatalf("%s Provider tools omitted source_search: %v", mode,
				desktopSourceWiringToolNames(capturedRequests[index].Tools))
		}
		if !strings.Contains(sourceSearch.Description, "GitHub") ||
			!strings.Contains(sourceSearch.Description, "Hacker News") {
			t.Fatalf("%s source_search description omitted default platform coverage: %q", mode,
				sourceSearch.Description)
		}
		webFetch, found := tools["web_fetch"]
		if !found {
			t.Fatalf("%s Provider tools omitted web_fetch: %v", mode,
				desktopSourceWiringToolNames(capturedRequests[index].Tools))
		}
		webFetchSchema := string(webFetch.InputSchema)
		for _, expected := range []string{`"connector"`, `"rss"`, `"max_items"`} {
			if !strings.Contains(webFetchSchema, expected) {
				t.Fatalf("%s web_fetch input schema omitted %s: %s", mode, expected, webFetchSchema)
			}
		}
	}
}

func createDesktopSourceWiringThread(t *testing.T, plane *ControlPlane,
	workspaceID string, goal string, operationKey string,
) httpapi.ThreadCreationControlView {
	t.Helper()
	response := desktopControlRequest(plane.Handler(), http.MethodPost, "/api/v1/threads",
		operationKey, fmt.Sprintf(`{"version":%q,"goal":%q,"workspace_id":%q,`+
			`"profile":"code","surface":"code","phase":"deliver"}`,
			domain.ThreadCreationProtocolVersion, goal, workspaceID))
	if response.Code != http.StatusAccepted {
		t.Fatalf("Thread creation status=%d body=%s", response.Code, response.Body.String())
	}
	var created httpapi.ThreadCreationControlView
	decodeDesktopControlData(t, response, &created)
	return created
}

func completeDesktopSourceWiringTurn(t *testing.T, plane *ControlPlane,
	thread httpapi.ThreadCreationControlView, content string, operationKey string,
) {
	t.Helper()
	response := desktopControlRequest(plane.Handler(), http.MethodPost,
		"/api/v1/threads/"+thread.Thread.ID+"/turns", operationKey,
		fmt.Sprintf(`{"version":"thread_message_submission.v1","content":%q}`, content))
	if response.Code != http.StatusAccepted {
		t.Fatalf("Thread turn status=%d body=%s", response.Code, response.Body.String())
	}
	var executed httpapi.ThreadMessageControlView
	decodeDesktopControlData(t, response, &executed)
	if !executed.ExecutionStarted || !executed.ModelCalled || executed.ToolCalled ||
		executed.RunID != thread.Run.ID ||
		executed.Steering.Status != string(domain.OperatorSteeringCommitted) {
		t.Fatalf("Desktop Thread did not complete one model-only turn: %#v", executed)
	}
}

func desktopSourceWiringToolsByName(tools []desktopSourceWiringTool) map[string]desktopSourceWiringTool {
	indexed := make(map[string]desktopSourceWiringTool, len(tools))
	for _, tool := range tools {
		indexed[tool.Name] = tool
	}
	return indexed
}

func desktopSourceWiringToolNames(tools []desktopSourceWiringTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// The current writer must bind the v2 preference to this process's authority.
// Historical Full Access/Debug rows cannot stand in for this explicit action.
func assertDesktopCurrentApproval(t *testing.T, plane *ControlPlane,
	capabilities domain.ExecutionPermissionRuntimeCapabilities, runID string,
	want domain.RunExecutionPermissionMode,
) {
	t.Helper()
	permission, err := plane.stateStore.GetRunExecutionPermission(t.Context(), runID)
	if err != nil || permission.ProtocolVersion != domain.RunApprovalPermissionProtocolVersion ||
		permission.Mode != want || permission.ProcessEnabled || permission.ExecutionAuthorized ||
		permission.CapabilityGrant {
		t.Fatalf("current Desktop approval=%#v want=%s err=%v", permission, want, err)
	}
	if capabilities.RuntimeAuthority == nil {
		t.Fatal("Desktop fixture omitted the shared runtime authority")
	}
	_, activated := capabilities.RuntimeAuthority.AllowsFullAccess(permission)
	if activated != (want == domain.RunExecutionPermissionFull) {
		t.Fatalf("Desktop approval activation=%t want mode=%s", activated, want)
	}
}
