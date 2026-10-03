package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/tools"
)

type commandApprovalChecker struct {
	policy.Checker
	require, deny bool
}

func (c *commandApprovalChecker) CheckToolCall(call tools.Call) policy.Decision {
	if call.Name == string(toolgateway.CommandRuntimeTool) {
		return policy.Decision{Allowed: !c.deny, NeedsApproval: c.require, Risk: "high", Reason: "fixture command host policy"}
	}
	return c.Checker.CheckToolCall(call)
}

type commandApprovalPrepareStore struct {
	*store.SQLiteStore
	afterPrepare func()
}

func (s *commandApprovalPrepareStore) PrepareCommandRuntimeJobForAgent(ctx context.Context, job runner.CommandRuntimeJob, actor domain.AgentAttribution) (runner.CommandRuntimeJob, bool, error) {
	value, replayed, err := s.SQLiteStore.PrepareCommandRuntimeJobForAgent(ctx, job, actor)
	if err == nil && !replayed && s.afterPrepare != nil {
		s.afterPrepare()
	}
	return value, replayed, err
}

type commandApprovalFixture struct {
	st            *store.SQLiteStore
	preparedStore *commandApprovalPrepareStore
	supervisor    *RunSupervisor
	service       *CommandRuntimeService
	turn          domain.SupervisorTurn
	call          domain.SupervisorToolCall
	caps          domain.ExecutionPermissionRuntimeCapabilities
	checker       *commandApprovalChecker
	root          string
	path          string
	manager       *runner.CommandRuntimeManager
}

func newCommandApprovalFixture(t *testing.T, mode domain.RunExecutionPermissionMode, require bool) *commandApprovalFixture {
	t.Helper()
	f := &commandApprovalFixture{root: t.TempDir(), checker: &commandApprovalChecker{Checker: policy.NewDefaultChecker(), require: require}}
	var err error
	f.path = filepath.Join(t.TempDir(), "command-approval.db")
	f.st, err = store.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.st.Close() })
	workspace := store.WorkspaceRecord{ID: "command-approval-workspace", Name: "owned process fixture", RootPath: f.root}
	if err = f.st.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	runs := NewRunService(f.st)
	_, run, err := runs.Create(t.Context(), CreateRunRequest{Goal: "Test exact native process review", Profile: "code", Surface: "code", Phase: "deliver", WorkspaceID: workspace.ID,
		Budget: domain.Budget{MaxTurns: 4, MaxTokens: 10000, MaxToolCalls: 12}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewRunExecutionProfileService(f.st).Change(t.Context(), ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "local", OperationKey: "command-approval-profile", RequestedBy: "operator"}); err != nil {
		t.Fatal(err)
	}
	f.caps = domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}
	if mode != domain.RunExecutionPermissionAsk {
		if _, err = NewRunExecutionPermissionService(f.st, f.caps).Change(t.Context(), ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: string(mode), ConfirmFull: mode == domain.RunExecutionPermissionFull, OperationKey: "command-approval-mode", RequestedBy: "operator"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = runs.Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := f.st.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "command-approval-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	f.turn, err = f.st.BeginSupervisorTurn(t.Context(), lease.Lease, "")
	if err != nil {
		t.Fatal(err)
	}
	f.preparedStore = &commandApprovalPrepareStore{SQLiteStore: f.st}
	manager, err := runner.NewPlatformCommandRuntimeManager(f.preparedStore, idgen.New("command-approval-owner"))
	if err != nil {
		t.Fatal(err)
	}
	f.manager = manager
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if err := f.manager.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	f.service, err = NewCommandRuntimeService(f.st, manager, f.caps)
	if err != nil {
		t.Fatal(err)
	}
	f.supervisor = NewRunSupervisor(f.st, nil, f.checker).WithExecutionPermissionCapabilities(f.caps).WithCommandRuntime(f.service)
	return f
}

func commandApprovalNativeInput(t *testing.T, background bool) toolgateway.CommandRuntimeInput {
	t.Helper()
	executable, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("native Node fixture is required", err)
	}
	script := `require('fs').appendFileSync('count.txt','1');process.stdout.write('command-approval-result');`
	stdin, closed := runner.CommandRuntimeStdinClosed, true
	action := toolgateway.CommandRuntimeActionRun
	if background {
		action = toolgateway.CommandRuntimeActionStart
		stdin = runner.CommandRuntimeStdinPipe
		closed = false
		script = `process.stdin.on('data',b=>{require('fs').appendFileSync('stdin.txt',b);process.stdout.write('stdin-observed');});process.stdin.on('end',()=>process.exit(0));`
	}
	input := toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: action,
		Commands: []runner.CommandRuntimeSpec{{Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimeProcess, Executable: executable, Arguments: []string{"-e", script},
			WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{}, StdinPolicy: stdin, CloseInitialStdin: closed, TimeoutMilliseconds: 10000,
			Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096}, Network: runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone, Purpose: "write a local fixture marker exactly once"}}}
	if !background {
		n := 4096
		input.MaxBytes = &n
		input.FailurePolicy = toolgateway.CommandRuntimeFailFast
	}
	return input
}

