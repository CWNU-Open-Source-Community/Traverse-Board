package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/workspace"
)

type AgentCodeToolStore interface {
	FileEditApplyStore
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	GetAgentNode(context.Context, string) (domain.AgentNode, error)
	GetRunExecutionLease(context.Context, string) (domain.RunExecutionLease, bool, error)
}

type AgentCodeToolExecutor struct {
	store   AgentCodeToolStore
	manager *fileedit.Manager
	apply   *FileEditApplyService
}

type agentCodeGitHubEvidenceStore interface {
	ListGitHubReviewEvidence(context.Context, string, int) ([]githubreview.EvidenceRecord, error)
	GetGitHubReviewEvidence(context.Context, string) (githubreview.EvidenceRecord, bool, error)
}

func NewAgentCodeToolExecutor(store AgentCodeToolStore,
	checker policy.Checker,
) *AgentCodeToolExecutor {
	checkpoints := embeddedWorkspaceCheckpointService(store,
		domain.ExecutionPermissionRuntimeCapabilities{})
	return &AgentCodeToolExecutor{store: store, manager: fileedit.NewManager(store),
		apply: NewFileEditApplyService(store, checker, checkpoints)}
}

func (e *AgentCodeToolExecutor) ExecuteAgentCode(ctx context.Context,
	scope toolgateway.AgentCodeExecutionScope, name toolgateway.ToolName,
	payload json.RawMessage,
) (toolgateway.AgentCodeExecutionResult, error) {
	if e == nil || e.store == nil || e.manager == nil || e.apply == nil {
		return toolgateway.AgentCodeExecutionResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "agent code tool dependencies are unavailable")
	}
	if err := e.validateScope(ctx, scope, name); err != nil {
		return toolgateway.AgentCodeExecutionResult{}, err
	}
	var value any
	var metadata = map[string]string{"workspace_id": scope.WorkspaceID}
	switch name {
	case toolgateway.WorkspaceListTool:
		var input toolgateway.WorkspaceListPayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		result, err := workspace.AgentCodeListDirectory(scope.WorkspaceRoot,
			scope.WorkspaceID, input.Path, input.Cursor, input.Limit, false)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		value = result
		metadata["result_count"] = fmt.Sprint(len(result.Items))
		metadata["truncated"] = fmt.Sprint(result.Truncated)
	case toolgateway.WorkspaceReadTool:
		var input toolgateway.WorkspaceReadPayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		result, err := workspace.AgentCodeReadFile(scope.WorkspaceRoot,
			scope.WorkspaceID, input.Path, input.StartLine, input.EndLine, false)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		value = result
		metadata["path"] = result.Path
		metadata["content_sha256"] = result.ContentSHA256
		metadata["truncated"] = fmt.Sprint(result.Truncated)
	case toolgateway.WorkspaceGlobTool:
		var input toolgateway.WorkspaceGlobPayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		result, err := workspace.AgentCodeGlobFiles(scope.WorkspaceRoot,
			scope.WorkspaceID, input.Pattern, input.Cursor, input.Limit, false)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		value = result
		metadata["result_count"] = fmt.Sprint(len(result.Paths))
		metadata["truncated"] = fmt.Sprint(result.Truncated)
	case toolgateway.WorkspaceGrepTool:
		var input toolgateway.WorkspaceGrepPayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		result, err := workspace.AgentCodeGrepFiles(scope.WorkspaceRoot,
			scope.WorkspaceID, input.Query, input.Pattern, input.Cursor, input.Limit,
			input.CaseSensitive, false)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		value = result
		metadata["result_count"] = fmt.Sprint(len(result.Matches))
		metadata["truncated"] = fmt.Sprint(result.Truncated)
	case toolgateway.GitHubEvidenceListTool:
		var input toolgateway.GitHubEvidenceListPayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		githubStore, ok := e.store.(agentCodeGitHubEvidenceStore)
		if !ok {
			return toolgateway.AgentCodeExecutionResult{}, apperror.New(
				apperror.CodeFailedPrecondition, "GitHub review evidence store is unavailable")
		}
		result, err := githubStore.ListGitHubReviewEvidence(ctx, scope.RunID, input.Limit)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, apperror.Normalize(err)
		}
		value = result
		metadata["result_count"] = fmt.Sprint(len(result))
		metadata["trust"] = "untrusted_remote_data"
	case toolgateway.GitHubEvidenceReadTool:
		var input toolgateway.GitHubEvidenceReadPayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		githubStore, ok := e.store.(agentCodeGitHubEvidenceStore)
		if !ok {
			return toolgateway.AgentCodeExecutionResult{}, apperror.New(
				apperror.CodeFailedPrecondition, "GitHub review evidence store is unavailable")
		}
		result, found, err := githubStore.GetGitHubReviewEvidence(ctx, input.EvidenceID)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, apperror.Normalize(err)
		}
		if !found || result.RunID != scope.RunID || result.WorkspaceID != scope.WorkspaceID {
			return toolgateway.AgentCodeExecutionResult{}, apperror.New(
				apperror.CodeNotFound, "GitHub review evidence was not found for this Run")
		}
		value = result
		metadata["evidence_id"] = result.ID
		metadata["trust"] = "untrusted_remote_data"
	case toolgateway.WorkspaceChangeTool:
		var input toolgateway.WorkspaceChangePayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		result, replayed, err := e.propose(ctx, scope, input)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		value = agentCodeEditResult(result, false)
		metadata["edit_id"] = result.ID
		metadata["status"] = result.Status
		metadata["operation"] = result.Operation
		encoded, err := json.Marshal(value)
		return toolgateway.AgentCodeExecutionResult{JSON: string(encoded),
			Metadata: metadata, Replayed: replayed}, err
	case toolgateway.WorkspaceApplyTool:
		var input toolgateway.WorkspaceApplyPayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		result, err := e.applyChange(ctx, scope, input)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		value = agentCodeApplyResult(result)
		metadata["edit_id"] = result.Edit.ID
		metadata["status"] = result.Edit.Status
		metadata["operation"] = result.Edit.Operation
		encoded, err := json.Marshal(value)
		return toolgateway.AgentCodeExecutionResult{JSON: string(encoded),
			Metadata: metadata, Replayed: result.Replayed}, err
	case toolgateway.WorkspaceDeleteTool:
		var input toolgateway.WorkspaceDeletePayload
		if err := json.Unmarshal(payload, &input); err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		if input.Action == "propose" {
			result, replayed, err := e.proposeDelete(ctx, scope, input)
			if err != nil {
				return toolgateway.AgentCodeExecutionResult{}, err
			}
			value = agentCodeEditResult(result, true)
			metadata["edit_id"] = result.ID
			metadata["status"] = result.Status
			metadata["operation"] = result.Operation
			encoded, err := json.Marshal(value)
			return toolgateway.AgentCodeExecutionResult{JSON: string(encoded),
				Metadata: metadata, Replayed: replayed}, err
		}
		result, err := e.applyDelete(ctx, scope, input)
		if err != nil {
			return toolgateway.AgentCodeExecutionResult{}, err
		}
		value = agentCodeApplyResult(result)
		metadata["edit_id"] = result.Edit.ID
		metadata["status"] = result.Edit.Status
		metadata["operation"] = result.Edit.Operation
		encoded, err := json.Marshal(value)
		return toolgateway.AgentCodeExecutionResult{JSON: string(encoded),
			Metadata: metadata, Replayed: result.Replayed}, err
	default:
		return toolgateway.AgentCodeExecutionResult{}, apperror.New(
			apperror.CodeInvalidArgument, "unsupported agent code tool")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return toolgateway.AgentCodeExecutionResult{}, err
	}
	return toolgateway.AgentCodeExecutionResult{JSON: string(encoded), Metadata: metadata}, nil
}

