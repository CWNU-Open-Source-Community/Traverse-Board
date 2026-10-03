package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

// Delay only the durable terminal update. The real manager and native process
// still stop and reap normally, leaving the in-memory Stopping window visible.
type completedStartTerminalGate struct {
	*commandApprovalPrepareStore
	hold        atomic.Bool
	entered     chan struct{}
	released    chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func (s *completedStartTerminalGate) UpdateCommandRuntimeJob(ctx context.Context, job runner.CommandRuntimeJob, version int64) (runner.CommandRuntimeJob, error) {
	if job.State.Terminal() && s.hold.Load() {
		s.enteredOnce.Do(func() { close(s.entered) })
		select {
		case <-s.released:
		case <-ctx.Done():
			return runner.CommandRuntimeJob{}, ctx.Err()
		}
	}
	return s.SQLiteStore.UpdateCommandRuntimeJob(ctx, job, version)
}

func (s *completedStartTerminalGate) release() {
	s.releaseOnce.Do(func() { close(s.released) })
}

// Pause one authorization read after it has observed the durable Running row.
// Other reads remain available so stop, replay and output inspection can race it.
type completedStartReadGate struct {
	*store.SQLiteStore
	jobID       string
	paused      atomic.Bool
	entered     chan struct{}
	released    chan struct{}
	releaseOnce sync.Once
}

func (s *completedStartReadGate) GetCommandRuntimeJob(ctx context.Context, id string) (runner.CommandRuntimeJob, error) {
	job, err := s.SQLiteStore.GetCommandRuntimeJob(ctx, id)
	if err != nil || id != s.jobID || !s.paused.CompareAndSwap(false, true) {
		return job, err
	}
	close(s.entered)
	select {
	case <-s.released:
		return job, nil
	case <-ctx.Done():
		return runner.CommandRuntimeJob{}, ctx.Err()
	}
}

func (s *completedStartReadGate) release() {
	s.releaseOnce.Do(func() { close(s.released) })
}

type completedStartFixture struct {
	*commandApprovalFixture
	terminal *completedStartTerminalGate
	job      runner.CommandRuntimeJob
	scope    toolgateway.CommandRuntimeContext
	bindings commandRuntimeBindings
	resolved runner.CommandRuntimeResolvedSpec
}

func newCompletedStartFixture(t *testing.T, mode domain.RunExecutionPermissionMode) *completedStartFixture {
	t.Helper()
	f := newCommandApprovalFixture(t, mode, false)
	if err := f.manager.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	gate := &completedStartTerminalGate{commandApprovalPrepareStore: f.preparedStore,
		entered: make(chan struct{}), released: make(chan struct{})}
	t.Cleanup(gate.release)
	var err error
	f.manager, err = runner.NewPlatformCommandRuntimeManager(gate, idgen.New("completed-start-lifecycle"))
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = NewCommandRuntimeService(f.st, f.manager, f.caps)
	if err != nil {
		t.Fatal(err)
	}
	f.supervisor = NewRunSupervisor(f.st, nil, f.checker).WithExecutionPermissionCapabilities(f.caps).WithCommandRuntime(f.service)
	input := commandApprovalNativeInput(t, true)
	input.Commands[0].TimeoutMilliseconds = 30000
	input.Commands[0].Arguments[1] = `process.stdout.write('completed-start-output\n');require('fs').appendFileSync('starts.txt','1');` + input.Commands[0].Arguments[1]
	f.record(t, input, 1)
	waiting, err := f.resume(t)
	if err != nil {
		t.Fatal(err)
	}
	if waiting {
		f.decide(t, ApprovalControlApproveOnce)
		waiting, err = f.resume(t)
	}
	if err != nil || waiting {
		t.Fatalf("background start did not complete: waiting=%t err=%v", waiting, err)
	}
	jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10})
	if err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("expected one running native Job: %+v err=%v", jobs, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, readErr := os.ReadFile(filepath.Join(f.root, "starts.txt"))
		if readErr == nil && string(body) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native process did not publish its startup marker: %q err=%v", body, readErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	scope, pinned := f.scope(t)
	scope.InvocationID = jobs[0].InvocationID
	bindings, err := f.service.loadAuthorizedBindings(t.Context(), scope, false)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.service.normalizeCommandRuntimeSpec(pinned.Commands[0], bindings.rootPath)
	if err != nil {
		t.Fatal(err)
	}
	return &completedStartFixture{commandApprovalFixture: f, terminal: gate, job: jobs[0], scope: scope, bindings: bindings, resolved: resolved}
}

func (f *completedStartFixture) request(t *testing.T) runner.CommandRuntimeStartRequest {
	t.Helper()
	request, err := f.service.authorizedCommandStart(f.scope, f.bindings, f.scope.OperationKey, f.resolved)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func (f *completedStartFixture) raceStateRead(t *testing.T, ctx context.Context) (<-chan error, func()) {
	t.Helper()
	gate := &completedStartReadGate{SQLiteStore: f.st, jobID: f.job.ID,
		entered: make(chan struct{}), released: make(chan struct{})}
	f.service.store = gate
	request := f.request(t)
	result, done := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(done)
		result <- request.DispatchCheck(ctx, f.resolved)
	}()
	t.Cleanup(func() {
		gate.release()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("completed-start state recheck did not exit")
		}
	})
	select {
	case <-gate.entered:
	case err := <-result:
		t.Fatalf("completed-start check returned before its durable Job read: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("completed-start recheck did not reach its durable Job read")
	}
	return result, gate.release
}

