package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/sandbox"
)

type dockerLifecycleTestRecorder struct {
	mu     sync.Mutex
	events []string
}

type dockerLifecycleLookupBarrier struct {
	mu       sync.Mutex
	arrivals int
	ready    chan struct{}
}

func newDockerLifecycleLookupBarrier() *dockerLifecycleLookupBarrier {
	return &dockerLifecycleLookupBarrier{ready: make(chan struct{})}
}

func (barrier *dockerLifecycleLookupBarrier) wait() {
	barrier.mu.Lock()
	barrier.arrivals++
	if barrier.arrivals == 2 {
		close(barrier.ready)
	}
	ready := barrier.ready
	barrier.mu.Unlock()
	<-ready
}

func (recorder *dockerLifecycleTestRecorder) add(event string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, event)
}

func (recorder *dockerLifecycleTestRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.events...)
}

type dockerLifecycleSupervisorTestStore struct {
	mu            sync.Mutex
	now           time.Time
	recorder      *dockerLifecycleTestRecorder
	record        sandbox.DockerContainerLifecycleRecord
	found         bool
	fenceErr      error
	beginCalls    int
	acquireCalls  int
	completeCalls int
	lookupBarrier *dockerLifecycleLookupBarrier
}

func (store *dockerLifecycleSupervisorTestStore) GetDockerContainerLifecycleByOperation(
	_ context.Context, operationKeyDigest string,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	store.mu.Lock()
	found := store.found && store.record.Intent.OperationKeyDigest == operationKeyDigest
	record := store.record
	barrier := store.lookupBarrier
	store.mu.Unlock()
	if !found && barrier != nil {
		barrier.wait()
	}
	return record, found, nil
}

func (store *dockerLifecycleSupervisorTestStore) BeginDockerContainerLifecycle(_ context.Context,
	intent sandbox.DockerContainerLaunchIntent, ownerID string, ttl time.Duration,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.beginCalls++
	store.recorder.add("begin")
	if store.found {
		if store.record.Intent.ID == intent.ID &&
			store.record.Intent.IntentFingerprint == intent.IntentFingerprint {
			return store.record, true, nil
		}
		return sandbox.DockerContainerLifecycleRecord{}, false, apperror.New(
			apperror.CodeConflict, "Docker lifecycle test operation already exists")
	}
	lease, err := sandbox.NewDockerContainerLifecycleLease(intent, "lifecycle-lease-1",
		ownerID, 1, store.now, ttl)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, false, err
	}
	store.record = sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease}
	store.found = true
	return store.record, false, nil
}

func (store *dockerLifecycleSupervisorTestStore) AcquireDockerContainerLifecycle(_ context.Context,
	intentID, requestedBy, ownerID string, ttl time.Duration,
) (sandbox.DockerContainerLifecycleRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.acquireCalls++
	store.recorder.add("acquire")
	if !store.found || store.record.Intent.ID != intentID ||
		store.record.Intent.RequestedBy != requestedBy {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeNotFound, "Docker lifecycle test record was not found")
	}
	if store.record.Receipt != nil {
		return store.record, nil
	}
	if store.record.Lease.ActiveAt(store.now) {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeConflict, "Docker lifecycle test lease is still active")
	}
	lease, err := sandbox.NewDockerContainerLifecycleLease(store.record.Intent,
		fmt.Sprintf("lifecycle-lease-%d", store.record.Lease.Generation+1), ownerID,
		store.record.Lease.Generation+1, store.now, ttl)
	if err != nil {
		return sandbox.DockerContainerLifecycleRecord{}, err
	}
	store.record.Lease = lease
	store.record.TookOver = true
	return store.record, nil
}

func (store *dockerLifecycleSupervisorTestStore) RenewDockerContainerLifecycleLease(
	_ context.Context, expected sandbox.DockerContainerLifecycleLease, ttl time.Duration,
) (sandbox.DockerContainerLifecycleLease, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.record.Lease.Fences(expected, store.now) {
		return sandbox.DockerContainerLifecycleLease{}, apperror.New(
			apperror.CodeConflict, "Docker lifecycle test lease was fenced")
	}
	renewed, err := store.record.Lease.Renew(store.now, ttl)
	if err == nil {
		store.record.Lease = renewed
	}
	return renewed, err
}

func (store *dockerLifecycleSupervisorTestStore) ReleaseDockerContainerLifecycleLease(
	_ context.Context, expected sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleLease, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.record.Lease.Status == sandbox.DockerContainerLifecycleLeaseReleased {
		return store.record.Lease, true, nil
	}
	if !store.record.Lease.Fences(expected, store.now) {
		return sandbox.DockerContainerLifecycleLease{}, false, apperror.New(
			apperror.CodeConflict, "Docker lifecycle test lease was fenced")
	}
	released := expected
	released.Status = sandbox.DockerContainerLifecycleLeaseReleased
	releasedAt := store.now
	released.ReleasedAt = &releasedAt
	store.record.Lease = released
	return released, false, nil
}

