package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
)

func commandDockerProofFixture(t *testing.T) (*commandApprovalSandboxFixture, *DockerSandboxCommandRuntimeExecutor, runner.CommandRuntimeScope, runner.CommandRuntimeResolvedSpec) {
	t.Helper()
	f := newCommandApprovalSandboxFixture(t, domain.RunExecutionPermissionAsk, domain.RunExecutionProfileDocker, true)
	e := f.service.sandbox.(*DockerSandboxCommandRuntimeExecutor)
	input := commandApprovalSandboxInput(t)
	spec, err := runner.NormalizeCommandRuntimeSpec(input.Commands[0], f.owned.Path)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := f.st.GetRunExecutionProfile(t.Context(), f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := f.st.GetRunExecutionPermission(t.Context(), f.turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := f.st.GetRunExecutionLease(t.Context(), f.turn.Run.ID)
	if err != nil || !found {
		t.Fatalf("lease found=%t error=%v", found, err)
	}
	scope := runner.CommandRuntimeScope{InvocationID: "docker-proof-invocation", OperationKey: "docker-proof-operation", RunID: f.turn.Run.ID, MissionID: f.turn.Mission.ID,
		RootAgentID: f.turn.Agent.ID, AgentID: f.turn.Agent.ID, AttributionSource: domain.AgentAttributionOperatorRoot, SessionID: f.turn.Run.SessionID,
		WorkspaceID: f.owned.SourceWorkspaceID, WorkspaceRootSHA256: spec.WorkspaceRootSHA256, ModeSnapshotID: f.turn.Mode.ID, ModeRevision: f.turn.Mode.Revision,
		ProfileSnapshotID: profile.ID, ProfileRevision: profile.Revision, PermissionSnapshotID: permission.ID, PermissionRevision: permission.Revision,
		PermissionMode: permission.Mode, LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation, LeaseOwnerID: lease.OwnerID, Adapter: e.Identity()}
	if err := scope.Validate(); err != nil {
		t.Fatal(err)
	}
	return f, e, scope, spec
}

func assertDockerProofNoMutation(t *testing.T, f *commandApprovalSandboxFixture) {
	t.Helper()
	for _, event := range f.recorder.snapshot() {
		if strings.HasPrefix(event, "mutate:") {
			t.Fatalf("pre-dispatch rejection mutated daemon: %s", event)
		}
	}
}

func TestDockerCommandPreDispatchAuthorityRejectionHasNoContainerToReap(t *testing.T) {
	for _, kind := range []string{"permission", "lease"} {
		t.Run(kind, func(t *testing.T) {
			f, e, scope, spec := commandDockerProofFixture(t)
			if kind == "permission" {
				scope.PermissionMode = domain.RunExecutionPermissionConservative
			} else {
				scope.LeaseGeneration++
			}
			result, err := e.ExecuteSandboxCommand(t.Context(), scope, spec, nil)
			if err == nil || !result.TreeReaped || result.ExitCode != 125 {
				t.Fatalf("pre-dispatch rejection became uncertain cleanup: result=%+v error=%v", result, err)
			}
			assertDockerProofNoMutation(t, f)
		})
	}
}

type commandDockerPreflightFailStore struct {
	StandardCodeDockerStore
	reads int
}

func (s *commandDockerPreflightFailStore) GetRun(ctx context.Context, id string) (domain.Run, error) {
	s.reads++
	if s.reads == 2 {
		return domain.Run{}, errors.New("controlled execute preflight lookup failure")
	}
	return s.StandardCodeDockerStore.GetRun(ctx, id)
}

func TestDockerCommandInternalPreflightFailurePreservesKnownNoDispatch(t *testing.T) {
	f, e, scope, spec := commandDockerProofFixture(t)
	e.service.store = &commandDockerPreflightFailStore{StandardCodeDockerStore: e.service.store}
	result, err := e.ExecuteSandboxCommand(t.Context(), scope, spec, nil)
	if err == nil || !result.TreeReaped || result.ExitCode != 125 {
		t.Fatalf("internal preflight failure became uncertain cleanup: result=%+v error=%v", result, err)
	}
	assertDockerProofNoMutation(t, f)
}

type commandDockerCleanupFailTransport struct {
	*dockerLifecycleSupervisorTestTransport
}

func (*commandDockerCleanupFailTransport) Cleanup(context.Context, sandbox.DockerContainerLifecycleRequest, sandbox.DockerContainerLifecycleFence) (sandbox.DockerContainerLifecycleCleanupResult, error) {
	return sandbox.DockerContainerLifecycleCleanupResult{}, errors.New("controlled Docker cleanup failure")
}

func TestDockerCommandCleanupFailureDoesNotClaimReapedContainer(t *testing.T) {
	f, e, scope, spec := commandDockerProofFixture(t)
	base := e.service.docker.lifecycleTransport.(*dockerLifecycleSupervisorTestTransport)
	e.service.docker.lifecycleTransport = &commandDockerCleanupFailTransport{base}
	result, err := e.ExecuteSandboxCommand(t.Context(), scope, spec, nil)
	if err == nil || result.TreeReaped || result.ExitCode != 125 || base.creates != 1 || base.starts != 1 || base.state == sandbox.DockerContainerLifecycleStateAbsent {
		t.Fatalf("cleanup failure became a terminal receipt: result=%+v creates=%d starts=%d state=%s error=%v calls=%v", result, base.creates, base.starts, base.state, err, f.recorder.snapshot())
	}
}

type commandDockerPostCleanupFailStore struct{ StandardCodeDockerStore }

func (*commandDockerPostCleanupFailStore) GetDockerLogCaptureReceiptByAttempt(context.Context, string) (sandbox.DockerLogCaptureReceipt, bool, error) {
	return sandbox.DockerLogCaptureReceipt{}, false, errors.New("controlled post-cleanup log receipt lookup failure")
}

func TestDockerCommandPostCleanupFailureRetainsConfirmedTreeProof(t *testing.T) {
	f, e, scope, spec := commandDockerProofFixture(t)
	e.service.store = &commandDockerPostCleanupFailStore{e.service.store}
	base := e.service.docker.lifecycleTransport.(*dockerLifecycleSupervisorTestTransport)
	result, err := e.ExecuteSandboxCommand(t.Context(), scope, spec, nil)
	if err == nil || !result.TreeReaped || result.ExitCode != 125 || base.creates != 1 || base.starts != 1 || base.state != sandbox.DockerContainerLifecycleStateAbsent {
		t.Fatalf("confirmed cleanup was lost to result finalization: result=%+v creates=%d starts=%d state=%s error=%v calls=%v", result, base.creates, base.starts, base.state, err, f.recorder.snapshot())
	}
}
