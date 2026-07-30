package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	terminalruntime "cyberagent-workbench/internal/terminal"
)

type debugTerminalBackendStub struct {
	process *debugTerminalProcessStub
}

func (b *debugTerminalBackendStub) Name() string {
	return "debug-terminal-agent-test"
}

func (b *debugTerminalBackendStub) Available() bool {
	return true
}

func (b *debugTerminalBackendStub) Start(
	context.Context,
	terminalruntime.BackendStartRequest,
) (terminalruntime.Process, error) {
	reader, writer := io.Pipe()
	b.process = &debugTerminalProcessStub{
		reader: reader, writer: writer, wait: make(chan struct{}),
	}
	return b.process, nil
}

type debugTerminalProcessStub struct {
	mu        sync.Mutex
	reader    *io.PipeReader
	writer    *io.PipeWriter
	input     bytes.Buffer
	writes    int
	failWrite bool
	wait      chan struct{}
	closeOnce sync.Once
}

func (p *debugTerminalProcessStub) Read(data []byte) (int, error) {
	return p.reader.Read(data)
}

func (p *debugTerminalProcessStub) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	if p.failWrite {
		count := len(data) / 2
		_, _ = p.input.Write(data[:count])
		return count, errors.New("simulated partial terminal write")
	}
	return p.input.Write(data)
}

func (p *debugTerminalProcessStub) Resize(int, int) error {
	return nil
}