func (store *dockerLifecycleSupervisorTestStore) FenceDockerContainerLifecycle(_ context.Context,
	expected sandbox.DockerContainerLifecycleLease,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.recorder.add("fence")
	if store.fenceErr != nil {
		return store.fenceErr
	}
	if !store.record.Lease.Fences(expected, store.now) {
		return apperror.New(apperror.CodeConflict, "Docker lifecycle test lease was fenced")
	}
	return nil
}

func (store *dockerLifecycleSupervisorTestStore) PrepareDockerContainerLifecycleAction(
	_ context.Context, action sandbox.DockerContainerLifecyclePreparedAction,
	_ sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.recorder.add("prepare:" + action.Verb)
	for _, existing := range store.record.Actions {
		if existing.ActionFingerprint == action.ActionFingerprint {
			return store.record, true, nil
		}
	}
	store.record.Actions = append(store.record.Actions, action)
	return store.record, false, nil
}

func (store *dockerLifecycleSupervisorTestStore) AppendDockerContainerLifecycleTransition(
	_ context.Context, transition sandbox.DockerContainerLifecycleTransition,
	_ sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.recorder.add("transition:" + transition.State)
	for _, existing := range store.record.Transitions {
		if existing.TransitionFingerprint == transition.TransitionFingerprint {
			return store.record, true, nil
		}
	}
	store.record.Transitions = append(store.record.Transitions, transition)
	return store.record, false, nil
}

func (store *dockerLifecycleSupervisorTestStore) CompleteDockerContainerLifecycle(
	_ context.Context, receipt sandbox.DockerContainerLifecycleReceipt,
	_ sandbox.DockerContainerLifecycleLease,
) (sandbox.DockerContainerLifecycleRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.recorder.add("receipt")
	if store.record.Receipt != nil {
		return store.record, true, nil
	}
	store.completeCalls++
	store.record.Receipt = &receipt
	return store.record, false, nil
}

func (store *dockerLifecycleSupervisorTestStore) GetDockerContainerLifecycle(_ context.Context,
	intentID string,
) (sandbox.DockerContainerLifecycleRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.found || store.record.Intent.ID != intentID {
		return sandbox.DockerContainerLifecycleRecord{}, apperror.New(
			apperror.CodeNotFound, "Docker lifecycle test record was not found")
	}
	return store.record, nil
}

func (store *dockerLifecycleSupervisorTestStore) ListRecoverableDockerContainerLifecycles(
	_ context.Context, _ int,
) ([]sandbox.DockerContainerLifecycleRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.found || store.record.Receipt != nil || store.record.Lease.ActiveAt(store.now) {
		return nil, nil
	}
	return []sandbox.DockerContainerLifecycleRecord{store.record}, nil
}

type dockerLifecycleSupervisorTestAuthority struct {
	plan    sandbox.DockerContainerPlan
	request sandbox.DockerContainerWriteRequest
}

func (authority dockerLifecycleSupervisorTestAuthority) RevalidateDockerContainerLifecycle(
	_ context.Context, _ sandbox.DockerContainerLaunchIntent,
) (sandbox.DockerContainerPlan, sandbox.DockerContainerWriteRequest, error) {
	return authority.plan, authority.request, nil
}

type dockerLifecycleSupervisorTestTransport struct {
	endpoint   sandbox.DockerObservationEndpoint
	recorder   *dockerLifecycleTestRecorder
	state      string
	resourceID string
	stageCalls int
	startCalls int
	waitCalls  int
	cleanup    int
	terminate  int
	creates    int
	starts     int
	terms      int
	deletes    int
}

func (transport *dockerLifecycleSupervisorTestTransport) Endpoint() sandbox.DockerObservationEndpoint {
	return transport.endpoint
}

func (transport *dockerLifecycleSupervisorTestTransport) Stage(_ context.Context,
	_ sandbox.DockerContainerWriteRequest,
) (sandbox.DockerContainerStageResult, error) {
	return sandbox.DockerContainerStageResult{}, errors.New("legacy Stage is not used by Supervisor")
}

