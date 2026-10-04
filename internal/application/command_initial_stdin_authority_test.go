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

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestCommandOperationInitialInputOutlivesRequestButRechecksAuthority(t *testing.T) {
	for _, boundary := range []string{"request_completion", "activation_revoked", "lease_lost"} {
		t.Run(boundary, func(t *testing.T) {
			state, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, t.Context())
			authority := domain.NewExecutionPermissionRuntimeAuthority()
			capabilities.FullAccessRequiresRuntimeGrant = true
			capabilities.RuntimeAuthority = authority
			permission, err := state.GetRunExecutionPermission(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := authority.ActivateRunFullAccess(permission); err != nil {
				t.Fatal(err)
			}
			probe := &commandOperationProbeStore{SQLiteStore: state}
			manager, err := runner.NewPlatformCommandRuntimeManager(probe, "initial-input-native-owner")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = manager.Shutdown(ctx)
			})
			service, err := NewCommandRuntimeService(probe, manager, capabilities)
			if err != nil {
				t.Fatal(err)
			}
			scope := commandRuntimeTestScope(t, t.Context(), state, service, run, root, lease, "initial-input-"+boundary)
			mode, err := state.GetRunMode(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			scope.Surface, scope.Phase, scope.Profile, scope.Role = mode.Surface, mode.Phase, mode.Profile, root.Role
			scope.ModeRevision, scope.PermissionRevision, scope.PermissionMode = mode.Revision, permission.Revision, permission.Mode
			scope.PermissionSnapshotID, scope.PermissionRuntimeEpoch = permission.ID, authority.RuntimeEpoch()
			generation, live := capabilities.FullAccessGeneration(permission)
			if !live {
				t.Fatal("missing test activation")
			}
			scope.PermissionGeneration = generation
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			probe.onPermissionRead = func(read int) {
				// Manager Authorize + BeforeDispatch and native Recheck have
				// already passed. The fourth read is the initial stdin Recheck.
				if read == 4 {
					close(entered)
					<-release
				}
			}
			input := commandOperationMarkerInput()
			input.Commands[0].StdinPolicy = runner.CommandRuntimeStdinPipe
			input.Commands[0].InitialStdin = "committed native stdin"
			input.Commands[0].TimeoutMilliseconds = 10000
			if runtime.GOOS == "windows" {
				input.Commands[0].Script = "$text = [Console]::In.ReadToEnd(); if ($text.Length -gt 0) { [System.IO.File]::WriteAllText((Join-Path (Get-Location) 'command-operation-marker'), $text) }"
			} else {
				input.Commands[0].Script = "text=$(cat); if [ -n \"$text\" ]; then printf '%s' \"$text\" > command-operation-marker; fi"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result, err := service.ExecuteCommandRuntime(ctx, scope, input)
			if err != nil || len(result.Jobs) != 1 || result.Jobs[0].State != runner.CommandRuntimeJobRunning {
				t.Fatalf("start: %+v %v", result.Jobs, err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("initial input authority was not rechecked")
			}
			cancel()
			switch boundary {
			case "activation_revoked":
				authority.RevokeRun(run.ID)
			case "lease_lost":
				if _, _, err := state.ReleaseRunExecutionLease(t.Context(), lease); err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			finished, _, err := manager.Wait(t.Context(), result.Jobs[0].ID, 5*time.Second, 0, 4096)
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := state.GetWorkspaceByID(t.Context(), scope.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := os.ReadFile(filepath.Join(workspace.RootPath, "command-operation-marker"))
			if boundary == "request_completion" {
				if finished.State != runner.CommandRuntimeJobCompleted || readErr != nil || string(body) != input.Commands[0].InitialStdin {
					t.Fatalf("completed request killed initial input: state=%s body=%q err=%v", finished.State, body, readErr)
				}
			} else if finished.State != runner.CommandRuntimeJobFailed || !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("lost authority delivered input: state=%s body=%q err=%v", finished.State, body, readErr)
			}
		})
	}
}
