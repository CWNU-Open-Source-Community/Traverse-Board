package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/tools"
)

type operatorInterruptedPrepareStore struct{ *store.SQLiteStore }

func (s *operatorInterruptedPrepareStore) PrepareCommandRuntimeJobForAgent(ctx context.Context, job runner.CommandRuntimeJob, actor domain.AgentAttribution) (runner.CommandRuntimeJob, bool, error) {
	job, replayed, err := s.SQLiteStore.PrepareCommandRuntimeJobForAgent(ctx, job, actor)
	if err == nil && !replayed {
		return job, false, errors.New("fixture interruption after durable prepare")
	}
	return job, replayed, err
}

func TestOperatorCommandUnknownIntentSurvivesRestartWithoutResend(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newOperatorCommandFixture(t, mode)
			if err := f.manager.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			var err error
			f.manager, err = runner.NewPlatformCommandRuntimeManager(&operatorInterruptedPrepareStore{f.st}, idgen.New("operator-interruption"))
			if err != nil {
				t.Fatal(err)
			}
			f.service, err = NewCommandRuntimeService(f.st, f.manager, f.caps)
			if err != nil {
				t.Fatal(err)
			}
			request := f.request(t, true)
			if _, err = f.service.RunOperatorCommand(t.Context(), request); err == nil {
				t.Fatal("interruption fixture did not stop before dispatch")
			}
			f.noProcess(t)
			if err = f.manager.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = f.st.Close(); err != nil {
				t.Fatal(err)
			}
			f.st, err = store.Open(f.path)
			if err != nil {
				t.Fatal(err)
			}
			cold := &CommandRuntimeService{store: f.st}
			result, err := cold.RunOperatorCommand(t.Context(), request)
			if !errors.Is(err, runner.ErrCommandRuntimeUncertain) || !result.Replayed || result.Job.State != runner.CommandRuntimeJobPrepared {
				t.Fatalf("unknown outcome was not retained: %+v %v", result, err)
			}
			f.noProcess(t)
			jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.run.ID, Limit: 10})
			if err != nil || len(jobs) != 1 {
				t.Fatalf("unknown replay created another Job: %v %v", jobs, err)
			}
		})
	}
}

func TestOperatorCommandRunningProcessHonorsRevocationAndCancellation(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		for _, action := range []string{"revoke", "cancel"} {
			t.Run(string(mode)+"/"+action, func(t *testing.T) {
				f := newOperatorCommandFixture(t, mode)
				request := f.request(t, true)
				request.Command.Arguments = []string{"-e", `require('fs').appendFileSync('count.txt','1');setInterval(()=>{},1000);`}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := f.service.RunOperatorCommand(ctx, request); done <- err }()
				var job runner.CommandRuntimeJob
				deadline := time.Now().Add(8 * time.Second)
				for {
					jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.run.ID, Limit: 10})
					if err != nil {
						t.Fatal(err)
					}
					if len(jobs) == 1 && jobs[0].State == runner.CommandRuntimeJobRunning {
						if b, err := os.ReadFile(filepath.Join(f.root, "count.txt")); err == nil && string(b) == "1" {
							job = jobs[0]
							break
						}
					}
					if time.Now().After(deadline) {
						t.Fatal("native operator Job did not reach running state")
					}
					select {
					case err := <-done:
						t.Fatalf("command stopped before fixture action: %v", err)
					case <-time.After(10 * time.Millisecond):
					}
				}
				if action == "revoke" {
					f.caps.RuntimeAuthority.RevokeRun(f.run.ID)
					if n, err := f.service.Reconcile(t.Context()); err != nil || n != 1 {
						t.Fatalf("revoked operator Job was not reaped: %d %v", n, err)
					}
				} else {
					cancel()
				}
				select {
				case err := <-done:
					if action == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation lost: %v", err)
					}
				case <-time.After(8 * time.Second):
					t.Fatal("operator process did not stop")
				}
				stored, err := f.st.GetCommandRuntimeJob(t.Context(), job.ID)
				if err != nil || !stored.State.Terminal() || !stored.TreeReaped || stored.State == runner.CommandRuntimeJobCompleted {
					t.Fatalf("native tree still live: %+v %v", stored, err)
				}
				cold := &CommandRuntimeService{store: f.st}
				_, _ = cold.RunOperatorCommand(t.Context(), request)
				if b, _ := os.ReadFile(filepath.Join(f.root, "count.txt")); string(b) != "1" {
					t.Fatalf("closed process was resent: %q", b)
				}
			})
		}
	}
}

