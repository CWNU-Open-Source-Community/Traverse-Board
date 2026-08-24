package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/sandbox"
)

const dockerSandboxStagingDirectory = "docker-sandbox-staging"

// newDockerSandboxService is the single App-owned composition root for the
// Docker product. Runtime callers cannot replace the fixed local endpoint or
// choose a staging root. Execution-disabled callers still receive the cleanup
// transports, so restart recovery can converge without restoring start power.
func (a *App) newDockerSandboxService(enabled bool,
	permissionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
) (*application.DockerSandboxService, error) {
	service, _, err := a.newDockerSandboxServiceWithStandardCode(enabled,
		permissionCapabilities, nil)
	return service, err
}

// newDockerSandboxServiceWithStandardCode adds the fixed-image Standard Code
// bridge only when the process already owns a Drydock service and the exact
// image digest is configured. A missing digest means that the Docker Command
// Runtime adapter is not installed; ordinary Docker proposal recovery remains
// available.
func (a *App) newDockerSandboxServiceWithStandardCode(enabled bool,
	permissionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
	drydocks *application.DrydockService,
) (*application.DockerSandboxService, *application.StandardCodeDockerService, error) {
	if a == nil || a.store == nil {
		return nil, nil, fmt.Errorf("Docker Sandbox store is unavailable")
	}
	if err := permissionCapabilities.Validate(); err != nil {
		return nil, nil, err
	}
	readiness := a.dockerReadinessProbe
	if readiness == nil {
		local, err := sandbox.NewLocalDockerReadinessProbe()
		if err != nil {
			return nil, nil, err
		}
		readiness = local
	}
	lifecycle := a.dockerLifecycle
	if lifecycle == nil {
		lifecycle = sandbox.NewLocalDockerContainerLifecycleTransport()
	}
	ioTransport := a.dockerIO
	if ioTransport == nil {
		ioTransport = sandbox.NewLocalDockerContainerIOTransport()
	}
	stagingRoot := filepath.Join(a.home, dockerSandboxStagingDirectory)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create trusted Docker Sandbox staging root: %w", err)
	}
	options := []application.DockerSandboxServiceOption{
		application.WithDockerSandboxExecution(lifecycle, ioTransport, stagingRoot,
			sandbox.DefaultDockerContainerLifecycleLeaseTTL),
	}
	imageDigest := strings.TrimSpace(os.Getenv(standardCodeDockerImageEnvironment))
	if enabled && permissionCapabilities.WorkspaceSandboxEnabled && drydocks != nil &&
		sandbox.ValidOCIImageDigest(imageDigest) {
		options = append(options,
			application.WithDockerStandardCode(drydocks, imageDigest))
	} else {
		imageDigest = ""
	}
	service, err := application.NewDockerSandboxService(a.store, readiness, a.checker,
		sandbox.DockerRuntimeCapabilities{Enabled: enabled}, permissionCapabilities,
		options...)
	if err != nil {
		return nil, nil, err
	}
	a.dockerSandbox = service
	if imageDigest == "" {
		return service, nil, nil
	}
	manifests := application.NewSandboxManifestService(a.store, a.checker)
	if a.dockerObserver != nil {
		manifests.WithDockerProductionObserver(a.dockerObserver)
	}
	standard, err := application.NewStandardCodeDockerService(a.store, drydocks,
		manifests, service, imageDigest)
	if err != nil {
		return nil, nil, err
	}
	return service, standard, nil
}