func (transport *dockerLifecycleSupervisorTestTransport) StageOwned(ctx context.Context,
	request sandbox.DockerContainerWriteRequest, _ sandbox.DockerContainerLifecycleOwnership,
	fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerStageResult, error) {
	transport.stageCalls++
	transport.recorder.add("stage-owned")
	if transport.state != sandbox.DockerContainerLifecycleStateAbsent {
		stage, err := sandbox.NewDockerContainerStageResult(transport.endpoint, request,
			strings.Repeat("c", 64), true)
		if err != nil {
			return sandbox.DockerContainerStageResult{}, err
		}
		if stage.ContainerIDFingerprint != transport.resourceID {
			return sandbox.DockerContainerStageResult{}, errors.New(
				"adopted Docker lifecycle container identity changed")
		}
		transport.recorder.add("adopt:create")
		return stage, nil
	}
	if err := fence(ctx, sandbox.DockerContainerLifecycleActionCreate); err != nil {
		return sandbox.DockerContainerStageResult{}, err
	}
	transport.creates++
	transport.recorder.add("mutate:create")
	stage, err := sandbox.NewDockerContainerStageResult(transport.endpoint, request,
		strings.Repeat("a", 64), false)
	if err == nil {
		transport.resourceID = stage.ContainerIDFingerprint
		transport.state = sandbox.DockerContainerLifecycleStateCreated
	}
	return stage, err
}

func (transport *dockerLifecycleSupervisorTestTransport) Observe(_ context.Context,
	request sandbox.DockerContainerLifecycleRequest,
) (sandbox.DockerContainerLifecycleObservation, error) {
	return transport.observation(request, transport.state), nil
}

func (transport *dockerLifecycleSupervisorTestTransport) Start(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest, fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleObservation, bool, error) {
	transport.startCalls++
	if err := fence(ctx, sandbox.DockerContainerLifecycleActionStart); err != nil {
		return sandbox.DockerContainerLifecycleObservation{}, false, err
	}
	transport.starts++
	transport.recorder.add("mutate:start")
	transport.state = sandbox.DockerContainerLifecycleStateRunning
	return transport.observation(request, transport.state), true, nil
}

func (transport *dockerLifecycleSupervisorTestTransport) Wait(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest, fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleObservation, error) {
	transport.waitCalls++
	if err := fence(ctx, sandbox.DockerContainerLifecycleActionWait); err != nil {
		return sandbox.DockerContainerLifecycleObservation{}, err
	}
	transport.state = sandbox.DockerContainerLifecycleStateExited
	return transport.observation(request, transport.state), nil
}

func (transport *dockerLifecycleSupervisorTestTransport) Terminate(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest, fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleTerminationResult, error) {
	transport.terminate++
	if err := fence(ctx, sandbox.DockerContainerLifecycleActionTERM); err != nil {
		return sandbox.DockerContainerLifecycleTerminationResult{}, err
	}
	transport.terms++
	transport.recorder.add("mutate:term")
	transport.state = sandbox.DockerContainerLifecycleStateExited
	observation := transport.observation(request, transport.state)
	return sandbox.DockerContainerLifecycleTerminationResult{
		Observation:        observation,
		ExitCode:           observation.ExitCode,
		DaemonReadCount:    1,
		DaemonWriteCount:   1,
		GracefulSignalSent: true,
	}, nil
}

func (transport *dockerLifecycleSupervisorTestTransport) Cleanup(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest, fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleCleanupResult, error) {
	transport.cleanup++
	if err := fence(ctx, sandbox.DockerContainerLifecycleActionDelete); err != nil {
		return sandbox.DockerContainerLifecycleCleanupResult{}, err
	}
	transport.deletes++
	transport.recorder.add("mutate:delete")
	transport.state = sandbox.DockerContainerLifecycleStateAbsent
	return sandbox.DockerContainerLifecycleCleanupResult{
		Observation:      transport.observation(request, transport.state),
		DaemonReadCount:  1,
		DaemonWriteCount: 1,
		ContainerRemoved: true,
		AbsenceConfirmed: true,
	}, nil
}

func (transport *dockerLifecycleSupervisorTestTransport) Run(_ context.Context,
	_ sandbox.DockerContainerLifecycleRequest,
) (sandbox.DockerContainerLifecycleResult, error) {
	return sandbox.DockerContainerLifecycleResult{}, errors.New("monolithic Run is not used by Supervisor")
}

func (transport *dockerLifecycleSupervisorTestTransport) observation(
	request sandbox.DockerContainerLifecycleRequest, state string,
) sandbox.DockerContainerLifecycleObservation {
	present := state != sandbox.DockerContainerLifecycleStateAbsent
	exitCode := 0
	return sandbox.DockerContainerLifecycleObservation{
		State:                     state,
		RequestFingerprint:        request.RequestFingerprint,
		OwnershipLabelFingerprint: request.Ownership.OwnershipLabelFingerprint,
		ContainerIDFingerprint:    map[bool]string{true: transport.resourceID}[present],
		ExitCode:                  exitCode,
		ContainerPresent:          present,
		ConfigurationMatched:      present,
		Running:                   state == sandbox.DockerContainerLifecycleStateRunning,
	}
}

func TestDockerContainerLifecycleSupervisorWritesIntentAndActionFenceBeforeCreate(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "begin-fence")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, "")
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	result, err := supervisor.BeginAndRun(context.Background(), plan, request,
		plan.RequestedBy, "begin-before-create")
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || store.beginCalls != 1 || transport.creates != 1 {
		t.Fatalf("lifecycle did not complete once: record=%#v begin=%d creates=%d",
			result, store.beginCalls, transport.creates)
	}
	assertDockerLifecycleEventOrder(t, recorder.snapshot(), "begin", "stage-owned",
		"prepare:create", "fence", "mutate:create")
}

