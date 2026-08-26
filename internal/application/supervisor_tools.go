package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/codeintel"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
	"cyberagent-workbench/internal/workspace"
)

const supervisorToolResultVersion = "supervisor_tool_result.v1"

const supervisorToolCallTimeout = 30 * time.Second

var errSupervisorWaitingApproval = errors.New("Supervisor tool is waiting for operator approval")

type supervisorAgentCodeTools struct {
	Capabilities toolgateway.AgentCodeCapabilitySnapshot
	Authority    json.RawMessage
}

type supervisorCodeIntelTools struct {
	Capabilities toolgateway.CodeIntelCapabilitySnapshot
	Authority    json.RawMessage
}

type supervisorCommandRuntimeTools struct {
	Adapter   commandruntimeadapter.Identity
	Authority json.RawMessage
}

type supervisorWebEvidenceTools struct {
	Capabilities toolgateway.WebEvidenceCapabilities
	Authority    json.RawMessage
}

type supervisorToolOptions struct {
	CommandRuntime supervisorCommandRuntimeTools
	AgentCode      supervisorAgentCodeTools
	CodeIntel      supervisorCodeIntelTools
	MCP            mcp.ScopedCapabilities
	WebEvidence    supervisorWebEvidenceTools
}

type supervisorToolResultEnvelope struct {
	Version   string            `json:"version"`
	Tool      string            `json:"tool"`
	Status    string            `json:"status"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Code      string            `json:"code,omitempty"`
	Message   string            `json:"message,omitempty"`
	Stdout    string            `json:"stdout,omitempty"`
	Stderr    string            `json:"stderr,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

func marshalSupervisorToolResultEnvelope(value supervisorToolResultEnvelope) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	// Web results are durable JSON and are never embedded as HTML. Keeping
	// literal '<', '>', and '&' avoids a sixfold expansion of bounded evidence;
	// all existing non-Web result encodings retain their prior canonical form.
	if toolgateway.IsWebEvidenceTool(toolgateway.ToolName(value.Tool)) {
		encoder.SetEscapeHTML(false)
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if !json.Valid(encoded) || len(encoded) > domain.MaxSupervisorToolResultBytes {
		return nil, errors.New("supervisor tool result envelope exceeds its durable JSON limit")
	}
	return encoded, nil
}

func supervisorStructuredToolSpecs(surface domain.ExecutionSurface,
	phase domain.ExecutionPhase,
	permissionMode domain.RunExecutionPermissionMode,
	skillCandidateEnabled bool,
	debugTerminalEnabled bool,
	options ...supervisorToolOptions,
) []llm.ToolSpec {
	configured := supervisorToolOptions{}
	if len(options) > 0 {
		configured = options[0]
	}
	runtimeEnabled := configured.CommandRuntime.Adapter.Executable()
	agentCode := toolgateway.AgentCodeCapabilitySnapshot{}
	if len(options) > 0 {
		agentCode = configured.AgentCode.Capabilities
	}
	definitions := toolgateway.SupervisorToolDefinitions()
	if phase == domain.ExecutionPhasePlan {
		definitions = toolgateway.PlanPhaseSupervisorToolDefinitions()
	}
	out := make([]llm.ToolSpec, 0, len(definitions))
	for _, definition := range definitions {
		if definition.Name == toolgateway.SkillCandidateProposeTool &&
			!skillCandidateEnabled {
			continue
		}
		if definition.Name == toolgateway.HostCommandProposeTool &&
			(permissionMode != domain.RunExecutionPermissionApproval &&
				permissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
				surface != domain.ExecutionSurfaceCode) {
			continue
		}
		if definition.Name == toolgateway.DebugTerminalTool &&
			(!debugTerminalEnabled || surface != domain.ExecutionSurfaceCode ||
				phase != domain.ExecutionPhaseDeliver ||
				permissionMode != domain.RunExecutionPermissionDebug) {
			continue
		}
		if definition.Name == toolgateway.CommandRuntimeTool &&
			(!runtimeEnabled || surface != domain.ExecutionSurfaceCode ||
				phase != domain.ExecutionPhaseDeliver ||
				!configured.CommandRuntime.Adapter.AllowsPermission(permissionMode)) {
			continue
		}
		if definition.Name == toolgateway.MCPToolCallTool {
			if surface != domain.ExecutionSurfaceCode || phase != domain.ExecutionPhaseDeliver ||
				permissionMode != domain.RunExecutionPermissionFullAccess ||
				len(configured.MCP.Servers) == 0 {
				continue
			}
			definition.InputSchema = supervisorMCPToolSchema(configured.MCP)
			definition.Description += " Only the server/tool/fingerprint combinations encoded in this schema are available."
		}
		if toolgateway.IsWebEvidenceTool(definition.Name) {
			if !configured.WebEvidence.Capabilities.Available ||
				(definition.Name == toolgateway.WebSearchTool &&
					!configured.WebEvidence.Capabilities.SearchAvailable) {
				continue
			}
		}
		out = append(out, llm.ToolSpec{
			Name: string(definition.Name), Description: definition.Description,
			Parameters: append(json.RawMessage(nil), definition.InputSchema...),
		})
	}
	for _, definition := range agentCode.VisibleDefinitions() {
		out = append(out, llm.ToolSpec{Name: string(definition.Name),
			Description: definition.Description,
			Parameters:  append(json.RawMessage(nil), definition.InputSchema...)})
	}
	if surface == domain.ExecutionSurfaceCode &&
		(phase == domain.ExecutionPhasePlan || phase == domain.ExecutionPhaseDeliver) {
		for _, definition := range configured.CodeIntel.Capabilities.VisibleDefinitions() {
			out = append(out, llm.ToolSpec{Name: string(definition.Name),
				Description: definition.Description,
				Parameters:  append(json.RawMessage(nil), definition.InputSchema...)})
		}
	}
	return out
}

func prepareSupervisorToolCalls(calls []llm.ToolCall, runID string, turn int, round int,
	surface domain.ExecutionSurface, phase domain.ExecutionPhase,
	permissionMode domain.RunExecutionPermissionMode,
	skillCandidateEnabled bool,
	debugTerminalEnabled bool,
	options ...supervisorToolOptions,
) ([]llm.ToolCall, error) {
	configured := supervisorToolOptions{}
	if len(options) > 0 {
		configured = options[0]
	}
	runtimeEnabled := configured.CommandRuntime.Adapter.Executable()
	commandRuntimeAuthority := configured.CommandRuntime.Authority
	agentCode := toolgateway.AgentCodeCapabilitySnapshot{}
	var agentCodeAuthority json.RawMessage
	codeIntel := toolgateway.CodeIntelCapabilitySnapshot{}
	var codeIntelAuthority json.RawMessage
	webEvidence := toolgateway.WebEvidenceCapabilities{}
	var webEvidenceAuthority json.RawMessage
	if len(options) > 0 {
		agentCode = configured.AgentCode.Capabilities
		agentCodeAuthority = configured.AgentCode.Authority
		codeIntel = configured.CodeIntel.Capabilities
		codeIntelAuthority = configured.CodeIntel.Authority
		webEvidence = configured.WebEvidence.Capabilities
		webEvidenceAuthority = configured.WebEvidence.Authority
	}
	if len(calls) == 0 || len(calls) > domain.MaxSupervisorToolCallsPerRound {
		return nil, fmt.Errorf("supervisor tool batch must contain 1 to %d calls",
			domain.MaxSupervisorToolCallsPerRound)
	}
	normalized, err := llm.NormalizeToolCalls(calls)
	if err != nil {
		return nil, err
	}
	out := make([]llm.ToolCall, len(normalized))
	seen := make(map[string]struct{}, len(normalized))
	for index, call := range normalized {
		name := toolgateway.ToolName(call.Name)
		if name != toolgateway.WorkItemCreateTool && name != toolgateway.NoteCreateTool &&
			name != toolgateway.SpecialistDelegationProposeTool &&
			name != toolgateway.ChildTaskProposeTool &&
			name != toolgateway.PlanDeliveryProposeTool &&
			name != toolgateway.ControlledCommandProposeTool &&
			name != toolgateway.OneShotCommandProposeTool &&
			name != toolgateway.HostCommandProposeTool &&
			name != toolgateway.DockerSandboxRunProposeTool &&
			name != toolgateway.SkillCandidateProposeTool &&
			name != toolgateway.DebugTerminalTool &&
			name != toolgateway.CommandRuntimeTool && name != toolgateway.MCPToolCallTool &&
			!toolgateway.IsAgentCodeTool(name) && !toolgateway.IsCodeIntelTool(name) &&
			!toolgateway.IsWebEvidenceTool(name) {
			return nil, fmt.Errorf("provider requested unsupported supervisor tool %q", call.Name)
		}
		if toolgateway.IsAgentCodeTool(name) {
			available := false
			for _, capability := range agentCode.Tools {
				if capability.Name == name && capability.Available {
					available = true
					break
				}
			}
			if !available || len(agentCodeAuthority) == 0 {
				return nil, fmt.Errorf("provider requested unavailable agent code tool %q", call.Name)
			}
		}
		if toolgateway.IsCodeIntelTool(name) && (len(codeIntelAuthority) == 0 ||
			surface != domain.ExecutionSurfaceCode ||
			(phase != domain.ExecutionPhasePlan && phase != domain.ExecutionPhaseDeliver)) {
			return nil, fmt.Errorf("provider requested unavailable code-intel tool %q", call.Name)
		}
		if toolgateway.IsWebEvidenceTool(name) && (!webEvidence.Available ||
			len(webEvidenceAuthority) == 0 ||
			(name == toolgateway.WebSearchTool && !webEvidence.SearchAvailable)) {
			return nil, fmt.Errorf("provider requested unavailable web evidence tool %q", call.Name)
		}
		if name == toolgateway.PlanDeliveryProposeTool && phase != domain.ExecutionPhasePlan {
			return nil, errors.New("provider requested Plan/Delivery proposal outside Plan phase")
		}
		if name == toolgateway.SkillCandidateProposeTool && phase != domain.ExecutionPhaseDeliver {
			return nil, errors.New("provider requested Skill candidate proposal outside Deliver phase")
		}
		if name == toolgateway.SkillCandidateProposeTool && !skillCandidateEnabled {
			return nil, errors.New(
				"provider requested Skill candidate proposal without the explicit generator Skill")
		}
		if name == toolgateway.HostCommandProposeTool &&
			(permissionMode != domain.RunExecutionPermissionApproval &&
				permissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
				surface != domain.ExecutionSurfaceCode) {
			return nil, errors.New(
				"provider requested host command proposal outside Code approval or Workspace Access mode")
		}
		if name == toolgateway.DebugTerminalTool &&
			(!debugTerminalEnabled || surface != domain.ExecutionSurfaceCode ||
				phase != domain.ExecutionPhaseDeliver ||
				permissionMode != domain.RunExecutionPermissionDebug) {
			return nil, errors.New(
				"provider requested Debug terminal outside Code/Deliver/Debug runtime")
		}
		if name == toolgateway.CommandRuntimeTool &&
			(!runtimeEnabled || surface != domain.ExecutionSurfaceCode ||
				phase != domain.ExecutionPhaseDeliver ||
				!configured.CommandRuntime.Adapter.AllowsPermission(permissionMode)) {
			return nil, errors.New(
				"provider requested command runtime outside its advertised Code/Deliver adapter authority")
		}
		if name == toolgateway.MCPToolCallTool &&
			(surface != domain.ExecutionSurfaceCode || phase != domain.ExecutionPhaseDeliver ||
				permissionMode != domain.RunExecutionPermissionFullAccess) {
			return nil, errors.New(
				"provider requested MCP outside Code/Deliver/full-access runtime")
		}
		payload, err := toolgateway.NormalizeSupervisorToolPayload(name, call.Arguments)
		if err != nil {
			return nil, err
		}
		if name == toolgateway.HostCommandProposeTool {
			hostSpec, _, hostErr := toolgateway.NormalizeHostCommandProposalPayload(payload)
			if hostErr != nil {
				return nil, hostErr
			}
			if (permissionMode == domain.RunExecutionPermissionWorkspaceAccess) !=
				(hostSpec.Version == runner.RiskEscalationProtocolVersion) {
				return nil, errors.New("host command proposal protocol does not match the current permission mode")
			}
		}
		if toolgateway.IsCodeIntelTool(name) {
			input, _, _ := toolgateway.NormalizeCodeIntelPayload(name, payload)
			if !codeIntel.Available(name, input) {
				return nil, errors.New(
					"provider requested code-intel capability absent from the current reviewed snapshot")
			}
		}
		if name == toolgateway.MCPToolCallTool {
			request, _, _ := toolgateway.NormalizeMCPToolPayload(payload)
			if !supervisorMCPToolAvailable(configured.MCP, request) {
				return nil, errors.New(
					"provider requested an MCP capability absent from the current reviewed snapshot")
			}
		}
		operationKey := supervisorToolOperationKey(runID, turn, name, payload)
		callID, err := runmutation.SupervisorToolCallID(operationKey, round)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[callID]; exists {
			return nil, errors.New("provider requested duplicate structured tool intent in one batch")
		}
		seen[callID] = struct{}{}
		out[index] = llm.ToolCall{ID: callID, Name: string(name), Arguments: payload,
			StreamResponseID: call.StreamResponseID, StreamItemID: call.StreamItemID,
			StreamCallID: call.StreamCallID}
		if toolgateway.IsAgentCodeTool(name) {
			out[index].Authority = append(json.RawMessage(nil), agentCodeAuthority...)
		}
		if name == toolgateway.HostCommandProposeTool &&
			permissionMode == domain.RunExecutionPermissionWorkspaceAccess {
			if len(agentCodeAuthority) == 0 {
				return nil, errors.New("risk escalation requires current Agent Code authority")
			}
			out[index].Authority = append(json.RawMessage(nil), agentCodeAuthority...)
		}
		if toolgateway.IsCodeIntelTool(name) {
			out[index].Authority = append(json.RawMessage(nil), codeIntelAuthority...)
		}
		if name == toolgateway.CommandRuntimeTool {
			if authority, authorityErr := commandruntimeadapter.DecodeAuthority(
				commandRuntimeAuthority); authorityErr != nil ||
				authority.RunID != runID ||
				!authority.Adapter.SameBackend(configured.CommandRuntime.Adapter) {
				return nil, errors.New("command runtime advertisement authority is invalid")
			}
			out[index].Authority = append(json.RawMessage(nil), commandRuntimeAuthority...)
		}
		if toolgateway.IsWebEvidenceTool(name) {
			authority, authorityErr := toolgateway.DecodeWebEvidenceCallAuthority(
				webEvidenceAuthority)
			if authorityErr != nil || authority.RunID != runID ||
				authority.Generation != webEvidence.Generation {
				return nil, errors.New("web evidence advertisement authority is invalid")
			}
			out[index].Authority = append(json.RawMessage(nil), webEvidenceAuthority...)
		}
	}
	return out, nil
}

func (s *RunSupervisor) supervisorWebEvidenceCapabilities(
	turn domain.SupervisorTurn, permission domain.RunExecutionPermissionSnapshot,
) (toolgateway.WebEvidenceCapabilities, json.RawMessage, error) {
	if s == nil || s.webEvidence == nil || turn.Agent.Role != domain.AgentRoleRoot {
		return toolgateway.WebEvidenceCapabilities{}, nil, nil
	}
	networkAuthority := webevidence.NetworkAuthority{Mode: turn.Mode.Scope.NetworkMode,
		AllowedTargets: append([]string(nil), turn.Mode.Scope.AllowedTargets...)}
	providerFingerprint := s.webEvidence.SearchProviderFingerprintFor(networkAuthority)
	context := toolgateway.WebEvidenceCapabilityContext{RunID: turn.Run.ID,
		MissionID: turn.Mission.ID, SessionID: turn.Run.SessionID,
		RootAgentID: turn.Agent.ID, WorkspaceID: turn.Mission.WorkspaceID,
		Surface: turn.Mode.Surface, Phase: turn.Mode.Phase, Role: turn.Agent.Role,
		Profile: turn.Mode.Profile, PermissionMode: permission.Mode,
		PermissionRevision: permission.Revision, ModeRevision: turn.Mode.Revision,
		NetworkMode:         turn.Mode.Scope.NetworkMode,
		AllowedTargets:      append([]string(nil), turn.Mode.Scope.AllowedTargets...),
		ProviderAvailable:   providerFingerprint != "",
		ProviderFingerprint: providerFingerprint}
	snapshot := toolgateway.WebEvidenceCapabilitySnapshot(context)
	if !snapshot.Available {
		return snapshot, nil, nil
	}
	authority, err := toolgateway.NewWebEvidenceCallAuthority(context)
	if err != nil {
		return toolgateway.WebEvidenceCapabilities{}, nil, err
	}
	encoded, err := toolgateway.EncodeWebEvidenceCallAuthority(authority)
	if err != nil {
		return toolgateway.WebEvidenceCapabilities{}, nil, err
	}
	return snapshot, encoded, nil
}

func (s *RunSupervisor) supervisorMCPCapabilities(ctx context.Context,
	turn domain.SupervisorTurn, permission domain.RunExecutionPermissionSnapshot,
) (mcp.ScopedCapabilities, error) {
	if s.mcpClient == nil || turn.Mode.Surface != domain.ExecutionSurfaceCode ||
		turn.Mode.Phase != domain.ExecutionPhaseDeliver || turn.Agent.Role != domain.AgentRoleRoot ||
		permission.Mode != domain.RunExecutionPermissionFullAccess ||
		strings.TrimSpace(turn.Mission.WorkspaceID) == "" {
		return mcp.ScopedCapabilities{}, nil
	}
	capabilities, err := s.mcpClient.Capabilities(ctx, turn.Run.ID, turn.Mission.WorkspaceID)
	if err != nil {
		return mcp.ScopedCapabilities{}, apperror.Normalize(err)
	}
	return boundedSupervisorMCPCapabilities(capabilities), nil
}

const maxSupervisorMCPTools = 128

const maxSupervisorMCPSchemaBytes = 128 * 1024

func boundedSupervisorMCPCapabilities(value mcp.ScopedCapabilities) mcp.ScopedCapabilities {
	if value.ProtocolVersion != mcp.ClientProtocolVersion {
		return mcp.ScopedCapabilities{}
	}
	result := mcp.ScopedCapabilities{ProtocolVersion: value.ProtocolVersion,
		Generation: value.Generation}
	budget := maxSupervisorMCPSchemaBytes
	count := 0
	for _, server := range value.Servers {
		if count >= maxSupervisorMCPTools || len(server.CapabilityFingerprint) != 64 {
			break
		}
		projected := mcp.ScopedServerCapability{ServerID: server.ServerID, Name: server.Name,
			CapabilityFingerprint: server.CapabilityFingerprint}
		for _, tool := range server.Tools {
			cost := len(tool.Name) + len(tool.Description) + len(tool.InputSchema) + 256
			if count >= maxSupervisorMCPTools || cost > budget {
				break
			}
			projected.Tools = append(projected.Tools, tool)
			count++
			budget -= cost
		}
		if len(projected.Tools) > 0 {
			result.Servers = append(result.Servers, projected)
		}
	}
	return result
}

func supervisorMCPToolAvailable(capabilities mcp.ScopedCapabilities,
	request toolgateway.MCPToolCallPayload,
) bool {
	for _, server := range capabilities.Servers {
		if server.ServerID != request.ServerID ||
			server.CapabilityFingerprint != request.CapabilityFingerprint {
			continue
		}
		for _, tool := range server.Tools {
			if tool.Name == request.ToolName {
				return true
			}
		}
	}
	return false
}

func supervisorMCPToolSchema(capabilities mcp.ScopedCapabilities) json.RawMessage {
	type choiceProperties struct {
		Version               map[string]string `json:"version"`
		ServerID              map[string]string `json:"server_id"`
		ToolName              map[string]string `json:"tool_name"`
		CapabilityFingerprint map[string]string `json:"capability_fingerprint"`
		Arguments             json.RawMessage   `json:"arguments"`
	}
	type choice struct {
		Type                 string           `json:"type"`
		AdditionalProperties bool             `json:"additionalProperties"`
		Required             []string         `json:"required"`
		Properties           choiceProperties `json:"properties"`
	}
	const requiredVersion = toolgateway.MCPClientToolProtocolVersion
	required := []string{"version", "server_id", "tool_name", "capability_fingerprint", "arguments"}
	choices := make([]choice, 0)
	for _, server := range capabilities.Servers {
		for _, tool := range server.Tools {
			choices = append(choices, choice{Type: "object", AdditionalProperties: false,
				Required: required, Properties: choiceProperties{
					Version:  map[string]string{"const": requiredVersion},
					ServerID: map[string]string{"const": server.ServerID},
					ToolName: map[string]string{"const": tool.Name},
					CapabilityFingerprint: map[string]string{
						"const": server.CapabilityFingerprint},
					Arguments: append(json.RawMessage(nil), tool.InputSchema...),
				}})
		}
	}
	raw, err := json.Marshal(struct {
		OneOf []choice `json:"oneOf"`
	}{OneOf: choices})
	if err != nil || len(raw) > maxSupervisorMCPSchemaBytes {
		return toolgateway.MCPToolDefinition().InputSchema
	}
	return raw
}

func (s *RunSupervisor) supervisorAgentCodeCapabilities(ctx context.Context,
	turn domain.SupervisorTurn, permission domain.RunExecutionPermissionSnapshot,
) (toolgateway.AgentCodeCapabilitySnapshot, json.RawMessage, error) {
	store, ok := s.store.(AgentCodeToolStore)
	if !ok || turn.Mode.Surface != domain.ExecutionSurfaceCode ||
		strings.TrimSpace(turn.Mission.WorkspaceID) == "" {
		return toolgateway.AgentCodeCapabilitySnapshot{}, nil, nil
	}
	registered, err := store.GetWorkspaceInfo(ctx, turn.Mission.WorkspaceID)
	if err != nil {
		return toolgateway.AgentCodeCapabilitySnapshot{}, nil, apperror.Normalize(err)
	}
	rootFingerprint, err := workspace.AgentCodeRootFingerprint(registered.RootPath)
	if err != nil {
		return toolgateway.AgentCodeCapabilitySnapshot{}, nil, apperror.Normalize(err)
	}
	scope := toolgateway.AgentCodeCapabilityContext{RunID: turn.Run.ID,
		MissionID: turn.Mission.ID, RootAgentID: turn.Agent.ID,
		WorkspaceID: turn.Mission.WorkspaceID, RootFingerprint: rootFingerprint,
		Surface: turn.Mode.Surface, Phase: turn.Mode.Phase, Role: turn.Agent.Role,
		Profile: turn.Mode.Profile, PermissionMode: permission.Mode,
		ModeRevision: turn.Mode.Revision, PermissionRevision: permission.Revision}
	snapshot := toolgateway.AgentCodeCapabilities(scope)
	authority, err := toolgateway.NewAgentCodeCallAuthority(scope, turn.Run.SessionID)
	if err != nil {
		return toolgateway.AgentCodeCapabilitySnapshot{}, nil, err
	}
	encoded, err := toolgateway.EncodeAgentCodeCallAuthority(authority)
	if err != nil {
		return toolgateway.AgentCodeCapabilitySnapshot{}, nil, err
	}
	return snapshot, encoded, nil
}

func (s *RunSupervisor) supervisorCodeIntelCapabilities(ctx context.Context,
	turn domain.SupervisorTurn,
) (toolgateway.CodeIntelCapabilitySnapshot, error) {
	result := toolgateway.CodeIntelCapabilitySnapshot{ProtocolVersion: codeintel.ProtocolVersion,
		Servers: []toolgateway.CodeIntelServerCapability{}, Refusals: map[string]string{}}
	available, _ := toolgateway.CodeIntelScopeEligibility(toolgateway.AgentCodeCapabilityContext{
		Surface: turn.Mode.Surface, Phase: turn.Mode.Phase, Role: turn.Agent.Role,
		Profile: turn.Mode.Profile})
	if s.codeIntel == nil || !available || strings.TrimSpace(turn.Mission.WorkspaceID) == "" {
		return result, nil
	}
	store, ok := s.store.(AgentCodeToolStore)
	if !ok {
		return result, nil
	}
	registered, err := store.GetWorkspaceInfo(ctx, turn.Mission.WorkspaceID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	for _, snapshot := range s.codeIntel.Capabilities(ctx, registered.ID, registered.RootPath) {
		if snapshot.Health != codeintel.HealthHealthy {
			reason := string(snapshot.Health)
			if snapshot.LastError != "" {
				reason += ": " + snapshot.LastError
			}
			result.Refusals[snapshot.ServerID] = reason
			continue
		}
		server := toolgateway.CodeIntelServerCapability{ServerID: snapshot.ServerID,
			ServerName: snapshot.ServerName, Languages: append([]string(nil), snapshot.Languages...),
			Generation:            snapshot.Generation,
			CapabilityFingerprint: snapshot.CapabilityFingerprint}
		for _, name := range snapshot.ModelVisibleTools {
			tool := toolgateway.ToolName(name)
			if toolgateway.IsCodeIntelTool(tool) {
				server.Tools = append(server.Tools, tool)
			}
		}
		result.Servers = append(result.Servers, server)
	}
	return result, nil
}

func supervisorToolOperationKey(runID string, turn int, name toolgateway.ToolName,
	payload json.RawMessage,
) string {
	return runmutation.SupervisorToolOperationKey(runID, turn, string(name), string(payload))
}

func (s *RunSupervisor) resumeSupervisorTools(ctx context.Context, turn domain.SupervisorTurn,
	rounds []domain.SupervisorToolRound, standardCode ...*standardCodeSupervisorTurn,
) ([]domain.SupervisorToolRound, bool, error) {
	var completion *standardCodeSupervisorTurn
	if len(standardCode) > 0 {
		completion = standardCode[0]
	}
	for _, round := range rounds {
		for _, call := range round.Calls {
			if call.Status != domain.SupervisorToolPending {
				if completion != nil {
					if err := completion.ObserveCall(ctx, call); err != nil {
						return rounds, false, apperror.Normalize(err)
					}
				}
				continue
			}
			decision := standardCodeCallDecision{Allowed: true}
			var err error
			if completion != nil {
				decision, err = completion.Authorize(ctx, call)
				if err != nil {
					return rounds, false, apperror.Normalize(err)
				}
			}
			if _, err := s.store.RecordSupervisorToolExecutionStarted(ctx, turn.Checkpoint,
				call.CallID); err != nil {
				return rounds, false, apperror.Normalize(err)
			}
			var result domain.SupervisorToolResult
			if decision.Allowed {
				result, err = s.invokeSupervisorTool(ctx, turn, call)
				if err != nil {
					if errors.Is(err, errSupervisorWaitingApproval) {
						return rounds, true, nil
					}
					return rounds, false, err
				}
			} else if decision.Result != nil {
				result = *decision.Result
			} else {
				return rounds, false, apperror.New(apperror.CodeFailedPrecondition,
					"Standard Code Supervisor denial omitted its durable result")
			}
			stored, _, err := s.store.RecordSupervisorToolResult(ctx, turn.Checkpoint, result)
			if err != nil {
				return rounds, false, apperror.Normalize(err)
			}
			if completion != nil {
				if err := completion.ObserveCall(ctx, stored); err != nil {
					return rounds, false, apperror.Normalize(err)
				}
			}
		}
	}
	stored, err := s.store.ListSupervisorToolRounds(ctx, turn.Checkpoint)
	if err != nil {
		return rounds, false, err
	}
	if completion != nil {
		for _, round := range stored {
			if err := completion.ObserveRound(ctx, round); err != nil {
				return stored, false, apperror.Normalize(err)
			}
		}
	}
	return stored, false, nil
}

func (s *RunSupervisor) invokeSupervisorTool(ctx context.Context, turn domain.SupervisorTurn,
	call domain.SupervisorToolCall,
) (domain.SupervisorToolResult, error) {
	name := toolgateway.ToolName(call.ToolName)
	operationKey := supervisorToolOperationKey(call.RunID, call.Turn, name, json.RawMessage(call.PayloadJSON))
	toolCall := toolgateway.ToolCall{
		Name: name, Payload: json.RawMessage(call.PayloadJSON), OperationKey: operationKey,
		RunID: call.RunID, AgentID: turn.Agent.ID, SessionID: turn.Run.SessionID,
		WorkspaceID: turn.Mission.WorkspaceID,
		LeaseID:     turn.Checkpoint.LeaseID, LeaseGeneration: turn.Checkpoint.LeaseGeneration,
		RequestedBy:    "run_supervisor",
		SupervisorTurn: call.Turn, SupervisorToolCallID: call.CallID,
	}
	if name == toolgateway.HostCommandProposeTool && len(call.AuthorityJSON) > 0 {
		authority, authorityErr := toolgateway.DecodeAgentCodeCallAuthority(
			json.RawMessage(call.AuthorityJSON))
		if authorityErr != nil || authority.RunID != call.RunID ||
			authority.RootAgentID != turn.Agent.ID || authority.SessionID != turn.Run.SessionID ||
			authority.MissionID != turn.Mission.ID ||
			authority.WorkspaceID != turn.Mission.WorkspaceID ||
			authority.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess {
			return domain.SupervisorToolResult{}, apperror.New(apperror.CodeFailedPrecondition,
				"durable risk escalation authority does not match the active Supervisor turn")
		}
		toolCall.MissionID = authority.MissionID
		toolCall.RootFingerprint = authority.RootFingerprint
		toolCall.Surface = authority.Surface
		toolCall.Phase = authority.Phase
		toolCall.Role = authority.Role
		toolCall.Profile = authority.Profile
		toolCall.PermissionMode = authority.PermissionMode
		toolCall.ModeRevision = authority.ModeRevision
		toolCall.PermissionRevision = authority.PermissionRevision
		toolCall.CapabilityGeneration = authority.CapabilityGeneration
	}
	if toolgateway.IsAgentCodeTool(name) || toolgateway.IsCodeIntelTool(name) {
		authority, authorityErr := toolgateway.DecodeAgentCodeCallAuthority(
			json.RawMessage(call.AuthorityJSON))
		if authorityErr != nil || authority.RunID != call.RunID ||
			authority.RootAgentID != turn.Agent.ID || authority.SessionID != turn.Run.SessionID ||
			authority.MissionID != turn.Mission.ID ||
			authority.WorkspaceID != turn.Mission.WorkspaceID {
			return domain.SupervisorToolResult{}, apperror.New(apperror.CodeFailedPrecondition,
				"durable agent code tool authority does not match the active Supervisor turn")
		}
		toolCall.MissionID = authority.MissionID
		toolCall.RootFingerprint = authority.RootFingerprint
		toolCall.Surface = authority.Surface
		toolCall.Phase = authority.Phase
		toolCall.Role = authority.Role
		toolCall.Profile = authority.Profile
		toolCall.PermissionMode = authority.PermissionMode
		toolCall.ModeRevision = authority.ModeRevision
		toolCall.PermissionRevision = authority.PermissionRevision
		toolCall.CapabilityGeneration = authority.CapabilityGeneration
	}
	if name == toolgateway.CommandRuntimeTool {
		authority, authorityErr := commandruntimeadapter.DecodeAuthority(
			json.RawMessage(call.AuthorityJSON))
		permission, permissionErr := s.store.GetRunExecutionPermission(ctx, turn.Run.ID)
		if authorityErr != nil || permissionErr != nil || authority.RunID != call.RunID ||
			!authority.Adapter.AllowsPermission(permission.Mode) {
			return domain.SupervisorToolResult{}, apperror.New(apperror.CodeFailedPrecondition,
				"durable command runtime adapter authority does not match the active Supervisor turn")
		}
		toolCall.MissionID = turn.Mission.ID
		toolCall.Surface = turn.Mode.Surface
		toolCall.Phase = turn.Mode.Phase
		toolCall.Role = turn.Agent.Role
		toolCall.Profile = turn.Mode.Profile
		toolCall.PermissionMode = permission.Mode
		toolCall.CapabilityGeneration = authority.Adapter.Generation
		toolCall.CommandRuntimeAdapter = authority.Adapter
	}
	if name == toolgateway.MCPToolCallTool {
		permission, permissionErr := s.store.GetRunExecutionPermission(ctx, turn.Run.ID)
		if permissionErr != nil {
			return domain.SupervisorToolResult{}, apperror.Normalize(permissionErr)
		}
		toolCall.Surface = turn.Mode.Surface
		toolCall.Phase = turn.Mode.Phase
		toolCall.Role = turn.Agent.Role
		toolCall.Profile = turn.Mode.Profile
		toolCall.PermissionMode = permission.Mode
	}
	if toolgateway.IsWebEvidenceTool(name) {
		authority, authorityErr := toolgateway.DecodeWebEvidenceCallAuthority(
			json.RawMessage(call.AuthorityJSON))
		if authorityErr != nil || authority.RunID != call.RunID ||
			authority.RootAgentID != turn.Agent.ID || authority.SessionID != turn.Run.SessionID ||
			authority.MissionID != turn.Mission.ID ||
			authority.WorkspaceID != turn.Mission.WorkspaceID {
			return domain.SupervisorToolResult{}, apperror.New(apperror.CodeFailedPrecondition,
				"durable web evidence authority does not match the active Supervisor turn")
		}
		toolCall.MissionID = authority.MissionID
		toolCall.Surface = authority.Surface
		toolCall.Phase = authority.Phase
		toolCall.Role = authority.Role
		toolCall.Profile = authority.Profile
		toolCall.PermissionMode = authority.PermissionMode
		toolCall.ModeRevision = authority.ModeRevision
		toolCall.PermissionRevision = authority.PermissionRevision
		toolCall.CapabilityGeneration = authority.Generation
	}
	toolCtx, cancelTool := context.WithTimeout(ctx, supervisorToolCallTimeout)
	outcome, err := s.tools.Invoke(toolCtx, toolCall)
	toolContextErr := toolCtx.Err()
	cancelTool()
	if ctx.Err() != nil {
		return domain.SupervisorToolResult{}, apperror.Normalize(ctx.Err())
	}
	if errors.Is(toolContextErr, context.DeadlineExceeded) {
		err = apperror.New(apperror.CodeDeadlineExceeded,
			"structured supervisor tool exceeded its 30 second execution limit")
	}
	completedAt := time.Now().UTC()
	if err != nil {
		code := apperror.CodeOf(apperror.Normalize(err))
		if !recoverableSupervisorToolError(name, code) {
			return domain.SupervisorToolResult{}, apperror.Normalize(err)
		}
		encoded, encodeErr := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
			Version: supervisorToolResultVersion, Tool: call.ToolName, Status: string(domain.SupervisorToolFailed),
			Code: string(code), Message: boundedSupervisorToolMessage(err.Error()),
		})
		if encodeErr != nil {
			return domain.SupervisorToolResult{}, encodeErr
		}
		return domain.SupervisorToolResult{
			CallID: call.CallID, Status: domain.SupervisorToolFailed, ResultJSON: string(encoded),
			ErrorCode: string(code), CompletedAt: completedAt,
		}, nil
	}
	if outcome.Proposal != nil && outcome.Proposal.Status == toolgateway.StatusProposed &&
		name == toolgateway.HostCommandProposeTool {
		return domain.SupervisorToolResult{}, errSupervisorWaitingApproval
	}
	if outcome.Result == nil {
		return domain.SupervisorToolResult{}, apperror.New(apperror.CodeInternal,
			"structured supervisor tool returned no result")
	}
	metadata := make(map[string]string, len(outcome.Result.Metadata))
	for key, value := range outcome.Result.Metadata {
		// Replay is an execution detail that can differ when two supervisors
		// recover the same pending call concurrently. Keep the durable/provider
		// result deterministic for the semantic operation.
		if key == "replayed" {
			continue
		}
		metadata[key] = redact.String(value)
	}
	status := domain.SupervisorToolCompleted
	code := ""
	message := ""
	if !outcome.Decision.Allowed || outcome.Result.Status == toolgateway.StatusDenied {
		status = domain.SupervisorToolDenied
		code = string(apperror.CodePolicyDenied)
		message = boundedSupervisorToolMessage(outcome.Decision.Reason)
	}
	envelope := supervisorToolResultEnvelope{
		Version: supervisorToolResultVersion, Tool: call.ToolName, Status: string(status),
		Metadata: metadata, Code: code, Message: message,
	}
	if name == toolgateway.DebugTerminalTool || name == toolgateway.CommandRuntimeTool ||
		name == toolgateway.MCPToolCallTool ||
		toolgateway.IsAgentCodeTool(name) || toolgateway.IsCodeIntelTool(name) ||
		toolgateway.IsWebEvidenceTool(name) {
		envelope.Stdout = redact.String(outcome.Result.Stdout)
		envelope.Stderr = redact.String(outcome.Result.Stderr)
		envelope.Truncated = outcome.Result.Truncated
	}
	encoded, err := marshalSupervisorToolResultEnvelope(envelope)
	if err != nil {
		return domain.SupervisorToolResult{}, err
	}
	return domain.SupervisorToolResult{
		CallID: call.CallID, Status: status, ResultJSON: string(encoded), ErrorCode: code,
		CompletedAt: completedAt,
	}, nil
}