func (f *commandApprovalFixture) record(t *testing.T, input toolgateway.CommandRuntimeInput, round int) {
	t.Helper()
	ctx := t.Context()
	permission, err := f.st.GetRunExecutionPermission(ctx, f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	advertised, err := f.supervisor.supervisorCommandRuntimeTools(ctx, f.turn.Run.ID, permission.Mode)
	if err != nil || len(advertised.Authority) == 0 {
		t.Fatalf("native command advertisement missing: %v", err)
	}
	raw, _ := json.Marshal(input)
	calls, err := prepareSupervisorToolCalls([]llm.ToolCall{{ID: "provider-command", Name: string(toolgateway.CommandRuntimeTool), Arguments: raw}}, f.turn.Run.ID, f.turn.Checkpoint.NextTurn, round,
		f.turn.Mode.Surface, f.turn.Mode.Phase, permission.Mode, false, false, supervisorToolOptions{CommandRuntime: advertised})
	if err != nil {
		t.Fatal(err)
	}
	calls, err = f.supervisor.bindCommandRuntimeCalls(ctx, calls)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, found, err := f.st.GetSupervisorCheckpoint(ctx, f.turn.Run.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	f.turn.Checkpoint = checkpoint
	attempt := llm.ModelAttempt{Number: round, ToolRound: round - 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "offline-command-fixture", Model: "fixture"}
	if _, err = f.st.RecordSupervisorModelStarted(ctx, f.turn.Checkpoint, attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	f.turn.Checkpoint, err = f.st.RecordSupervisorModelCompleted(ctx, f.turn.Checkpoint, attempt, llm.ChatResponse{Provider: attempt.Provider, Model: attempt.Model, ToolCalls: calls})
	if err != nil {
		t.Fatal(err)
	}
	rounds, err := f.st.ListSupervisorToolRounds(ctx, f.turn.Checkpoint)
	if err != nil || len(rounds) != round {
		t.Fatalf("recorded round missing: %v", err)
	}
	f.call = rounds[round-1].Calls[0]
}

func (f *commandApprovalFixture) resume(t *testing.T) (bool, error) {
	t.Helper()
	rounds, err := f.st.ListSupervisorToolRounds(t.Context(), f.turn.Checkpoint)
	if err != nil {
		return false, err
	}
	_, waiting, err := f.supervisor.resumeSupervisorTools(t.Context(), f.turn, rounds)
	return waiting, err
}
func (f *commandApprovalFixture) decide(t *testing.T, action ApprovalControlAction) {
	t.Helper()
	record, err := f.st.GetApprovalByProposal(t.Context(), f.call.CallID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker).Decide(t.Context(), DecideApprovalControlRequest{Version: ApprovalControlProtocolVersion,
		RunID: f.call.RunID, ApprovalID: record.ID, Action: action, OperationKey: "command-review-" + f.call.CallID, ReviewedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
}
func (f *commandApprovalFixture) scope(t *testing.T) (toolgateway.CommandRuntimeContext, toolgateway.CommandRuntimeInput) {
	t.Helper()
	a, err := commandruntimeadapter.DecodeAuthority(json.RawMessage(f.call.AuthorityJSON))
	if err != nil {
		t.Fatal(err)
	}
	input, _, err := toolgateway.NormalizeCommandRuntimePayload(json.RawMessage(f.call.PayloadJSON))
	if err != nil {
		t.Fatal(err)
	}
	return toolgateway.CommandRuntimeContext{InvocationID: "direct-command-operation", OperationKey: supervisorToolOperationKey(f.call.RunID, f.call.Turn, toolgateway.CommandRuntimeTool, json.RawMessage(f.call.PayloadJSON)),
		RunID: f.call.RunID, MissionID: f.turn.Mission.ID, RootAgentID: f.call.AgentID, AgentID: f.call.AgentID, AgentAttemptID: f.call.AgentAttemptID, SessionID: f.turn.Run.SessionID, WorkspaceID: f.turn.Mission.WorkspaceID,
		Surface: f.turn.Mode.Surface, Phase: f.turn.Mode.Phase, Role: domain.AgentRoleRoot, Profile: f.turn.Mode.Profile, ModeRevision: f.turn.Mode.Revision,
		PermissionMode: a.PermissionMode, PermissionRevision: a.PermissionRevision, PermissionSnapshotID: a.PermissionSnapshotID, PermissionGeneration: a.PermissionGeneration,
		PermissionRuntimeEpoch: a.PermissionRuntimeEpoch, RunAuthorizationFence: a.RunAuthorizationFence, SupervisorToolCallID: f.call.CallID,
		CapabilityGeneration: a.Adapter.Generation, LeaseID: f.turn.Checkpoint.LeaseID, LeaseGeneration: f.turn.Checkpoint.LeaseGeneration, RequestedBy: "run_supervisor", Adapter: a.Adapter,
		PolicyDecision: toolgateway.Decision{Allowed: true, Approval: toolgateway.ApprovalAutomatic, Risk: "high", Reason: "fixture passes upstream decision"}}, input
}
func (f *commandApprovalFixture) assertNoMarker(t *testing.T, name string) {
	t.Helper()
	if value, err := os.ReadFile(filepath.Join(f.root, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected process effect: %s %q %v", name, value, err)
	}
}

func TestCommandOperationApprovalRealNativeThreeModes(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCommandApprovalFixture(t, mode, false)
			f.record(t, commandApprovalNativeInput(t, false), 1)
			waiting, err := f.resume(t)
			if mode != domain.RunExecutionPermissionFull {
				if err != nil || !waiting {
					t.Fatalf("missing review: waiting=%t err=%v", waiting, err)
				}
				f.assertNoMarker(t, "count.txt")
				jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 20})
				if err != nil || len(jobs) != 0 {
					t.Fatal("review created a job", jobs, err)
				}
				f.decide(t, ApprovalControlApproveOnce)
				waiting, err = f.resume(t)
			}
			if err != nil || waiting {
				t.Fatalf("dispatch failed: %v waiting=%t", err, waiting)
			}
			call, _, err := f.st.GetSupervisorApprovalCall(t.Context(), f.call.RunID, f.call.CallID)
			if err != nil || call.Status != domain.SupervisorToolCompleted || !strings.Contains(call.ResultJSON, "command-approval-result") {
				t.Fatalf("missing process receipt: %+v %v", call, err)
			}
			if waiting, err = f.resume(t); err != nil || waiting {
				t.Fatal(err)
			}
			count, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
			if err != nil || string(count) != "1" {
				t.Fatalf("duplicate/missing execution: %q %v", count, err)
			}
			jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 20})
			if err != nil || len(jobs) != 1 || jobs[0].PermissionMode != mode || jobs[0].RunAuthorizationFence == 0 || jobs[0].PermissionRuntimeEpoch != f.caps.RuntimeAuthority.RuntimeEpoch() {
				t.Fatalf("lost job authority %+v %v", jobs, err)
			}
		})
	}
}

