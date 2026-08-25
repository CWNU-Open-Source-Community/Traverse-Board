package application

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	eventpkg "cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
)

func TestStandardCodePresetConfiguresOneAtomicLocalTupleAndReplays(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-preset")
	runtime := CapabilityReadinessRuntime{
		RunControlEnabled:                 true,
		RunExecutionEnabled:               true,
		ExecutionPermissionControlEnabled: true,
		StandardCodePresetEnabled:         true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		},
		LocalSandboxInstalled: true, LocalSandboxProven: true,
		LocalBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{
			commandruntimeadapter.SandboxedWorkspace(
				CommandRuntimeLocalSandboxBackend, "local-windows-lpac.v1",
				"standard-code-test-generation"),
		},
	}
	service, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-preview-0001", RequestedBy: "operator",
	})
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" ||
		preview.Status != StandardCodeResultBlocked {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	request := ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-configure-0001", RequestedBy: "operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest,
	}
	configured, err := service.Configure(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Status != StandardCodeResultConfigured || configured.Replayed ||
		configured.RunID != fixture.run.ID || !configured.DrydockReady ||
		configured.SelectedBackend != domain.StandardCodeSelectedLocal ||
		configured.SelectionReason != domain.StandardCodeReasonAutoLocalReady ||
		configured.Mode == nil || configured.Mode.Surface != domain.ExecutionSurfaceCode ||
		configured.Mode.Phase != domain.ExecutionPhasePlan ||
		configured.Profile == nil || configured.Profile.Profile != domain.RunExecutionProfileLocal ||
		configured.Interaction == nil || configured.Interaction.Mode != domain.RunExecutionInteractionControlled ||
		configured.Permission == nil || configured.Permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		configured.BrowserCDP == nil || configured.BrowserCDP.Mode != domain.RunBrowserCDPPermissionRestricted ||
		configured.CapabilityGrant {
		t.Fatalf("configured=%+v", configured)
	}
	operation, found, err := fixture.state.GetStandardCodePresetOperation(t.Context(),
		runmutation.Fingerprint("standard_code_preset_operation.v1", request.OperationKey))
	if err != nil || !found || operation.Status != domain.StandardCodePresetConfigured {
		t.Fatalf("configured operation=%+v found=%t err=%v", operation, found, err)
	}
	timeline, err := fixture.state.ListRunEvents(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	startType, endType := "", ""
	for _, item := range timeline {
		if item.Sequence == operation.EventSequenceStart {
			startType = item.Type
		}
		if item.Sequence == operation.EventSequenceEnd {
			endType = item.Type
		}
	}
	if startType != eventpkg.StandardCodePresetIntentRecordedEvent ||
		endType != eventpkg.StandardCodePresetConfiguredEvent {
		t.Fatalf("preset event range=%d..%d start=%q end=%q",
			operation.EventSequenceStart, operation.EventSequenceEnd, startType, endType)
	}
	eventCount := len(timeline)
	replayed, err := service.Configure(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.Status != StandardCodeResultConfigured ||
		replayed.RunID != configured.RunID || replayed.Mode.ID != configured.Mode.ID ||
		replayed.Profile.ID != configured.Profile.ID ||
		replayed.Interaction.ID != configured.Interaction.ID ||
		replayed.Permission.ID != configured.Permission.ID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	timeline, err = fixture.state.ListRunEvents(t.Context(), fixture.run.ID)
	if err != nil || len(timeline) != eventCount {
		t.Fatalf("exact replay appended events: before=%d after=%d err=%v",
			eventCount, len(timeline), err)
	}
	changed := request
	changed.BackendIntent = "docker"
	if _, err := service.Configure(t.Context(), changed); err == nil ||
		apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed request did not conflict: %v", err)
	}
}

func TestStandardCodePresetCreatesConfiguredCodeRunFromWorkspace(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-workspace-create")
	runtime := CapabilityReadinessRuntime{
		RunControlEnabled:                 true,
		RunExecutionEnabled:               true,
		ExecutionPermissionControlEnabled: true,
		StandardCodePresetEnabled:         true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		},
		LocalSandboxInstalled: true, LocalSandboxProven: true,
		LocalBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{
			commandruntimeadapter.SandboxedWorkspace(
				CommandRuntimeLocalSandboxBackend, "local-windows-lpac.v1",
				"standard-code-workspace-create-generation"),
		},
	}
	service, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version:     domain.StandardCodePresetProtocolVersion,
		WorkspaceID: fixture.workspace.ID, Goal: "implement the requested change",
		BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-workspace-preview-0001", RequestedBy: "operator",
	})
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" ||
		preview.RunID != "" {
		t.Fatalf("workspace preview=%+v err=%v", preview, err)
	}
	configured, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version:     domain.StandardCodePresetProtocolVersion,
		WorkspaceID: fixture.workspace.ID, Goal: "implement the requested change",
		BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-workspace-create-0001", RequestedBy: "operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if configured.Status != StandardCodeResultConfigured || configured.RunID == "" ||
		configured.Run == nil || configured.Run.ID != configured.RunID ||
		configured.Run.Status != domain.RunCreated ||
		configured.Mode == nil || configured.Mode.Surface != domain.ExecutionSurfaceCode ||
		configured.Mode.Phase != domain.ExecutionPhasePlan ||
		configured.Profile == nil || configured.Profile.Profile != domain.RunExecutionProfileLocal ||
		configured.Interaction == nil || configured.Interaction.Mode != domain.RunExecutionInteractionControlled ||
		configured.Permission == nil || configured.Permission.Mode != domain.RunExecutionPermissionWorkspaceAccess ||
		configured.BrowserCDP == nil || configured.BrowserCDP.Mode != domain.RunBrowserCDPPermissionRestricted ||
		!configured.DrydockReady || configured.Network != "disabled" ||
		configured.Credentials != "none" || configured.CapabilityGrant {
		t.Fatalf("workspace configured=%+v", configured)
	}
	if configured.RunID == fixture.run.ID {
		t.Fatal("Workspace preset reused an unrelated existing Run")
	}
	persisted, err := fixture.state.GetRun(t.Context(), configured.RunID)
	if err != nil || persisted.MissionID != configured.Run.MissionID {
		t.Fatalf("persisted Run=%+v err=%v", persisted, err)
	}
}

