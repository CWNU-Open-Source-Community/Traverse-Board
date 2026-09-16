package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/scriptprocess"
	"cyberagent-workbench/internal/toolrun"
)

// The queue stays metadata-only. This bounded projection is read on demand and
// contains only the proposal bound to this approval, never raw tool output.
type ApprovalPreviewView struct {
	ProtocolVersion  string                     `json:"protocol_version"`
	RunID            string                     `json:"run_id"`
	ApprovalID       string                     `json:"approval_id"`
	ProposalID       string                     `json:"proposal_id"`
	ToolName         string                     `json:"tool_name"`
	WorkspaceID      string                     `json:"workspace_id"`
	Effect           string                     `json:"effect"`
	WorkingDirectory string                     `json:"working_directory"`
	Fields           []ApprovalPreviewFieldView `json:"fields"`
	SourceCurrent    bool                       `json:"source_current"`
	Redacted         bool                       `json:"redacted"`
	Truncated        bool                       `json:"truncated"`
}

type ApprovalPreviewFieldView struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (a *API) runApprovalPreview(request *http.Request, runID, approvalID string) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	if err := validatePathIdentity(approvalID); err != nil {
		return nil, nil, err
	}
	ctx := request.Context()
	run, err := a.store.GetRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	record, err := a.store.GetApproval(ctx, approvalID)
	if err != nil {
		return nil, nil, err
	}
	if record.RunID != run.ID {
		return nil, nil, apperror.New(apperror.CodeNotFound, "approval does not belong to this Run")
	}
	view := ApprovalPreviewView{ProtocolVersion: application.ApprovalQueueProtocolVersion,
		RunID: run.ID, ApprovalID: record.ID, ProposalID: record.ProposalID,
		ToolName: record.ToolName, WorkspaceID: record.WorkspaceID, Effect: "unavailable",
		Fields: []ApprovalPreviewFieldView{}}
	stale := func() (any, *Page, error) {
		return nil, nil, apperror.New(apperror.CodeFailedPrecondition,
			"approval proposal is unavailable or its exact binding has changed; refresh approvals")
	}
	add := func(name, raw string) {
		value := redact.String(raw)
		view.Redacted = view.Redacted || value != raw || strings.Contains(value, "[REDACTED:")
		runes := []rune(value)
		if len(runes) > 8192 {
			value = string(runes[:8192])
			view.Truncated = true
		}
		view.Fields = append(view.Fields, ApprovalPreviewFieldView{Name: name, Value: value})
	}
	view.SourceCurrent = !run.Terminal() && record.Status == approval.StatusPending &&
		record.GrantID == "" && record.Mode != "never"
	switch record.ToolName {
	case "shell":
		source, ok := a.store.(interface {
			GetToolRun(context.Context, string) (toolrun.ToolRun, error)
		})
		if !ok {
			return stale()
		}
		value, err := source.GetToolRun(ctx, record.ProposalID)
		if err != nil {
			return nil, nil, err
		}
		if value.ID != record.ProposalID || value.SessionID != record.SessionID ||
			value.WorkspaceID != record.WorkspaceID || record.RequestFingerprint !=
			approval.ShellFingerprint(value.SessionID, value.WorkspaceID, value.Command) {
			return stale()
		}
		view.Effect, view.WorkingDirectory = "dry_run", "."
		view.SourceCurrent = view.SourceCurrent && value.Status == toolrun.StatusProposed
		add("command", value.Command)
	case "script_process":
		source, ok := a.store.(interface {
			GetScriptProcess(context.Context, string) (scriptprocess.Process, error)
		})
		if !ok {
			return stale()
		}
		value, err := source.GetScriptProcess(ctx, record.ProposalID)
		if err != nil {
			return nil, nil, err
		}
		if value.ID != record.ProposalID || value.RunID != record.RunID ||
			value.SessionID != record.SessionID || value.WorkspaceID != record.WorkspaceID ||
			value.ApprovalFingerprint != record.RequestFingerprint || value.Validate() != nil {
			return stale()
		}
		view.Effect, view.WorkingDirectory = "dry_run", value.WorkingDirectory
		view.SourceCurrent = view.SourceCurrent && value.Status == scriptprocess.StatusProposed &&
			value.ExecutionMode == scriptprocess.ExecutionDisabled
		add("executable", value.Executable)
		arguments, _ := json.Marshal(value.Arguments)
		add("arguments", string(arguments))
		add("requested_backend", value.RequestedBackend)
	case gitadvanced.ApprovalToolName:
		source, ok := a.store.(interface {
			GetGitAdvancedOperation(context.Context, string) (gitadvanced.OperationRecord, bool, error)
		})
		if !ok {
			return stale()
		}
		value, found, err := source.GetGitAdvancedOperation(ctx, record.ProposalID)
		if err != nil {
			return nil, nil, err
		}
		var preview gitadvanced.Preview
		if !found || value.RunID != record.RunID || value.SessionID != record.SessionID ||
			value.WorkspaceID != record.WorkspaceID || value.ApprovalFingerprint != record.RequestFingerprint ||
			json.Unmarshal([]byte(value.PreviewJSON), &preview) != nil ||
			preview.ID != value.PreviewID || preview.ApprovalFingerprint != record.RequestFingerprint ||
			preview.Spec.Validate() != nil || preview.Operation != value.Operation {
			return stale()
		}
		view.Effect, view.WorkingDirectory = "record_git_approval", "."
		view.SourceCurrent = view.SourceCurrent && value.Status == gitadvanced.OperationProposed &&
			value.ApprovalID == "" && preview.Executable()
		add("operation", string(value.Operation))
		spec, _ := json.MarshalIndent(preview.Spec, "", "  ")
		add("parameters", string(spec))
		add("summary", preview.Summary)
	case "replace_file", "create_file", "move_file", "delete_file":
		value, err := a.store.GetFileEditPreview(ctx, record.ProposalID)
		if err != nil {
			return nil, nil, err
		}
		belongs, err := a.store.FileEditWorkspaceBelongsToRun(ctx, run.ID, value.SessionID, value.WorkspaceID)
		if err != nil {
			return nil, nil, err
		}
		if value.ID != record.ProposalID || value.SessionID != run.SessionID ||
			value.SessionID != record.SessionID || value.WorkspaceID != record.WorkspaceID || !belongs {
			return stale()
		}
		mission, err := a.store.GetMission(ctx, run.MissionID)
		if err != nil {
			return nil, nil, err
		}
		target, targetErr := application.ResolveRunFileWorkspace(ctx, a.store, run, mission, a.fileWorkspaceDrydocks)
		view.SourceCurrent = view.SourceCurrent && value.Status == "proposed" &&
			targetErr == nil && value.WorkspaceID == target.Workspace.ID
		view.Effect, view.WorkingDirectory = "file_review_required", "."
		add("operation", value.Operation)
		add("path", value.Path)
		if value.DestinationPath != "" {
			add("destination_path", value.DestinationPath)
		}
		view.Redacted = view.Redacted || value.SecretsRedacted
	case "web_fetch":
		source, ok := a.store.(interface {
			GetWebFetchAuthorizationByApproval(context.Context, string) (domain.WebFetchAuthorization, error)
		})
		if !ok {
			return stale()
		}
		value, err := source.GetWebFetchAuthorizationByApproval(ctx, record.ID)
		if err != nil {
			return nil, nil, err
		}
		if value.ApprovalID != record.ID || value.RunID != record.RunID ||
			value.SessionID != record.SessionID || value.WorkspaceID != record.WorkspaceID ||
			value.RequestFingerprint != record.RequestFingerprint {
			return stale()
		}
		view.Effect = "fetch_public_https"
		view.SourceCurrent = view.SourceCurrent || (!run.Terminal() &&
			len(recoverableWebFetchApprovalActions(value)) > 0)
		add("url", value.CanonicalURL)
		add("host", value.ExactTarget)
	default:
		view.SourceCurrent = false
	}
	if view.Truncated {
		view.SourceCurrent = false
	}
	return view, nil, nil
}
