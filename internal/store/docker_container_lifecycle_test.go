package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/sandbox"
)

func TestDockerContainerLifecycleWriteAheadLedgerAndRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-lifecycle.db")
	st, run, root := openSandboxManifestStoreAt(t, ctx, path)
	t.Cleanup(func() { _ = st.Close() })
	intent, request := newDockerContainerLifecycleStoreIntent(t, ctx, st, run.ID, root,
		"docker-lifecycle-ledger")

	record, replayed, err := st.BeginDockerContainerLifecycle(ctx, intent,
		"lifecycle_owner_one", sandbox.MinDockerContainerLifecycleLeaseTTL)
	if err != nil || replayed || record.Intent.IntentFingerprint != intent.IntentFingerprint ||
		record.Lease.Generation != 1 || !record.Lease.ActiveAt(time.Now().UTC()) ||
		len(record.Actions) != 0 || len(record.Transitions) != 0 || record.Receipt != nil {
		t.Fatalf("begin Docker lifecycle: record=%#v replayed=%t err=%v", record, replayed, err)
	}
	var intents, leases int
	if err := st.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM sandbox_docker_lifecycle_intents WHERE id = ?),
		(SELECT COUNT(*) FROM sandbox_docker_lifecycle_leases WHERE intent_id = ?)`,
		intent.ID, intent.ID).Scan(&intents, &leases); err != nil || intents != 1 || leases != 1 {
		t.Fatalf("intent and initial lease were not one durable WAL: intents=%d leases=%d err=%v",
			intents, leases, err)
	}
	replayedRecord, wasReplay, err := st.BeginDockerContainerLifecycle(ctx, intent,
		"ignored_replay_owner", time.Minute)
	if err != nil || !wasReplay || !replayedRecord.Replayed ||
		replayedRecord.Lease.LeaseID != record.Lease.LeaseID {
		t.Fatalf("begin replay changed WAL identity: record=%#v replayed=%t err=%v",
			replayedRecord, wasReplay, err)
	}

	actionAt := record.Lease.RenewedAt.Add(100 * time.Millisecond)
	action, err := sandbox.NewDockerContainerLifecyclePreparedAction(intent.ID, 1,
		record.Lease, string(sandbox.DockerContainerLifecycleActionCreate), actionAt)
	if err != nil {
		t.Fatal(err)
	}
	actionRecord, wasReplay, err := st.PrepareDockerContainerLifecycleAction(ctx, action,
		record.Lease)
	if err != nil || wasReplay || len(actionRecord.Actions) != 1 {
		t.Fatalf("prepare lifecycle action: record=%#v replayed=%t err=%v",
			actionRecord, wasReplay, err)
	}
	if _, wasReplay, err = st.PrepareDockerContainerLifecycleAction(ctx, action,
		record.Lease); err != nil || !wasReplay {
		t.Fatalf("exact action replay did not converge: replayed=%t err=%v", wasReplay, err)
	}
	conflicting, err := sandbox.NewDockerContainerLifecyclePreparedAction(intent.ID, 1,
		record.Lease, string(sandbox.DockerContainerLifecycleActionCreate),
		actionAt.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PrepareDockerContainerLifecycleAction(ctx, conflicting,
		record.Lease); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("non-exact action replay was not rejected: %v", err)
	}

	stage, err := sandbox.NewDockerContainerStageResult(mustDockerLifecycleEndpoint(t), request,
		strings.Repeat("c", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := actionAt.Add(100 * time.Millisecond)
	created, err := sandbox.NewDockerContainerLifecycleTransition(intent.ID, 1, record.Lease,
		sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, nil, stage.ContainerIDFingerprint, "",
		createdAt)
	if err != nil {
		t.Fatal(err)
	}
	transitionRecord, wasReplay, err := st.AppendDockerContainerLifecycleTransition(ctx,
		created, record.Lease)
	if err != nil || wasReplay || len(transitionRecord.Transitions) != 1 {
		t.Fatalf("append created transition: record=%#v replayed=%t err=%v",
			transitionRecord, wasReplay, err)
	}
	if _, wasReplay, err = st.AppendDockerContainerLifecycleTransition(ctx, created,
		record.Lease); err != nil || !wasReplay {
		t.Fatalf("exact transition replay did not converge: replayed=%t err=%v", wasReplay, err)
	}

	stale := record.Lease
	stale.OwnerID = "stale_lifecycle_owner"
	if err := st.FenceDockerContainerLifecycle(ctx, stale); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale lease passed the durable fence: %v", err)
	}
	staleAction, err := sandbox.NewDockerContainerLifecyclePreparedAction(intent.ID, 2,
		stale, string(sandbox.DockerContainerLifecycleActionStart), createdAt.Add(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PrepareDockerContainerLifecycleAction(ctx, staleAction,
		stale); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale owner prepared a daemon action: %v", err)
	}

	time.Sleep(sandbox.MinDockerContainerLifecycleLeaseTTL + 100*time.Millisecond)
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	recoverable, err := second.ListRecoverableDockerContainerLifecycles(ctx, 10)
	if err != nil || len(recoverable) != 1 || recoverable[0].Intent.ID != intent.ID {
		t.Fatalf("recoverable lifecycle list: values=%#v err=%v", recoverable, err)
	}
	takenOver, err := second.AcquireDockerContainerLifecycle(ctx, intent.ID,
		intent.RequestedBy, "lifecycle_owner_two", time.Minute)
	if err != nil || !takenOver.TookOver || takenOver.Lease.Generation != 2 ||
		takenOver.Lease.ResourceGeneration != 1 || takenOver.Lease.OwnerID != "lifecycle_owner_two" {
		t.Fatalf("second store takeover: record=%#v err=%v", takenOver, err)
	}
	if err := st.FenceDockerContainerLifecycle(ctx, record.Lease); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("replaced generation passed fence: %v", err)
	}
	staleStarted, err := sandbox.NewDockerContainerLifecycleTransition(intent.ID, 2,
		record.Lease, sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted, nil, stage.ContainerIDFingerprint,
		created.TransitionFingerprint, createdAt.Add(200*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AppendDockerContainerLifecycleTransition(ctx, staleStarted,
		record.Lease); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("replaced generation appended a transition: %v", err)
	}
	if loaded, err := second.GetDockerContainerLifecycle(ctx, intent.ID); err != nil ||
		len(loaded.Transitions) != 1 {
		t.Fatalf("stale transition changed the ledger: transitions=%#v err=%v",
			loaded.Transitions, err)
	}
}

func TestDockerContainerLifecycleTransitionsUniqueReceiptAndPrivateEvents(t *testing.T) {
	ctx := context.Background()
	st, run, root := openSandboxManifestStore(t, ctx)
	intent, request := newDockerContainerLifecycleStoreIntent(t, ctx, st, run.ID, root,
		"docker-lifecycle-complete")
	record, _, err := st.BeginDockerContainerLifecycle(ctx, intent,
		"lifecycle_completion_owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := sandbox.NewDockerContainerStageResult(mustDockerLifecycleEndpoint(t), request,
		strings.Repeat("d", 64), false)
	if err != nil {
		t.Fatal(err)
	}

	nextAt := record.Lease.RenewedAt.Add(time.Second)
	appendAction := func(verb string) {
		t.Helper()
		action, actionErr := sandbox.NewDockerContainerLifecyclePreparedAction(intent.ID,
			len(record.Actions)+1, record.Lease, verb, nextAt)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		record, _, actionErr = st.PrepareDockerContainerLifecycleAction(ctx, action, record.Lease)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		nextAt = nextAt.Add(time.Second)
	}
	appendTransition := func(state, reason, containerID string, exitCode *int) {
		t.Helper()
		previous := ""
		if len(record.Transitions) > 0 {
			previous = record.Transitions[len(record.Transitions)-1].TransitionFingerprint
		}
		transition, transitionErr := sandbox.NewDockerContainerLifecycleTransition(intent.ID,
			len(record.Transitions)+1, record.Lease, state, reason, exitCode, containerID,
			previous, nextAt)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		nextAt = nextAt.Add(time.Second)
		record, _, transitionErr = st.AppendDockerContainerLifecycleTransition(ctx,
			transition, record.Lease)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
	}

	appendAction(string(sandbox.DockerContainerLifecycleActionCreate))
	appendTransition(sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, stage.ContainerIDFingerprint, nil)
	appendAction(string(sandbox.DockerContainerLifecycleActionStart))
	appendTransition(sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted, stage.ContainerIDFingerprint, nil)
	exitCode := 0
	appendTransition(sandbox.DockerContainerLifecycleTransitionExited,
		sandbox.DockerContainerLifecycleReasonNaturalExit, stage.ContainerIDFingerprint, &exitCode)
	appendTransition(sandbox.DockerContainerLifecycleTransitionCleaning,
		sandbox.DockerContainerLifecycleReasonNaturalExit, "", nil)
	appendAction(string(sandbox.DockerContainerLifecycleActionDelete))
	appendTransition(sandbox.DockerContainerLifecycleTransitionCleaned,
		sandbox.DockerContainerLifecycleReasonCleanupCompleted, "", nil)

	final := record.Transitions[len(record.Transitions)-1]
	receipt, err := sandbox.NewDockerContainerLifecycleReceipt(intent.ID, record.Lease, final,
		stage.ContainerIDFingerprint, sandbox.DockerContainerLifecycleOutcomeNaturalExit,
		&exitCode, true, false, nextAt)
	if err != nil {
		t.Fatal(err)
	}
	completed, replayed, err := st.CompleteDockerContainerLifecycle(ctx, receipt, record.Lease)
	if err != nil || replayed || completed.Receipt == nil ||
		completed.Receipt.CleanupFingerprint != receipt.CleanupFingerprint {
		t.Fatalf("complete lifecycle: record=%#v replayed=%t err=%v", completed, replayed, err)
	}
	if _, replayed, err = st.CompleteDockerContainerLifecycle(ctx, receipt,
		record.Lease); err != nil || !replayed {
		t.Fatalf("exact receipt replay did not converge: replayed=%t err=%v", replayed, err)
	}
	conflicting, err := sandbox.NewDockerContainerLifecycleReceipt(intent.ID, record.Lease,
		final, stage.ContainerIDFingerprint, sandbox.DockerContainerLifecycleOutcomeNaturalExit,
		&exitCode, true, false, nextAt.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CompleteDockerContainerLifecycle(ctx, conflicting,
		record.Lease); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("second cleanup receipt was not rejected: %v", err)
	}
	values, err := st.ListRecoverableDockerContainerLifecycles(ctx, 10)
	if err != nil || len(values) != 0 {
		t.Fatalf("completed lifecycle remained recoverable: values=%#v err=%v", values, err)
	}

	timeline, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, event := range timeline {
		switch event.Type {
		case events.SandboxDockerLifecyclePreparedEvent,
			events.SandboxDockerLifecycleActionPreparedEvent,
			events.SandboxDockerLifecycleTransitionEvent,
			events.SandboxDockerLifecycleCompletedEvent:
			found[event.Type] = true
		}
		for _, private := range []string{root, request.Spec.ContainerName, strings.Repeat("d", 64),
			stage.ContainerIDFingerprint, record.Lease.LeaseID, record.Lease.OwnerID,
			intent.OperationKeyDigest, intent.RequestFingerprint} {
			if strings.Contains(event.PayloadJSON, private) {
				t.Fatalf("Docker lifecycle event leaked private material %q: %#v", private, event)
			}
		}
	}
	for _, eventType := range []string{events.SandboxDockerLifecyclePreparedEvent,
		events.SandboxDockerLifecycleActionPreparedEvent,
		events.SandboxDockerLifecycleTransitionEvent,
		events.SandboxDockerLifecycleCompletedEvent} {
		if !found[eventType] {
			t.Fatalf("Docker lifecycle timeline lacks %q", eventType)
		}
	}
}

func TestDockerContainerLifecycleTakeoverCompletesReceiptAfterCleanedCrashWindow(t *testing.T) {
	ctx := context.Background()
	st, run, root := openSandboxManifestStore(t, ctx)
	intent, request := newDockerContainerLifecycleStoreIntent(t, ctx, st, run.ID, root,
		"docker-lifecycle-cleaned-crash")
	record, _, err := st.BeginDockerContainerLifecycle(ctx, intent,
		"lifecycle_cleaned_owner_one", sandbox.MinDockerContainerLifecycleLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := sandbox.NewDockerContainerStageResult(mustDockerLifecycleEndpoint(t), request,
		strings.Repeat("e", 64), false)
	if err != nil {
		t.Fatal(err)
	}

	nextAt := record.Lease.RenewedAt.Add(50 * time.Millisecond)
	appendAction := func(verb string) {
		t.Helper()
		action, actionErr := sandbox.NewDockerContainerLifecyclePreparedAction(intent.ID,
			len(record.Actions)+1, record.Lease, verb, nextAt)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		record, _, actionErr = st.PrepareDockerContainerLifecycleAction(ctx, action, record.Lease)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		nextAt = nextAt.Add(50 * time.Millisecond)
	}
	appendTransition := func(state, reason, containerID string, exitCode *int) {
		t.Helper()
		previous := ""
		if len(record.Transitions) > 0 {
			previous = record.Transitions[len(record.Transitions)-1].TransitionFingerprint
		}
		transition, transitionErr := sandbox.NewDockerContainerLifecycleTransition(intent.ID,
			len(record.Transitions)+1, record.Lease, state, reason, exitCode, containerID,
			previous, nextAt)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		record, _, transitionErr = st.AppendDockerContainerLifecycleTransition(ctx,
			transition, record.Lease)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		nextAt = nextAt.Add(50 * time.Millisecond)
	}

	appendAction(string(sandbox.DockerContainerLifecycleActionCreate))
	appendTransition(sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, stage.ContainerIDFingerprint, nil)
	appendAction(string(sandbox.DockerContainerLifecycleActionStart))
	appendTransition(sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted, stage.ContainerIDFingerprint, nil)
	exitCode := 0
	appendTransition(sandbox.DockerContainerLifecycleTransitionExited,
		sandbox.DockerContainerLifecycleReasonNaturalExit, stage.ContainerIDFingerprint, &exitCode)
	appendTransition(sandbox.DockerContainerLifecycleTransitionCleaning,
		sandbox.DockerContainerLifecycleReasonNaturalExit, "", nil)
	appendAction(string(sandbox.DockerContainerLifecycleActionDelete))
	appendTransition(sandbox.DockerContainerLifecycleTransitionCleaned,
		sandbox.DockerContainerLifecycleReasonCleanupCompleted, "", nil)
	final := record.Transitions[len(record.Transitions)-1]
	staleLease := record.Lease

	remaining := time.Until(staleLease.ExpiresAt)
	if remaining > 0 {
		time.Sleep(remaining + 100*time.Millisecond)
	}
	takenOver, err := st.AcquireDockerContainerLifecycle(ctx, intent.ID, intent.RequestedBy,
		"lifecycle_cleaned_owner_two", time.Minute)
	if err != nil || !takenOver.TookOver || takenOver.Lease.Generation != 2 {
		t.Fatalf("take over cleaned lifecycle: record=%#v err=%v", takenOver, err)
	}

	wrongOutcome, err := sandbox.NewDockerContainerLifecycleReceipt(intent.ID, takenOver.Lease,
		final, stage.ContainerIDFingerprint, sandbox.DockerContainerLifecycleOutcomeTimedOut,
		&exitCode, false, true, takenOver.Lease.RenewedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CompleteDockerContainerLifecycle(ctx, wrongOutcome,
		takenOver.Lease); err == nil {
		t.Fatal("takeover receipt changed the durable cleaning outcome")
	}
	wrongContainer, err := sandbox.NewDockerContainerLifecycleReceipt(intent.ID,
		takenOver.Lease, final, strings.Repeat("f", 64),
		sandbox.DockerContainerLifecycleOutcomeNaturalExit, &exitCode, false, true,
		takenOver.Lease.RenewedAt.Add(2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CompleteDockerContainerLifecycle(ctx, wrongContainer,
		takenOver.Lease); err == nil {
		t.Fatal("takeover receipt changed the durable container identity")
	}

	receipt, err := sandbox.NewDockerContainerLifecycleReceipt(intent.ID, takenOver.Lease,
		final, stage.ContainerIDFingerprint, sandbox.DockerContainerLifecycleOutcomeNaturalExit,
		&exitCode, false, true, takenOver.Lease.RenewedAt.Add(3*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	completed, replayed, err := st.CompleteDockerContainerLifecycle(ctx, receipt,
		takenOver.Lease)
	if err != nil || replayed || completed.Receipt == nil ||
		completed.Receipt.LeaseGeneration != 2 || final.LeaseGeneration != 1 {
		t.Fatalf("complete takeover receipt: record=%#v replayed=%t err=%v",
			completed, replayed, err)
	}

	staleReceipt, err := sandbox.NewDockerContainerLifecycleReceipt(intent.ID, staleLease,
		final, stage.ContainerIDFingerprint, sandbox.DockerContainerLifecycleOutcomeNaturalExit,
		&exitCode, false, true, final.RecordedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CompleteDockerContainerLifecycle(ctx, staleReceipt,
		staleLease); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale generation completed takeover receipt: %v", err)
	}
	loaded, err := st.GetDockerContainerLifecycle(ctx, intent.ID)
	if err != nil || loaded.Receipt == nil ||
		loaded.Receipt.CleanupFingerprint != receipt.CleanupFingerprint {
		t.Fatalf("takeover receipt was not unique and durable: record=%#v err=%v", loaded, err)
	}
}

func TestDockerContainerLifecycleConcurrentStoresAdmitOneInitialLease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-lifecycle-concurrent.db")
	first, run, root := openSandboxManifestStoreAt(t, ctx, path)
	t.Cleanup(func() { _ = first.Close() })
	intent, _ := newDockerContainerLifecycleStoreIntent(t, ctx, first, run.ID, root,
		"docker-lifecycle-concurrent")
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	stores := []*SQLiteStore{first, second}
	owners := []string{"lifecycle_concurrent_owner_one", "lifecycle_concurrent_owner_two"}
	records := make([]sandbox.DockerContainerLifecycleRecord, len(stores))
	replayed := make([]bool, len(stores))
	errorsFound := make([]error, len(stores))
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range stores {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			records[index], replayed[index], errorsFound[index] =
				stores[index].BeginDockerContainerLifecycle(ctx, intent, owners[index], time.Minute)
		}(index)
	}
	close(start)
	group.Wait()

	successes, exactReplays := 0, 0
	for index, beginErr := range errorsFound {
		if beginErr != nil {
			t.Fatalf("concurrent lifecycle begin %d failed: %v", index, beginErr)
		}
		if replayed[index] {
			exactReplays++
		} else {
			successes++
		}
		if records[index].Intent.ID != intent.ID || records[index].Lease.Generation != 1 {
			t.Fatalf("concurrent lifecycle identity diverged: %#v", records[index])
		}
	}
	if successes != 1 || exactReplays != 1 {
		t.Fatalf("concurrent lifecycle did not converge to one lease: replayed=%v records=%#v",
			replayed, records)
	}
	var intents, leases int
	if err := first.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM sandbox_docker_lifecycle_intents WHERE id = ?),
		(SELECT COUNT(*) FROM sandbox_docker_lifecycle_leases WHERE intent_id = ?)`,
		intent.ID, intent.ID).Scan(&intents, &leases); err != nil || intents != 1 || leases != 1 {
		t.Fatalf("concurrent lifecycle fabricated ownership rows: intents=%d leases=%d err=%v",
			intents, leases, err)
	}
}