func TestCommandOperationApprovalNativeSinkRejectsBypass(t *testing.T) {
	for _, scenario := range []string{"pending_full", "denied_full", "payload_changed", "cancelled", "revoked", "new_epoch", "no_execution_start", "revoke_after_job_prepare", "lease_after_job_prepare", "policy_deny_after_review"} {
		t.Run(scenario, func(t *testing.T) {
			mode := domain.RunExecutionPermissionAsk
			if strings.HasSuffix(scenario, "_full") {
				mode = domain.RunExecutionPermissionFull
			}
			f := newCommandApprovalFixture(t, mode, true)
			f.record(t, commandApprovalNativeInput(t, false), 1)
			if waiting, err := f.resume(t); err != nil || !waiting {
				t.Fatal("missing review", err)
			}
			if scenario == "denied_full" {
				f.decide(t, ApprovalControlDeny)
			} else if scenario != "pending_full" {
				f.decide(t, ApprovalControlApproveOnce)
			}
			if scenario != "no_execution_start" {
				if _, err := f.st.RecordSupervisorToolExecutionStarted(t.Context(), f.turn.Checkpoint, f.call.CallID); err != nil {
					t.Fatal(err)
				}
			}
			scope, input := f.scope(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "payload_changed":
				input.Commands[0].Arguments[1] = `require('fs').appendFileSync('count.txt','2')`
			case "cancelled":
				cancel()
			case "revoked":
				f.caps.RuntimeAuthority.RevokeRun(f.call.RunID)
			case "new_epoch":
				f.service.capabilities.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
			case "revoke_after_job_prepare":
				f.preparedStore.afterPrepare = func() { f.caps.RuntimeAuthority.RevokeRun(f.call.RunID) }
			case "lease_after_job_prepare":
				f.preparedStore.afterPrepare = func() {
					lease, _, err := f.st.GetRunExecutionLease(t.Context(), f.call.RunID)
					if err != nil {
						t.Error(err)
						return
					}
					if _, _, err = f.st.ReleaseRunExecutionLease(t.Context(), lease); err != nil {
						t.Error(err)
					}
				}
			case "policy_deny_after_review":
				f.checker.deny = true
			}
			if _, err := f.service.ExecuteCommandRuntime(ctx, scope, input); err == nil {
				t.Fatal("native sink accepted invalid authority")
			}
			f.assertNoMarker(t, "count.txt")
		})
	}
}

