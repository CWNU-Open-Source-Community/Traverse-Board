package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/sandbox"
)

const (
	DockerSandboxReadinessPath = "/api/v1/sandbox/docker/readiness"
	DockerSandboxAdmissionPath = "/api/v1/sandbox/docker/admissions"
	DockerSandboxStartPath     = "/api/v1/sandbox/docker/starts"
	DockerSandboxCancelPath    = "/api/v1/sandbox/docker/cancellations"
	DockerSandboxStatusPath    = "/api/v1/sandbox/docker/status"

	MaxDockerSandboxRequestBodyBytes = sandbox.MaxManifestBytes + 8*1024
)

// DockerSandboxController is intentionally identical to the product service
// surface. HTTP cannot construct a plan, Docker request, endpoint, mount, or
// daemon option and therefore cannot bypass the application gates.
type DockerSandboxController interface {
	RuntimeCapabilities() (sandbox.DockerRuntimeCapabilities, string, error)
	Readiness(context.Context, application.DockerSandboxReadinessRequest) (
		sandbox.DockerReadiness, error)
	Admit(context.Context, application.DockerSandboxAdmissionRequest) (
		application.DockerSandboxAdmissionResult, error)
	Get(context.Context, string) (domain.DockerSandboxRecord, error)
	Start(context.Context, application.DockerSandboxStartRequest) (
		application.DockerSandboxStartResult, error)
	Cancel(context.Context, application.DockerSandboxCancelRequest) (
		application.DockerSandboxCancelResult, error)
}

type DockerSandboxReadinessRequestView struct {
	PlanID   string           `json:"plan_id"`
	Manifest sandbox.Manifest `json:"manifest"`
}

type DockerSandboxAdmissionRequestView struct {
	PlanID      string           `json:"plan_id"`
	Manifest    sandbox.Manifest `json:"manifest"`
	RequestedBy string           `json:"requested_by"`
}

type DockerSandboxStartRequestView struct {
	AdmissionID string `json:"admission_id"`
	RequestedBy string `json:"requested_by"`
}

type DockerSandboxCancelRequestView struct {
	AdmissionID string `json:"admission_id"`
	RequestedBy string `json:"requested_by"`
}

type DockerSandboxReadinessView struct {
	ProtocolVersion      string    `json:"protocol_version"`
	Status               string    `json:"status"`
	Ready                bool      `json:"ready"`
	FeatureEnabled       bool      `json:"feature_enabled"`
	ReasonCode           string    `json:"reason_code"`
	RemediationCode      string    `json:"remediation_code"`
	CheckedAt            time.Time `json:"checked_at"`
	ExpiresAt            time.Time `json:"expires_at"`
	EndpointClass        string    `json:"endpoint_class"`
	EndpointFingerprint  string    `json:"endpoint_fingerprint"`
	DaemonReachable      bool      `json:"daemon_reachable"`
	ImageInspected       bool      `json:"image_inspected"`
	ImageProfileSafe     bool      `json:"image_profile_safe"`
	NetworkMode          string    `json:"network_mode,omitempty"`
	ReadinessFingerprint string    `json:"readiness_fingerprint"`
}

type DockerSandboxAdmissionView struct {
	Readiness      DockerSandboxReadinessView `json:"readiness"`
	Allowed        bool                       `json:"allowed"`
	ReasonCode     string                     `json:"reason_code"`
	Remediation    string                     `json:"remediation_code"`
	AdmissionID    string                     `json:"admission_id,omitempty"`
	RunID          string                     `json:"run_id,omitempty"`
	PlanID         string                     `json:"plan_id,omitempty"`
	Decision       string                     `json:"decision,omitempty"`
	PermissionMode string                     `json:"permission_mode,omitempty"`
	CreatedAt      *time.Time                 `json:"created_at,omitempty"`
	Replayed       bool                       `json:"replayed"`
}

