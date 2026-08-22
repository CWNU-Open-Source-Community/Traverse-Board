package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/store"
)

type dockerSandboxReadinessTransport struct {
	endpoint sandbox.DockerObservationEndpoint
	image    string
	mu       sync.Mutex
	calls    int
}

func TestDockerSandboxStandardCodeGitMetadataMaskIsCreatedOnceWithoutOverwrite(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "standard-code-mask")
	mask, err := fixture.service.standardCodeGitMetadataMaskPath()
	if err != nil {
		t.Fatal(err)
	}
	if mask != filepath.Join(fixture.stagingRoot, standardCodeGitMetadataMaskFile) {
		t.Fatalf("mask path escaped fixed staging root: %q", mask)
	}
	content, err := os.ReadFile(mask)
	if err != nil || string(content) != sandbox.DockerStandardCodeGitMetadataMask {
		t.Fatalf("mask content=%q err=%v", content, err)
	}
	if replay, replayErr := fixture.service.standardCodeGitMetadataMaskPath(); replayErr != nil || replay != mask {
		t.Fatalf("mask replay=%q err=%v", replay, replayErr)
	}
	conflictingRoot := t.TempDir()
	conflicting, err := NewDockerSandboxService(fixture.store, fixture.readiness,
		policy.NewDefaultChecker(), sandbox.DockerRuntimeCapabilities{Enabled: true},
		fixture.permission, WithDockerSandboxExecution(fixture.lifecycle, fixture.io,
			conflictingRoot, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	conflictingPath := filepath.Join(conflictingRoot, standardCodeGitMetadataMaskFile)
	if err := os.WriteFile(conflictingPath, []byte("unproven"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := conflicting.standardCodeGitMetadataMaskPath()
	if err != nil || path != conflictingPath {
		t.Fatalf("existing mask lookup=%q err=%v", path, err)
	}
	if content, readErr := os.ReadFile(conflictingPath); readErr != nil ||
		string(content) != "unproven" {
		t.Fatalf("unproven file was overwritten: %q err=%v", content, readErr)
	}
}

func (transport *dockerSandboxReadinessTransport) Endpoint() sandbox.DockerObservationEndpoint {
	return transport.endpoint
}

func (transport *dockerSandboxReadinessTransport) Ping(context.Context) error {
	transport.mu.Lock()
	transport.calls++
	transport.mu.Unlock()
	return nil
}

func (transport *dockerSandboxReadinessTransport) Version(context.Context) (
	sandbox.DockerDaemonVersion, error,
) {
	transport.mu.Lock()
	transport.calls++
	transport.mu.Unlock()
	return sandbox.DockerDaemonVersion{APIVersion: "1.47", MinAPIVersion: "1.24",
		EngineVersion: "27.5.1", GitCommit: "abc123", OSType: "linux",
		Architecture: "amd64"}, nil
}

func (transport *dockerSandboxReadinessTransport) Info(context.Context) (
	sandbox.DockerDaemonInfo, error,
) {
	transport.mu.Lock()
	transport.calls++
	transport.mu.Unlock()
	return sandbox.DockerDaemonInfo{ID: "daemon-id", ServerVersion: "27.5.1",
		OSType: "linux", Architecture: "amd64", NCPU: 8,
		MemoryBytes: 8 * 1024 * 1024 * 1024, PidsLimit: true}, nil
}

func (transport *dockerSandboxReadinessTransport) InspectImage(context.Context,
	string,
) (sandbox.DockerImageInspection, error) {
	transport.mu.Lock()
	transport.calls++
	transport.mu.Unlock()
	return sandbox.DockerImageInspection{ID: "sha256:" + strings.Repeat("a", 64),
		RepoDigests: []string{"example.invalid/workbench@" + transport.image},
		OSType:      "linux", Architecture: "amd64", SizeBytes: 1024,
		User: "65532:65532", RootFSType: "layers", GraphDriver: "overlay2"}, nil
}

func (transport *dockerSandboxReadinessTransport) callCount() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.calls
}

func TestDockerSandboxServiceAdmissionStartIOAndReplay(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "product-success")
	ctx := context.Background()
	admitted, err := fixture.service.Admit(ctx, DockerSandboxAdmissionRequest{
		PlanID: fixture.plan.ID, Manifest: fixture.manifest,
		OperationKey: "product-success-operation", RequestedBy: fixture.requestedBy,
	})
	if err != nil || !admitted.Allowed || admitted.Admission == nil || admitted.Replayed {
		t.Fatalf("Admit() = %#v, %v", admitted, err)
	}
	started, err := fixture.service.Start(ctx, DockerSandboxStartRequest{
		AdmissionID: admitted.Admission.ID, OperationKey: "product-success-start",
		RequestedBy: fixture.requestedBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Record.Receipt == nil ||
		started.Record.Receipt.Outcome != domain.DockerSandboxOutcomeSucceeded ||
		started.Record.Receipt.ReasonCode != domain.DockerSandboxReasonCompleted ||
		started.Record.Receipt.ArtifactCount != 1 ||
		started.Record.Receipt.LogReceiptID == "" ||
		started.Record.Receipt.OutputStagingReceiptID == "" ||
		started.Record.Receipt.OutputCommitReceiptID == "" ||
		fixture.lifecycle.creates != 1 || fixture.lifecycle.starts != 1 ||
		fixture.lifecycle.deletes != 1 || fixture.io.ownedAttaches != 1 ||
		fixture.io.ownedExports != 1 {
		t.Fatalf("product execution did not compose lifecycle and I/O once: record=%#v lifecycle=%#v io=%#v",
			started.Record, fixture.lifecycle, fixture.io)
	}
	replayed, err := fixture.service.Start(ctx, DockerSandboxStartRequest{
		AdmissionID: admitted.Admission.ID, OperationKey: "product-success-start",
		RequestedBy: fixture.requestedBy,
	})
	if err != nil || !replayed.Replayed || replayed.Record.Receipt == nil ||
		fixture.lifecycle.creates != 1 || fixture.lifecycle.starts != 1 ||
		fixture.lifecycle.deletes != 1 || fixture.io.ownedAttaches != 1 ||
		fixture.io.ownedExports != 1 {
		t.Fatalf("terminal replay repeated side effects: result=%#v err=%v", replayed, err)
	}
	if _, err := fixture.service.Start(ctx, DockerSandboxStartRequest{
		AdmissionID: admitted.Admission.ID, OperationKey: "different-start-key",
		RequestedBy: fixture.requestedBy,
	}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("different Start key error=%v, want conflict", err)
	}
}

func TestDockerSandboxServiceDenialIsAuditedAndStickyPerOperation(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "product-denial-audit")
	disabled, err := NewDockerSandboxService(fixture.store, fixture.readiness,
		policy.NewDefaultChecker(), sandbox.DockerRuntimeCapabilities{}, fixture.permission,
		WithDockerSandboxExecution(fixture.lifecycle, fixture.io,
			fixture.stagingRoot, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	request := DockerSandboxAdmissionRequest{PlanID: fixture.plan.ID,
		Manifest: fixture.manifest, OperationKey: "product-denial-audit-operation",
		RequestedBy: fixture.requestedBy}
	first, err := disabled.Admit(context.Background(), request)
	if err != nil || first.Allowed || first.Replayed ||
		first.ReasonCode != domain.DockerSandboxReasonFeatureDisabled {
		t.Fatalf("first denial=%#v err=%v", first, err)
	}
	second, err := disabled.Admit(context.Background(), request)
	if err != nil || second.Allowed || !second.Replayed ||
		second.ReasonCode != first.ReasonCode ||
		second.RemediationCode != first.RemediationCode {
		t.Fatalf("denial replay=%#v err=%v", second, err)
	}
	// Changing the process-local capability cannot turn the same idempotent
	// operation into an authorization; the caller must make a fresh request.
	afterEnable, err := fixture.service.Admit(context.Background(), request)
	if err != nil || afterEnable.Allowed || !afterEnable.Replayed ||
		afterEnable.ReasonCode != domain.DockerSandboxReasonFeatureDisabled {
		t.Fatalf("sticky denial after capability change=%#v err=%v", afterEnable, err)
	}
	eventList, err := fixture.store.ListRunEvents(context.Background(), fixture.plan.RunID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range eventList {
		if event.Type == events.SandboxDockerProductAdmissionDeniedEvent {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("denial events=%d, want 1", count)
	}
}

func TestDockerSandboxServiceRestartDoesNotRestoreStartAuthority(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "product-restart")
	ctx := context.Background()
	admitted, err := fixture.service.Admit(ctx, DockerSandboxAdmissionRequest{
		PlanID: fixture.plan.ID, Manifest: fixture.manifest,
		OperationKey: "product-restart-operation", RequestedBy: fixture.requestedBy,
	})
	if err != nil || admitted.Admission == nil {
		t.Fatalf("Admit() = %#v, %v", admitted, err)
	}
	// A new service receives the same configured feature bit, but generates a
	// fresh process epoch. The persisted admission cannot recreate that bearer.
	restarted := fixture.newService(t)
	result, err := restarted.Start(ctx, DockerSandboxStartRequest{
		AdmissionID: admitted.Admission.ID, OperationKey: "product-restart-operation",
		RequestedBy: fixture.requestedBy,
	})
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition ||
		result.Record.Receipt != nil ||
		fixture.lifecycle.creates != 0 || fixture.lifecycle.starts != 0 ||
		fixture.lifecycle.deletes != 0 || fixture.io.ownedAttaches != 0 ||
		fixture.io.ownedExports != 0 {
		t.Fatalf("restart restored Docker start authority: result=%#v lifecycle=%#v io=%#v",
			result, fixture.lifecycle, fixture.io)
	}
}

func TestDockerSandboxServiceStartWALCrashRetryUsesIndependentKey(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "product-start-wal-retry")
	ctx := context.Background()
	admitted, err := fixture.service.Admit(ctx, DockerSandboxAdmissionRequest{
		PlanID: fixture.plan.ID, Manifest: fixture.manifest,
		OperationKey: "product-start-wal-admit", RequestedBy: fixture.requestedBy,
	})
	if err != nil || admitted.Admission == nil {
		t.Fatalf("Admit() = %#v, %v", admitted, err)
	}
	startKey := "product-start-wal-independent-start"
	start := domain.DockerSandboxStartIntent{
		AdmissionID:     admitted.Admission.ID,
		ProtocolVersion: domain.DockerSandboxStartProtocolVersion,
		OperationKeyDigest: runmutation.Fingerprint(dockerSandboxStartOperationProtocol,
			admitted.Admission.ID, admitted.Admission.RunID, startKey),
		RequestFingerprint: runmutation.Fingerprint(dockerSandboxStartRequestProtocol,
			admitted.Admission.ID, admitted.Admission.RunID, fixture.requestedBy,
			admitted.Admission.AdmissionFingerprint,
			admitted.Admission.RuntimeEpochFingerprint),
		RuntimeEpochFingerprint: admitted.Admission.RuntimeEpochFingerprint,
		RunID:                   admitted.Admission.RunID, RequestedBy: fixture.requestedBy,
		CreatedAt: fixture.service.now().UTC(),
	}
	start.StartFingerprint = domain.DockerSandboxStartFingerprint(start)
	if _, replayed, err := fixture.store.BeginDockerSandboxStart(ctx,
		start); err != nil || replayed {
		t.Fatalf("persist simulated pre-execution WAL: replayed=%t err=%v", replayed, err)
	}
	result, err := fixture.service.Start(ctx, DockerSandboxStartRequest{
		AdmissionID: admitted.Admission.ID, OperationKey: startKey,
		RequestedBy: fixture.requestedBy,
	})
	if err != nil || result.Record.Start == nil || result.Record.Receipt == nil ||
		result.Record.Receipt.Outcome != domain.DockerSandboxOutcomeSucceeded ||
		fixture.lifecycle.creates != 1 || fixture.lifecycle.starts != 1 {
		t.Fatalf("WAL retry result=%#v err=%v lifecycle=%#v",
			result, err, fixture.lifecycle)
	}
}

func TestDockerSandboxServiceAdmissionOnlyCancelIsStickyAndCancelled(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "product-admission-only-cancel")
	ctx := context.Background()
	admitted, err := fixture.service.Admit(ctx, DockerSandboxAdmissionRequest{
		PlanID: fixture.plan.ID, Manifest: fixture.manifest,
		OperationKey: "product-admission-only-admit", RequestedBy: fixture.requestedBy,
	})
	if err != nil || admitted.Admission == nil {
		t.Fatalf("Admit() = %#v, %v", admitted, err)
	}
	request := DockerSandboxCancelRequest{AdmissionID: admitted.Admission.ID,
		OperationKey: "product-admission-only-cancel", RequestedBy: fixture.requestedBy}
	first, err := fixture.service.Cancel(ctx, request)
	if err != nil || first.Record.Receipt == nil ||
		first.Record.Receipt.Outcome != domain.DockerSandboxOutcomeCancelled ||
		first.Record.Start != nil || fixture.lifecycle.creates != 0 ||
		fixture.lifecycle.starts != 0 || fixture.lifecycle.deletes != 0 {
		t.Fatalf("admission-only Cancel()=%#v err=%v lifecycle=%#v",
			first, err, fixture.lifecycle)
	}
	restarted := fixture.newService(t)
	replayed, err := restarted.Cancel(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Record.Receipt == nil ||
		replayed.Record.Receipt.Outcome != domain.DockerSandboxOutcomeCancelled {
		t.Fatalf("restart cancellation replay=%#v err=%v", replayed, err)
	}
	if _, err := restarted.Start(ctx, DockerSandboxStartRequest{
		AdmissionID: admitted.Admission.ID, OperationKey: "late-start",
		RequestedBy: fixture.requestedBy,
	}); err == nil {
		t.Fatal("cancelled admission accepted a later Start")
	}
}

func TestDockerSandboxServiceCancelPersistsBeforeActiveCleanup(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "product-cancel")
	ctx := context.Background()
	admitted, err := fixture.service.Admit(ctx, DockerSandboxAdmissionRequest{
		PlanID: fixture.plan.ID, Manifest: fixture.manifest,
		OperationKey: "product-cancel-operation", RequestedBy: fixture.requestedBy,
	})
	if err != nil || admitted.Admission == nil {
		t.Fatalf("Admit() = %#v, %v", admitted, err)
	}
	waitStarted := make(chan struct{})
	fixture.service.lifecycleTransport = &dockerSandboxBlockingLifecycleTransport{
		dockerLifecycleSupervisorTestTransport: fixture.lifecycle,
		waitStarted:                            waitStarted,
	}
	done := make(chan DockerSandboxStartResult, 1)
	errs := make(chan error, 1)
	go func() {
		result, startErr := fixture.service.Start(ctx, DockerSandboxStartRequest{
			AdmissionID:  admitted.Admission.ID,
			OperationKey: "product-cancel-operation", RequestedBy: fixture.requestedBy,
		})
		done <- result
		errs <- startErr
	}()
	select {
	case <-waitStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("Docker lifecycle did not reach wait")
	}
	cancelled, err := fixture.service.Cancel(ctx, DockerSandboxCancelRequest{
		AdmissionID: admitted.Admission.ID, OperationKey: "product-cancel-request",
		RequestedBy: fixture.requestedBy,
	})
	if err != nil || cancelled.Cancellation.Validate() != nil {
		t.Fatalf("Cancel() = %#v, %v", cancelled, err)
	}
	result := <-done
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if result.Record.Receipt == nil ||
		result.Record.Receipt.Outcome != domain.DockerSandboxOutcomeCancelled ||
		result.Record.Receipt.ReasonCode != domain.DockerSandboxReasonCancelled ||
		fixture.lifecycle.starts != 1 || fixture.lifecycle.terms != 1 ||
		fixture.lifecycle.deletes != 1 || fixture.io.ownedAttaches != 1 ||
		fixture.io.ownedExports != 0 {
		t.Fatalf("durable cancellation did not converge: result=%#v lifecycle=%#v io=%#v",
			result, fixture.lifecycle, fixture.io)
	}
}

type dockerSandboxBlockingLifecycleTransport struct {
	*dockerLifecycleSupervisorTestTransport
	waitStarted chan struct{}
	once        sync.Once
}

func (transport *dockerSandboxBlockingLifecycleTransport) Wait(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest,
	fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleObservation, error) {
	transport.waitCalls++
	if err := fence(ctx, sandbox.DockerContainerLifecycleActionWait); err != nil {
		return sandbox.DockerContainerLifecycleObservation{}, err
	}
	transport.once.Do(func() { close(transport.waitStarted) })
	<-ctx.Done()
	return sandbox.DockerContainerLifecycleObservation{}, ctx.Err()
}

type dockerSandboxServiceFixture struct {
	store       *store.SQLiteStore
	service     *DockerSandboxService
	manifest    sandbox.Manifest
	plan        sandbox.DockerContainerPlan
	requestedBy string
	readiness   sandbox.DockerReadinessProbe
	lifecycle   *dockerLifecycleSupervisorTestTransport
	io          *fakeDockerContainerIOTransport
	permission  domain.ExecutionPermissionRuntimeCapabilities
	stagingRoot string
}

func newDockerSandboxServiceFixture(t *testing.T,
	prefix string,
) dockerSandboxServiceFixture {
	t.Helper()
	ctx := context.Background()
	st, run, root := newSandboxManifestTestRuntime(t, ctx)
	manifestService := NewSandboxManifestService(st, policy.NewDefaultChecker())
	requestedBy := "docker_product_operator"
	manifest, observation := prepareDockerContainerPlanAuthority(t, ctx,
		manifestService, run.ID, root, prefix, requestedBy)
	manifestService.WithDockerContainerTransactionHarness(
		sandbox.NewInMemoryDockerWriteTransaction())
	plan, err := manifestService.CompileDockerContainerPlan(ctx,
		CompileDockerContainerPlanRequest{ObservationID: observation.ID,
			Manifest: manifest, OperationKey: prefix + "-plan", RequestedBy: requestedBy})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunExecutionProfileService(st).Change(ctx,
		ChangeRunExecutionProfileRequest{RunID: run.ID, Profile: "docker",
			OperationKey: prefix + "-profile", RequestedBy: requestedBy,
			Reason: "execute approved Docker Sandbox"}); err != nil {
		t.Fatal(err)
	}
	permission := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true,
	}
	if _, err := NewRunExecutionPermissionService(st, permission).Change(ctx,
		ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: "approval",
			OperationKey: prefix + "-permission", RequestedBy: requestedBy,
			Reason: "execute approved Docker Sandbox", ConfirmUserApproval: true}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := sandbox.NewDockerObservationEndpoint(
		sandbox.DockerObservationEndpointLocalUnix)
	if err != nil {
		t.Fatal(err)
	}
	readTransport := &dockerSandboxReadinessTransport{endpoint: endpoint,
		image: plan.ImageDigest}
	readiness, err := sandbox.NewDockerReadinessProbe(readTransport)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &dockerLifecycleTestRecorder{}
	lifecycle := newDockerLifecycleSupervisorTransport(t, recorder,
		sandbox.DockerContainerLifecycleStateAbsent, "")
	ioTransport := &fakeDockerContainerIOTransport{
		attachBody: dockerLogFramePayload(1, "sandbox stdout\n"),
		exportBody: buildServiceOutputTar(t, map[string]string{"report.json": `{}`}),
	}
	stagingRoot := t.TempDir()
	service, err := NewDockerSandboxService(st, readiness, policy.NewDefaultChecker(),
		sandbox.DockerRuntimeCapabilities{Enabled: true}, permission,
		WithDockerSandboxExecution(lifecycle, ioTransport, stagingRoot, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return dockerSandboxServiceFixture{store: st, service: service, manifest: manifest,
		plan: plan, requestedBy: requestedBy, readiness: readiness,
		lifecycle: lifecycle, io: ioTransport, permission: permission,
		stagingRoot: stagingRoot}
}

func (fixture dockerSandboxServiceFixture) newService(t *testing.T) *DockerSandboxService {
	t.Helper()
	service, err := NewDockerSandboxService(fixture.store, fixture.readiness,
		policy.NewDefaultChecker(), sandbox.DockerRuntimeCapabilities{Enabled: true},
		fixture.permission, WithDockerSandboxExecution(fixture.lifecycle, fixture.io,
			fixture.stagingRoot, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return service
}