func TestDockerContainerLifecycleSupervisorBeginReplayIsMetadataOnly(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "begin-replay")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, "")
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	first, err := supervisor.BeginAndRun(context.Background(), plan, request,
		plan.RequestedBy, "same-operation")
	if err != nil {
		t.Fatal(err)
	}
	writes := transport.creates + transport.starts + transport.deletes
	second, err := supervisor.BeginAndRun(context.Background(), plan, request,
		plan.RequestedBy, "same-operation")
	if err != nil {
		t.Fatal(err)
	}
	if first.Receipt == nil || second.Receipt == nil || !second.Replayed ||
		store.beginCalls != 1 || transport.creates+transport.starts+transport.deletes != writes {
		t.Fatalf("begin replay repeated daemon work: first=%#v second=%#v begins=%d before=%d after=%d",
			first, second, store.beginCalls, writes,
			transport.creates+transport.starts+transport.deletes)
	}
}

func TestDockerContainerLifecycleSupervisorConcurrentBeginHasOneDaemonOwner(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "concurrent-begin")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder,
		lookupBarrier: newDockerLifecycleLookupBarrier()}
	transports := []*dockerLifecycleSupervisorTestTransport{
		newDockerLifecycleSupervisorTransport(t, recorder,
			sandbox.DockerContainerLifecycleStateAbsent, ""),
		newDockerLifecycleSupervisorTransport(t, recorder,
			sandbox.DockerContainerLifecycleStateAbsent, ""),
	}
	supervisors := make([]*DockerContainerLifecycleSupervisor, len(transports))
	for index, transport := range transports {
		var err error
		supervisors[index], err = NewDockerContainerLifecycleSupervisor(store, transport,
			dockerLifecycleSupervisorTestAuthority{plan: plan, request: request},
			fmt.Sprintf("docker-lifecycle-supervisor-%d", index+1), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		supervisors[index].now = func() time.Time { return now }
	}

	start := make(chan struct{})
	errs := make([]error, len(supervisors))
	var workers sync.WaitGroup
	for index, supervisor := range supervisors {
		workers.Add(1)
		go func(index int, supervisor *DockerContainerLifecycleSupervisor) {
			defer workers.Done()
			<-start
			_, errs[index] = supervisor.BeginAndRun(context.Background(), plan, request,
				plan.RequestedBy, "same-concurrent-operation")
		}(index, supervisor)
	}
	close(start)
	workers.Wait()

	successes, conflicts := 0, 0
	for _, err := range errs {
		switch apperror.CodeOf(err) {
		case "":
			successes++
		case apperror.CodeConflict:
			conflicts++
		default:
			t.Fatalf("concurrent lifecycle returned unexpected error: %v", err)
		}
	}
	createWrites := transports[0].creates + transports[1].creates
	startWrites := transports[0].starts + transports[1].starts
	deleteWrites := transports[0].deletes + transports[1].deletes
	firstOwned := transports[0].creates+transports[0].starts+transports[0].deletes > 0
	secondOwned := transports[1].creates+transports[1].starts+transports[1].deletes > 0
	if successes != 1 || conflicts != 1 || store.beginCalls != 2 ||
		createWrites != 1 || startWrites != 1 || deleteWrites != 1 || firstOwned == secondOwned {
		t.Fatalf("concurrent lifecycle did not elect one daemon owner: errs=%v begins=%d writes=create:%d start:%d delete:%d perTransport=%#v/%#v",
			errs, store.beginCalls, createWrites, startWrites, deleteWrites,
			transports[0], transports[1])
	}
}

func TestDockerContainerLifecycleSupervisorStaleFencePreventsCreateSideEffect(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "stale-fence")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder,
		fenceErr: apperror.New(apperror.CodeConflict, "stale lifecycle owner")}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, "")
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	_, err := supervisor.BeginAndRun(context.Background(), plan, request,
		plan.RequestedBy, "stale-before-create")
	if err == nil || transport.creates != 0 {
		t.Fatalf("stale fence reached create side effect: creates=%d err=%v",
			transport.creates, err)
	}
	assertDockerLifecycleEventOrder(t, recorder.snapshot(), "begin", "stage-owned",
		"prepare:create", "fence")
	for _, event := range recorder.snapshot() {
		if event == "mutate:create" {
			t.Fatalf("stale fence allowed create mutation: events=%v", recorder.snapshot())
		}
	}
}

