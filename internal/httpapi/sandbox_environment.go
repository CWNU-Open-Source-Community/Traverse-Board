package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const SandboxEnvironmentPath = "/api/v1/sandbox/environment"
const MaxSandboxEnvironmentRequestBodyBytes = 8 * 1024

type SandboxEnvironmentSettingsView struct {
	DefaultBackend    string `json:"default_backend"`
	DockerEnabled     bool   `json:"docker_enabled"`
	DockerImageDigest string `json:"docker_image_digest"`
	SBXEnabled        bool   `json:"sbx_enabled"`
	SBXTemplate       string `json:"sbx_template"`
}

type SandboxEnvironmentControlRequestView struct {
	Version          string                         `json:"version"`
	ExpectedRevision int64                          `json:"expected_revision"`
	Settings         SandboxEnvironmentSettingsView `json:"settings"`
}

type SandboxEnvironmentBlockerView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SandboxEnvironmentBackendView struct {
	Backend    string                          `json:"backend"`
	Enabled    bool                            `json:"enabled"`
	Installed  bool                            `json:"installed"`
	Configured bool                            `json:"configured"`
	Ready      bool                            `json:"ready"`
	Status     string                          `json:"status"`
	Blockers   []SandboxEnvironmentBlockerView `json:"blockers"`
}

// Preferences and startup gates are separate; this view grants no execution.
type SandboxEnvironmentView struct {
	ProtocolVersion string                          `json:"protocol_version"`
	Revision        int64                           `json:"revision"`
	Settings        SandboxEnvironmentSettingsView  `json:"settings"`
	ActiveSettings  SandboxEnvironmentSettingsView  `json:"active_settings"`
	RestartRequired bool                            `json:"restart_required"`
	ProbeStatus     string                          `json:"probe_status"`
	Backends        []SandboxEnvironmentBackendView `json:"backends"`
	CapabilityGrant bool                            `json:"capability_grant"`
	Replayed        bool                            `json:"replayed"`
}

type SandboxEnvironmentController interface {
	SandboxEnvironment(context.Context) (SandboxEnvironmentView, error)
	SaveSandboxEnvironment(context.Context, SandboxEnvironmentControlRequestView) (SandboxEnvironmentView, error)
}

type sandboxEnvironmentSource interface {
	SandboxEnvironment(context.Context) (application.SandboxEnvironment, error)
	SaveSandboxEnvironment(context.Context, application.SaveSandboxEnvironmentRequest) (application.SandboxEnvironment, error)
}

type sandboxEnvironmentProjection struct{ source sandboxEnvironmentSource }

func NewSandboxEnvironmentController(source sandboxEnvironmentSource) SandboxEnvironmentController {
	return sandboxEnvironmentProjection{source: source}
}

func (projection sandboxEnvironmentProjection) SandboxEnvironment(ctx context.Context) (SandboxEnvironmentView, error) {
	value, err := projection.source.SandboxEnvironment(ctx)
	if err != nil {
		return SandboxEnvironmentView{}, err
	}
	return sandboxEnvironmentView(value), nil
}

func (projection sandboxEnvironmentProjection) SaveSandboxEnvironment(ctx context.Context, request SandboxEnvironmentControlRequestView) (SandboxEnvironmentView, error) {
	value, err := projection.source.SaveSandboxEnvironment(ctx, application.SaveSandboxEnvironmentRequest{
		Version: request.Version, ExpectedRevision: request.ExpectedRevision,
		Settings: application.SandboxEnvironmentSettings(request.Settings),
	})
	if err != nil {
		return SandboxEnvironmentView{}, err
	}
	return sandboxEnvironmentView(value), nil
}

func sandboxEnvironmentView(value application.SandboxEnvironment) SandboxEnvironmentView {
	view := SandboxEnvironmentView{ProtocolVersion: value.ProtocolVersion, Revision: value.Revision,
		Settings: SandboxEnvironmentSettingsView(value.Settings), ActiveSettings: SandboxEnvironmentSettingsView(value.ActiveSettings),
		RestartRequired: value.RestartRequired, ProbeStatus: value.ProbeStatus, CapabilityGrant: false, Replayed: value.Replayed,
		Backends: make([]SandboxEnvironmentBackendView, len(value.Backends))}
	for index, backend := range value.Backends {
		row := SandboxEnvironmentBackendView{Backend: backend.Backend, Enabled: backend.Enabled, Installed: backend.Installed,
			Configured: backend.Configured, Ready: backend.Ready, Status: backend.Status,
			Blockers: make([]SandboxEnvironmentBlockerView, len(backend.Blockers))}
		for index, blocker := range backend.Blockers {
			row.Blockers[index] = SandboxEnvironmentBlockerView{Code: blocker.Code, Message: blocker.Message}
		}
		view.Backends[index] = row
	}
	return view
}

