package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/standardcode"
)

type DockerEnvironment struct {
	FeatureEnabled  bool
	ImageConfigured bool
	ImageDigest     string
	RestartRequired bool
	Readiness       sandbox.DockerReadiness
}

type DockerEnvironmentService struct {
	enabled          bool
	imageDigest      string
	adapterInstalled bool
}

func NewDockerEnvironmentService(enabled bool, imageDigest string, adapterInstalled bool) *DockerEnvironmentService {
	return &DockerEnvironmentService{enabled: enabled, imageDigest: strings.TrimSpace(imageDigest), adapterInstalled: adapterInstalled}
}
func (service *DockerEnvironmentService) DockerEnvironment(ctx context.Context) (DockerEnvironment, error) {
	view := DockerEnvironment{FeatureEnabled: service.enabled, ImageConfigured: sandbox.ValidOCIImageDigest(service.imageDigest), RestartRequired: !service.adapterInstalled}
	if view.ImageConfigured {
		view.ImageDigest = service.imageDigest
	}
	readiness, err := ProbeStandardCodeDockerReadiness(ctx, service.enabled, service.imageDigest)
	view.Readiness = readiness
	return view, err
}

// ProbeStandardCodeDockerReadiness uses the fixed local daemon and process-owned
// image only. It never pulls an image, creates a container or grants execution.
func ProbeStandardCodeDockerReadiness(ctx context.Context, enabled bool, imageDigest string) (sandbox.DockerReadiness, error) {
	probe, err := sandbox.NewLocalDockerReadinessProbe()
	if err != nil {
		return sandbox.DockerReadiness{}, err
	}
	return ProbeStandardCodeDockerReadinessWithProbe(ctx, probe, enabled, imageDigest)
}
func ProbeStandardCodeDockerReadinessWithProbe(ctx context.Context, probe sandbox.ReadinessProbe, enabled bool, imageDigest string) (sandbox.DockerReadiness, error) {
	if ctx == nil || probe == nil {
		return sandbox.DockerReadiness{}, errors.New("Docker readiness dependencies are required")
	}
	manifest, err := StandardCodeDockerProbeManifest()
	if err != nil {
		return sandbox.DockerReadiness{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return probe.Check(bounded, sandbox.DockerRuntimeCapabilities{Enabled: enabled}, manifest, imageDigest)
}
func StandardCodeDockerProbeManifest() (sandbox.Manifest, error) {
	return standardcode.CompileDockerManifest(standardcode.ExecutionContext{
		RunID: "readiness-run", MissionID: "readiness-mission", SessionID: "readiness-session",
		WorkspaceID: "readiness-workspace", DrydockID: "readiness-drydock",
		DrydockWorkspaceID: "readiness-drydock-workspace", DrydockGeneration: 1,
		CheckpointID: "readiness-checkpoint", DrydockBindingSHA256: strings.Repeat("a", 64),
		ProfileSnapshotID: "readiness-profile", ProfileRevision: 1,
		PermissionSnapshotID: "readiness-permission", PermissionRevision: 1,
		CapabilityGeneration: strings.Repeat("b", 64),
	}, standardcode.Command{ProtocolVersion: standardcode.CommandProtocolVersion,
		Toolchain: sandbox.DockerStandardCodeToolchainGo, Arguments: []string{"version"},
		WorkingDirectory: ".", TimeoutSeconds: 30, Purpose: "readiness probe"})
}