func TestStandardCodePresetRejectsNonOperatorInvocation(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-model-denied")
	service, err := NewStandardCodePresetService(fixture.state, fixture.service,
		CapabilityReadinessRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	for _, requester := range []string{"model", "skill", "mcp", "repository"} {
		_, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
			Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
			BackendIntent: "auto", Action: "configure",
			OperationKey: "standard-code-denied-" + requester, RequestedBy: requester,
		})
		if err == nil || apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("requester %q err=%v", requester, err)
		}
	}
}

func TestStandardCodePauseAndConfigureRequiresExistingRun(t *testing.T) {
	_, _, _, err := normalizeStandardCodePresetRequest(ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, WorkspaceID: "workspace-pause-new",
		Goal: "must not create", BackendIntent: "auto", Action: "pause_and_configure",
		OperationKey: "standard-code-pause-new-0001", RequestedBy: "operator",
	})
	if err == nil {
		t.Fatal("pause-and-configure without a Run was accepted")
	}
}

func TestStandardCodePresetBoundsNewRunGoal(t *testing.T) {
	base := ConfigureStandardCodeRequest{Version: domain.StandardCodePresetProtocolVersion,
		WorkspaceID: "workspace-goal-bound", BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-goal-bound-0001", RequestedBy: "operator"}
	base.Goal = strings.Repeat("x", domain.MaxRunCreationGoalBytes+1)
	if _, _, _, err := normalizeStandardCodePresetRequest(base); err == nil {
		t.Fatal("oversized Standard Code goal was accepted")
	}
	base.Goal = string([]byte{0xff})
	if _, _, _, err := normalizeStandardCodePresetRequest(base); err == nil {
		t.Fatal("invalid UTF-8 Standard Code goal was accepted")
	}
}

func TestStandardCodePresetRequiresExplicitDockerWhenLocalIsUnavailable(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-explicit-docker")
	runtime := CapabilityReadinessRuntime{
		RunControlEnabled: true, RunExecutionEnabled: true,
		ExecutionPermissionControlEnabled: true, StandardCodePresetEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		},
		DockerStartupGateEnabled: true, DockerAvailable: true, DockerBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{
			commandruntimeadapter.SandboxedWorkspace(
				CommandRuntimeDockerSandboxBackend, "standard-code-docker-runner.v2",
				"standard-code-docker-test-generation"),
		},
	}
	service, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
	if err != nil {
		t.Fatal(err)
	}
	autoKey := "standard-code-auto-no-fallback-0001"
	blocked, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "auto", Action: "configure", OperationKey: autoKey,
		RequestedBy: "operator",
	})
	if err != nil || blocked.Status != StandardCodeResultBlocked ||
		blocked.RunID != fixture.run.ID || blocked.WorkspaceID != fixture.workspace.ID ||
		blocked.SelectedBackend != "" || blocked.TrustRequired ||
		len(blocked.NextSteps) != 2 ||
		blocked.NextSteps[0] != StandardCodeNextSelectDocker ||
		blocked.NextSteps[1] != StandardCodeNextSelectApproval {
		t.Fatalf("auto result=%+v err=%v", blocked, err)
	}
	if _, found, err := fixture.state.GetStandardCodePresetOperation(t.Context(),
		runmutation.Fingerprint("standard_code_preset_operation.v1", autoKey)); err != nil || found {
		t.Fatalf("blocked auto intent mutated durable state: found=%t err=%v", found, err)
	}

	preview, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "docker", Action: "configure",
		OperationKey: "standard-code-docker-preview-0001", RequestedBy: "operator",
	})
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" {
		t.Fatalf("docker preview=%+v err=%v", preview, err)
	}
	configured, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "docker", Action: "configure",
		OperationKey: "standard-code-docker-configure-0001", RequestedBy: "operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest,
	})
	if err != nil || configured.Status != StandardCodeResultConfigured ||
		configured.SelectedBackend != domain.StandardCodeSelectedDocker ||
		configured.SelectionReason != domain.StandardCodeReasonExplicitDocker ||
		configured.Profile == nil ||
		configured.Profile.Profile != domain.RunExecutionProfileDocker ||
		configured.Interaction == nil ||
		configured.Interaction.RequiredGate != domain.ExecutionInteractionGateDockerSandbox ||
		configured.Interaction.NetworkScope != domain.ExecutionNetworkDisabled ||
		configured.Permission == nil ||
		configured.Permission.NetworkScope != domain.ExecutionPermissionNetworkDisabled {
		t.Fatalf("docker configured=%+v err=%v", configured, err)
	}
}