func (e *AgentCodeToolExecutor) validateScope(ctx context.Context,
	scope toolgateway.AgentCodeExecutionScope, name toolgateway.ToolName,
) error {
	if err := scope.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"agent code tool scope is invalid", err)
	}
	run, err := e.store.GetRun(ctx, scope.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	mission, err := e.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return apperror.Normalize(err)
	}
	linkedSession, err := e.store.GetSession(ctx, run.SessionID)
	if err != nil {
		return apperror.Normalize(err)
	}
	registered, err := e.store.GetWorkspaceInfo(ctx, mission.WorkspaceID)
	if err != nil {
		return apperror.Normalize(err)
	}
	mode, err := e.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return apperror.Normalize(err)
	}
	permission, err := e.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return apperror.Normalize(err)
	}
	agent, err := e.store.GetAgentNode(ctx, scope.RootAgentID)
	if err != nil {
		return apperror.Normalize(err)
	}
	lease, found, err := e.store.GetRunExecutionLease(ctx, run.ID)
	if err != nil {
		return apperror.Normalize(err)
	}
	rootFingerprint, err := workspace.AgentCodeRootFingerprint(registered.RootPath)
	if err != nil {
		return apperror.Normalize(err)
	}
	if run.Status != domain.RunRunning || linkedSession.Status != session.StatusActive ||
		run.MissionID != scope.MissionID || run.SessionID != scope.SessionID ||
		mission.ID != scope.MissionID || mission.WorkspaceID != scope.WorkspaceID ||
		linkedSession.WorkspaceID != scope.WorkspaceID || registered.ID != scope.WorkspaceID ||
		registered.RootPath != scope.WorkspaceRoot || rootFingerprint != scope.RootFingerprint ||
		mode.Revision != scope.ModeRevision || mode.Surface != scope.Surface ||
		mode.Phase != scope.Phase || mode.Profile != scope.Profile ||
		permission.Revision != scope.PermissionRevision || permission.Mode != scope.PermissionMode ||
		agent.RunID != run.ID || agent.SessionID != run.SessionID ||
		agent.Role != domain.AgentRoleRoot || agent.Profile != scope.Profile ||
		!found || !lease.ActiveAt(scopeNow()) || lease.LeaseID != scope.LeaseID ||
		lease.Generation != scope.LeaseGeneration {
		return apperror.New(apperror.CodeFailedPrecondition,
			"agent code tool authority binding changed before execution")
	}
	capabilities := toolgateway.AgentCodeCapabilities(toolgateway.AgentCodeCapabilityContext{
		RunID: run.ID, MissionID: mission.ID, RootAgentID: agent.ID,
		WorkspaceID:     registered.ID,
		RootFingerprint: rootFingerprint, Surface: mode.Surface, Phase: mode.Phase,
		Role: agent.Role, Profile: agent.Profile, PermissionMode: permission.Mode,
		ModeRevision: mode.Revision, PermissionRevision: permission.Revision})
	if capabilities.Generation != scope.CapabilityGeneration {
		return apperror.New(apperror.CodeConflict,
			"agent code capability generation changed before execution")
	}
	// code-intel tools deliberately reuse the exact Agent Code authority and
	// fencing checks above. Their reviewed server generation and capability
	// fingerprint are independently validated by the Go-owned LSP manager.
	if toolgateway.IsCodeIntelTool(name) {
		if available, reason := toolgateway.CodeIntelScopeEligibility(
			toolgateway.AgentCodeCapabilityContext{RunID: run.ID, MissionID: mission.ID,
				RootAgentID: agent.ID, WorkspaceID: registered.ID,
				RootFingerprint: rootFingerprint, Surface: mode.Surface, Phase: mode.Phase,
				Role: agent.Role, Profile: agent.Profile, PermissionMode: permission.Mode,
				ModeRevision: mode.Revision, PermissionRevision: permission.Revision}); !available {
			return apperror.New(apperror.CodePolicyDenied, reason)
		}
		return nil
	}
	for _, item := range capabilities.Tools {
		if item.Name == name {
			if !item.Available {
				return apperror.New(apperror.CodePolicyDenied, item.Refusal)
			}
			return nil
		}
	}
	return apperror.New(apperror.CodePolicyDenied,
		"agent code tool is absent from the current capability registry")
}

