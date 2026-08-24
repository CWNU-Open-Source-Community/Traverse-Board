package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/store"
)

const desktopControlPlaneTestToken = "desktop-control-plane-read-token-0123456789"
const desktopControlPlaneControlToken = "desktop-control-plane-control-token-012345"

type desktopAPIEnvelope struct {
	Version   string          `json:"version"`
	RequestID string          `json:"request_id"`
	Data      json.RawMessage `json:"data"`
}

func TestControlPlaneResolvesOnlyRegisteredWorkspaceRoots(t *testing.T) {
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "workspace-open.db"),
		ReadToken:    desktopControlPlaneTestToken, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	root := t.TempDir()
	record := store.WorkspaceRecord{ID: "workspace-desktop-open", Name: "desktop-open",
		RootPath: root}
	if err := plane.stateStore.SaveWorkspace(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	target, err := plane.ResolveWorkspace(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != record.ID || target.Name != record.Name || target.RootPath != root {
		t.Fatalf("unexpected Workspace target: %#v", target)
	}
	if _, err := plane.ResolveWorkspace(t.Context(), "missing-workspace"); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("missing Workspace error = %v, code = %s", err, apperror.CodeOf(err))
	}
	if _, err := plane.ResolveWorkspace(t.Context(), "bad workspace"); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("invalid Workspace error = %v, code = %s", err, apperror.CodeOf(err))
	}
}

