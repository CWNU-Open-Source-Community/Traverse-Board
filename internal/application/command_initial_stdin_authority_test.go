package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func TestCommandOperationInitialInputOutlivesRequestButRechecksAuthority(t *testing.T) {
	for _, boundary := range []string{"request_completion", "activation_revoked", "lease_lost", "completed_request_completion", "completed_activation_revoked", "completed_lease_lost"} {
		t.Run(boundary, func(t *testing.T) {
			state, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, t.Context())
			authority := domain.NewExecutionPermissionRuntimeAuthority()
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
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
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
			f := commandFixtureForScope(t, state, service, scope)
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
			var enteredOnce sync.Once
			request.DispatchCheck = func(c context.Context, s runner.CommandRuntimeResolvedSpec) error {
				// The first two checks are synchronous manager/native dispatch.
				// Hold initial stdin before its authority check. Ownership has a
				// separate phase and cannot authorize these pending bytes.
				if checks.Add(1) >= 3 {
					enteredOnce.Do(func() { close(entered) })
					<-release
				}
				return original(c, s)
			}
			job, _, err := manager.Start(ctx, request)
			if err != nil || job.State != runner.CommandRuntimeJobRunning {
				t.Fatalf("start: %+v %v", job, err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("initial input authority was not rechecked")
			}
			if strings.HasPrefix(boundary, "completed_") {
				raw, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{Version: supervisorToolResultVersion, Tool: f.call.ToolName, Status: string(domain.SupervisorToolCompleted), Message: "background Job started"})
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := state.RecordSupervisorToolResult(t.Context(), f.turn.Checkpoint, domain.SupervisorToolResult{CallID: f.call.CallID, Status: domain.SupervisorToolCompleted, ResultJSON: string(raw), CompletedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			switch strings.TrimPrefix(boundary, "completed_") {
			case "activation_revoked":
				authority.RevokeRun(run.ID)
			case "lease_lost":
				if _, _, err := state.ReleaseRunExecutionLease(t.Context(), lease); err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			finished, _, err := manager.Wait(t.Context(), job.ID, 5*time.Second, 0, 4096)
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := state.GetWorkspaceByID(t.Context(), scope.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := os.ReadFile(filepath.Join(workspace.RootPath, "command-operation-marker"))
			if strings.TrimPrefix(boundary, "completed_") == "request_completion" {
				if finished.State != runner.CommandRuntimeJobCompleted || readErr != nil || string(body) != input.Commands[0].InitialStdin {
					t.Fatalf("completed request killed initial input: state=%s body=%q err=%v", finished.State, body, readErr)
				}
			} else if finished.State != runner.CommandRuntimeJobFailed || !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("lost authority delivered input: state=%s body=%q err=%v", finished.State, body, readErr)
			}
		})
	}
}