func newDockerContainerLifecycleStoreIntent(t *testing.T, ctx context.Context,
	st *SQLiteStore, runID, root, prefix string,
) (sandbox.DockerContainerLaunchIntent, sandbox.DockerContainerWriteRequest) {
	t.Helper()
	_, manifest, observation := createDockerContainerPlanStoreAuthority(t, ctx, st,
		runID, root, prefix)
	plan, operation := newDockerContainerPlanStoreRecord(t, ctx, observation, manifest,
		prefix+"-plan")
	if _, _, err := st.CreateDockerContainerPlan(ctx, plan, operation); err != nil {
		t.Fatal(err)
	}
	spec, err := sandbox.CompileDockerContainerSpec(ctx, observation, manifest)
	if err != nil {
		t.Fatal(err)
	}
	request, err := sandbox.NewDockerContainerWriteRequest(ctx, root, spec)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := sandbox.NewDockerContainerLaunchIntent(
		idgen.New("sandbox-docker-lifecycle"), idgen.New("sandbox-docker-attempt"),
		runmutation.Fingerprint("sandbox_docker_lifecycle_store_test.v1", prefix), plan,
		request, mustDockerLifecycleEndpoint(t), plan.RequestedBy, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return intent, request
}

func mustDockerLifecycleEndpoint(t *testing.T) sandbox.DockerObservationEndpoint {
	t.Helper()
	endpoint, err := sandbox.NewDockerObservationEndpoint(
		sandbox.DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}
