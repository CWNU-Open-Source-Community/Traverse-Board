package desktop

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/store"
	terminalruntime "cyberagent-workbench/internal/terminal"
)

const DesktopUserTerminalProtocolVersion = "desktop_user_terminal.v1"

type UserTerminalController interface {
	Start(context.Context, DesktopTerminalStartRequest) (
		DesktopTerminalSession, error)
	Get(context.Context, string) (DesktopTerminalSession, error)
	Read(context.Context, DesktopTerminalReadRequest) (
		DesktopTerminalOutput, error)
	Write(context.Context, DesktopTerminalWriteRequest) (
		DesktopTerminalWriteResult, error)
	Resize(context.Context, DesktopTerminalResizeRequest) error
	Close(context.Context, DesktopTerminalCloseRequest) error
}

type DesktopTerminalStartRequest struct {
	ProtocolVersion      string `json:"protocol_version"`
	RunID                string `json:"run_id"`
	Columns              int    `json:"columns"`
	Rows                 int    `json:"rows"`
	ReplaceExisting      bool   `json:"replace_existing"`
	ConfirmDebugBoundary bool   `json:"confirm_debug_boundary"`
}

type DesktopTerminalReadRequest struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	Cursor          uint64 `json:"cursor"`
	MaxBytes        int    `json:"max_bytes"`
}

type DesktopTerminalWriteRequest struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	Data            string `json:"data"`
	UserConfirmed   bool   `json:"user_confirmed"`
}

type DesktopTerminalResizeRequest struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	Columns         int    `json:"columns"`
	Rows            int    `json:"rows"`
	UserConfirmed   bool   `json:"user_confirmed"`
}

type DesktopTerminalCloseRequest struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	UserConfirmed   bool   `json:"user_confirmed"`
}

type DesktopTerminalSession struct {
	ProtocolVersion       string `json:"protocol_version"`
	SessionID             string `json:"session_id"`
	RunID                 string `json:"run_id"`
	State                 string `json:"state"`
	Backend               string `json:"backend"`
	Columns               int    `json:"columns"`
	Rows                  int    `json:"rows"`
	OutputBaseCursor      uint64 `json:"output_base_cursor"`
	OutputNextCursor      uint64 `json:"output_next_cursor"`
	ExitCode              int    `json:"exit_code"`
	UserOwned             bool   `json:"user_owned"`
	AgentInputDefault     bool   `json:"agent_input_default"`
	JobAssignedAtCreation bool   `json:"job_assigned_at_creation"`
	KillOnJobClose        bool   `json:"kill_on_job_close"`
	Persistent            bool   `json:"persistent"`
	ProcessLocal          bool   `json:"process_local"`
	RawOutputPersisted    bool   `json:"raw_output_persisted"`
}

type DesktopTerminalOutput struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	BaseCursor      uint64 `json:"base_cursor"`
	NextCursor      uint64 `json:"next_cursor"`
	DataBase64      string `json:"data_base64"`
	DataBytes       int    `json:"data_bytes"`
	Dropped         bool   `json:"dropped"`
	State           string `json:"state"`
}

type DesktopTerminalWriteResult struct {
	ProtocolVersion string `json:"protocol_version"`
	SessionID       string `json:"session_id"`
	BytesWritten    int    `json:"bytes_written"`
}

// desktopUserTerminalService keeps all path and durable state lookups in Go.
// Its Wails projection accepts only Run/session IDs and user keystrokes.
type desktopUserTerminalService struct {
	store        *store.SQLiteStore
	manager      *terminalruntime.Manager
	capabilities domain.ExecutionPermissionRuntimeCapabilities
}

func newDesktopUserTerminalService(stateStore *store.SQLiteStore,
	manager *terminalruntime.Manager,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) (*desktopUserTerminalService, error) {
	if stateStore == nil || manager == nil || !manager.Available() {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"desktop user terminal runtime is unavailable")
	}
	if err := capabilities.Validate(); err != nil {
		return nil, apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop terminal permission capabilities are invalid", err)
	}
	return &desktopUserTerminalService{
		store: stateStore, manager: manager, capabilities: capabilities,
	}, nil
}

