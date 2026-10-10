//go:build windows

package application

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/runworktree"
	"cyberagent-workbench/internal/sandbox"
)

type localCompileBackend struct{ sandbox.LocalBackend }

func (localCompileBackend) Generation() string { return strings.Repeat("a", 64) }

func TestLocalCommandCapabilitiesFollowPinnedRuntimeAndPreserveDrydock(t *testing.T) {
	baseSpec := localLaunchTestSpec(t)
	digest := strings.Repeat("a", 64)
	executor := &LocalSandboxCommandRuntimeExecutor{backend: localCompileBackend{},
		identity: commandruntimeadapter.Identity{Generation: digest}}
	workspace := runworktree.Workspace{ID: "drydock-test", Path: baseSpec.WorkspaceRoot,
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

// This backend never starts a native process. Its explicit cleanup outcomes
// exercise the application-to-manager proof boundary separately from LPAC.
type localCommandProofBackend struct {
	sandbox.LocalBackend
	calls  int
	reaped bool
}

func (*localCommandProofBackend) Generation() string { return strings.Repeat("a", 64) }
func (b *localCommandProofBackend) Run(context.Context, sandbox.LocalRunRequest) (sandbox.LocalExecutionResult, error) {
	b.calls++
	return sandbox.LocalExecutionResult{ExitCode: 125, TreeReaped: b.reaped}, errors.New("controlled Local cleanup failure")
}
func (b *localCommandProofBackend) RunWithStdin(ctx context.Context, r sandbox.LocalRunRequest, _ io.ReadCloser) (sandbox.LocalExecutionResult, error) {
	return b.Run(ctx, r)
}

func localCommandProofFixture(t *testing.T) (*LocalSandboxCommandRuntimeExecutor, *sbxApplicationStore, *localCommandProofBackend, runner.CommandRuntimeScope, runner.CommandRuntimeResolvedSpec) {
	t.Helper()
	spec := localLaunchTestSpec(t)
	at, digest := time.Now().UTC(), strings.Repeat("a", 64)
	state := &sbxApplicationStore{workspace: runworktree.Workspace{ID: "drydock-test", RunID: "run-test", MissionID: "mission-test", SessionID: "session-test",
		SourceWorkspaceID: "source-test", Path: spec.WorkspaceRoot, State: runworktree.StateReady, Generation: 1, RootFingerprint: digest, ExpectedBindingFingerprint: digest},
		profile:     domain.RunExecutionProfileSnapshot{ID: "profile-test", RunID: "run-test", MissionID: "mission-test", Revision: 1, Profile: domain.RunExecutionProfileLocal},
		permission:  domain.RunExecutionPermissionSnapshot{ID: "permission-test", RunID: "run-test", MissionID: "mission-test", Revision: 1, Mode: domain.RunExecutionPermissionAsk},
		interaction: domain.RunExecutionInteractionSnapshot{ID: "interaction-test", Revision: 1, Mode: domain.RunExecutionInteractionControlled},
		lease: domain.RunExecutionLease{RunID: "run-test", LeaseID: "lease-test", OwnerID: "owner-test", Generation: 1, Status: domain.RunExecutionLeaseActive,
			AcquiredAt: at, RenewedAt: at, ExpiresAt: at.Add(time.Minute)}}
	backend := &localCommandProofBackend{}
	executor := &LocalSandboxCommandRuntimeExecutor{store: state, backend: backend,
		identity: commandruntimeadapter.SandboxedWorkspace(CommandRuntimeLocalSandboxBackend, sandbox.LocalBackendName+"."+sandbox.LocalBackendPolicyVersion, digest)}
	scope := runner.CommandRuntimeScope{InvocationID: "invocation-test", OperationKey: "operation-test", RunID: "run-test", MissionID: "mission-test",
		RootAgentID: "root-test", AgentID: "root-test", AttributionSource: domain.AgentAttributionOperatorRoot, SessionID: "session-test", WorkspaceID: "source-test",
		WorkspaceRootSHA256: spec.WorkspaceRootSHA256, ModeSnapshotID: "mode-test", ModeRevision: 1, ProfileSnapshotID: "profile-test", ProfileRevision: 1,
		PermissionSnapshotID: "permission-test", PermissionRevision: 1, PermissionMode: domain.RunExecutionPermissionAsk,
		LeaseID: "lease-test", LeaseGeneration: 1, LeaseOwnerID: "owner-test", Adapter: executor.Identity()}
	if err := scope.Validate(); err != nil {
		t.Fatal(err)
	}
	return executor, state, backend, scope, spec
}

func TestLocalCommandPreDispatchAuthorityRejectionHasNoProcessToReap(t *testing.T) {
	for _, kind := range []string{"permission", "lease"} {
		t.Run(kind, func(t *testing.T) {
			executor, state, backend, scope, spec := localCommandProofFixture(t)
			if kind == "permission" {
				state.permission.Revision++
			} else {
				state.lease.Generation++
			}
			result, err := executor.ExecuteSandboxCommand(t.Context(), scope, spec, nil)
			if !errors.Is(err, runner.ErrCommandRuntimeBoundary) || backend.calls != 0 || !result.TreeReaped || result.ExitCode != 125 {
				t.Fatalf("known pre-dispatch rejection lost absence proof: result=%+v calls=%d error=%v", result, backend.calls, err)
			}
		})
	}
}

func TestLocalCommandRetainsBackendCleanupProofWhenReceiptValidationFails(t *testing.T) {
	for _, reaped := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup_unconfirmed", true: "tree_reaped_receipt_invalid"}[reaped], func(t *testing.T) {
			executor, _, backend, scope, spec := localCommandProofFixture(t)
			backend.reaped = reaped
			result, err := executor.ExecuteSandboxCommand(t.Context(), scope, spec, nil)
			if err == nil || backend.calls != 1 || result.TreeReaped != reaped || result.ExitCode != 125 {
				t.Fatalf("backend cleanup proof changed: result=%+v calls=%d error=%v", result, backend.calls, err)
			}
		})
	}
}
