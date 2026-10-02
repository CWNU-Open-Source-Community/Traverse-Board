package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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
	mu               sync.Mutex
	prepared         bool
	permissionReads  int
	afterPrepare     func()
	onPermissionRead func(int)
}

func (s *commandOperationProbeStore) PrepareCommandRuntimeJobForAgent(ctx context.Context,
	job runner.CommandRuntimeJob, actor domain.AgentAttribution,
) (runner.CommandRuntimeJob, bool, error) {
	stored, replayed, err := s.SQLiteStore.PrepareCommandRuntimeJobForAgent(ctx, job, actor)
	if err == nil && !replayed {
		s.mu.Lock()
		s.prepared = true
		s.permissionReads = 0
		s.mu.Unlock()
		if s.afterPrepare != nil {
			s.afterPrepare()
		}
	}
	return stored, replayed, err
}

func (s *commandOperationProbeStore) GetRunExecutionPermission(ctx context.Context, runID string) (domain.RunExecutionPermissionSnapshot, error) {
	s.mu.Lock()
	read := 0
	if s.prepared {
		s.permissionReads++
		read = s.permissionReads
	}
	s.mu.Unlock()
	if read != 0 && s.onPermissionRead != nil {
		s.onPermissionRead(read)
	}
	return s.SQLiteStore.GetRunExecutionPermission(ctx, runID)
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
			capabilities.FullAccessRequiresRuntimeGrant = true
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
			scope := commandRuntimeTestScope(t, ctx, state, service, run, root, lease, "common-authority-"+boundary)
			scope.PermissionSnapshotID = permission.ID
			mode, err := state.GetRunMode(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			scope.Surface, scope.Phase, scope.Profile, scope.Role = mode.Surface, mode.Phase, mode.Profile, root.Role
			scope.ModeRevision, scope.PermissionRevision, scope.PermissionMode = mode.Revision, permission.Revision, permission.Mode
			scope.PermissionRuntimeEpoch = authority.RuntimeEpoch()
			generation, live := capabilities.FullAccessGeneration(permission)
			if !live {
				t.Fatal("test Full activation is unavailable")
			}
			scope.PermissionGeneration = generation
			switch boundary {
			case "cancel_after_prepare":
				probe.afterPrepare = cancel
			case "revoke_after_prepare":
				probe.afterPrepare = func() { authority.RevokeRun(run.ID) }
			case "revoke_at_native_launch", "lease_at_native_launch":
				probe.onPermissionRead = func(read int) {
					// Authorize + its consumed guard are the manager boundary;
					// the next resolver invocation is inside the platform starter.
					if read != 3 {
						return
					}
					if boundary == "revoke_at_native_launch" {
						authority.RevokeRun(run.ID)
					} else {
						if _, _, err := state.ReleaseRunExecutionLease(context.Background(), lease); err != nil {
							t.Error(err)
						}
					}
				}
			}
			_, err = service.ExecuteCommandRuntime(ctx, scope, commandOperationMarkerInput())
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
				probe.mu.Lock()
				reads := probe.permissionReads
				probe.mu.Unlock()
				if reads != 3 {
					t.Fatalf("native boundary not reached: permission reads=%d", reads)
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