func (s *desktopUserTerminalService) Start(ctx context.Context,
	request DesktopTerminalStartRequest,
) (DesktopTerminalSession, error) {
	if request.ProtocolVersion != DesktopUserTerminalProtocolVersion ||
		!request.ConfirmDebugBoundary || !validWorkspaceIdentity(request.RunID) {
		return DesktopTerminalSession{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"desktop user terminal requires an explicit Debug confirmation")
	}
	run, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return DesktopTerminalSession{}, classifyTerminalLookup(err,
			"desktop terminal Run lookup failed")
	}
	if run.Terminal() {
		return DesktopTerminalSession{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"desktop terminal cannot start for a terminal Run")
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return DesktopTerminalSession{}, classifyTerminalLookup(err,
			"desktop terminal Mission lookup failed")
	}
	if !validWorkspaceIdentity(mission.WorkspaceID) {
		return DesktopTerminalSession{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"desktop terminal Run has no registered Workspace")
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return DesktopTerminalSession{}, classifyTerminalLookup(err,
			"desktop terminal Workspace lookup failed")
	}
	mode, err := s.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return DesktopTerminalSession{}, classifyTerminalLookup(err,
			"desktop terminal mode lookup failed")
	}
	profile, err := s.store.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		return DesktopTerminalSession{}, classifyTerminalLookup(err,
			"desktop terminal profile lookup failed")
	}
	interaction, err := s.store.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		return DesktopTerminalSession{}, classifyTerminalLookup(err,
			"desktop terminal interaction lookup failed")
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return DesktopTerminalSession{}, classifyTerminalLookup(err,
			"desktop terminal permission lookup failed")
	}
	decision, err := executionauth.EvaluateExecutionPermission(
		permission, s.capabilities, executionauth.PermissionRequest{
			Kind:              executionauth.PermissionOperationPersistentTerminal,
			HostFilesystem:    true,
			Network:           true,
			BackgroundProcess: true,
		})
	if err != nil {
		return DesktopTerminalSession{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"desktop terminal permission request is invalid", err)
	}
	if mode.Surface != domain.ExecutionSurfaceCode ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		interaction.Mode != domain.RunExecutionInteractionDebug ||
		interaction.WorkspaceTrust != domain.WorkspaceTrustTrusted ||
		permission.Mode != domain.RunExecutionPermissionDebug ||
		!decision.Allowed || !decision.PersistentTerminal ||
		!decision.BackgroundProcess {
		return DesktopTerminalSession{}, apperror.New(
			apperror.CodePolicyDenied,
			"desktop user terminal requires Code/Local/Debug and the current debug maximum-access process gate")
	}
	root := filepath.Clean(workspace.RootPath)
	session, err := s.manager.Start(ctx, terminalruntime.StartRequest{
		ID: idgen.New("user-terminal"),
		Scope: terminalruntime.SessionScope{
			WorkspaceID:              mission.WorkspaceID,
			RunID:                    run.ID,
			InteractionSnapshotID:    interaction.ID,
			InteractionRevision:      interaction.Revision,
			ExecutionProfileRevision: profile.Revision,
			PermissionSnapshotID:     permission.ID,
			PermissionRevision:       permission.Revision,
			PermissionMode:           permission.Mode,
			Mode:                     interaction.Mode,
		},
		WorkspaceRoot: root, Interaction: interaction,
		CurrentProfile: profile, CurrentPermission: permission,
		Columns: request.Columns, Rows: request.Rows,
		RequestedBy: "desktop_operator", OperatorConfirmed: true,
		ReplaceExisting: request.ReplaceExisting,
	})
	if err != nil {
		return DesktopTerminalSession{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"desktop user terminal start was denied", err)
	}
	return projectDesktopTerminalSession(session), nil
}