func TestDockerContainerLifecycleSupervisorRecoveryDoesNotRestartRunningContainer(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "running-recovery")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	intent, lease, resourceID := newDockerLifecycleRecoveryIntent(t, plan, request, now)
	created := newDockerLifecycleTransition(t, intent.ID, 1, lease,
		sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, nil, resourceID, "", now.Add(-80*time.Second))
	started := newDockerLifecycleTransition(t, intent.ID, 2, lease,
		sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted, nil, resourceID,
		created.TransitionFingerprint, now.Add(-70*time.Second))
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder, found: true,
		record: sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
			Transitions: []sandbox.DockerContainerLifecycleTransition{created, started}}}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateRunning, resourceID)
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	result, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || store.acquireCalls != 1 || transport.startCalls != 0 ||
		transport.starts != 0 || transport.waitCalls != 1 || transport.deletes != 1 {
		t.Fatalf("running recovery duplicated or skipped work: record=%#v acquire=%d startCalls=%d starts=%d waits=%d deletes=%d",
			result, store.acquireCalls, transport.startCalls, transport.starts,
			transport.waitCalls, transport.deletes)
	}
}

func TestDockerContainerLifecycleSupervisorRecoveryBeforeCreateContinuesOnce(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "pre-create-recovery")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	intent, lease, _ := newDockerLifecycleRecoveryIntent(t, plan, request, now)
	record := sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder,
		found: true, record: record}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, "")
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	result, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || store.acquireCalls != 1 || transport.stageCalls != 1 ||
		transport.creates != 1 || transport.starts != 1 || transport.deletes != 1 {
		t.Fatalf("pre-create recovery did not converge once: record=%#v acquire=%d stage=%d create=%d start=%d delete=%d",
			result, store.acquireCalls, transport.stageCalls, transport.creates,
			transport.starts, transport.deletes)
	}
}

func TestDockerContainerLifecycleSupervisorRecoveryAdoptsCreateBeforeTransitionCommit(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "create-commit-window")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	intent, lease, resourceID := newDockerLifecycleRecoveryIntent(t, plan, request, now)
	createAction := newDockerLifecyclePreparedAction(t, intent.ID, 1, lease,
		sandbox.DockerContainerLifecycleActionCreate, now.Add(-110*time.Second))
	record := sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
		Actions: []sandbox.DockerContainerLifecyclePreparedAction{createAction}}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder,
		found: true, record: record}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateCreated, resourceID)
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	result, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || store.acquireCalls != 1 || transport.stageCalls != 1 ||
		transport.creates != 0 || transport.startCalls != 1 || transport.starts != 1 {
		t.Fatalf("create commit-window recovery was not idempotent: record=%#v acquire=%d stage=%d creates=%d startCalls=%d starts=%d",
			result, store.acquireCalls, transport.stageCalls, transport.creates,
			transport.startCalls, transport.starts)
	}
	assertDockerLifecycleEventOrder(t, recorder.snapshot(), "acquire", "stage-owned",
		"adopt:create", "transition:created")
	for _, event := range recorder.snapshot() {
		if event == "mutate:create" {
			t.Fatalf("create commit-window recovery duplicated create: events=%v",
				recorder.snapshot())
		}
	}
}

func TestDockerContainerLifecycleSupervisorRecoveryObservesStartBeforeTransitionCommit(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "start-commit-window")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	intent, lease, resourceID := newDockerLifecycleRecoveryIntent(t, plan, request, now)
	createAction := newDockerLifecyclePreparedAction(t, intent.ID, 1, lease,
		sandbox.DockerContainerLifecycleActionCreate, now.Add(-115*time.Second))
	created := newDockerLifecycleTransition(t, intent.ID, 1, lease,
		sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, nil, resourceID, "",
		now.Add(-110*time.Second))
	startAction := newDockerLifecyclePreparedAction(t, intent.ID, 2, lease,
		sandbox.DockerContainerLifecycleActionStart, now.Add(-105*time.Second))
	record := sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
		Actions:     []sandbox.DockerContainerLifecyclePreparedAction{createAction, startAction},
		Transitions: []sandbox.DockerContainerLifecycleTransition{created}}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder,
		found: true, record: record}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateRunning, resourceID)
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	result, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || store.acquireCalls != 1 || transport.stageCalls != 0 ||
		transport.startCalls != 0 || transport.starts != 0 || transport.waitCalls != 1 {
		t.Fatalf("start commit-window recovery was not idempotent: record=%#v acquire=%d stage=%d startCalls=%d starts=%d waits=%d",
			result, store.acquireCalls, transport.stageCalls, transport.startCalls,
			transport.starts, transport.waitCalls)
	}
	assertDockerLifecycleEventOrder(t, recorder.snapshot(), "acquire", "transition:started")
	for _, event := range recorder.snapshot() {
		if event == "mutate:start" {
			t.Fatalf("start commit-window recovery duplicated start: events=%v",
				recorder.snapshot())
		}
	}
}

