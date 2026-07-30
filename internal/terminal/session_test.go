package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
)

type terminalBackendStub struct {
	mu      sync.Mutex
	process *terminalProcessStub
}

func (b *terminalBackendStub) Name() string    { return "terminal-backend-stub" }
func (b *terminalBackendStub) Available() bool { return true }
func (b *terminalBackendStub) Start(_ context.Context,
	_ BackendStartRequest,
) (Process, error) {
	reader, writer := io.Pipe()
	process := &terminalProcessStub{
		reader: reader, writer: writer, wait: make(chan int, 1),
	}
	b.mu.Lock()
	b.process = process
	b.mu.Unlock()
	return process, nil
}

type terminalProcessStub struct {
	mu        sync.Mutex
	reader    *io.PipeReader
	writer    *io.PipeWriter
	wait      chan int
	input     bytes.Buffer
	columns   int
	rows      int
	closeOnce sync.Once
}

func (p *terminalProcessStub) Read(data []byte) (int, error) {
	return p.reader.Read(data)
}
func (p *terminalProcessStub) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.Write(data)
}
func (p *terminalProcessStub) Resize(columns int, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.columns, p.rows = columns, rows
	return nil
}
func (p *terminalProcessStub) Wait(ctx context.Context) (int, error) {
	select {
	case code := <-p.wait:
		return code, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func (p *terminalProcessStub) Close() error {
	p.closeOnce.Do(func() {
		_ = p.writer.Close()
		p.wait <- 0
	})
	return nil
}
func (p *terminalProcessStub) Boundary() ProcessBoundary {
	return ProcessBoundary{
		UserOwned: true, JobAssignedAtCreation: true,
		KillOnJobClose: true, Persistent: true,
	}
}
func (p *terminalProcessStub) emit(data string) {
	_, _ = p.writer.Write([]byte(data))
}
func (p *terminalProcessStub) inputString() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.String()
}

func TestUserTerminalOwnsLifecycleAndBoundsOutput(t *testing.T) {
	backend := &terminalBackendStub{}
	broker := executionauth.NewTerminalInputBroker()
	manager, err := NewManager(backend, broker)
	if err != nil {
		t.Fatal(err)
	}
	request := terminalStartTestRequest(t)
	session, err := manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != SessionRunning || !session.UserOwned ||
		session.AgentInputDefault || !session.JobAssignedAtCreation ||
		!session.ProcessLocal || session.RawOutputPersisted {
		t.Fatalf("unexpected terminal session: %+v", session)
	}
	backend.process.emit("ready\r\n")
	waitForTerminalOutput(t, manager, session.ID)
	page, err := manager.Read(session.ID, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(page.Data) != "ready\r\n" || page.Dropped {
		t.Fatalf("unexpected output page: %+v", page)
	}
	if _, err := manager.WriteUser(context.Background(), UserInputRequest{
		SessionID: session.ID, Data: []byte("go test\r"),
		RequestedBy: "test_operator", UserConfirmed: true,
	}); err != nil {
		t.Fatal(err)
	}
	if backend.process.inputString() != "go test\r" {
		t.Fatalf("user input=%q", backend.process.inputString())
	}
	if err := manager.Close(session.ID, "test_operator", true); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(session.ID); !errors.Is(err, ErrTerminalClosed) {
		t.Fatalf("closed session error=%v", err)
	}
}

func TestTerminalRejectsControlledAndCyberNativeStarts(t *testing.T) {
	manager, err := NewManager(&terminalBackendStub{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := terminalStartTestRequest(t)
	request.Scope.Mode = domain.RunExecutionInteractionControlled
	if _, err := manager.Start(context.Background(), request); !errors.Is(err, ErrTerminalBoundary) {
		t.Fatalf("controlled terminal error=%v", err)
	}
	request = terminalStartTestRequest(t)
	request.Scope.Mode = domain.RunExecutionInteractionCyber
	request.Interaction.Mode = domain.RunExecutionInteractionCyber
	if _, err := manager.Start(context.Background(), request); !errors.Is(err, ErrTerminalBoundary) {
		t.Fatalf("mismatched cyber terminal error=%v", err)
	}
}

func TestAgentInputBridgeRequiresExactLiveLease(t *testing.T) {
	backend := &terminalBackendStub{}
	broker := executionauth.NewTerminalInputBroker()
	manager, err := NewManager(backend, broker)
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Start(context.Background(),
		terminalStartTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := NewAgentInputBridge(manager, broker)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := bridge.Issue(context.Background(), IssueAgentInputRequest{
		SessionID: session.ID, RequestedBy: "test_operator",
		OperatorConfirmed: true, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := issued.Lease.Scope
	result, err := bridge.Write(context.Background(), AgentWriteRequest{
		Token: issued.Token, Scope: scope, Data: []byte("go version\r"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesWritten != len("go version\r") ||
		backend.process.inputString() != "go version\r" {
		t.Fatalf("unexpected Agent write: %+v input=%q",
			result, backend.process.inputString())
	}
	mismatched := scope
	mismatched.InteractionRevision++
	if _, err := bridge.Write(context.Background(), AgentWriteRequest{
		Token: issued.Token, Scope: mismatched, Data: []byte("whoami\r"),
	}); !errors.Is(err, executionauth.ErrLeaseDenied) {
		t.Fatalf("mismatched lease error=%v", err)
	}
	if count := manager.RevokeForLockOrSleep(); count != 1 {
		t.Fatalf("revoked leases=%d", count)
	}
	if _, err := bridge.Write(context.Background(), AgentWriteRequest{
		Token: issued.Token, Scope: scope, Data: []byte("whoami\r"),
	}); !errors.Is(err, executionauth.ErrLeaseRevoked) {
		t.Fatalf("revoked lease error=%v", err)
	}
	_ = manager.Shutdown()
}

func TestTerminalReplacementRevokesPreviousLease(t *testing.T) {
	backend := &terminalBackendStub{}
	broker := executionauth.NewTerminalInputBroker()
	manager, err := NewManager(backend, broker)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Start(context.Background(),
		terminalStartTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	bridge, _ := NewAgentInputBridge(manager, broker)
	issued, err := bridge.Issue(context.Background(), IssueAgentInputRequest{
		SessionID: first.ID, RequestedBy: "test_operator",
		OperatorConfirmed: true, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement := terminalStartTestRequest(t)
	replacement.ID = "terminal-replacement"
	replacement.ReplaceExisting = true
	if _, err := manager.Start(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Authorize(issued.Token, issued.Lease.Scope); !errors.Is(err, executionauth.ErrLeaseRevoked) {
		t.Fatalf("replacement lease error=%v", err)
	}
	_ = manager.Shutdown()
}

func TestExitedTerminalDoesNotConsumeActiveCapacity(t *testing.T) {
	backend := &terminalBackendStub{}
	manager, err := NewManager(backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxSessions; index++ {
		request := terminalStartTestRequestFor(t,
			fmt.Sprintf("exited-%d", index))
		session, startErr := manager.Start(context.Background(), request)
		if startErr != nil {
			t.Fatal(startErr)
		}
		backend.process.wait <- 0
		waitForTerminalState(t, manager, session.ID, SessionExited)
	}
	request := terminalStartTestRequestFor(t, "after-exited-capacity")
	if _, err := manager.Start(context.Background(), request); err != nil {
		t.Fatalf("exited sessions retained active capacity: %v", err)
	}
	_ = manager.Shutdown()
}

func TestOutputRingDropsOnlyOldestBytes(t *testing.T) {
	ring := outputRing{}
	ring.append(bytes.Repeat([]byte("a"), MaxTerminalOutputBytes))
	ring.append([]byte("bc"))
	data, base, next, dropped := ring.read(1, 4)
	if !dropped || base != 2 || next != 6 || string(data) != "aaaa" {
		t.Fatalf("unexpected ring page base=%d next=%d dropped=%t data=%q",
			base, next, dropped, data)
	}
}

func terminalStartTestRequest(t *testing.T) StartRequest {
	return terminalStartTestRequestFor(t, "test")
}

func terminalStartTestRequestFor(t *testing.T, suffix string) StartRequest {
	t.Helper()
	at := time.Date(2026, 7, 26, 14, 0, 0, 0, time.UTC)
	mission := domain.Mission{
		ID: "mission-terminal-" + suffix, Goal: "debug locally",
		Profile:     domain.ProfileCode,
		WorkspaceID: "workspace-terminal-" + suffix,
		Scope:       domain.DefaultScope("workspace-terminal-" + suffix),
		CreatedAt:   at, UpdatedAt: at,
	}
	run := domain.Run{
		ID: "run-terminal-" + suffix, MissionID: mission.ID,
		SessionID: "session-terminal-" + suffix,
		Status:    domain.RunCreated,
		Config:    domain.RunConfig{ModelRoute: "mock/default"},
		Budget:    domain.DefaultBudget(), CreatedAt: at, UpdatedAt: at,
	}
	mode, err := domain.NewInitialRunModeSnapshot("mode-terminal-"+suffix,
		run, mission,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		"test_operator", "code", at)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := domain.NewInitialRunExecutionProfileSnapshot(
		"profile-terminal-preview-"+suffix, run, mission,
		"test_operator", "preview", at)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := domain.NewInitialRunExecutionInteractionSnapshot(
		"interaction-terminal-preview-"+suffix, run, mission, mode, preview,
		"test_operator", at)
	if err != nil {
		t.Fatal(err)
	}
	local, err := preview.Next("profile-terminal-local-"+suffix,
		domain.RunExecutionProfileLocal, "test_operator", "local",
		at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	debug, err := initial.Next("interaction-terminal-debug-"+suffix,
		domain.RunExecutionInteractionDebug, mode, local,
		domain.WorkspaceTrustTrusted, true, "test_operator", "debug",
		at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	initialPermission, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"permission-terminal-conservative-"+suffix, run, mission,
		"test_operator", at)
	if err != nil {
		t.Fatal(err)
	}
	debugPermission, err := initialPermission.Next(
		"permission-terminal-debug-"+suffix,
		domain.RunExecutionPermissionDebug, true, "test_operator",
		"debug maximum access", at.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return StartRequest{
		ID: "terminal-" + suffix, Scope: SessionScope{
			WorkspaceID: mission.WorkspaceID, RunID: run.ID,
			InteractionSnapshotID: debug.ID,
			InteractionRevision:   debug.Revision, Mode: debug.Mode,
			ExecutionProfileRevision: local.Revision,
			PermissionSnapshotID:     debugPermission.ID,
			PermissionRevision:       debugPermission.Revision,
			PermissionMode:           debugPermission.Mode,
		},
		WorkspaceRoot: filepath.Clean(t.TempDir()),
		Interaction:   debug, CurrentProfile: local,
		CurrentPermission: debugPermission,
		Columns:           100, Rows: 30, RequestedBy: "test_operator",
		OperatorConfirmed: true,
	}
}

func waitForTerminalOutput(t *testing.T, manager *Manager, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		value, err := manager.Get(sessionID)
		if err == nil && value.OutputNextCursor > value.OutputBaseCursor {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal output was not observed")
}

func waitForTerminalState(t *testing.T, manager *Manager, sessionID string,
	state SessionState,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		value, err := manager.Get(sessionID)
		if err == nil && value.State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("terminal %s did not reach state %s", sessionID, state)
}
