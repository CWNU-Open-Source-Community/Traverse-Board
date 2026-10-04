package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

// These fixtures represent already sealed records. The nil embedded interface
// makes an unexpected grant creation, consumption or native-intent write panic.
// Current-mode native execution is covered by the command runtime tests.
type riskReviewRecoveryStore struct {
	RiskEscalationReviewStore
	proposal                                  runner.RiskEscalationProposal
	record                                    approval.Record
	grant                                     *approval.SessionGrant
	consumption                               *approval.GrantConsumption
	intent                                    *runner.HostExecutionIntent
	result                                    *runner.RiskEscalationResult
	receipt                                   *runner.HostExecutionReceipt
	invalidation                              *runner.RiskEscalationInvalidation
	run                                       domain.Run
	resumeErr, invalidateErr                  error
	resumeCalls, invalidateCalls, decideCalls int
}

func (s *riskReviewRecoveryStore) GetRiskEscalationProposal(context.Context, string) (runner.RiskEscalationProposal, error) {
	return s.proposal, nil
}
func (s *riskReviewRecoveryStore) GetApprovalByProposal(context.Context, string) (approval.Record, error) {
	return s.record, nil
}
func (s *riskReviewRecoveryStore) GetSessionGrant(context.Context, string) (approval.SessionGrant, error) {
	return *s.grant, nil
}
func (s *riskReviewRecoveryStore) GetGrantConsumptionByProposal(context.Context, string) (approval.GrantConsumption, bool, error) {
	if s.consumption == nil {
		return approval.GrantConsumption{}, false, nil
	}
	return *s.consumption, true, nil
}
func (s *riskReviewRecoveryStore) GetRiskEscalationExecutionIntentByProposal(context.Context, string) (runner.HostExecutionIntent, bool, error) {
	if s.intent == nil {
		return runner.HostExecutionIntent{}, false, nil
	}
	return *s.intent, true, nil
}
func (s *riskReviewRecoveryStore) GetRiskEscalationResult(context.Context, string) (runner.RiskEscalationResult, bool, error) {
	if s.result == nil {
		return runner.RiskEscalationResult{}, false, nil
	}
	return *s.result, true, nil
}
func (s *riskReviewRecoveryStore) GetRiskEscalationReceipt(context.Context, string) (runner.HostExecutionReceipt, bool, error) {
	if s.receipt == nil {
		return runner.HostExecutionReceipt{}, false, nil
	}
	return *s.receipt, true, nil
}
func (s *riskReviewRecoveryStore) GetRiskEscalationInvalidation(context.Context, string) (runner.RiskEscalationInvalidation, bool, error) {
	if s.invalidation == nil {
		return runner.RiskEscalationInvalidation{}, false, nil
	}
	return *s.invalidation, true, nil
}
func (s *riskReviewRecoveryStore) InvalidateRiskEscalation(_ context.Context, value runner.RiskEscalationInvalidation) (runner.RiskEscalationInvalidation, bool, error) {
	s.invalidateCalls++
	if s.invalidateErr != nil {
		return runner.RiskEscalationInvalidation{}, false, s.invalidateErr
	}
	s.invalidation = &value
	// The real Store invalidates an attached grant in the same transaction.
	// Reflect that response so recovery cannot depend on the grant staying active.
	if s.grant != nil {
		s.grant.Status, s.grant.RevokedBy, s.grant.RevocationReason = approval.GrantRevoked, "risk_escalation", value.Detail
		s.grant.RevokedAt, s.grant.UpdatedAt = &value.CreatedAt, value.CreatedAt
		s.grant.Version++
	}
	return value, false, nil
}
func (s *riskReviewRecoveryStore) ResumeRiskEscalationRun(context.Context, string, string) (domain.Run, bool, error) {
	s.resumeCalls++
	if s.resumeErr != nil {
		return domain.Run{}, false, s.resumeErr
	}
	replayed := s.run.Status == domain.RunRunning
	s.run.Status = domain.RunRunning
	return s.run, replayed, nil
}
func (s *riskReviewRecoveryStore) DecideApproval(_ context.Context, request approval.DecisionRequest) (approval.DecisionResult, error) {
	s.decideCalls++
	if request.Action != approval.ActionDeny || request.ProposalID != s.proposal.ID || s.record.Status != approval.StatusPending {
		panic("recovery attempted another approval")
	}
	now := time.Now().UTC()
	s.record.Status, s.record.ReviewedBy, s.record.DecisionReason = approval.StatusDenied, request.ReviewedBy, request.Reason
	s.record.UpdatedAt, s.record.DecidedAt = now, &now
	s.record.Version++
	return approval.DecisionResult{Approval: s.record}, nil
}