func TestStandardCodePresetPersistsPauseIntentAndWaitsForLease(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-pause-intent")
	runtime := CapabilityReadinessRuntime{
		RunControlEnabled: true, RunExecutionEnabled: true,
		ExecutionPermissionControlEnabled: true, StandardCodePresetEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
		},
		LocalSandboxInstalled: true, LocalSandboxProven: true, LocalBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{
			commandruntimeadapter.SandboxedWorkspace(
				CommandRuntimeLocalSandboxBackend, "local-windows-lpac.v1",
				"standard-code-pause-test-generation"),
		},
	}
	service, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
	if err != nil {
		t.Fatal(err)
	}
	running, err := NewRunService(fixture.state).Start(t.Context(), fixture.run.ID)
	if err != nil || running.Status != domain.RunRunning {
		t.Fatalf("start=%+v err=%v", running, err)
	}
	ordinary, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-running-ordinary-0001", RequestedBy: "operator",
	})
	if err != nil || ordinary.Status != StandardCodeResultBlocked ||
		len(ordinary.NextSteps) != 1 ||
		ordinary.NextSteps[0] != StandardCodeNextPauseAndConfigure {
		t.Fatalf("ordinary running result=%+v err=%v", ordinary, err)
	}
	preview, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "auto", Action: "pause_and_configure",
		OperationKey: "standard-code-pause-preview-0001", RequestedBy: "operator",
	})
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" {
		t.Fatalf("pause preview=%+v err=%v", preview, err)
	}
	beforeMode, _ := fixture.state.GetRunMode(t.Context(), fixture.run.ID)
	beforeProfile, _ := fixture.state.GetRunExecutionProfile(t.Context(), fixture.run.ID)
	beforeInteraction, _ := fixture.state.GetRunExecutionInteraction(t.Context(), fixture.run.ID)
	beforePermission, _ := fixture.state.GetRunExecutionPermission(t.Context(), fixture.run.ID)
	lease, err := fixture.state.AcquireRunExecutionLease(t.Context(),
		domain.AcquireRunExecutionLeaseRequest{RunID: fixture.run.ID,
			OwnerID: "standard-code-test-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	request := ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: fixture.run.ID,
		BackendIntent: "auto", Action: "pause_and_configure",
		OperationKey: "standard-code-pause-configure-0001", RequestedBy: "operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest,
	}
	waiting, err := service.Configure(t.Context(), request)
	if err != nil || waiting.Status != StandardCodeResultWaitingForPause ||
		waiting.DrydockReady || len(waiting.NextSteps) != 1 ||
		waiting.NextSteps[0] != StandardCodeNextWaitForQuiescence {
		t.Fatalf("waiting=%+v err=%v", waiting, err)
	}
	operation, found, err := fixture.state.GetStandardCodePresetOperation(t.Context(),
		runmutation.Fingerprint("standard_code_preset_operation.v1", request.OperationKey))
	if err != nil || !found || operation.Status != domain.StandardCodePresetWaitingForPause {
		t.Fatalf("persisted pause intent=%+v found=%t err=%v", operation, found, err)
	}
	afterMode, _ := fixture.state.GetRunMode(t.Context(), fixture.run.ID)
	afterProfile, _ := fixture.state.GetRunExecutionProfile(t.Context(), fixture.run.ID)
	afterInteraction, _ := fixture.state.GetRunExecutionInteraction(t.Context(), fixture.run.ID)
	afterPermission, _ := fixture.state.GetRunExecutionPermission(t.Context(), fixture.run.ID)
	if afterMode.ID != beforeMode.ID || afterProfile.ID != beforeProfile.ID ||
		afterInteraction.ID != beforeInteraction.ID || afterPermission.ID != beforePermission.ID {
		t.Fatal("waiting pause intent partially changed the preset tuple")
	}
	if _, _, err := fixture.state.ReleaseRunExecutionLease(t.Context(), lease.Lease); err != nil {
		t.Fatal(err)
	}
	changedRuntime := runtime
	changedRuntime.LocalBackendReady = false
	unavailableService, err := NewStandardCodePresetService(fixture.state,
		fixture.service, changedRuntime)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := unavailableService.Configure(t.Context(), request)
	if err != nil || unavailable.Status != StandardCodeResultWaitingForPause ||
		len(unavailable.BlockedBy) == 0 ||
		unavailable.BlockedBy[len(unavailable.BlockedBy)-1] !=
			CapabilityBlockerBackendNotReady {
		t.Fatalf("changed backend readiness=%+v err=%v", unavailable, err)
	}
	stillRunning, err := fixture.state.GetRun(t.Context(), fixture.run.ID)
	if err != nil || stillRunning.Status != domain.RunRunning {
		t.Fatalf("backend readiness change partially paused Run=%+v err=%v",
			stillRunning, err)
	}
	externallyPaused, err := NewRunService(fixture.state).Pause(t.Context(), fixture.run.ID)
	if err != nil || externallyPaused.Status != domain.RunPaused {
		t.Fatalf("external pause=%+v err=%v", externallyPaused, err)
	}
	configured, err := service.Configure(t.Context(), request)
	if err != nil || configured.Status != StandardCodeResultConfigured || !configured.Replayed ||
		configured.Run == nil || configured.Run.Status != domain.RunPaused {
		t.Fatalf("configured after quiescence=%+v err=%v", configured, err)
	}
}

