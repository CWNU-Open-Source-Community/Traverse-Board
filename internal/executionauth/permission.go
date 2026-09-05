package executionauth

import (
	"errors"
	"fmt"

	"cyberagent-workbench/internal/domain"
)

type PermissionOperationKind string

const (
	PermissionOperationFixedTemplate      PermissionOperationKind = "fixed_template"
	PermissionOperationSandboxedWorkspace PermissionOperationKind = "sandboxed_workspace_command"
	PermissionOperationStatelessCommand   PermissionOperationKind = "stateless_command"
	PermissionOperationManagedCommand     PermissionOperationKind = "managed_command"
	PermissionOperationPersistentTerminal PermissionOperationKind = "persistent_terminal"
)

type PermissionRequest struct {
	Kind               PermissionOperationKind
	HostFilesystem     bool
	Network            bool
	BackgroundProcess  bool
	AgentTerminalInput bool
	OperatorApproved   bool
}

type PermissionDecision struct {
	Allowed             bool
	RequiresApproval    bool
	HostFilesystem      bool
	WorkspaceFilesystem bool
	SandboxedCommand    bool
	Network             bool
	PersistentTerminal  bool
	BackgroundProcess   bool
	AgentTerminalInput  bool
	Mode                domain.RunExecutionPermissionMode
	RequiredGate        domain.ExecutionPermissionGate
	Reason              string
}

func EvaluateExecutionPermission(snapshot domain.RunExecutionPermissionSnapshot,
	runtime domain.ExecutionPermissionRuntimeCapabilities,
	request PermissionRequest,
) (PermissionDecision, error) {
	if err := snapshot.Validate(); err != nil {
		return PermissionDecision{}, fmt.Errorf("invalid execution permission snapshot: %w", err)
	}
	if err := runtime.Validate(); err != nil {
		return PermissionDecision{}, fmt.Errorf("invalid execution permission runtime: %w", err)
	}
	if err := validatePermissionRequest(request); err != nil {
		return PermissionDecision{}, err
	}
	decision := PermissionDecision{
		Mode: snapshot.Mode, RequiredGate: snapshot.RequiredGate,
	}
	if !runtime.AllowsSnapshot(snapshot) {
		decision.Reason = "the current process lacks the required permission gate or live Full Access grant"
		return decision, nil
	}
	if request.Kind == PermissionOperationSandboxedWorkspace &&
		!runtime.WorkspaceSandboxEnabled {
		decision.Reason = "the verified Workspace Sandbox adapter is unavailable"
		return decision, nil
	}

	switch snapshot.Mode {
	case domain.RunExecutionPermissionConservative:
		if request.Kind != PermissionOperationFixedTemplate {
			decision.Reason = "conservative mode only permits Go-owned fixed command templates"
			return decision, nil
		}
		if request.HostFilesystem || request.Network || request.BackgroundProcess ||
			request.AgentTerminalInput {
			decision.Reason = "conservative mode denies host, network, background, and Agent terminal capabilities"
			return decision, nil
		}
		decision.Allowed = true
		decision.Reason = "Go-owned fixed command template is allowed"
	case domain.RunExecutionPermissionWorkspaceAccess:
		if request.Kind != PermissionOperationSandboxedWorkspace {
			decision.Reason = "workspace-access mode permits process execution only through the verified sandbox adapter"
			return decision, nil
		}
		if request.HostFilesystem || request.Network || request.AgentTerminalInput ||
			request.OperatorApproved {
			decision.Reason = "workspace-access mode denies host, network, credential, home, and terminal capabilities"
			return decision, nil
		}
		decision.Allowed = true
		decision.WorkspaceFilesystem = true
		decision.SandboxedCommand = true
		decision.BackgroundProcess = request.BackgroundProcess
		decision.Reason = "verified Workspace Sandbox adapter permits one bounded managed command"
	case domain.RunExecutionPermissionApproval:
		if request.Kind == PermissionOperationPersistentTerminal ||
			request.BackgroundProcess || request.AgentTerminalInput {
			decision.Reason = "user-approval mode only permits one-shot commands"
			return decision, nil
		}
		decision.RequiresApproval = true
		if !request.OperatorApproved {
			decision.Reason = "this exact one-shot command requires operator approval"
			return decision, nil
		}
		decision.Allowed = true
		decision.HostFilesystem = request.HostFilesystem
		decision.Network = request.Network
		decision.WorkspaceFilesystem = request.Kind == PermissionOperationSandboxedWorkspace
		decision.SandboxedCommand = request.Kind == PermissionOperationSandboxedWorkspace
		decision.Reason = "operator approved this exact one-shot command"
	case domain.RunExecutionPermissionFullAccess:
		if request.Kind == PermissionOperationPersistentTerminal ||
			(request.BackgroundProcess && request.Kind != PermissionOperationManagedCommand) ||
			request.AgentTerminalInput {
			decision.Reason = "full-access mode does not grant persistent debug terminal capabilities"
			return decision, nil
		}
		decision.Allowed = true
		decision.HostFilesystem = request.HostFilesystem
		decision.Network = request.Network
		decision.BackgroundProcess = request.BackgroundProcess
		decision.WorkspaceFilesystem = request.Kind == PermissionOperationSandboxedWorkspace
		decision.SandboxedCommand = request.Kind == PermissionOperationSandboxedWorkspace
		if request.Kind == PermissionOperationManagedCommand {
			decision.Reason = "danger-full-access process gate permits a Run-owned managed command"
		} else {
			decision.Reason = "danger-full-access process gate permits one-shot host execution"
		}
	case domain.RunExecutionPermissionDebug:
		decision.Allowed = true
		decision.HostFilesystem = request.HostFilesystem
		decision.Network = request.Network
		decision.PersistentTerminal = request.Kind == PermissionOperationPersistentTerminal
		decision.BackgroundProcess = request.BackgroundProcess
		decision.AgentTerminalInput = request.AgentTerminalInput
		decision.WorkspaceFilesystem = request.Kind == PermissionOperationSandboxedWorkspace
		decision.SandboxedCommand = request.Kind == PermissionOperationSandboxedWorkspace
		decision.Reason = "debug maximum-access process gate permits the requested capability"
	default:
		return PermissionDecision{}, errors.New("unsupported execution permission mode")
	}
	return decision, nil
}

func validatePermissionRequest(request PermissionRequest) error {
	switch request.Kind {
	case PermissionOperationFixedTemplate, PermissionOperationSandboxedWorkspace,
		PermissionOperationStatelessCommand,
		PermissionOperationManagedCommand,
		PermissionOperationPersistentTerminal:
	default:
		return fmt.Errorf("unsupported execution permission operation kind %q", request.Kind)
	}
	if request.Kind != PermissionOperationPersistentTerminal &&
		request.AgentTerminalInput {
		return errors.New("agent terminal input requires a persistent terminal request")
	}
	if request.Kind == PermissionOperationFixedTemplate && request.BackgroundProcess {
		return errors.New("fixed command templates cannot request a background process")
	}
	if request.Kind == PermissionOperationManagedCommand &&
		(!request.HostFilesystem || !request.BackgroundProcess || request.Network ||
			request.AgentTerminalInput || request.OperatorApproved) {
		return errors.New("managed commands require host/background ownership without network, terminal input, or approval")
	}
	return nil
}
