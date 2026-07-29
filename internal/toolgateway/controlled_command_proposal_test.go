package toolgateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
)

type controlledCommandProposalExecutorStub struct {
	mu        sync.Mutex
	calls     int
	lastScope ControlledCommandProposalContext
	lastSpec  ControlledCommandProposalSpec
}

func (s *controlledCommandProposalExecutorStub) ProposeControlledCommand(
	_ context.Context,
	scope ControlledCommandProposalContext,
	spec ControlledCommandProposalSpec,
) (ControlledCommandProposalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastScope = scope
	s.lastSpec = spec
	return ControlledCommandProposalResult{
		ProposalID: "command-proposal-1",
		Kind:       spec.Kind,
	}, nil
}

func TestControlledCommandProposalDefinitionAndPayloadAreStrict(t *testing.T) {
	definition, found := SupervisorToolDefinition(ControlledCommandProposeTool)
	if !found || definition.Class != ClassAgentProposal ||
		definition.Approval != ApprovalAutomatic ||
		!json.Valid(definition.InputSchema) ||
		!strings.Contains(string(definition.InputSchema),
			`"const":"controlled_command_proposal.v1"`) {
		t.Fatalf("invalid controlled command proposal definition: %#v", definition)
	}

	valid := json.RawMessage(`{"version":"controlled_command_proposal.v1","kind":"git-status","purpose":"Inspect repository status","relative_path":"","timeout_millis":15000}`)
	canonical, err := NormalizeSupervisorToolPayload(
		ControlledCommandProposeTool, valid)
	if err != nil || !json.Valid(canonical) {
		t.Fatalf("valid proposal payload failed: %s err=%v", canonical, err)
	}

	for _, payload := range []json.RawMessage{
		json.RawMessage(`{"version":"controlled_command_proposal.v1","kind":"git-status","purpose":"Inspect","relative_path":"","timeout_millis":15000,"shell":"git status"}`),
		json.RawMessage(`{"version":"controlled_command_proposal.v1","kind":"git-status","purpose":"Inspect","relative_path":"","timeout_millis":15000,"executable":"git"}`),
		json.RawMessage(`{"version":"controlled_command_proposal.v1","kind":"git-status","purpose":"Inspect","relative_path":"","timeout_millis":15000,"argv":["status"]}`),
		json.RawMessage(`{"version":"controlled_command_proposal.v1","kind":"git-status","purpose":"Inspect","relative_path":"outside","timeout_millis":15000}`),
		json.RawMessage(`{"version":"controlled_command_proposal.v1","kind":"powershell-workspace-list","purpose":"Inspect","relative_path":"","timeout_millis":15000}`),
		json.RawMessage(`{"version":"controlled_command_proposal.v2","kind":"git-status","purpose":"Inspect","relative_path":"","timeout_millis":15000}`),
		json.RawMessage(`{"version":"controlled_command_proposal.v1","kind":"git-status","purpose":"Inspect","relative_path":"","timeout_millis":15000} {}`),
	} {
		if _, err := NormalizeSupervisorToolPayload(
			ControlledCommandProposeTool, payload); err == nil {
			t.Fatalf("unsafe proposal payload was accepted: %s", payload)
		}
	}
}

func TestControlledCommandProposalGatewayOnlyRecordsReviewRequest(t *testing.T) {
	tracked := newTrackedStructuredStore()
	executor := &controlledCommandProposalExecutorStub{}
	gateway := New(tracked, policy.NewDefaultChecker()).
		WithControlledCommandProposalExecutor(executor)
	token := "s" + "k-" + strings.Repeat("p", 28)
	call := ToolCall{
		Name: ControlledCommandProposeTool,
		Payload: json.RawMessage(
			`{"version":"controlled_command_proposal.v1","kind":"go-version","purpose":"Confirm compiler; token=` +
				token + `","relative_path":"","timeout_millis":15000}`),
		OperationKey:    "command-proposal-operation",
		RunID:           "run-1",
		AgentID:         "agent-root",
		SessionID:       "session-1",
		WorkspaceID:     "workspace-1",
		RequestedBy:     "run_supervisor",
		LeaseID:         "lease-1",
		LeaseGeneration: 1,
	}

	outcome, err := gateway.Invoke(t.Context(), call)
	if err != nil || outcome.Result == nil ||
		outcome.Result.Status != StatusCompleted ||
		outcome.Execution == nil ||
		outcome.Execution.Backend != "agent_proposal" ||
		outcome.Result.Metadata["proposal_id"] != "command-proposal-1" ||
		outcome.Result.Metadata["operator_review_required"] != "true" ||
		outcome.Result.Metadata["execution_authorized"] != "false" ||
		outcome.Result.Metadata["capability_grant"] != "false" {
		t.Fatalf("unexpected proposal outcome: %#v err=%v", outcome, err)
	}
	if outcome.Call.OperationKey != "" || outcome.Call.LeaseID != "" ||
		strings.Contains(string(outcome.Call.Payload), token) {
		t.Fatalf("proposal outcome exposed control or secret data: %#v", outcome.Call)
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.calls != 1 ||
		executor.lastSpec.Kind != runner.ControlledCommandGoVersion ||
		strings.Contains(executor.lastSpec.Purpose, token) ||
		executor.lastScope.PolicyDecision.Approval != ApprovalAutomatic {
		t.Fatalf("executor received unsafe proposal: %#v %#v",
			executor.lastScope, executor.lastSpec)
	}
}
