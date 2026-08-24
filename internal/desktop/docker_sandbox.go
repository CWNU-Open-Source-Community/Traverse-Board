package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/store"
)

const desktopDockerSandboxStagingDirectory = "docker-sandbox-staging"

// newDesktopDockerSandboxService is the Desktop process composition root for
// Docker product admission. The daemon endpoint and staging root are fixed Go
// configuration. Cleanup transports remain installed while start authority is
// disabled so a restarted process can converge durable launched work without
// recreating the previous process' grant.
func newDesktopDockerSandboxService(ctx context.Context, stateStore *store.SQLiteStore,
	home string, enabled bool,
	permissionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
	drydocks *application.DrydockService, imageDigest string,
) (*application.DockerSandboxService, *application.StandardCodeDockerService, error) {
	if ctx == nil || stateStore == nil || permissionCapabilities.Validate() != nil {
		return nil, nil, errors.New("Desktop Docker Sandbox dependencies are invalid")
	}
	readiness, err := sandbox.NewLocalDockerReadinessProbe()
	if err != nil {
		return nil, nil, err
	}
	stagingRoot := filepath.Join(home, desktopDockerSandboxStagingDirectory)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return nil, nil, errors.New("Desktop Docker Sandbox staging root is unavailable")
	}
	options := []application.DockerSandboxServiceOption{
		application.WithDockerSandboxExecution(
			sandbox.NewLocalDockerContainerLifecycleTransport(),
			sandbox.NewLocalDockerContainerIOTransport(), stagingRoot,
			sandbox.DefaultDockerContainerLifecycleLeaseTTL),
	}
	if enabled && permissionCapabilities.WorkspaceSandboxEnabled && drydocks != nil &&
		sandbox.ValidOCIImageDigest(imageDigest) {
		options = append(options,
			application.WithDockerStandardCode(drydocks, imageDigest))
	} else {
		imageDigest = ""
	}
	service, err := application.NewDockerSandboxService(stateStore, readiness,
		policy.NewDefaultChecker(), sandbox.DockerRuntimeCapabilities{Enabled: enabled},
		permissionCapabilities, options...)
	if err != nil {
		return nil, nil, err
	}
	if _, err := service.RecoverStartup(ctx); err != nil {
		return nil, nil, err
	}
	if imageDigest == "" {
		return service, nil, nil
	}
	manifests := application.NewSandboxManifestService(stateStore,
		policy.NewDefaultChecker())
	standard, err := application.NewStandardCodeDockerService(stateStore, drydocks,
		manifests, service, imageDigest)
	if err != nil {
		return nil, nil, err
	}
	return service, standard, nil
}

// DockerExecutionEnabled reports the capability held by the live process,
// rather than reconstructing it from SQLite or a renderer-provided value.
func (c *ControlPlane) DockerExecutionEnabled() (bool, error) {
	if c == nil || c.dockerSandbox == nil {
		return false, errors.New("Desktop Docker Sandbox service is unavailable")
	}
	capabilities, _, err := c.dockerSandbox.RuntimeCapabilities()
	if err != nil {
		return false, err
	}
	return capabilities.Enabled, nil
}
