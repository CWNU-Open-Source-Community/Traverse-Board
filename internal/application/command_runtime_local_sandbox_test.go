//go:build windows

package application

import (
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
)

type localCompileBackend struct{ sandbox.LocalBackend }

func (localCompileBackend) Generation() string { return strings.Repeat("a", 64) }

func TestLocalCommandCapabilitiesFollowPinnedRuntimeAndPreserveDrydock(t *testing.T) {
	baseSpec := localLaunchTestSpec(t)
	digest := strings.Repeat("a", 64)
	executor := &LocalSandboxCommandRuntimeExecutor{backend: localCompileBackend{},
		identity: commandruntimeadapter.Identity{Generation: digest}}
	workspace := drydock.Workspace{ID: "drydock-test", Path: baseSpec.WorkspaceRoot,
		Generation: 1, RootFingerprint: digest, ExpectedBindingFingerprint: digest}
	scope := runner.CommandRuntimeScope{RunID: "run-test", MissionID: "mission-test",
		SessionID: "session-test", WorkspaceID: "source-workspace", OperationKey: "test-operation"}
	profile := domain.RunExecutionProfileSnapshot{ID: "profile-test", Revision: 1}
	permission := domain.RunExecutionPermissionSnapshot{ID: "permission-test", Revision: 1}
	interaction := domain.RunExecutionInteractionSnapshot{ID: "interaction-test", Revision: 1}
	lease := domain.RunExecutionLease{LeaseID: "lease-test", Generation: 1}
	for _, test := range []struct {
		name            string
		profile         runner.CommandRuntimeProfile
		instrumentation bool
	}{
		{"pwsh.exe", runner.CommandRuntimePowerShell, true},
		{"bash.exe", runner.CommandRuntimeBash, false},
		{"go.exe", runner.CommandRuntimeProcess, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := baseSpec
			spec.Spec.Profile = test.profile
			request, err := executor.compile(scope, spec, workspace, profile, permission, interaction, lease)
			if err != nil {
				t.Fatal(err)
			}
			if request.Instrumentation != test.instrumentation {
				t.Fatalf("runtime instrumentation=%t", request.Instrumentation)
			}
			if request.MaxDiskWriteBytes != sandbox.DefaultLocalDiskWriteLimit {
				t.Fatalf("Local total Job/scratch write budget must use its backend policy, got %d", request.MaxDiskWriteBytes)
			}
			if request.Binding.DrydockRoot != workspace.Path || request.Binding.WorkspaceID != scope.WorkspaceID ||
				request.Manifest.Network.Mode != "disabled" || len(request.ToolchainInputs) != 1 ||
				request.ToolchainInputs[0].Root != filepath.Dir(spec.ExecutablePath) {
				t.Fatalf("workspace or network boundary changed: %+v", request)
			}
		})
	}
}
