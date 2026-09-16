package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
	workspacefs "cyberagent-workbench/internal/workspace"
)

func TestHostPlanBoundaryHidesAndRejectsForgedCalls(t *testing.T) {
	for _, permission := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionApproval, domain.RunExecutionPermissionWorkspaceAccess,
	} {
		t.Run(string(permission), func(t *testing.T) {
			for _, phase := range []domain.ExecutionPhase{domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver} {
				foundHost, foundPlan := false, false
				for _, spec := range supervisorStructuredToolSpecs(domain.ExecutionSurfaceCode, phase, permission, false, false) {
					foundHost = foundHost || spec.Name == string(toolgateway.HostCommandProposeTool)
					foundPlan = foundPlan || spec.Name == string(toolgateway.PlanDeliveryProposeTool)
				}
				if foundHost != (phase == domain.ExecutionPhaseDeliver) || foundPlan != (phase == domain.ExecutionPhasePlan) {
					t.Errorf("phase=%s host=%t plan=%t", phase, foundHost, foundPlan)
				}
			}
			// The phase boundary precedes payload parsing or authority creation.
			calls := []llm.ToolCall{{ID: "forged-host", Name: string(toolgateway.HostCommandProposeTool), Arguments: json.RawMessage(`{}`)}}
			prepared, err := prepareSupervisorToolCalls(calls, "run-plan", 1, 1,
				domain.ExecutionSurfaceCode, domain.ExecutionPhasePlan, permission, false, false)
			if err == nil || !strings.Contains(err.Error(), "Deliver") || len(prepared) != 0 {
				t.Fatalf("Plan host call was not rejected at the phase boundary: %#v %v", prepared, err)
			}
		})
	}
}

type hostPlanMutationStore struct {
	*hostCommandProposalReviewStoreStub
	creates int
}

func (s *hostPlanMutationStore) CreateHostCommandProposal(_ context.Context,
	_ runner.HostCommandProposalOperation, proposal runner.HostCommandProposal,
) (runner.HostCommandProposal, bool, error) {
	s.creates++
	return proposal, false, nil
}

func TestHostPlanBoundaryRejectsProposalBeforeMutationOrShellResolution(t *testing.T) {
	for _, version := range []string{"host_command_proposal.v1", runner.RiskEscalationProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			state := &hostPlanMutationStore{hostCommandProposalReviewStoreStub: hostCommandProposalReviewFixture(t)}
			state.mode.Phase = domain.ExecutionPhasePlan
			scope := toolgateway.HostCommandProposalContext{
				InvocationID: "invocation-plan", OperationKey: "host-plan-001", RunID: state.run.ID,
				RootAgentID: "agent-root", SessionID: state.run.SessionID, WorkspaceID: state.workspace.ID,
				LeaseID: "lease-plan", LeaseGeneration: 1, RequestedBy: "run_supervisor",
				PolicyDecision: toolgateway.Decision{Allowed: true, Approval: toolgateway.ApprovalAutomatic, Risk: "high", Reason: "explicit test scope"},
				SupervisorTurn: 1, SupervisorToolCallID: "tool-plan", RootFingerprint: strings.Repeat("a", 64),
				CapabilityGeneration: strings.Repeat("b", 64), ModeRevision: state.mode.Revision, PermissionRevision: state.permission.Revision,
			}
			payload := toolgateway.HostCommandProposalSpec{Version: version, Transport: "shell", Shell: "powershell",
				Command: "Write-Output 'must never execute in Plan'", WorkingDirectory: state.workspace.RootPath,
				TimeoutMilliseconds: 1000, Purpose: "Plan boundary fixture"}
			if version == runner.RiskEscalationProtocolVersion {
				payload.RiskKinds = []string{"other_high_risk"}
				payload.OtherRiskReason = "Explicit boundary fixture"
				permission, err := state.permission.Next("permission-risk-proposal", domain.RunExecutionPermissionWorkspaceAccess,
					true, "operator", "bounded risk fixture", time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				state.permission = permission
				scope.PermissionRevision = permission.Revision
			}
			if err := payload.Validate(); err != nil {
				t.Fatal(err)
			}
			executor := NewHostCommandProposalToolExecutor(state)
			resolved := 0
			executor.resolveShell = func(string) (string, error) { resolved++; return state.proposal.Spec.ExecutablePath, nil }
			_, err := executor.ProposeHostCommand(t.Context(), scope, payload)
			if apperror.CodeOf(err) != apperror.CodePolicyDenied || !strings.Contains(err.Error(), "Deliver") || state.creates != 0 || resolved != 0 {
				t.Fatalf("Plan proposal crossed the boundary: err=%v writes=%d resolved=%d", err, state.creates, resolved)
			}
		})
	}
}

