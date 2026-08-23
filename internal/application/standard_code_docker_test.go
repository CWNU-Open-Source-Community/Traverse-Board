package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/standardcode"
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
}
