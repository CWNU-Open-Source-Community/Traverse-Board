package domain

import "testing"

func TestOperatorRootAttributionKeepsAuthoritySeparateFromAgentAttempt(t *testing.T) {
	value := AgentAttribution{AgentID: "agent-root-operator",
		Source: AgentAttributionOperatorRoot}
	if err := value.Validate(); err != nil {
		t.Fatalf("operator root attribution was rejected: %v", err)
	}
	value.AgentAttemptID = "attempt-fabricated"
	if err := value.Validate(); err == nil {
		t.Fatal("operator root attribution claimed an Agent attempt")
	}
	if err := (AgentAttribution{AgentID: "agent-root-recorded",
		Source: AgentAttributionRecorded}).Validate(); err == nil {
		t.Fatal("recorded Agent attribution omitted its real attempt")
	}
}
