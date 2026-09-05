package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func TestCommandRuntimeMultiplexerRejectsDuplicateBackendAcrossGenerations(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "command-runtime-multiplexer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true}
	managers := make([]*runner.CommandRuntimeManager, 0, 2)
	for _, owner := range []string{"duplicate-backend-owner-a", "duplicate-backend-owner-b"} {
		manager, err := runner.NewPlatformCommandRuntimeManager(state, idgen.New(owner))
		if err != nil {
			t.Skipf("platform host command runtime is unavailable: %v", err)
		}
		managers = append(managers, manager)
	}
	defer func() {
		for _, manager := range managers {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = manager.Shutdown(shutdownCtx)
			cancel()
		}
	}()
	services := make([]*CommandRuntimeService, 0, len(managers))
	for _, manager := range managers {
		service, err := NewCommandRuntimeService(state, manager, capabilities)
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
	}
	if services[0].adapter.Generation == services[1].adapter.Generation {
		t.Fatal("test managers unexpectedly share an adapter generation")
	}
	if value, err := NewCommandRuntimeMultiplexer(services...); value != nil ||
		apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("duplicate backend generations were accepted: value=%v err=%v", value, err)
	}
}

func TestCommandRuntimeDockerCommandMapsOnlyFixedToolchains(t *testing.T) {
	for _, test := range []struct {
		executable string
		want       string
	}{
		{"go", "go"}, {"node", "node"}, {"python3", "python"}, {"cargo", "rust"},
	} {
		t.Run(test.executable, func(t *testing.T) {
			resolved := runner.CommandRuntimeResolvedSpec{
				Spec: runner.CommandRuntimeSpec{Profile: runner.CommandRuntimeProcess,
					WorkingDirectory: "src", TimeoutMilliseconds: 1501,
					Purpose: "exercise a fixed Docker toolchain"},
				ExecutablePath: filepath.Join("opt", "toolchains", test.executable),
				CanonicalArgv:  []string{"version"},
			}
			command, err := commandRuntimeDockerCommand(resolved)
			if err != nil || command.Validate() != nil || command.Toolchain != test.want ||
				command.TimeoutSeconds != 2 || command.WorkingDirectory != "src" ||
				len(command.Arguments) != 1 || command.Arguments[0] != "version" {
				t.Fatalf("Docker command=%#v err=%v", command, err)
			}
		})
	}
	for _, executable := range []string{"bash", "powershell.exe", "curl", "docker"} {
		resolved := runner.CommandRuntimeResolvedSpec{
			Spec: runner.CommandRuntimeSpec{Profile: runner.CommandRuntimeProcess,
				WorkingDirectory: ".", TimeoutMilliseconds: 1000,
				Purpose: "reject an arbitrary Docker executable"},
			ExecutablePath: filepath.Join("opt", "toolchains", executable),
			CanonicalArgv:  []string{},
		}
		if command, err := commandRuntimeDockerCommand(resolved); err == nil {
			t.Fatalf("arbitrary executable %q mapped to %#v", executable, command)
		}
	}
}

func TestCommandRuntimeAdapterReceiptsCannotMasqueradeAcrossIsolationGrades(t *testing.T) {
	host := commandruntimeadapter.HostUnsandboxed(strings.Repeat("a", 64))
	sandboxed := commandruntimeadapter.SandboxedWorkspace(
		CommandRuntimeLocalSandboxBackend, "windows-local-sandbox.v1",
		strings.Repeat("b", 64))
	forged := host
	forged.Kind = commandruntimeadapter.KindSandboxedWorkspace
	if host.SameBackend(sandboxed) || forged.Validate() == nil || forged.Executable() {
		t.Fatalf("adapter identities crossed isolation grades: host=%#v sandbox=%#v forged=%#v",
			host, sandboxed, forged)
	}
}

func TestCommandRuntimeDoesNotMapWorkspaceAccessToHostExecution(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "workspace-access-host-runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("workspace-access-host-owner"))
	if err != nil {
		t.Skipf("platform host command runtime is unavailable: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdownCtx)
	}()
	service, err := NewCommandRuntimeService(state, manager,
		domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true})
	if service != nil || apperror.CodeOf(err) != apperror.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "danger-full-access") {
		t.Fatalf("Workspace Access enabled the host command runtime: service=%v err=%v",
			service, err)
	}
}

