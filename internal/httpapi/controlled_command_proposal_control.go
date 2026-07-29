package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/runner"
)

const (
	ControlledCommandProposalCollectionPathTemplate = "/api/v1/runs/{run_id}/command-proposals"
	ControlledCommandProposalDetailPathTemplate     = "/api/v1/runs/{run_id}/command-proposals/{proposal_id}"
	ControlledCommandProposalReviewPathTemplate     = "/api/v1/runs/{run_id}/command-proposals/{proposal_id}/review"
)

type ControlledCommandProposalController interface {
	List(context.Context, string, int) (
		[]application.ControlledCommandProposalView, error)
	Get(context.Context, string) (
		application.ControlledCommandProposalView, error)
	Review(context.Context, application.ReviewControlledCommandProposalRequest) (
		application.ReviewControlledCommandProposalResult, error)
}

type ControlledCommandProposalReviewRequestView struct {
	Version          string `json:"version"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason,omitempty"`
	ConfirmExecution bool   `json:"confirm_execution,omitempty"`
}

type ControlledCommandProposalReviewView struct {
	ID                           string `json:"id"`
	Decision                     string `json:"decision"`
	ReviewedBy                   string `json:"reviewed_by"`
	Reason                       string `json:"reason"`
	SingleUseExecutionAuthorized bool   `json:"single_use_execution_authorized"`
	CapabilityGrant              bool   `json:"capability_grant"`
	CreatedAt                    string `json:"created_at"`
}

type ControlledCommandProposalResultView struct {
	ID                    string `json:"id"`
	Status                string `json:"status"`
	SourceKind            string `json:"source_kind"`
	SourceRef             string `json:"source_ref"`
	ContentSHA256         string `json:"content_sha256"`
	InstructionAuthorized bool   `json:"instruction_authorized"`
	RawOutputPersisted    bool   `json:"raw_output_persisted"`
	AutomaticRetryAllowed bool   `json:"automatic_retry_allowed"`
	CreatedAt             string `json:"created_at"`
}

type ControlledCommandExecutionReceiptView struct {
	RequestID               string `json:"request_id"`
	Backend                 string `json:"backend"`
	ExitCode                int    `json:"exit_code"`
	StdoutObservedBytes     int64  `json:"stdout_observed_bytes"`
	StdoutCapturedBytes     int    `json:"stdout_captured_bytes"`
	StdoutPrefixSHA256      string `json:"stdout_prefix_sha256"`
	StdoutTruncated         bool   `json:"stdout_truncated"`
	StderrObservedBytes     int64  `json:"stderr_observed_bytes"`
	StderrCapturedBytes     int    `json:"stderr_captured_bytes"`
	StderrPrefixSHA256      string `json:"stderr_prefix_sha256"`
	StderrTruncated         bool   `json:"stderr_truncated"`
	StartedAt               string `json:"started_at"`
	CompletedAt             string `json:"completed_at"`
	TimedOut                bool   `json:"timed_out"`
	Cancelled               bool   `json:"cancelled"`
	OutputLimitExceeded     bool   `json:"output_limit_exceeded"`
	TreeReaped              bool   `json:"tree_reaped"`
	RestrictedToken         bool   `json:"restricted_token"`
	LowIntegrityToken       bool   `json:"low_integrity_token"`
	JobAssignedAtCreation   bool   `json:"job_assigned_at_creation"`
	KillOnJobClose          bool   `json:"kill_on_job_close"`
	ActiveProcessLimit      int    `json:"active_process_limit"`
	ProcessMemoryLimit      int64  `json:"process_memory_limit"`
	StdinClosed             bool   `json:"stdin_closed"`
	EnvironmentInherited    bool   `json:"environment_inherited"`
	NetworkRequested        bool   `json:"network_requested"`
	PersistentProcess       bool   `json:"persistent_process"`
	ProductExecutionEnabled bool   `json:"product_execution_enabled"`
}

type ControlledCommandProposalView struct {
	ID                       string                                 `json:"id"`
	ProtocolVersion          string                                 `json:"protocol_version"`
	PolicyVersion            string                                 `json:"policy_version"`
	RunID                    string                                 `json:"run_id"`
	MissionID                string                                 `json:"mission_id"`
	SessionID                string                                 `json:"session_id"`
	WorkspaceID              string                                 `json:"workspace_id"`
	Kind                     string                                 `json:"kind"`
	RelativePath             string                                 `json:"relative_path,omitempty"`
	TimeoutMilliseconds      int64                                  `json:"timeout_milliseconds"`
	Purpose                  string                                 `json:"purpose"`
	PermissionMode           string                                 `json:"permission_mode"`
	PermissionRevision       int64                                  `json:"permission_revision"`
	OperatorReviewRequired   bool                                   `json:"operator_review_required"`
	InstructionAuthorized    bool                                   `json:"instruction_authorized"`
	ExecutionAuthorized      bool                                   `json:"execution_authorized"`
	CapabilityGrant          bool                                   `json:"capability_grant"`
	Fingerprint              string                                 `json:"fingerprint"`
	CreatedAt                string                                 `json:"created_at"`
	Review                   *ControlledCommandProposalReviewView   `json:"review,omitempty"`
	Result                   *ControlledCommandProposalResultView   `json:"result,omitempty"`
	Receipt                  *ControlledCommandExecutionReceiptView `json:"receipt,omitempty"`
	ReviewReplayed           bool                                   `json:"review_replayed,omitempty"`
	ExecutionReplayed        bool                                   `json:"execution_replayed,omitempty"`
	UntrustedEvidence        string                                 `json:"untrusted_evidence,omitempty"`
	EvidenceInstructionTrust bool                                   `json:"evidence_instruction_authorized"`
}

