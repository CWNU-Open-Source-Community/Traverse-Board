package app

import (
	"fmt"
	"os"
	"path/filepath"

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
	if a == nil || a.store == nil {
		return nil, fmt.Errorf("Docker Sandbox store is unavailable")
	}
	if err := permissionCapabilities.Validate(); err != nil {
		return nil, err
	}
	readiness := a.dockerReadinessProbe
	if readiness == nil {
		local, err := sandbox.NewLocalDockerReadinessProbe()
		if err != nil {
			return nil, err
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
		return nil, fmt.Errorf("create trusted Docker Sandbox staging root: %w", err)
	}
	service, err := application.NewDockerSandboxService(a.store, readiness, a.checker,
		sandbox.DockerRuntimeCapabilities{Enabled: enabled}, permissionCapabilities,
		application.WithDockerSandboxExecution(lifecycle, ioTransport, stagingRoot,
			sandbox.DefaultDockerContainerLifecycleLeaseTTL))
	if err != nil {
		return nil, err
	}
	a.dockerSandbox = service
	return service, nil
}
