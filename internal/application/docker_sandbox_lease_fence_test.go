package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/sandbox"
)

type dockerLeaseFenceTransport struct {
	*dockerLifecycleSupervisorTestTransport
	check func(context.Context, sandbox.DockerContainerLifecycleActionKind, sandbox.DockerContainerLifecycleFence) error
}

func (transport *dockerLeaseFenceTransport) StageOwned(ctx context.Context,
	request sandbox.DockerContainerWriteRequest, ownership sandbox.DockerContainerLifecycleOwnership,
	fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerStageResult, error) {
	return transport.dockerLifecycleSupervisorTestTransport.StageOwned(ctx, request, ownership,
		func(ctx context.Context, action sandbox.DockerContainerLifecycleActionKind) error {
			return transport.check(ctx, action, fence)
		})
}

func (transport *dockerLeaseFenceTransport) Start(ctx context.Context,
	request sandbox.DockerContainerLifecycleRequest, fence sandbox.DockerContainerLifecycleFence,
) (sandbox.DockerContainerLifecycleObservation, bool, error) {
	return transport.dockerLifecycleSupervisorTestTransport.Start(ctx, request,
		func(ctx context.Context, action sandbox.DockerContainerLifecycleActionKind) error {
			return transport.check(ctx, action, fence)
		})
}

func TestDockerSandboxNativeWritesRequireActiveAdmissionLease(t *testing.T) {
	for _, phase := range []sandbox.DockerContainerLifecycleActionKind{sandbox.DockerContainerLifecycleActionCreate, sandbox.DockerContainerLifecycleActionStart} {
		t.Run(string(phase), func(t *testing.T) {
			ctx := t.Context()
			fixture := newDockerSandboxServiceFixture(t, "native-lease-fence")
			if _, err := NewRunService(fixture.store).Start(ctx, fixture.plan.RunID); err != nil {
				t.Fatal(err)
			}
			acquired, err := fixture.store.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
				RunID: fixture.plan.RunID, OwnerID: "docker-admission-owner", TTL: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := fixture.store.GetWorkspaceByID(ctx, fixture.plan.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			manifests := NewSandboxManifestService(fixture.store, policy.NewDefaultChecker()).
				WithDockerContainerTransactionHarness(sandbox.NewInMemoryDockerWriteTransaction())
			manifest, observation := prepareDockerContainerPlanAuthority(t, ctx, manifests,
				fixture.plan.RunID, workspace.RootPath, "leased-native-fence", fixture.requestedBy, acquired.Lease)
			plan, err := manifests.CompileDockerContainerPlan(ctx, CompileDockerContainerPlanRequest{
				ObservationID: observation.ID, Manifest: manifest, OperationKey: "leased-native-plan", RequestedBy: fixture.requestedBy,
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := fixture.store.GetSandboxExecutionCandidate(ctx, plan.CandidateID)
			if err != nil || !executionCandidateMatchesRunLease(candidate.Candidate, acquired.Lease) {
				t.Fatalf("candidate did not bind original lease: %+v %v", candidate, err)
			}
			checked := false
			var denied error
			fixture.service.lifecycleTransport = &dockerLeaseFenceTransport{
				dockerLifecycleSupervisorTestTransport: fixture.lifecycle,
				check: func(ctx context.Context, action sandbox.DockerContainerLifecycleActionKind, fence sandbox.DockerContainerLifecycleFence) error {
					if action != phase || checked {
						return fence(ctx, action)
					}
					if err := fence(ctx, action); err != nil {
						t.Fatalf("valid admission was already denied before release: %v", err)
					}
					if _, _, err := fixture.store.ReleaseRunExecutionLease(ctx, acquired.Lease); err != nil {
						t.Fatal(err)
					}
					_, err := fixture.service.loadCurrentDockerSandboxAuthority(ctx, plan.ID, manifest, fixture.requestedBy)
					if apperror.CodeOf(err) != apperror.CodeConflict || !strings.Contains(err.Error(), "Docker Sandbox Run lease authority changed") {
						t.Fatalf("release did not fail specifically at the admission lease: %v", err)
					}
					checked = true
					denied = fence(ctx, action)
					if denied == nil || ctx.Err() != nil {
						t.Fatalf("native fence accepted released lease or relied on cancellation: %v ctx=%v", denied, ctx.Err())
					}
					return denied
				},
			}
			admitted, err := fixture.service.Admit(ctx, DockerSandboxAdmissionRequest{
				PlanID: plan.ID, Manifest: manifest, OperationKey: "leased-native-admit", RequestedBy: fixture.requestedBy,
			})
			if err != nil || !admitted.Allowed || admitted.Admission == nil {
				t.Fatalf("admission failed: %+v %v", admitted, err)
			}
			result, err := fixture.service.Start(ctx, DockerSandboxStartRequest{
				AdmissionID: admitted.Admission.ID, OperationKey: "leased-native-start", RequestedBy: fixture.requestedBy,
			})
			wantCreated := 0
			if phase == sandbox.DockerContainerLifecycleActionStart {
				wantCreated = 1
			}
			if !checked || denied == nil || result.Record.Receipt == nil || result.Record.Receipt.Outcome != domain.DockerSandboxOutcomeFailed ||
				fixture.lifecycle.creates != wantCreated || fixture.lifecycle.starts != 0 ||
				fixture.lifecycle.terms != 0 || fixture.lifecycle.deletes != wantCreated || fixture.lifecycle.waitCalls != 0 ||
				fixture.io.ownedAttaches != 0 || fixture.io.ownedExports != 0 {
				t.Fatalf("released admission reached native write: checked=%t denied=%v result=%+v err=%v lifecycle=%+v", checked, denied, result, err, fixture.lifecycle)
			}
		})
	}
}
