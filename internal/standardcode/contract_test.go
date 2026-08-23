package standardcode

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/sandbox"
)

type readinessTransport struct {
	pingErr  error
	imageErr error
}

func (readinessTransport) Endpoint() sandbox.DockerObservationEndpoint {
	value, _ := sandbox.NewDockerObservationEndpoint(
		sandbox.DockerObservationEndpointLocalUnix)
	return value
}

func (transport readinessTransport) Ping(context.Context) error { return transport.pingErr }

func (readinessTransport) Version(context.Context) (sandbox.DockerDaemonVersion, error) {
	return sandbox.DockerDaemonVersion{APIVersion: "1.47", MinAPIVersion: "1.24",
		EngineVersion: "27.5.1", GitCommit: "abc123", OSType: "linux",
		Architecture: "amd64"}, nil
}

func (readinessTransport) Info(context.Context) (sandbox.DockerDaemonInfo, error) {
	return sandbox.DockerDaemonInfo{ID: "daemon", Name: "host",
		DockerRootDir: "/var/lib/docker", ServerVersion: "27.5.1",
		OSType: "linux", Architecture: "amd64", Driver: "overlay2",
		CgroupDriver: "systemd", CgroupVersion: "2", DefaultRuntime: "runc",
		NCPU: 8, MemoryBytes: 8 * 1024 * 1024 * 1024, PidsLimit: true,
		SecurityOptions: []string{"name=rootless"}}, nil
}

func (transport readinessTransport) InspectImage(_ context.Context,
	digest string,
) (sandbox.DockerImageInspection, error) {
	if transport.imageErr != nil {
		return sandbox.DockerImageInspection{}, transport.imageErr
	}
	return sandbox.DockerImageInspection{ID: "sha256:" + strings.Repeat("f", 64),
		RepoDigests: []string{"example.invalid/standard-code@" + digest},
		OSType:      "linux", Architecture: "amd64", SizeBytes: 4096,
		User: "65532:65532", RootFSType: "layers", GraphDriver: "overlay2"}, nil
}

func standardCodeManifestFixture(t *testing.T) sandbox.Manifest {
	t.Helper()
	manifest, err := CompileDockerManifest(ExecutionContext{
		RunID: "standard-code-run-1", MissionID: "standard-code-mission-1",
		SessionID: "standard-code-session-1", WorkspaceID: "standard-code-workspace-1",
		DrydockID:          "standard-code-drydock-1",
		DrydockWorkspaceID: "standard-code-drydock-workspace-1",
		DrydockGeneration:  1, CheckpointID: "standard-code-checkpoint-1",
		DrydockBindingSHA256: strings.Repeat("a", 64),
		ProfileSnapshotID:    "standard-code-profile-1", ProfileRevision: 1,
		PermissionSnapshotID: "standard-code-permission-1", PermissionRevision: 1,
		CapabilityGeneration: strings.Repeat("b", 64),
	}, Command{ProtocolVersion: CommandProtocolVersion,
		Toolchain: sandbox.DockerStandardCodeToolchainGo,
		Arguments: []string{"test", "./..."}, WorkingDirectory: ".",
		TimeoutSeconds: 120, Purpose: "run the fixed offline fixture"})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestDockerReadinessProjectsStableBlockedByAndRemediation(t *testing.T) {
	manifest := standardCodeManifestFixture(t)
	digest := "sha256:" + strings.Repeat("7", 64)
	tests := []struct {
		name        string
		capability  sandbox.DockerRuntimeCapabilities
		transport   readinessTransport
		reason      string
		blockedBy   []string
		remediation []string
	}{
		{name: "disabled", reason: sandbox.DockerReadinessReasonFeatureDisabled,
			blockedBy:   []string{"startup_gate_closed"},
			remediation: []string{"restart_with_startup_gate"}},
		{name: "daemon unavailable", capability: sandbox.DockerRuntimeCapabilities{Enabled: true},
			transport:   readinessTransport{pingErr: errors.New("daemon unavailable")},
			reason:      sandbox.DockerReadinessReasonDaemonUnreachable,
			blockedBy:   []string{"docker_unavailable"},
			remediation: []string{"install_or_start_docker"}},
		{name: "image missing", capability: sandbox.DockerRuntimeCapabilities{Enabled: true},
			transport:   readinessTransport{imageErr: errors.New("image missing")},
			reason:      sandbox.DockerReadinessReasonImageUnavailable,
			blockedBy:   []string{"backend_not_ready"},
			remediation: []string{"retry_backend_readiness"}},
		{name: "ready", capability: sandbox.DockerRuntimeCapabilities{Enabled: true},
			reason: sandbox.DockerReadinessReasonNone, blockedBy: []string{},
			remediation: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, err := sandbox.NewDockerReadinessProbe(test.transport)
			if err != nil {
				t.Fatal(err)
			}
			readiness, err := probe.Check(context.Background(), test.capability,
				manifest, digest)
			if err != nil {
				t.Fatal(err)
			}
			projection, err := DockerReadiness(readiness)
			if err != nil {
				t.Fatal(err)
			}
			if projection.ReasonCode != test.reason ||
				!reflect.DeepEqual(projection.BlockedBy, test.blockedBy) ||
				!reflect.DeepEqual(projection.Remediation, test.remediation) ||
				projection.CapabilityGrant {
				t.Fatalf("unstable readiness projection: %#v", projection)
			}
		})
	}
}