func (e *AgentCodeToolExecutor) propose(ctx context.Context,
	scope toolgateway.AgentCodeExecutionScope, input toolgateway.WorkspaceChangePayload,
) (fileedit.Edit, bool, error) {
	operation := fileedit.OperationReplace
	proposedText := ""
	destination := ""
	destinationHash := ""
	switch input.Action {
	case "propose_patch":
		text, observedHash, err := workspace.AgentCodeReadMutationSource(
			scope.WorkspaceRoot, input.Path)
		if err != nil {
			return fileedit.Edit{}, false, err
		}
		if observedHash != input.ExpectedSHA256 {
			return fileedit.Edit{}, false, apperror.New(apperror.CodeConflict,
				"workspace patch source changed after it was read")
		}
		proposedText = text
		for _, replacement := range input.Replacements {
			if count := strings.Count(proposedText, replacement.OldText); count != replacement.ExpectedOccurrences {
				return fileedit.Edit{}, false, apperror.New(apperror.CodeConflict,
					fmt.Sprintf("workspace patch expected %d occurrence(s), found %d",
						replacement.ExpectedOccurrences, count))
			}
			proposedText = strings.ReplaceAll(proposedText, replacement.OldText,
				replacement.NewText)
		}
	case "create":
		operation = fileedit.OperationCreate
		if _, _, err := workspace.AgentCodeResolveWritePath(scope.WorkspaceRoot,
			input.Path, true); err != nil {
			return fileedit.Edit{}, false, err
		}
		proposedText = input.Content
	case "move":
		operation = fileedit.OperationMove
		if _, _, err := workspace.AgentCodeResolveWritePath(scope.WorkspaceRoot,
			input.Path, false); err != nil {
			return fileedit.Edit{}, false, err
		}
		if _, _, err := workspace.AgentCodeResolveWritePath(scope.WorkspaceRoot,
			input.DestinationPath, true); err != nil {
			return fileedit.Edit{}, false, err
		}
		destination = input.DestinationPath
		destinationHash = input.DestinationExpectedSHA256
	default:
		return fileedit.Edit{}, false, apperror.New(apperror.CodeInvalidArgument,
			"workspace change action is invalid")
	}
	if redact.String(proposedText) != proposedText {
		return fileedit.Edit{}, false, apperror.New(apperror.CodePolicyDenied,
			"workspace mutation content contains secret-like material")
	}
	proposal := fileedit.Proposal{ID: agentCodeEditID(scope.OperationKey),
		SessionID: scope.SessionID, WorkspaceID: scope.WorkspaceID,
		WorkspaceRoot: scope.WorkspaceRoot, Path: input.Path, Operation: operation,
		DestinationPath: destination, ProposedText: proposedText,
		ExpectedOriginalHash:    input.ExpectedSHA256,
		ExpectedDestinationHash: destinationHash}
	return e.saveProposal(ctx, proposal)
}