func TestControlPlanePublishesExtensionInventoryToDesktopRenderer(t *testing.T) {
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "desktop-extensions.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		AppVersion: "desktop-extension-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plane.Close() })
	response := desktopAPIRequest(plane.Handler(), httpapi.ExtensionInventoryPath)
	if response.Code != http.StatusOK {
		t.Fatalf("extension inventory status=%d body=%s", response.Code,
			response.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var inventory httpapi.ExtensionInventoryView
	if err := json.Unmarshal(envelope.Data, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.ProtocolVersion != application.ExtensionInventoryProtocolVersion ||
		len(inventory.MCPServers) != 0 || len(inventory.Plugins) != 0 {
		t.Fatalf("unexpected Desktop extension inventory: %#v", inventory)
	}
}

func TestControlPlanePublishesGoOwnedRunCapabilityReadiness(t *testing.T) {
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "desktop-capability-readiness.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunControlEnabled: true, AppVersion: "desktop-readiness-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plane.Close() })
	_, run, err := application.NewRunService(plane.stateStore).Create(t.Context(),
		application.CreateRunRequest{Goal: "project Desktop readiness", Profile: "code",
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
	var readiness httpapi.RunCapabilityReadinessView
	if err := json.Unmarshal(envelope.Data, &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.ProtocolVersion != application.RunCapabilityReadinessProtocolVersion ||
		readiness.RunID != run.ID || readiness.CapabilityGrant ||
		!readiness.CommandRuntime.ProtocolAvailable ||
		readiness.CommandRuntime.AdapterInstalled ||
		readiness.CommandRuntime.AdapterReady ||
		readiness.CommandRuntime.CurrentRunGranted {
		t.Fatalf("unexpected Desktop readiness envelope: %#v", readiness)
	}
	var docker httpapi.CapabilityReadinessOptionView
	for _, option := range readiness.Profiles {
		if option.Value == string(domain.RunExecutionProfileDocker) {
			docker = option
			break
		}
	}
	if docker.Value == "" || !docker.Selectable || docker.RuntimeAvailable ||
		!containsDesktopReadinessValue(docker.BlockedBy,
			string(application.CapabilityBlockerDockerUnavailable)) {
		t.Fatalf("Desktop did not preserve selectable intent and unavailable runtime: %#v",
			docker)
	}
}

func containsDesktopReadinessValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestControlPlaneRegistersAnExistingWorkspaceDirectoryWithoutModifyingIt(t *testing.T) {
	home := t.TempDir()
	selected := filepath.Join(t.TempDir(), "selected-project")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(selected, "existing.txt")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(home, "workspace-import.db"), HomePath: home,
		ReadToken: desktopControlPlaneTestToken, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()

	registered, err := plane.RegisterWorkspaceDirectory(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	target, err := plane.ResolveWorkspace(t.Context(), registered.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedRoot, err := filepath.EvalSymlinks(selected)
	if err != nil {
		t.Fatal(err)
	}
	expectedRoot, err = filepath.Abs(expectedRoot)
	if err != nil {
		t.Fatal(err)
	}
	rootMatches := filepath.Clean(target.RootPath) == filepath.Clean(expectedRoot)
	if runtime.GOOS == "windows" {
		rootMatches = strings.EqualFold(filepath.Clean(target.RootPath), filepath.Clean(expectedRoot))
	}
	if registered.Name != "selected-project" || target.Name != registered.Name ||
		!rootMatches {
		t.Fatalf("unexpected imported Workspace: summary=%#v target=%#v selected=%q canonical=%q",
			registered, target, selected, expectedRoot)
	}
	entries, err := os.ReadDir(selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "existing.txt" {
		t.Fatalf("workspace import modified selected directory: %#v", entries)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "unchanged" {
		t.Fatalf("workspace content changed: %q, err=%v", content, err)
	}
}

func TestControlPlaneBootstrapsOnlyAnEmptyWorkspaceRegistry(t *testing.T) {
	home := t.TempDir()
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(home, "prayu.db"), HomePath: home,
		ReadToken: desktopControlPlaneTestToken, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	records, err := plane.stateStore.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "ws-default" ||
		records[0].Name != "default" ||
		records[0].RootPath != filepath.Join(home, "workspaces", "default") {
		t.Fatalf("unexpected first-run Workspace: %#v", records)
	}
}

func TestControlPlaneDockerCapabilityIsProcessLocalAcrossRestart(t *testing.T) {
	home := t.TempDir()
	databasePath := filepath.Join(home, "docker-capability.db")
	if _, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: databasePath, HomePath: home,
		ReadToken: desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		DockerExecutionEnabled: true, AppVersion: "desktop-test",
	}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("Docker execution without permission gate error=%v code=%s",
			err, apperror.CodeOf(err))
	}

	enabled, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: databasePath, HomePath: home,
		ReadToken: desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true,
		},
		DockerExecutionEnabled: true, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enabled.Close() })
	capability, err := enabled.DockerExecutionEnabled()
	if err != nil || !capability {
		t.Fatalf("enabled process capability=%t err=%v", capability, err)
	}
	assertDesktopDockerCapability(t, enabled.Handler(), true)
	if err := enabled.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(home, desktopDockerSandboxStagingDirectory)); err != nil || !info.IsDir() {
		t.Fatalf("trusted Desktop staging root was not created: info=%v err=%v", info, err)
	}

	disabled, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: databasePath, HomePath: home,
		ReadToken: desktopControlPlaneTestToken, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer disabled.Close()
	capability, err = disabled.DockerExecutionEnabled()
	if err != nil || capability {
		t.Fatalf("SQLite restored Docker start capability=%t err=%v", capability, err)
	}
	assertDesktopDockerCapability(t, disabled.Handler(), false)
}

func TestControlPlaneKeepsDebugAgentInputInsideGoControlPlane(t *testing.T) {
	disabled, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "debug-agent-disabled.db"),
		ReadToken:    desktopControlPlaneTestToken,
		AppVersion:   "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.DebugTerminalAgentInputController() != nil {
		t.Fatal("disabled Desktop process exposed a debug Agent-input controller")
	}
	if err := disabled.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		return
	}

	enabled, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath:                      filepath.Join(t.TempDir(), "debug-agent-enabled.db"),
		ReadToken:                         desktopControlPlaneTestToken,
		ControlToken:                      desktopControlPlaneControlToken,
		UserTerminalEnabled:               true,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled:   true,
			DangerFullAccessEnabled:   true,
			DebugMaximumAccessEnabled: true,
		},
		AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer enabled.Close()
	if enabled.DebugTerminalAgentInputController() == nil {
		t.Fatal("debug-enabled Go control plane did not retain its internal controller")
	}
}

