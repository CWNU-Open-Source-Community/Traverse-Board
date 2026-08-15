package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dockerLifecycleIntegrationImageEnv = "CYBERAGENT_DOCKER_LIFECYCLE_TEST_IMAGE_DIGEST"

// Build the environment-free scratch fixture with:
//
//	powershell -ExecutionPolicy Bypass -File testdata/docker-lifecycle-fixture/build-fixture.ps1
//
// and export the printed digest above. The image must be environment-free,
// secret-free, non-root, and referenced by an exact sha256 digest; the script
// strips the builder-injected PATH and reloads the image so Docker Desktop
// re-synthesizes the tag RepoDigest.

func TestDockerContainerLifecycleRealDaemonOptIn(t *testing.T) {
	imageDigest := dockerLifecycleIntegrationImageDigest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	writeRequest := newDockerLifecycleIntegrationWriteRequest(t, ctx, imageDigest)
	local, ok := newLocalDockerContainerLifecycleTransport().(localDockerContainerLifecycleTransport)
	if !ok {
		t.Fatalf("fixed local Docker lifecycle transport is unavailable: %T", local)
	}
	stage, err := local.Stage(ctx, writeRequest)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewDockerContainerLifecycleRequest(
		"docker-lifecycle-real-daemon-attempt", 1, writeRequest, stage,
		DockerContainerLifecycleConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		counters := &dockerLifecycleCounters{}
		inspection, found, inspectErr := local.inner.inspect(cleanupCtx,
			writeRequest.Spec.ContainerName, counters)
		if inspectErr != nil {
			t.Errorf("inspect exact lifecycle fixture during cleanup: %v", inspectErr)
			return
		}
		if !found {
			return
		}
		if _, _, _, _, cleanupErr := local.inner.terminateRemoveAndConfirm(
			request, inspection.ID, counters); cleanupErr != nil {
			t.Errorf("remove exact lifecycle fixture during cleanup: %v", cleanupErr)
		}
	})
	result, err := local.Run(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Validate() != nil || result.Status != DockerContainerLifecycleStatusTimedOut ||
		!result.TimeoutObserved || !result.GracefulSignalSent || !result.ForcedSignalSent ||
		result.ExitCode != 137 || !result.ContainerRemoved || !result.TargetAbsenceConfirmed ||
		result.ProductEntryEnabled || result.ProductExecutionAuthorized ||
		result.ArtifactCommitAuthorized {
		t.Fatalf("real Docker lifecycle evidence is invalid: %#v", result)
	}
	if _, found, inspectErr := local.inner.inspect(ctx, writeRequest.Spec.ContainerName,
		&dockerLifecycleCounters{}); inspectErr != nil || found {
		t.Fatalf("real Docker lifecycle left its exact container: found=%t err=%v", found, inspectErr)
	}
}

func TestDockerContainerLifecycleNetworkDeniedRealDaemonOptIn(t *testing.T) {
	imageDigest := dockerLifecycleIntegrationImageDigest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	writeRequest := newDockerLifecycleIntegrationWriteRequestForCommand(t, ctx,
		imageDigest, []string{"network-denied"}, 10, 150)
	local, ok := newLocalDockerContainerLifecycleTransport().(localDockerContainerLifecycleTransport)
	if !ok {
		t.Fatalf("fixed local Docker lifecycle transport is unavailable: %T", local)
	}
	stage, err := local.Stage(ctx, writeRequest)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewDockerContainerLifecycleRequest(
		"docker-lifecycle-network-denied-attempt", 1, writeRequest, stage,
		DockerContainerLifecycleConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupDockerLifecycleIntegrationContainer(t, local, request, writeRequest)
	})
	result, err := local.Run(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Validate() != nil || result.Status != DockerContainerLifecycleStatusExited ||
		result.ExitCode != 0 || result.TimeoutObserved || result.CancellationObserved ||
		result.GracefulSignalSent || result.ForcedSignalSent ||
		!result.ContainerRemoved || !result.TargetAbsenceConfirmed {
		t.Fatalf("real Docker network-none evidence is invalid: %#v", result)
	}
}

func TestDockerReadinessRealDaemonOptIn(t *testing.T) {
	imageDigest := dockerLifecycleIntegrationImageDigest(t)
	probe, err := NewLocalDockerReadinessProbe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manifest := dockerContainerCompilerManifest()
	manifest.Command = CommandSpec{Executable: "/lifecycle-fixture",
		Arguments: []string{"network-denied"}, WorkingDirectory: "/workspace"}
	manifest.Network = NetworkScope{Mode: "disabled"}
	manifest.Environment = nil
	manifest.InputArtifactIDs = nil
	readiness, err := probe.Check(ctx, DockerRuntimeCapabilities{Enabled: true},
		manifest, imageDigest)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Validate() != nil || !readiness.Ready ||
		!readiness.DaemonReachable || !readiness.ImageInspected ||
		!readiness.ImageProfileSafe || readiness.NetworkMode != "disabled" {
		t.Fatalf("real Docker readiness is not usable: %#v", readiness)
	}
}