func (e *AgentCodeToolExecutor) proposeDelete(ctx context.Context,
	scope toolgateway.AgentCodeExecutionScope, input toolgateway.WorkspaceDeletePayload,
) (fileedit.Edit, bool, error) {
	if _, _, err := workspace.AgentCodeResolveWritePath(scope.WorkspaceRoot,
		input.Path, false); err != nil {
		return fileedit.Edit{}, false, err
	}
	return e.saveProposal(ctx, fileedit.Proposal{ID: agentCodeEditID(scope.OperationKey),
		SessionID: scope.SessionID, WorkspaceID: scope.WorkspaceID,
		WorkspaceRoot: scope.WorkspaceRoot, Path: input.Path,
		Operation: fileedit.OperationDelete, ExpectedOriginalHash: input.ExpectedSHA256})
}

func (e *AgentCodeToolExecutor) saveProposal(ctx context.Context,
	proposal fileedit.Proposal,
) (fileedit.Edit, bool, error) {
	if existing, err := e.store.GetFileEdit(ctx, proposal.ID); err == nil {
		if !sameAgentCodeProposal(existing, proposal) {
			return fileedit.Edit{}, false, apperror.New(apperror.CodeConflict,
				"agent code operation key was already used for a different change")
		}
		return existing, true, nil
	} else if apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeNotFound {
		return fileedit.Edit{}, false, apperror.Normalize(err)
	}
	edit, err := e.manager.Propose(ctx, proposal)
	return edit, false, apperror.Normalize(err)
}

func sameAgentCodeProposal(edit fileedit.Edit, proposal fileedit.Proposal) bool {
	operation, err := fileedit.NormalizeOperation(proposal.Operation)
	return err == nil && edit.ID == proposal.ID && edit.SessionID == proposal.SessionID &&
		edit.WorkspaceID == proposal.WorkspaceID && edit.Path == proposal.Path &&
		edit.Operation == operation && edit.DestinationPath == proposal.DestinationPath &&
		edit.OriginalHash == proposal.ExpectedOriginalHash &&
		edit.DestinationOriginalHash == proposal.ExpectedDestinationHash &&
		(edit.Operation == fileedit.OperationMove || edit.Operation == fileedit.OperationDelete ||
			fileedit.HashText(proposal.ProposedText) == edit.ProposedHash)
}