type DockerSandboxStatusView struct {
	ProtocolVersion string     `json:"protocol_version"`
	AdmissionID     string     `json:"admission_id"`
	RunID           string     `json:"run_id"`
	PlanID          string     `json:"plan_id"`
	State           string     `json:"state"`
	Decision        string     `json:"decision"`
	ReasonCode      string     `json:"reason_code"`
	RemediationCode string     `json:"remediation_code"`
	AttemptID       string     `json:"attempt_id,omitempty"`
	Outcome         string     `json:"outcome,omitempty"`
	ReceiptReason   string     `json:"receipt_reason_code,omitempty"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	CleanupComplete bool       `json:"cleanup_complete"`
	ArtifactCount   int        `json:"artifact_count"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Replayed        bool       `json:"replayed"`
}

type DockerSandboxCancellationView struct {
	CancellationID string                  `json:"cancellation_id"`
	AdmissionID    string                  `json:"admission_id"`
	ReasonCode     string                  `json:"reason_code"`
	RequestedAt    time.Time               `json:"requested_at"`
	Status         DockerSandboxStatusView `json:"status"`
	Replayed       bool                    `json:"replayed"`
}

type dockerSandboxManifestRequestWire struct {
	PlanID      string          `json:"plan_id"`
	Manifest    json.RawMessage `json:"manifest"`
	RequestedBy string          `json:"requested_by"`
}

type dockerSandboxReadinessRequestWire struct {
	PlanID   string          `json:"plan_id"`
	Manifest json.RawMessage `json:"manifest"`
}

type dockerSandboxIdentityRequestWire struct {
	AdmissionID string `json:"admission_id"`
	RequestedBy string `json:"requested_by"`
}

func isDockerSandboxPath(value string) bool {
	switch value {
	case DockerSandboxReadinessPath, DockerSandboxAdmissionPath,
		DockerSandboxStartPath, DockerSandboxCancelPath, DockerSandboxStatusPath:
		return true
	default:
		return false
	}
}

func (a *API) serveDockerSandbox(writer http.ResponseWriter, request *http.Request,
	requestID string,
) {
	if a.dockerSandboxController == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	switch request.URL.Path {
	case DockerSandboxReadinessPath:
		a.serveDockerSandboxReadiness(writer, request, requestID)
	case DockerSandboxAdmissionPath:
		a.serveDockerSandboxAdmission(writer, request, requestID)
	case DockerSandboxStartPath:
		a.serveDockerSandboxStart(writer, request, requestID)
	case DockerSandboxCancelPath:
		a.serveDockerSandboxCancel(writer, request, requestID)
	case DockerSandboxStatusPath:
		a.serveDockerSandboxStatus(writer, request, requestID)
	default:
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
	}
}

func (a *API) serveDockerSandboxReadiness(writer http.ResponseWriter,
	request *http.Request, requestID string,
) {
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"),
			http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox readiness endpoint only supports POST"), http.StatusMethodNotAllowed)
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
	view, err := decodeDockerSandboxManifestRequest(request, false)
	if err != nil {
		a.writeDockerSandboxRequestError(writer, requestID, err)
		return
	}
	result, err := a.dockerSandboxController.Readiness(request.Context(),
		application.DockerSandboxReadinessRequest{PlanID: view.PlanID, Manifest: view.Manifest})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, dockerSandboxReadinessView(result), nil,
		http.StatusOK)
}

func (a *API) serveDockerSandboxAdmission(writer http.ResponseWriter,
	request *http.Request, requestID string,
) {
	operationKey, ok := a.authorizeDockerSandboxControl(writer, request, requestID)
	if !ok {
		return
	}
	view, err := decodeDockerSandboxManifestRequest(request, true)
	if err != nil {
		a.writeDockerSandboxRequestError(writer, requestID, err)
		return
	}
	result, err := a.dockerSandboxController.Admit(request.Context(),
		application.DockerSandboxAdmissionRequest{
			PlanID: view.PlanID, Manifest: view.Manifest,
			OperationKey: operationKey, RequestedBy: view.RequestedBy,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, dockerSandboxAdmissionView(result), nil,
		http.StatusOK)
}

func (a *API) serveDockerSandboxStart(writer http.ResponseWriter,
	request *http.Request, requestID string,
) {
	operationKey, ok := a.authorizeDockerSandboxControl(writer, request, requestID)
	if !ok {
		return
	}
	view, err := decodeDockerSandboxIdentityRequest(request)
	if err != nil {
		a.writeDockerSandboxRequestError(writer, requestID, err)
		return
	}
	deadline := time.Now().Add(time.Duration(sandbox.MaxTimeoutSeconds)*time.Second + 30*time.Second)
	if err := http.NewResponseController(writer).SetWriteDeadline(deadline); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeUnavailable,
			"Docker Sandbox response deadline could not be configured", err), 0)
		return
	}
	result, err := a.dockerSandboxController.Start(request.Context(),
		application.DockerSandboxStartRequest{AdmissionID: view.AdmissionID,
			OperationKey: operationKey, RequestedBy: view.RequestedBy})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID,
		dockerSandboxStatusView(result.Record, result.Replayed), nil, http.StatusOK)
}

