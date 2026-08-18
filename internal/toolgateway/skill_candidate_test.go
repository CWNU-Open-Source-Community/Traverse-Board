package toolgateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/policy"
)

const validSkillCandidatePayload = `{"version":"skill_candidate_proposal.v1","name":"bounded-helper","skill_version":"1.0.0","description":"A reusable generated workflow.","profiles":["code"],"surfaces":["code"],"phases":["plan","deliver"],"roles":["root"],"user_invocable":true,"model_invocable":false,"explicit_only":true,"tool_dependencies":["list_workspace","read_file"],"content":"# Bounded helper\n\nInspect, verify, and report.\n"}`

type skillCandidateExecutorStub struct {
	calls int
	spec  SkillCandidateSpec
}

func (s *skillCandidateExecutorStub) ProposeSkillCandidate(_ context.Context,
	_ SkillCandidateContext, spec SkillCandidateSpec,
) (SkillCandidateResult, error) {
	s.calls++
	s.spec = spec
	return SkillCandidateResult{
		CandidateID: "skill-candidate-1", CandidateFingerprint: strings.Repeat("a", 64),
		Name: spec.Name, Version: spec.SkillVersion, Status: "proposed",
		PackageFingerprint: strings.Repeat("b", 64),
		ContentSHA256:      strings.Repeat("c", 64), ContentBytes: len([]byte(spec.Content)),
	}, nil
}

func TestSkillCandidatePayloadIsStrictAndCodeOnly(t *testing.T) {
	definition, found := SupervisorToolDefinition(SkillCandidateProposeTool)
	if !found || definition.Class != ClassAgentProposal ||
		definition.Approval != ApprovalAutomatic || !json.Valid(definition.InputSchema) {
		t.Fatalf("invalid Skill candidate definition: %#v", definition)
	}
	canonical, err := NormalizeSupervisorToolPayload(SkillCandidateProposeTool,
		json.RawMessage(validSkillCandidatePayload))
	if err != nil || !json.Valid(canonical) ||
		!strings.Contains(string(canonical), `"surfaces":["code"]`) {
		t.Fatalf("valid candidate payload failed: %s err=%v", canonical, err)
	}
	for _, payload := range []string{
		strings.Replace(validSkillCandidatePayload, `"surfaces":["code"]`, `"surfaces":["cyber"]`, 1),
		strings.Replace(validSkillCandidatePayload, `"phases":["plan","deliver"]`, `"phases":["deliver","plan"]`, 1),
		strings.Replace(validSkillCandidatePayload, `"explicit_only":true`, `"explicit_only":true,"authority":true`, 1),
		strings.Replace(validSkillCandidatePayload, `"name":"bounded-helper"`, `"name":"first","name":"second"`, 1),
		strings.Replace(validSkillCandidatePayload, "Inspect, verify, and report.", "token sk-123456789012345678901234", 1),
		validSkillCandidatePayload + `{}`,
	} {
		if _, err := NormalizeSupervisorToolPayload(SkillCandidateProposeTool,
			json.RawMessage(payload)); err == nil {
			t.Fatalf("invalid Skill candidate payload was accepted: %s", payload)
		}
	}
}

func TestSkillCandidateGatewayCreatesOnlyUntrustedProposal(t *testing.T) {
	tracked := newTrackedStructuredStore()
	executor := &skillCandidateExecutorStub{}
	gateway := New(tracked, policy.NewDefaultChecker()).WithSkillCandidateExecutor(executor)
	outcome, err := gateway.Invoke(t.Context(), ToolCall{
		Name: SkillCandidateProposeTool, Payload: json.RawMessage(validSkillCandidatePayload),
		OperationKey: "skill-candidate-operation", RunID: "run-1", AgentID: "agent-root",
		SessionID: "session-1", WorkspaceID: "workspace-1", RequestedBy: "run_supervisor",
		LeaseID: "lease-1", LeaseGeneration: 1,
	})
	if err != nil || executor.calls != 1 || outcome.Result == nil ||
		outcome.Result.Metadata["status"] != "proposed" ||
		outcome.Result.Metadata["human_review_required"] != "true" ||
		outcome.Result.Metadata["installation_authorized"] != "false" ||
		outcome.Result.Metadata["selection_authorized"] != "false" ||
		string(outcome.Call.Payload) != `{"redacted":true}` {
		t.Fatalf("candidate outcome=%#v calls=%d err=%v", outcome, executor.calls, err)
	}
}