func TestDockerContainerLifecycleSupervisorAbsentAfterCleaningCommitsOneReceipt(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "absent-recovery")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	intent, lease, resourceID := newDockerLifecycleRecoveryIntent(t, plan, request, now)
	exitCode := 0
	created := newDockerLifecycleTransition(t, intent.ID, 1, lease,
		sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, nil, resourceID, "", now.Add(-80*time.Second))
	started := newDockerLifecycleTransition(t, intent.ID, 2, lease,
		sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted, nil, resourceID,
		created.TransitionFingerprint, now.Add(-70*time.Second))
	exited := newDockerLifecycleTransition(t, intent.ID, 3, lease,
		sandbox.DockerContainerLifecycleTransitionExited,
		sandbox.DockerContainerLifecycleReasonNaturalExit, &exitCode, resourceID,
		started.TransitionFingerprint, now.Add(-60*time.Second))
	cleaning := newDockerLifecycleTransition(t, intent.ID, 4, lease,
		sandbox.DockerContainerLifecycleTransitionCleaning,
		sandbox.DockerContainerLifecycleReasonNaturalExit, nil, "",
		exited.TransitionFingerprint, now.Add(-50*time.Second))
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder, found: true,
		record: sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
			Transitions: []sandbox.DockerContainerLifecycleTransition{
				created, started, exited, cleaning,
			}}}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, resourceID)
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	first, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Receipt == nil || second.Receipt == nil || !second.Replayed ||
		store.completeCalls != 1 || store.acquireCalls != 1 || transport.cleanup != 0 {
		t.Fatalf("absent recovery was not idempotent: first=%#v second=%#v completes=%d acquires=%d cleanup=%d",
			first, second, store.completeCalls, store.acquireCalls, transport.cleanup)
	}
}

func TestDockerContainerLifecycleSupervisorCleanedWithoutReceiptTakeover(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "cleaned-no-receipt")
	recorder := &dockerLifecycleTestRecorder{}
	now := time.Now().UTC()
	intent, lease, resourceID := newDockerLifecycleRecoveryIntent(t, plan, request, now)
	exitCode := 0
	createAction := newDockerLifecyclePreparedAction(t, intent.ID, 1, lease,
		sandbox.DockerContainerLifecycleActionCreate, now.Add(-119*time.Second))
	created := newDockerLifecycleTransition(t, intent.ID, 1, lease,
		sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated, nil, resourceID, "",
		now.Add(-115*time.Second))
	startAction := newDockerLifecyclePreparedAction(t, intent.ID, 2, lease,
		sandbox.DockerContainerLifecycleActionStart, now.Add(-112*time.Second))
	started := newDockerLifecycleTransition(t, intent.ID, 2, lease,
		sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted, nil, resourceID,
		created.TransitionFingerprint, now.Add(-108*time.Second))
	exited := newDockerLifecycleTransition(t, intent.ID, 3, lease,
		sandbox.DockerContainerLifecycleTransitionExited,
		sandbox.DockerContainerLifecycleReasonNaturalExit, &exitCode, resourceID,
		started.TransitionFingerprint, now.Add(-100*time.Second))
	cleaning := newDockerLifecycleTransition(t, intent.ID, 4, lease,
		sandbox.DockerContainerLifecycleTransitionCleaning,
		sandbox.DockerContainerLifecycleReasonNaturalExit, nil, "",
		exited.TransitionFingerprint, now.Add(-95*time.Second))
	deleteAction := newDockerLifecyclePreparedAction(t, intent.ID, 3, lease,
		sandbox.DockerContainerLifecycleActionDelete, now.Add(-90*time.Second))
	cleaned := newDockerLifecycleTransition(t, intent.ID, 5, lease,
		sandbox.DockerContainerLifecycleTransitionCleaned,
		sandbox.DockerContainerLifecycleReasonCleanupCompleted, nil, "",
		cleaning.TransitionFingerprint, now.Add(-85*time.Second))
	record := sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
		Actions: []sandbox.DockerContainerLifecyclePreparedAction{
			createAction, startAction, deleteAction,
		},
		Transitions: []sandbox.DockerContainerLifecycleTransition{
			created, started, exited, cleaning, cleaned,
		}}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder,
		found: true, record: record}
	transport := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, resourceID)
	supervisor := newDockerLifecycleSupervisorForTest(t, store, transport, plan, request, now)

	first, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := supervisor.RecoverOne(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Receipt == nil || second.Receipt == nil || !second.Replayed ||
		first.Receipt.FinalTransitionFingerprint != cleaned.TransitionFingerprint ||
		first.Receipt.Outcome != sandbox.DockerContainerLifecycleOutcomeNaturalExit ||
		store.acquireCalls != 1 || store.completeCalls != 1 || transport.cleanup != 0 ||
		len(first.Transitions) != len(record.Transitions) {
		t.Fatalf("cleaned-without-receipt takeover was not idempotent: first=%#v second=%#v acquires=%d completes=%d cleanup=%d",
			first, second, store.acquireCalls, store.completeCalls, transport.cleanup)
	}
}