func (e *AgentCodeToolExecutor) applyChange(ctx context.Context,
	scope toolgateway.AgentCodeExecutionScope, input toolgateway.WorkspaceApplyPayload,
) (ApplyFileEditResult, error) {
	edit, err := e.store.GetFileEdit(ctx, input.EditID)
	if err != nil {
		return ApplyFileEditResult{}, apperror.Normalize(err)
	}
	expectedOperation := fileedit.OperationReplace
	if input.ExpectedAction == "create" {
		expectedOperation = fileedit.OperationCreate
	} else if input.ExpectedAction == "move" {
		expectedOperation = fileedit.OperationMove
	}
	if agentCodeEditBindingMismatch(edit, scope) || edit.Operation != expectedOperation ||
		edit.OriginalHash != input.ExpectedOriginalSHA256 ||
		edit.ProposedHash != input.ExpectedProposedSHA256 ||
		edit.Operation == fileedit.OperationDelete {
		return ApplyFileEditResult{}, apperror.New(apperror.CodeConflict,
			"workspace apply expectations do not match the approved proposal")
	}
	return e.apply.Apply(ctx, ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion,
		RunID: scope.RunID, EditID: edit.ID, OperationKey: scope.OperationKey,
		AppliedBy: scope.RootAgentID, InvocationID: scope.InvocationID,
		CapabilityGeneration: scope.CapabilityGeneration, LeaseID: scope.LeaseID,
		LeaseGeneration: scope.LeaseGeneration})
}

func (e *AgentCodeToolExecutor) applyDelete(ctx context.Context,
	scope toolgateway.AgentCodeExecutionScope, input toolgateway.WorkspaceDeletePayload,
) (ApplyFileEditResult, error) {
	edit, err := e.store.GetFileEdit(ctx, input.EditID)
	if err != nil {
		return ApplyFileEditResult{}, apperror.Normalize(err)
	}
	if agentCodeEditBindingMismatch(edit, scope) || edit.Operation != fileedit.OperationDelete ||
		edit.Path != input.Path || edit.Path != input.ConfirmPath ||
		edit.OriginalHash != input.ExpectedSHA256 {
		return ApplyFileEditResult{}, apperror.New(apperror.CodeConflict,
			"workspace delete expectations do not match the approved proposal")
	}
	return e.apply.Apply(ctx, ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion,
		RunID: scope.RunID, EditID: edit.ID, OperationKey: scope.OperationKey,
		AppliedBy: scope.RootAgentID, InvocationID: scope.InvocationID,
		CapabilityGeneration: scope.CapabilityGeneration, LeaseID: scope.LeaseID,
		LeaseGeneration: scope.LeaseGeneration})
}

func agentCodeEditID(operationKey string) string {
	sum := sha256.Sum256([]byte("agent-code-edit.v1\x00" + operationKey))
	return "edit-" + hex.EncodeToString(sum[:16])
}

func agentCodeEditResult(edit fileedit.Edit, deleteConfirmation bool) map[string]any {
	return map[string]any{"version": toolgateway.AgentCodeRegistryVersion,
		"edit_id": edit.ID, "operation": edit.Operation, "path": edit.Path,
		"destination_path": edit.DestinationPath, "status": edit.Status,
		"diff": edit.Diff, "original_sha256": edit.OriginalHash,
		"proposed_sha256":             edit.ProposedHash,
		"destination_original_sha256": edit.DestinationOriginalHash,
		"destination_proposed_sha256": edit.DestinationProposedHash,
		"review_required":             true, "apply_authorized": false,
		"delete_confirmation_required": deleteConfirmation}
}

func agentCodeApplyResult(result ApplyFileEditResult) map[string]any {
	return map[string]any{"version": toolgateway.AgentCodeRegistryVersion,
		"edit_id": result.Edit.ID, "operation": result.Edit.Operation,
		"path": result.Edit.Path, "destination_path": result.Edit.DestinationPath,
		"status": result.Edit.Status, "original_sha256": result.Edit.OriginalHash,
		"proposed_sha256":             result.Edit.ProposedHash,
		"destination_proposed_sha256": result.Edit.DestinationProposedHash,
		"file_written":                result.FileWritten, "replayed": result.Replayed}
}

func agentCodeEditBindingMismatch(edit fileedit.Edit,
	scope toolgateway.AgentCodeExecutionScope,
) bool {
	return edit.SessionID != scope.SessionID || edit.WorkspaceID != scope.WorkspaceID
}

var scopeNow = func() time.Time { return time.Now().UTC() }
