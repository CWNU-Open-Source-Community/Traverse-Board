package terminal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/toolcontract"
)

type terminalBackendStub struct {
	mu      sync.Mutex
	process *terminalProcessStub
	onStart func()
}

func (b *terminalBackendStub) Name() string    { return "terminal-backend-stub" }
func (b *terminalBackendStub) Available() bool { return true }
func (b *terminalBackendStub) Start(_ context.Context,
	_ BackendStartRequest,
) (Process, error) {
	reader, writer := io.Pipe()
	process := &terminalProcessStub{
		reader: reader, writer: writer,
		wait: make(chan terminalProcessWaitResult, 1),
	}
	b.mu.Lock()
	b.process = process
	b.mu.Unlock()
	if b.onStart != nil {
		b.onStart()
	}
	return process, nil
}

type terminalProcessWaitResult struct {
	code int
	err  error
}

type terminalProcessStub struct {
	mu         sync.Mutex
	reader     *io.PipeReader
	writer     *io.PipeWriter
	wait       chan terminalProcessWaitResult
	input      bytes.Buffer
	columns    int
	rows       int
	closeOnce  sync.Once
	writeErr   error
	writeLimit int
	writes     int
}

func (p *terminalProcessStub) Read(data []byte) (int, error) {
	return p.reader.Read(data)
}
func (p *terminalProcessStub) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	if p.writeLimit > 0 && len(data) > p.writeLimit {
		data = data[:p.writeLimit]
	}
	n, err := p.input.Write(data)
	if p.writeErr != nil {
		err = p.writeErr
	}
	return n, err
}
func (p *terminalProcessStub) Resize(columns int, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.columns, p.rows = columns, rows
	return nil
}
func (p *terminalProcessStub) Wait(ctx context.Context) (int, error) {
	select {
	case result := <-p.wait:
		return result.code, result.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func (p *terminalProcessStub) Close() error {
	p.closeOnce.Do(func() {
		_ = p.writer.Close()
		p.wait <- terminalProcessWaitResult{}
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

func TestTerminalBackendCrashMarksSessionFailedAndDeniesAgentInput(t *testing.T) {
	backend := &terminalBackendStub{}
	broker := executionauth.NewTerminalInputBroker()
	manager, err := NewManager(backend, broker)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	request := terminalStartTestRequest(t)
	session, err := manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != SessionRunning {
		t.Fatalf("state=%s", session.State)
	}
	backend.mu.Lock()
	process := backend.process
	backend.mu.Unlock()
	_ = process.writer.CloseWithError(io.ErrUnexpectedEOF)
	process.wait <- terminalProcessWaitResult{err: io.ErrUnexpectedEOF}
	deadline := time.Now().Add(3 * time.Second)
	for {
		session, err = manager.Get(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if session.State == SessionFailed || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if session.State != SessionFailed {
		t.Fatalf("crashed session state=%s, want failed", session.State)
	}
	bridge, err := NewAgentInputBridge(manager, broker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Issue(context.Background(), IssueAgentInputRequest{
		SessionID: session.ID, RequestedBy: "desktop_operator",
		OperatorConfirmed: true, TTL: time.Minute,
	}); !errors.Is(err, ErrAgentInputBridgeDenied) {
		t.Fatalf("failed session granted agent input: %v", err)
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
		backend.process.wait <- terminalProcessWaitResult{}
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
		"permission-terminal-full-"+suffix,
		domain.RunExecutionPermissionFull, true, "test_operator",
		"explicit Full preference", at.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	request := StartRequest{
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
	request.Authorizer = newTerminalAuthorityFixture(t, request, true).authorizer()
	return request
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

// This fixture uses the production common policy and runtime authority. The
// resolver's mutable state stands in for the host's current database bindings.
type terminalAuthorityFixture struct {
	mu        sync.Mutex
	request   StartRequest
	authority *domain.ExecutionPermissionRuntimeAuthority
	available bool
	calls     int
	onResolve func(int)
}

func newTerminalAuthorityFixture(t *testing.T, request StartRequest, activate bool) *terminalAuthorityFixture {
	t.Helper()
	f := &terminalAuthorityFixture{request: request,
		authority: domain.NewExecutionPermissionRuntimeAuthority(), available: true}
	if activate {
		if _, err := f.authority.ActivateRunFullAccess(request.CurrentPermission); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *terminalAuthorityFixture) authorizer() *executionauth.PolicyAuthorizer {
	return executionauth.NewPolicyAuthorizer(func(ctx context.Context, subject executionauth.SubjectRef,
		operation toolcontract.Operation, approvalRef string,
	) (executionauth.OperationAuthority, error) {
		if err := ctx.Err(); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		f.mu.Lock()
		f.calls++
		call, hook := f.calls, f.onResolve
		f.mu.Unlock()
		if hook != nil {
			hook(call)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		request := f.request
		if approvalRef != "" || subject.RunID != request.Scope.RunID ||
			subject.ActorID != request.RequestedBy || operation.ToolID != "user_terminal" {
			return executionauth.OperationAuthority{}, ErrTerminalDenied
		}
		generation, active := f.authority.AllowsFullAccess(request.CurrentPermission)
		raw, err := json.Marshal(struct {
			Scope       SessionScope
			Root        string
			Profile     domain.RunExecutionProfileSnapshot
			Interaction domain.RunExecutionInteractionSnapshot
			Permission  domain.RunExecutionPermissionSnapshot
			Epoch       string
			Generation  uint64
		}{request.Scope, request.WorkspaceRoot, request.CurrentProfile, request.Interaction,
			request.CurrentPermission, f.authority.RuntimeEpoch(), generation})
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		sum := sha256.Sum256(raw)
		mode, err := domain.ParseExecutionApprovalMode(string(request.CurrentPermission.Mode))
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		return executionauth.OperationAuthority{Mode: mode, BindingFingerprint: hex.EncodeToString(sum[:]),
			RuntimeAvailable: f.available, FullActivated: active, EffectsVerified: false}, nil
	})
}

func TestTerminalCurrentFullRequiresLiveHostAuthorityAtDispatch(t *testing.T) {
	for _, name := range []string{"missing_authorizer", "cold_full", "revoked_before_dispatch", "cancelled_before_dispatch"} {
		t.Run(name, func(t *testing.T) {
			backend := &terminalBackendStub{}
			manager, err := NewManager(backend, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown()
			request := terminalStartTestRequest(t)
			if request.CurrentPermission.PersistentTerminal || request.CurrentPermission.BackgroundProcess ||
				request.CurrentPermission.AgentTerminalInput || request.CurrentPermission.ProcessEnabled ||
				request.CurrentPermission.ExecutionAuthorized || request.CurrentPermission.CapabilityGrant {
				t.Fatal("current Full fixture persisted execution authority")
			}
			f := newTerminalAuthorityFixture(t, request, name != "cold_full")
			request.Authorizer = f.authorizer()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "missing_authorizer":
				request.Authorizer = nil
			case "revoked_before_dispatch":
				f.onResolve = func(call int) {
					if call == 2 {
						f.authority.RevokeRun(request.Scope.RunID)
					}
				}
			case "cancelled_before_dispatch":
				f.onResolve = func(call int) {
					if call == 2 {
						cancel()
					}
				}
			}
			if _, err := manager.Start(ctx, request); err == nil {
				t.Fatal("unavailable authority started a terminal")
			}
			if backend.process != nil {
				t.Fatal("denied start reached the native backend")
			}
		})
	}
}

func TestTerminalCurrentFullPinsLiveBindingsBeforeEveryNativeMutation(t *testing.T) {
	mutations := map[string]func(*terminalAuthorityFixture){
		"runtime_revoked": func(f *terminalAuthorityFixture) { f.authority.RevokeRun(f.request.Scope.RunID) },
		"reactivated_same_snapshot": func(f *terminalAuthorityFixture) {
			f.authority.RevokeRun(f.request.Scope.RunID)
			if _, err := f.authority.ActivateRunFullAccess(f.request.CurrentPermission); err != nil {
				t.Fatal(err)
			}
		},
		"cold_restart": func(f *terminalAuthorityFixture) { f.authority = domain.NewExecutionPermissionRuntimeAuthority() },
		"cold_restart_reactivated": func(f *terminalAuthorityFixture) {
			f.authority = domain.NewExecutionPermissionRuntimeAuthority()
			if _, err := f.authority.ActivateRunFullAccess(f.request.CurrentPermission); err != nil {
				t.Fatal(err)
			}
		},
		"permission_revision":  func(f *terminalAuthorityFixture) { f.request.CurrentPermission.Revision++ },
		"profile_revision":     func(f *terminalAuthorityFixture) { f.request.CurrentProfile.Revision++ },
		"interaction_revision": func(f *terminalAuthorityFixture) { f.request.Interaction.Revision++ },
		"workspace_root":       func(f *terminalAuthorityFixture) { f.request.WorkspaceRoot += "-changed" },
		"runtime_unavailable":  func(f *terminalAuthorityFixture) { f.available = false },
	}
	for name, mutate := range mutations {
		for _, action := range []string{"user_write", "agent_write", "resize", "reconcile"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				backend := &terminalBackendStub{}
				broker := executionauth.NewTerminalInputBroker()
				manager, err := NewManager(backend, broker)
				if err != nil {
					t.Fatal(err)
				}
				defer manager.Shutdown()
				request := terminalStartTestRequest(t)
				f := newTerminalAuthorityFixture(t, request, true)
				request.Authorizer = f.authorizer()
				session, err := manager.Start(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				bridge, err := NewAgentInputBridge(manager, broker)
				if err != nil {
					t.Fatal(err)
				}
				issued, err := bridge.Issue(context.Background(), IssueAgentInputRequest{
					SessionID: session.ID, RequestedBy: request.RequestedBy, OperatorConfirmed: true})
				if err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				mutate(f)
				f.mu.Unlock()
				switch action {
				case "user_write":
					_, err = manager.WriteUser(context.Background(), UserInputRequest{
						SessionID: session.ID, Data: []byte("must not execute\r"), RequestedBy: request.RequestedBy, UserConfirmed: true})
				case "agent_write":
					_, err = bridge.Write(context.Background(), AgentWriteRequest{Token: issued.Token,
						Scope: issued.Lease.Scope, Data: []byte("must not execute\r")})
				case "resize":
					err = manager.Resize(session.ID, 120, 40, request.RequestedBy, true)
				case "reconcile":
					if closed := manager.ReconcileBindings(context.Background()); closed != 1 {
						t.Fatalf("closed=%d", closed)
					}
					if closed := manager.ReconcileBindings(context.Background()); closed != 0 {
						t.Fatalf("second closed=%d", closed)
					}
				}
				if action != "reconcile" && !errors.Is(err, ErrTerminalDenied) {
					t.Fatalf("mutation error=%v", err)
				}
				if got := backend.process.inputString(); got != "" {
					t.Fatalf("native input received %q", got)
				}
				backend.process.mu.Lock()
				columns, rows := backend.process.columns, backend.process.rows
				backend.process.mu.Unlock()
				if columns != 0 || rows != 0 {
					t.Fatal("denied resize reached the native backend")
				}
				if _, err := broker.Authorize(issued.Token, issued.Lease.Scope); !errors.Is(err, executionauth.ErrLeaseRevoked) {
					t.Fatalf("lost binding did not revoke lease: %v", err)
				}
			})
		}
	}
}

func TestTerminalNativeWriteFreezesInputAndRechecksDispatch(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprintf("revoke_%t", revoke), func(t *testing.T) {
			backend := &terminalBackendStub{}
			manager, err := NewManager(backend, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown()
			request := terminalStartTestRequest(t)
			f := newTerminalAuthorityFixture(t, request, true)
			request.Authorizer = f.authorizer()
			session, err := manager.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("original\r")
			f.mu.Lock()
			f.calls = 0
			f.onResolve = func(call int) {
				if call == 2 {
					copy(data, []byte("mutated!\r"))
				}
				if call == 3 && revoke {
					f.authority.RevokeRun(request.Scope.RunID)
				}
			}
			f.mu.Unlock()
			n, err := manager.WriteUser(context.Background(), UserInputRequest{
				SessionID: session.ID, Data: data, RequestedBy: request.RequestedBy, UserConfirmed: true})
			if revoke {
				if !errors.Is(err, ErrTerminalDenied) || n != 0 || backend.process.inputString() != "" {
					t.Fatalf("revoked input n=%d err=%v native=%q", n, err, backend.process.inputString())
				}
			} else if err != nil || n != len("original\r") || backend.process.inputString() != "original\r" {
				t.Fatalf("frozen input n=%d err=%v native=%q", n, err, backend.process.inputString())
			}
		})
	}
}

func TestTerminalLegacyDebugRemainsReadableButCannotStart(t *testing.T) {
	backend := &terminalBackendStub{}
	manager, err := NewManager(backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	request := terminalStartTestRequest(t)
	legacy, err := request.CurrentPermission.Next("permission-legacy-debug",
		domain.RunExecutionPermissionDebug, true, "test_operator", "historical Debug fixture",
		request.CurrentPermission.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	request.CurrentPermission = legacy
	request.Scope.PermissionSnapshotID = legacy.ID
	request.Scope.PermissionRevision = legacy.Revision
	request.Scope.PermissionMode = legacy.Mode
	if err := request.Scope.Validate(); err != nil {
		t.Fatalf("legacy scope lost read support: %v", err)
	}
	if _, err := manager.Start(context.Background(), request); !errors.Is(err, ErrTerminalBoundary) {
		t.Fatalf("historical Debug launched a new terminal: %v", err)
	}
	if backend.process != nil {
		t.Fatal("historical Debug reached native start")
	}
}

func TestTerminalNativePartialInputIsReportedWithoutRetry(t *testing.T) {
	backend := &terminalBackendStub{}
	manager, err := NewManager(backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	session, err := manager.Start(context.Background(), terminalStartTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	backend.process.mu.Lock()
	backend.process.writeLimit = 3
	backend.process.writeErr = io.ErrUnexpectedEOF
	backend.process.mu.Unlock()
	n, err := manager.WriteUser(context.Background(), UserInputRequest{
		SessionID: session.ID, Data: []byte("command\r"), RequestedBy: "test_operator", UserConfirmed: true})
	if n != 3 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial result n=%d err=%v", n, err)
	}
	backend.process.mu.Lock()
	writes, input := backend.process.writes, backend.process.input.String()
	backend.process.mu.Unlock()
	if writes != 1 || input != "com" {
		t.Fatalf("partial input retried: writes=%d input=%q", writes, input)
	}
}

func TestTerminalBindingLossDuringNativeStartClosesUnpublishedProcess(t *testing.T) {
	backend := &terminalBackendStub{}
	manager, err := NewManager(backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	request := terminalStartTestRequest(t)
	f := newTerminalAuthorityFixture(t, request, true)
	request.Authorizer = f.authorizer()
	backend.onStart = func() { f.authority.RevokeRun(request.Scope.RunID) }
	if _, err := manager.Start(context.Background(), request); !errors.Is(err, ErrTerminalDenied) {
		t.Fatalf("binding loss during native start: %v", err)
	}
	if backend.process == nil {
		t.Fatal("fixture did not reach native start")
	}
	if len(manager.ActiveSessions()) != 0 {
		t.Fatal("revoked native process was published")
	}
	select {
	case <-backend.process.wait:
	default:
		t.Fatal("revoked native process was not closed")
	}
}

func TestTerminalRejectsNonFullPermissionEvenWithHostAuthorizer(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFullAccess,
	} {
		t.Run(string(mode), func(t *testing.T) {
			backend := &terminalBackendStub{}
			manager, err := NewManager(backend, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown()
			request := terminalStartTestRequest(t)
			permission, err := request.CurrentPermission.Next("permission-other", mode,
				mode == domain.RunExecutionPermissionFullAccess, "test_operator", "non-Full fixture",
				request.CurrentPermission.CreatedAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			request.CurrentPermission = permission
			request.Scope.PermissionSnapshotID = permission.ID
			request.Scope.PermissionRevision = permission.Revision
			request.Scope.PermissionMode = permission.Mode
			if _, err := manager.Start(context.Background(), request); !errors.Is(err, ErrTerminalBoundary) {
				t.Fatalf("non-Full native terminal: %v", err)
			}
			if backend.process != nil {
				t.Fatal("non-Full reached native start")
			}
		})
	}
}

func TestTerminalCancelledMutationDoesNotRevokeValidUserSession(t *testing.T) {
	backend := &terminalBackendStub{}
	manager, err := NewManager(backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown()
	request := terminalStartTestRequest(t)
	f := newTerminalAuthorityFixture(t, request, true)
	request.Authorizer = f.authorizer()
	session, err := manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.mu.Lock()
	f.calls = 0
	f.onResolve = func(call int) {
		if call == 2 {
			cancel()
		}
	}
	f.mu.Unlock()
	if n, err := manager.WriteUser(ctx, UserInputRequest{SessionID: session.ID, Data: []byte("cancelled\r"),
		RequestedBy: request.RequestedBy, UserConfirmed: true}); n != 0 || err == nil {
		t.Fatalf("cancelled input n=%d err=%v", n, err)
	}
	if backend.process.inputString() != "" {
		t.Fatal("cancelled input reached native process")
	}
	if current, err := manager.Get(session.ID); err != nil || current.State != SessionRunning {
		t.Fatalf("request cancellation revoked valid user terminal: %#v %v", current, err)
	}
	f.mu.Lock()
	f.onResolve = nil
	f.mu.Unlock()
	if _, err := manager.WriteUser(context.Background(), UserInputRequest{SessionID: session.ID,
		Data: []byte("later\r"), RequestedBy: request.RequestedBy, UserConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	if backend.process.inputString() != "later\r" {
		t.Fatal("valid later input did not reach existing terminal")
	}
}

func TestAgentInputBridgeCancellationBeforeNativeWrite(t *testing.T) {
	for _, cancelAt := range []int{1, 3} {
		t.Run(fmt.Sprintf("cancel_at_native_check_%d", cancelAt), func(t *testing.T) {
			backend := &terminalBackendStub{}
			broker := executionauth.NewTerminalInputBroker()
			manager, err := NewManager(backend, broker)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Shutdown()
			request := terminalStartTestRequest(t)
			authority := newTerminalAuthorityFixture(t, request, true)
			request.Authorizer = authority.authorizer()
			session, err := manager.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := NewAgentInputBridge(manager, broker)
			if err != nil {
				t.Fatal(err)
			}
			issued, err := bridge.Issue(context.Background(), IssueAgentInputRequest{
				SessionID: session.ID, RequestedBy: request.RequestedBy, OperatorConfirmed: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authority.mu.Lock()
			authority.calls = 0
			authority.onResolve = func(call int) {
				// This resolver is reached only after Bridge.Write has validated
				// the exact live lease and matched the current terminal session.
				if call == cancelAt {
					cancel()
				}
			}
			authority.mu.Unlock()
			result, writeErr := bridge.Write(ctx, AgentWriteRequest{
				Token: issued.Token, Scope: issued.Lease.Scope, Data: []byte("cancelled-agent-input\r"),
			})
			nativeInput := backend.process.inputString()
			backend.process.mu.Lock()
			nativeWrites := backend.process.writes
			backend.process.mu.Unlock()
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("fixture did not cancel the original request")
			}
			t.Logf("request_error=%v result_bytes=%d write_error=%v native_writes=%d native_input=%q",
				ctx.Err(), result.BytesWritten, writeErr, nativeWrites, nativeInput)
			if writeErr == nil || result.BytesWritten != 0 || nativeWrites != 0 || nativeInput != "" {
				t.Fatalf("cancelled Agent request reached native input: result=%#v err=%v writes=%d input=%q",
					result, writeErr, nativeWrites, nativeInput)
			}
			current, err := manager.Get(session.ID)
			if err != nil || current.State != SessionRunning {
				t.Fatalf("request cancellation revoked the valid user-owned session: state=%#v err=%v", current, err)
			}
			if _, err := broker.Authorize(issued.Token, issued.Lease.Scope); err != nil {
				t.Fatalf("request cancellation revoked the valid input lease: %v", err)
			}
			authority.mu.Lock()
			authority.onResolve = nil
			authority.mu.Unlock()
			later, err := bridge.Write(context.Background(), AgentWriteRequest{
				Token: issued.Token, Scope: issued.Lease.Scope, Data: []byte("later-agent-input\r"),
			})
			if err != nil || later.BytesWritten != len("later-agent-input\r") ||
				backend.process.inputString() != "later-agent-input\r" {
				t.Fatalf("fresh request could not use still-valid terminal and lease: result=%#v err=%v", later, err)
			}
		})
	}
}