func TestCommandRuntimeAdvertisementRequiresCurrentRunLease(t *testing.T) {
	ctx := context.Background()
	state, runRecord, _, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-advertisement-owner"))
	if err != nil {
		t.Skipf("platform host command runtime is unavailable: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdownCtx)
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	advertised, available, err := service.AdvertisedCommandRuntimeAdapter(ctx,
		runRecord.ID, domain.RunExecutionPermissionFullAccess)
	if err != nil || !available || !advertised.SameBackend(service.adapter) {
		t.Fatalf("active Run advertisement=%#v available=%t err=%v",
			advertised, available, err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	dynamicCapabilities := capabilities
	dynamicCapabilities.FullAccessRequiresRuntimeGrant = true
	dynamicCapabilities.RuntimeAuthority = authority
	dynamicService, err := NewCommandRuntimeService(state, manager, dynamicCapabilities)
	if err != nil {
		t.Fatal(err)
	}
	if advertised, available, err := dynamicService.AdvertisedCommandRuntimeAdapter(ctx,
		runRecord.ID, domain.RunExecutionPermissionFullAccess); err != nil || available ||
		advertised != (commandruntimeadapter.Identity{}) {
		t.Fatalf("cold persisted Full Access was advertised: adapter=%+v available=%t err=%v",
			advertised, available, err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.ActivateRunFullAccess(permission); err != nil {
		t.Fatal(err)
	}
	if advertised, available, err := dynamicService.AdvertisedCommandRuntimeAdapter(ctx,
		runRecord.ID, domain.RunExecutionPermissionFullAccess); err != nil || !available ||
		!advertised.SameBackend(dynamicService.adapter) {
		t.Fatalf("live exact Full Access was not advertised: adapter=%+v available=%t err=%v",
			advertised, available, err)
	}
	if _, _, err := state.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	advertised, available, err = service.AdvertisedCommandRuntimeAdapter(ctx,
		runRecord.ID, domain.RunExecutionPermissionFullAccess)
	if err != nil || available || advertised != (commandruntimeadapter.Identity{}) {
		t.Fatalf("released lease retained advertisement=%#v available=%t err=%v",
			advertised, available, err)
	}
}

func TestCommandRuntimeServiceRunsAndReplaysFencedForegroundCommand(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	root = ensureCommandRuntimeTestAgent(t, ctx, state, lease, root)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-test-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	profile := runner.CommandRuntimeBash
	script := "printf 'application-command-runtime\\n'"
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = "[Console]::Out.WriteLine('application-command-runtime')"
	}
	maxBytes := 4096
	input := toolgateway.CommandRuntimeInput{
		Version:       toolgateway.CommandRuntimeToolProtocolVersion,
		Action:        toolgateway.CommandRuntimeActionRun,
		FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
		Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
			WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 5000,
			Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network:             runner.CommandRuntimeNetworkDisabled,
			Credentials:         runner.CommandRuntimeCredentialsNone,
			Purpose:             "exercise the application command runtime",
		}},
	}
	scope := toolgateway.CommandRuntimeContext{
		InvocationID: "command-runtime-invocation-1",
		OperationKey: "command-runtime-operation-1", RunID: runRecord.ID,
		MissionID:   runRecord.MissionID,
		RootAgentID: root.ID, AgentID: root.ID, AgentAttemptID: root.ActiveAttemptID,
		SessionID:   runRecord.SessionID,
		WorkspaceID: "workspace-command-runtime-app", LeaseID: lease.LeaseID,
		CapabilityGeneration: service.adapter.Generation,
		LeaseGeneration:      lease.Generation, RequestedBy: "run_supervisor",
		PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "high", Reason: "test"},
		Adapter: service.adapter,
	}
	result, err := service.ExecuteCommandRuntime(ctx, scope, input)
	if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
		t.Skipf("%s is unavailable: %v", profile, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed || len(result.Jobs) != 1 ||
		result.Jobs[0].State != runner.CommandRuntimeJobCompleted ||
		result.Jobs[0].ExitCode == nil || *result.Jobs[0].ExitCode != 0 ||
		len(result.Pages) != 1 || len(result.Artifacts) != 1 ||
		!strings.Contains(result.Artifacts[0].Stdout, "application-command-runtime") {
		t.Fatalf("foreground result is incomplete: %#v", result)
	}
	replayed, err := service.ExecuteCommandRuntime(ctx, scope, input)
	if err != nil || !replayed.Replayed || len(replayed.Jobs) != 1 ||
		replayed.Jobs[0].ID != result.Jobs[0].ID ||
		replayed.Jobs[0].State != runner.CommandRuntimeJobCompleted {
		t.Fatalf("foreground replay duplicated or changed the job: %#v err=%v", replayed, err)
	}
	mode, err := state.GetRunMode(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := scope
	stale.InvocationID = "command-runtime-invocation-stale-permission"
	stale.OperationKey = "command-runtime-operation-stale-permission"
	stale.Surface, stale.Phase = mode.Surface, mode.Phase
	stale.Role, stale.Profile = root.Role, mode.Profile
	stale.PermissionMode = permission.Mode
	stale.ModeRevision = mode.Revision
	stale.PermissionRevision = permission.Revision + 1
	if _, err := service.ExecuteCommandRuntime(ctx, stale, input); err == nil ||
		apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale supplied permission revision did not fail closed: %v", err)
	}
	staleBackend := scope
	staleBackend.InvocationID = "command-runtime-invocation-stale-backend"
	staleBackend.OperationKey = "command-runtime-operation-stale-backend"
	staleBackend.CapabilityGeneration = strings.Repeat("0", 64)
	if _, err := service.ExecuteCommandRuntime(ctx, staleBackend, input); err == nil ||
		apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale supplied backend generation did not fail closed: %v", err)
	}
	stored, err := state.GetCommandRuntimeJob(ctx, result.Jobs[0].ID)
	if err != nil || stored.State != runner.CommandRuntimeJobCompleted ||
		!stored.TreeReaped || stored.OwnerID == "" || stored.LeaseGeneration != lease.Generation {
		t.Fatalf("durable job binding is incomplete: %#v err=%v", stored, err)
	}
}

func TestDebugInheritsAdvertisedAndExecutableHostCommandRuntime(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, capabilities :=
		newCommandRuntimeTestRuntimeWithPermission(t, ctx,
			domain.RunExecutionPermissionDebug)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-debug-owner"))
	if err != nil {
		t.Skipf("platform host command runtime is unavailable: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdownCtx)
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	advertised, available, err := service.AdvertisedCommandRuntimeAdapter(ctx,
		runRecord.ID, domain.RunExecutionPermissionDebug)
	if err != nil || !available || !advertised.SameBackend(service.adapter) {
		t.Fatalf("Debug host runtime advertisement=%+v available=%t err=%v",
			advertised, available, err)
	}
	profile := runner.CommandRuntimeBash
	script := "printf 'debug-command-runtime\\n'"
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = "[Console]::Out.WriteLine('debug-command-runtime')"
	}
	maxBytes := 4096
	result, err := service.ExecuteCommandRuntime(ctx,
		commandRuntimeTestScope(t, ctx, state, service, runRecord, root, lease,
			"debug-command-runtime-0001"),
		toolgateway.CommandRuntimeInput{
			Version:       toolgateway.CommandRuntimeToolProtocolVersion,
			Action:        toolgateway.CommandRuntimeActionRun,
			FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
			Commands: []runner.CommandRuntimeSpec{{
				Version: runner.CommandRuntimeProtocolVersion, Profile: profile,
				Script: script, WorkingDirectory: ".",
				Environment: []runner.CommandRuntimeEnvironment{},
				StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
				TimeoutMilliseconds: 5000,
				Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096,
					ArtifactBytes: 4096},
				Network:     runner.CommandRuntimeNetworkDisabled,
				Credentials: runner.CommandRuntimeCredentialsNone,
				Purpose:     "prove Debug inherits the stateless host command runtime",
			}},
		})
	if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
		t.Skipf("%s is unavailable: %v", profile, err)
	}
	if err != nil || len(result.Jobs) != 1 ||
		result.Jobs[0].State != runner.CommandRuntimeJobCompleted {
		t.Fatalf("Debug command runtime result=%+v err=%v", result, err)
	}
	stored, err := state.GetCommandRuntimeJob(ctx, result.Jobs[0].ID)
	if err != nil || stored.PermissionMode != domain.RunExecutionPermissionDebug {
		t.Fatalf("Debug command runtime durable binding=%+v err=%v", stored, err)
	}
}

func TestCommandRuntimeForegroundBatchHonorsOrderedFailurePolicy(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-batch-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	profile := runner.CommandRuntimeBash
	scripts := []string{
		"printf 'batch-first\\n'",
		"printf 'batch-failed\\n'; exit 7",
		"printf 'batch-third\\n'",
	}
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		scripts = []string{
			"[Console]::Out.WriteLine('batch-first')",
			"[Console]::Out.WriteLine('batch-failed'); exit 7",
			"[Console]::Out.WriteLine('batch-third')",
		}
	}
	for _, testCase := range []struct {
		policy   string
		wantJobs int
	}{
		{policy: toolgateway.CommandRuntimeFailFast, wantJobs: 2},
		{policy: toolgateway.CommandRuntimeContinue, wantJobs: 3},
	} {
		t.Run(testCase.policy, func(t *testing.T) {
			commands := make([]runner.CommandRuntimeSpec, 0, len(scripts))
			for index, script := range scripts {
				commands = append(commands, runner.CommandRuntimeSpec{
					Version: runner.CommandRuntimeProtocolVersion, Profile: profile,
					Script: script, WorkingDirectory: ".",
					Environment: []runner.CommandRuntimeEnvironment{},
					StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
					TimeoutMilliseconds: 3000,
					Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096,
						ArtifactBytes: 4096},
					Network:     runner.CommandRuntimeNetworkDisabled,
					Credentials: runner.CommandRuntimeCredentialsNone,
					Purpose:     "ordered batch command " + string(rune('1'+index)),
				})
			}
			maxBytes := 4096
			result, err := service.ExecuteCommandRuntime(ctx,
				commandRuntimeTestScope(t, ctx, state, service, runRecord, root, lease,
					"command-runtime-batch-"+testCase.policy),
				toolgateway.CommandRuntimeInput{
					Version:       toolgateway.CommandRuntimeToolProtocolVersion,
					Action:        toolgateway.CommandRuntimeActionRun,
					FailurePolicy: testCase.policy, MaxBytes: &maxBytes,
					Commands: commands,
				})
			if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
				t.Skipf("%s is unavailable: %v", profile, err)
			}
			if err != nil || len(result.Jobs) != testCase.wantJobs ||
				len(result.Artifacts) != testCase.wantJobs {
				t.Fatalf("batch result=%#v err=%v", result, err)
			}
			if result.Jobs[0].State != runner.CommandRuntimeJobCompleted ||
				result.Jobs[1].State != runner.CommandRuntimeJobFailed ||
				result.Jobs[1].ExitCode == nil || *result.Jobs[1].ExitCode != 7 {
				t.Fatalf("batch order/status is wrong: %#v", result.Jobs)
			}
			if testCase.wantJobs == 3 &&
				result.Jobs[2].State != runner.CommandRuntimeJobCompleted {
				t.Fatalf("continue policy skipped the third command: %#v", result.Jobs)
			}
		})
	}
}