func newRiskReviewRecoveryFixture(t *testing.T, bounded, completed bool) (*riskReviewRecoveryStore, *HostCommandProposalReviewService, ReviewHostCommandProposalRequest, *hostCommandProposalExecutorStub) {
	t.Helper()
	base := hostCommandProposalReviewFixture(t)
	now := time.Now().UTC()
	permission, err := base.permission.Next("risk-recovery-permission", domain.RunExecutionPermissionWorkspaceAccess, true, "operator", "historical fixture", now)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := runner.NewRiskEscalationScope(runner.RiskEscalationScopeRequest{Kinds: []runner.RiskEscalationKind{runner.RiskEscalationOtherHighRisk}, OtherReason: "retain the operator's original purpose"})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := runner.NewRiskEscalationProposal(runner.RiskEscalationProposalRequest{
		ID: "risk-escalation-recovery", RunID: base.run.ID, MissionID: base.mission.ID, SessionID: base.run.SessionID, WorkspaceID: base.workspace.ID, RootAgentID: "agent-root",
		SupervisorTurn: 1, SupervisorToolCallID: "risk-recovery-call", ToolInvocationID: "risk-recovery-invocation", ModeSnapshotID: base.mode.ID, ModeRevision: base.mode.Revision,
		InteractionSnapshotID: base.interaction.ID, InteractionRevision: base.interaction.Revision, ExecutionProfileSnapshotID: base.profile.ID, ExecutionProfileRevision: base.profile.Revision,
		Permission: permission, WorkspaceRootFingerprint: strings.Repeat("a", 64), CapabilityGeneration: strings.Repeat("b", 64), Spec: base.proposal.Spec, Scope: scope, RequestedBy: "run_supervisor", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &riskReviewRecoveryStore{proposal: proposal, run: base.run}
	s.run.Status = domain.RunWaitingApproval
	s.record = approval.Record{ID: "risk-recovery-approval", IdempotencyKey: "risk-recovery-proposal-key", ProposalID: proposal.ID, RunID: proposal.RunID, SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		ToolName: "host_command_propose", ActionClass: "risk_escalation", Mode: "per_call", Status: approval.StatusApproved, RequestFingerprint: proposal.Fingerprint, RequestedBy: "run_supervisor", ReviewedBy: "operator", Version: 2, CreatedAt: now, UpdatedAt: now, DecidedAt: &now}
	request := ReviewHostCommandProposalRequest{ProposalID: proposal.ID, Decision: "approve", OperationKey: "risk-recovery-review", ReviewedBy: "operator", ConfirmExecution: true, Authorization: "once"}
	if bounded {
		request.Authorization, request.GrantTTLSeconds, request.GrantMaxUses = "run_scope", 120, 3
		expires := now.Add(120 * time.Second)
		grant := approval.SessionGrant{ID: "risk-recovery-grant", RunID: proposal.RunID, SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID, ToolName: "host_command_propose", ActionClass: "risk_escalation", Status: approval.GrantActive,
			RequestFingerprint: strings.Repeat("c", 64), ScopeFingerprint: proposal.Scope.Fingerprint, Generation: 1, MaxUses: 3, UsesRemaining: 2, ExpiresAt: &expires,
			ModeSnapshotID: proposal.ModeSnapshotID, ModeRevision: proposal.ModeRevision, InteractionSnapshotID: proposal.InteractionSnapshotID, InteractionRevision: proposal.InteractionRevision,
			ExecutionProfileSnapshotID: proposal.ExecutionProfileSnapshotID, ExecutionProfileRevision: proposal.ExecutionProfileRevision, PermissionSnapshotID: proposal.PermissionSnapshotID, PermissionRevision: proposal.PermissionRevision,
			PermissionMode: string(proposal.PermissionMode), WorkspaceRootFingerprint: proposal.WorkspaceRootFingerprint, CapabilityGeneration: proposal.CapabilityGeneration, Reason: "retain the operator's original purpose", GrantedBy: "operator", Version: 2, CreatedAt: now, UpdatedAt: now}
		if err := grant.Validate(); err != nil {
			t.Fatal(err)
		}
		consumption := approval.GrantConsumption{ID: "risk-recovery-consumption", GrantID: grant.ID, ProposalID: proposal.ID, ApprovalID: s.record.ID, RunID: proposal.RunID, ScopeFingerprint: proposal.Scope.Fingerprint, GrantGeneration: grant.Generation, UseOrdinal: 1, CreatedAt: now}
		consumption.Fingerprint = approval.GrantConsumptionFingerprint(consumption)
		if err := consumption.Validate(); err != nil {
			t.Fatal(err)
		}
		s.grant, s.consumption, s.record.GrantID = &grant, &consumption, grant.ID
	}
	if err := s.record.Validate(); err != nil {
		t.Fatal(err)
	}
	generation, consumptionID := int64(0), ""
	if s.grant != nil {
		generation, consumptionID = s.grant.Generation, s.consumption.ID
	}
	authorization, err := runner.NewRiskEscalationAuthorization(proposal, s.record.ID, s.record.Version, approval.RecordFingerprint(s.record), s.record.GrantID, generation, consumptionID, s.record.ReviewedBy, now)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := runner.NewRiskEscalationHostExecutionIntent(proposal, authorization, strings.Repeat("d", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	s.intent = &intent
	if completed {
		// Build a sealed receipt without starting a process. Its execution stub
		// is separate from the service's executor, which must never be called.
		seed := &hostCommandProposalExecutorStub{output: "historical output"}
		execution, err := seed.Execute(t.Context(), runner.HostExecutionRequest{Intent: intent})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := runner.ProjectHostExecutionReceipt(execution)
		if err != nil {
			t.Fatal(err)
		}
		result, err := runner.NewRiskEscalationResult("risk-recovery-result", proposal, authorization, intent.RequestID, "completed", "", session.SourceGoCommandResult, "risk-escalation:"+proposal.ID, strings.Repeat("e", 64), false, now)
		if err != nil {
			t.Fatal(err)
		}
		s.receipt, s.result = &receipt, &result
	}
	executor := &hostCommandProposalExecutorStub{}
	service := NewHostCommandProposalReviewService(s, executor, domain.ExecutionPermissionRuntimeCapabilities{})
	return s, service, request, executor
}

func TestRiskReviewDenialRecoveryPropagatesResumeFailure(t *testing.T) {
	for _, decided := range []bool{false, true} {
		name := "pending"
		if decided {
			name = "already_denied"
		}
		t.Run(name, func(t *testing.T) {
			s, service, request, executor := newRiskReviewRecoveryFixture(t, false, false)
			s.intent = nil
			request.Decision, request.ConfirmExecution, request.Authorization = "deny", false, ""
			s.record.Status = approval.StatusDenied
			if !decided {
				s.record.Status, s.record.ReviewedBy, s.record.DecidedAt = approval.StatusPending, "", nil
			}
			failure := errors.New("injected resume transaction failure")
			s.resumeErr = failure
			for i := 0; i < 2; i++ {
				if _, err := service.Review(t.Context(), request); !errors.Is(err, failure) {
					t.Fatalf("resume failure was hidden: %v", err)
				}
				if s.run.Status != domain.RunWaitingApproval || executor.calls != 0 || s.intent != nil {
					t.Fatal("failed denial recovery changed execution state")
				}
			}
			s.resumeErr = nil
			result, err := service.Review(t.Context(), request)
			if err != nil || !result.ReviewReplayed || result.ExecutionReplayed || s.run.Status != domain.RunRunning || executor.calls != 0 {
				t.Fatalf("denial did not recover: %+v %v", result, err)
			}
			wantDecisions := 0
			if !decided {
				wantDecisions = 1
			}
			if s.decideCalls != wantDecisions || s.resumeCalls != 3 {
				t.Fatalf("decision replay changed: decisions=%d resumes=%d", s.decideCalls, s.resumeCalls)
			}
		})
	}
}

func TestRiskReviewCompletedRecoveryPreservesBoundedGrant(t *testing.T) {
	for _, name := range []string{"once", "run_scope", "run_scope_expired"} {
		t.Run(name, func(t *testing.T) {
			bounded := name != "once"
			s, service, request, executor := newRiskReviewRecoveryFixture(t, bounded, true)
			if name == "run_scope_expired" {
				s.grant.CreatedAt = time.Now().UTC().Add(-20 * time.Minute)
				expires := s.grant.CreatedAt.Add(time.Duration(request.GrantTTLSeconds) * time.Second)
				s.grant.ExpiresAt = &expires
				if err := s.grant.Validate(); err != nil {
					t.Fatal(err)
				}
			}
			originalRecord, originalIntent, originalResult := s.record, *s.intent, *s.result
			var originalGrant approval.SessionGrant
			var originalConsumption approval.GrantConsumption
			if bounded {
				originalGrant, originalConsumption = *s.grant, *s.consumption
			}
			failure := errors.New("injected completed-result resume failure")
			s.resumeErr = failure
			if _, err := service.Review(t.Context(), request); !errors.Is(err, failure) {
				t.Fatalf("completed result hid resume failure: %v", err)
			}
			s.resumeErr = nil
			for i := 0; i < 2; i++ {
				result, err := service.Review(t.Context(), request)
				if err != nil || !result.ReviewReplayed || !result.ExecutionReplayed || result.View.Receipt == nil || s.run.Status != domain.RunRunning {
					t.Fatalf("completed replay failed: %+v %v", result, err)
				}
			}
			if executor.calls != 0 || s.decideCalls != 0 || s.invalidateCalls != 0 || !reflect.DeepEqual(s.record, originalRecord) || !reflect.DeepEqual(*s.intent, originalIntent) || !reflect.DeepEqual(*s.result, originalResult) {
				t.Fatal("receipt recovery changed sealed state")
			}
			if bounded && (!reflect.DeepEqual(*s.grant, originalGrant) || !reflect.DeepEqual(*s.consumption, originalConsumption) || s.proposal.Scope.OtherReason != s.grant.Reason) {
				t.Fatal("bounded grant purpose, TTL, count or consumption changed")
			}
		})
	}
}

func TestRiskReviewUnknownRecoveryRequiresDurableInvalidation(t *testing.T) {
	s, service, request, executor := newRiskReviewRecoveryFixture(t, true, false)
	originalIntent, originalGrant, originalConsumption := *s.intent, *s.grant, *s.consumption
	failure := errors.New("injected invalidation transaction failure")
	s.invalidateErr = failure
	if _, err := service.Review(t.Context(), request); !errors.Is(err, failure) {
		t.Fatalf("unknown intent hid invalidation failure: %v", err)
	}
	if s.resumeCalls != 0 || s.invalidation != nil {
		t.Fatal("unknown recovery resumed without durable invalidation")
	}
	s.invalidateErr, s.resumeErr = nil, errors.New("injected unknown-result resume failure")
	if _, err := service.Review(t.Context(), request); !errors.Is(err, s.resumeErr) {
		t.Fatalf("unknown intent hid resume failure: %v", err)
	}
	if s.invalidation == nil || s.invalidation.ReasonCode != "execution_uncertain" {
		t.Fatal("uncertainty was not durable before resume")
	}
	invalidated := *s.invalidation
	s.resumeErr = nil
	for i := 0; i < 2; i++ {
		result, err := service.Review(t.Context(), request)
		if err != nil || !result.ReviewReplayed || !result.ExecutionReplayed || !result.View.Uncertain || result.View.RiskResult != nil || result.View.Receipt != nil || s.run.Status != domain.RunRunning {
			t.Fatalf("unknown replay failed: %+v %v", result, err)
		}
	}
	if executor.calls != 0 || s.decideCalls != 0 || s.invalidateCalls != 2 || !reflect.DeepEqual(*s.intent, originalIntent) || !reflect.DeepEqual(*s.invalidation, invalidated) || !reflect.DeepEqual(*s.consumption, originalConsumption) || s.grant.Status != approval.GrantRevoked || s.grant.UsesRemaining != originalGrant.UsesRemaining || s.grant.MaxUses != originalGrant.MaxUses || s.grant.ScopeFingerprint != originalGrant.ScopeFingerprint || s.grant.Reason != originalGrant.Reason || !s.grant.ExpiresAt.Equal(*originalGrant.ExpiresAt) {
		t.Fatal("unknown recovery rewrote intent, repeated invalidation, or consumed another use")
	}
}
