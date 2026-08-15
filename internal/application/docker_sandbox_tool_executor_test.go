package application

import (
	"testing"

	"cyberagent-workbench/internal/toolgateway"
)

func dockerSandboxProposalTestScope(
	fixture dockerSandboxServiceFixture,
) toolgateway.DockerSandboxProposalContext {
	return toolgateway.DockerSandboxProposalContext{
		InvocationID: "toolcall-docker-proposal", OperationKey: "model-docker-proposal",
		RunID: fixture.plan.RunID, RootAgentID: "agent-root",
		SessionID: "session-docker-proposal", WorkspaceID: fixture.plan.WorkspaceID,
		LeaseID: "lease-docker-proposal", LeaseGeneration: 1,
		RequestedBy: "run_supervisor",
		PolicyDecision: toolgateway.Decision{Allowed: true,
			Approval: toolgateway.ApprovalAutomatic, Risk: "medium",
			Reason: "allowed by product proposal policy"},
	}
}

func TestDockerSandboxProposalExecutorAdmitsWithoutStarting(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "model-proposal")
	executor, err := NewDockerSandboxProposalExecutor(fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ProposeDockerSandbox(t.Context(),
		dockerSandboxProposalTestScope(fixture),
		toolgateway.DockerSandboxProposalSpec{
			Version: toolgateway.DockerSandboxRunProposalVersion,
			PlanID:  fixture.plan.ID, Manifest: fixture.manifest,
		})
	if err != nil || !result.Allowed || result.AdmissionID == "" || result.Replayed {
		t.Fatalf("Docker Sandbox model proposal failed: %#v err=%v", result, err)
	}
	record, err := fixture.service.Get(t.Context(), result.AdmissionID)
	if err != nil || record.Admission.RunID != fixture.plan.RunID ||
		record.Admission.WorkspaceID != fixture.plan.WorkspaceID ||
		record.Admission.PlanID != fixture.plan.ID || record.Launch != nil ||
		record.Receipt != nil {
		t.Fatalf("proposal did not stop at exact admission: %#v err=%v", record, err)
	}
	if fixture.lifecycle.stageCalls != 0 || fixture.lifecycle.startCalls != 0 ||
		fixture.lifecycle.waitCalls != 0 || fixture.lifecycle.cleanup != 0 ||
		fixture.lifecycle.creates != 0 || fixture.lifecycle.starts != 0 ||
		fixture.lifecycle.deletes != 0 || fixture.io.ownedAttaches != 0 ||
		fixture.io.ownedExports != 0 {
		t.Fatalf("model proposal reached Docker execution: lifecycle=%#v io=%#v",
			fixture.lifecycle, fixture.io)
	}
}

func TestDockerSandboxProposalExecutorRejectsScopeMismatchBeforeAdmission(t *testing.T) {
	fixture := newDockerSandboxServiceFixture(t, "model-scope-mismatch")
	executor, err := NewDockerSandboxProposalExecutor(fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	scope := dockerSandboxProposalTestScope(fixture)
	scope.WorkspaceID = "different-workspace"
	_, err = executor.ProposeDockerSandbox(t.Context(), scope,
		toolgateway.DockerSandboxProposalSpec{
			Version: toolgateway.DockerSandboxRunProposalVersion,
			PlanID:  fixture.plan.ID, Manifest: fixture.manifest,
		})
	if err == nil {
		t.Fatal("Workspace-mismatched model proposal was admitted")
	}
	if fixture.lifecycle.creates != 0 || fixture.lifecycle.starts != 0 ||
		fixture.lifecycle.deletes != 0 {
		t.Fatalf("scope mismatch reached Docker: %#v", fixture.lifecycle)
	}

	// The same operation remains usable by the exact scope, demonstrating that
	// the mismatched call was rejected before Admit appended an admission.
	result, err := executor.ProposeDockerSandbox(t.Context(),
		dockerSandboxProposalTestScope(fixture),
		toolgateway.DockerSandboxProposalSpec{
			Version: toolgateway.DockerSandboxRunProposalVersion,
			PlanID:  fixture.plan.ID, Manifest: fixture.manifest,
		})
	if err != nil || !result.Allowed || result.Replayed {
		t.Fatalf("exact scope could not use untouched operation: %#v err=%v", result, err)
	}
}
