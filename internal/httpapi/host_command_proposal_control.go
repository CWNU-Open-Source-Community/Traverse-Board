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
	HostCommandProposalCollectionPathTemplate = "/api/v1/runs/{run_id}/host-command-proposals"
	HostCommandProposalDetailPathTemplate     = "/api/v1/runs/{run_id}/host-command-proposals/{proposal_id}"
	HostCommandProposalReviewPathTemplate     = "/api/v1/runs/{run_id}/host-command-proposals/{proposal_id}/review"
)

type HostCommandProposalController interface {
	List(context.Context, string, int) ([]application.HostCommandProposalView, error)
	Get(context.Context, string) (application.HostCommandProposalView, error)
	Review(context.Context, application.ReviewHostCommandProposalRequest) (
		application.ReviewHostCommandProposalResult, error)
}

type HostCommandProposalReviewRequestView struct {
	Version          string `json:"version"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason,omitempty"`
	ConfirmExecution bool   `json:"confirm_execution,omitempty"`
}

type HostCommandProposalReviewView struct {
	ID                           string `json:"id"`
	Decision                     string `json:"decision"`
	ReviewedBy                   string `json:"reviewed_by"`
	Reason                       string `json:"reason"`
	SingleUseExecutionAuthorized bool   `json:"single_use_execution_authorized"`
	CapabilityGrant              bool   `json:"capability_grant"`
	CreatedAt                    string `json:"created_at"`
}

type HostCommandProposalResultView struct {
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

type HostCommandExecutionReceiptView struct {
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
	NonSandboxed            bool   `json:"non_sandboxed"`
	RestrictedToken         bool   `json:"restricted_token"`
	LowIntegrityToken       bool   `json:"low_integrity_token"`
	JobAssignedAtCreation   bool   `json:"job_assigned_at_creation"`
	KillOnJobClose          bool   `json:"kill_on_job_close"`
	ActiveProcessLimit      int    `json:"active_process_limit"`
	JobMemoryLimit          int64  `json:"job_memory_limit"`
	StdinClosed             bool   `json:"stdin_closed"`
	EnvironmentInherited    bool   `json:"environment_inherited"`
	NetworkRequested        bool   `json:"network_requested"`
	PersistentProcess       bool   `json:"persistent_process"`
	ProductExecutionEnabled bool   `json:"product_execution_enabled"`
}

type HostCommandProposalView struct {
	ID                       string                           `json:"id"`
	ProtocolVersion          string                           `json:"protocol_version"`
	PolicyVersion            string                           `json:"policy_version"`
	RunID                    string                           `json:"run_id"`
	MissionID                string                           `json:"mission_id"`
	SessionID                string                           `json:"session_id"`
	WorkspaceID              string                           `json:"workspace_id"`
	ExecutablePath           string                           `json:"executable_path"`
	ExecutableSHA256         string                           `json:"executable_sha256"`
	Argv                     []string                         `json:"argv"`
	WorkingDirectory         string                           `json:"working_directory"`
	EnvironmentPolicy        string                           `json:"environment_policy"`
	EnvironmentKeys          []string                         `json:"environment_keys"`
	EnvironmentSHA256        string                           `json:"environment_sha256"`
	NetworkIntent            string                           `json:"network_intent"`
	TimeoutMilliseconds      int64                            `json:"timeout_milliseconds"`
	Purpose                  string                           `json:"purpose"`
	SpecFingerprint          string                           `json:"spec_fingerprint"`
	PermissionMode           string                           `json:"permission_mode"`
	PermissionRevision       int64                            `json:"permission_revision"`
	OperatorReviewRequired   bool                             `json:"operator_review_required"`
	NonSandboxed             bool                             `json:"non_sandboxed"`
	AutomaticRetryAllowed    bool                             `json:"automatic_retry_allowed"`
	InstructionAuthorized    bool                             `json:"instruction_authorized"`
	ExecutionAuthorized      bool                             `json:"execution_authorized"`
	CapabilityGrant          bool                             `json:"capability_grant"`
	Fingerprint              string                           `json:"fingerprint"`
	CreatedAt                string                           `json:"created_at"`
	Review                   *HostCommandProposalReviewView   `json:"review,omitempty"`
	Result                   *HostCommandProposalResultView   `json:"result,omitempty"`
	Receipt                  *HostCommandExecutionReceiptView `json:"receipt,omitempty"`
	ReviewReplayed           bool                             `json:"review_replayed,omitempty"`
	ExecutionReplayed        bool                             `json:"execution_replayed,omitempty"`
	UntrustedEvidence        string                           `json:"untrusted_evidence,omitempty"`
	EvidenceInstructionTrust bool                             `json:"evidence_instruction_authorized"`
}

type hostCommandProposalRoute int

const (
	hostCommandProposalCollection hostCommandProposalRoute = iota + 1
	hostCommandProposalDetail
	hostCommandProposalReview
)

func matchHostCommandProposalPath(requestPath string) (
	runID string, proposalID string, route hostCommandProposalRoute, matched bool,
) {
	const prefix = "/api/v1/runs/"
	if !strings.HasPrefix(requestPath, prefix) {
		return "", "", 0, false
	}
	segments := strings.Split(strings.TrimPrefix(requestPath, prefix), "/")
	if len(segments) == 2 && segments[0] != "" &&
		segments[1] == "host-command-proposals" {
		return segments[0], "", hostCommandProposalCollection, true
	}
	if len(segments) == 3 && segments[0] != "" &&
		segments[1] == "host-command-proposals" && segments[2] != "" {
		return segments[0], segments[2], hostCommandProposalDetail, true
	}
	if len(segments) == 4 && segments[0] != "" &&
		segments[1] == "host-command-proposals" && segments[2] != "" &&
		segments[3] == "review" {
		return segments[0], segments[2], hostCommandProposalReview, true
	}
	return "", "", 0, false
}

func (a *API) serveHostCommandProposal(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string, proposalID string,
	route hostCommandProposalRoute,
) {
	if !a.hostCommandProposalControlEnabled {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound,
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
	if route == hostCommandProposalReview {
		a.serveHostCommandProposalReview(writer, request, requestID, runID, proposalID)
		return
	}
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"host command proposal read endpoint only supports GET"),
			http.StatusMethodNotAllowed)
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"host command proposal reads cannot contain a body"), 0)
		return
	}
	if route == hostCommandProposalCollection {
		a.serveHostCommandProposalList(writer, request, requestID, runID)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	view, err := a.hostCommandProposalController.Get(request.Context(), proposalID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Proposal.RunID != runID {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound,
			"host command proposal was not found for this Run"), 0)
		return
	}
	a.writeSuccess(writer, requestID,
		hostCommandProposalView(view, false, false, ""), nil)
}