func TestStandardCodePresetCreatesCodeRunForIncompatibleSurface(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-new-code-run")
	_, cyberRun, err := NewRunService(fixture.state).Create(t.Context(), CreateRunRequest{
		Goal: "cyber source run", Profile: "code", Surface: "cyber", Phase: "deliver",
		WorkspaceID: fixture.workspace.ID, Budget: domain.Budget{MaxTurns: 8},
		RequestedBy: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := CapabilityReadinessRuntime{
		RunControlEnabled: true, ExecutionPermissionControlEnabled: true,
		StandardCodePresetEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true,
		},
		LocalSandboxInstalled: true, LocalSandboxProven: true, LocalBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{
			commandruntimeadapter.SandboxedWorkspace(
				CommandRuntimeLocalSandboxBackend, "local-windows-lpac.v1",
				"standard-code-new-run-generation"),
		},
	}
	service, err := NewStandardCodePresetService(fixture.state, fixture.service, runtime)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: cyberRun.ID,
		BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-new-run-preview-0001", RequestedBy: "operator",
	})
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" ||
		preview.RunID != "" {
		t.Fatalf("new Run preview=%+v err=%v", preview, err)
	}
	configured, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: cyberRun.ID,
		BackendIntent: "auto", Action: "configure",
		OperationKey: "standard-code-new-run-configure-0001", RequestedBy: "operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest,
	})
	if err != nil || configured.Status != StandardCodeResultConfigured ||
		configured.RunID == cyberRun.ID || configured.Mode == nil ||
		configured.Mode.Surface != domain.ExecutionSurfaceCode ||
		configured.Mode.Phase != domain.ExecutionPhasePlan {
		t.Fatalf("incompatible Surface result=%+v err=%v", configured, err)
	}
	unchanged, err := fixture.state.GetRun(t.Context(), cyberRun.ID)
	if err != nil || unchanged.Status != domain.RunCreated {
		t.Fatalf("source Cyber Run changed: %+v err=%v", unchanged, err)
	}
}