func (s *desktopUserTerminalService) Get(_ context.Context,
	sessionID string,
) (DesktopTerminalSession, error) {
	session, err := s.manager.Get(strings.TrimSpace(sessionID))
	if err != nil {
		return DesktopTerminalSession{}, apperror.Wrap(
			apperror.CodeNotFound, "desktop terminal session was not found", err)
	}
	return projectDesktopTerminalSession(session), nil
}

func (s *desktopUserTerminalService) Read(_ context.Context,
	request DesktopTerminalReadRequest,
) (DesktopTerminalOutput, error) {
	if request.ProtocolVersion != DesktopUserTerminalProtocolVersion {
		return DesktopTerminalOutput{}, apperror.New(
			apperror.CodeInvalidArgument,
			"desktop terminal read protocol is invalid")
	}
	page, err := s.manager.Read(strings.TrimSpace(request.SessionID),
		request.Cursor, request.MaxBytes)
	if err != nil {
		return DesktopTerminalOutput{}, apperror.Wrap(
			apperror.CodeNotFound, "desktop terminal output is unavailable", err)
	}
	return DesktopTerminalOutput{
		ProtocolVersion: DesktopUserTerminalProtocolVersion,
		SessionID:       page.SessionID, BaseCursor: page.BaseCursor,
		NextCursor: page.NextCursor,
		DataBase64: base64.StdEncoding.EncodeToString(page.Data),
		DataBytes:  len(page.Data), Dropped: page.Dropped,
		State: string(page.State),
	}, nil
}

func (s *desktopUserTerminalService) Write(ctx context.Context,
	request DesktopTerminalWriteRequest,
) (DesktopTerminalWriteResult, error) {
	if request.ProtocolVersion != DesktopUserTerminalProtocolVersion {
		return DesktopTerminalWriteResult{}, apperror.New(
			apperror.CodeInvalidArgument,
			"desktop terminal input protocol is invalid")
	}
	count, err := s.manager.WriteUser(ctx, terminalruntime.UserInputRequest{
		SessionID: strings.TrimSpace(request.SessionID),
		Data:      []byte(request.Data), RequestedBy: "desktop_operator",
		UserConfirmed: request.UserConfirmed,
	})
	if err != nil {
		return DesktopTerminalWriteResult{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"desktop terminal user input was denied", err)
	}
	return DesktopTerminalWriteResult{
		ProtocolVersion: DesktopUserTerminalProtocolVersion,
		SessionID:       strings.TrimSpace(request.SessionID), BytesWritten: count,
	}, nil
}

func (s *desktopUserTerminalService) Resize(_ context.Context,
	request DesktopTerminalResizeRequest,
) error {
	if request.ProtocolVersion != DesktopUserTerminalProtocolVersion {
		return apperror.New(apperror.CodeInvalidArgument,
			"desktop terminal resize protocol is invalid")
	}
	return s.manager.Resize(strings.TrimSpace(request.SessionID),
		request.Columns, request.Rows, "desktop_operator",
		request.UserConfirmed)
}

func (s *desktopUserTerminalService) Close(_ context.Context,
	request DesktopTerminalCloseRequest,
) error {
	if request.ProtocolVersion != DesktopUserTerminalProtocolVersion {
		return apperror.New(apperror.CodeInvalidArgument,
			"desktop terminal close protocol is invalid")
	}
	return s.manager.Close(strings.TrimSpace(request.SessionID),
		"desktop_operator", request.UserConfirmed)
}

