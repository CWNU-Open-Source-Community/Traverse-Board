package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

const MaxWorkspaceCheckpointRequestBodyBytes = 64 * 1024

type WorkspaceCheckpointController interface {
	Capture(context.Context, application.WorkspaceCheckpointCaptureRequest) (
		workspacecheckpoint.Checkpoint, bool, error)
	Timeline(context.Context, string, int) (application.WorkspaceCheckpointTimeline, error)
	Restore(context.Context, application.WorkspaceRestoreRequest) (
		application.WorkspaceRestoreResult, error)
	Undo(context.Context, string, string, string, string, bool) (
		application.WorkspaceRestoreResult, error)
	Redo(context.Context, string, string, string, string, bool) (
		application.WorkspaceRestoreResult, error)
	Fork(context.Context, application.WorkspaceForkRequest) (
		application.WorkspaceForkResult, error)
}

type workspaceCheckpointCaptureView struct {
	OperationKey string `json:"operation_key"`
	Title        string `json:"title,omitempty"`
}

type workspaceCheckpointPreviewView struct {
	TargetCheckpointID          string `json:"target_checkpoint_id"`
	ExpectedCurrentCheckpointID string `json:"expected_current_checkpoint_id"`
}

type workspaceCheckpointRewindView struct {
	TargetCheckpointID          string `json:"target_checkpoint_id"`
	ExpectedCurrentCheckpointID string `json:"expected_current_checkpoint_id"`
	OperationKey                string `json:"operation_key"`
	Confirm                     *bool  `json:"confirm"`
}

type workspaceCheckpointCursorActionView struct {
	ExpectedCurrentCheckpointID string `json:"expected_current_checkpoint_id"`
	OperationKey                string `json:"operation_key"`
	Confirm                     *bool  `json:"confirm"`
}

type workspaceCheckpointForkView struct {
	TargetCheckpointID          string `json:"target_checkpoint_id"`
	ExpectedCurrentCheckpointID string `json:"expected_current_checkpoint_id"`
	OperationKey                string `json:"operation_key"`
	WorkspaceName               string `json:"workspace_name"`
	Branch                      string `json:"branch"`
	Goal                        string `json:"goal,omitempty"`
	Confirm                     *bool  `json:"confirm"`
}

type workspaceCheckpointForkResultView struct {
	ProtocolVersion string                                `json:"protocol_version"`
	SourceRunID     string                                `json:"source_run_id"`
	Target          workspacecheckpoint.Checkpoint        `json:"target"`
	Workspace       workspaceCheckpointForkWorkspaceView  `json:"workspace"`
	Mission         workspaceCheckpointForkMissionView    `json:"mission"`
	Run             workspaceCheckpointForkRunView        `json:"run"`
	Continuity      workspaceCheckpointForkContinuityView `json:"continuity_node"`
	Checkpoint      workspacecheckpoint.Checkpoint        `json:"checkpoint"`
	Transaction     workspacecheckpoint.Transaction       `json:"transaction"`
	NotInherited    []string                              `json:"not_inherited"`
	Replayed        bool                                  `json:"replayed"`
}

type workspaceCheckpointForkWorkspaceView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type workspaceCheckpointForkMissionView struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Profile     string `json:"profile"`
}

