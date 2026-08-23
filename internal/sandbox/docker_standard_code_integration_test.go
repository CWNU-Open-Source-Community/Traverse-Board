package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dockerStandardCodeIntegrationImageEnv = "CYBERAGENT_STANDARD_CODE_DOCKER_TEST_IMAGE_DIGEST"

// Build the fixed image with testdata/standard-code-docker/build-fixture.ps1.
// The opt-in test uses only the fixed local Engine transport and the same
// compiler/lifecycle request types as the product path.
func TestDockerStandardCodeFourToolchainsRealDaemonOptIn(t *testing.T) {
	imageDigest := strings.TrimSpace(os.Getenv(dockerStandardCodeIntegrationImageEnv))
	if imageDigest == "" {
		t.Skip("set " + dockerStandardCodeIntegrationImageEnv +
			" to the pre-existing environment/volume-free Standard Code image digest")
	}
	if !ValidOCIImageDigest(imageDigest) {
		t.Fatalf("%s must be an exact OCI sha256 digest",
			dockerStandardCodeIntegrationImageEnv)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata",
		"standard-code-docker", "fixtures"))); err != nil {
		t.Fatal(err)
	}
	hostGitdir := filepath.Join(t.TempDir(), ".git", "worktrees", "drydock")
	if err := os.WriteFile(filepath.Join(root, ".git"),
		[]byte("gitdir: "+hostGitdir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := NewLocalDockerReadinessProbe()
	if err != nil {
		t.Fatal(err)
	}
	local, ok := newLocalDockerContainerLifecycleTransport().(localDockerContainerLifecycleTransport)
	if !ok {
		t.Fatalf("fixed local Docker lifecycle transport is unavailable: %T", local)
	}
	tests := []struct {
		toolchain string
		cwd       string
		arguments []string
		output    string
	}{
		{DockerStandardCodeToolchainGo, "go", []string{"test", "./..."},
			"go/go-output.txt"},
		{DockerStandardCodeToolchainNode, "node", []string{"fixture.js"},
			"node/node-output.txt"},
		{DockerStandardCodeToolchainPython, "python", []string{"fixture.py"},
			"python/python-output.txt"},
		{DockerStandardCodeToolchainRust, "rust", []string{"run", "--offline"},
			"rust/rust-output.txt"},
	}
	for _, test := range tests {
		t.Run(test.toolchain, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			binding := dockerStandardCodeBindingFixture(test.toolchain)
			binding.WorkingDirectory = test.cwd
			binding.Arguments = test.arguments
			binding.TimeoutSeconds = 75
			manifest, err := DockerStandardCodeManifest(binding)
			if err != nil {
				t.Fatal(err)
			}
			readiness, err := probe.Check(ctx, DockerRuntimeCapabilities{Enabled: true},
				manifest, imageDigest)
			if err != nil || !readiness.Ready || readiness.Validate() != nil ||
				!readiness.ImageProfileSafe || readiness.NetworkMode != "disabled" {
				t.Fatalf("fixed image readiness=%#v err=%v", readiness, err)
			}
			writeRequest := standardCodeIntegrationWriteRequest(t, ctx, root,
				imageDigest, manifest, test.toolchain)
			stage, err := local.Stage(ctx, writeRequest)
			if err != nil {
				t.Fatal(err)
			}
			request, err := NewDockerContainerLifecycleRequest(
				"standard-code-"+test.toolchain+"-attempt", 1, writeRequest, stage,
				DockerContainerLifecycleConfirmation)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupDockerLifecycleIntegrationContainer(t, local, request, writeRequest)
			})
			result, err := local.Run(ctx, request)
			if err != nil || result.Validate() != nil ||
				result.Status != DockerContainerLifecycleStatusExited || result.ExitCode != 0 ||
				!result.ContainerRemoved || !result.TargetAbsenceConfirmed {
				t.Fatalf("%s fixed fixture result=%#v err=%v", test.toolchain, result, err)
			}
			if content, err := os.ReadFile(filepath.Join(root,
				filepath.FromSlash(test.output))); err != nil ||
				!strings.Contains(string(content), "offline fixture") {
				t.Fatalf("%s Workspace output=%q err=%v", test.toolchain, content, err)
			}
		})
	}
	for _, test := range []struct {
		name       string
		argument   string
		file       string
		maxBytes   int64
		maxEntries int
	}{
		{name: "single-file-limit", argument: "resource_file.js",
			file: "node/resource-limit.bin", maxBytes: DockerStandardCodeWorkspaceFileBytes},
		{name: "entry-growth-limit", argument: "resource_entries.js",
			maxEntries: DockerStandardCodeWorkspaceGrowthEntries + 2_048},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			binding := dockerStandardCodeBindingFixture(DockerStandardCodeToolchainNode)
			binding.WorkingDirectory = "node"
			binding.Arguments = []string{test.argument}
			binding.TimeoutSeconds = 30
			manifest, err := DockerStandardCodeManifest(binding)
			if err != nil {
				t.Fatal(err)
			}
			writeRequest := standardCodeIntegrationWriteRequest(t, ctx, root,
				imageDigest, manifest, test.name)
			stage, err := local.Stage(ctx, writeRequest)
			if err != nil {
				t.Fatal(err)
			}
			request, err := NewDockerContainerLifecycleRequest(
				"standard-code-"+test.name+"-attempt", 1, writeRequest, stage,
				DockerContainerLifecycleConfirmation)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupDockerLifecycleIntegrationContainer(t, local, request, writeRequest)
			})
			result, runErr := local.Run(ctx, request)
			if runErr != nil || result.Validate() != nil || result.ExitCode == 0 ||
				!result.ContainerRemoved || !result.TargetAbsenceConfirmed {
				t.Fatalf("resource fixture result=%#v err=%v", result, runErr)
			}
			if test.file != "" {
				info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(test.file)))
				if statErr == nil && info.Size() > test.maxBytes {
					t.Fatalf("single file exceeded fixed bound: %d", info.Size())
				}
				if statErr != nil && !os.IsNotExist(statErr) {
					t.Fatal(statErr)
				}
			}
			if test.maxEntries > 0 {
				matches, globErr := filepath.Glob(filepath.Join(root, "node",
					"resource-entry-*"))
				if globErr != nil || len(matches) > test.maxEntries {
					t.Fatalf("entry growth was not bounded: count=%d err=%v",
						len(matches), globErr)
				}
			}
		})
	}
}

