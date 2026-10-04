package application

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
	"encoding/json"
	"strings"
	"testing"
)

// Only history reads are implemented. Any accidental mutation/dispatch through
// the embedded interfaces panics instead of silently simulating an execution.
type historicalRiskReadStore struct {
	RunSupervisorStore
	RiskEscalationHistoryStore
	proposal runner.RiskEscalationProposal
	approval approval.Record
	result   *runner.RiskEscalationResult
	intent   bool
	missing  bool
	evidence string
}

func (s *historicalRiskReadStore) GetRiskEscalationProposal(_ context.Context, id string) (runner.RiskEscalationProposal, error) {
	if s.missing || id != s.proposal.ID {
		return runner.RiskEscalationProposal{}, apperror.New(apperror.CodeNotFound, "history absent")
	}
	return s.proposal, nil
}
func (s *historicalRiskReadStore) GetApprovalByProposal(context.Context, string) (approval.Record, error) {
	return s.approval, nil
}
func (s *historicalRiskReadStore) GetRiskEscalationResult(context.Context, string) (runner.RiskEscalationResult, bool, error) {
	if s.result == nil {
		return runner.RiskEscalationResult{}, false, nil
	}
	return *s.result, true, nil
}
func (s *historicalRiskReadStore) GetRiskEscalationInvalidation(context.Context, string) (runner.RiskEscalationInvalidation, bool, error) {
	return runner.RiskEscalationInvalidation{}, false, nil
}
func (s *historicalRiskReadStore) GetRiskEscalationExecutionIntentByProposal(context.Context, string) (runner.HostExecutionIntent, bool, error) {
	return runner.HostExecutionIntent{}, s.intent, nil
}
func (s *historicalRiskReadStore) ListSessionMessages(context.Context, string, bool) ([]session.Message, error) {
	if s.result == nil {
		return nil, nil
	}
	return []session.Message{{Content: s.evidence, Provenance: session.ContextProvenance{SourceKind: s.result.SourceKind, SourceRef: s.result.SourceRef, ContentSHA256: s.result.ContentSHA256}}}, nil
}
func TestHistoricalRiskRecoveryUsesOnlyExactSavedOutcome(t *testing.T) {
	for _, scenario := range []string{"completed", "nonzero", "denied", "unknown", "pending", "missing", "wrong_run", "wrong_call", "wrong_agent", "wrong_turn", "wrong_attempt"} {
		t.Run(scenario, func(t *testing.T) {
			payload := `{"version":"risk_escalation.v1","transport":"process","executable_path":"/usr/bin/git","argv":["status"],"working_directory":"/workspace","timeout_milliseconds":1000,"purpose":"saved inspection","risk_kinds":["network"],"network_targets":["example.test:443"],"network_purpose":"saved target"}`
			call := domain.SupervisorToolCall{RunID: "run-history", Turn: 2, CallID: "call-history", AgentID: "root-history", AgentAttemptID: "attempt-history", ToolName: string(toolgateway.HostCommandProposeTool), PayloadJSON: payload}
			turn := domain.SupervisorTurn{Run: domain.Run{ID: call.RunID, SessionID: "session-history"}, Mission: domain.Mission{ID: "mission-history", WorkspaceID: "workspace-history"},
				Agent: domain.AgentNode{ID: call.AgentID}, Checkpoint: domain.SupervisorCheckpoint{AttemptID: call.AgentAttemptID}}
			key := supervisorToolOperationKey(call.RunID, call.Turn, toolgateway.HostCommandProposeTool, json.RawMessage(payload))
			digest := runmutation.OperationKeyDigest(string(toolgateway.HostCommandProposeTool), call.RunID, key)
			st := &historicalRiskReadStore{proposal: runner.RiskEscalationProposal{ID: "risk-escalation-" + digest[:24], RunID: call.RunID,
				MissionID: turn.Mission.ID, SessionID: turn.Run.SessionID, WorkspaceID: turn.Mission.WorkspaceID, RootAgentID: call.AgentID,
				SupervisorTurn: call.Turn, SupervisorToolCallID: call.CallID, Spec: runner.HostCommandSpec{Fingerprint: strings.Repeat("a", 64)}},
				approval: approval.Record{ID: "approval-history", Status: approval.StatusApproved}, evidence: "UNTRUSTED SAVED EVIDENCE\noriginal output"}
			switch scenario {
			case "completed", "nonzero":
				st.result = &runner.RiskEscalationResult{Status: "completed", SourceKind: session.SourceGoCommandResult, SourceRef: "saved-command", ContentSHA256: session.ContentSHA256(st.evidence)}
				if scenario == "nonzero" {
					st.result.Status = "failed"
					st.result.ErrorCode = "exit_nonzero"
				}
			case "denied":
				st.approval.Status = approval.StatusDenied
				st.approval.DecisionReason = "saved denial"
			case "unknown":
				st.intent = true
			case "pending":
				st.approval.Status = approval.StatusPending
			case "missing":
				st.missing = true
			case "wrong_run":
				st.proposal.RunID = "other-run"
			case "wrong_call":
				st.proposal.SupervisorToolCallID = "other-call"
			case "wrong_turn":
				st.proposal.SupervisorTurn++
			case "wrong_attempt":
				call.AgentAttemptID = "different-attempt"
			case "wrong_agent":
				st.proposal.RootAgentID = "other-agent"
			}
			supervisor := &RunSupervisor{store: st}
			result, err := supervisor.invokeSupervisorTool(t.Context(), turn, call)
			if scenario == "pending" || strings.HasPrefix(scenario, "wrong_") {
				if err == nil {
					t.Fatalf("unsettled or mismatched history resumed: %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var envelope supervisorToolResultEnvelope
			if err = json.Unmarshal([]byte(result.ResultJSON), &envelope); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "completed":
				if result.Status != domain.SupervisorToolCompleted || envelope.Stdout != st.evidence {
					t.Fatalf("saved result lost: %+v", envelope)
				}
			case "nonzero":
				if result.Status != domain.SupervisorToolFailed || result.ErrorCode != "exit_nonzero" {
					t.Fatalf("nonzero result changed: %+v", result)
				}
			case "denied":
				if result.Status != domain.SupervisorToolDenied || envelope.Stdout != "" {
					t.Fatalf("denial changed: %+v", envelope)
				}
			case "unknown":
				if result.ErrorCode != "execution_uncertain" || envelope.Stdout != "" || envelope.Metadata["automatic_retry_allowed"] != "false" {
					t.Fatalf("unknown intent became execution: %+v", envelope)
				}
			case "missing":
				if result.ErrorCode != string(apperror.CodePolicyDenied) {
					t.Fatalf("new historical proposal was accepted: %+v", result)
				}
			}
			replay, err := supervisor.invokeSupervisorTool(t.Context(), turn, call)
			if err != nil || replay.ResultJSON != result.ResultJSON {
				t.Fatalf("history replay changed: %+v %v", replay, err)
			}
		})
	}
}

func (*historicalRiskReadStore) GetSessionGrant(context.Context, string) (approval.SessionGrant, error) {
	panic("unexpected historical grant lookup")
}
func (*historicalRiskReadStore) GetGrantConsumptionByProposal(context.Context, string) (approval.GrantConsumption, bool, error) {
	panic("unexpected historical grant consumption lookup")
}