func TestHostPlanBoundaryRejectsApprovalBeforeReviewAndIntent(t *testing.T) {
	state := hostCommandProposalReviewFixture(t)
	state.mode.Phase = domain.ExecutionPhasePlan
	executor := &hostCommandProposalExecutorStub{}
	service := NewHostCommandProposalReviewService(state, executor, domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
	_, err := service.Review(t.Context(), ReviewHostCommandProposalRequest{
		ProposalID: state.proposal.ID, Decision: "approve", OperationKey: "plan-approve-001",
		ReviewedBy: "desktop_operator", ConfirmExecution: true,
	})
	if apperror.CodeOf(err) != apperror.CodePolicyDenied || state.review != nil || state.intent != nil || executor.calls != 0 {
		t.Fatalf("Plan approval was recorded/executed: err=%v review=%+v intent=%+v calls=%d", err, state.review, state.intent, executor.calls)
	}
	// Denying an old proposal does not authorize work and remains available.
	denied, err := service.Review(t.Context(), ReviewHostCommandProposalRequest{
		ProposalID: state.proposal.ID, Decision: "deny", OperationKey: "plan-host-deny-001", ReviewedBy: "desktop_operator",
	})
	if err != nil || denied.View.Review == nil || denied.View.Review.Decision != runner.HostCommandReviewDeny || state.intent != nil || executor.calls != 0 {
		t.Fatalf("safe Plan denial failed: %+v err=%v", denied, err)
	}
}

func TestHostPlanBoundaryPreservesSealedReviewReplay(t *testing.T) {
	for _, decision := range []string{"approve", "deny"} {
		t.Run(decision, func(t *testing.T) {
			state := hostCommandProposalReviewFixture(t)
			executor := &hostCommandProposalExecutorStub{output: "saved output"}
			service := NewHostCommandProposalReviewService(state, executor, domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
			request := ReviewHostCommandProposalRequest{ProposalID: state.proposal.ID, Decision: decision,
				OperationKey: "sealed-host-review-001", ReviewedBy: "desktop_operator", ConfirmExecution: decision == "approve"}
			original, err := service.Review(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			review, intent, calls := state.review, state.intent, executor.calls
			state.mode.Phase = domain.ExecutionPhasePlan
			state.mode.Revision++
			result, err := service.Review(t.Context(), request)
			if err != nil || !result.ReviewReplayed || result.ExecutionReplayed != (decision == "approve") || executor.calls != calls || state.review != review || state.intent != intent {
				t.Fatalf("sealed replay required new authority or mutated execution: result=%+v err=%v", result, err)
			}
			if decision == "approve" && result.View.Receipt.RequestID != original.View.Receipt.RequestID {
				t.Fatal("replay returned a different receipt")
			}
		})
	}
}

func TestHostPlanBoundaryRejectsLegacyRiskProposalWithMatchingPlanMode(t *testing.T) {
	for _, phase := range []domain.ExecutionPhase{domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver} {
		t.Run(string(phase), func(t *testing.T) {
			state := hostCommandProposalReviewFixture(t)
			state.mode.Phase = phase
			state.run.Status = domain.RunWaitingApproval
			permission, err := state.permission.Next("permission-workspace-access", domain.RunExecutionPermissionWorkspaceAccess,
				true, "operator", "bounded risk fixture", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			state.permission = permission
			root, err := workspacefs.AgentCodeRootFingerprint(state.workspace.RootPath)
			if err != nil {
				t.Fatal(err)
			}
			capability := toolgateway.AgentCodeCapabilities(toolgateway.AgentCodeCapabilityContext{
				RunID: state.run.ID, MissionID: state.mission.ID, RootAgentID: state.proposal.RootAgentID,
				WorkspaceID: state.workspace.ID, RootFingerprint: root, Surface: state.mode.Surface, Phase: phase,
				Role: domain.AgentRoleRoot, Profile: state.mode.Profile, PermissionMode: state.permission.Mode,
				ModeRevision: state.mode.Revision, PermissionRevision: state.permission.Revision,
			})
			scope, err := runner.NewRiskEscalationScope(runner.RiskEscalationScopeRequest{
				Kinds: []runner.RiskEscalationKind{runner.RiskEscalationOtherHighRisk}, OtherReason: "Legacy Plan boundary fixture"})
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := runner.NewRiskEscalationProposal(runner.RiskEscalationProposalRequest{
				ID: "risk-escalation-plan", RunID: state.run.ID, MissionID: state.mission.ID, SessionID: state.run.SessionID,
				WorkspaceID: state.workspace.ID, RootAgentID: state.proposal.RootAgentID, SupervisorTurn: 1,
				SupervisorToolCallID: "tool-risk-plan", ToolInvocationID: "invocation-risk-plan",
				ModeSnapshotID: state.mode.ID, ModeRevision: state.mode.Revision,
				InteractionSnapshotID: state.interaction.ID, InteractionRevision: state.interaction.Revision,
				ExecutionProfileSnapshotID: state.profile.ID, ExecutionProfileRevision: state.profile.Revision,
				Permission: state.permission, WorkspaceRootFingerprint: root, CapabilityGeneration: capability.Generation,
				Spec: state.proposal.Spec, Scope: scope, RequestedBy: "run_supervisor", CreatedAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatal(err)
			}
			service := NewHostCommandProposalReviewService(state, &hostCommandProposalExecutorStub{}, domain.ExecutionPermissionRuntimeCapabilities{})
			_, drift, err := service.loadAndVerifyRiskEscalationBindings(t.Context(), proposal)
			if phase == domain.ExecutionPhasePlan {
				if apperror.CodeOf(err) != apperror.CodeConflict || drift != "mode_drift" {
					t.Fatalf("matching legacy Plan proposal passed execution binding: drift=%q err=%v", drift, err)
				}
			} else if err != nil || drift != "" {
				t.Fatalf("Deliver binding regressed: %q %v", drift, err)
			}
		})
	}
}