func TestCommandSchemaContainsNoBackendOrDockerControl(t *testing.T) {
	command := Command{ProtocolVersion: CommandProtocolVersion,
		Toolchain: sandbox.DockerStandardCodeToolchainPython,
		Arguments: []string{"-m", "pytest"}, WorkingDirectory: ".",
		TimeoutSeconds: 60, Purpose: "test offline"}
	if err := command.Validate(); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"backend", "image", "endpoint", "mount",
		"network", "environment", "credential", "docker", "flag", "host"} {
		if strings.Contains(strings.ToLower(string(payload)), forbidden) {
			t.Fatalf("Supervisor command schema exposed %q: %s", forbidden, payload)
		}
	}
}

func TestCommandFingerprintBindsOperatorPurpose(t *testing.T) {
	command := Command{ProtocolVersion: CommandProtocolVersion,
		Toolchain: sandbox.DockerStandardCodeToolchainGo,
		Arguments: []string{"test", "./..."}, WorkingDirectory: ".",
		TimeoutSeconds: 60, Purpose: "verify the offline fixture"}
	first, err := command.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	command.Purpose = "a different operator intent"
	second, err := command.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("command fingerprint did not bind operator purpose")
	}
}

func TestLocalAndDockerResultUseTheSameSchema(t *testing.T) {
	now := time.Now().UTC()
	exitCode := 0
	base := Result{ProtocolVersion: ResultProtocolVersion,
		ExecutionID: "standard-code-execution-1", RunID: "standard-code-run-1",
		DrydockID: "standard-code-drydock-1", Status: StatusSucceeded,
		ExitCode: &exitCode, Network: NetworkDisabled, Credentials: CredentialsNone,
		StartedAt: now, CompletedAt: now.Add(time.Second),
		Checkpoint: CheckpointResult{DrydockID: "standard-code-drydock-1",
			GenerationBefore: 1, GenerationAfter: 2,
			BeforeID:  "standard-code-checkpoint-1",
			AfterID:   "standard-code-checkpoint-2",
			ReceiptID: "standard-code-receipt-1"}, Artifacts: []ArtifactResult{}}
	keys := func(value Result) []string {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		result := make([]string, 0, len(decoded))
		for key := range decoded {
			result = append(result, key)
		}
		sort.Strings(result)
		return result
	}
	docker, local := base, base
	docker.Backend, local.Backend = BackendDocker, BackendLocal
	if docker.Validate() != nil || local.Validate() != nil ||
		!reflect.DeepEqual(keys(docker), keys(local)) {
		t.Fatalf("Local and Docker result schema drifted: docker=%v local=%v",
			keys(docker), keys(local))
	}
}