func (s *desktopUserTerminalService) reconcileBindings(ctx context.Context) int {
	if s == nil || s.store == nil || s.manager == nil ||
		ctx == nil || ctx.Err() != nil {
		return 0
	}
	closed := 0
	for _, session := range s.manager.ActiveSessions() {
		run, err := s.store.GetRun(ctx, session.Scope.RunID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				_ = s.manager.CloseForBindingInvalidation(session.ID)
				closed++
			}
			continue
		}
		if run.Terminal() {
			_ = s.manager.CloseForRunTermination(run.ID)
			closed++
			continue
		}
		mode, modeErr := s.store.GetRunMode(ctx, run.ID)
		mission, missionErr := s.store.GetMission(ctx, run.MissionID)
		var workspace store.WorkspaceRecord
		var workspaceErr error
		if missionErr == nil {
			workspace, workspaceErr = s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
		}
		profile, profileErr := s.store.GetRunExecutionProfile(ctx, run.ID)
		interaction, interactionErr := s.store.GetRunExecutionInteraction(ctx, run.ID)
		permission, permissionErr := s.store.GetRunExecutionPermission(ctx, run.ID)
		if modeErr != nil || missionErr != nil || workspaceErr != nil ||
			profileErr != nil || interactionErr != nil ||
			permissionErr != nil {
			if errors.Is(modeErr, sql.ErrNoRows) ||
				errors.Is(missionErr, sql.ErrNoRows) ||
				errors.Is(workspaceErr, sql.ErrNoRows) ||
				errors.Is(profileErr, sql.ErrNoRows) ||
				errors.Is(interactionErr, sql.ErrNoRows) ||
				errors.Is(permissionErr, sql.ErrNoRows) {
				_ = s.manager.CloseForBindingInvalidation(session.ID)
				closed++
			}
			continue
		}
		workspaceRootSHA256, rootErr := terminalruntime.WorkspaceRootSHA256(
			filepath.Clean(workspace.RootPath))
		if mode.Surface != domain.ExecutionSurfaceCode ||
			rootErr != nil || mission.WorkspaceID != session.Scope.WorkspaceID ||
			workspace.ID != session.Scope.WorkspaceID ||
			workspaceRootSHA256 != session.WorkspaceRootSHA256 ||
			profile.Profile != domain.RunExecutionProfileLocal ||
			interaction.ID != session.Scope.InteractionSnapshotID ||
			interaction.Revision != session.Scope.InteractionRevision ||
			profile.Revision != session.Scope.ExecutionProfileRevision ||
			permission.ID != session.Scope.PermissionSnapshotID ||
			permission.Revision != session.Scope.PermissionRevision ||
			permission.Mode != session.Scope.PermissionMode ||
			permission.Mode != domain.RunExecutionPermissionDebug ||
			!s.capabilities.Allows(permission.Mode) ||
			interaction.Mode != session.Scope.Mode ||
			interaction.Mode != domain.RunExecutionInteractionDebug ||
			interaction.ExecutionProfileRevision != profile.Revision {
			_ = s.manager.CloseForBindingInvalidation(session.ID)
			closed++
		}
	}
	return closed
}

func projectDesktopTerminalSession(
	session terminalruntime.Session,
) DesktopTerminalSession {
	return DesktopTerminalSession{
		ProtocolVersion: DesktopUserTerminalProtocolVersion,
		SessionID:       session.ID, RunID: session.Scope.RunID,
		State: string(session.State), Backend: session.Backend,
		Columns: session.Columns, Rows: session.Rows,
		OutputBaseCursor: session.OutputBaseCursor,
		OutputNextCursor: session.OutputNextCursor, ExitCode: session.ExitCode,
		UserOwned:             session.UserOwned,
		AgentInputDefault:     session.AgentInputDefault,
		JobAssignedAtCreation: session.JobAssignedAtCreation,
		KillOnJobClose:        session.KillOnJobClose, Persistent: session.Persistent,
		ProcessLocal:       session.ProcessLocal,
		RawOutputPersisted: session.RawOutputPersisted,
	}
}

func classifyTerminalLookup(err error, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.New(apperror.CodeNotFound, message)
	}
	return apperror.Wrap(apperror.CodeUnavailable, message, err)
}