func TestControlPlaneInstallsCommandRuntimeOnlyWithRunExecution(t *testing.T) {
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true,
		DangerFullAccessEnabled: true,
	}
	disabled, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath:                      filepath.Join(t.TempDir(), "command-runtime-disabled.db"),
		ReadToken:                         desktopControlPlaneTestToken,
		ControlToken:                      desktopControlPlaneControlToken,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities:   capabilities,
		AppVersion:                        "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.commandRuntime != nil || disabled.commandRuntimeManager == nil {
		t.Fatal("full-access without Run execution installed the command runtime adapter")
	}
	if err := disabled.Close(); err != nil {
		t.Fatal(err)
	}

	enabled, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath:                      filepath.Join(t.TempDir(), "command-runtime-enabled.db"),
		ReadToken:                         desktopControlPlaneTestToken,
		ControlToken:                      desktopControlPlaneControlToken,
		RunExecutionEnabled:               true,
		ExecutionPermissionControlEnabled: true,
		ExecutionPermissionCapabilities:   capabilities,
		AppVersion:                        "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer enabled.Close()
	if enabled.commandRuntime == nil || enabled.commandRuntimeManager == nil {
		t.Fatal("Run execution plus full-access did not install the command runtime adapter")
	}
}

func TestControlPlaneSharesCLIStoreAndReopensFromAHighWaterCursor(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared.db")
	cliStore, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cliStore.Close()
	_, run, err := application.NewRunService(cliStore).Create(context.Background(),
		application.CreateRunRequest{
			Goal: "verify Desktop and CLI SQLite concurrency", Profile: "review", ModelRoute: "review",
			Budget: domain.Budget{MaxTurns: 4},
		})
	if err != nil {
		t.Fatal(err)
	}
	run, err = application.NewRunService(cliStore).Start(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}

	first, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: databasePath, ReadToken: desktopControlPlaneTestToken, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	initial := readDesktopEventPoll(t, first.Handler(), run.ID, "")
	if len(initial.Frames) == 0 || initial.Cursor == "" {
		t.Fatalf("initial Desktop timeline is empty: %#v", initial)
	}

	created, err := application.NewNoteService(cliStore).Create(context.Background(),
		application.CreateNoteRequest{
			RunID: run.ID, Title: "CLI concurrent write", Content: "visible to the open Desktop connection",
		})
	if err != nil {
		t.Fatal(err)
	}
	afterWrite := readDesktopEventPoll(t, first.Handler(), run.ID, initial.Cursor)
	if len(afterWrite.Frames) != 1 || afterWrite.Frames[0].Event.Type != "note.created" ||
		afterWrite.Frames[0].Event.SubjectID != created.ID {
		t.Fatalf("Desktop did not observe the CLI write: %#v", afterWrite)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("idempotent close failed: %v", err)
	}

	secondNote, err := application.NewNoteService(cliStore).Create(context.Background(),
		application.CreateNoteRequest{
			RunID: run.ID, Title: "CLI write while Desktop closed", Content: "visible after reopen",
		})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: databasePath, ReadToken: desktopControlPlaneTestToken, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	afterReopen := readDesktopEventPoll(t, reopened.Handler(), run.ID, afterWrite.Cursor)
	if len(afterReopen.Frames) != 1 || afterReopen.Frames[0].Event.SubjectID != secondNote.ID ||
		afterReopen.Frames[0].Sequence != afterWrite.Frames[0].Sequence+1 {
		t.Fatalf("Desktop reopen did not resume exactly: %#v", afterReopen)
	}
}