func TestStandardCodePresetCommitFailureRollsBackCompleteTupleAndRetries(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "standard-code-fault.db")
	state, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	sourceRoot := newDrydockTestRepository(t, "standard-code-rollback")
	workspace := store.WorkspaceRecord{ID: "workspace-standard-code-rollback",
		Name: "workspace-standard-code-rollback", RootPath: sourceRoot}
	if err := state.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	_, run, err := NewRunService(state).Create(t.Context(), CreateRunRequest{
		Goal: "atomic rollback", Profile: "code", Surface: "code", Phase: "deliver",
		WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 8},
		RequestedBy: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = NewRunService(state).Start(t.Context(), run.ID)
	if err != nil || run.Status != domain.RunRunning {
		t.Fatalf("start rollback Run=%+v err=%v", run, err)
	}
	executor, err := repository.NewDrydockExecutor(filepath.Join(t.TempDir(), "drydocks"))
	if err != nil {
		t.Fatal(err)
	}
	drydocks, err := NewDrydockService(state, executor)
	if err != nil {
		t.Fatal(err)
	}
	runtime := CapabilityReadinessRuntime{
		RunControlEnabled: true, ExecutionPermissionControlEnabled: true,
		StandardCodePresetEnabled: true,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled: true,
		},
		LocalSandboxInstalled: true, LocalSandboxProven: true, LocalBackendReady: true,
		CommandRuntimeAdapters: []commandruntimeadapter.Identity{
			commandruntimeadapter.SandboxedWorkspace(
				CommandRuntimeLocalSandboxBackend, "local-windows-lpac.v1",
				"standard-code-rollback-generation"),
		},
	}
	service, err := NewStandardCodePresetService(state, drydocks, runtime)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Configure(t.Context(), ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: run.ID,
		BackendIntent: "auto", Action: "pause_and_configure",
		OperationKey: "standard-code-rollback-preview-0001", RequestedBy: "operator",
	})
	if err != nil || !preview.TrustRequired || preview.TrustDigest == "" {
		t.Fatalf("rollback preview=%+v err=%v", preview, err)
	}
	beforeMode, _ := state.GetRunMode(t.Context(), run.ID)
	beforeProfile, _ := state.GetRunExecutionProfile(t.Context(), run.ID)
	beforeInteraction, _ := state.GetRunExecutionInteraction(t.Context(), run.ID)
	beforePermission, _ := state.GetRunExecutionPermission(t.Context(), run.ID)
	beforeCDP, _ := state.GetRunBrowserCDPPermission(t.Context(), run.ID)
	raw, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`CREATE TRIGGER test_standard_code_permission_failure
		BEFORE INSERT ON run_execution_permission_snapshots
		WHEN NEW.mode = 'workspace_access' BEGIN
			SELECT RAISE(ABORT, 'injected Standard Code tuple failure');
		END`); err != nil {
		t.Fatal(err)
	}
	request := ConfigureStandardCodeRequest{
		Version: domain.StandardCodePresetProtocolVersion, RunID: run.ID,
		BackendIntent: "auto", Action: "pause_and_configure",
		OperationKey: "standard-code-rollback-configure-0001", RequestedBy: "operator",
		ConfirmWorkspaceTrust: true, ExpectedTrustDigest: preview.TrustDigest,
	}
	if _, err := service.Configure(t.Context(), request); err == nil {
		t.Fatal("injected Standard Code tuple failure succeeded")
	}
	afterMode, _ := state.GetRunMode(t.Context(), run.ID)
	afterProfile, _ := state.GetRunExecutionProfile(t.Context(), run.ID)
	afterInteraction, _ := state.GetRunExecutionInteraction(t.Context(), run.ID)
	afterPermission, _ := state.GetRunExecutionPermission(t.Context(), run.ID)
	afterCDP, _ := state.GetRunBrowserCDPPermission(t.Context(), run.ID)
	if afterMode.ID != beforeMode.ID || afterProfile.ID != beforeProfile.ID ||
		afterInteraction.ID != beforeInteraction.ID || afterPermission.ID != beforePermission.ID ||
		afterCDP.ID != beforeCDP.ID {
		t.Fatal("injected failure committed a partial Standard Code tuple")
	}
	afterRun, err := state.GetRun(t.Context(), run.ID)
	if err != nil || afterRun.Status != domain.RunRunning {
		t.Fatalf("injected failure partially paused Run=%+v err=%v", afterRun, err)
	}
	operation, found, err := state.GetStandardCodePresetOperation(t.Context(),
		runmutation.Fingerprint("standard_code_preset_operation.v1", request.OperationKey))
	if err != nil || !found || operation.Status != domain.StandardCodePresetWaitingForPause {
		t.Fatalf("recoverable operation=%+v found=%t err=%v", operation, found, err)
	}
	if _, err := raw.Exec(`DROP TRIGGER test_standard_code_permission_failure`); err != nil {
		t.Fatal(err)
	}
	retried, err := service.Configure(t.Context(), request)
	if err != nil || retried.Status != StandardCodeResultConfigured || !retried.Replayed ||
		retried.Run == nil || retried.Run.Status != domain.RunPaused {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
}

