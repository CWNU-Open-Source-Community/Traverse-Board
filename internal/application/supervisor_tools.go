package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

const supervisorToolResultVersion = "supervisor_tool_result.v1"

const supervisorToolCallTimeout = 30 * time.Second

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

func supervisorStructuredToolSpecs(surface domain.ExecutionSurface,
	phase domain.ExecutionPhase,
	permissionMode domain.RunExecutionPermissionMode,
	skillCandidateEnabled bool,
	debugTerminalEnabled bool,
	commandRuntimeEnabled ...bool,
) []llm.ToolSpec {
	runtimeEnabled := len(commandRuntimeEnabled) > 0 && commandRuntimeEnabled[0]
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
			(permissionMode != domain.RunExecutionPermissionApproval ||
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
				permissionMode != domain.RunExecutionPermissionFullAccess) {
			continue
		}
		out = append(out, llm.ToolSpec{
			Name: string(definition.Name), Description: definition.Description,
			Parameters: append(json.RawMessage(nil), definition.InputSchema...),
		})
	}
	return out
}

func prepareSupervisorToolCalls(calls []llm.ToolCall, runID string, turn int, round int,
	surface domain.ExecutionSurface, phase domain.ExecutionPhase,
	permissionMode domain.RunExecutionPermissionMode,
	skillCandidateEnabled bool,
	debugTerminalEnabled bool,
	commandRuntimeEnabled ...bool,
) ([]llm.ToolCall, error) {
	runtimeEnabled := len(commandRuntimeEnabled) > 0 && commandRuntimeEnabled[0]
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
			name != toolgateway.CommandRuntimeTool {
			return nil, fmt.Errorf("provider requested unsupported supervisor tool %q", call.Name)
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
			(permissionMode != domain.RunExecutionPermissionApproval ||
				surface != domain.ExecutionSurfaceCode) {
			return nil, errors.New(
				"provider requested host command proposal outside Code/approval mode")
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
				permissionMode != domain.RunExecutionPermissionFullAccess) {
			return nil, errors.New(
				"provider requested command runtime outside Code/Deliver/full-access runtime")
		}
		payload, err := toolgateway.NormalizeSupervisorToolPayload(name, call.Arguments)
		if err != nil {
			return nil, err
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
		out[index] = llm.ToolCall{ID: callID, Name: string(name), Arguments: payload}
	}
	return out, nil
}

func supervisorToolOperationKey(runID string, turn int, name toolgateway.ToolName,
	payload json.RawMessage,
) string {
	return runmutation.SupervisorToolOperationKey(runID, turn, string(name), string(payload))
}

func (s *RunSupervisor) resumeSupervisorTools(ctx context.Context, turn domain.SupervisorTurn,
	rounds []domain.SupervisorToolRound,
) ([]domain.SupervisorToolRound, error) {
	for _, round := range rounds {
		for _, call := range round.Calls {
			if call.Status != domain.SupervisorToolPending {
				continue
			}
			result, err := s.invokeSupervisorTool(ctx, turn, call)
			if err != nil {
				return rounds, err
			}
			if _, _, err := s.store.RecordSupervisorToolResult(ctx, turn.Checkpoint, result); err != nil {
				return rounds, apperror.Normalize(err)
			}
		}
	}
	return s.store.ListSupervisorToolRounds(ctx, turn.Checkpoint)
}

func (s *RunSupervisor) invokeSupervisorTool(ctx context.Context, turn domain.SupervisorTurn,
	call domain.SupervisorToolCall,
) (domain.SupervisorToolResult, error) {
	name := toolgateway.ToolName(call.ToolName)
	operationKey := supervisorToolOperationKey(call.RunID, call.Turn, name, json.RawMessage(call.PayloadJSON))
	toolCtx, cancelTool := context.WithTimeout(ctx, supervisorToolCallTimeout)
	outcome, err := s.tools.Invoke(toolCtx, toolgateway.ToolCall{
		Name: name, Payload: json.RawMessage(call.PayloadJSON), OperationKey: operationKey,
		RunID: call.RunID, AgentID: turn.Agent.ID, SessionID: turn.Run.SessionID,
		WorkspaceID: turn.Mission.WorkspaceID,
		LeaseID:     turn.Checkpoint.LeaseID, LeaseGeneration: turn.Checkpoint.LeaseGeneration,
		RequestedBy: "run_supervisor",
	})
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
		encoded, encodeErr := json.Marshal(supervisorToolResultEnvelope{
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
	if name == toolgateway.DebugTerminalTool || name == toolgateway.CommandRuntimeTool {
		envelope.Stdout = redact.String(outcome.Result.Stdout)
		envelope.Stderr = redact.String(outcome.Result.Stderr)
		envelope.Truncated = outcome.Result.Truncated
	}
	encoded, err := json.Marshal(envelope)
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
	case apperror.CodeFailedPrecondition, apperror.CodeNotFound:
		return name == toolgateway.DebugTerminalTool ||
			name == toolgateway.CommandRuntimeTool
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
