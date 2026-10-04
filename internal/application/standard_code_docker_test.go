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

func TestStandardCodeDockerApprovalPreferencesCheckpointAndRecovery(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) { standardCodeDockerCheckpointForApprovalMode(t, mode) })
	}
}

func standardCodeDockerCheckpointForApprovalMode(t *testing.T, mode domain.RunExecutionPermissionMode) {
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
		DangerFullAccessEnabled: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority(),
	}
	if mode != domain.RunExecutionPermissionAsk {
		if _, err := NewRunExecutionPermissionService(fixture.state,
			permissionCapabilities).Change(ctx, ChangeRunExecutionPermissionRequest{
			RunID: fixture.run.ID, Mode: string(mode),
			OperationKey: "standard-code-permission-0001", RequestedBy: requestedBy,
			Reason:      "exercise the fixed Standard Code Docker backend",
			ConfirmFull: mode == domain.RunExecutionPermissionFull,
		}); err != nil {
			t.Fatal(err)
		}

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
		attachBody: append(dockerLogFramePayload(1, "standard code output 中文\n"),
			dockerLogFramePayload(2, "standard code stderr 中文\n")...),
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
	acquired, err := fixture.state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: runRecord.ID,
			OwnerID: "command-runtime-docker-test-owner", TTL: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.state.BeginSupervisorTurn(ctx, acquired.Lease,
		"exercise attributed Docker Command Runtime")
	if err != nil {
		t.Fatal(err)
	}
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
		runRecord.ID, mode)
	if err != nil || !available || !advertised.SameBackend(executor.Identity()) {
		t.Fatalf("Docker adapter advertisement=%#v available=%t err=%v",
			advertised, available, err)
	}
	maxBytes := 32 * 1024
	f := commandFixtureForScope(t, fixture.state, commandRuntime, toolgateway.CommandRuntimeContext{RunID: runRecord.ID, MissionID: runRecord.MissionID})
	runtimeResult, err := f.execute(t, ctx,
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
		}, 1)
	if err != nil || runtimeResult.ValidateBoundAdapter() != nil ||
		!runtimeResult.Adapter.SameBackend(advertised) || len(runtimeResult.Jobs) != 1 ||
		runtimeResult.Jobs[0].State.Terminal() {
		t.Fatalf("Docker Command Runtime start=%+v err=%v", runtimeResult, err)
	}
	jobID := runtimeResult.Jobs[0].ID
	cursor := uint64(0)
	waitMilliseconds := int((5 * time.Second).Milliseconds())
	// Wait for Docker admission/checkpoint finalization before recording a new
	// model tool round, which legitimately changes the frozen budget snapshot.
	if _, err := commandRuntime.waitForTerminal(ctx, jobID); err != nil {
		t.Fatal(err)
	}
	runtimeResult, err = f.execute(t, ctx, toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionWait, JobID: jobID, Cursor: &cursor, MaxBytes: &maxBytes, WaitMilliseconds: &waitMilliseconds}, 2)
	if err != nil || runtimeResult.ValidateBoundAdapter() != nil || len(runtimeResult.Jobs) != 1 || len(runtimeResult.Pages) != 1 {
		t.Fatalf("Docker wait: %+v %v", runtimeResult, err)
	}
	if runtimeResult.Jobs[0].State != runner.CommandRuntimeJobCompleted ||
		len(runtimeResult.Artifacts) != 1 ||
		runtimeResult.Artifacts[0].Stdout != "standard code output 中文\n" ||
		runtimeResult.Artifacts[0].Stderr != "standard code stderr 中文\n" || baseLifecycle.starts != 3 {
		candidates, _ := fixture.state.ListSandboxExecutionCandidates(ctx,
			runRecord.ID, 20)
		t.Fatalf("Docker Command Runtime result=%+v err=%v lifecycle=%+v candidates=%+v",
			runtimeResult, err, baseLifecycle, candidates)
	}
	storedJob, err := fixture.state.GetCommandRuntimeJob(ctx, jobID)
	if err != nil || storedJob.Stdout != runtimeResult.Artifacts[0].Stdout ||
		storedJob.Stderr != runtimeResult.Artifacts[0].Stderr ||
		storedJob.StdoutSHA256 != serviceTestDigest(storedJob.Stdout) ||
		storedJob.StderrSHA256 != serviceTestDigest(storedJob.Stderr) {
		t.Fatalf("Command Runtime did not persist actual output and hashes: %+v err=%v", storedJob, err)
	}
	attachesBeforeReplay := ioTransport.ownedAttaches
	before, err := fixture.state.ListSupervisorToolRounds(ctx, f.turn.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatalf("Docker replay: %t %v", waiting, err)
	}
	after, err := fixture.state.ListSupervisorToolRounds(ctx, f.turn.Checkpoint)
	if err != nil || len(before) != 2 || len(after) != 2 || before[0].Calls[0].ResultJSON != after[0].Calls[0].ResultJSON || before[1].Calls[0].ResultJSON != after[1].Calls[0].ResultJSON || baseLifecycle.starts != 3 || ioTransport.ownedAttaches != attachesBeforeReplay {
		t.Fatalf("completed call replay re-executed or reattached: %v", err)
	}
	f.nextTurn(t, acquired.Lease)
	beforePipe, found, err := readRunFileDrydock(ctx, fixture.state, runRecord.ID)
	if err != nil || !found {
		t.Fatalf("Drydock before background command: found=%t err=%v", found, err)
	}
	pipeResult, err := f.execute(t, ctx,
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
		}, 1)
	if err != nil || len(pipeResult.Jobs) != 1 ||
		pipeResult.Jobs[0].State != runner.CommandRuntimeJobRunning ||
		len(pipeResult.IncompleteReasons) != 0 {
		t.Fatalf("Docker Command Runtime stdin start=%+v err=%v", pipeResult, err)
	}
	pipeJobID := pipeResult.Jobs[0].ID
	interactive, closePipe := "interactive\n", true
	for {
		ioTransport.mu.Lock()
		initial := string(ioTransport.stdin)
		ioTransport.mu.Unlock()
		if initial == "initial\n" {
			break
		}
		// Admission performs asynchronous Git checks before attaching stdin.
		// Observe the owned Job's existing lifecycle, not a second test timer.
		live, _, waitErr := manager.Wait(ctx, pipeJobID, 100*time.Millisecond, ^uint64(0), runner.MinCommandRuntimeOutputRead)
		if waitErr != nil || live.State.Terminal() {
			job, jobErr := fixture.state.GetCommandRuntimeJob(ctx, pipeJobID)
			ioTransport.mu.Lock()
			attached := ioTransport.ownedAttaches
			ioTransport.mu.Unlock()
			t.Fatalf("Docker initial stdin absent: bytes=%q state=%s stderr=%q reaped=%t job_err=%v attaches=%d lifecycle=%s starts=%d", initial, job.State, job.Stderr, job.TreeReaped, jobErr, attached, baseLifecycle.state, baseLifecycle.starts)
		}
	}
	f.nextTurnWithBackgroundJob(t, manager, pipeJobID)
	pipeResult, err = f.execute(t, ctx,
		toolgateway.CommandRuntimeInput{
			Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action:  toolgateway.CommandRuntimeActionWriteStdin, JobID: pipeJobID,
			Stdin: &interactive, CloseStdin: &closePipe,
		}, 1)
	if err != nil || len(pipeResult.Jobs) != 1 || !pipeResult.Jobs[0].StdinClosed {
		t.Fatalf("Docker Command Runtime stdin write=%+v err=%v", pipeResult, err)
	}
	pipeCursor := uint64(0)
	if _, err := commandRuntime.waitForTerminal(ctx, pipeJobID); err != nil {
		t.Fatal(err)
	}
	pipeResult, err = f.execute(t, ctx, toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionWait, JobID: pipeJobID, Cursor: &pipeCursor, MaxBytes: &maxBytes, WaitMilliseconds: &waitMilliseconds}, 2)
	if err != nil || len(pipeResult.Jobs) != 1 || len(pipeResult.Pages) != 1 {
		t.Fatalf("Docker stdin wait: %+v %v", pipeResult, err)
	}
	ioTransport.mu.Lock()
	stdinBytes := append([]byte(nil), ioTransport.stdin...)
	ioTransport.mu.Unlock()
	if pipeResult.Jobs[0].State != runner.CommandRuntimeJobCompleted ||
		!pipeResult.Jobs[0].StdinClosed || string(stdinBytes) !=
		"initial\ninteractive\n" || baseLifecycle.starts != 4 {
		candidates, _ := fixture.state.ListSandboxExecutionCandidates(ctx, runRecord.ID, 20)
		t.Fatalf("Docker Command Runtime stdin result=%+v input=%q lifecycle=%+v candidates=%+v",
			pipeResult, stdinBytes, baseLifecycle, candidates)
	}
	if len(pipeResult.Artifacts) != 1 || pipeResult.Artifacts[0].Stdout != storedJob.Stdout ||
		pipeResult.Artifacts[0].Stderr != storedJob.Stderr || baseLifecycle.terms != 0 ||
		baseLifecycle.deletes != 4 {
		t.Fatalf("background handoff lost output or interrupted the container: %+v lifecycle=%+v", pipeResult, baseLifecycle)
	}
	afterPipe, found, err := readRunFileDrydock(ctx, fixture.state, runRecord.ID)
	if err != nil || !found || afterPipe.Generation != beforePipe.Generation+1 ||
		afterPipe.LastCheckpointID == beforePipe.LastCheckpointID {
		t.Fatalf("background result lost its Drydock checkpoint: before=%+v after=%+v err=%v", beforePipe, afterPipe, err)
	}
}