func (a *API) serveHostCommandProposalList(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
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
	views, err := a.hostCommandProposalController.List(
		request.Context(), runID, pageRequest.Limit)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	response := make([]HostCommandProposalView, 0, len(views))
	for _, view := range views {
		if view.Proposal.RunID != runID {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInternal,
				"host command proposal list crossed its Run boundary"), 0)
			return
		}
		response = append(response,
			hostCommandProposalView(view, false, false, ""))
	}
	a.writeSuccess(writer, requestID, response, &Page{Limit: pageRequest.Limit})
}

func (a *API) serveHostCommandProposalReview(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string, proposalID string,
) {
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid control bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"host command proposal review only supports POST"),
			http.StatusMethodNotAllowed)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, body, err := a.readRunOperationRequest(
		request, "Host command proposal review")
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	var view HostCommandProposalReviewRequestView
	if err := decodeStrictRunOperation(body, &view,
		"Host command proposal review"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if view.Version != runner.HostCommandReviewProtocolVersion {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"host command proposal review protocol version is invalid"), 0)
		return
	}
	current, err := a.hostCommandProposalController.Get(request.Context(), proposalID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if current.Proposal.RunID != runID {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound,
			"host command proposal was not found for this Run"), 0)
		return
	}
	result, err := a.hostCommandProposalController.Review(request.Context(),
		application.ReviewHostCommandProposalRequest{
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
		a.writeError(writer, requestID, apperror.New(apperror.CodeInternal,
			"host command review crossed its durable binding"), 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, hostCommandProposalView(
		result.View, result.ReviewReplayed, result.ExecutionReplayed,
		result.EvidenceContent), nil, http.StatusAccepted)
}

func hostCommandProposalView(view application.HostCommandProposalView,
	reviewReplayed bool, executionReplayed bool, evidence string,
) HostCommandProposalView {
	proposal := view.Proposal
	spec := proposal.Spec
	result := HostCommandProposalView{
		ID: proposal.ID, ProtocolVersion: proposal.ProtocolVersion,
		PolicyVersion: proposal.PolicyVersion, RunID: proposal.RunID,
		MissionID: proposal.MissionID, SessionID: proposal.SessionID,
		WorkspaceID:    proposal.WorkspaceID,
		ExecutablePath: spec.ExecutablePath, ExecutableSHA256: spec.ExecutableSHA256,
		Argv: append([]string(nil), spec.Argv...), WorkingDirectory: spec.WorkingDirectory,
		EnvironmentPolicy: spec.EnvironmentPolicy,
		EnvironmentKeys:   append([]string(nil), spec.EnvironmentKeys...),
		EnvironmentSHA256: spec.EnvironmentSHA256,
		NetworkIntent:     string(spec.NetworkIntent), TimeoutMilliseconds: spec.TimeoutMilliseconds,
		Purpose: spec.Purpose, SpecFingerprint: spec.Fingerprint,
		PermissionMode:         string(proposal.PermissionMode),
		PermissionRevision:     proposal.PermissionRevision,
		OperatorReviewRequired: true, NonSandboxed: true,
		AutomaticRetryAllowed: false,
		InstructionAuthorized: proposal.InstructionAuthorized,
		ExecutionAuthorized:   proposal.ExecutionAuthorized,
		CapabilityGrant:       proposal.CapabilityGrant, Fingerprint: proposal.Fingerprint,
		CreatedAt:      proposal.CreatedAt.Format(time.RFC3339Nano),
		ReviewReplayed: reviewReplayed, ExecutionReplayed: executionReplayed,
		UntrustedEvidence: evidence, EvidenceInstructionTrust: false,
	}
	if view.Review != nil {
		result.Review = &HostCommandProposalReviewView{
			ID: view.Review.ID, Decision: string(view.Review.Decision),
			ReviewedBy: view.Review.ReviewedBy, Reason: view.Review.Reason,
			SingleUseExecutionAuthorized: view.Review.SingleUseExecutionAuthorized,
			CapabilityGrant:              view.Review.CapabilityGrant,
			CreatedAt:                    view.Review.CreatedAt.Format(time.RFC3339Nano),
		}
	}
	if view.Result != nil {
		result.Result = &HostCommandProposalResultView{
			ID: view.Result.ID, Status: view.Result.Status,
			SourceKind: view.Result.SourceKind, SourceRef: view.Result.SourceRef,
			ContentSHA256:         view.Result.ContentSHA256,
			InstructionAuthorized: view.Result.InstructionAuthorized,
			RawOutputPersisted:    view.Result.RawOutputPersisted,
			AutomaticRetryAllowed: view.Result.AutomaticRetryAllowed,
			CreatedAt:             view.Result.CreatedAt.Format(time.RFC3339Nano),
		}
	}
	if view.Receipt != nil {
		receipt := view.Receipt
		result.Receipt = &HostCommandExecutionReceiptView{
			RequestID: receipt.RequestID, Backend: receipt.Backend,
			ExitCode:            receipt.ExitCode,
			StdoutObservedBytes: receipt.StdoutObservedBytes,
			StdoutCapturedBytes: receipt.StdoutCapturedBytes,
			StdoutPrefixSHA256:  receipt.StdoutPrefixSHA256,
			StdoutTruncated:     receipt.StdoutTruncated,
			StderrObservedBytes: receipt.StderrObservedBytes,
			StderrCapturedBytes: receipt.StderrCapturedBytes,
			StderrPrefixSHA256:  receipt.StderrPrefixSHA256,
			StderrTruncated:     receipt.StderrTruncated,
			StartedAt:           receipt.StartedAt.Format(time.RFC3339Nano),
			CompletedAt:         receipt.CompletedAt.Format(time.RFC3339Nano),
			TimedOut:            receipt.TimedOut, Cancelled: receipt.Cancelled,
			OutputLimitExceeded: receipt.OutputLimitExceeded,
			TreeReaped:          receipt.TreeReaped, NonSandboxed: receipt.NonSandboxed,
			RestrictedToken:         receipt.RestrictedToken,
			LowIntegrityToken:       receipt.LowIntegrityToken,
			JobAssignedAtCreation:   receipt.JobAssignedAtCreation,
			KillOnJobClose:          receipt.KillOnJobClose,
			ActiveProcessLimit:      receipt.ActiveProcessLimit,
			JobMemoryLimit:          receipt.JobMemoryLimit,
			StdinClosed:             receipt.StdinClosed,
			EnvironmentInherited:    receipt.EnvironmentInherited,
			NetworkRequested:        receipt.NetworkRequested,
			PersistentProcess:       receipt.PersistentProcess,
			ProductExecutionEnabled: receipt.ProductExecutionEnabled,
		}
	}
	return result
}