func TestCommandRuntimeForegroundBatchPreflightsEveryCommand(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-preflight-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	profile := runner.CommandRuntimeBash
	script := "printf ran > command-runtime-preflight-marker"
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = "[IO.File]::WriteAllText('command-runtime-preflight-marker', 'ran')"
	}
	command := runner.CommandRuntimeSpec{
		Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
		WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
		StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
		TimeoutMilliseconds: 3000,
		Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
		Network:             runner.CommandRuntimeNetworkDisabled,
		Credentials:         runner.CommandRuntimeCredentialsNone,
		Purpose:             "prove batch preflight prevents partial execution",
	}
	invalid := command
	invalid.WorkingDirectory = "missing-directory"
	invalid.Purpose = "invalid second command must fail before the first starts"
	maxBytes := 4096
	result, err := service.ExecuteCommandRuntime(ctx,
		commandRuntimeTestScope(t, ctx, state, service, runRecord, root, lease, "command-runtime-preflight"),
		toolgateway.CommandRuntimeInput{
			Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action:  toolgateway.CommandRuntimeActionRun, FailurePolicy: toolgateway.CommandRuntimeFailFast,
			MaxBytes: &maxBytes, Commands: []runner.CommandRuntimeSpec{command, invalid},
		})
	if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
		t.Skipf("%s is unavailable: %v", profile, err)
	}
	if err == nil || len(result.Jobs) != 0 {
		t.Fatalf("invalid batch was partially executed: result=%#v err=%v", result, err)
	}
	workspace, err := state.GetWorkspaceByID(ctx, "workspace-command-runtime-app")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace.RootPath,
		"command-runtime-preflight-marker")); !os.IsNotExist(statErr) {
		t.Fatalf("first command ran before later-command preflight: %v", statErr)
	}
}