func TestStandardCodePresetNewRunGeneratedIdentityConvergesOnReplay(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "standard-code-create-replay")
	prepare := func() preparedRun {
		value, err := prepareRun(t.Context(), CreateRunRequest{
			Goal: "convergent create", Profile: string(domain.ProfileCode),
			Surface: string(domain.ExecutionSurfaceCode),
			Phase:   string(domain.ExecutionPhasePlan), WorkspaceID: fixture.workspace.ID,
			Interactive: true, Budget: domain.DefaultBudget(), RequestedBy: "operator",
		}, fixture.state.GetSession)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first, second := prepare(), prepare()
	if first.Run.ID == second.Run.ID || first.Mission.ID == second.Mission.ID {
		t.Fatal("prepared new Run graphs unexpectedly reused generated identities")
	}
	now := time.Now().UTC()
	operation := func(value preparedRun) domain.StandardCodePresetOperation {
		return domain.StandardCodePresetOperation{
			ProtocolVersion: domain.StandardCodePresetProtocolVersion,
			KeyDigest: runmutation.Fingerprint("standard_code_preset_operation.v1",
				"standard-code-create-replay-0001"),
			RequestFingerprint: runmutation.Fingerprint("standard_code_preset_request.v1",
				fixture.workspace.ID, "convergent create", "auto", "configure"),
			RunID: value.Run.ID, MissionID: value.Mission.ID,
			WorkspaceID: fixture.workspace.ID, Action: domain.StandardCodePresetConfigure,
			BackendIntent:   domain.StandardCodeBackendAuto,
			SelectedBackend: domain.StandardCodeSelectedLocal,
			SelectionReason: domain.StandardCodeReasonAutoLocalReady,
			Status:          domain.StandardCodePresetPreparing, RequestedBy: "operator",
			CreatedAt: now, UpdatedAt: now,
		}
	}
	created, replayed, err := fixture.state.CreateMissionRunWithStandardCodePresetIntent(
		t.Context(), first.Mission, first.Run, first.Mode, first.Session,
		first.InitialEvents, operation(first))
	if err != nil || replayed {
		t.Fatalf("first create=%+v replayed=%t err=%v", created, replayed, err)
	}
	replayedOperation, replayed, err :=
		fixture.state.CreateMissionRunWithStandardCodePresetIntent(t.Context(),
			second.Mission, second.Run, second.Mode, second.Session,
			second.InitialEvents, operation(second))
	if err != nil || !replayed || replayedOperation.RunID != first.Run.ID ||
		replayedOperation.MissionID != first.Mission.ID {
		t.Fatalf("generated identity replay=%+v replayed=%t err=%v",
			replayedOperation, replayed, err)
	}
	if _, err := fixture.state.GetRun(t.Context(), second.Run.ID); err == nil ||
		apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeNotFound {
		t.Fatalf("losing generated Run was persisted: %v", err)
	}
}
