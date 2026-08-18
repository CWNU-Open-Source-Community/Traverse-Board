package desktop

import (
	"context"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

type desktopDebugTerminalAgentControllerStub struct {
	grantRequest  application.GrantDebugTerminalAgentInputRequest
	revokeRequest application.RevokeDebugTerminalAgentInputRequest
	binding       application.DebugTerminalAgentInputBinding
	found         bool
	grantCalls    int
}

func (s *desktopDebugTerminalAgentControllerStub) Grant(_ context.Context,
	request application.GrantDebugTerminalAgentInputRequest,
) (application.DebugTerminalAgentInputBinding, error) {
	s.grantCalls++
	s.grantRequest = request
	return s.binding, nil
}

func (s *desktopDebugTerminalAgentControllerStub) Write(context.Context,
	application.WriteDebugTerminalAgentInputRequest,
) (application.DebugTerminalAgentInputWriteResult, error) {
	return application.DebugTerminalAgentInputWriteResult{}, nil
}

func (s *desktopDebugTerminalAgentControllerStub) Read(context.Context,
	application.ReadDebugTerminalAgentOutputRequest,
) (application.DebugTerminalAgentOutputResult, error) {
	return application.DebugTerminalAgentOutputResult{}, nil
}

func (s *desktopDebugTerminalAgentControllerStub) Active(context.Context,
	string,
) (application.DebugTerminalAgentInputBinding, bool, error) {
	return s.binding, s.found, nil
}

func (s *desktopDebugTerminalAgentControllerStub) Revoke(_ context.Context,
	request application.RevokeDebugTerminalAgentInputRequest,
) error {
	s.revokeRequest = request
	s.found = false
	return nil
}

func (s *desktopDebugTerminalAgentControllerStub) Reconcile(context.Context) int { return 0 }
func (s *desktopDebugTerminalAgentControllerStub) Shutdown(context.Context) int  { return 0 }

func TestDesktopDebugTerminalAgentInputKeepsBearerInsideGo(t *testing.T) {
	now := time.Now().UTC()
	controller := &desktopDebugTerminalAgentControllerStub{
		found: true,
		binding: application.DebugTerminalAgentInputBinding{
			ID: "terminal-input-binding-desktop", RunID: "run-desktop-debug",
			TerminalSessionID: "terminal-desktop-debug",
			IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
			ProcessLocal: true,
		},
	}
	selector, preview := NewSkillPackagePreviewBoundary()
	bridge, err := NewDesktopBridge(DesktopBridgeConfig{
		ContextProvider: func() context.Context { return context.Background() },
		FilePicker:      &testSkillPackagePicker{},
		ReadToken:       testDesktopReadToken,
		ControlToken:    testDesktopControlToken,
		ExecutionPermissionControlEnabled: true,
		OperatorApprovalEnabled:           true,
		DangerFullAccessEnabled:           true,
		DebugMaximumAccessEnabled:         true,
		UserTerminalEnabled:               true,
		APIVersion: "api.v1", AppVersion: "test", UIDigest: testDesktopUIDigest,
		Selector: selector, PreviewBridge: preview,
		UserTerminalController:            &testUserTerminalController{},
		DebugTerminalAgentInputController: controller,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalid := DesktopDebugTerminalAgentInputGrantRequest{
		ProtocolVersion: DesktopDebugTerminalAgentInputProtocolVersion,
		RunID: "run-desktop-debug", TerminalSessionID: "terminal-desktop-debug",
		TTLSeconds: 14, ConfirmDebugMaximumAccess: true,
		ConfirmAgentTerminalInput: true,
	}
	if _, err := bridge.GrantDebugTerminalAgentInput(invalid); apperror.CodeOf(err) !=
		apperror.CodeInvalidArgument || controller.grantCalls != 0 {
		t.Fatalf("invalid TTL reached controller: calls=%d err=%v",
			controller.grantCalls, err)
	}
	request := invalid
	request.TTLSeconds = 300
	binding, err := bridge.GrantDebugTerminalAgentInput(request)
	if err != nil {
		t.Fatal(err)
	}
	if binding.BindingID != controller.binding.ID || !binding.ProcessLocal ||
		binding.TokenExposed || binding.RawInputPersisted ||
		controller.grantRequest.RequestedBy != "desktop_operator" ||
		controller.grantRequest.TTL != 5*time.Minute ||
		!controller.grantRequest.ConfirmDebugMaximumAccess ||
		!controller.grantRequest.ConfirmAgentTerminalInput {
		t.Fatalf("unsafe projection=%#v request=%#v", binding,
			controller.grantRequest)
	}
	queried, err := bridge.GetDebugTerminalAgentInput(request.RunID)
	if err != nil || queried != binding {
		t.Fatalf("queried=%#v err=%v", queried, err)
	}
	if err := bridge.RevokeDebugTerminalAgentInput(
		DesktopDebugTerminalAgentInputRevokeRequest{
			ProtocolVersion: DesktopDebugTerminalAgentInputProtocolVersion,
			BindingID: binding.BindingID, OperatorConfirmed: true,
		}); err != nil {
		t.Fatal(err)
	}
	if controller.revokeRequest.RequestedBy != "desktop_operator" ||
		!controller.revokeRequest.OperatorConfirmed {
		t.Fatalf("revoke request=%#v", controller.revokeRequest)
	}
}

var _ application.DebugTerminalAgentInputController =
	(*desktopDebugTerminalAgentControllerStub)(nil)
