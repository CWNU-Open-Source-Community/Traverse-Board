package desktop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/sandbox"
)

type environmentDockerTransport struct {
	calls       int
	imageDigest string
}

func (stub *environmentDockerTransport) Endpoint() sandbox.DockerObservationEndpoint {
	endpoint, _ := sandbox.NewDockerObservationEndpoint(sandbox.DockerObservationEndpointLocalUnix)
	return endpoint
}
func (stub *environmentDockerTransport) Ping(context.Context) error { stub.calls++; return nil }
func (stub *environmentDockerTransport) Version(context.Context) (sandbox.DockerDaemonVersion, error) {
	return sandbox.DockerDaemonVersion{APIVersion: "1.47", MinAPIVersion: "1.24", EngineVersion: "27.5.1", OSType: "linux", Architecture: "amd64"}, nil
}
func (stub *environmentDockerTransport) Info(context.Context) (sandbox.DockerDaemonInfo, error) {
	return sandbox.DockerDaemonInfo{ID: "fixture", ServerVersion: "27.5.1", OSType: "linux", Architecture: "amd64", NCPU: 8, MemoryBytes: 8 * 1024 * 1024 * 1024, PidsLimit: true}, nil
}
func (stub *environmentDockerTransport) InspectImage(context.Context, string) (sandbox.DockerImageInspection, error) {
	return sandbox.DockerImageInspection{ID: stub.imageDigest, RepoDigests: []string{"fixture@" + stub.imageDigest}, OSType: "linux", Architecture: "amd64", SizeBytes: 1024, User: "65532:65532", RootFSType: "layers", GraphDriver: "overlay2"}, nil
}

func readyEnvironmentDockerProof(t *testing.T) sandbox.DockerReadiness {
	t.Helper()
	stub := &environmentDockerTransport{imageDigest: "sha256:" + strings.Repeat("a", 64)}
	probe, err := sandbox.NewDockerReadinessProbe(stub)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := probeStandardCodeDockerReadiness(t.Context(), probe, true, stub.imageDigest)
	if err != nil || !proof.Ready {
		t.Fatalf("readiness=%#v err=%v", proof, err)
	}
	return proof
}

func TestDockerEnvironmentProbeNeverContactsDaemonWithoutPinnedConfiguration(t *testing.T) {
	stub := &environmentDockerTransport{}
	probe, _ := sandbox.NewDockerReadinessProbe(stub)
	for _, enabled := range []bool{false, true} {
		value, err := probeStandardCodeDockerReadiness(t.Context(), probe, enabled, "")
		if err != nil || value.Ready || stub.calls != 0 {
			t.Fatalf("invalid configuration probed daemon: %#v %v", value, err)
		}
	}
}

func TestDesktopDockerAssemblyWorksIndependentlyOfLocalBackend(t *testing.T) {
	proof := readyEnvironmentDockerProof(t)
	local, err := sandbox.NewPlatformLocalBackend()
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	localReadiness, err := local.Readiness(t.Context(), sandbox.LocalRuntimeCapabilities{Enabled: false})
	if err != nil || localReadiness.Ready {
		t.Fatalf("expected closed Local backend: %#v %v", localReadiness, err)
	}
	// Resolve platform temp aliases before deriving managed worktree paths.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plane, err := OpenControlPlane(ControlPlaneConfig{DatabasePath: filepath.Join(root, "desktop.db"), HomePath: root,
		ReadToken: desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunControlEnabled: true, RunExecutionEnabled: true, ExecutionPermissionControlEnabled: true,
		DockerExecutionEnabled: true, StandardCodeDockerImageDigest: proof.ImageDigest, StandardCodeDockerReadiness: &proof,
		LocalSandboxBackend: local, LocalSandboxReadiness: &localReadiness,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	if plane.standardCodeDrydocks == nil || !plane.StandardCodePresetEnabled() {
		t.Fatal("Docker could not assemble Drydock and Standard Code without Local")
	}
	installed, _ := plane.CommandRuntimeProcessStatus()
	if !installed {
		t.Fatal("Docker command adapter was not installed")
	}
}

func TestDesktopWorkspaceSandboxAvailabilityKeepsDockerProofAndLocalIndependent(t *testing.T) {
	proof := readyEnvironmentDockerProof(t)
	local := &sandbox.LocalReadiness{Ready: false}
	if !WorkspaceSandboxRuntimeAvailable(true, true, local, &proof) {
		t.Fatal("unavailable Local erased current Docker readiness")
	}
	if WorkspaceSandboxRuntimeAvailable(false, true, local, &proof) || WorkspaceSandboxRuntimeAvailable(true, false, local, &proof) {
		t.Fatal("Docker readiness bypassed a startup gate")
	}
	tampered := proof
	tampered.ReadinessFingerprint = strings.Repeat("0", 64)
	if WorkspaceSandboxRuntimeAvailable(true, true, local, &tampered) || WorkspaceSandboxRuntimeAvailable(true, true, local, &sandbox.DockerReadiness{Ready: true}) {
		t.Fatal("unvalidated Docker readiness opened a runtime gate")
	}
}