func (a *API) serveDockerSandboxCancel(writer http.ResponseWriter,
	request *http.Request, requestID string,
) {
	operationKey, ok := a.authorizeDockerSandboxControl(writer, request, requestID)
	if !ok {
		return
	}
	view, err := decodeDockerSandboxIdentityRequest(request)
	if err != nil {
		a.writeDockerSandboxRequestError(writer, requestID, err)
		return
	}
	result, err := a.dockerSandboxController.Cancel(request.Context(),
		application.DockerSandboxCancelRequest{AdmissionID: view.AdmissionID,
			OperationKey: operationKey, RequestedBy: view.RequestedBy})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, DockerSandboxCancellationView{
		CancellationID: result.Cancellation.ID,
		AdmissionID:    result.Cancellation.AdmissionID,
		ReasonCode:     result.Cancellation.ReasonCode,
		RequestedAt:    result.Cancellation.RequestedAt,
		Status:         dockerSandboxStatusView(result.Record, result.Replayed),
		Replayed:       result.Replayed,
	}, nil, http.StatusOK)
}

func (a *API) serveDockerSandboxStatus(writer http.ResponseWriter,
	request *http.Request, requestID string,
) {
	if !a.authorized(request, a.tokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(writer, requestID,
			apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"),
			http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox status endpoint only supports GET"), http.StatusMethodNotAllowed)
		return
	}
	if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox status request cannot contain a body"), 0)
		return
	}
	values := request.URL.Query()
	if len(values) != 1 || len(values["admission_id"]) != 1 ||
		strings.TrimSpace(values.Get("admission_id")) == "" {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox status requires exactly one admission_id query parameter"), 0)
		return
	}
	record, err := a.dockerSandboxController.Get(request.Context(),
		values.Get("admission_id"))
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID,
		dockerSandboxStatusView(record, record.Replayed), nil, http.StatusOK)
}

func (a *API) authorizeDockerSandboxControl(writer http.ResponseWriter,
	request *http.Request, requestID string,
) (string, bool) {
	if !a.dockerSandboxControlEnabled {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return "", false
	}
	if !a.authorized(request, a.controlTokenHash) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
			"valid control bearer authorization is required"), http.StatusUnauthorized)
		return "", false
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox control endpoint only supports POST"), http.StatusMethodNotAllowed)
		return "", false
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return "", false
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return "", false
	}
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 || keys[0] == "" {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Docker Sandbox control requires exactly one Idempotency-Key header"), 0)
		return "", false
	}
	return keys[0], true
}

