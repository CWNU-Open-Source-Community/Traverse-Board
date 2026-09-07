package application

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/standardcode"
	"cyberagent-workbench/internal/toolgateway"
)

type standardCodeDockerLifecycleTransport struct {
	*dockerLifecycleSupervisorTestTransport
	afterStart   func()
	afterCleanup func()
}

func (transport *standardCodeDockerLifecycleTransport) Start(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest,
	fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleObservation, bool, error) {
	observation, started, err := transport.dockerLifecycleSupervisorTestTransport.Start(
		ctx, request, fence)
	if err == nil && started && transport.afterStart != nil {
		transport.afterStart()
	}
	return observation, started, err
}

func (transport *standardCodeDockerLifecycleTransport) Cleanup(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest,
	fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleCleanupResult, error) {
	result, err := transport.dockerLifecycleSupervisorTestTransport.Cleanup(
		ctx, request, fence)
	if err == nil && transport.afterCleanup != nil {
		transport.afterCleanup()
	}
	return result, err
}

func TestStandardCodeDockerServiceExecutesIntoDrydockCheckpoint(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard code docker product")
	workspace := mustCreateDrydock(t, fixture)
	ctx := context.Background()
	requestedBy := "standard_code_operator"

	if _, err := NewRunExecutionProfileService(fixture.state).Change(ctx,
		ChangeRunExecutionProfileRequest{RunID: fixture.run.ID,
			Profile:      string(domain.RunExecutionProfileDocker),
			OperationKey: "standard-code-profile-0001", RequestedBy: requestedBy,
			Reason: "exercise the fixed Standard Code Docker backend"}); err != nil {
		t.Fatal(err)
	}
	permissionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
	}
	if _, err := NewRunExecutionPermissionService(fixture.state,
		permissionCapabilities).Change(ctx, ChangeRunExecutionPermissionRequest{
		RunID: fixture.run.ID, Mode: string(domain.RunExecutionPermissionWorkspaceAccess),
		OperationKey: "standard-code-permission-0001", RequestedBy: requestedBy,
		Reason:                 "exercise the fixed Standard Code Docker backend",
		ConfirmWorkspaceAccess: true,
	}); err != nil {
		t.Fatal(err)
	}

	imageDigest := "sha256:" + strings.Repeat("7", 64)
	endpoint, err := sandbox.NewDockerObservationEndpoint(
		sandbox.DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := sandbox.NewDockerReadinessProbe(&dockerSandboxReadinessTransport{
		endpoint: endpoint, image: imageDigest})
	if err != nil {
		t.Fatal(err)
	}
	manifestService := NewSandboxManifestService(fixture.state,
		policy.NewDefaultChecker()).WithStandardCodeDrydock(fixture.service).
		WithDockerContainerTransactionHarness(sandbox.NewInMemoryDockerWriteTransaction()).
		WithDockerProductionObserver(sandbox.NewReadOnlyDockerProductionObserver(
			applicationDockerObservationTransport{imageDigest: imageDigest}))
	recorder := &dockerLifecycleTestRecorder{}
	baseLifecycle := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, "")
	generatedPath := filepath.Join(workspace.Path, "standard-code-output.txt")
	lifecycle := &standardCodeDockerLifecycleTransport{
		dockerLifecycleSupervisorTestTransport: baseLifecycle,
		afterStart: func() {
			writeDrydockTestFile(t, generatedPath, "generated inside fixed Drydock\n")
		},
	}
	ioTransport := &fakeDockerContainerIOTransport{
		attachBody: dockerLogFramePayload(1, "standard code output\n"),
	}
	dockerService, err := NewDockerSandboxService(fixture.state, readiness,
		policy.NewDefaultChecker(), sandbox.DockerRuntimeCapabilities{Enabled: true},
		permissionCapabilities,
		WithDockerSandboxExecution(lifecycle, ioTransport, t.TempDir(), time.Minute),
		WithDockerStandardCode(fixture.service, imageDigest))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewStandardCodeDockerService(fixture.state, fixture.service,
		manifestService, dockerService, imageDigest)
	if err != nil {
		t.Fatal(err)
	}
	command := standardcode.Command{ProtocolVersion: standardcode.CommandProtocolVersion,
		Toolchain: sandbox.DockerStandardCodeToolchainGo,
		Arguments: []string{"test", "./..."}, WorkingDirectory: ".",
		TimeoutSeconds: 30, Purpose: "verify the fixed Docker backend"}

	ready, err := service.Readiness(ctx, StandardCodeDockerReadinessRequest{
		RunID: fixture.run.ID, ExpectedGeneration: workspace.Generation,
		ExpectedCheckpoint: workspace.LastCheckpointID, Command: command})
	if err != nil || ready.Validate() != nil || ready.Status != standardcode.ReadinessReady {
		t.Fatalf("Readiness()=%+v err=%v", ready, err)
	}
	prepared, err := service.Prepare(ctx, StandardCodeDockerPrepareRequest{
		RunID: fixture.run.ID, ExpectedGeneration: workspace.Generation,
		ExpectedCheckpoint: workspace.LastCheckpointID, OperationKey: "standard-code-prepare-0001",
		RequestedBy: requestedBy, Command: command})
	if err != nil || prepared.Blocked || prepared.Preparation == nil ||
		prepared.Approval == nil {
		t.Fatalf("Prepare()=%+v err=%v", prepared, err)
	}
	if _, err := manifestService.ReviewApproval(ctx, prepared.Preparation.Preparation.ID,
		approval.ActionApprove, "standard-code-review-0001", requestedBy, ""); err != nil {
		t.Fatal(err)
	}
	executeRequest := StandardCodeDockerExecuteRequest{
		RunID: fixture.run.ID, ExpectedGeneration: workspace.Generation,
		ExpectedCheckpoint: workspace.LastCheckpointID,
		PreparationID:      prepared.Preparation.Preparation.ID,
		ApprovalID:         prepared.Approval.ID, OperationKey: "standard-code-execute-0001",
		RequestedBy: requestedBy, Command: command}
	executed, err := service.Execute(ctx, executeRequest)
	if err != nil || !executed.Executed || executed.Result == nil ||
		executed.Result.Validate() != nil {
		t.Fatalf("Execute()=%+v err=%v", executed, err)
	}
	result := *executed.Result
	if result.Status != standardcode.StatusSucceeded ||
		result.Checkpoint.GenerationBefore != workspace.Generation ||
		result.Checkpoint.GenerationAfter != workspace.Generation+1 ||
		result.Checkpoint.BeforeID != workspace.LastCheckpointID ||
		result.Checkpoint.AfterID == workspace.LastCheckpointID ||
		len(result.Artifacts) != 1 || result.Artifacts[0].Kind != "logs" {
		t.Fatalf("unexpected Standard Code result: %+v", result)
	}
	snapshot, err := fixture.state.GetWorkspaceCheckpointSnapshot(ctx,
		result.Checkpoint.AfterID)
	if err != nil {
		t.Fatal(err)
	}
	foundGenerated := false
	for _, entry := range snapshot.Entries {
		if entry.Path == "standard-code-output.txt" && !entry.Tracked && !entry.Staged {
			foundGenerated = true
		}
	}
	if !foundGenerated || readDrydockTestFile(t, generatedPath) !=
		"generated inside fixed Drydock\n" {
		t.Fatalf("Drydock checkpoint did not preserve the generated file: %+v", snapshot.Entries)
	}
	if baseLifecycle.creates != 1 || baseLifecycle.starts != 1 ||
		baseLifecycle.deletes != 1 || ioTransport.ownedAttaches != 1 ||
		ioTransport.ownedExports != 0 {
		t.Fatalf("execution side effects were not exact: lifecycle=%+v io=%+v",
			baseLifecycle, ioTransport)
	}
	replayed, err := service.Execute(ctx, executeRequest)
	if err != nil || !replayed.Executed || replayed.Result == nil ||
		!replayed.Result.Replayed ||
		baseLifecycle.creates != 1 || baseLifecycle.starts != 1 ||
		baseLifecycle.deletes != 1 {
		t.Fatalf("terminal product replay repeated execution: replay=%+v err=%v lifecycle=%+v",
			replayed, err, baseLifecycle)
	}
	changedPurpose := executeRequest
	changedPurpose.Command.Purpose = "different operator intent"
	if _, err := service.Execute(ctx, changedPurpose); err == nil ||
		baseLifecycle.starts != 1 {
		t.Fatalf("operation replay accepted a different command purpose: err=%v lifecycle=%+v",
			err, baseLifecycle)
	}
	restartedDocker, err := NewDockerSandboxService(fixture.state, readiness,
		policy.NewDefaultChecker(), sandbox.DockerRuntimeCapabilities{Enabled: true},
		permissionCapabilities,
		WithDockerSandboxExecution(lifecycle, ioTransport, t.TempDir(), time.Minute),
		WithDockerStandardCode(fixture.service, imageDigest))
	if err != nil {
		t.Fatal(err)
	}
	restartedService, err := NewStandardCodeDockerService(fixture.state,
		fixture.service, manifestService, restartedDocker, imageDigest)
	if err != nil {
		t.Fatal(err)
	}
	restartedReplay, err := restartedService.Execute(ctx, executeRequest)
	if err != nil || !restartedReplay.Executed || restartedReplay.Result == nil ||
		!restartedReplay.Result.Replayed || baseLifecycle.starts != 1 {
		t.Fatalf("restart replay restored start authority: replay=%+v err=%v lifecycle=%+v",
			restartedReplay, err, baseLifecycle)
	}
	if recovered, err := restartedService.RecoverStartup(ctx); err != nil ||
		len(recovered) != 0 {
		t.Fatalf("completed checkpoint was recovered again: results=%+v err=%v", recovered, err)
	}

	current, found, err := fixture.state.GetDrydockByRun(ctx, fixture.run.ID)
	if err != nil || !found || current.Generation != workspace.Generation+1 ||
		current.LastCheckpointID != result.Checkpoint.AfterID {
		t.Fatalf("current Drydock=%+v found=%t err=%v", current, found, err)
	}
	preparedAfterCrash, err := service.Prepare(ctx, StandardCodeDockerPrepareRequest{
		RunID: fixture.run.ID, ExpectedGeneration: current.Generation,
		ExpectedCheckpoint: current.LastCheckpointID,
		OperationKey:       "standard-code-prepare-crash-0001",
		RequestedBy:        requestedBy, Command: command})
	if err != nil || preparedAfterCrash.Preparation == nil ||
		preparedAfterCrash.Approval == nil {
		t.Fatalf("crash preparation=%+v err=%v", preparedAfterCrash, err)
	}
	if _, err := manifestService.ReviewApproval(ctx,
		preparedAfterCrash.Preparation.Preparation.ID, approval.ActionApprove,
		"standard-code-review-crash-0001", requestedBy, ""); err != nil {
		t.Fatal(err)
	}
	lifecycle.afterStart = func() {
		writeDrydockTestFile(t, generatedPath,
			"generated before control-plane restart\n")
	}
	crashCtx, loseControlPlane := context.WithCancel(ctx)
	lifecycle.afterCleanup = loseControlPlane
	crashRequest := StandardCodeDockerExecuteRequest{
		RunID: fixture.run.ID, ExpectedGeneration: current.Generation,
		ExpectedCheckpoint: current.LastCheckpointID,
		PreparationID:      preparedAfterCrash.Preparation.Preparation.ID,
		ApprovalID:         preparedAfterCrash.Approval.ID,
		OperationKey:       "standard-code-execute-crash-0001",
		RequestedBy:        requestedBy, Command: command}
	crashed, crashErr := service.Execute(crashCtx, crashRequest)
	lifecycle.afterCleanup = nil
	if crashErr == nil || crashed.Executed || crashed.AdmissionID == "" ||
		baseLifecycle.starts != 2 || baseLifecycle.state !=
		sandbox.DockerContainerLifecycleStateAbsent {
		t.Fatalf("simulated restart gap did not preserve terminal ownership: result=%+v err=%v lifecycle=%+v",
			crashed, crashErr, baseLifecycle)
	}
	recovered, err := restartedService.RecoverStartup(ctx)
	if err != nil || len(recovered) != 1 ||
		recovered[0].Status != standardcode.StatusSucceeded ||
		recovered[0].Checkpoint.GenerationBefore != current.Generation ||
		recovered[0].Checkpoint.GenerationAfter != current.Generation+1 ||
		baseLifecycle.starts != 2 || baseLifecycle.state !=
		sandbox.DockerContainerLifecycleStateAbsent {
		t.Fatalf("restart recovery=%+v err=%v lifecycle=%+v", recovered, err, baseLifecycle)
	}
	if readDrydockTestFile(t, generatedPath) !=
		"generated before control-plane restart\n" {
		t.Fatal("restart recovery lost the container-attributed Drydock output")
	}
	if replay, err := restartedService.RecoverStartup(ctx); err != nil ||
		len(replay) != 0 || baseLifecycle.starts != 2 {
		t.Fatalf("restart recovery was not idempotent: replay=%+v err=%v lifecycle=%+v",
			replay, err, baseLifecycle)
	}

	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("Go executable is unavailable for Command Runtime adapter integration: %v", err)
	}
	goExecutable, err = filepath.Abs(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	runRecord, err := NewRunService(fixture.state).Start(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := fixture.state.GetRootAgent(ctx, runRecord.ID)
	if err != nil || !found {
		t.Fatalf("Command Runtime root found=%t err=%v", found, err)
	}
	acquired, err := fixture.state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: runRecord.ID,
			OwnerID: "command-runtime-docker-test-owner", TTL: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := fixture.state.BeginSupervisorTurn(ctx, acquired.Lease,
		"exercise attributed Docker Command Runtime")
	if err != nil {
		t.Fatal(err)
	}
	root = turn.Agent
	executor, err := NewDockerSandboxCommandRuntimeExecutor(restartedService)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runner.NewSandboxCommandRuntimeManager(fixture.state, executor,
		idgen.New("command-runtime-docker-manager"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdownCtx)
	}()
	commandRuntime, err := NewSandboxedCommandRuntimeService(fixture.state, manager,
		executor, permissionCapabilities, fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	advertised, available, err := commandRuntime.AdvertisedCommandRuntimeAdapter(ctx,
		runRecord.ID, domain.RunExecutionPermissionWorkspaceAccess)
	if err != nil || !available || !advertised.SameBackend(executor.Identity()) {
		t.Fatalf("Docker adapter advertisement=%#v available=%t err=%v",
			advertised, available, err)
	}
	maxBytes := 32 * 1024
	runtimeScope := toolgateway.CommandRuntimeContext{
		InvocationID: "command-runtime-docker-invocation",
		OperationKey: "command-runtime-docker-operation", RunID: runRecord.ID,
		MissionID:   runRecord.MissionID,
		RootAgentID: root.ID, AgentID: root.ID, AgentAttemptID: root.ActiveAttemptID,
		SessionID:            runRecord.SessionID,
		WorkspaceID:          fixture.workspace.ID,
		CapabilityGeneration: advertised.Generation,
		LeaseID:              acquired.Lease.LeaseID, LeaseGeneration: acquired.Lease.Generation,
		RequestedBy: "run_supervisor", Adapter: advertised,
		PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "medium",
			Reason: "test exact Docker sandbox adapter"},
	}
	if err := runtimeScope.Validate(); err != nil {
		t.Fatalf("Docker Command Runtime context is invalid before execution: %v", err)
	}
	bindings, err := commandRuntime.loadAuthorizedBindings(ctx, runtimeScope)
	if err != nil {
		t.Fatalf("Docker Command Runtime bindings are invalid before execution: %v", err)
	}
	resolved, err := runner.NormalizeCommandRuntimeSpec(runner.CommandRuntimeSpec{
		Version: runner.CommandRuntimeProtocolVersion,
		Profile: runner.CommandRuntimeProcess, Executable: goExecutable,
		Arguments: []string{"version"}, WorkingDirectory: ".",
		Environment: []runner.CommandRuntimeEnvironment{},
		StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
		TimeoutMilliseconds: 60_000,
		Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096,
			ArtifactBytes: 64 * 1024},
		Network:     runner.CommandRuntimeNetworkDisabled,
		Credentials: runner.CommandRuntimeCredentialsNone,
		Purpose:     "exercise Command Runtime through fixed Docker Standard Code",
	}, bindings.rootPath)
	if err != nil {
		t.Fatalf("Docker Command Runtime spec is invalid before execution: %v", err)
	}
	runnerScope := commandRuntime.runnerScope(runtimeScope, bindings,
		runtimeScope.OperationKey)
	if err := runnerScope.Validate(); err != nil ||
		resolved.WorkspaceRootSHA256 != runnerScope.WorkspaceRootSHA256 {
		t.Fatalf("Docker Command Runtime runner boundary is invalid before execution: scope=%+v resolved_root=%s err=%v",
			runnerScope, resolved.WorkspaceRootSHA256, err)
	}
	runtimeResult, err := commandRuntime.ExecuteCommandRuntime(ctx, runtimeScope,
		toolgateway.CommandRuntimeInput{
			Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action:  toolgateway.CommandRuntimeActionStart,
			Commands: []runner.CommandRuntimeSpec{{
				Version: runner.CommandRuntimeProtocolVersion,
				Profile: runner.CommandRuntimeProcess, Executable: goExecutable,
				Arguments: []string{"version"}, WorkingDirectory: ".",
				Environment: []runner.CommandRuntimeEnvironment{},
				StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
				TimeoutMilliseconds: 60_000,
				Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096,
					ArtifactBytes: 64 * 1024},
				Network:     runner.CommandRuntimeNetworkDisabled,
				Credentials: runner.CommandRuntimeCredentialsNone,
				Purpose:     "exercise Command Runtime through fixed Docker Standard Code",
			}},
		})
	if err != nil || runtimeResult.ValidateBoundAdapter() != nil ||
		!runtimeResult.Adapter.SameBackend(advertised) || len(runtimeResult.Jobs) != 1 ||
		runtimeResult.Jobs[0].State.Terminal() {
		t.Fatalf("Docker Command Runtime start=%+v err=%v", runtimeResult, err)
	}
	jobID := runtimeResult.Jobs[0].ID
	cursor := uint64(0)
	waitMilliseconds := int((5 * time.Second).Milliseconds())
	deadline := time.Now().Add(2 * time.Minute)
	for !runtimeResult.Jobs[0].State.Terminal() {
		if time.Now().After(deadline) {
			t.Fatalf("Docker Command Runtime Job did not become terminal: %+v", runtimeResult)
		}
		runtimeResult, err = commandRuntime.ExecuteCommandRuntime(ctx, runtimeScope,
			toolgateway.CommandRuntimeInput{
				Version: toolgateway.CommandRuntimeToolProtocolVersion,
				Action:  toolgateway.CommandRuntimeActionWait, JobID: jobID,
				Cursor: &cursor, MaxBytes: &maxBytes,
				WaitMilliseconds: &waitMilliseconds,
			})
		if err != nil || runtimeResult.ValidateBoundAdapter() != nil ||
			len(runtimeResult.Jobs) != 1 || len(runtimeResult.Pages) != 1 {
			t.Fatalf("Docker Command Runtime wait=%+v err=%v", runtimeResult, err)
		}
		cursor = runtimeResult.Pages[0].NextCursor
	}
	if runtimeResult.Jobs[0].State != runner.CommandRuntimeJobCompleted ||
		len(runtimeResult.Artifacts) != 1 ||
		!strings.Contains(runtimeResult.Artifacts[0].Stdout,
			standardcode.ResultProtocolVersion) || baseLifecycle.starts != 3 {
		candidates, _ := fixture.state.ListSandboxExecutionCandidates(ctx,
			runRecord.ID, 20)
		t.Fatalf("Docker Command Runtime result=%+v err=%v lifecycle=%+v candidates=%+v",
			runtimeResult, err, baseLifecycle, candidates)
	}

	pipeScope := runtimeScope
	pipeScope.InvocationID = "command-runtime-docker-stdin-invocation"
	pipeScope.OperationKey = "command-runtime-docker-stdin-start"
	pipeResult, err := commandRuntime.ExecuteCommandRuntime(ctx, pipeScope,
		toolgateway.CommandRuntimeInput{
			Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action:  toolgateway.CommandRuntimeActionStart,
			Commands: []runner.CommandRuntimeSpec{{
				Version: runner.CommandRuntimeProtocolVersion,
				Profile: runner.CommandRuntimeProcess, Executable: goExecutable,
				Arguments: []string{"version"}, WorkingDirectory: ".",
				Environment:  []runner.CommandRuntimeEnvironment{},
				StdinPolicy:  runner.CommandRuntimeStdinPipe,
				InitialStdin: "initial\n", CloseInitialStdin: false,
				TimeoutMilliseconds: 60_000,
				Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096,
					ArtifactBytes: 64 * 1024},
				Network:     runner.CommandRuntimeNetworkDisabled,
				Credentials: runner.CommandRuntimeCredentialsNone,
				Purpose:     "stream stdin through fixed Docker Standard Code",
			}},
		})
	if err != nil || len(pipeResult.Jobs) != 1 ||
		pipeResult.Jobs[0].State != runner.CommandRuntimeJobRunning ||
		len(pipeResult.IncompleteReasons) != 0 {
		t.Fatalf("Docker Command Runtime stdin start=%+v err=%v", pipeResult, err)
	}
	pipeJobID := pipeResult.Jobs[0].ID
	interactive, closePipe := "interactive\n", true
	pipeScope.InvocationID = "command-runtime-docker-stdin-write-invocation"
	pipeScope.OperationKey = "command-runtime-docker-stdin-write"
	pipeResult, err = commandRuntime.ExecuteCommandRuntime(ctx, pipeScope,
		toolgateway.CommandRuntimeInput{
			Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action:  toolgateway.CommandRuntimeActionWriteStdin, JobID: pipeJobID,
			Stdin: &interactive, CloseStdin: &closePipe,
		})
	if err != nil || len(pipeResult.Jobs) != 1 || !pipeResult.Jobs[0].StdinClosed {
		t.Fatalf("Docker Command Runtime stdin write=%+v err=%v", pipeResult, err)
	}
	pipeCursor := uint64(0)
	pipeScope.InvocationID = "command-runtime-docker-stdin-wait-invocation"
	for !pipeResult.Jobs[0].State.Terminal() {
		if time.Now().After(deadline.Add(2 * time.Minute)) {
			t.Fatalf("Docker Command Runtime stdin Job did not become terminal: %+v",
				pipeResult)
		}
		pipeScope.OperationKey = "command-runtime-docker-stdin-wait-" +
			string(rune('a'+len(pipeResult.Pages)))
		pipeResult, err = commandRuntime.ExecuteCommandRuntime(ctx, pipeScope,
			toolgateway.CommandRuntimeInput{
				Version: toolgateway.CommandRuntimeToolProtocolVersion,
				Action:  toolgateway.CommandRuntimeActionWait, JobID: pipeJobID,
				Cursor: &pipeCursor, MaxBytes: &maxBytes,
				WaitMilliseconds: &waitMilliseconds,
			})
		if err != nil || len(pipeResult.Jobs) != 1 || len(pipeResult.Pages) != 1 {
			t.Fatalf("Docker Command Runtime stdin wait=%+v err=%v", pipeResult, err)
		}
		pipeCursor = pipeResult.Pages[0].NextCursor
	}
	ioTransport.mu.Lock()
	stdinBytes := append([]byte(nil), ioTransport.stdin...)
	ioTransport.mu.Unlock()
	if pipeResult.Jobs[0].State != runner.CommandRuntimeJobCompleted ||
		!pipeResult.Jobs[0].StdinClosed || string(stdinBytes) !=
		"initial\ninteractive\n" || baseLifecycle.starts != 4 {
		t.Fatalf("Docker Command Runtime stdin result=%+v input=%q lifecycle=%+v",
			pipeResult, stdinBytes, baseLifecycle)
	}
}