func (a *API) serveSandboxEnvironment(writer http.ResponseWriter, request *http.Request, requestID string) {
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodPut {
		if !a.sandboxEnvironmentControlEnabled {
			a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "sandbox environment settings are read-only on this connection"), http.StatusNotFound)
			return
		}
		if !a.authorized(request, a.controlTokenHash) {
			a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied, "valid control bearer authorization is required"), http.StatusUnauthorized)
			return
		}
	} else if !a.authorized(request, a.tokenHash) {
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		writer.Header().Set("Allow", "GET, PUT")
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "sandbox environment supports GET and PUT"), http.StatusMethodNotAllowed)
		return
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "sandbox environment endpoint does not accept query parameters"), 0)
		return
	}
	if a.sandboxEnvironmentController == nil {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "sandbox environment is unavailable on this connection"), http.StatusNotFound)
		return
	}
	var view SandboxEnvironmentView
	var err error
	if request.Method == http.MethodGet {
		var body []byte
		var readErr error
		if request.Body != nil {
			body, readErr = io.ReadAll(io.LimitReader(request.Body, 1))
		}
		if readErr != nil || len(body) != 0 || request.ContentLength > 0 {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "sandbox environment GET must have an empty body"), http.StatusBadRequest)
			return
		}
		view, err = a.sandboxEnvironmentController.SandboxEnvironment(request.Context())
	} else {
		if err := validateJSONContentType(request.Header); err != nil {
			a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
			return
		}
		body, readErr := readBoundedRequestBody(request, MaxSandboxEnvironmentRequestBodyBytes)
		if readErr != nil {
			status := 0
			if apperror.CodeOf(apperror.Normalize(readErr)) == apperror.CodeResourceExhausted {
				status = http.StatusRequestEntityTooLarge
			}
			a.writeError(writer, requestID, readErr, status)
			return
		}
		control, decodeErr := decodeSandboxEnvironmentControl(body)
		if decodeErr != nil {
			a.writeError(writer, requestID, decodeErr, 0)
			return
		}
		view, err = a.sandboxEnvironmentController.SaveSandboxEnvironment(request.Context(), control)
	}
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, view, nil)
}

func decodeSandboxEnvironmentControl(body []byte) (SandboxEnvironmentControlRequestView, error) {
	invalid := apperror.New(apperror.CodeInvalidArgument, "sandbox environment settings require one complete JSON object with exact field names and types")
	if !utf8.Valid(body) || rejectDuplicateJSONObjectFields(body, "sandbox environment") != nil {
		return SandboxEnvironmentControlRequestView{}, invalid
	}
	fields, valid := sandboxEnvironmentRequiredFields(body, "version", "expected_revision", "settings")
	if !valid {
		return SandboxEnvironmentControlRequestView{}, invalid
	}
	if _, valid := sandboxEnvironmentRequiredFields(fields["settings"], "default_backend", "docker_enabled", "docker_image_digest", "sbx_enabled", "sbx_template"); !valid {
		return SandboxEnvironmentControlRequestView{}, invalid
	}
	var view SandboxEnvironmentControlRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&view) != nil || decoder.Decode(new(any)) != io.EOF || view.Version != application.SandboxEnvironmentVersion || view.ExpectedRevision < 1 {
		return SandboxEnvironmentControlRequestView{}, invalid
	}
	if err := application.SandboxEnvironmentSettings(view.Settings).Validate(); err != nil {
		return SandboxEnvironmentControlRequestView{}, err
	}
	return view, nil
}

func sandboxEnvironmentRequiredFields(raw []byte, names ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != len(names) {
		return nil, false
	}
	for _, name := range names {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, false
		}
	}
	return fields, true
}
