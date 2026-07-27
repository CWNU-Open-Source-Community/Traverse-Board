package desktop

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
	terminalruntime "cyberagent-workbench/internal/terminal"
)

type desktopTerminalBackendStub struct {
	process *desktopTerminalProcessStub
}

func (b *desktopTerminalBackendStub) Name() string {
	return "desktop-terminal-test"
}

func (b *desktopTerminalBackendStub) Available() bool {
	return true
}

func (b *desktopTerminalBackendStub) Start(context.Context,
	terminalruntime.BackendStartRequest,
) (terminalruntime.Process, error) {
	reader, writer := io.Pipe()
	b.process = &desktopTerminalProcessStub{
		reader: reader, writer: writer, wait: make(chan struct{}),
	}
	return b.process, nil
}

type desktopTerminalProcessStub struct {
	mu        sync.Mutex
	reader    *io.PipeReader
	writer    *io.PipeWriter
	input     bytes.Buffer
	wait      chan struct{}
	closeOnce sync.Once
}

func (p *desktopTerminalProcessStub) Read(value []byte) (int, error) {
	return p.reader.Read(value)
}

func (p *desktopTerminalProcessStub) Write(value []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.Write(value)
}

func (p *desktopTerminalProcessStub) Resize(int, int) error {
	return nil
}

func (p *desktopTerminalProcessStub) Wait(ctx context.Context) (int, error) {
	select {
	case <-p.wait:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (p *desktopTerminalProcessStub) Close() error {
	p.closeOnce.Do(func() {
		_ = p.writer.Close()
		close(p.wait)
	})
	return nil
}

func (p *desktopTerminalProcessStub) Boundary() terminalruntime.ProcessBoundary {
	return terminalruntime.ProcessBoundary{
		UserOwned: true, JobAssignedAtCreation: true,
		KillOnJobClose: true, Persistent: true,
	}
}

func TestDesktopUserTerminalRequiresCurrentDebugBinding(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "desktop-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	workspaceRoot := filepath.Clean(t.TempDir())
	workspace := store.WorkspaceRecord{
		ID: "workspace-desktop-terminal", Name: "desktop-terminal",
		RootPath: workspaceRoot,
	}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{
			Goal: "debug through the user terminal", Profile: "code",
			WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	backend := &desktopTerminalBackendStub{}
	manager, err := terminalruntime.NewManager(backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	service, err := newDesktopUserTerminalService(state, manager)
	if err != nil {
		t.Fatal(err)
	}
	start := DesktopTerminalStartRequest{
		ProtocolVersion: DesktopUserTerminalProtocolVersion,
		RunID:           run.ID, Columns: 100, Rows: 30,
		ConfirmDebugBoundary: true,
	}
	if _, err := service.Start(ctx, start); err == nil {
		t.Fatal("preview interaction started a user terminal")
	}
	if _, err := application.NewRunExecutionProfileService(state).Change(ctx,
		application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "local",
			OperationKey: "desktop-terminal-profile-0001",
			RequestedBy:  "test_operator", Reason: "prepare local debug",
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionInteractionService(state).Change(ctx,
		application.ChangeRunExecutionInteractionRequest{
			RunID: run.ID, Mode: "debug", Trust: "trusted",
			OperationKey: "desktop-terminal-interaction-0001",
			RequestedBy:  "test_operator", Reason: "user-owned debug terminal",
			ConfirmWorkspaceTrust: true, ConfirmDebugBoundary: true,
		}); err != nil {
		t.Fatal(err)
	}
	session, err := service.Start(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	if session.RunID != run.ID || session.State != "running" ||
		!session.UserOwned || session.AgentInputDefault ||
		!session.JobAssignedAtCreation || !session.KillOnJobClose ||
		!session.Persistent || !session.ProcessLocal ||
		session.RawOutputPersisted {
		t.Fatalf("unexpected desktop terminal session: %#v", session)
	}
	written, err := service.Write(ctx, DesktopTerminalWriteRequest{
		ProtocolVersion: DesktopUserTerminalProtocolVersion,
		SessionID:       session.SessionID, Data: "go test\r", UserConfirmed: true,
	})
	if err != nil || written.BytesWritten != len("go test\r") {
		t.Fatalf("write result=%#v err=%v", written, err)
	}
	backend.process.mu.Lock()
	input := backend.process.input.String()
	backend.process.mu.Unlock()
	if input != "go test\r" {
		t.Fatalf("terminal input=%q", input)
	}
	if _, err := application.NewRunExecutionInteractionService(state).Change(ctx,
		application.ChangeRunExecutionInteractionRequest{
			RunID: run.ID, Mode: "preview", Trust: "untrusted",
			OperationKey: "desktop-terminal-interaction-0002",
			RequestedBy:  "test_operator", Reason: "leave debug mode",
		}); err != nil {
		t.Fatal(err)
	}
	if count := service.reconcileBindings(ctx); count != 1 {
		t.Fatalf("stale binding reconciliation closed %d sessions", count)
	}
	if _, err := service.Get(ctx, session.SessionID); err == nil {
		t.Fatal("terminal remained available after its interaction binding changed")
	}
	if _, err := application.NewRunExecutionInteractionService(state).Change(ctx,
		application.ChangeRunExecutionInteractionRequest{
			RunID: run.ID, Mode: "debug", Trust: "trusted",
			OperationKey: "desktop-terminal-interaction-0003",
			RequestedBy:  "test_operator", Reason: "return to debug mode",
			ConfirmWorkspaceTrust: true, ConfirmDebugBoundary: true,
		}); err != nil {
		t.Fatal(err)
	}
	second, err := service.Start(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(state).Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if count := service.reconcileBindings(ctx); count != 1 {
		t.Fatalf("terminal Run reconciliation closed %d sessions", count)
	}
	if _, err := service.Get(ctx, second.SessionID); err == nil {
		t.Fatal("terminal remained available after its Run terminated")
	}
	if count := service.reconcileBindings(ctx); count != 0 {
		t.Fatalf("terminal reconciliation was not idempotent: %d", count)
	}
}
