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

func TestDockerContainerLifecycleRealDaemonOptIn(t *testing.T) {
	imageDigest := strings.TrimSpace(os.Getenv(dockerLifecycleIntegrationImageEnv))
	if imageDigest == "" {
		t.Skip("set " + dockerLifecycleIntegrationImageEnv +
			" to a pre-existing environment-free Linux image digest")
	}
	if !ValidOCIImageDigest(imageDigest) {
		t.Fatalf("%s must be an exact sha256 image digest", dockerLifecycleIntegrationImageEnv)
	}

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

func newDockerLifecycleIntegrationWriteRequest(t *testing.T, ctx context.Context,
	imageDigest string,
) DockerContainerWriteRequest {
	t.Helper()
	manifest := dockerContainerCompilerManifest()
	manifest.Command = CommandSpec{Executable: "/lifecycle-fixture",
		Arguments: []string{"ignore-term"}, WorkingDirectory: "/workspace"}
	manifest.Network = NetworkScope{Mode: "disabled"}
	manifest.Environment = nil
	manifest.InputArtifactIDs = nil
	manifest.TimeoutSeconds = 1
	manifest.Cancellation.GracePeriodMillis = 150
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
