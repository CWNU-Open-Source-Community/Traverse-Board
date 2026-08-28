package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

const ThreadExecutionPermissionControlPathTemplate = "/api/v1/threads/{thread_id}/execution-permission"

type ThreadExecutionPermissionControlRequestView struct {
	Mode                    string `json:"mode"`
	Reason                  string `json:"reason,omitempty"`
	ConfirmWorkspaceAccess  bool   `json:"confirm_workspace_access,omitempty"`
	ConfirmUserApproval     bool   `json:"confirm_user_approval,omitempty"`
	ConfirmDangerFullAccess bool   `json:"confirm_danger_full_access,omitempty"`
	ConfirmDebugAccess      bool   `json:"confirm_debug_access,omitempty"`
}

type ThreadExecutionPermissionView struct {
	ThreadID                     string                                  `json:"thread_id"`
	ProtocolVersion              string                                  `json:"protocol_version"`
	Revision                     int64                                   `json:"revision"`
	Mode                         string                                  `json:"mode"`
	ApprovalPolicy               string                                  `json:"approval_policy"`
	CommandScope                 string                                  `json:"command_scope"`
	FilesystemScope              string                                  `json:"filesystem_scope"`
	NetworkScope                 string                                  `json:"network_scope"`
	PersistentTerminal           bool                                    `json:"persistent_terminal"`
	BackgroundProcess            bool                                    `json:"background_process"`
	AgentTerminalInput           bool                                    `json:"agent_terminal_input"`
	RiskTier                     string                                  `json:"risk_tier"`
	RequiredGate                 string                                  `json:"required_gate"`
	PolicyVersion                string                                  `json:"policy_version"`
	OperatorConfirmed            bool                                    `json:"operator_confirmed"`
	RuntimeGateAvailable         bool                                    `json:"runtime_gate_available"`
	Runtime                      ExecutionPermissionRuntimeView          `json:"runtime"`
	CapabilityMatrix             ExecutionPermissionCapabilityMatrixView `json:"capability_matrix"`
	CreatedAt                    time.Time                               `json:"created_at"`
	ProcessEnabled               bool                                    `json:"process_enabled"`
	ExecutionAuthorized          bool                                    `json:"execution_authorized"`
	CapabilityGrant              bool                                    `json:"capability_grant"`
	AppliesToCurrentRun          bool                                    `json:"applies_to_current_run"`
	AppliesToFutureSuccessorRuns bool                                    `json:"applies_to_future_successor_runs"`
}

type ThreadExecutionPermissionControlView struct {
	ExecutionPermission    ThreadExecutionPermissionView `json:"execution_permission"`
	CurrentRunID           string                        `json:"current_run_id,omitempty"`
	CurrentRunEffect       string                        `json:"current_run_effect,omitempty"`
	CurrentRunMode         string                        `json:"current_run_mode,omitempty"`
	CurrentRunSynchronized bool                          `json:"current_run_synchronized"`
	Replayed               bool                          `json:"replayed"`
}

func matchThreadExecutionPermissionControlPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/threads/"
	const suffix = "/execution-permission"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	threadID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if threadID == "" || strings.Contains(threadID, "/") {
		return "", false
	}
	return threadID, true
}