func recoverableSupervisorToolError(name toolgateway.ToolName,
	code apperror.Code,
) bool {
	switch code {
	case apperror.CodeInvalidArgument, apperror.CodeConflict,
		apperror.CodeResourceExhausted, apperror.CodeDeadlineExceeded:
		return true
	case apperror.CodeFailedPrecondition, apperror.CodeNotFound, apperror.CodePolicyDenied:
		return name == toolgateway.DebugTerminalTool || name == toolgateway.CommandRuntimeTool ||
			name == toolgateway.MCPToolCallTool || toolgateway.IsAgentCodeTool(name) ||
			toolgateway.IsWebEvidenceTool(name) ||
			toolgateway.IsCodeIntelTool(name)
	case apperror.CodeUnavailable:
		return toolgateway.IsCodeIntelTool(name) || toolgateway.IsWebEvidenceTool(name)
	default:
		return false
	}
}

func boundedSupervisorToolMessage(value string) string {
	value = redact.String(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	runes := []rune(value)
	if len(runes) > 1024 {
		value = string(runes[:1024])
	}
	if value == "" {
		return "structured tool call failed"
	}
	return value
}

func supervisorRequestWithToolRounds(request llm.ChatRequest,
	rounds []domain.SupervisorToolRound,
) (llm.ChatRequest, error) {
	messages := append([]llm.Message(nil), request.Messages...)
	for _, round := range rounds {
		if err := round.Validate(); err != nil {
			return llm.ChatRequest{}, err
		}
		if !round.Complete() {
			return llm.ChatRequest{}, errors.New("cannot build model context with pending supervisor tools")
		}
		calls := make([]llm.ToolCall, 0, len(round.Calls))
		results := make([]llm.ToolResult, 0, len(round.Calls))
		for _, call := range round.Calls {
			calls = append(calls, llm.ToolCall{
				ID: call.CallID, Name: call.ToolName, Arguments: json.RawMessage(call.PayloadJSON),
			})
			results = append(results, llm.ToolResult{
				ToolCallID: call.CallID, Content: call.ResultJSON,
				IsError: call.Status == domain.SupervisorToolDenied || call.Status == domain.SupervisorToolFailed,
			})
		}
		messages = append(messages,
			llm.Message{Role: "assistant", ToolCalls: calls},
			llm.Message{Role: "user", ToolResults: results},
		)
	}
	request.Messages = messages
	metadata := make(map[string]string, len(request.Metadata)+1)
	for key, value := range request.Metadata {
		metadata[key] = value
	}
	metadata["tool_round"] = strconv.Itoa(len(rounds))
	request.Metadata = metadata
	return request, nil
}

func supervisorToolStats(rounds []domain.SupervisorToolRound) (int, int) {
	calls := 0
	for _, round := range rounds {
		calls += len(round.Calls)
	}
	return len(rounds), calls
}