func TestControlPlaneConcurrentOpenOnAnInitializedDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "concurrent.db")
	initialized, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := initialized.Close(); err != nil {
		t.Fatal(err)
	}

	const workers = 6
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			plane, err := OpenControlPlane(ControlPlaneConfig{
				DatabasePath: databasePath, ReadToken: desktopControlPlaneTestToken,
				AppVersion: fmt.Sprintf("desktop-test-%d", index),
			})
			if err != nil {
				results <- err
				return
			}
			response := desktopAPIRequest(plane.Handler(), "/api/v1/health")
			if response.Code != http.StatusOK {
				results <- fmt.Errorf("health status %d: %s", response.Code, response.Body.String())
				_ = plane.Close()
				return
			}
			results <- plane.Close()
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestControlPlaneSeparatesRunCreationFromExistingRunControls(t *testing.T) {
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "creation.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunCreationEnabled: true, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	workspace, err := plane.RegisterWorkspaceDirectory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version":"run_creation.v1","goal":"Desktop Run",` +
		`"workspace_id":"` + workspace.ID + `"}`
	created := desktopControlRequest(plane.Handler(), http.MethodPost, "/api/v1/runs",
		"desktop-run-create-operation-0001", body)
	if created.Code != http.StatusAccepted {
		t.Fatalf("Run creation status=%d body=%s", created.Code, created.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var result httpapi.RunCreationControlView
	if err := json.Unmarshal(envelope.Data, &result); err != nil || result.Run.ID == "" {
		t.Fatalf("Run creation response=%#v err=%v", result, err)
	}
	profile := desktopControlRequest(plane.Handler(), http.MethodPost,
		"/api/v1/runs/"+result.Run.ID+"/execution-profile",
		"desktop-profile-operation-0001", `{"profile":"docker"}`)
	if profile.Code != http.StatusNotFound {
		t.Fatalf("Run creation capability widened profile control: status=%d body=%s",
			profile.Code, profile.Body.String())
	}
}

func TestControlPlaneSeparatesSessionMessagesFromOtherControls(t *testing.T) {
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "messages.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		SessionMessageEnabled: true, AppVersion: "desktop-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	runs := application.NewRunService(plane.stateStore)
	_, created, err := runs.Create(t.Context(), application.CreateRunRequest{
		Goal: "Desktop Session message", Profile: "review",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runs.Start(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := "/api/v1/sessions/" + run.SessionID + "/messages"
	submitted := desktopControlRequest(plane.Handler(), http.MethodPost, requestPath,
		"desktop-session-message-operation-0001",
		`{"version":"session_message_submission.v1","content":"Review the current diff"}`)
	if submitted.Code != http.StatusAccepted {
		t.Fatalf("Session message status=%d body=%s", submitted.Code, submitted.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(submitted.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var result httpapi.SessionMessageControlView
	if err := json.Unmarshal(envelope.Data, &result); err != nil ||
		result.RunID != run.ID || result.SessionID != run.SessionID ||
		result.Steering.Status != string(domain.OperatorSteeringPending) ||
		result.ExecutionStarted || result.ModelCalled || result.ToolCalled || result.CapabilityGrant {
		t.Fatalf("Session message response=%#v err=%v", result, err)
	}
	history, err := plane.stateStore.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(history) != 0 {
		t.Fatalf("Session message was committed before Supervisor delivery: %#v err=%v", history, err)
	}
	creation := desktopControlRequest(plane.Handler(), http.MethodPost, "/api/v1/runs",
		"desktop-session-message-operation-0002",
		`{"version":"run_creation.v1","goal":"blocked","workspace_id":"workspace"}`)
	if creation.Code != http.StatusNotFound {
		t.Fatalf("Session capability widened Run creation: status=%d body=%s",
			creation.Code, creation.Body.String())
	}
	profile := desktopControlRequest(plane.Handler(), http.MethodPost,
		"/api/v1/runs/"+run.ID+"/execution-profile",
		"desktop-session-message-operation-0003", `{"profile":"preview"}`)
	if profile.Code != http.StatusNotFound {
		t.Fatalf("Session capability widened profile control: status=%d body=%s",
			profile.Code, profile.Body.String())
	}
}

func TestControlPlaneWakeWorkerStopsOnceAndCannotRestartAfterClose(t *testing.T) {
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "wake-worker.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunWakeWorkerEnabled: true, AppVersion: "desktop-worker-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plane.StartWakeWorker(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := plane.StartWakeWorker(t.Context()); err == nil {
		t.Fatal("a second desktop wake worker was started")
	}
	closed := make(chan error, 1)
	go func() { closed <- plane.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("desktop wake worker did not stop during close")
	}
	if err := plane.StartWakeWorker(t.Context()); err == nil {
		t.Fatal("closed desktop control plane restarted its wake worker")
	}
}

func TestControlPlaneScheduledWorkerIsExplicitSingleInstanceAndStopsOnClose(t *testing.T) {
	plane, err := OpenControlPlane(ControlPlaneConfig{
		DatabasePath: filepath.Join(t.TempDir(), "scheduled-worker.db"),
		ReadToken:    desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		ScheduledJobControlEnabled: true, ScheduledJobWorkerEnabled: true,
		AppVersion: "desktop-scheduled-worker-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := desktopAPIRequest(plane.Handler(), "/api/v1/capabilities")
	if response.Code != http.StatusOK {
		t.Fatalf("runtime capability status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var capabilities httpapi.RuntimeCapabilitiesView
	if err := json.Unmarshal(envelope.Data, &capabilities); err != nil {
		t.Fatal(err)
	}
	worker := capabilities.ScheduledJobWorker
	if !capabilities.ScheduledJobControlEnabled || !capabilities.ScheduledJobWorkerEnabled ||
		!worker.Enabled || worker.Concurrency != 1 || worker.PersistentService ||
		worker.RuntimeEnableSupported || worker.AuthorityEscalation {
		t.Fatalf("scheduled worker capability widened authority: %#v", capabilities)
	}
	if err := plane.StartWakeWorker(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := plane.StartWakeWorker(t.Context()); err == nil {
		t.Fatal("a second desktop scheduled worker was started")
	}
	closed := make(chan error, 1)
	go func() { closed <- plane.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("desktop scheduled worker did not stop during close")
	}
	if err := plane.StartWakeWorker(t.Context()); err == nil {
		t.Fatal("closed desktop control plane restarted its scheduled worker")
	}
}

func desktopControlRequest(handler http.Handler, method string, path string,
	key string, body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45678"
	request.Header.Set("Authorization", "Bearer "+desktopControlPlaneControlToken)
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func readDesktopEventPoll(t *testing.T, handler http.Handler, runID string,
	cursor string,
) httpapi.RunEventPollView {
	t.Helper()
	path := "/api/v1/runs/" + url.PathEscape(runID) + "/events/poll?limit=100"
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	response := desktopAPIRequest(handler, path)
	if response.Code != http.StatusOK {
		t.Fatalf("event poll status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var view httpapi.RunEventPollView
	if err := json.Unmarshal(envelope.Data, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func desktopAPIRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
	request.Host = "127.0.0.1:8765"
	request.RemoteAddr = "127.0.0.1:45678"
	request.Header.Set("Authorization", "Bearer "+desktopControlPlaneTestToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertDesktopDockerCapability(t *testing.T, handler http.Handler, want bool) {
	t.Helper()
	response := desktopAPIRequest(handler, "/api/v1/capabilities")
	if response.Code != http.StatusOK {
		t.Fatalf("runtime capability status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope desktopAPIEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var capabilities httpapi.RuntimeCapabilitiesView
	if err := json.Unmarshal(envelope.Data, &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.DockerExecutionEnabled != want ||
		capabilities.ProcessExecutionEnabled || capabilities.ShellExecutionEnabled {
		t.Fatalf("unexpected Desktop runtime capability projection: %#v", capabilities)
	}
}
