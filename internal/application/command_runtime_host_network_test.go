package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

func TestFullAccessHostCommandNetworkRunsOnceAndRejectsOldGrant(t *testing.T) {
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("curl"); err != nil {
			t.Skipf("curl is unavailable: %v", err)
		}
	}
	ctx := context.Background()
	state, runRecord, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities.FullAccessRequiresRuntimeGrant = true
	capabilities.RuntimeAuthority = authority
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := authority.ActivateRunFullAccess(permission)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("full-access-host-network-owner"))
	if err != nil {
		t.Skipf("platform command runtime unavailable: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	})
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("host-network-response"))
	}))
	defer server.Close()
	profile := runner.CommandRuntimeBash
	script := "curl --noproxy '*' -fsS '" + server.URL + "'"
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = "$r = Invoke-WebRequest -UseBasicParsing -Uri '" + server.URL + "'; [Console]::Out.WriteLine($r.Content)"
	}
	maxBytes := 4096
	input := toolgateway.CommandRuntimeInput{
		Version:       toolgateway.CommandRuntimeToolProtocolVersion,
		Action:        toolgateway.CommandRuntimeActionRun,
		FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
		Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion, Profile: profile,
			Script: script, WorkingDirectory: ".",
			Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 20000,
			Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network:             runner.CommandRuntimeNetworkHost,
			Credentials:         runner.CommandRuntimeCredentialsNone,
			Purpose:             "fetch one loopback response under current Full Access",
		}},
	}
	scope := commandRuntimeTestScope(t, ctx, state, service, runRecord, root,
		lease, "full-access-host-network-0001")
	mode, err := state.GetRunMode(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	scope.Surface, scope.Phase = mode.Surface, mode.Phase
	scope.Role, scope.Profile = domain.AgentRoleRoot, mode.Profile
	scope.ModeRevision = mode.Revision
	scope.PermissionMode = domain.RunExecutionPermissionFullAccess
	scope.PermissionRevision = permission.Revision
	scope.PermissionSnapshotID = permission.ID
	scope.PermissionGeneration = grant.Generation
	scope.PermissionRuntimeEpoch = authority.RuntimeEpoch()
	result, err := service.ExecuteCommandRuntime(ctx, scope, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 1 || result.Jobs[0].State != runner.CommandRuntimeJobCompleted ||
		result.Jobs[0].ExitCode == nil || *result.Jobs[0].ExitCode != 0 ||
		len(result.Artifacts) != 1 ||
		!strings.Contains(result.Artifacts[0].Stdout, "host-network-response") ||
		requests.Load() != 1 {
		t.Fatalf("host request failed or duplicated: jobs=%+v artifacts=%+v count=%d",
			result.Jobs, result.Artifacts, requests.Load())
	}
	replay, err := service.ExecuteCommandRuntime(ctx, scope, input)
	if err != nil || !replay.Replayed || requests.Load() != 1 ||
		len(replay.Jobs) != 1 || replay.Jobs[0].ID != result.Jobs[0].ID {
		t.Fatalf("replay started another request: jobs=%+v replayed=%t count=%d err=%v",
			replay.Jobs, replay.Replayed, requests.Load(), err)
	}
	changed := input
	changed.Commands = append([]runner.CommandRuntimeSpec(nil), input.Commands...)
	changed.Commands[0].Purpose = "changed purpose with the same operation key"
	if _, err := service.ExecuteCommandRuntime(ctx, scope, changed); err == nil ||
		apperror.CodeOf(err) != apperror.CodeConflict || requests.Load() != 1 {
		t.Fatalf("changed same-key intent started another request: count=%d err=%v",
			requests.Load(), err)
	}
	if os.Getenv("TRAVERSE_HOST_NETWORK_SMOKE") == "1" {
		gitInput := input
		gitInput.Commands = append([]runner.CommandRuntimeSpec(nil), input.Commands...)
		gitInput.Commands[0].Script = "git ls-remote https://github.com/CWNU-Open-Source-Community/Traverse-Board.git HEAD"
		gitInput.Commands[0].Purpose = "read a public Git HEAD through the host proxy"
		if proxy := os.Getenv("TRAVERSE_HOST_NETWORK_SMOKE_PROXY"); proxy != "" {
			gitInput.Commands[0].Environment = []runner.CommandRuntimeEnvironment{
				{Name: "HTTP_PROXY", Value: proxy},
				{Name: "HTTPS_PROXY", Value: proxy},
			}
		} else if runtime.GOOS == "windows" {
			bindings, bindErr := service.loadAuthorizedBindings(ctx, scope, true)
			if bindErr != nil {
				t.Fatal(bindErr)
			}
			resolved, resolveErr := service.normalizeCommandRuntimeSpec(
				gitInput.Commands[0], bindings.rootPath)
			if resolveErr != nil {
				t.Fatal(resolveErr)
			}
			var bridgeURL, routeDigest string
			for _, entry := range resolved.Environment {
				name, value, _ := strings.Cut(entry, "=")
				switch strings.ToUpper(name) {
				case "HTTPS_PROXY":
					bridgeURL = value
				case "CYBERAGENT_HOST_PROXY_ROUTE_SHA256":
					routeDigest = value
				}
			}
			if !strings.HasPrefix(bridgeURL, "http://127.0.0.1:") ||
				len(routeDigest) != 64 {
				t.Fatalf("system proxy bridge was not bound to Full command: proxy=%q route_bound=%t",
					bridgeURL, routeDigest != "")
			}
		}
		gitScope := scope
		gitScope.InvocationID = "full-access-host-network-git-0001"
		gitScope.OperationKey = "full-access-host-network-git-0001"
		gitResult, gitErr := service.ExecuteCommandRuntime(ctx, gitScope, gitInput)
		if gitErr != nil || len(gitResult.Jobs) != 1 ||
			gitResult.Jobs[0].ExitCode == nil || *gitResult.Jobs[0].ExitCode != 0 ||
			len(gitResult.Artifacts) != 1 ||
			!strings.Contains(gitResult.Artifacts[0].Stdout, "HEAD") {
			t.Fatalf("public Git HTTPS through host proxy failed: jobs=%+v artifacts=%+v err=%v",
				gitResult.Jobs, gitResult.Artifacts, gitErr)
		}
		t.Log("public Git HTTPS command completed through the host network")
		if runtime.GOOS == "windows" {
			if nodePath, lookupErr := exec.LookPath("node.exe"); lookupErr == nil {
				nodeInput := input
				nodeInput.Commands = append([]runner.CommandRuntimeSpec(nil), input.Commands...)
				nodeInput.Commands[0].Profile = runner.CommandRuntimeProcess
				nodeInput.Commands[0].Executable = nodePath
				nodeInput.Commands[0].Script = ""
				nodeInput.Commands[0].Arguments = []string{"-e",
					"fetch('https://www.example.com/').then(r => { console.log('http_status='+r.status); if (r.status !== 200) process.exit(1) }).catch(e => { console.error(e.message); process.exit(2) })"}
				nodeInput.Commands[0].Purpose = "verify Node default fetch uses the managed system proxy"
				nodeScope := scope
				nodeScope.InvocationID = "full-access-host-network-node-0001"
				nodeScope.OperationKey = "full-access-host-network-node-0001"
				nodeResult, nodeErr := service.ExecuteCommandRuntime(ctx, nodeScope, nodeInput)
				if nodeErr != nil || len(nodeResult.Jobs) != 1 ||
					nodeResult.Jobs[0].ExitCode == nil || *nodeResult.Jobs[0].ExitCode != 0 ||
					len(nodeResult.Artifacts) != 1 ||
					!strings.Contains(nodeResult.Artifacts[0].Stdout, "http_status=200") {
					t.Fatalf("Node default fetch through host bridge failed: jobs=%+v artifacts=%+v err=%v",
						nodeResult.Jobs, nodeResult.Artifacts, nodeErr)
				}
				t.Log("Node default fetch completed through the managed system proxy")
			} else {
				t.Log("Node is unavailable; default fetch was not checked")
			}
		}
	}
	background := input
	background.Action = toolgateway.CommandRuntimeActionStart
	background.FailurePolicy = ""
	background.MaxBytes = nil
	background.Commands = append([]runner.CommandRuntimeSpec(nil), input.Commands...)
	background.Commands[0].TimeoutMilliseconds = 10000
	background.Commands[0].Purpose = "verify revocation reaps an active host network job"
	background.Commands[0].Script = "sleep 5; curl --noproxy '*' -fsS '" + server.URL + "'"
	if runtime.GOOS == "windows" {
		background.Commands[0].Script = "Start-Sleep -Seconds 5; Invoke-WebRequest -UseBasicParsing -Uri '" + server.URL + "'"
	}
	backgroundScope := scope
	backgroundScope.InvocationID = "full-access-host-network-background-0001"
	backgroundScope.OperationKey = "full-access-host-network-background-0001"
	started, err := service.ExecuteCommandRuntime(ctx, backgroundScope, background)
	if err != nil || len(started.Jobs) != 1 ||
		started.Jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("background host command did not start: jobs=%+v err=%v", started.Jobs, err)
	}
	authority.RevokeRun(runRecord.ID)
	if stopped, err := service.Reconcile(ctx); err != nil || stopped != 1 {
		t.Fatalf("revoked host job survived reconciliation: stopped=%d err=%v", stopped, err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		job, _, err := manager.Wait(ctx, started.Jobs[0].ID,
			100*time.Millisecond, 0, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if job.State.Terminal() {
			if job.State != runner.CommandRuntimeJobKilled || !job.TreeReaped || requests.Load() != 1 {
				t.Fatalf("revoked host job had an unsafe terminal result: job=%+v requests=%d",
					job, requests.Load())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revoked host job did not terminate")
		}
	}
	stale := scope
	stale.InvocationID = "full-access-host-network-stale-0001"
	stale.OperationKey = "full-access-host-network-stale-0001"
	if _, err := service.ExecuteCommandRuntime(ctx, stale, input); err == nil ||
		apperror.CodeOf(err) != apperror.CodePolicyDenied || requests.Load() != 1 {
		t.Fatalf("revoked grant started network: count=%d err=%v", requests.Load(), err)
	}
	if _, err := authority.ActivateRunFullAccess(permission); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteCommandRuntime(ctx, stale, input); err == nil ||
		apperror.CodeOf(err) != apperror.CodePolicyDenied || requests.Load() != 1 {
		t.Fatalf("regranted Run revived an old invocation: count=%d err=%v", requests.Load(), err)
	}
	coldAuthority := domain.NewExecutionPermissionRuntimeAuthority()
	if _, err := coldAuthority.ActivateRunFullAccess(permission); err != nil {
		t.Fatal(err)
	}
	coldCapabilities := capabilities
	coldCapabilities.RuntimeAuthority = coldAuthority
	coldService, err := NewCommandRuntimeService(state, manager, coldCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coldService.ExecuteCommandRuntime(ctx, stale, input); err == nil ||
		apperror.CodeOf(err) != apperror.CodePolicyDenied || requests.Load() != 1 {
		t.Fatalf("new process epoch revived an old invocation: count=%d err=%v",
			requests.Load(), err)
	}
}
