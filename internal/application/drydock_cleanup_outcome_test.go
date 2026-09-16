package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/store"
)

type cleanupOutcomeExecutor struct {
	*repository.DrydockExecutor
	plan    func(context.Context, string, string, string) (gitadvanced.Preview, error)
	execute func(context.Context, string, gitadvanced.Preview) (gitadvanced.Receipt, error)
}

func (e cleanupOutcomeExecutor) PlanRemove(ctx context.Context, root, name, id string) (gitadvanced.Preview, error) {
	if e.plan != nil {
		return e.plan(ctx, root, name, id)
	}
	return e.DrydockExecutor.PlanRemove(ctx, root, name, id)
}

func (e cleanupOutcomeExecutor) ExecuteRemove(ctx context.Context, root string, preview gitadvanced.Preview) (gitadvanced.Receipt, error) {
	if e.execute != nil {
		return e.execute(ctx, root, preview)
	}
	return e.DrydockExecutor.ExecuteRemove(ctx, root, preview)
}

func TestDrydockCleanupConfirmsAbsenceAfterActualRemoveReturnsError(t *testing.T) {
	f := newDrydockApplicationFixture(t, "cleanup post remove observation error")
	created := mustCreateDrydock(t, f)
	request := DrydockCleanupRequest{RunID: f.run.ID, ExpectedGeneration: created.Generation,
		OperationKey: "cleanup-post-remove-failure", RequestedBy: "operator", Confirm: true}
	var gitReceiptID string
	executor := cleanupOutcomeExecutor{DrydockExecutor: f.executor, execute: func(ctx context.Context, root string, preview gitadvanced.Preview) (gitadvanced.Receipt, error) {
		receipt, err := f.executor.ExecuteRemove(ctx, root, preview)
		if err != nil || receipt.Status != gitadvanced.ReceiptSucceeded {
			return receipt, errors.Join(err, errors.New("real Git removal did not succeed"))
		}
		gitReceiptID = receipt.ID
		// The real worktree is already removed. AdvancedExecutor can return a
		// failure here when its subsequent repository observation fails.
		receipt.Status = gitadvanced.ReceiptFailed
		receipt.ErrorCode = gitadvanced.FailureGit
		receipt.ErrorSummary = "injected post-removal repository observation failure"
		return receipt, errors.New(receipt.ErrorSummary)
	}}
	result, err := f.service.cleanup(t.Context(), request, executor)
	if err != nil || result.Preserved || result.Workspace.State != drydock.StateCleaned || result.Receipt.Outcome != drydock.OutcomeSucceeded {
		t.Fatalf("removed directory was not truthfully confirmed: result=%+v err=%v", result, err)
	}
	if gitReceiptID == "" || result.Receipt.GitReceiptID != gitReceiptID || !strings.Contains(result.Receipt.Summary, "cleanup reported an error") {
		t.Fatalf("original Git error attribution was lost: %+v", result.Receipt)
	}
	assertCleanupOutcomePersisted(t, f, created, request, result)
}

func TestDrydockCleanupUnconfirmedFailureRetainsReservationForSameKey(t *testing.T) {
	f := newDrydockApplicationFixture(t, "cleanup present after error")
	created := mustCreateDrydock(t, f)
	request := DrydockCleanupRequest{RunID: f.run.ID, ExpectedGeneration: created.Generation,
		OperationKey: "cleanup-present-retry-original", RequestedBy: "operator", Confirm: true}
	planFailure := errors.New("injected preflight failure before removal")
	executeFailure := errors.New("injected execution failure before removal")
	for _, executor := range []cleanupOutcomeExecutor{
		{DrydockExecutor: f.executor, plan: func(context.Context, string, string, string) (gitadvanced.Preview, error) {
			return gitadvanced.Preview{}, planFailure
		}},
		{DrydockExecutor: f.executor, execute: func(context.Context, string, gitadvanced.Preview) (gitadvanced.Receipt, error) {
			return gitadvanced.Receipt{}, executeFailure
		}},
	} {
		result, err := f.service.cleanup(t.Context(), request, executor)
		if err == nil || result.Receipt.ID != "" || result.Preserved {
			t.Fatalf("uncertain cleanup was sealed: result=%+v err=%v", result, err)
		}
		pending, pendingErr := f.state.HasPendingDrydockCleanup(t.Context(), created.ID)
		if pendingErr != nil || !pending {
			t.Fatalf("missing reservation: %t %v", pending, pendingErr)
		}
		stored, found, getErr := f.state.GetDrydock(t.Context(), created.ID)
		if getErr != nil || !found || stored.Generation != created.Generation || stored.State != created.State || stored.LastCheckpointID != created.LastCheckpointID {
			t.Fatalf("uncertain cleanup changed durable state: %+v %v", stored, getErr)
		}
		if _, statErr := os.Stat(created.Path); statErr != nil {
			t.Fatal(statErr)
		}
	}
	changed := request
	changed.OperationKey = "cleanup-must-not-release-fence"
	if _, err := f.service.Cleanup(t.Context(), changed); err == nil {
		t.Fatal("another operation took over an unresolved removal")
	}
	result, err := f.service.Cleanup(t.Context(), request)
	if err != nil || result.Preserved || result.Workspace.State != drydock.StateCleaned {
		t.Fatalf("original request could not recover: %+v %v", result, err)
	}
	assertCleanupOutcomePersisted(t, f, created, request, result)
}

