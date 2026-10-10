package httpapi

import (
	"context"
	"net/http"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const DockerEnvironmentPath = "/api/v1/sandbox/docker/environment"

// DockerEnvironmentView reports fixed process configuration and fresh read-only
// observations. It grants no execution, image pull, or daemon configuration.
type DockerEnvironmentView struct {
	ProtocolVersion string                      `json:"protocol_version"`
	FeatureEnabled  bool                        `json:"feature_enabled"`
	ImageConfigured bool                        `json:"image_configured"`
	ImageDigest     string                      `json:"image_digest,omitempty"`
	RestartRequired bool                        `json:"restart_required"`
	Readiness       *DockerSandboxReadinessView `json:"readiness,omitempty"`
}

type DockerEnvironmentController interface {
	DockerEnvironment(context.Context) (DockerEnvironmentView, error)
}

type dockerEnvironmentSource interface {
	DockerEnvironment(context.Context) (application.DockerEnvironment, error)
}
type dockerEnvironmentProjection struct{ source dockerEnvironmentSource }

func NewDockerEnvironmentController(source dockerEnvironmentSource) DockerEnvironmentController {
	return dockerEnvironmentProjection{source: source}
}
func (projection dockerEnvironmentProjection) DockerEnvironment(ctx context.Context) (DockerEnvironmentView, error) {
	value, err := projection.source.DockerEnvironment(ctx)
	if err != nil {
		return DockerEnvironmentView{}, err
	}
	readiness := dockerSandboxReadinessView(value.Readiness)
	return DockerEnvironmentView{ProtocolVersion: "docker_environment.v1", FeatureEnabled: value.FeatureEnabled,
		ImageConfigured: value.ImageConfigured, ImageDigest: value.ImageDigest, RestartRequired: value.RestartRequired, Readiness: &readiness}, nil
}

func (a *API) serveDockerEnvironment(writer http.ResponseWriter, request *http.Request, requestID string) {
	if !a.authorized(request, a.tokenHash) {
		a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "Docker environment only supports GET"), http.StatusMethodNotAllowed)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if a.dockerEnvironmentController == nil {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "Docker environment is unavailable on this connection"), http.StatusNotFound)
		return
	}
	view, err := a.dockerEnvironmentController.DockerEnvironment(request.Context())
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	a.writeSuccess(writer, requestID, view, nil)
}