func (p *debugTerminalProcessStub) Wait(ctx context.Context) (int, error) {
	select {
	case <-p.wait:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (p *debugTerminalProcessStub) Close() error {
	p.closeOnce.Do(func() {
		_ = p.writer.Close()
		close(p.wait)
	})
	return nil
}

func (p *debugTerminalProcessStub) Boundary() terminalruntime.ProcessBoundary {
	return terminalruntime.ProcessBoundary{
		UserOwned: true, JobAssignedAtCreation: true,
		KillOnJobClose: true, Persistent: true,
	}
}

func (p *debugTerminalProcessStub) snapshot() (string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.String(), p.writes
}

type debugTerminalAgentFixture struct {
	state      *store.SQLiteStore
	service    *DebugTerminalAgentInputService
	manager    *terminalruntime.Manager
	process    *debugTerminalProcessStub
	run        domain.Run
	permission *RunExecutionPermissionService
	sessionID  string
}

func TestDebugTerminalAgentInputIsShortLivedPolicyCheckedAndExactlyOnce(
	t *testing.T,
) {
	fixture := newDebugTerminalAgentFixture(t, false)
	ctx := context.Background()
	if _, err := fixture.service.Grant(ctx,
		GrantDebugTerminalAgentInputRequest{
			ProtocolVersion: DebugTerminalAgentInputProtocolVersion,
			RunID:           fixture.run.ID, TerminalSessionID: fixture.sessionID,
			RequestedBy: "model", ConfirmDebugMaximumAccess: true,
			ConfirmAgentTerminalInput: true, TTL: time.Minute,
		}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("model grant error=%v code=%s", err, apperror.CodeOf(err))
	}
	binding, err := fixture.service.Grant(ctx,
		GrantDebugTerminalAgentInputRequest{
			ProtocolVersion: DebugTerminalAgentInputProtocolVersion,
			RunID:           fixture.run.ID, TerminalSessionID: fixture.sessionID,
			RequestedBy: "test_operator", ConfirmDebugMaximumAccess: true,
			ConfirmAgentTerminalInput: true, TTL: time.Minute,
		})
	if err != nil {
		t.Fatal(err)
	}
	if binding.ID == "" || binding.TokenExposed || binding.TokenPersisted ||
		binding.RawInputPersisted || binding.AgentInputDefault ||
		binding.AutomaticRetryAllowed ||
		binding.PermissionMode != domain.RunExecutionPermissionDebug {
		t.Fatalf("unsafe public binding: %#v", binding)
	}
	request := WriteDebugTerminalAgentInputRequest{
		ProtocolVersion: DebugTerminalAgentInputProtocolVersion,
		BindingID:       binding.ID,
		OperationKey:    "debug-terminal-operation-0001",
		Data:            []byte("go version\r"),
	}
	result, err := fixture.service.Write(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesWritten != len(request.Data) || result.Replayed ||
		result.TokenExposed || result.RawInputPersisted ||
		result.AutomaticRetryAllowed {
		t.Fatalf("unexpected write result: %#v", result)
	}
	replayed, err := fixture.service.Write(ctx, request)
	if err != nil || !replayed.Replayed ||
		replayed.OperationDigest != result.OperationDigest {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
	input, writes := fixture.process.snapshot()
	if input != "go version\r" || writes != 1 {
		t.Fatalf("input=%q writes=%d", input, writes)
	}
	conflict := request
	conflict.Data = []byte("go env\r")
	if _, err := fixture.service.Write(ctx, conflict); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("operation conflict error=%v code=%s",
			err, apperror.CodeOf(err))
	}
	multiline := request
	multiline.OperationKey = "debug-terminal-operation-0002"
	multiline.Data = []byte("go version\rwhoami\r")
	if _, err := fixture.service.Write(ctx, multiline); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("multiline error=%v code=%s", err, apperror.CodeOf(err))
	}
	dangerous := request
	dangerous.OperationKey = "debug-terminal-operation-0003"
	dangerous.Data = []byte("mass" + "can 0.0.0.0/0\r")
	if _, err := fixture.service.Write(ctx, dangerous); apperror.CodeOf(err) != apperror.CodePolicyDenied {
		t.Fatalf("dangerous input error=%v code=%s",
			err, apperror.CodeOf(err))
	}
	if _, err := fixture.permission.Change(ctx,
		ChangeRunExecutionPermissionRequest{
			RunID: fixture.run.ID, Mode: "conservative",
			OperationKey: "debug-terminal-permission-reset-0001",
			RequestedBy:  "test_operator", Reason: "leave debug access",
		}); err != nil {
		t.Fatal(err)
	}
	stale := request
	stale.OperationKey = "debug-terminal-operation-0004"
	stale.Data = []byte("go test\r")
	if _, err := fixture.service.Write(ctx, stale); apperror.CodeOf(err) != apperror.CodePolicyDenied {
		t.Fatalf("stale input error=%v code=%s", err, apperror.CodeOf(err))
	}
	after, afterWrites := fixture.process.snapshot()
	if after != input || afterWrites != writes {
		t.Fatalf("stale binding wrote input=%q writes=%d", after, afterWrites)
	}
	audit, err := fixture.state.ListRunEvents(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range audit {
		counts[event.Type]++
		if event.Type != events.DebugTerminalAgentInputGrantedEvent &&
			event.Type != events.DebugTerminalAgentInputPreparedEvent &&
			event.Type != events.DebugTerminalAgentInputCompletedEvent &&
			event.Type != events.DebugTerminalAgentInputRevokedEvent {
			continue
		}
		if strings.Contains(event.PayloadJSON, "go version") ||
			strings.Contains(event.PayloadJSON, "masscan") ||
			strings.Contains(strings.ToLower(event.PayloadJSON), "token") &&
				!strings.Contains(event.PayloadJSON, `"token_exposed":false`) &&
				!strings.Contains(event.PayloadJSON, `"token_persisted":false`) {
			t.Fatalf("raw input or bearer leaked into audit: %s", event.PayloadJSON)
		}
	}
	for eventType, want := range map[string]int{
		events.DebugTerminalAgentInputGrantedEvent:   1,
		events.DebugTerminalAgentInputPreparedEvent:  1,
		events.DebugTerminalAgentInputCompletedEvent: 1,
		events.DebugTerminalAgentInputRevokedEvent:   1,
	} {
		if counts[eventType] != want {
			t.Fatalf("event %s count=%d want=%d", eventType, counts[eventType], want)
		}
	}
}

func TestDebugTerminalAgentInputDisablesRetryAfterAmbiguousWrite(t *testing.T) {
	fixture := newDebugTerminalAgentFixture(t, true)
	ctx := context.Background()
	binding, err := fixture.service.Grant(ctx,
		GrantDebugTerminalAgentInputRequest{
			ProtocolVersion: DebugTerminalAgentInputProtocolVersion,
			RunID:           fixture.run.ID, TerminalSessionID: fixture.sessionID,
			RequestedBy: "test_operator", ConfirmDebugMaximumAccess: true,
			ConfirmAgentTerminalInput: true, TTL: time.Minute,
		})
	if err != nil {
		t.Fatal(err)
	}
	request := WriteDebugTerminalAgentInputRequest{
		ProtocolVersion: DebugTerminalAgentInputProtocolVersion,
		BindingID:       binding.ID,
		OperationKey:    "debug-terminal-ambiguous-operation-0001",
		Data:            []byte("go test\r"),
	}
	if _, err := fixture.service.Write(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("ambiguous write error=%v code=%s", err, apperror.CodeOf(err))
	}
	if _, err := fixture.service.Write(ctx, request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("ambiguous replay error=%v code=%s", err, apperror.CodeOf(err))
	}
	_, writes := fixture.process.snapshot()
	if writes != 1 {
		t.Fatalf("ambiguous operation executed %d times", writes)
	}
	audit, err := fixture.state.ListRunEvents(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, completed := 0, 0
	for _, event := range audit {
		if event.Type == events.DebugTerminalAgentInputPreparedEvent {
			prepared++
		}
		if event.Type == events.DebugTerminalAgentInputCompletedEvent {
			completed++
		}
	}
	if prepared != 1 || completed != 0 {
		t.Fatalf("ambiguous audit prepared=%d completed=%d", prepared, completed)
	}
}

func newDebugTerminalAgentFixture(t *testing.T,
	failWrite bool,
) debugTerminalAgentFixture {
	t.Helper()
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "debug-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	workspace := store.WorkspaceRecord{
		ID: "workspace-debug-terminal-agent", Name: "debug-terminal-agent",
		RootPath: filepath.Clean(t.TempDir()),
	}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := NewRunService(state).Create(ctx, CreateRunRequest{
		Goal: "debug with a bounded Agent terminal lease", Profile: "code",
		WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunExecutionProfileService(state).Change(ctx,
		ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "local",
			OperationKey: "debug-terminal-profile-0001",
			RequestedBy:  "test_operator", Reason: "local debug terminal",
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunExecutionInteractionService(state).Change(ctx,
		ChangeRunExecutionInteractionRequest{
			RunID: run.ID, Mode: "debug", Trust: "trusted",
			OperationKey: "debug-terminal-interaction-0001",
			RequestedBy:  "test_operator", Reason: "debug interaction",
			ConfirmWorkspaceTrust: true, ConfirmDebugBoundary: true,
		}); err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
	}
	permissionService := NewRunExecutionPermissionService(state, capabilities)
	if _, err := permissionService.Change(ctx,
		ChangeRunExecutionPermissionRequest{
			RunID: run.ID, Mode: "debug",
			OperationKey: "debug-terminal-permission-0001",
			RequestedBy:  "test_operator", Reason: "debug maximum access",
			ConfirmDebugAccess: true,
		}); err != nil {
		t.Fatal(err)
	}
	profile, err := state.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := state.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	broker := executionauth.NewTerminalInputBroker()
	backend := &debugTerminalBackendStub{}
	manager, err := terminalruntime.NewManager(backend, broker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	sessionID := "terminal-debug-agent-input"
	if _, err := manager.Start(ctx, terminalruntime.StartRequest{
		ID: sessionID,
		Scope: terminalruntime.SessionScope{
			WorkspaceID: workspace.ID, RunID: run.ID,
			InteractionSnapshotID:    interaction.ID,
			InteractionRevision:      interaction.Revision,
			ExecutionProfileRevision: profile.Revision,
			PermissionSnapshotID:     permission.ID,
			PermissionRevision:       permission.Revision,
			PermissionMode:           permission.Mode, Mode: interaction.Mode,
		},
		WorkspaceRoot: workspace.RootPath, Interaction: interaction,
		CurrentProfile: profile, CurrentPermission: permission,
		Columns: 100, Rows: 30, RequestedBy: "test_operator",
		OperatorConfirmed: true,
	}); err != nil {
		t.Fatal(err)
	}
	backend.process.failWrite = failWrite
	bridge, err := terminalruntime.NewAgentInputBridge(manager, broker)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewDebugTerminalAgentInputService(
		state, bridge, policy.NewDefaultChecker(), capabilities, true)
	if err != nil {
		t.Fatal(err)
	}
	return debugTerminalAgentFixture{
		state: state, service: service, manager: manager,
		process: backend.process, run: run, permission: permissionService,
		sessionID: sessionID,
	}
}
