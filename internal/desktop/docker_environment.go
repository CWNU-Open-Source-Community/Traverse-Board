package desktop

import (
	"context"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/sandbox"
)

// WorkspaceSandboxRuntimeAvailable keeps independent backend availability
// separate. Local input comes from the validated platform backend; Docker must
// additionally carry a current fixed-image proof and explicit Docker opt-in.
func WorkspaceSandboxRuntimeAvailable(requested, dockerEnabled bool, local *sandbox.LocalReadiness, docker *sandbox.DockerReadiness) bool {
	if !requested {
		return false
	}
	if local != nil && local.Ready {
		return true
	}
	return dockerEnabled && docker != nil && docker.Validate() == nil && docker.FeatureEnabled && docker.ReadyAt(time.Now().UTC())
}

// ProbeStandardCodeDockerReadiness shares the fixed read-only application probe.
func ProbeStandardCodeDockerReadiness(ctx context.Context, enabled bool, imageDigest string) (sandbox.DockerReadiness, error) {
	return application.ProbeStandardCodeDockerReadiness(ctx, enabled, imageDigest)
}
func probeStandardCodeDockerReadiness(ctx context.Context, probe sandbox.DockerReadinessProbe, enabled bool, imageDigest string) (sandbox.DockerReadiness, error) {
	return application.ProbeStandardCodeDockerReadinessWithProbe(ctx, probe, enabled, imageDigest)
}
func standardCodeDockerProbeManifest() (sandbox.Manifest, error) {
	return application.StandardCodeDockerProbeManifest()
}
