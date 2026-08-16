package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
)

type onceExecutorStub struct{ executed bool }

func (e *onceExecutorStub) Available() bool { return true }
func (e *onceExecutorStub) Execute(_ context.Context, request runner.OnceCommandRequest) (runner.OnceExecutionResult, error) {
	e.executed = true
	now := time.Now().UTC()
	return runner.OnceExecutionResult{
		ProtocolVersion: runner.OnceExecutionProtocolVersion,
		ExitCode:        0, StartedAt: now, CompletedAt: now.Add(time.Millisecond), TreeReaped: true,
	}, nil
}

func newOnceFixture(t *testing.T) (*store.SQLiteStore, string, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "once.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspaceRoot := t.TempDir()
	if err := st.SaveWorkspace(context.Background(), store.WorkspaceRecord{
		ID: "ws-1", Name: "once", RootPath: workspaceRoot, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	runService := NewRunService(st)
	_, run, err := runService.Create(context.Background(), CreateRunRequest{
		Goal: "once", Profile: "code", Surface: "code", Phase: "plan",
		WorkspaceID: "ws-1", Budget: domain.Budget{MaxTurns: 10, MaxToolCalls: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, run.ID, workspaceRoot
}

func setPermissionMode(t *testing.T, st *store.SQLiteStore, runID, mode string) {
	t.Helper()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, DebugMaximumAccessEnabled: true,
	}
	service := NewRunExecutionPermissionService(st, capabilities)
	_, err := service.Change(context.Background(), ChangeRunExecutionPermissionRequest{
		RunID: runID, Mode: mode, OperationKey: "once-key-" + mode + "-0001",
		RequestedBy: "operator", Reason: "once command test",
		ConfirmUserApproval:     mode == string(domain.RunExecutionPermissionApproval) || mode == string(domain.RunExecutionPermissionDebug),
		ConfirmDangerFullAccess: mode == string(domain.RunExecutionPermissionFullAccess),
		ConfirmDebugAccess:      mode == string(domain.RunExecutionPermissionDebug),
	})
	if err != nil {
		if mode == string(domain.RunExecutionPermissionConservative) &&
			strings.Contains(err.Error(), "already uses") {
			return // conservative is the default mode
		}
		t.Fatalf("set permission %s: %v", mode, err)
	}
}

func TestOnceCommandServiceTierGatingWithRealPermissions(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		mode     string
		approved bool
		wantRun  bool
	}{
		{name: "conservative denies", mode: "conservative"},
		{name: "approval without approval denies", mode: "approval"},
		{name: "approval approved runs", mode: "approval", approved: true, wantRun: true},
		{name: "full access runs", mode: "full_access", wantRun: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, runID, root := newOnceFixture(t)
			setPermissionMode(t, st, runID, tc.mode)
			executor := &onceExecutorStub{}
			service := NewOnceCommandService(st, executor,
				domain.ExecutionPermissionRuntimeCapabilities{
					OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
				})
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			request := OnceCommandRunRequest{
				RunID: runID, ExecutablePath: executable, Argv: []string{"x"},
				WorkingDirectory: root, TimeoutMilliseconds: 30000,
				Purpose: "tier test", RequestedBy: "operator", OperatorApproved: tc.approved,
			}
			_, err = service.Execute(ctx, request)
			if tc.wantRun {
				if err != nil || !executor.executed {
					t.Fatalf("expected execution: err=%v executed=%t", err, executor.executed)
				}
				timeline, err := st.ListRunEvents(ctx, runID)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, event := range timeline {
					if event.Type == "once_command.executed" && event.Source == "once_command_runner" {
						found = true
					}
				}
				if !found {
					t.Fatal("once_command.executed audit event missing")
				}
				return
			}
			if err == nil || executor.executed {
				t.Fatalf("expected denial: err=%v executed=%t", err, executor.executed)
			}
			if apperror.CodeOf(err) != apperror.CodePolicyDenied {
				t.Fatalf("denial code = %s", apperror.CodeOf(err))
			}
		})
	}
}

func TestOnceCommandServiceRejectsWorkspaceEscapeAndShellInterpreter(t *testing.T) {
	ctx := context.Background()
	st, runID, _ := newOnceFixture(t)
	setPermissionMode(t, st, runID, "full_access")
	executor := &onceExecutorStub{}
	service := NewOnceCommandService(st, executor,
		domain.ExecutionPermissionRuntimeCapabilities{DangerFullAccessEnabled: true})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	escape := OnceCommandRunRequest{
		RunID: runID, ExecutablePath: executable, Argv: []string{"x"},
		WorkingDirectory:    filepath.Join(t.TempDir(), "elsewhere"),
		TimeoutMilliseconds: 30000, Purpose: "escape", RequestedBy: "operator",
	}
	if _, err := service.Execute(ctx, escape); err == nil || executor.executed {
		t.Fatal("workspace escape was executed")
	}
	shell := OnceCommandRunRequest{
		RunID: runID, ExecutablePath: filepath.Join(os.TempDir(), "powershell.exe"),
		Argv: []string{"x"}, WorkingDirectory: ".",
		TimeoutMilliseconds: 30000, Purpose: "shell", RequestedBy: "operator",
	}
	if err := os.WriteFile(shell.ExecutablePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(ctx, shell); err == nil || executor.executed {
		t.Fatal("shell interpreter wrapped a one-shot command")
	}
}

var _ = strings.TrimSpace
