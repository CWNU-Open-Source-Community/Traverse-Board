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
	capabilities.RuntimeAuthority = authority
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = authority.ActivateRunFullAccess(permission)
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
	f := commandFixtureForScope(t, state, service, scope)
	round := 1
	result, err := f.execute(t, ctx, input, round)
	scope, _ = f.scope(t)
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
	before, _, err := state.GetSupervisorApprovalCall(ctx, runRecord.ID, f.call.CallID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatalf("network replay: %t %v", waiting, err)
	}
	after, _, err := state.GetSupervisorApprovalCall(ctx, runRecord.ID, f.call.CallID)
	jobs, listErr := state.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: runRecord.ID, Limit: 10})
	if err != nil || listErr != nil || before.ResultJSON != after.ResultJSON || requests.Load() != 1 || len(jobs) != 1 || jobs[0].ID != result.Jobs[0].ID {
		t.Fatalf("network replay duplicated or changed receipt: %v %v", err, listErr)
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
		round++
		gitResult, gitErr := f.execute(t, ctx, gitInput, round)
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
				round++
				nodeResult, nodeErr := f.execute(t, ctx, nodeInput, round)
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
	if round >= 3 {
		f.nextTurn(t, lease)
		round = 0
	}
	round++
	started, err := f.execute(t, ctx, background, round)
	backgroundScope, _ := f.scope(t)
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
	stale := backgroundScope
	if _, err := service.ExecuteCommandRuntime(ctx, stale, background); err == nil ||
		apperror.CodeOf(err) != apperror.CodePolicyDenied || requests.Load() != 1 {
		t.Fatalf("revoked grant started network: count=%d err=%v", requests.Load(), err)
	}
	if _, err := authority.ActivateRunFullAccess(permission); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteCommandRuntime(ctx, stale, background); err == nil ||
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
	if _, err := coldService.ExecuteCommandRuntime(ctx, stale, background); err == nil ||
		apperror.CodeOf(err) != apperror.CodePolicyDenied || requests.Load() != 1 {
		t.Fatalf("new process epoch revived an old invocation: count=%d err=%v",
			requests.Load(), err)
	}
}
