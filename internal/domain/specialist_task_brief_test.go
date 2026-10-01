package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSpecialistTaskBriefOperationProtocolRejectsAmbiguityAndUnknownVersions(t *testing.T) {
	for _, op := range []string{"append", "replace", "withdraw"} {
		p := AgentInstructionPayload{Version: SpecialistInstructionOperationVersion, Operation: op, Instruction: "current scoped correction"}
		if op != "append" {
			p.TargetMessageID = "agentmsg-earlier"
			p.TargetPayloadSHA256 = strings.Repeat("a", 64)
		}
		if op == "withdraw" {
			p.Instruction = ""
		}
		raw, _ := json.Marshal(p)
		decoded, err := DecodeAgentInstructionPayload(string(raw))
		if err != nil || decoded != p {
			t.Fatalf("valid %s operation rejected: %#v %v", op, decoded, err)
		}
	}
	for _, raw := range []string{
		`{"version":"specialist_instruction.v2","instruction":"scope"}`,
		`{"version":"specialist_instruction.v2","instruction":"scope","operation":"replace"}`,
		`{"version":"specialist_instruction.v2","instruction":"scope","operation":"append","target_message_id":"agentmsg-earlier"}`,
		`{"version":"specialist_instruction.v2","instruction":"scope","operation":"withdraw","target_message_id":"agentmsg-earlier","target_payload_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"version":"specialist_instruction.v2","instruction":null,"operation":"withdraw","target_message_id":"agentmsg-earlier","target_payload_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"version":"specialist_instruction.v2","instruction":"scope","operation":"append","authority":"root"}`,
		`{"version":"specialist_instruction.v1","instruction":"scope","operation":""}`,
		fmt.Sprintf(`{"version":%q,"instruction":"scope","operation":"append"}`, strings.Replace(SpecialistInstructionOperationVersion, ".v2", ".v99", 1)),
	} {
		if _, err := DecodeAgentInstructionPayload(raw); err == nil {
			t.Fatalf("ambiguous operation accepted: %s", raw)
		}
	}
	legacy, _ := json.Marshal(AgentInstructionPayload{Version: SpecialistInstructionVersion, Instruction: "legacy scope"})
	if string(legacy) != `{"version":"specialist_instruction.v1","instruction":"legacy scope"}` {
		t.Fatal("existing v1 writer changed shape")
	}
}

func TestSpecialistTaskBriefSourceAndActiveCountBoundsStopExplicitly(t *testing.T) {
	messages := []AgentMessage{}
	for i := range MaxSpecialistBriefInstructions + 1 {
		raw, _ := json.Marshal(AgentInstructionPayload{Version: SpecialistInstructionVersion, Instruction: fmt.Sprintf("required scope%d", i)})
		messages = append(messages, AgentMessage{ID: fmt.Sprintf("agentmsg-%d", i), RunID: "run-brief", SenderAgentID: "agent-root", RecipientAgentID: "agent-child",
			Sequence: int64(i + 1), Kind: AgentMessageInstruction, Semantic: AgentMessageSemanticMessage, Status: AgentMessageConsumed, PayloadJSON: string(raw)})
	}
	if _, err := BuildSpecialistTaskBrief("run-brief", "agent-child", "agent-root", messages, nil); err == nil {
		t.Fatal("active constraint overflow silently omitted a source")
	}
	if _, err := BuildSpecialistTaskBrief("run-brief", "agent-child", "agent-root", make([]AgentMessage, MaxSpecialistBriefSources+1), nil); err == nil {
		t.Fatal("source history overflow silently omitted retirement evidence")
	}
}