type controlledCommandProposalRoute int

const (
	controlledCommandProposalCollection controlledCommandProposalRoute = iota + 1
	controlledCommandProposalDetail
	controlledCommandProposalReview
)

func matchControlledCommandProposalPath(requestPath string) (
	runID string,
	proposalID string,
	route controlledCommandProposalRoute,
	matched bool,
) {
	const prefix = "/api/v1/runs/"
	if !strings.HasPrefix(requestPath, prefix) {
		return "", "", 0, false
	}
	segments := strings.Split(strings.TrimPrefix(requestPath, prefix), "/")
	if len(segments) == 2 && segments[0] != "" &&
		segments[1] == "command-proposals" {
		return segments[0], "", controlledCommandProposalCollection, true
	}
	if len(segments) == 3 && segments[0] != "" &&
		segments[1] == "command-proposals" && segments[2] != "" {
		return segments[0], segments[2],
			controlledCommandProposalDetail, true
	}
	if len(segments) == 4 && segments[0] != "" &&
		segments[1] == "command-proposals" && segments[2] != "" &&
		segments[3] == "review" {
		return segments[0], segments[2],
			controlledCommandProposalReview, true
	}
	return "", "", 0, false
}

func (a *API) serveControlledCommandProposal(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	runID string,
	proposalID string,
	route controlledCommandProposalRoute,
) {
	if !a.controlledCommandProposalControlEnabled {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound,
				"HTTP API endpoint was not found"), http.StatusNotFound)
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if proposalID != "" {
		if err := validatePathIdentity(proposalID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	if route == controlledCommandProposalReview {
		a.serveControlledCommandProposalReview(
			writer, request, requestID, runID, proposalID)
		return
	}
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate",
			`Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodePolicyDenied,
				"valid bearer authorization is required"),
			http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeInvalidArgument,
				"controlled command proposal read endpoint only supports GET"),
			http.StatusMethodNotAllowed)
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeInvalidArgument,
				"controlled command proposal reads cannot contain a body"), 0)
		return
	}
	if route == controlledCommandProposalCollection {
		a.serveControlledCommandProposalList(
			writer, request, requestID, runID)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	view, err := a.controlledCommandProposalController.Get(
		request.Context(), proposalID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Proposal.RunID != runID {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound,
				"controlled command proposal was not found for this Run"), 0)
		return
	}
	a.writeSuccess(writer, requestID,
		controlledCommandProposalView(view, false, false, ""), nil)
}

func (a *API) serveControlledCommandProposalList(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	runID string,
) {
	values := request.URL.Query()
	if err := validateSingleQueryValues(values, "limit"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	pageRequest, err := parsePage(values, request.URL.Path)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	views, err := a.controlledCommandProposalController.List(
		request.Context(), runID, pageRequest.Limit)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	response := make([]ControlledCommandProposalView, 0, len(views))
	for _, view := range views {
		if view.Proposal.RunID != runID {
			a.writeError(writer, requestID,
				apperror.New(apperror.CodeInternal,
					"controlled command proposal list crossed its Run boundary"), 0)
			return
		}
		response = append(response,
			controlledCommandProposalView(view, false, false, ""))
	}
	a.writeSuccess(writer, requestID, response,
		&Page{Limit: pageRequest.Limit})
}

func (a *API) serveControlledCommandProposalReview(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	runID string,
	proposalID string,
) {
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate",
			`Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodePolicyDenied,
				"valid control bearer authorization is required"),
			http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeInvalidArgument,
				"controlled command proposal review only supports POST"),
			http.StatusMethodNotAllowed)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, body, err := a.readRunOperationRequest(
		request, "Controlled command proposal review")
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	var view ControlledCommandProposalReviewRequestView
	if err := decodeStrictRunOperation(body, &view,
		"Controlled command proposal review"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Version != runner.ControlledCommandReviewProtocolVersion {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeInvalidArgument,
				"controlled command proposal review protocol version is invalid"), 0)
		return
	}
	current, err := a.controlledCommandProposalController.Get(
		request.Context(), proposalID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if current.Proposal.RunID != runID {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound,
				"controlled command proposal was not found for this Run"), 0)
		return
	}
	result, err := a.controlledCommandProposalController.Review(
		request.Context(),
		application.ReviewControlledCommandProposalRequest{
			ProposalID: proposalID, Decision: view.Decision,
			OperationKey: operationKey, ReviewedBy: "http_control_operator",
			Reason: view.Reason, ConfirmExecution: view.ConfirmExecution,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if result.View.Proposal.RunID != runID ||
		result.View.Proposal.ID != proposalID {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeInternal,
				"controlled command review crossed its durable binding"), 0)
		return
	}
	a.writeSuccessStatus(writer, requestID,
		controlledCommandProposalView(
			result.View, result.ReviewReplayed,
			result.ExecutionReplayed, result.EvidenceContent),
		nil, http.StatusAccepted)
}