func TestCommandRuntimeForegroundCancellationReapsProcessTree(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-cancel-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	profile := runner.CommandRuntimeBash
	script := "sleep 30"
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = "Start-Sleep -Seconds 30"
	}
	maxBytes := 4096
	input := toolgateway.CommandRuntimeInput{
		Version:       toolgateway.CommandRuntimeToolProtocolVersion,
		Action:        toolgateway.CommandRuntimeActionRun,
		FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
		Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
			WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
			StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
			TimeoutMilliseconds: 20_000,
			Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
			Network:             runner.CommandRuntimeNetworkDisabled,
			Credentials:         runner.CommandRuntimeCredentialsNone,
			Purpose:             "prove foreground cancellation reaps the process tree",
		}},
	}
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	executionErr := make(chan error, 1)
	go func() {
		_, executeErr := service.ExecuteCommandRuntime(callCtx,
			commandRuntimeTestScope(t, ctx, state, service, runRecord, root, lease,
				"command-runtime-cancel-foreground"), input)
		executionErr <- executeErr
	}()
	startupDeadline := time.Now().Add(10 * time.Second)
	for {
		jobs, listErr := state.ListCommandRuntimeJobs(ctx,
			runner.CommandRuntimeListFilter{RunID: runRecord.ID, Limit: 10})
		if listErr != nil {
			t.Fatal(listErr)
		}
		// The durable running row is visible before Start installs its
		// process-local entry. Require both signals so cancellation exercises
		// foreground cleanup instead of racing the startup commit.
		if len(jobs) == 1 && jobs[0].State == runner.CommandRuntimeJobRunning &&
			manager.OwnsActiveJob(jobs[0]) {
			break
		}
		select {
		case executeErr := <-executionErr:
			if errors.Is(executeErr, runner.ErrCommandRuntimeUnavailable) {
				t.Skipf("%s is unavailable: %v", profile, executeErr)
			}
			t.Fatalf("foreground command returned before durable startup: jobs=%#v err=%v",
				jobs, executeErr)
		default:
		}
		if time.Now().After(startupDeadline) {
			t.Fatalf("foreground command did not reach durable owned running state: jobs=%#v",
				jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	var cancellationErr error
	select {
	case cancellationErr = <-executionErr:
		if !errors.Is(cancellationErr, context.Canceled) {
			t.Fatalf("foreground cancellation error=%v", cancellationErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("foreground cancellation did not return after durable startup")
	}
	durableDeadline := time.Now().Add(10 * time.Second)
	for {
		jobs, listErr := state.ListCommandRuntimeJobs(ctx,
			runner.CommandRuntimeListFilter{RunID: runRecord.ID, Limit: 10})
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(jobs) == 1 && jobs[0].State == runner.CommandRuntimeJobCancelled &&
			jobs[0].TreeReaped {
			break
		}
		if time.Now().After(durableDeadline) {
			t.Fatalf("foreground cancellation was not durably reaped: jobs=%#v execution_err=%v",
				jobs, cancellationErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCommandRuntimeBackgroundJobSurvivesTurnAndFailsClosedOnDurableDrift(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, firstLease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	originalWorkspace, err := state.GetWorkspaceByID(ctx, "workspace-command-runtime-app")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-turnover-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	profile := runner.CommandRuntimeBash
	script := `IFS= read -r line; printf 'cross-turn:%s\n' "$line"`
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = `$line = [Console]::In.ReadLine(); [Console]::Out.WriteLine("cross-turn:$line")`
	}
	scope := commandRuntimeTestScope(t, ctx, state, service, runRecord, root, firstLease,
		"command-runtime-start-turn-1")
	started, err := service.ExecuteCommandRuntime(ctx, scope,
		toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action: toolgateway.CommandRuntimeActionStart,
			Commands: []runner.CommandRuntimeSpec{{
				Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
				WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
				StdinPolicy: runner.CommandRuntimeStdinPipe, CloseInitialStdin: false,
				TimeoutMilliseconds: 5000,
				Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
				Network:             runner.CommandRuntimeNetworkDisabled,
				Credentials:         runner.CommandRuntimeCredentialsNone,
				Purpose:             "prove background ownership across Supervisor turns",
			}}})
	if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
		t.Skipf("%s is unavailable: %v", profile, err)
	}
	if err != nil || len(started.Jobs) != 1 ||
		started.Jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("background start=%#v err=%v", started, err)
	}
	jobID := started.Jobs[0].ID
	if _, _, err := state.ReleaseRunExecutionLease(ctx, firstLease); err != nil {
		t.Fatal(err)
	}
	if stopped, err := service.Reconcile(ctx); err != nil || stopped != 0 {
		t.Fatalf("turn release stopped owned background job: stopped=%d err=%v", stopped, err)
	}
	job, err := manager.Get(ctx, jobID)
	if err != nil || job.State != runner.CommandRuntimeJobRunning {
		t.Fatalf("background job did not survive turn release: %#v err=%v", job, err)
	}
	acquired, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: runRecord.ID,
			OwnerID: "command-runtime-test-worker-next-turn", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	scope = commandRuntimeTestScope(t, ctx, state, service, runRecord, root, acquired.Lease,
		"command-runtime-stdin-turn-2")
	stdin := "resumed"
	closeStdin := true
	if _, err := service.ExecuteCommandRuntime(ctx, scope,
		toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action: toolgateway.CommandRuntimeActionWriteStdin, JobID: jobID,
			Stdin: &stdin, CloseStdin: &closeStdin}); err != nil {
		t.Fatal(err)
	}
	cursor := uint64(0)
	maxBytes := 4096
	waitMilliseconds := 1000
	var output strings.Builder
	var result toolgateway.CommandRuntimeExecutionResult
	for attempt := 0; attempt < 5; attempt++ {
		scope.OperationKey = "command-runtime-wait-turn-2-" + string(rune('a'+attempt))
		result, err = service.ExecuteCommandRuntime(ctx, scope,
			toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
				Action: toolgateway.CommandRuntimeActionWait, JobID: jobID,
				Cursor: &cursor, MaxBytes: &maxBytes, WaitMilliseconds: &waitMilliseconds})
		if err != nil || len(result.Jobs) != 1 || len(result.Pages) != 1 {
			t.Fatalf("cross-turn wait=%#v err=%v", result, err)
		}
		for _, frame := range result.Pages[0].Frames {
			output.WriteString(frame.Text)
		}
		cursor = result.Pages[0].NextCursor
		if result.Jobs[0].State.Terminal() {
			break
		}
	}
	if result.Jobs[0].State != runner.CommandRuntimeJobCompleted {
		t.Fatalf("cross-turn job did not complete: %#v", result)
	}
	if !strings.Contains(output.String(), "cross-turn:resumed") {
		t.Fatalf("cross-turn output=%q", output.String())
	}
	scope.OperationKey = "command-runtime-root-drift-start"
	drifted, err := service.ExecuteCommandRuntime(ctx, scope,
		toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action: toolgateway.CommandRuntimeActionStart,
			Commands: []runner.CommandRuntimeSpec{{
				Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
				WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
				StdinPolicy: runner.CommandRuntimeStdinPipe, CloseInitialStdin: false,
				TimeoutMilliseconds: 5000,
				Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
				Network:             runner.CommandRuntimeNetworkDisabled,
				Credentials:         runner.CommandRuntimeCredentialsNone,
				Purpose:             "prove workspace root drift terminates owned jobs",
			}}})
	if err != nil || len(drifted.Jobs) != 1 ||
		drifted.Jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("root-drift start=%#v err=%v", drifted, err)
	}
	if err := state.SaveWorkspace(ctx, store.WorkspaceRecord{
		ID: "workspace-command-runtime-app", Name: "command-runtime-app",
		RootPath: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	if stopped, err := service.Reconcile(ctx); err != nil || stopped != 1 {
		t.Fatalf("root drift did not stop the owned job: stopped=%d err=%v", stopped, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, _, waitErr := manager.Wait(ctx, drifted.Jobs[0].ID,
			50*time.Millisecond, 0, 4096)
		if waitErr != nil {
			t.Fatal(waitErr)
		}
		if job.State.Terminal() {
			if job.State != runner.CommandRuntimeJobKilled || !job.TreeReaped {
				t.Fatalf("root-drift terminal state=%#v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("root-drift job did not terminate")
		}
	}
	if err := state.SaveWorkspace(ctx, originalWorkspace); err != nil {
		t.Fatal(err)
	}
	scope.OperationKey = "command-runtime-permission-drift-start"
	permissionDrift, err := service.ExecuteCommandRuntime(ctx, scope,
		toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action: toolgateway.CommandRuntimeActionStart,
			Commands: []runner.CommandRuntimeSpec{{
				Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
				WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
				StdinPolicy: runner.CommandRuntimeStdinPipe, CloseInitialStdin: false,
				TimeoutMilliseconds: 5000,
				Output:              runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
				Network:             runner.CommandRuntimeNetworkDisabled,
				Credentials:         runner.CommandRuntimeCredentialsNone,
				Purpose:             "prove permission drift terminates owned jobs",
			}}})
	if err != nil || len(permissionDrift.Jobs) != 1 ||
		permissionDrift.Jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("permission-drift start=%#v err=%v", permissionDrift, err)
	}
	oldPermission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs := NewRunService(state)
	if _, err := runs.Pause(ctx, runRecord.ID); err != nil {
		t.Fatal(err)
	}
	changedPermission, err := NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		ChangeRunExecutionPermissionRequest{RunID: runRecord.ID,
			Mode:         string(domain.RunExecutionPermissionConservative),
			OperationKey: "command-runtime-permission-drift-0001",
			RequestedBy:  "test_operator", Reason: "revoke managed command authority"})
	if err != nil {
		t.Fatal(err)
	}
	releasedLease, found, err := state.GetRunExecutionLease(ctx, runRecord.ID)
	if err != nil || !found || releasedLease.Status != domain.RunExecutionLeaseReleased ||
		changedPermission.Permission.Revision == oldPermission.Revision {
		t.Fatalf("permission change did not revoke old Job authority: permission=%+v lease=%+v found=%t err=%v",
			changedPermission.Permission, releasedLease, found, err)
	}
	if _, err := runs.Resume(ctx, runRecord.ID); err != nil {
		t.Fatal(err)
	}
	if stopped, err := service.Reconcile(ctx); err != nil || stopped != 1 {
		t.Fatalf("permission drift did not stop the owned job: stopped=%d err=%v", stopped, err)
	}
	permissionJob, _, err := manager.Wait(ctx, permissionDrift.Jobs[0].ID,
		time.Second, 0, 4096)
	if err != nil || permissionJob.State != runner.CommandRuntimeJobKilled ||
		!permissionJob.TreeReaped {
		t.Fatalf("permission-drift terminal state=%#v err=%v", permissionJob, err)
	}
}

func TestCommandRuntimeBindingBecomesStaleWhenRunningDowngradeCommits(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, _, _ := newCommandRuntimeTestRuntime(t, ctx)
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	if _, err := authority.ActivateRunFullAccess(permission); err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-revocation-owner"))
	if err != nil {
		t.Skipf("platform host command runtime is unavailable: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := state.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := state.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	rootSHA256, err := runner.CommandRuntimeWorkspaceRootSHA256(workspace.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := state.GetRunExecutionProfile(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	job := runner.CommandRuntimeJob{
		RunID: runRecord.ID, MissionID: runRecord.MissionID,
		SessionID: runRecord.SessionID, WorkspaceID: workspace.ID,
		RootAgentID: root.ID, WorkspaceRootSHA256: rootSHA256,
		ModeSnapshotID: mode.ID, ModeRevision: mode.Revision,
		ProfileSnapshotID: profile.ID, ProfileRevision: profile.Revision,
		PermissionSnapshotID: permission.ID, PermissionRevision: permission.Revision,
		PermissionMode: permission.Mode, Adapter: service.adapter,
	}
	if current, err := service.commandRuntimeJobBindingsCurrent(ctx, job); err != nil || !current {
		t.Fatalf("live Full binding current=%t err=%v", current, err)
	}
	transition, transitionErr := NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		ChangeRunExecutionPermissionRequest{RunID: runRecord.ID,
			Mode:         string(domain.RunExecutionPermissionConservative),
			OperationKey: "command-runtime-failed-revoke-0001", RequestedBy: "test_operator",
			Reason: "immediately lower permission while the Run is active"})
	if transitionErr != nil ||
		transition.Permission.Mode != domain.RunExecutionPermissionConservative {
		t.Fatalf("running permission downgrade=%+v err=%v", transition, transitionErr)
	}
	durable, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil || durable.ID == permission.ID ||
		durable.Mode != domain.RunExecutionPermissionConservative {
		t.Fatalf("running downgrade was not durable: %+v err=%v", durable, err)
	}
	if current, err := service.commandRuntimeJobBindingsCurrent(ctx, job); err != nil || current {
		t.Fatalf("revoked Full binding remained current=%t err=%v", current, err)
	}
}

func TestCommandRuntimeBindingBecomesStaleAcrossThreadFullReconfirmation(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, _ := newCommandRuntimeTestRuntime(t, ctx)
	if _, _, err := state.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	var err error
	runRecord, err = NewRunService(state).Pause(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	threadPermissions := NewThreadExecutionPermissionService(state, capabilities)
	_, err = threadPermissions.Change(ctx, ChangeThreadExecutionPermissionRequest{
		ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
		OperationKey: "command-runtime-thread-full-first-0001",
		RequestedBy:  "test_operator", Reason: "bind Full Access to the current task",
		ConfirmDangerFullAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runRecord, err = NewRunService(state).Resume(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil || !capabilities.AllowsSnapshot(permission) {
		t.Fatalf("initial Thread Full snapshot is not live: %+v err=%v", permission, err)
	}
	browserPermission, err := state.GetRunBrowserCDPPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-thread-reconfirmation-owner"))
	if err != nil {
		t.Skipf("platform host command runtime is unavailable: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := state.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := state.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	rootSHA256, err := runner.CommandRuntimeWorkspaceRootSHA256(workspace.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := state.GetRunExecutionProfile(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldJob := runner.CommandRuntimeJob{
		ID: "command-runtime-thread-full-old-job", State: runner.CommandRuntimeJobRunning,
		RunID: runRecord.ID, MissionID: runRecord.MissionID,
		SessionID: runRecord.SessionID, WorkspaceID: workspace.ID,
		RootAgentID: root.ID, WorkspaceRootSHA256: rootSHA256,
		ModeSnapshotID: mode.ID, ModeRevision: mode.Revision,
		ProfileSnapshotID: profile.ID, ProfileRevision: profile.Revision,
		PermissionSnapshotID: permission.ID, PermissionRevision: permission.Revision,
		PermissionMode: permission.Mode, Adapter: service.adapter,
	}
	if current, err := service.commandRuntimeJobBindingsCurrent(ctx, oldJob); err != nil || !current {
		t.Fatalf("old Full job binding current=%t err=%v", current, err)
	}
	if _, err := NewRunService(state).Pause(ctx, runRecord.ID); err != nil {
		t.Fatal(err)
	}

	reconfirmed, err := threadPermissions.Change(ctx,
		ChangeThreadExecutionPermissionRequest{
			ThreadID: threadRecord.ID, Mode: string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "command-runtime-thread-full-reconfirm-0001",
			RequestedBy:  "test_operator", Reason: "reconfirm Full Access for the current task",
			ConfirmDangerFullAccess: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	currentPermission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil || currentPermission.ID == permission.ID ||
		currentPermission.Revision <= permission.Revision ||
		!capabilities.AllowsSnapshot(currentPermission) {
		t.Fatalf("same-mode Full did not rotate and reactivate the Run snapshot: old=%+v current=%+v err=%v",
			permission, currentPermission, err)
	}
	currentBrowserPermission, err := state.GetRunBrowserCDPPermission(ctx, runRecord.ID)
	if err != nil || currentBrowserPermission.ID != browserPermission.ID ||
		currentBrowserPermission.Revision != browserPermission.Revision ||
		currentBrowserPermission.Mode != browserPermission.Mode {
		t.Fatalf("same-mode Full changed the independent CDP sub-permission: old=%+v current=%+v err=%v",
			browserPermission, currentBrowserPermission, err)
	}
	if reconfirmed.CurrentRunEffect == domain.ThreadExecutionPermissionPausedAndApplied {
		runRecord, err = NewRunService(state).Resume(ctx, runRecord.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if current, err := service.commandRuntimeJobBindingsCurrent(ctx, oldJob); err != nil || current {
		t.Fatalf("old Full job binding survived same-mode reconfirmation: current=%t err=%v",
			current, err)
	}
}

type commandRuntimeTerminalTransitionStore struct {
	CommandRuntimeStore
	running  runner.CommandRuntimeJob
	terminal runner.CommandRuntimeJob
	calls    int
}

func (s *commandRuntimeTerminalTransitionStore) GetCommandRuntimeJob(
	context.Context, string,
) (runner.CommandRuntimeJob, error) {
	s.calls++
	if s.calls == 1 {
		return s.running, nil
	}
	return s.terminal, nil
}

func TestCommandRuntimeReadableAuthorizationAllowsTerminalOwnershipTransition(t *testing.T) {
	running := runner.CommandRuntimeJob{ID: "command-runtime-terminal-transition",
		RunID: "run-terminal-transition", MissionID: "mission-terminal-transition",
		SessionID: "session-terminal-transition", WorkspaceID: "workspace-terminal-transition",
		RootAgentID: "agent-terminal-transition", State: runner.CommandRuntimeJobRunning}
	terminal := running
	terminal.State = runner.CommandRuntimeJobCompleted
	state := &commandRuntimeTerminalTransitionStore{running: running, terminal: terminal}
	service := &CommandRuntimeService{store: state}
	bindings := commandRuntimeBindings{
		run:       domain.Run{ID: running.RunID, SessionID: running.SessionID},
		mission:   domain.Mission{ID: running.MissionID},
		workspace: store.WorkspaceRecord{ID: running.WorkspaceID},
		root:      domain.AgentNode{ID: running.RootAgentID},
	}
	result, err := service.authorizeReadableJob(context.Background(), running.ID, bindings)
	if err != nil || result.State != runner.CommandRuntimeJobCompleted || state.calls != 3 {
		t.Fatalf("terminal transition authorization=%#v calls=%d err=%v", result, state.calls, err)
	}
}

func TestCommandRuntimeUIEvidenceCleanupReapsExactJobAfterLeaseRelease(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state,
		idgen.New("command-runtime-ui-cleanup-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown command runtime: %v", err)
		}
	}()
	service, err := NewCommandRuntimeService(state, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	profile := runner.CommandRuntimeBash
	script := `IFS= read -r line; printf 'unexpected:%s\n' "$line"`
	if runtime.GOOS == "windows" {
		profile = runner.CommandRuntimePowerShell
		script = `$line = [Console]::In.ReadLine(); [Console]::Out.WriteLine("unexpected:$line")`
	}
	identity := "ui-attempt-cleanup-test:application"
	scope := commandRuntimeTestScope(t, ctx, state, service, runRecord, root, lease, identity)
	scope.InvocationID = identity
	started, err := service.ExecuteCommandRuntime(ctx, scope,
		toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action: toolgateway.CommandRuntimeActionStart,
			Commands: []runner.CommandRuntimeSpec{{
				Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script,
				WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{},
				StdinPolicy: runner.CommandRuntimeStdinPipe, CloseInitialStdin: false,
				TimeoutMilliseconds: 10000,
				Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096,
					ArtifactBytes: 4096},
				Network:     runner.CommandRuntimeNetworkDisabled,
				Credentials: runner.CommandRuntimeCredentialsNone,
				Purpose:     "prove exact UI evidence cleanup survives lease release",
			}}})
	if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
		t.Skipf("%s is unavailable: %v", profile, err)
	}
	if err != nil || len(started.Jobs) != 1 ||
		started.Jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("UI cleanup Job start=%#v err=%v", started, err)
	}
	jobID := started.Jobs[0].ID
	if _, _, err := state.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteCommandRuntime(ctx, scope,
		toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
			Action: toolgateway.CommandRuntimeActionKill, JobID: jobID}); err == nil {
		t.Fatal("ordinary command authority killed a Job after its Run lease was released")
	}
	binding := uiEvidenceCommandCleanupBinding{JobID: jobID,
		InvocationID: identity, OperationKey: identity, RunID: runRecord.ID,
		MissionID: runRecord.MissionID, SessionID: runRecord.SessionID,
		WorkspaceID: "workspace-command-runtime-app", RootAgentID: root.ID,
		LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation}
	wrong := binding
	wrong.OperationKey = identity + "-other"
	if _, err := service.cleanupUIEvidenceJob(ctx, wrong); err == nil {
		t.Fatal("cleanup-only authority accepted a different operation identity")
	}
	active, err := manager.Get(ctx, jobID)
	if err != nil || active.State != runner.CommandRuntimeJobRunning {
		t.Fatalf("mismatched cleanup disturbed Job=%#v err=%v", active, err)
	}
	cleaned, err := service.cleanupUIEvidenceJob(ctx, binding)
	if err != nil || cleaned.State != runner.CommandRuntimeJobKilled ||
		!cleaned.TreeReaped {
		t.Fatalf("exact UI cleanup Job=%#v err=%v", cleaned, err)
	}
}

func commandRuntimeTestScope(t *testing.T, ctx context.Context, state *store.SQLiteStore,
	service *CommandRuntimeService,
	runRecord domain.Run, root domain.AgentNode,
	lease domain.RunExecutionLease, operationKey string,
) toolgateway.CommandRuntimeContext {
	t.Helper()
	root = ensureCommandRuntimeTestAgent(t, ctx, state, lease, root)
	return toolgateway.CommandRuntimeContext{
		InvocationID: "command-runtime-invocation-" + operationKey,
		OperationKey: operationKey, RunID: runRecord.ID,
		MissionID: runRecord.MissionID, RootAgentID: root.ID,
		AgentID: root.ID, AgentAttemptID: root.ActiveAttemptID,
		SessionID: runRecord.SessionID, WorkspaceID: "workspace-command-runtime-app",
		CapabilityGeneration: service.adapter.Generation,
		LeaseID:              lease.LeaseID, LeaseGeneration: lease.Generation,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{
			Allowed: true, Approval: toolgateway.ApprovalAutomatic,
			Risk: "high", Reason: "test"},
		Adapter: service.adapter,
	}
}

func ensureCommandRuntimeTestAgent(t *testing.T, ctx context.Context,
	state *store.SQLiteStore, lease domain.RunExecutionLease, root domain.AgentNode,
) domain.AgentNode {
	t.Helper()
	current, found, err := state.GetRootAgent(ctx, root.RunID)
	if err != nil || !found {
		t.Fatalf("root agent found=%t err=%v", found, err)
	}
	if current.ActiveAttemptID != "" {
		return current
	}
	turn, err := state.BeginSupervisorTurn(ctx, lease,
		"exercise the attributed Command Runtime")
	if err != nil {
		t.Fatal(err)
	}
	return turn.Agent
}

func newCommandRuntimeTestRuntime(t *testing.T, ctx context.Context) (
	*store.SQLiteStore, domain.Run, domain.AgentNode, domain.RunExecutionLease,
	domain.ExecutionPermissionRuntimeCapabilities,
) {
	return newCommandRuntimeTestRuntimeWithPermission(t, ctx,
		domain.RunExecutionPermissionFullAccess)
}

func newCommandRuntimeTestRuntimeWithPermission(t *testing.T, ctx context.Context,
	permissionMode domain.RunExecutionPermissionMode,
) (
	*store.SQLiteStore, domain.Run, domain.AgentNode, domain.RunExecutionLease,
	domain.ExecutionPermissionRuntimeCapabilities,
) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "command-runtime-application.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	workspaceRoot := t.TempDir()
	workspace := store.WorkspaceRecord{ID: "workspace-command-runtime-app",
		Name: "command-runtime-app", RootPath: workspaceRoot}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	runs := NewRunService(state)
	_, runRecord, err := runs.Create(ctx, CreateRunRequest{
		Goal: "execute an owned command", Profile: "code", WorkspaceID: workspace.ID,
		Budget: domain.Budget{MaxTurns: 4, MaxTokens: 1000, MaxToolCalls: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunExecutionProfileService(state).Change(ctx,
		ChangeRunExecutionProfileRequest{RunID: runRecord.ID, Profile: "local",
			OperationKey: "command-runtime-profile-app-0001", RequestedBy: "test_operator",
			Reason: "exercise local command runtime"}); err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
	}
	permissionRequest := ChangeRunExecutionPermissionRequest{RunID: runRecord.ID,
		Mode:         string(permissionMode),
		OperationKey: "command-runtime-permission-app-0001",
		RequestedBy:  "test_operator", Reason: "exercise managed commands"}
	if permissionMode == domain.RunExecutionPermissionDebug {
		permissionRequest.ConfirmDebugAccess = true
	} else {
		permissionRequest.ConfirmDangerFullAccess = true
	}
	if _, err := NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		permissionRequest); err != nil {
		t.Fatal(err)
	}
	runRecord, err = runs.Start(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := state.GetRootAgent(ctx, runRecord.ID)
	if err != nil || !found {
		t.Fatalf("root agent found=%t err=%v", found, err)
	}
	acquired, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{RunID: runRecord.ID,
			OwnerID: "command-runtime-test-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return state, runRecord, root, acquired.Lease, capabilities
}