type operatorOriginalActionPolicy struct {
	policy.Checker
	observed []string
}

func (p *operatorOriginalActionPolicy) CheckToolCall(call tools.Call) policy.Decision {
	if call.Name == string(toolgateway.CommandRuntimeTool) {
		var input toolgateway.CommandRuntimeInput
		if err := json.Unmarshal([]byte(call.Args["payload"]), &input); err != nil {
			return policy.Decision{Allowed: false, Risk: "high", Reason: "invalid original request"}
		}
		p.observed = append(p.observed, input.Action)
		return policy.Decision{Allowed: input.Action != toolgateway.CommandRuntimeActionRun, Risk: "high", Reason: "fixture denies original foreground action"}
	}
	return p.Checker.CheckToolCall(call)
}
func TestOperatorCommandPolicyReceivesOriginalAction(t *testing.T) {
	f := newOperatorCommandFixture(t, domain.RunExecutionPermissionFull)
	checker := &operatorOriginalActionPolicy{Checker: policy.NewDefaultChecker()}
	f.service.SetCommandRuntimePolicy(checker)
	if _, err := f.service.RunOperatorCommand(t.Context(), f.request(t, false)); err == nil {
		t.Fatal("native start action concealed denied foreground action")
	}
	if len(checker.observed) == 0 || checker.observed[0] != toolgateway.CommandRuntimeActionRun {
		t.Fatalf("host policy saw wrong input: %v", checker.observed)
	}
	f.noProcess(t)
}

// The SQL update and its readback are separate operations. Cancel exactly after
// committing Running, so a cancelled read cannot silently orphan the owned Job.
type operatorCancelHandoffStore struct {
	*store.SQLiteStore
	cancel context.CancelFunc
}

func (s *operatorCancelHandoffStore) UpdateCommandRuntimeJob(ctx context.Context, job runner.CommandRuntimeJob, previous int64) (runner.CommandRuntimeJob, error) {
	updated, err := s.SQLiteStore.UpdateCommandRuntimeJob(ctx, job, previous)
	if err == nil && previous == 1 && updated.State == runner.CommandRuntimeJobRunning {
		s.cancel()
		return s.SQLiteStore.GetCommandRuntimeJob(ctx, updated.ID)
	}
	return updated, err
}

func TestOperatorCommandCancellationDuringRunningReceiptHandoff(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newOperatorCommandFixture(t, mode)
			if err := f.manager.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var err error
			f.manager, err = runner.NewPlatformCommandRuntimeManager(&operatorCancelHandoffStore{SQLiteStore: f.st, cancel: cancel}, idgen.New("operator-handoff"))
			if err != nil {
				t.Fatal(err)
			}
			f.service, err = NewCommandRuntimeService(f.st, f.manager, f.caps)
			if err != nil {
				t.Fatal(err)
			}
			request := f.request(t, true)
			request.Command.Arguments = []string{"-e", `setInterval(()=>{},1000);`}
			if _, err = f.service.RunOperatorCommand(ctx, request); !errors.Is(err, context.Canceled) {
				t.Fatalf("handoff lost cancellation: %v", err)
			}
			jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.run.ID, Limit: 10})
			if err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobCancelled || !jobs[0].TreeReaped {
				t.Fatalf("cancelled handoff did not settle the owned Job: %+v %v", jobs, err)
			}
			cold := &CommandRuntimeService{store: f.st}
			result, err := cold.RunOperatorCommand(t.Context(), request)
			if err != nil || !result.Replayed || result.Job.ID != jobs[0].ID || result.Job.State != runner.CommandRuntimeJobCancelled {
				t.Fatalf("cancelled handoff replay: %+v %v", result, err)
			}
		})
	}
}