func controlledCommandProposalView(
	view application.ControlledCommandProposalView,
	reviewReplayed bool,
	executionReplayed bool,
	evidence string,
) ControlledCommandProposalView {
	proposal := view.Proposal
	result := ControlledCommandProposalView{
		ID: proposal.ID, ProtocolVersion: proposal.ProtocolVersion,
		PolicyVersion: proposal.PolicyVersion, RunID: proposal.RunID,
		MissionID: proposal.MissionID, SessionID: proposal.SessionID,
		WorkspaceID: proposal.WorkspaceID, Kind: string(proposal.Kind),
		RelativePath:        proposal.RelativePath,
		TimeoutMilliseconds: proposal.TimeoutMilliseconds,
		Purpose:             proposal.Purpose, PermissionMode: string(proposal.PermissionMode),
		PermissionRevision:       proposal.PermissionRevision,
		OperatorReviewRequired:   true,
		InstructionAuthorized:    proposal.InstructionAuthorized,
		ExecutionAuthorized:      proposal.ExecutionAuthorized,
		CapabilityGrant:          proposal.CapabilityGrant,
		Fingerprint:              proposal.Fingerprint,
		CreatedAt:                proposal.CreatedAt.Format(time.RFC3339Nano),
		ReviewReplayed:           reviewReplayed,
		ExecutionReplayed:        executionReplayed,
		UntrustedEvidence:        evidence,
		EvidenceInstructionTrust: false,
	}
	if view.Review != nil {
		result.Review = &ControlledCommandProposalReviewView{
			ID: view.Review.ID, Decision: string(view.Review.Decision),
			ReviewedBy: view.Review.ReviewedBy, Reason: view.Review.Reason,
			SingleUseExecutionAuthorized: view.Review.SingleUseExecutionAuthorized,
			CapabilityGrant:              view.Review.CapabilityGrant,
			CreatedAt:                    view.Review.CreatedAt.Format(time.RFC3339Nano),
		}
	}
	if view.Result != nil {
		result.Result = &ControlledCommandProposalResultView{
			ID: view.Result.ID, Status: string(view.Result.Status),
			SourceKind:            view.Result.SourceKind,
			SourceRef:             view.Result.SourceRef,
			ContentSHA256:         view.Result.ContentSHA256,
			InstructionAuthorized: view.Result.InstructionAuthorized,
			RawOutputPersisted:    view.Result.RawOutputPersisted,
			AutomaticRetryAllowed: view.Result.AutomaticRetryAllowed,
			CreatedAt:             view.Result.CreatedAt.Format(time.RFC3339Nano),
		}
	}
	if view.Receipt != nil {
		receipt := view.Receipt
		result.Receipt = &ControlledCommandExecutionReceiptView{
			RequestID: receipt.RequestID, Backend: receipt.Backend,
			ExitCode:                receipt.ExitCode,
			StdoutObservedBytes:     receipt.StdoutObservedBytes,
			StdoutCapturedBytes:     receipt.StdoutCapturedBytes,
			StdoutPrefixSHA256:      receipt.StdoutPrefixSHA256,
			StdoutTruncated:         receipt.StdoutTruncated,
			StderrObservedBytes:     receipt.StderrObservedBytes,
			StderrCapturedBytes:     receipt.StderrCapturedBytes,
			StderrPrefixSHA256:      receipt.StderrPrefixSHA256,
			StderrTruncated:         receipt.StderrTruncated,
			StartedAt:               receipt.StartedAt.Format(time.RFC3339Nano),
			CompletedAt:             receipt.CompletedAt.Format(time.RFC3339Nano),
			TimedOut:                receipt.TimedOut,
			Cancelled:               receipt.Cancelled,
			OutputLimitExceeded:     receipt.OutputLimitExceeded,
			TreeReaped:              receipt.TreeReaped,
			RestrictedToken:         receipt.RestrictedToken,
			LowIntegrityToken:       receipt.LowIntegrityToken,
			JobAssignedAtCreation:   receipt.JobAssignedAtCreation,
			KillOnJobClose:          receipt.KillOnJobClose,
			ActiveProcessLimit:      receipt.ActiveProcessLimit,
			ProcessMemoryLimit:      receipt.ProcessMemoryLimit,
			StdinClosed:             receipt.StdinClosed,
			EnvironmentInherited:    receipt.EnvironmentInherited,
			NetworkRequested:        receipt.NetworkRequested,
			PersistentProcess:       receipt.PersistentProcess,
			ProductExecutionEnabled: receipt.ProductExecutionEnabled,
		}
	}
	return result
}