func TestCommandOperationApprovalExpiredAndUnknownNeverResends(t *testing.T) {
	for _, scenario := range []string{"denied", "revoked", "started_unknown", "executable_changed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
			input := commandApprovalNativeInput(t, false)
			if scenario == "executable_changed" {
				original, err := exec.LookPath("whoami")
				if err != nil {
					t.Fatal("native identity fixture is required", err)
				}
				image, err := os.ReadFile(original)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "pinned-fixture.exe")
				if err := os.WriteFile(path, image, 0700); err != nil {
					t.Fatal(err)
				}
				input.Commands[0].Executable = path
			}
			f.record(t, input, 1)
			if waiting, err := f.resume(t); err != nil || !waiting {
				t.Fatal("missing review", err)
			}
			if scenario == "denied" {
				f.decide(t, ApprovalControlDeny)
			} else {
				f.decide(t, ApprovalControlApproveOnce)
			}
			want := "command_authority_expired"
			switch scenario {
			case "denied":
				want = "policy_denied"
			case "revoked":
				f.caps.RuntimeAuthority.RevokeRun(f.call.RunID)
			case "started_unknown":
				want = "outcome_unknown"
				if _, err := f.st.RecordSupervisorToolExecutionStarted(t.Context(), f.turn.Checkpoint, f.call.CallID); err != nil {
					t.Fatal(err)
				}
			case "executable_changed":
				// Keep the native header valid: the pinned image hash must reject
				// the changed bytes before an OS launch is possible.
				file, err := os.OpenFile(input.Commands[0].Executable, os.O_APPEND|os.O_WRONLY, 0700)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString("changed fixture executable bytes")
				if err = errors.Join(err, file.Close()); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if waiting, err := f.resume(t); err != nil || waiting {
					t.Fatalf("recovery failed %t %v", waiting, err)
				}
			}
			call, _, err := f.st.GetSupervisorApprovalCall(t.Context(), f.call.RunID, f.call.CallID)
			if err != nil || call.ErrorCode != want {
				t.Fatalf("lost negative receipt %+v %v", call, err)
			}
			f.assertNoMarker(t, "count.txt")
		})
	}
}

