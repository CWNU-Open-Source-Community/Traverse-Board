package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/standardcode"
)

const CommandRuntimeDockerSandboxBackend = "docker_standard_code"

// DockerSandboxCommandRuntimeExecutor projects command-runtime.v2's native
// process profile onto the fixed Standard Code Docker toolchains. Image,
// endpoint, mounts, network, credentials, and Docker flags remain Go-owned.
type DockerSandboxCommandRuntimeExecutor struct {
	service  *StandardCodeDockerService
	identity commandruntimeadapter.Identity
}

func NewDockerSandboxCommandRuntimeExecutor(service *StandardCodeDockerService) (
	*DockerSandboxCommandRuntimeExecutor, error,
) {
	if service == nil || service.docker == nil {
		return nil, errors.New("Docker Standard Code Command Runtime is unavailable")
	}
	generation, err := service.docker.StandardCodeCapabilityGeneration()
	if err != nil {
		return nil, err
	}
	identity := commandruntimeadapter.SandboxedWorkspace(
		CommandRuntimeDockerSandboxBackend, "docker-standard-code.v1", generation)
	if !identity.Executable() {
		return nil, errors.New("Docker Standard Code Command Runtime identity is invalid")
	}
	return &DockerSandboxCommandRuntimeExecutor{service: service,
		identity: identity}, nil
}

func (e *DockerSandboxCommandRuntimeExecutor) Identity() commandruntimeadapter.Identity {
	if e == nil {
		return commandruntimeadapter.Identity{}
	}
	return e.identity
}

func (e *DockerSandboxCommandRuntimeExecutor) Available() bool {
	if e == nil || e.service == nil || e.service.docker == nil ||
		!e.identity.Executable() || !e.service.docker.commandRuntimeStdinAvailable() {
		return false
	}
	generation, err := e.service.docker.StandardCodeCapabilityGeneration()
	return err == nil && generation == e.identity.Generation
}

func (e *DockerSandboxCommandRuntimeExecutor) Ready(ctx context.Context,
	runID string,
) (bool, error) {
	if !e.Available() {
		return false, nil
	}
	workspace, found, err := e.service.store.GetDrydockByRun(ctx, runID)
	if err != nil || !found {
		return false, err
	}
	command := standardcode.Command{ProtocolVersion: standardcode.CommandProtocolVersion,
		Toolchain: sandbox.DockerStandardCodeToolchainGo,
		Arguments: []string{"version"}, WorkingDirectory: ".", TimeoutSeconds: 30,
		Purpose: "probe the installed Command Runtime Docker adapter"}
	readiness, err := e.service.Readiness(ctx, StandardCodeDockerReadinessRequest{
		RunID: runID, ExpectedGeneration: workspace.Generation,
		ExpectedCheckpoint: workspace.LastCheckpointID, Command: command})
	return err == nil && readiness.Status == standardcode.ReadinessReady &&
		len(readiness.BlockedBy) == 0, err
}