type workspaceCheckpointForkRunView struct {
	ID        string    `json:"id"`
	MissionID string    `json:"mission_id"`
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type workspaceCheckpointForkContinuityView struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	SessionID    string    `json:"session_id"`
	RunID        string    `json:"run_id"`
	WorkspaceID  string    `json:"workspace_id"`
	SourceNodeID string    `json:"source_node_id,omitempty"`
	GitBranch    string    `json:"git_branch,omitempty"`
	GitHead      string    `json:"git_head,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func matchWorkspaceCheckpointPath(value string) (string, string, bool) {
	segments := strings.Split(strings.TrimPrefix(value, "/api/v1/"), "/")
	if len(segments) == 3 && segments[0] == "runs" &&
		segments[2] == "workspace-checkpoints" && segments[1] != "" {
		return segments[1], "", true
	}
	if len(segments) == 4 && segments[0] == "runs" &&
		segments[2] == "workspace-checkpoints" && segments[1] != "" {
		switch segments[3] {
		case "preview", "rewind", "undo", "redo", "fork":
			return segments[1], segments[3], true
		}
	}
	return "", "", false
}

func (a *API) serveWorkspaceCheckpoint(writer http.ResponseWriter,
	request *http.Request, requestID, runID, action string,
) {
	if a.workspaceCheckpointController == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if request.Method == http.MethodGet {
		a.serveWorkspaceCheckpointTimeline(writer, request, requestID, runID, action)
		return
	}
	if !a.workspaceCheckpointControlEnabled {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid control bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Workspace checkpoint mutation only supports POST"), http.StatusMethodNotAllowed)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	body, err := readBoundedRequestBody(request, MaxWorkspaceCheckpointRequestBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Workspace checkpoint"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.serveWorkspaceCheckpointMutation(writer, request, requestID, runID, action, body)
}

func (a *API) serveWorkspaceCheckpointTimeline(writer http.ResponseWriter,
	request *http.Request, requestID, runID, action string,
) {
	if action != "" {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Workspace checkpoint action only supports POST"), http.StatusMethodNotAllowed)
		return
	}
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"read-only HTTP API requests cannot contain a body"), 0)
		return
	}
	if err := validateSingleQueryValues(request.URL.Query(), "limit"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
				"Workspace checkpoint limit must be an integer"), 0)
			return
		}
		limit = parsed
	}
	value, err := a.workspaceCheckpointController.Timeline(request.Context(), runID, limit)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, value, nil)
}

func (a *API) serveWorkspaceCheckpointMutation(writer http.ResponseWriter,
	request *http.Request, requestID, runID, action string, body []byte,
) {
	decode := func(destination any) error {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(destination); err != nil {
			return apperror.Wrap(apperror.CodeInvalidArgument,
				"Workspace checkpoint body must be one JSON object", err)
		}
		return ensureJSONEOF(decoder)
	}
	switch action {
	case "":
		var view workspaceCheckpointCaptureView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err := requireWorkspaceCheckpointFields("operation_key", view.OperationKey); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, _, err := a.workspaceCheckpointController.Capture(request.Context(),
			application.WorkspaceCheckpointCaptureRequest{RunID: runID,
				OperationKey: view.OperationKey, RequestedBy: "api_operator", Title: view.Title})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, value, nil, http.StatusCreated)
	case "preview":
		var view workspaceCheckpointPreviewView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err := requireWorkspaceCheckpointFields(
			"target_checkpoint_id", view.TargetCheckpointID,
			"expected_current_checkpoint_id", view.ExpectedCurrentCheckpointID,
		); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.workspaceCheckpointController.Restore(request.Context(),
			application.WorkspaceRestoreRequest{RunID: runID,
				TargetCheckpointID:          view.TargetCheckpointID,
				ExpectedCurrentCheckpointID: view.ExpectedCurrentCheckpointID,
				RequestedBy:                 "api_operator", Kind: workspacecheckpoint.TransactionRewind,
				TriggerReceiptID: view.TargetCheckpointID})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case "rewind":
		var view workspaceCheckpointRewindView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err := requireWorkspaceCheckpointFields(
			"target_checkpoint_id", view.TargetCheckpointID,
			"expected_current_checkpoint_id", view.ExpectedCurrentCheckpointID,
			"operation_key", view.OperationKey,
		); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if view.Confirm == nil || !*view.Confirm {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
				"Workspace rewind requires confirm=true"), 0)
			return
		}
		value, err := a.workspaceCheckpointController.Restore(request.Context(),
			application.WorkspaceRestoreRequest{RunID: runID,
				TargetCheckpointID:          view.TargetCheckpointID,
				ExpectedCurrentCheckpointID: view.ExpectedCurrentCheckpointID,
				OperationKey:                view.OperationKey, RequestedBy: "api_operator",
				Kind:             workspacecheckpoint.TransactionRewind,
				TriggerReceiptID: view.TargetCheckpointID, Confirm: true})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case "undo", "redo":
		var view workspaceCheckpointCursorActionView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err := requireWorkspaceCheckpointFields(
			"expected_current_checkpoint_id", view.ExpectedCurrentCheckpointID,
			"operation_key", view.OperationKey,
		); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if view.Confirm == nil || !*view.Confirm {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
				"Workspace checkpoint action requires confirm=true"), 0)
			return
		}
		var value application.WorkspaceRestoreResult
		var err error
		if action == "undo" {
			value, err = a.workspaceCheckpointController.Undo(request.Context(), runID,
				view.ExpectedCurrentCheckpointID, view.OperationKey, "api_operator", true)
		} else {
			value, err = a.workspaceCheckpointController.Redo(request.Context(), runID,
				view.ExpectedCurrentCheckpointID, view.OperationKey, "api_operator", true)
		}
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccess(writer, requestID, value, nil)
	case "fork":
		var view workspaceCheckpointForkView
		if err := decode(&view); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err := requireWorkspaceCheckpointFields(
			"target_checkpoint_id", view.TargetCheckpointID,
			"expected_current_checkpoint_id", view.ExpectedCurrentCheckpointID,
			"operation_key", view.OperationKey,
			"workspace_name", view.WorkspaceName,
			"branch", view.Branch,
		); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if view.Confirm == nil || !*view.Confirm {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
				"Workspace fork requires confirm=true"), 0)
			return
		}
		value, err := a.workspaceCheckpointController.Fork(request.Context(),
			application.WorkspaceForkRequest{RunID: runID,
				TargetCheckpointID:          view.TargetCheckpointID,
				ExpectedCurrentCheckpointID: view.ExpectedCurrentCheckpointID,
				OperationKey:                view.OperationKey, RequestedBy: "api_operator",
				WorkspaceName: view.WorkspaceName, Branch: view.Branch, Goal: view.Goal,
				Confirm: true})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, workspaceCheckpointForkResultProjection(value),
			nil, http.StatusCreated)
	}
}

func workspaceCheckpointForkResultProjection(
	value application.WorkspaceForkResult,
) workspaceCheckpointForkResultView {
	return workspaceCheckpointForkResultView{
		ProtocolVersion: value.ProtocolVersion,
		SourceRunID:     value.SourceRunID,
		Target:          value.Target,
		Workspace: workspaceCheckpointForkWorkspaceView{ID: value.Workspace.ID,
			Name: value.Workspace.Name, CreatedAt: value.Workspace.CreatedAt},
		Mission: workspaceCheckpointForkMissionView{ID: value.Mission.ID,
			WorkspaceID: value.Mission.WorkspaceID, Profile: string(value.Mission.Profile)},
		Run: workspaceCheckpointForkRunView{ID: value.Run.ID,
			MissionID: value.Run.MissionID, SessionID: value.Run.SessionID,
			Status: string(value.Run.Status), CreatedAt: value.Run.CreatedAt},
		Continuity: workspaceCheckpointForkContinuityView{ID: value.Node.ID,
			Kind: string(value.Node.Kind), SessionID: value.Node.SessionID,
			RunID: value.Node.RunID, WorkspaceID: value.Node.WorkspaceID,
			SourceNodeID: value.Node.SourceNodeID, GitBranch: value.Node.GitBranch,
			GitHead: value.Node.GitHead, CreatedAt: value.Node.CreatedAt},
		Checkpoint: value.Checkpoint, Transaction: value.Transaction,
		NotInherited: append([]string(nil), value.NotInherited...), Replayed: value.Replayed,
	}
}

func requireWorkspaceCheckpointFields(nameValuePairs ...string) error {
	for index := 0; index+1 < len(nameValuePairs); index += 2 {
		if strings.TrimSpace(nameValuePairs[index+1]) == "" {
			return apperror.New(apperror.CodeInvalidArgument,
				"Workspace checkpoint "+nameValuePairs[index]+" is required")
		}
	}
	return nil
}