func TestDockerContainerLifecycleSupervisorCleaningRecoveryTerminatesWithStickyOutcome(t *testing.T) {
	plan, request := newDockerLifecycleSupervisorPlan(t, "cleaning-sticky-recovery")
	tests := []struct {
		name    string
		reason  string
		outcome string
	}{
		{name: "timeout", reason: sandbox.DockerContainerLifecycleReasonTimeout,
			outcome: sandbox.DockerContainerLifecycleOutcomeTimedOut},
		{name: "cancelled", reason: sandbox.DockerContainerLifecycleReasonCancelled,
			outcome: sandbox.DockerContainerLifecycleOutcomeCancelled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &dockerLifecycleTestRecorder{}
			now := time.Now().UTC()
			intent, lease, resourceID := newDockerLifecycleRecoveryIntent(t, plan, request, now)
			createAction := newDockerLifecyclePreparedAction(t, intent.ID, 1, lease,
				sandbox.DockerContainerLifecycleActionCreate, now.Add(-119*time.Second))
			created := newDockerLifecycleTransition(t, intent.ID, 1, lease,
				sandbox.DockerContainerLifecycleTransitionCreated,
				sandbox.DockerContainerLifecycleReasonCreated, nil, resourceID, "",
				now.Add(-115*time.Second))
			startAction := newDockerLifecyclePreparedAction(t, intent.ID, 2, lease,
				sandbox.DockerContainerLifecycleActionStart, now.Add(-112*time.Second))
			started := newDockerLifecycleTransition(t, intent.ID, 2, lease,
				sandbox.DockerContainerLifecycleTransitionStarted,
				sandbox.DockerContainerLifecycleReasonStarted, nil, resourceID,
				created.TransitionFingerprint, now.Add(-108*time.Second))
			cleaning := newDockerLifecycleTransition(t, intent.ID, 3, lease,
				sandbox.DockerContainerLifecycleTransitionCleaning, test.reason, nil, "",
				started.TransitionFingerprint, now.Add(-100*time.Second))
			record := sandbox.DockerContainerLifecycleRecord{Intent: intent, Lease: lease,
				Actions: []sandbox.DockerContainerLifecyclePreparedAction{
					createAction, startAction,
				},
				Transitions: []sandbox.DockerContainerLifecycleTransition{
					created, started, cleaning,
				}}
			if err := record.Validate(); err != nil {
				t.Fatal(err)
			}
			store := &dockerLifecycleSupervisorTestStore{now: now, recorder: recorder,
				found: true, record: record}
			transport := newDockerLifecycleSupervisorTransport(t, recorder,
				sandbox.DockerContainerLifecycleStateRunning, resourceID)
			supervisor := newDockerLifecycleSupervisorForTest(t, store, transport,
				plan, request, now)

			first, err := supervisor.RecoverOne(context.Background(), intent.ID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := supervisor.RecoverOne(context.Background(), intent.ID)
			if err != nil {
				t.Fatal(err)
			}
			exited := latestLifecycleTransition(first,
				sandbox.DockerContainerLifecycleTransitionExited)
			if first.Receipt == nil || second.Receipt == nil || !second.Replayed ||
				first.Receipt.Outcome != test.outcome || exited == nil ||
				exited.ReasonCode != test.reason || transport.waitCalls != 0 ||
				transport.terminate != 1 || transport.terms != 1 || transport.deletes != 1 ||
				store.acquireCalls != 1 || store.completeCalls != 1 {
				t.Fatalf("%s cleaning recovery lost sticky outcome or repeated cleanup: first=%#v second=%#v exited=%#v wait=%d terminate=%d term=%d delete=%d acquire=%d complete=%d",
					test.name, first, second, exited, transport.waitCalls,
					transport.terminate, transport.terms, transport.deletes,
					store.acquireCalls, store.completeCalls)
			}
		})
	}
}

func TestDockerContainerLifecycleApplicationErrorCodesAreStableAndFailClosed(t *testing.T) {
	tests := []struct {
		reason string
		want   apperror.Code
	}{
		{sandbox.DockerContainerLifecycleFailureDisabled, apperror.CodeUnavailable},
		{sandbox.DockerContainerLifecycleFailureUnsupported, apperror.CodeUnavailable},
		{sandbox.DockerContainerLifecycleFailureConnection, apperror.CodeUnavailable},
		{sandbox.DockerContainerLifecycleFailureInvalidResponse, apperror.CodeFailedPrecondition},
		{sandbox.DockerContainerLifecycleFailureConfigMismatch, apperror.CodeFailedPrecondition},
		{sandbox.DockerContainerLifecycleFailureUnsafeExisting, apperror.CodeFailedPrecondition},
		{sandbox.DockerContainerLifecycleReasonTimeout, apperror.CodeDeadlineExceeded},
		{sandbox.DockerContainerLifecycleReasonCancelled, apperror.CodeCancelled},
	}
	for _, test := range tests {
		if got := dockerLifecycleApplicationCode(test.reason); got != test.want {
			t.Errorf("reason %q mapped to %q, want %q", test.reason, got, test.want)
		}
	}
}

