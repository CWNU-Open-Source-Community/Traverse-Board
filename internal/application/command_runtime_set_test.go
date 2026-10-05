package application

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/toolgateway"
)

type commandRuntimeSetUnreadyLocalBackend struct{ sandbox.LocalBackend }

func TestCommandRuntimeSetPartialStartupFailureReapsOwnedJobBeforeReturning(t *testing.T) {
	ctx := t.Context()
	state, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("partial-startup-owner"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
	})
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	profile, script := runner.CommandRuntimeBash, "sleep 10"
	if runtime.GOOS == "windows" {
		profile, script = runner.CommandRuntimePowerShell, "Start-Sleep -Seconds 10"
	}
	scope := commandRuntimeTestScope(t, ctx, state, service, run, root, lease, "partial-startup")
	fixture := commandFixtureForScope(t, state, service, scope)
	started, err := fixture.execute(t, ctx, toolgateway.CommandRuntimeInput{
		Version: toolgateway.CommandRuntimeToolProtocolVersion,
		Action:  toolgateway.CommandRuntimeActionStart,
		Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion, Profile: profile,
			Script: script, WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 30_000,
			Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network:             runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone,
			Purpose: "hold an owned Job until partial startup rollback",
		}},
	}, 1)
	if err != nil || len(started.Jobs) != 1 || started.Jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("owned Job did not start: jobs=%+v err=%v", started.Jobs, err)
	}
	set, err := OpenCommandRuntimeSet(ctx, state, manager, CommandRuntimeSetOptions{
		HostEnabled: true, Capabilities: capabilities,
		Drydocks: &RunWorktreeService{}, LocalBackend: commandRuntimeSetUnreadyLocalBackend{},
		LocalReadiness: &sandbox.LocalReadiness{}, StartupShutdownTimeout: 5 * time.Second,
	})
	if set != nil || err == nil || !strings.Contains(err.Error(), "Local Sandbox Command Runtime readiness is invalid") {
		t.Fatalf("partial Local startup failure was lost: set=%v err=%v", set, err)
	}
	job, err := state.GetCommandRuntimeJob(ctx, started.Jobs[0].ID)
	if err != nil || job.State != runner.CommandRuntimeJobInterrupted || !job.TreeReaped {
		t.Fatalf("failed startup returned before the owned Job drained: job=%+v err=%v", job, err)
	}
	if _, err := NewNoteService(state).Create(ctx, CreateNoteRequest{
		RunID: run.ID, Title: "startup failure retained store", Content: "the caller still owns the writable SQLite connection",
	}); err != nil {
		t.Fatalf("startup rollback closed or invalidated the borrowed store: %v", err)
	}
}
