package desktop

import (
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/executionauth"
)

const DesktopDebugTerminalAgentInputProtocolVersion = "desktop_debug_terminal_agent_input.v1"

type DesktopDebugTerminalAgentInputGrantRequest struct {
	ProtocolVersion           string `json:"protocol_version"`
	RunID                     string `json:"run_id"`
	TerminalSessionID         string `json:"terminal_session_id"`
	TTLSeconds                int    `json:"ttl_seconds"`
	ConfirmDebugMaximumAccess bool   `json:"confirm_debug_maximum_access"`
	ConfirmAgentTerminalInput bool   `json:"confirm_agent_terminal_input"`
}

type DesktopDebugTerminalAgentInputRevokeRequest struct {
	ProtocolVersion   string `json:"protocol_version"`
	BindingID         string `json:"binding_id"`
	OperatorConfirmed bool   `json:"operator_confirmed"`
}

type DesktopDebugTerminalAgentInputBinding struct {
	ProtocolVersion   string `json:"protocol_version"`
	BindingID         string `json:"binding_id"`
	RunID             string `json:"run_id"`
	TerminalSessionID string `json:"terminal_session_id"`
	IssuedAt          string `json:"issued_at"`
	ExpiresAt         string `json:"expires_at"`
	ProcessLocal      bool   `json:"process_local"`
	TokenExposed      bool   `json:"token_exposed"`
	RawInputPersisted bool   `json:"raw_input_persisted"`
}

func (b *DesktopBridge) GrantDebugTerminalAgentInput(
	request DesktopDebugTerminalAgentInputGrantRequest,
) (DesktopDebugTerminalAgentInputBinding, error) {
	if b == nil || !b.bootstrap.UserTerminalEnabled ||
		!b.bootstrap.DebugMaximumAccessEnabled ||
		b.debugTerminalAgentInput == nil {
		return DesktopDebugTerminalAgentInputBinding{}, apperror.New(
			apperror.CodeNotFound,
			"desktop Debug terminal Agent input is disabled")
	}
	if request.ProtocolVersion != DesktopDebugTerminalAgentInputProtocolVersion ||
		request.TTLSeconds < int(executionauth.MinTerminalInputLeaseTTL/time.Second) ||
		request.TTLSeconds > int(executionauth.MaxTerminalInputLeaseTTL/time.Second) {
		return DesktopDebugTerminalAgentInputBinding{}, apperror.New(
			apperror.CodeInvalidArgument,
			"desktop Debug terminal Agent-input grant is invalid")
	}
	ctx, err := b.lifecycleContext()
	if err != nil {
		return DesktopDebugTerminalAgentInputBinding{}, err
	}
	binding, err := b.debugTerminalAgentInput.Grant(ctx,
		application.GrantDebugTerminalAgentInputRequest{
			ProtocolVersion: application.DebugTerminalAgentInputProtocolVersion,
			RunID:           request.RunID, TerminalSessionID: request.TerminalSessionID,
			RequestedBy:               "desktop_operator",
			TTL:                       time.Duration(request.TTLSeconds) * time.Second,
			ConfirmDebugMaximumAccess: request.ConfirmDebugMaximumAccess,
			ConfirmAgentTerminalInput: request.ConfirmAgentTerminalInput,
		})
	if err != nil {
		return DesktopDebugTerminalAgentInputBinding{}, err
	}
	return projectDesktopDebugTerminalAgentInputBinding(binding), nil
}

func (b *DesktopBridge) GetDebugTerminalAgentInput(
	runID string,
) (DesktopDebugTerminalAgentInputBinding, error) {
	if b == nil || !b.bootstrap.UserTerminalEnabled ||
		!b.bootstrap.DebugMaximumAccessEnabled ||
		b.debugTerminalAgentInput == nil {
		return DesktopDebugTerminalAgentInputBinding{}, apperror.New(
			apperror.CodeNotFound,
			"desktop Debug terminal Agent input is disabled")
	}
	ctx, err := b.lifecycleContext()
	if err != nil {
		return DesktopDebugTerminalAgentInputBinding{}, err
	}
	binding, found, err := b.debugTerminalAgentInput.Active(ctx, runID)
	if err != nil {
		return DesktopDebugTerminalAgentInputBinding{}, err
	}
	if !found {
		return DesktopDebugTerminalAgentInputBinding{}, apperror.New(
			apperror.CodeNotFound,
			"Run has no active Debug terminal Agent-input binding")
	}
	return projectDesktopDebugTerminalAgentInputBinding(binding), nil
}

func (b *DesktopBridge) RevokeDebugTerminalAgentInput(
	request DesktopDebugTerminalAgentInputRevokeRequest,
) error {
	if b == nil || !b.bootstrap.UserTerminalEnabled ||
		!b.bootstrap.DebugMaximumAccessEnabled ||
		b.debugTerminalAgentInput == nil {
		return apperror.New(apperror.CodeNotFound,
			"desktop Debug terminal Agent input is disabled")
	}
	if request.ProtocolVersion != DesktopDebugTerminalAgentInputProtocolVersion {
		return apperror.New(apperror.CodeInvalidArgument,
			"desktop Debug terminal Agent-input protocol is invalid")
	}
	ctx, err := b.lifecycleContext()
	if err != nil {
		return err
	}
	return b.debugTerminalAgentInput.Revoke(ctx,
		application.RevokeDebugTerminalAgentInputRequest{
			ProtocolVersion: application.DebugTerminalAgentInputProtocolVersion,
			BindingID:       request.BindingID, RequestedBy: "desktop_operator",
			OperatorConfirmed: request.OperatorConfirmed,
		})
}

func projectDesktopDebugTerminalAgentInputBinding(
	binding application.DebugTerminalAgentInputBinding,
) DesktopDebugTerminalAgentInputBinding {
	return DesktopDebugTerminalAgentInputBinding{
		ProtocolVersion: DesktopDebugTerminalAgentInputProtocolVersion,
		BindingID:       binding.ID, RunID: binding.RunID,
		TerminalSessionID: binding.TerminalSessionID,
		IssuedAt:          binding.IssuedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:         binding.ExpiresAt.UTC().Format(time.RFC3339Nano),
		ProcessLocal:      binding.ProcessLocal, TokenExposed: binding.TokenExposed,
		RawInputPersisted: binding.RawInputPersisted,
	}
}