func (e *DockerSandboxCommandRuntimeExecutor) ExecuteSandboxCommand(ctx context.Context,
	scope runner.CommandRuntimeScope, spec runner.CommandRuntimeResolvedSpec,
	stdin io.ReadCloser,
) (runner.CommandRuntimeSandboxResult, error) {
	if ctx == nil || ctx.Err() != nil {
		return runner.CommandRuntimeSandboxResult{}, fmt.Errorf(
			"%w: execution context is unavailable", runner.ErrCommandRuntimeBoundary)
	}
	if !e.Available() {
		return runner.CommandRuntimeSandboxResult{}, fmt.Errorf(
			"%w: Docker Standard Code adapter is unavailable or stale",
			runner.ErrCommandRuntimeBoundary)
	}
	if scope.Validate() != nil || !scope.Adapter.SameBackend(e.identity) {
		return runner.CommandRuntimeSandboxResult{}, fmt.Errorf(
			"%w: execution scope does not match the Docker Standard Code adapter",
			runner.ErrCommandRuntimeBoundary)
	}
	if scope.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
		spec.Spec.Profile != runner.CommandRuntimeProcess ||
		(spec.Spec.StdinPolicy == runner.CommandRuntimeStdinClosed &&
			(stdin != nil || !spec.Spec.CloseInitialStdin || spec.Spec.InitialStdin != "")) ||
		(spec.Spec.StdinPolicy == runner.CommandRuntimeStdinPipe && stdin == nil) ||
		(spec.Spec.StdinPolicy != runner.CommandRuntimeStdinClosed &&
			spec.Spec.StdinPolicy != runner.CommandRuntimeStdinPipe) {
		return runner.CommandRuntimeSandboxResult{}, fmt.Errorf(
			"%w: command or stdin policy is unsupported by Docker Standard Code",
			runner.ErrCommandRuntimeBoundary)
	}
	command, err := commandRuntimeDockerCommand(spec)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	workspace, found, err := e.service.store.GetDrydockByRun(ctx, scope.RunID)
	if err != nil || !found {
		return runner.CommandRuntimeSandboxResult{}, errors.Join(err,
			runner.ErrCommandRuntimeBoundary)
	}
	baseKey := "command-runtime-docker-" + runmutation.Fingerprint(
		"command_runtime_docker_adapter_operation.v1", scope.RunID,
		scope.OperationKey)[:24]
	stdinPolicy := sandbox.DockerStandardCodeStdinClosed
	if spec.Spec.StdinPolicy == runner.CommandRuntimeStdinPipe {
		stdinPolicy = sandbox.DockerStandardCodeStdinPipe
	}
	prepared, err := e.service.prepareCommandRuntime(ctx, StandardCodeDockerPrepareRequest{
		RunID: scope.RunID, ExpectedGeneration: workspace.Generation,
		ExpectedCheckpoint: workspace.LastCheckpointID,
		OperationKey:       baseKey + "-prepare", RequestedBy: scope.RootAgentID,
		Command: command}, stdinPolicy)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	if prepared.Blocked || prepared.Preparation == nil || prepared.Approval == nil {
		return runner.CommandRuntimeSandboxResult{}, errors.New(
			"Docker Standard Code adapter is not ready for this command")
	}
	decision, err := e.service.manifests.ReviewApproval(ctx,
		prepared.Preparation.Preparation.ID, approval.ActionApprove,
		baseKey+"-approve", "command_runtime_adapter", "")
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	lease, found, err := e.service.store.GetRunExecutionLease(ctx, scope.RunID)
	if err != nil || !found || lease.LeaseID != scope.LeaseID ||
		lease.Generation != scope.LeaseGeneration || lease.OwnerID != scope.LeaseOwnerID ||
		lease.Status != domain.RunExecutionLeaseActive {
		return runner.CommandRuntimeSandboxResult{}, errors.Join(err,
			fmt.Errorf("%w: Run execution lease changed during Docker admission",
				runner.ErrCommandRuntimeBoundary))
	}
	executed, err := e.service.executeCommandRuntime(ctx,
		StandardCodeDockerExecuteRequest{
			RunID: scope.RunID, ExpectedGeneration: workspace.Generation,
			ExpectedCheckpoint: workspace.LastCheckpointID,
			PreparationID:      prepared.Preparation.Preparation.ID,
			ApprovalID:         decision.Approval.ID, OperationKey: baseKey + "-execute",
			RequestedBy: scope.RootAgentID, Command: command}, lease, stdinPolicy, stdin)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	if !executed.Executed || executed.Result == nil ||
		executed.Result.Validate() != nil || executed.Result.ExitCode == nil {
		return runner.CommandRuntimeSandboxResult{}, errors.New(
			"Docker Standard Code adapter did not return a terminal receipt")
	}
	encoded, err := json.Marshal(executed.Result)
	if err != nil {
		return runner.CommandRuntimeSandboxResult{}, err
	}
	return runner.CommandRuntimeSandboxResult{ExitCode: *executed.Result.ExitCode,
		Stdout: append(encoded, '\n'), TreeReaped: true}, nil
}

func commandRuntimeDockerCommand(spec runner.CommandRuntimeResolvedSpec) (
	standardcode.Command, error,
) {
	base := strings.ToLower(filepath.Base(spec.ExecutablePath))
	toolchain := ""
	switch base {
	case "go", "go.exe":
		toolchain = sandbox.DockerStandardCodeToolchainGo
	case "node", "node.exe":
		toolchain = sandbox.DockerStandardCodeToolchainNode
	case "python", "python.exe", "python3", "python3.exe":
		toolchain = sandbox.DockerStandardCodeToolchainPython
	case "cargo", "cargo.exe":
		toolchain = sandbox.DockerStandardCodeToolchainRust
	default:
		return standardcode.Command{}, errors.New(
			"Docker Standard Code accepts only the fixed Go, Node, Python, or Rust toolchain")
	}
	command := standardcode.Command{ProtocolVersion: standardcode.CommandProtocolVersion,
		Toolchain: toolchain, Arguments: append([]string(nil), spec.CanonicalArgv...),
		WorkingDirectory: filepath.ToSlash(spec.Spec.WorkingDirectory),
		TimeoutSeconds:   int((spec.Spec.TimeoutMilliseconds + 999) / 1000),
		Purpose:          spec.Spec.Purpose}
	if err := command.Validate(); err != nil {
		return standardcode.Command{}, err
	}
	return command, nil
}

var _ runner.CommandRuntimeSandboxExecutor = (*DockerSandboxCommandRuntimeExecutor)(nil)

func (*DockerSandboxCommandRuntimeExecutor) OwnsWorkspaceCheckpoint() bool { return true }