func TestDrydockCleanupConcurrentSameKeyReturnsPersistedOutcome(t *testing.T) {
	f := newDrydockApplicationFixture(t, "cleanup concurrent same intent")
	created := mustCreateDrydock(t, f)
	second, err := store.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	other, err := NewDrydockService(second, f.executor)
	if err != nil {
		t.Fatal(err)
	}
	request := DrydockCleanupRequest{RunID: f.run.ID, ExpectedGeneration: created.Generation,
		OperationKey: "cleanup-concurrent-same-intent", RequestedBy: "operator", Confirm: true}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	planning, removed, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	type outcome struct {
		result DrydockCleanupResult
		err    error
	}
	lateResult := make(chan outcome, 1)
	late := cleanupOutcomeExecutor{DrydockExecutor: f.executor, plan: func(ctx context.Context, root, name, id string) (gitadvanced.Preview, error) {
		close(planning)
		select {
		case <-removed:
		case <-ctx.Done():
			return gitadvanced.Preview{}, ctx.Err()
		}
		return f.executor.PlanRemove(ctx, root, name, id)
	}}
	go func() {
		result, err := f.service.cleanup(ctx, request, late)
		lateResult <- outcome{result, err}
		close(settled)
	}()
	select {
	case <-planning:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	leading := cleanupOutcomeExecutor{DrydockExecutor: f.executor, execute: func(ctx context.Context, root string, preview gitadvanced.Preview) (gitadvanced.Receipt, error) {
		receipt, err := f.executor.ExecuteRemove(ctx, root, preview)
		close(removed)
		select {
		case <-settled:
		case <-ctx.Done():
			return receipt, ctx.Err()
		}
		return receipt, err
	}}
	first, firstErr := other.cleanup(ctx, request, leading)
	var last outcome
	select {
	case last = <-lateResult:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if firstErr != nil || last.err != nil || first.Preserved || last.result.Preserved || first.Workspace.State != drydock.StateCleaned || last.result.Workspace.State != drydock.StateCleaned {
		t.Fatalf("same intent contenders disagreed: leading=%+v err=%v late=%+v err=%v", first, firstErr, last.result, last.err)
	}
	if !first.Replayed || !reflect.DeepEqual(first.Receipt, last.result.Receipt) {
		t.Fatalf("returned a constructed losing receipt instead of durable outcome: first=%+v last=%+v", first, last.result)
	}
	assertCleanupOutcomePersisted(t, f, created, request, first)
}

type pausePreservedCleanupStore struct {
	*store.SQLiteStore
	preserving chan<- struct{}
	removed    <-chan struct{}
}

func (s *pausePreservedCleanupStore) CompleteThreadDrydockCleanup(ctx context.Context, workspace drydock.Workspace,
	generation int64, receipt drydock.Receipt,
) (drydock.Workspace, bool, error) {
	if receipt.Outcome == drydock.OutcomePreserved {
		s.preserving <- struct{}{}
		select {
		case <-s.removed:
		case <-ctx.Done():
			return drydock.Workspace{}, false, ctx.Err()
		}
	}
	return s.SQLiteStore.CompleteThreadDrydockCleanup(ctx, workspace, generation, receipt)
}

func TestDrydockCleanupDirtyObservationCannotSealAnotherSameKeyRemoval(t *testing.T) {
	f := newDrydockApplicationFixture(t, "cleanup dirty concurrent observation")
	created := mustCreateDrydock(t, f)
	second, err := store.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	planned, removeAllowed, removed, observerDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	preserving := make(chan struct{}, 1)
	observer, err := NewDrydockService(&pausePreservedCleanupStore{SQLiteStore: second, preserving: preserving, removed: removed}, f.executor)
	if err != nil {
		t.Fatal(err)
	}
	request := DrydockCleanupRequest{RunID: f.run.ID, ExpectedGeneration: created.Generation,
		OperationKey: "cleanup-dirty-concurrent-same-key", RequestedBy: "operator", Confirm: true}
	type outcome struct {
		result DrydockCleanupResult
		err    error
	}
	leadingResult, observedResult := make(chan outcome, 1), make(chan outcome, 1)
	leader := cleanupOutcomeExecutor{DrydockExecutor: f.executor,
		plan: func(ctx context.Context, root, name, id string) (gitadvanced.Preview, error) {
			close(planned)
			select {
			case <-removeAllowed:
			case <-ctx.Done():
				return gitadvanced.Preview{}, ctx.Err()
			}
			return f.executor.PlanRemove(ctx, root, name, id)
		},
		execute: func(ctx context.Context, root string, preview gitadvanced.Preview) (gitadvanced.Receipt, error) {
			receipt, err := f.executor.ExecuteRemove(ctx, root, preview)
			close(removed)
			select {
			case <-observerDone:
			case <-ctx.Done():
				return receipt, ctx.Err()
			}
			return receipt, err
		}}
	go func() {
		result, err := f.service.cleanup(ctx, request, leader)
		leadingResult <- outcome{result, err}
	}()
	select {
	case <-planned:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A second caller observes a real temporary file change while the first
	// is about to obtain its Git removal preview. No filesystem result is mocked.
	writeDrydockTestFile(t, filepath.Join(created.Path, "tracked.txt"), "temporary external edit\n")
	go func() {
		result, err := observer.Cleanup(ctx, request)
		observedResult <- outcome{result, err}
		close(observerDone)
	}()
	select {
	case <-preserving: // Before the fix: an unsafe preserved receipt is pending.
	case <-observerDone: // After the fix: only this caller's observation failed.
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pending, err := f.state.HasPendingDrydockCleanup(ctx, created.ID)
	if err != nil || !pending {
		t.Fatalf("dirty observation released removal reservation: %t %v", pending, err)
	}
	// Restore the exact bytes. Git obtains a fresh preview and independently
	// revalidates it before performing the real non-force removal.
	writeDrydockTestFile(t, filepath.Join(created.Path, "tracked.txt"), "base\n")
	close(removeAllowed)
	var first, last outcome
	select {
	case first = <-leadingResult:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case last = <-observedResult:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, statErr := os.Lstat(created.Path); !os.IsNotExist(statErr) {
		t.Fatalf("fixture did not actually remove its Git worktree: %v", statErr)
	}
	t.Log("actual Git worktree removal confirmed absent before checking lifecycle result")
	if first.err != nil || first.result.Preserved || first.result.Workspace.State != drydock.StateCleaned || first.result.Receipt.Outcome != drydock.OutcomeSucceeded {
		t.Fatalf("actual removal was mislabeled after dirty observation: result=%+v err=%v observer=%+v observerErr=%v", first.result, first.err, last.result, last.err)
	}
	if last.err == nil || last.result.Receipt.ID != "" || last.result.Preserved {
		t.Fatalf("dirty observer sealed an outcome it could not prove: %+v %v", last.result, last.err)
	}
	assertCleanupOutcomePersisted(t, f, created, request, first.result)
}

func assertCleanupOutcomePersisted(t *testing.T, f drydockApplicationFixture, created drydock.Workspace, request DrydockCleanupRequest, result DrydockCleanupResult) {
	t.Helper()
	if _, err := os.Lstat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("removed directory remains: %v", err)
	}
	if got := readDrydockTestFile(t, filepath.Join(f.sourceRoot, "tracked.txt")); got != "base\n" {
		t.Fatalf("source changed: %q", got)
	}
	if result.Workspace.LastCheckpointID != created.LastCheckpointID || result.Receipt.CheckpointID != "" || result.Workspace.Generation != created.Generation+1 {
		t.Fatalf("cleanup invented a checkpoint or extra generation: %+v", result)
	}
	replay, err := f.service.Cleanup(t.Context(), request)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Receipt, result.Receipt) || replay.Workspace.State != drydock.StateCleaned {
		t.Fatalf("durable replay=%+v err=%v", replay, err)
	}
	receipts, err := f.state.ListDrydockReceipts(t.Context(), created.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, receipt := range receipts {
		if receipt.Operation == drydock.OperationCleanup {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("cleanup receipts=%d", count)
	}
	pending, err := f.state.HasPendingDrydockCleanup(t.Context(), created.ID)
	if err != nil || pending {
		t.Fatalf("cleanup reservation remained pending=%t err=%v", pending, err)
	}
}
