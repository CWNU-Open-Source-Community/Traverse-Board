package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/toolgateway"
)

type commandOperationProbeStore struct {
	*store.SQLiteStore
	afterPrepare func()
}

func (s *commandOperationProbeStore) PrepareCommandRuntimeJobForAgent(ctx context.Context, job runner.CommandRuntimeJob, actor domain.AgentAttribution) (runner.CommandRuntimeJob, bool, error) {
	stored, replayed, err := s.SQLiteStore.PrepareCommandRuntimeJobForAgent(ctx, job, actor)
	if err == nil && !replayed && s.afterPrepare != nil {
		s.afterPrepare()
	}
	return stored, replayed, err
}

func commandOperationMarkerInput() toolgateway.CommandRuntimeInput {
	profile := runner.CommandRuntimeBash
	script := "printf dispatched > command-operation-marker"
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = "[System.IO.File]::WriteAllText((Join-Path (Get-Location) 'command-operation-marker'), 'dispatched')"
	}
	return toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
		Action: toolgateway.CommandRuntimeActionStart, Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
			WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 5000, Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network: runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone,
			Purpose: "verify common authorization at native process dispatch",
		}}}
}

func TestCommandOperationRechecksAfterPrepareAndAtNativeLaunch(t *testing.T) {
	for _, boundary := range []string{"cancel_after_prepare", "revoke_after_prepare", "revoke_at_native_launch", "lease_at_native_launch"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			state, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
			authority := domain.NewExecutionPermissionRuntimeAuthority()
			capabilities.RuntimeAuthority = authority
			permission, err := state.GetRunExecutionPermission(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := authority.ActivateRunFullAccess(permission); err != nil {
				t.Fatal(err)
			}
			probe := &commandOperationProbeStore{SQLiteStore: state}
			manager, err := runner.NewPlatformCommandRuntimeManager(probe, "common-command-authority-owner")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				c, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				_ = manager.Shutdown(c)
			})
			service, err := NewCommandRuntimeService(probe, manager, capabilities)
			if err != nil {
				t.Fatal(err)
			}
			scope := commandRuntimeTestScope(t, ctx, state, service, run, root, lease, "native-authority")
			f := commandFixtureForScope(t, state, service, scope)
			input := commandOperationMarkerInput()
			scope = f.startScope(t, input, 1)
			bindings, err := service.loadAuthorizedBindings(ctx, scope, false)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := service.normalizeCommandRuntimeSpec(input.Commands[0], bindings.rootPath)
			if err != nil {
				t.Fatal(err)
			}
			request, err := service.authorizedCommandStart(scope, bindings, scope.OperationKey, spec)
			if err != nil {
				t.Fatal(err)
			}
			original := request.DispatchCheck
			var checks atomic.Int32
			request.DispatchCheck = func(c context.Context, s runner.CommandRuntimeResolvedSpec) error {
				// The manager checks first; the platform starter checks immediately before
				// native creation. Count this explicit callback, never storage reads.
				n := checks.Add(1)
				if n == 2 {
					if boundary == "revoke_at_native_launch" {
						authority.RevokeRun(run.ID)
					}
					if boundary == "lease_at_native_launch" {
						if _, _, err := state.ReleaseRunExecutionLease(c, lease); err != nil {
							return err
						}
					}
				}
				return original(c, s)
			}
			switch boundary {
			case "cancel_after_prepare":
				probe.afterPrepare = cancel
			case "revoke_after_prepare":
				probe.afterPrepare = func() { authority.RevokeRun(run.ID) }
			}
			_, _, err = manager.Start(ctx, request)
			if err == nil {
				t.Fatal("stale operation reached native dispatch")
			}
			_, jobID := runner.CommandRuntimeOperationIdentity(run.ID, scope.OperationKey)
			job, err := state.GetCommandRuntimeJob(context.Background(), jobID)
			if err != nil || job.State != runner.CommandRuntimeJobFailed || job.PID != 0 || !job.TreeReaped {
				t.Fatalf("denied launch did not retain a no-process receipt: %+v err=%v", job, err)
			}
			workspace, err := state.GetWorkspaceByID(context.Background(), scope.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(workspace.RootPath, "command-operation-marker")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("denied launch created marker: %v", err)
			}
			if boundary == "revoke_at_native_launch" || boundary == "lease_at_native_launch" {
				if checks.Load() != 2 {
					t.Fatalf("native dispatch boundary not reached: checks=%d", checks.Load())
				}
			}
		})
	}
}

func TestCommandOperationBindsActualInputsAndDoesNotRenewDeniedGuard(t *testing.T) {
	ctx := t.Context()
	state, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state, "input-bound-process-owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	scope := commandRuntimeTestScope(t, ctx, state, service, run, root, lease, "input-bound-process")
	f := commandFixtureForScope(t, state, service, scope)
	scope = f.startScope(t, commandOperationMarkerInput(), 1)
	bindings, err := service.loadAuthorizedBindings(ctx, scope, false)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.normalizeCommandRuntimeSpec(commandOperationMarkerInput().Commands[0], bindings.rootPath)
	if err != nil {
		t.Fatal(err)
	}
	start, err := service.authorizedCommandStart(scope, bindings, scope.OperationKey, spec)
	if err != nil {
		t.Fatal(err)
	}
	changed := spec
	changed.Environment = append(append([]string{}, spec.Environment...), "UC_CHANGED=yes")
	if err := start.DispatchCheck(ctx, changed); err == nil {
		t.Fatal("changed final environment was accepted")
	}
	if err := start.DispatchCheck(ctx, spec); err == nil {
		t.Fatal("denied guard was renewed with original inputs")
	}
	jobs, err := state.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: run.ID, Limit: 10})
	if err != nil || len(jobs) != 0 {
		t.Fatalf("guard-only check created a Job: %v %v", jobs, err)
	}
}

func TestCommandOperationNeverTreatsHostCwdOrNetworkIntentAsIsolation(t *testing.T) {
	op, err := commandOperation(make([]byte, 32), "host-operation", commandruntimeadapter.HostUnsandboxed("test-generation"),
		"input", runner.CommandRuntimeNetworkDisabled, []toolcontract.Target{{Kind: "directory", Locator: "workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Effects) != 2 || op.Effects[1] != toolcontract.EffectUnknown {
		t.Fatalf("host effects promoted to bounded: %+v", op.Effects)
	}
}