func decodeDockerSandboxManifestRequest(request *http.Request,
	requireRequester bool,
) (DockerSandboxAdmissionRequestView, error) {
	body, err := readBoundedRequestBody(request, MaxDockerSandboxRequestBodyBytes)
	if err != nil {
		return DockerSandboxAdmissionRequestView{}, err
	}
	var wire dockerSandboxManifestRequestWire
	var readinessWire dockerSandboxReadinessRequestWire
	target := any(&wire)
	if !requireRequester {
		target = &readinessWire
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return DockerSandboxAdmissionRequestView{}, apperror.Wrap(
			apperror.CodeInvalidArgument,
			"Docker Sandbox request body must be one strict JSON object", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return DockerSandboxAdmissionRequestView{}, err
	}
	if !requireRequester {
		wire.PlanID = readinessWire.PlanID
		wire.Manifest = readinessWire.Manifest
	}
	manifest, err := sandbox.DecodeManifest(wire.Manifest)
	if err != nil {
		return DockerSandboxAdmissionRequestView{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Docker Sandbox Manifest is invalid", err)
	}
	return DockerSandboxAdmissionRequestView{PlanID: wire.PlanID,
		Manifest: manifest, RequestedBy: wire.RequestedBy}, nil
}

func decodeDockerSandboxIdentityRequest(request *http.Request) (
	dockerSandboxIdentityRequestWire, error,
) {
	body, err := readBoundedRequestBody(request, MaxControlRequestBodyBytes)
	if err != nil {
		return dockerSandboxIdentityRequestWire{}, err
	}
	var view dockerSandboxIdentityRequestWire
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		return dockerSandboxIdentityRequestWire{}, apperror.Wrap(
			apperror.CodeInvalidArgument,
			"Docker Sandbox request body must be one strict JSON object", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return dockerSandboxIdentityRequestWire{}, err
	}
	return view, nil
}

func (a *API) writeDockerSandboxRequestError(writer http.ResponseWriter,
	requestID string, err error,
) {
	status := 0
	if apperror.CodeOf(apperror.Normalize(err)) == apperror.CodeResourceExhausted {
		status = http.StatusRequestEntityTooLarge
	}
	a.writeError(writer, requestID, err, status)
}

func dockerSandboxReadinessView(value sandbox.DockerReadiness) DockerSandboxReadinessView {
	return DockerSandboxReadinessView{
		ProtocolVersion: value.ProtocolVersion, Status: value.Status,
		Ready: value.Ready, FeatureEnabled: value.FeatureEnabled,
		ReasonCode: value.ReasonCode, RemediationCode: value.RemediationCode,
		CheckedAt: value.CheckedAt, ExpiresAt: value.ExpiresAt,
		EndpointClass: value.EndpointClass, EndpointFingerprint: value.EndpointFingerprint,
		DaemonReachable: value.DaemonReachable, ImageInspected: value.ImageInspected,
		ImageProfileSafe: value.ImageProfileSafe,
		NetworkMode:      value.NetworkMode, ReadinessFingerprint: value.ReadinessFingerprint,
	}
}

func dockerSandboxAdmissionView(
	value application.DockerSandboxAdmissionResult,
) DockerSandboxAdmissionView {
	view := DockerSandboxAdmissionView{Readiness: dockerSandboxReadinessView(value.Readiness),
		Allowed: value.Allowed, ReasonCode: value.ReasonCode,
		Remediation: value.RemediationCode, Replayed: value.Replayed}
	if value.Admission != nil {
		createdAt := value.Admission.CreatedAt
		view.AdmissionID = value.Admission.ID
		view.RunID = value.Admission.RunID
		view.PlanID = value.Admission.PlanID
		view.Decision = value.Admission.Decision
		view.PermissionMode = string(value.Admission.PermissionMode)
		view.CreatedAt = &createdAt
	}
	return view
}

func dockerSandboxStatusView(value domain.DockerSandboxRecord,
	replayed bool,
) DockerSandboxStatusView {
	view := DockerSandboxStatusView{
		ProtocolVersion: value.Admission.ProtocolVersion,
		AdmissionID:     value.Admission.ID, RunID: value.Admission.RunID,
		PlanID: value.Admission.PlanID, State: "admitted",
		Decision: value.Admission.Decision, ReasonCode: value.Admission.ReasonCode,
		RemediationCode: value.Admission.RemediationCode,
		CreatedAt:       value.Admission.CreatedAt, Replayed: replayed,
	}
	if value.Launch != nil {
		view.State = "launched"
		view.AttemptID = value.Launch.AttemptID
	}
	if value.Receipt != nil {
		completedAt := value.Receipt.CompletedAt
		view.State = "terminal"
		view.Outcome = value.Receipt.Outcome
		view.ReceiptReason = value.Receipt.ReasonCode
		view.ExitCode = value.Receipt.ExitCode
		view.CleanupComplete = value.Receipt.CleanupComplete
		view.ArtifactCount = value.Receipt.ArtifactCount
		view.CompletedAt = &completedAt
	}
	return view
}