func newDockerLifecycleSupervisorPlan(t *testing.T, prefix string,
) (sandbox.DockerContainerPlan, sandbox.DockerContainerWriteRequest) {
	t.Helper()
	ctx := context.Background()
	st, run, root := newSandboxManifestTestRuntime(t, ctx)
	service := NewSandboxManifestService(st, policy.NewDefaultChecker())
	requestedBy := "docker_lifecycle_operator"
	manifest, observation := prepareDockerContainerPlanAuthority(t, ctx, service, run.ID,
		root, prefix, requestedBy)
	service.WithDockerContainerTransactionHarness(sandbox.NewInMemoryDockerWriteTransaction())
	plan, err := service.CompileDockerContainerPlan(ctx, CompileDockerContainerPlanRequest{
		ObservationID: observation.ID,
		Manifest:      manifest,
		OperationKey:  prefix + "-plan",
		RequestedBy:   requestedBy,
	})
	if err != nil {
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
	return plan, request
}

func newDockerLifecycleSupervisorTransport(t *testing.T,
	recorder *dockerLifecycleTestRecorder, state, resourceID string,
) *dockerLifecycleSupervisorTestTransport {
	t.Helper()
	endpoint, err := sandbox.NewDockerObservationEndpoint(
		sandbox.DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	return &dockerLifecycleSupervisorTestTransport{endpoint: endpoint, recorder: recorder,
		state: state, resourceID: resourceID}
}

func newDockerLifecycleSupervisorForTest(t *testing.T,
	store *dockerLifecycleSupervisorTestStore,
	transport *dockerLifecycleSupervisorTestTransport,
	plan sandbox.DockerContainerPlan, request sandbox.DockerContainerWriteRequest,
	now time.Time,
) *DockerContainerLifecycleSupervisor {
	t.Helper()
	supervisor, err := NewDockerContainerLifecycleSupervisor(store, transport,
		dockerLifecycleSupervisorTestAuthority{plan: plan, request: request},
		"docker-lifecycle-supervisor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	supervisor.now = func() time.Time { return now }
	return supervisor
}

func newDockerLifecycleRecoveryIntent(t *testing.T,
	plan sandbox.DockerContainerPlan, request sandbox.DockerContainerWriteRequest,
	now time.Time,
) (sandbox.DockerContainerLaunchIntent, sandbox.DockerContainerLifecycleLease, string) {
	t.Helper()
	endpoint, err := sandbox.NewDockerObservationEndpoint(
		sandbox.DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := sandbox.NewDockerContainerLaunchIntent(
		"docker-lifecycle-recovery-intent", "docker-lifecycle-recovery-attempt",
		strings.Repeat("b", 64), plan, request, endpoint, plan.RequestedBy, 1,
		now.Add(-3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := sandbox.NewDockerContainerLifecycleLease(intent,
		"docker-lifecycle-expired-lease", "crashed-supervisor", 1,
		now.Add(-2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := sandbox.NewDockerContainerStageResult(endpoint, request,
		strings.Repeat("c", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	return intent, lease, stage.ContainerIDFingerprint
}

func newDockerLifecycleTransition(t *testing.T, intentID string, ordinal int,
	lease sandbox.DockerContainerLifecycleLease, state, reason string, exitCode *int,
	resourceID, previous string, at time.Time,
) sandbox.DockerContainerLifecycleTransition {
	t.Helper()
	transition, err := sandbox.NewDockerContainerLifecycleTransition(intentID, ordinal,
		lease, state, reason, exitCode, resourceID, previous, at)
	if err != nil {
		t.Fatal(err)
	}
	return transition
}

func newDockerLifecyclePreparedAction(t *testing.T, intentID string, ordinal int,
	lease sandbox.DockerContainerLifecycleLease,
	action sandbox.DockerContainerLifecycleActionKind, at time.Time,
) sandbox.DockerContainerLifecyclePreparedAction {
	t.Helper()
	prepared, err := sandbox.NewDockerContainerLifecyclePreparedAction(intentID, ordinal,
		lease, string(action), at)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func assertDockerLifecycleEventOrder(t *testing.T, events []string, expected ...string) {
	t.Helper()
	position := 0
	for _, event := range events {
		if position < len(expected) && event == expected[position] {
			position++
		}
	}
	if position != len(expected) {
		t.Fatalf("Docker lifecycle event order mismatch: expected=%v events=%v", expected, events)
	}
}