func standardCodeIntegrationWriteRequest(t *testing.T, ctx context.Context,
	root, imageDigest string, manifest Manifest, suffix string,
) DockerContainerWriteRequest {
	t.Helper()
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
	observation := DockerObservation{ID: "standard-code-observation-" + suffix,
		EvidenceID:          "standard-code-evidence-" + suffix,
		OutputSimulationID:  "standard-code-output-" + suffix,
		PreflightID:         "standard-code-preflight-" + suffix,
		ExecutionID:         "standard-code-execution-" + suffix,
		CandidateID:         "standard-code-candidate-" + suffix,
		PreparationID:       "standard-code-preparation-" + suffix,
		RunID:               "standard-code-run-" + suffix,
		MissionID:           "standard-code-mission-" + suffix,
		WorkspaceID:         "standard-code-workspace-" + suffix,
		ManifestFingerprint: manifestFingerprint, AuthorizationFingerprint: digest,
		PolicyFingerprint: digest, MountBindingFingerprint: digest,
		InputArtifactDigest: digest, ThreatModelFingerprint: digest,
		OutputPlanFingerprint: outputPlan.Fingerprint,
		Report:                DockerObservationReport{ImageDigest: imageDigest},
		RequestedBy:           "standard_code_operator", CreatedAt: time.Now().UTC()}
	observer := NewReadOnlyDockerProductionObserver(dockerContainerCompilerTransport{
		imageDigest: imageDigest, pids: true, ncpu: 8,
		memory: 8 * 1024 * 1024 * 1024})
	report, err := observer.Observe(ctx, DockerObservationProbeRequest{
		BindingFingerprint: DockerObservationBindingFingerprint(observation),
		ImageDigest:        imageDigest})
	if err != nil {
		t.Fatal(err)
	}
	observation.Report = report
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	spec, err := CompileDockerContainerSpec(ctx, observation, normalized)
	if err != nil {
		t.Fatal(err)
	}
	mask := filepath.Join(t.TempDir(), "git-metadata-mask")
	if err := os.WriteFile(mask, []byte(DockerStandardCodeGitMetadataMask), 0o444); err != nil {
		t.Fatal(err)
	}
	request, err := NewDockerStandardCodeContainerWriteRequest(ctx, root, mask, spec)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