func (f *completedStartFixture) assertReplay(t *testing.T, state runner.CommandRuntimeJobState) {
	t.Helper()
	request := f.request(t)
	checks := 0
	request.DispatchCheck = func(context.Context, runner.CommandRuntimeResolvedSpec) error {
		checks++
		return errors.New("replay attempted a new native dispatch")
	}
	job, replayed, err := f.manager.Start(t.Context(), request)
	if err != nil || !replayed || job.ID != f.job.ID || job.State != state || checks != 0 {
		t.Fatalf("read-only replay changed the Job or consumed dispatch authority: %+v replay=%t checks=%d err=%v", job, replayed, checks, err)
	}
	body, err := os.ReadFile(filepath.Join(f.root, "starts.txt"))
	if err != nil || string(body) != "1" {
		t.Fatalf("replay started another process: %q err=%v", body, err)
	}
}

func (f *completedStartFixture) assertReadable(t *testing.T, state runner.CommandRuntimeJobState) {
	t.Helper()
	if _, err := f.service.authorizeReadableJob(t.Context(), f.job.ID, f.bindings); err != nil {
		t.Fatalf("owned output became unreadable: %v", err)
	}
	job, page, err := f.manager.Wait(t.Context(), f.job.ID, 0, 0, 4096)
	var text strings.Builder
	for _, frame := range page.Frames {
		text.WriteString(frame.Text)
	}
	if err != nil || job.State != state || !strings.Contains(text.String(), "completed-start-output") {
		t.Fatalf("owned output/state was lost: state=%s output=%q err=%v", job.State, text.String(), err)
	}
}

func TestCompletedCommandStartRejectsStoppingAndStoppedJob(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		for _, kill := range []bool{false, true} {
			stop := "cancel"
			if kill {
				stop = "kill"
			}
			t.Run(string(mode)+"/"+stop, func(t *testing.T) {
				f := newCompletedStartFixture(t, mode)
				admitted := f.request(t)
				if err := admitted.DispatchCheck(t.Context(), f.resolved); err != nil {
					t.Fatalf("normal owned continuation was denied: %v", err)
				}
				f.assertReplay(t, runner.CommandRuntimeJobRunning)
				result, resumeRead := f.raceStateRead(t, t.Context())
				f.terminal.hold.Store(true)
				if _, err := f.manager.Stop(t.Context(), f.job.ID, kill, 0); err != nil {
					t.Fatal(err)
				}
				select {
				case <-f.terminal.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("stopped native process did not reach terminal persistence")
				}
				persisted, err := f.st.GetCommandRuntimeJob(t.Context(), f.job.ID)
				live, liveErr := f.manager.Get(t.Context(), f.job.ID)
				if err != nil || liveErr != nil || persisted.State != runner.CommandRuntimeJobRunning || live.State != runner.CommandRuntimeJobStopping {
					t.Fatalf("fixture missed the stale durable Running window: durable=%s live=%s err=%v/%v", persisted.State, live.State, err, liveErr)
				}
				resumeRead()
				if err := <-result; err == nil {
					t.Error("completed start accepted a stale Running read after stop")
				}
				if err := admitted.DispatchCheck(t.Context(), f.resolved); err == nil {
					t.Error("previously admitted continuation survived stop")
				}
				if err := f.request(t).DispatchCheck(t.Context(), f.resolved); err == nil {
					t.Error("fresh completed-start check authorized a Stopping Job")
				}
				f.assertReplay(t, runner.CommandRuntimeJobStopping)
				f.assertReadable(t, runner.CommandRuntimeJobStopping)
				f.terminal.release()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				terminal, err := f.service.waitForTerminal(ctx, f.job.ID)
				want := runner.CommandRuntimeJobCancelled
				if kill {
					want = runner.CommandRuntimeJobKilled
				}
				if err != nil || terminal.State != want || !terminal.TreeReaped {
					t.Fatalf("stopped process was not reaped with its original reason: %+v err=%v", terminal, err)
				}
				if err := f.request(t).DispatchCheck(t.Context(), f.resolved); err == nil {
					t.Error("completed start authorized a terminal Job")
				}
				f.assertReplay(t, want)
				f.assertReadable(t, want)
				jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10})
				if err != nil || len(jobs) != 1 {
					t.Fatalf("stop/replay created another Job: %+v err=%v", jobs, err)
				}
			})
		}
	}
}

func TestCompletedCommandStartStateRecheckObservesRequestCancellation(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCompletedStartFixture(t, mode)
			if err := f.request(t).DispatchCheck(t.Context(), f.resolved); err != nil {
				t.Fatalf("normal owned continuation was denied: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result, _ := f.raceStateRead(t, ctx)
			cancel()
			// The existing completed-source boundary may wrap a canceled store
			// read as a denial; it must never grant continuation for that request.
			if err := <-result; err == nil || ctx.Err() != context.Canceled {
				t.Fatalf("canceled in-flight state recheck was authorized: %v", err)
			}
			live, err := f.manager.Get(t.Context(), f.job.ID)
			if err != nil || live.State != runner.CommandRuntimeJobRunning {
				t.Fatalf("canceling a recheck changed the already owned Job lifetime: %+v err=%v", live, err)
			}
			f.assertReplay(t, runner.CommandRuntimeJobRunning)
		})
	}
}
