package application

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
)

const (
	CommandRuntimeLocalSandboxBackend = "local_windows_lpac"
	commandRuntimeLocalToolchainRoot  = "/toolchains/runtime"
)

type LocalSandboxCommandRuntimeStore interface {
	GetRunExecutionProfile(context.Context, string) (
		domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	GetRunExecutionInteraction(context.Context, string) (
		domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionLease(context.Context, string) (
		domain.RunExecutionLease, bool, error)
	GetDrydockByRun(context.Context, string) (drydock.Workspace, bool, error)
}

// LocalSandboxCommandRuntimeExecutor compiles the backend-neutral
// command-runtime.v2 process shape into the already-proven Windows
// AppContainer/LPAC LocalRunRequest. It never accepts a source Workspace root
// or restores authority from a persisted Job row.
type LocalSandboxCommandRuntimeExecutor struct {
	store    LocalSandboxCommandRuntimeStore
	backend  sandbox.LocalBackend
	identity commandruntimeadapter.Identity
}

func NewLocalSandboxCommandRuntimeExecutor(store LocalSandboxCommandRuntimeStore,
	backend sandbox.LocalBackend, readiness sandbox.LocalReadiness,
) (*LocalSandboxCommandRuntimeExecutor, error) {
	if store == nil || backend == nil || readiness.Validate() != nil ||
		!readiness.Ready || readiness.Status != sandbox.LocalReadinessReady ||
		readiness.RuntimeGeneration != backend.Generation() {
		return nil, errors.New("Local Sandbox Command Runtime readiness is invalid")
	}
	identity := commandruntimeadapter.SandboxedWorkspace(
		CommandRuntimeLocalSandboxBackend,
		sandbox.LocalBackendName+"."+sandbox.LocalBackendPolicyVersion,
		backend.Generation())
	if !identity.Executable() {
		return nil, errors.New("Local Sandbox Command Runtime identity is invalid")
	}
	return &LocalSandboxCommandRuntimeExecutor{store: store, backend: backend,
		identity: identity}, nil
}

func (e *LocalSandboxCommandRuntimeExecutor) Identity() commandruntimeadapter.Identity {
	if e == nil {
		return commandruntimeadapter.Identity{}
	}
	return e.identity
}

func (e *LocalSandboxCommandRuntimeExecutor) Available() bool {
	return e != nil && e.store != nil && e.backend != nil &&
		e.backend.Generation() == e.identity.Generation && e.identity.Executable()
}

func (e *LocalSandboxCommandRuntimeExecutor) Ready(ctx context.Context,
	runID string,
) (bool, error) {
	if ctx == nil || ctx.Err() != nil || !e.Available() ||
		!domain.ValidAgentID(runID) {
		return false, nil
	}
	readiness, err := e.backend.Readiness(ctx,
		sandbox.LocalRuntimeCapabilities{Enabled: true})
	if err != nil {
		return false, err
	}
	if readiness.Validate() != nil || !readiness.Ready ||
		readiness.Status != sandbox.LocalReadinessReady ||
		readiness.RuntimeGeneration != e.identity.Generation {
		return false, nil
	}
	interaction, err := e.store.GetRunExecutionInteraction(ctx, runID)
	if err != nil {
		return false, err
	}
	return interaction.Validate() == nil && interaction.RunID == runID &&
		interaction.Mode == domain.RunExecutionInteractionControlled &&
		interaction.ExecutionProfile == domain.RunExecutionProfileLocal &&
		interaction.NetworkScope == domain.ExecutionNetworkDisabled &&
		readiness.Ready &&
		readiness.Status == sandbox.LocalReadinessReady &&
		readiness.RuntimeGeneration == e.identity.Generation, nil
}

func (e *LocalSandboxCommandRuntimeExecutor) ExecuteSandboxCommand(ctx context.Context,
	scope runner.CommandRuntimeScope, spec runner.CommandRuntimeResolvedSpec,
) (runner.CommandRuntimeSandboxResult, error) {
	if ctx == nil || ctx.Err() != nil || !e.Available() ||
		scope.Validate() != nil || !scope.Adapter.SameBackend(e.identity) ||
		scope.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
		spec.Spec.StdinPolicy != runner.CommandRuntimeStdinClosed ||
		!spec.Spec.CloseInitialStdin || spec.Spec.InitialStdin != "" {
		return runner.CommandRuntimeSandboxResult{}, runner.ErrCommandRuntimeBoundary
	}
	workspace, found, err := e.store.GetDrydockByRun(ctx, scope.RunID)
	if err != nil || !found {
		return runner.CommandRuntimeSandboxResult{}, errors.Join(err,
			runner.ErrCommandRuntimeBoundary)
	}
	profile, err := e.store.GetRunExecutionProfile(ctx, scope.RunID)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	permission, err := e.store.GetRunExecutionPermission(ctx, scope.RunID)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	interaction, err := e.store.GetRunExecutionInteraction(ctx, scope.RunID)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	lease, leaseFound, err := e.store.GetRunExecutionLease(ctx, scope.RunID)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	rootSHA256, err := runner.CommandRuntimeWorkspaceRootSHA256(workspace.Path)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	if !leaseFound || workspace.RunID != scope.RunID ||
		workspace.MissionID != scope.MissionID || workspace.SessionID != scope.SessionID ||
		workspace.SourceWorkspaceID != scope.WorkspaceID ||
		(workspace.State != drydock.StateReady && workspace.State != drydock.StateDelivered) ||
		rootSHA256 != scope.WorkspaceRootSHA256 || rootSHA256 != spec.WorkspaceRootSHA256 ||
		profile.ID != scope.ProfileSnapshotID || profile.Revision != scope.ProfileRevision ||
		profile.Profile != domain.RunExecutionProfileLocal ||
		permission.ID != scope.PermissionSnapshotID ||
		permission.Revision != scope.PermissionRevision ||
		permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		interaction.Mode != domain.RunExecutionInteractionControlled ||
		lease.LeaseID != scope.LeaseID || lease.Generation != scope.LeaseGeneration ||
		lease.OwnerID != scope.LeaseOwnerID || lease.Status != domain.RunExecutionLeaseActive {
		return runner.CommandRuntimeSandboxResult{}, runner.ErrCommandRuntimeBoundary
	}

	request, err := e.compile(scope, spec, workspace, profile, permission,
		interaction, lease)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	value, runErr := e.backend.Run(ctx, request)
	if validateErr := value.Validate(request); validateErr != nil {
		return runner.CommandRuntimeSandboxResult{}, errors.Join(runErr, validateErr)
	}
	return runner.CommandRuntimeSandboxResult{ExitCode: value.ExitCode,
		Stdout:     append([]byte(nil), value.Stdout.Data...),
		Stderr:     append([]byte(nil), value.Stderr.Data...),
		TreeReaped: value.TreeReaped}, runErr
}

func (e *LocalSandboxCommandRuntimeExecutor) compile(scope runner.CommandRuntimeScope,
	spec runner.CommandRuntimeResolvedSpec, workspace drydock.Workspace,
	profile domain.RunExecutionProfileSnapshot,
	permission domain.RunExecutionPermissionSnapshot,
	interaction domain.RunExecutionInteractionSnapshot, lease domain.RunExecutionLease,
) (sandbox.LocalRunRequest, error) {
	toolchainRoot := filepath.Clean(filepath.Dir(spec.ExecutablePath))
	toolchainSHA256, err := sandbox.LocalHostPathDigest(toolchainRoot)
	if err != nil {
		return sandbox.LocalRunRequest{}, err
	}
	drydockSHA256, err := sandbox.LocalHostPathDigest(workspace.Path)
	if err != nil {
		return sandbox.LocalRunRequest{}, err
	}
	relativeExecutable, err := filepath.Rel(toolchainRoot, spec.ExecutablePath)
	if err != nil || relativeExecutable == "." || strings.HasPrefix(relativeExecutable, "..") {
		return sandbox.LocalRunRequest{}, runner.ErrCommandRuntimeBoundary
	}
	virtualExecutable := path.Join(commandRuntimeLocalToolchainRoot,
		filepath.ToSlash(relativeExecutable))
	workingDirectory := "/workspace"
	if spec.Spec.WorkingDirectory != "." {
		workingDirectory = path.Join(workingDirectory,
			filepath.ToSlash(spec.Spec.WorkingDirectory))
	}
	arguments := make([]string, len(spec.CanonicalArgv))
	for index, argument := range spec.CanonicalArgv {
		arguments[index] = commandRuntimeLocalVirtualArgument(argument,
			workspace.Path, toolchainRoot)
	}
	environment := make([]sandbox.EnvironmentBinding, len(spec.Spec.Environment))
	for index, binding := range spec.Spec.Environment {
		environment[index] = sandbox.EnvironmentBinding{Name: binding.Name,
			Source: sandbox.EnvironmentLiteral, Value: binding.Value}
	}
	timeoutSeconds := int((spec.Spec.TimeoutMilliseconds + 999) / 1000)
	request := sandbox.LocalRunRequest{Manifest: sandbox.Manifest{
		ProtocolVersion: sandbox.ManifestProtocolVersion, Backend: sandbox.BackendLocal,
		Command: sandbox.CommandSpec{Executable: virtualExecutable,
			Arguments: arguments, WorkingDirectory: workingDirectory},
		Mounts: []sandbox.Mount{{Source: ".", Target: "/workspace",
			Access: sandbox.MountReadWrite}},
		Environment: environment, Network: sandbox.NetworkScope{Mode: "disabled"},
		Resources: sandbox.ResourceLimits{CPUQuotaMillis: 2_000,
			MemoryBytes: 2 * 1024 * 1024 * 1024, PIDs: 128,
			MaxOutputBytes: int64(spec.Spec.Output.ArtifactBytes)},
		Output:         sandbox.OutputSpec{CaptureStdout: true, CaptureStderr: true},
		TimeoutSeconds: timeoutSeconds,
		Cancellation:   sandbox.CancellationSpec{GracePeriodMillis: 2_000}},
		Binding: sandbox.LocalExecutionBinding{RunID: scope.RunID,
			MissionID: scope.MissionID, SessionID: scope.SessionID,
			WorkspaceID: scope.WorkspaceID, DrydockID: workspace.ID,
			DrydockRoot: workspace.Path, DrydockPathSHA256: drydockSHA256,
			DrydockRootFingerprint:    workspace.RootFingerprint,
			DrydockBindingFingerprint: workspace.ExpectedBindingFingerprint,
			DrydockGeneration:         workspace.Generation,
			PermissionSnapshotID:      permission.ID,
			PermissionRevision:        permission.Revision,
			ProfileSnapshotID:         profile.ID, ProfileRevision: profile.Revision,
			InteractionSnapshotID: interaction.ID,
			InteractionRevision:   interaction.Revision,
			CapabilityGeneration:  e.identity.Generation,
			LeaseID:               lease.LeaseID, LeaseGeneration: lease.Generation,
			OperationKeySHA256: runmutation.Fingerprint(
				"command_runtime_local_sandbox_operation.v1", scope.RunID,
				scope.OperationKey),
			RuntimeGeneration: e.backend.Generation()},
		ToolchainInputs: []sandbox.LocalToolchainInput{{
			ID: "command-runtime-toolchain", Root: toolchainRoot,
			VirtualRoot: commandRuntimeLocalToolchainRoot,
			RootSHA256:  toolchainSHA256}},
		MaxDiskWriteBytes: sandbox.DockerStandardCodeWorkspaceGrowthBytes}
	return sandbox.NormalizeLocalRunRequest(request)
}

func commandRuntimeLocalVirtualArgument(value, workspaceRoot,
	toolchainRoot string,
) string {
	for _, binding := range []struct {
		root    string
		virtual string
	}{{workspaceRoot, "/workspace"}, {toolchainRoot, commandRuntimeLocalToolchainRoot}} {
		if relative, err := filepath.Rel(binding.root, value); err == nil &&
			relative != "." && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return path.Join(binding.virtual, filepath.ToSlash(relative))
		}
	}
	return value
}

var _ runner.CommandRuntimeSandboxExecutor = (*LocalSandboxCommandRuntimeExecutor)(nil)