func TestCommandOperationApprovalStdinHasIndependentConsent(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	f.record(t, commandApprovalNativeInput(t, true), 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal("missing launch review", err)
	}
	f.decide(t, ApprovalControlApproveOnce)
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal("launch failed", err)
	}
	jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 20})
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	data, closeAfter := "exact reviewed stdin", true
	f.record(t, toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionWriteStdin, JobID: jobs[0].ID, Stdin: &data, CloseStdin: &closeAfter}, 2)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal("launch approval leaked to stdin", err)
	}
	f.assertNoMarker(t, "stdin.txt")
	f.decide(t, ApprovalControlApproveOnce)
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal("stdin failed", err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal("stdin replay failed", err)
	}
	job, err := f.service.waitForTerminal(t.Context(), jobs[0].ID)
	if err != nil || job.State != runner.CommandRuntimeJobCompleted {
		t.Fatal(job, err)
	}
	value, err := os.ReadFile(filepath.Join(f.root, "stdin.txt"))
	if err != nil || string(value) != data {
		t.Fatalf("stdin changed or duplicated: %q %v", value, err)
	}
}

func TestCommandOperationApprovalColdDatabaseNeverReactivatesOrResends(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		for _, state := range []string{"pending", "approved", "started_unknown"} {
			t.Run(string(mode)+"/"+state, func(t *testing.T) {
				f := newCommandApprovalFixture(t, mode, true)
				f.record(t, commandApprovalNativeInput(t, false), 1)
				if waiting, err := f.resume(t); err != nil || !waiting {
					t.Fatal("missing persisted review", err)
				}
				if state != "pending" {
					f.decide(t, ApprovalControlApproveOnce)
				}
				if state == "started_unknown" {
					if _, err := f.st.RecordSupervisorToolExecutionStarted(t.Context(), f.turn.Checkpoint, f.call.CallID); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.manager.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := f.st.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				f.st, err = store.Open(f.path)
				if err != nil {
					t.Fatal(err)
				}
				f.preparedStore.SQLiteStore = f.st
				f.caps.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
				f.manager, err = runner.NewPlatformCommandRuntimeManager(f.preparedStore, idgen.New("command-cold-owner"))
				if err != nil {
					t.Fatal(err)
				}
				f.service, err = NewCommandRuntimeService(f.st, f.manager, f.caps)
				if err != nil {
					t.Fatal(err)
				}
				f.supervisor = NewRunSupervisor(f.st, nil, f.checker).WithExecutionPermissionCapabilities(f.caps).WithCommandRuntime(f.service)
				for i := 0; i < 2; i++ {
					if waiting, err := f.resume(t); err != nil || waiting {
						t.Fatal("cold recovery did not settle", err)
					}
				}
				call, _, err := f.st.GetSupervisorApprovalCall(t.Context(), f.call.RunID, f.call.CallID)
				want := "command_authority_expired"
				if state == "started_unknown" {
					want = "outcome_unknown"
				}
				if err != nil || call.ErrorCode != want {
					t.Fatalf("cold receipt %+v %v", call, err)
				}
				if jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10}); err != nil || len(jobs) != 0 {
					t.Fatal("cold recovery created a process", jobs, err)
				}
				f.assertNoMarker(t, "count.txt")
			})
		}
	}
}