func dockerLifecycleIntegrationImageDigest(t *testing.T) string {
	t.Helper()
	imageDigest := strings.TrimSpace(os.Getenv(dockerLifecycleIntegrationImageEnv))
	if imageDigest == "" {
		t.Skip("set " + dockerLifecycleIntegrationImageEnv +
			" to a pre-existing environment-free Linux image digest")
	}
	if !ValidOCIImageDigest(imageDigest) {
		t.Fatalf("%s must be an exact sha256 image digest", dockerLifecycleIntegrationImageEnv)
	}
	return imageDigest
}

func cleanupDockerLifecycleIntegrationContainer(t *testing.T,
	local localDockerContainerLifecycleTransport, request DockerContainerLifecycleRequest,
	writeRequest DockerContainerWriteRequest,
) {
	t.Helper()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cleanupCancel()
	counters := &dockerLifecycleCounters{}
	inspection, found, inspectErr := local.inner.inspect(cleanupCtx,
		writeRequest.Spec.ContainerName, counters)
	if inspectErr != nil {
		t.Errorf("inspect exact lifecycle fixture during cleanup: %v", inspectErr)
		return
	}
	if !found {
		return
	}
	if _, _, _, _, cleanupErr := local.inner.terminateRemoveAndConfirm(
		request, inspection.ID, counters); cleanupErr != nil {
		t.Errorf("remove exact lifecycle fixture during cleanup: %v", cleanupErr)
	}
}

func newDockerLifecycleIntegrationWriteRequest(t *testing.T, ctx context.Context,
	imageDigest string,
) DockerContainerWriteRequest {
	return newDockerLifecycleIntegrationWriteRequestForCommand(t, ctx, imageDigest,
		[]string{"ignore-term"}, 1, 150)
}

func newDockerLifecycleIntegrationWriteRequestForCommand(t *testing.T,
	ctx context.Context, imageDigest string, arguments []string,
	timeoutSeconds, gracePeriodMillis int,
) DockerContainerWriteRequest {
	t.Helper()
	manifest := dockerContainerCompilerManifest()
	manifest.Command = CommandSpec{Executable: "/lifecycle-fixture",
		Arguments: append([]string(nil), arguments...), WorkingDirectory: "/workspace"}
	manifest.Network = NetworkScope{Mode: "disabled"}
	manifest.Environment = nil
	manifest.InputArtifactIDs = nil
	manifest.TimeoutSeconds = timeoutSeconds
	manifest.Cancellation.GracePeriodMillis = gracePeriodMillis
	normalized, err := NormalizeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestFingerprint, err := normalized.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	outputPlan, err := NewOutputExportPlan(normalized)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	observation := DockerObservation{
		ID:                       "docker-lifecycle-observation-integration",
		EvidenceID:               "docker-lifecycle-evidence-integration",
		OutputSimulationID:       "docker-lifecycle-output-integration",
		PreflightID:              "docker-lifecycle-preflight-integration",
		ExecutionID:              "docker-lifecycle-execution-integration",
		CandidateID:              "docker-lifecycle-candidate-integration",
		PreparationID:            "docker-lifecycle-preparation-integration",
		RunID:                    "docker-lifecycle-run-integration",
		MissionID:                "docker-lifecycle-mission-integration",
		WorkspaceID:              "docker-lifecycle-workspace-integration",
		ManifestFingerprint:      manifestFingerprint,
		AuthorizationFingerprint: digest,
		PolicyFingerprint:        digest,
		MountBindingFingerprint:  digest,
		InputArtifactDigest:      digest,
		ThreatModelFingerprint:   digest,
		OutputPlanFingerprint:    outputPlan.Fingerprint,
		Report:                   DockerObservationReport{ImageDigest: imageDigest},
		RequestedBy:              "integration_operator", CreatedAt: time.Now().UTC(),
	}
	observer := NewReadOnlyDockerProductionObserver(dockerContainerCompilerTransport{
		imageDigest: imageDigest, pids: true, ncpu: 8, memory: 8 * 1024 * 1024 * 1024,
	})
	report, err := observer.Observe(ctx, DockerObservationProbeRequest{
		BindingFingerprint: DockerObservationBindingFingerprint(observation),
		ImageDigest:        imageDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation.Report = report
	spec, err := CompileDockerContainerSpec(ctx, observation, normalized)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"output", "src"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request, err := NewDockerContainerWriteRequest(ctx, root, spec)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
