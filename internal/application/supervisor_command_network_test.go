package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSupervisorFullAccessHostCommandFetchesWithoutApproval(t *testing.T) {
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("curl"); err != nil {
			t.Skipf("curl unavailable: %v", err)
		}
	}
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "supervisor-host-network.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	workspaceRoot := t.TempDir()
	if err := state.SaveWorkspace(ctx, store.WorkspaceRecord{
		ID: "ws-supervisor-host-network", Name: "supervisor-host-network",
		RootPath: workspaceRoot, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	_, runRecord, err := runs.Create(ctx, application.CreateRunRequest{
		Goal:    "fetch a local HTTP response using a Full Access command",
		Profile: "code", Surface: "code", Phase: "deliver",
		WorkspaceID: "ws-supervisor-host-network", ModelRoute: "tool-loop/model",
		Budget: domain.Budget{MaxTurns: 3, MaxToolCalls: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionProfileService(state).Change(ctx,
		application.ChangeRunExecutionProfileRequest{RunID: runRecord.ID,
			Profile: "local", OperationKey: "supervisor-host-network-profile-0001",
			RequestedBy: "test_operator", Reason: "exercise host network"}); err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	selected, err := application.NewRunExecutionPermissionService(state, capabilities).
		Change(ctx, application.ChangeRunExecutionPermissionRequest{
			RunID: runRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "supervisor-host-network-permission-0001",
			RequestedBy:  "test_operator", Reason: "allow ordinary host network",
			ConfirmDangerFullAccess: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Start(ctx, runRecord.ID); err != nil {
		t.Fatal(err)
	}
	if _, live := capabilities.FullAccessGeneration(selected.Permission); !live {
		if _, err := authority.ActivateRunFullAccess(selected.Permission); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("supervisor-host-network-owner"))
	if err != nil {
		t.Skipf("host command runtime unavailable: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown host command runtime: %v", err)
		}
	}()
	commandRuntime, err := application.NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("supervisor-host-network-response"))
	}))
	defer server.Close()
	profile := runner.CommandRuntimeBash
	script := "curl --noproxy '*' -fsS '" + server.URL + "'"
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = "$r = Invoke-WebRequest -UseBasicParsing -Uri '" + server.URL + "'; [Console]::Out.WriteLine($r.Content)"
	}
	maxBytes := 4096
	payload, err := json.Marshal(toolgateway.CommandRuntimeInput{
		Version:       toolgateway.CommandRuntimeToolProtocolVersion,
		Action:        toolgateway.CommandRuntimeActionRun,
		FailurePolicy: toolgateway.CommandRuntimeFailFast,
		MaxBytes:      &maxBytes,
		Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion, Profile: profile,
			Script: script, WorkingDirectory: ".",
			Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 20000,
			Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network:             runner.CommandRuntimeNetworkHost,
			Credentials:         runner.CommandRuntimeCredentialsNone,
			Purpose:             "read a local HTTP endpoint through the host network",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedToolProvider{}
	provider.respond = func(request llm.ChatRequest, index int) (*llm.ChatResponse, error) {
		switch index {
		case 0:
			found := false
			for _, tool := range request.Tools {
				if tool.Name == string(toolgateway.CommandRuntimeTool) {
					found = strings.Contains(string(tool.Parameters), `"network":{"const":"host"}`)
				}
			}
			if !found {
				return nil, errors.New("Full Access host network was absent from model tool schema")
			}
			return toolResponse("provider-host-command", string(toolgateway.CommandRuntimeTool), string(payload)), nil
		case 1:
			for _, message := range request.Messages {
				for _, result := range message.ToolResults {
					if strings.Contains(result.Content, "supervisor-host-network-response") {
						return textResponse(rootActionResponse(domain.RootActionContinue,
							"host network response received", "", "")), nil
					}
				}
			}
			return nil, errors.New("model did not receive the host network command result")
		default:
			return nil, errors.New("unexpected extra model attempt")
		}
	}
	supervisor := newToolLoopSupervisor(state, provider).
		WithExecutionPermissionCapabilities(capabilities).
		WithCommandRuntime(commandRuntime)
	result, err := supervisor.Step(ctx, runRecord.ID)
	if err != nil || result.ToolCalls != 1 || result.ToolRounds != 1 ||
		result.Text != "host network response received" || requests.Load() != 1 {
		t.Fatalf("Supervisor host network journey=%+v count=%d err=%v",
			result, requests.Load(), err)
	}
}
