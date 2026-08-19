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

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func TestCommandRuntimeServiceRunsAndReplaysFencedForegroundCommand(t *testing.T) {
	ctx := context.Background()
	state, runRecord, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
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
		RootAgentID: root.ID, SessionID: runRecord.SessionID,
		WorkspaceID: "workspace-command-runtime-app", LeaseID: lease.LeaseID,
		CapabilityGeneration: strings.Repeat("a", 64),
		LeaseGeneration:      lease.Generation, RequestedBy: "run_supervisor",
		PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "high", Reason: "test"},
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
	stored, err := state.GetCommandRuntimeJob(ctx, result.Jobs[0].ID)
	if err != nil || stored.State != runner.CommandRuntimeJobCompleted ||
		!stored.TreeReaped || stored.OwnerID == "" || stored.LeaseGeneration != lease.Generation {
		t.Fatalf("durable job binding is incomplete: %#v err=%v", stored, err)
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
				commandRuntimeTestScope(runRecord, root, lease,
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
		commandRuntimeTestScope(runRecord, root, lease, "command-runtime-preflight"),
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
	callCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = service.ExecuteCommandRuntime(callCtx,
		commandRuntimeTestScope(runRecord, root, lease,
			"command-runtime-cancel-foreground"), input)
	if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
		t.Skipf("%s is unavailable: %v", profile, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("foreground cancellation error=%v", err)
	}
	jobs, err := state.ListCommandRuntimeJobs(ctx,
		runner.CommandRuntimeListFilter{RunID: runRecord.ID, Limit: 10})
	if err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobCancelled ||
		!jobs[0].TreeReaped {
		t.Fatalf("cancelled foreground job=%#v err=%v", jobs, err)
	}
}

func TestCommandRuntimeBackgroundJobSurvivesSupervisorLeaseTurnover(t *testing.T) {
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
	scope := commandRuntimeTestScope(runRecord, root, firstLease,
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
	scope = commandRuntimeTestScope(runRecord, root, acquired.Lease,
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
	if _, _, err := state.ReleaseRunExecutionLease(ctx, acquired.Lease); err != nil {
		t.Fatal(err)
	}
	runs := NewRunService(state)
	if _, err := runs.Pause(ctx, runRecord.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		ChangeRunExecutionPermissionRequest{RunID: runRecord.ID,
			Mode:         string(domain.RunExecutionPermissionConservative),
			OperationKey: "command-runtime-permission-drift-0001",
			RequestedBy:  "test_operator", Reason: "revoke managed command authority"}); err != nil {
		t.Fatal(err)
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

func commandRuntimeTestScope(runRecord domain.Run, root domain.AgentNode,
	lease domain.RunExecutionLease, operationKey string,
) toolgateway.CommandRuntimeContext {
	return toolgateway.CommandRuntimeContext{
		InvocationID: "command-runtime-invocation-" + operationKey,
		OperationKey: operationKey, RunID: runRecord.ID, RootAgentID: root.ID,
		SessionID: runRecord.SessionID, WorkspaceID: "workspace-command-runtime-app",
		CapabilityGeneration: strings.Repeat("a", 64),
		LeaseID:              lease.LeaseID, LeaseGeneration: lease.Generation,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{
			Allowed: true, Approval: toolgateway.ApprovalAutomatic,
			Risk: "high", Reason: "test"},
	}
}

func newCommandRuntimeTestRuntime(t *testing.T, ctx context.Context) (
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
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true}
	if _, err := NewRunExecutionPermissionService(state, capabilities).Change(ctx,
		ChangeRunExecutionPermissionRequest{RunID: runRecord.ID,
			Mode:         string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "command-runtime-permission-app-0001",
			RequestedBy:  "test_operator", Reason: "exercise managed commands",
			ConfirmDangerFullAccess: true}); err != nil {
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