func (a *API) serveThreadExecutionPermissionControl(writer http.ResponseWriter,
	request *http.Request, requestID, threadID string,
) {
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeInvalidArgument,
				"Thread execution permission endpoint only supports GET and POST"),
			http.StatusMethodNotAllowed)
		return
	}
	tokenHash := a.tokenHash
	if request.Method == http.MethodPost {
		if !a.executionPermissionControlEnabled {
			a.writeError(writer, requestID,
				apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
				http.StatusNotFound)
			return
		}
		tokenHash = a.controlTokenHash
	}
	if !a.authorized(request, tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodePolicyDenied,
				"valid bearer authorization is required"),
			http.StatusUnauthorized)
		return
	}
	if err := validatePathIdentity(threadID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	service := application.NewThreadExecutionPermissionService(
		a.store, a.executionPermissionCapabilities)
	if request.Method == http.MethodGet {
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			a.writeError(writer, requestID,
				apperror.New(apperror.CodeInvalidArgument,
					"Thread execution permission GET cannot contain a body"), 0)
			return
		}
		current, err := service.Inspect(request.Context(), threadID)
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		projected := threadExecutionPermissionView(
			current.Permission, a.executionPermissionCapabilities)
		projected.AppliesToCurrentRun = current.CurrentRunSynchronized
		effect := string(domain.ThreadExecutionPermissionNoActiveRun)
		if current.CurrentRunID != "" {
			effect = "pending"
			if current.CurrentRunSynchronized {
				effect = string(domain.ThreadExecutionPermissionApplied)
			}
		}
		a.writeSuccess(writer, requestID, ThreadExecutionPermissionControlView{
			ExecutionPermission: projected, CurrentRunID: current.CurrentRunID,
			CurrentRunEffect: effect, CurrentRunMode: string(current.CurrentRunMode),
			CurrentRunSynchronized: current.CurrentRunSynchronized,
		}, nil)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, err := runExecutionProfileIdempotencyKey(request.Header)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	body, err := readBoundedControlBody(request)
	if err != nil {
		status := 0
		if apperror.CodeOf(apperror.Normalize(err)) == apperror.CodeResourceExhausted {
			status = http.StatusRequestEntityTooLarge
		}
		a.writeError(writer, requestID, err, status)
		return
	}
	var view ThreadExecutionPermissionControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"Thread execution permission body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	result, err := service.Change(request.Context(),
		application.ChangeThreadExecutionPermissionRequest{
			ThreadID: threadID, Mode: view.Mode, OperationKey: operationKey,
			RequestedBy: "http_control", Reason: view.Reason,
			ConfirmWorkspaceAccess:  view.ConfirmWorkspaceAccess,
			ConfirmUserApproval:     view.ConfirmUserApproval,
			ConfirmDangerFullAccess: view.ConfirmDangerFullAccess,
			ConfirmDebugAccess:      view.ConfirmDebugAccess,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	projected := threadExecutionPermissionView(
		result.Permission, a.executionPermissionCapabilities)
	projected.AppliesToCurrentRun = result.CurrentRunID != ""
	currentRunMode := ""
	if result.CurrentRunID != "" {
		currentRunMode = string(result.Permission.Mode)
	}
	a.writeSuccessStatus(writer, requestID, ThreadExecutionPermissionControlView{
		ExecutionPermission: projected, CurrentRunID: result.CurrentRunID,
		CurrentRunEffect:       string(result.CurrentRunEffect),
		CurrentRunMode:         currentRunMode,
		CurrentRunSynchronized: result.CurrentRunID != "", Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}

func threadExecutionPermissionView(value domain.ThreadExecutionPermissionSnapshot,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) ThreadExecutionPermissionView {
	matrix, _ := value.CapabilityMatrix()
	return ThreadExecutionPermissionView{
		ThreadID: value.ThreadID, ProtocolVersion: value.ProtocolVersion,
		Revision: value.Revision, Mode: string(value.Mode),
		ApprovalPolicy: string(value.ApprovalPolicy), CommandScope: string(value.CommandScope),
		FilesystemScope: string(value.FilesystemScope), NetworkScope: string(value.NetworkScope),
		PersistentTerminal: value.PersistentTerminal,
		BackgroundProcess:  value.BackgroundProcess, AgentTerminalInput: value.AgentTerminalInput,
		RiskTier: string(value.RiskTier), RequiredGate: string(value.RequiredGate),
		PolicyVersion: value.PolicyVersion, OperatorConfirmed: value.OperatorConfirmed,
		RuntimeGateAvailable: capabilities.Allows(value.Mode),
		Runtime:              executionPermissionRuntimeView(capabilities),
		CapabilityMatrix: ExecutionPermissionCapabilityMatrixView{
			WorkspaceRead: matrix.WorkspaceRead, WorkspaceWrite: matrix.WorkspaceWrite,
			SandboxedCommandRuntime: matrix.SandboxedCommandRuntime,
			UnsandboxedHostProcess:  matrix.UnsandboxedHostProcess,
			NetworkAccess:           matrix.NetworkAccess, CredentialAccess: matrix.CredentialAccess,
			UserHomeAccess:          matrix.UserHomeAccess,
			PersistentUserTerminal:  matrix.PersistentUserTerminal,
			PersistentAgentTerminal: matrix.PersistentAgentTerminal,
			FullCDP:                 matrix.FullCDP, OutOfScopePolicy: string(matrix.OutOfScopePolicy),
		},
		CreatedAt: value.CreatedAt, ProcessEnabled: value.ProcessEnabled,
		ExecutionAuthorized: value.ExecutionAuthorized, CapabilityGrant: value.CapabilityGrant,
		AppliesToCurrentRun: false, AppliesToFutureSuccessorRuns: true,
	}
}
