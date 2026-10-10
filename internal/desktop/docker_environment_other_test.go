//go:build !windows

package desktop

import (
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/sandbox"
)

func TestDesktopUnsupportedLocalBackendDoesNotPreventDockerComposition(t *testing.T) {
	local, err := sandbox.NewPlatformLocalBackend()
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	localReadiness, err := local.Readiness(t.Context(), sandbox.LocalRuntimeCapabilities{Enabled: true})
	if err != nil || localReadiness.Ready {
		t.Fatalf("expected unsupported Local boundary: %#v %v", localReadiness, err)
	}
	docker := readyEnvironmentDockerProof(t)
	if !WorkspaceSandboxRuntimeAvailable(true, true, &localReadiness, &docker) {
		t.Fatal("Docker proof could not supply independent generic readiness")
	}
	root := t.TempDir()
	plane, err := OpenControlPlane(ControlPlaneConfig{DatabasePath: filepath.Join(root, "desktop.db"), HomePath: root,
		ReadToken: desktopControlPlaneTestToken, ControlToken: desktopControlPlaneControlToken,
		RunControlEnabled: true, RunExecutionEnabled: true, ExecutionPermissionControlEnabled: true,
		DockerExecutionEnabled: true, StandardCodeDockerImageDigest: docker.ImageDigest, StandardCodeDockerReadiness: &docker,
		LocalSandboxReadiness: &localReadiness, LocalSandboxBackend: local,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plane.Close()
	if plane.standardCodeDrydocks == nil || !plane.StandardCodePresetEnabled() {
		t.Fatal("unsupported Local backend blocked Docker coding")
	}
}
