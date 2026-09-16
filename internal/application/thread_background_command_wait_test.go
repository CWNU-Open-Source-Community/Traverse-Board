package application_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

func TestThreadBackgroundCommandWaitRetainsJobUntilExplicitPause(t *testing.T) {
	for _, pendingApproval := range []bool{false, true} {
		name := "ordinary_wait"
		if pendingApproval {
			name = "file_approval_requires_pause"
		}
		t.Run(name, func(t *testing.T) {
			st, run, _, request := toolBoundaryFixture(t, domain.Budget{MaxTurns: 8, MaxToolCalls: 12})
			ctx := t.Context()
			if _, err := application.NewRunExecutionProfileService(st).Change(ctx,
				application.ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "local",
					OperationKey: "background-local-profile", RequestedBy: "test_operator", Reason: "isolated managed job"}); err != nil {
				t.Fatal(err)
			}
			capabilities := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true}
			if _, err := application.NewThreadExecutionPermissionService(st, capabilities).Change(ctx,
				application.ChangeThreadExecutionPermissionRequest{ThreadID: request.ThreadID, Mode: "full_access", ConfirmDangerFullAccess: true,
					OperationKey: "background-full-access", RequestedBy: "test_operator", Reason: "isolated owned process"}); err != nil {
				t.Fatal(err)
			}
			manager, err := runner.NewPlatformCommandRuntimeManager(st, "thread-background-wait-owner")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := manager.Shutdown(stopCtx); err != nil {
					t.Error(err)
				}
			})
			commands, err := application.NewCommandRuntimeService(st, manager, capabilities)
			if err != nil {
				t.Fatal(err)
			}
			profile, script := runner.CommandRuntimeBash, "printf 'preview-ready\\n'; sleep 20"
			if runtime.GOOS == "windows" {
				profile, script = runner.CommandRuntimePowerShell, `[Console]::Out.WriteLine('preview-ready'); Start-Sleep -Seconds 20`
			}
			input := toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
				Action: toolgateway.CommandRuntimeActionStart, Commands: []runner.CommandRuntimeSpec{{
					Version: runner.CommandRuntimeProtocolVersion, Profile: profile, Script: script, WorkingDirectory: ".",
					Environment: []runner.CommandRuntimeEnvironment{}, StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
					TimeoutMilliseconds: 30000, Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
					Network: runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone,
					Purpose: "keep an owned preview process alive while awaiting the next user input",
				}}}
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			provider := &scriptedToolProvider{responses: []*llm.ChatResponse{toolResponse("background-start", "command_runtime", string(body))}}
			if pendingApproval {
				provider.responses = append(provider.responses, boundaryPropose("background-file-proposal"))
			}
			provider.responses = append(provider.responses, textResponse(rootActionResponse(domain.RootActionWait,
				"The managed process is started; waiting for your next choice.", "", "user input required")))
			router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
			router.RegisterProvider(provider)
			execution := application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()).
				WithExecutionPermissionCapabilities(capabilities).WithCommandRuntime(commands)
			turns := application.NewThreadTurnServiceWithExecutionCapabilities(st, application.NewRunLifecycleControlService(st), execution, capabilities)
			request.Content = "Start one bounded owned background process, then wait for my next input."
			result, err := turns.Execute(ctx, request)
			if err != nil || result.Execution == nil || result.Execution.Handoff.Result == nil {
				items, _ := st.ListRunEvents(ctx, result.Submission.Run.ID)
				for _, event := range items {
					if event.Type == events.ModelFailedEvent || event.Type == events.AgentTurnFailedEvent {
						t.Log(event.Type, event.PayloadJSON)
					}
				}
				t.Fatalf("interactive start/wait did not settle: err=%v", err)
			}
			jobs, err := st.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: run.ID, Limit: 10})
			if err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobRunning {
				t.Fatalf("one real managed process did not start: jobs=%+v err=%v", jobs, err)
			}
			ready := false
			for attempt := 0; attempt < 20; attempt++ {
				job, page, err := manager.Wait(ctx, jobs[0].ID, 250*time.Millisecond, 0, 4096)
				if err != nil || job.State.Terminal() {
					t.Fatalf("owned process failed before readiness: job=%+v err=%v", job, err)
				}
				for _, frame := range page.Frames {
					ready = ready || strings.Contains(frame.Text, "preview-ready")
				}
				if ready {
					break
				}
			}
			if !ready {
				t.Fatal("owned process did not produce its real readiness output")
			}
			checkpoint, found, err := st.GetSupervisorCheckpoint(ctx, run.ID)
			root, rootFound, rootErr := st.GetRootAgent(ctx, run.ID)
			if err != nil || !found || checkpoint.Phase != domain.SupervisorWaiting || checkpoint.AttemptID != "" ||
				rootErr != nil || !rootFound || root.Status != domain.AgentWaiting {
				t.Fatalf("model did not park after waiting: checkpoint=%+v root=%+v err=%v/%v", checkpoint, root, err, rootErr)
			}
			if pendingApproval {
				if result.Submission.Run.Status != domain.RunPaused {
					t.Fatalf("pending approval did not retain its paused control boundary: %+v", result.Submission.Run)
				}
			} else {
				if result.Submission.Run.Status != domain.RunRunning {
					t.Fatalf("ordinary model wait paused the Job's environment: %+v", result.Submission.Run)
				}
				lease, found, err := st.GetRunExecutionLease(ctx, run.ID)
				if err != nil || !found || lease.Status != domain.RunExecutionLeaseReleased {
					t.Fatalf("interactive model lease was not released: %+v found=%v err=%v", lease, found, err)
				}
				if stopped, err := commands.Reconcile(ctx); err != nil || stopped != 0 {
					t.Fatalf("ordinary model wait reaped the owned Job: stopped=%d err=%v", stopped, err)
				}
				job, err := manager.Get(ctx, jobs[0].ID)
				if err != nil || job.State != runner.CommandRuntimeJobRunning || len(provider.Requests()) != 2 {
					t.Fatalf("Job did not survive or model resumed without input: job=%+v calls=%d err=%v", job, len(provider.Requests()), err)
				}
				provider.responses = append(provider.responses, textResponse(rootActionResponse(domain.RootActionFinish,
					"This reply is complete; the same owned process remains available.", "reply complete", "")))
				request.Content, request.OperationKey = "End this reply without starting or stopping any process.", "background-followup-finish"
				finished, err := turns.Execute(ctx, request)
				if err != nil || finished.Submission.Run.ID != run.ID || finished.Submission.Run.Status != domain.RunRunning ||
					finished.Submission.Message.Status != domain.OperatorSteeringCommitted || len(provider.Requests()) != 3 {
					t.Fatalf("next explicit input did not resume the same waiting Thread: err=%v run=%s status=%s calls=%d", err,
						finished.Submission.Run.ID, finished.Submission.Run.Status, len(provider.Requests()))
				}
				if stopped, err := commands.Reconcile(ctx); err != nil || stopped != 0 {
					t.Fatalf("interactive Finish killed the retained process: stopped=%d err=%v", stopped, err)
				}
				jobsAfter, err := st.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: run.ID, Limit: 10})
				if err != nil || len(jobsAfter) != 1 || jobsAfter[0].ID != jobs[0].ID || jobsAfter[0].State != runner.CommandRuntimeJobRunning {
					t.Fatalf("ordinary followup duplicated or lost the original process: jobs=%+v err=%v", jobsAfter, err)
				}
				if _, err := application.NewRunLifecycleControlService(st).Apply(ctx, application.ControlRunLifecycleRequest{
					Version: domain.RunLifecycleControlProtocolVersion, RunID: run.ID, Action: domain.RunLifecyclePause,
					OperationKey: "background-explicit-pause", RequestedBy: "test_operator",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if stopped, err := commands.Reconcile(ctx); err != nil || stopped != 1 {
				t.Fatalf("actual pause did not reap the Job: stopped=%d err=%v", stopped, err)
			}
			job, err := manager.Get(ctx, jobs[0].ID)
			deadline := time.Now().Add(5 * time.Second)
			for err == nil && !job.State.Terminal() && time.Now().Before(deadline) {
				job, _, err = manager.Wait(ctx, jobs[0].ID, 250*time.Millisecond, job.OutputCursor, 4096)
			}
			if err != nil || job.State != runner.CommandRuntimeJobKilled || !job.TreeReaped {
				t.Fatalf("paused Run retained its process: job=%+v err=%v", job, err)
			}
			items, err := st.ListRunEvents(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			waits := 0
			for _, event := range items {
				if event.Type == events.AgentTurnCompletedEvent {
					var payload struct {
						Action    string `json:"lifecycle_action"`
						Requested string `json:"requested_lifecycle_action"`
						Retained  bool   `json:"background_jobs_retained"`
					}
					if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
						t.Fatal(err)
					}
					if payload.Action == "wait" && payload.Requested == "wait" && payload.Retained == !pendingApproval {
						waits++
					}
				}
			}
			if waits != 1 {
				t.Fatalf("durable model wait changed or lost the retention fact: %d", waits)
			}
		})
	}
}