func TestCommandOperationApprovalWithoutRuntimeAuthorityStillRequiresReview(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCommandApprovalFixture(t, mode, false)
			f.caps.RuntimeAuthority = nil
			f.caps.FullAccessRequiresRuntimeGrant = false
			f.service.capabilities = f.caps
			f.supervisor.WithExecutionPermissionCapabilities(f.caps)
			f.record(t, commandApprovalNativeInput(t, false), 1)
			if waiting, err := f.resume(t); err != nil || !waiting {
				t.Fatal("missing exact review", err)
			}
			f.assertNoMarker(t, "count.txt")
			f.decide(t, ApprovalControlApproveOnce)
			if waiting, err := f.resume(t); err != nil || waiting {
				t.Fatal("reviewed native action failed", err)
			}
			value, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
			if err != nil || string(value) != "1" {
				t.Fatal("missing native action", string(value), err)
			}
			jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10})
			if err != nil || len(jobs) != 1 || jobs[0].RunAuthorizationFence != 0 || jobs[0].PermissionRuntimeEpoch != "" {
				t.Fatal("invented runtime revocation proof", jobs, err)
			}
		})
	}
}

func TestCommandOperationApprovalRevocationReapsOriginalJob(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCommandApprovalFixture(t, mode, false)
			f.record(t, commandApprovalNativeInput(t, true), 1)
			waiting, err := f.resume(t)
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				f.decide(t, ApprovalControlApproveOnce)
				waiting, err = f.resume(t)
			}
			if err != nil || waiting {
				t.Fatal("native job did not start", err)
			}
			jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10})
			if err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobRunning {
				t.Fatal(jobs, err)
			}
			f.caps.RuntimeAuthority.RevokeRun(f.call.RunID)
			if count, err := f.service.Reconcile(t.Context()); err != nil || count != 1 {
				t.Fatal("revoked job was not reconciled", count, err)
			}
			job, err := f.service.waitForTerminal(t.Context(), jobs[0].ID)
			if err != nil || !job.State.Terminal() || !job.TreeReaped {
				t.Fatal("process tree was not reaped", job, err)
			}
			permission, err := f.st.GetRunExecutionPermission(t.Context(), f.call.RunID)
			if err != nil || permission.Mode != mode || permission.ID != jobs[0].PermissionSnapshotID {
				t.Fatal("test revoked a snapshot instead of the runtime fence", permission, err)
			}
			f.assertNoMarker(t, "stdin.txt")
		})
	}
}

func TestCommandOperationApprovalLifecycleCannotEraseExistingReview(t *testing.T) {
	for _, state := range []string{"pending", "denied", "host_denied"} {
		t.Run(state, func(t *testing.T) {
			f := newCommandApprovalFixture(t, domain.RunExecutionPermissionFull, true)
			f.record(t, toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionList}, 1)
			if waiting, err := f.resume(t); err != nil || !waiting {
				t.Fatal("missing lifecycle review", err)
			}
			if state == "denied" {
				f.decide(t, ApprovalControlDeny)
			}
			if state == "host_denied" {
				f.decide(t, ApprovalControlApproveOnce)
				f.checker.deny = true
			}
			f.checker.require = false
			if _, err := f.st.RecordSupervisorToolExecutionStarted(t.Context(), f.turn.Checkpoint, f.call.CallID); err != nil {
				t.Fatal(err)
			}
			scope, input := f.scope(t)
			if _, err := f.service.ExecuteCommandRuntime(t.Context(), scope, input); err == nil {
				t.Fatal("native lifecycle sink ignored existing review or current denial")
			}
		})
	}
}
