package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/redact"
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

func TestSpecialistTaskBriefSafeProjectionPreservesOriginalSourceBindings(t *testing.T) {
	const raw = "TOKEN=synthetic-secret-215\nREQUIRED_ORIGINAL_SOURCE_TAIL_215"
	payload, _ := json.Marshal(AgentInstructionPayload{Version: SpecialistInstructionVersion, Instruction: raw})
	now := time.Now().UTC()
	item := WorkItem{ID: "work-source", RunID: "run-source", OwnerAgentID: "agent-child", Title: raw,
		Description: raw, AcceptanceCriteria: []string{raw}, BlockedReason: raw,
		Status: WorkItemBlocked, Priority: WorkItemPriorityHigh, Version: 2, CreatedAt: now, UpdatedAt: now}
	message := AgentMessage{ID: "agentmsg-source", RunID: item.RunID, SenderAgentID: "agent-root",
		RecipientAgentID: item.OwnerAgentID, Sequence: 1, Kind: AgentMessageInstruction,
		Semantic: AgentMessageSemanticMessage, Status: AgentMessageConsumed, PayloadJSON: string(payload)}
	brief, err := BuildSpecialistTaskBrief(item.RunID, item.OwnerAgentID, message.SenderAgentID,
		[]AgentMessage{message}, []WorkItem{item})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(brief)
	projection := SpecialistTaskWorkProjection(brief.WorkItems[0])
	safe := redact.String(raw)
	if SpecialistTaskInstructionProjection(brief.Instructions[0].Instruction) != safe ||
		projection.Title != safe || projection.Description != safe || projection.BlockedReason != safe ||
		len(projection.AcceptanceCriteria) != 1 || projection.AcceptanceCriteria[0] != safe ||
		projection.ID != item.ID || projection.Priority != item.Priority || projection.Status != item.Status || projection.Version != item.Version {
		t.Fatal("safe projection lost required task text or state")
	}
	after, _ := json.Marshal(brief)
	if string(before) != string(after) || brief.Validate() != nil || brief.Instructions[0].Instruction != raw ||
		brief.Sources[0].SHA256 != SpecialistInstructionPayloadSHA256(string(payload)) {
		t.Fatal("safe delivery rewrote original task truth or its fingerprint/source binding")
	}
}

func TestSpecialistTaskBriefSafeContextEncodingPreservesJSONAcrossTextRedaction(t *testing.T) {
	value := struct {
		Text  string   `json:"text"`
		Links []string `json:"links"`
		Count uint64   `json:"count"`
	}{Text: "TOKEN=synthetic-secret-215\nREQUIRED_NEWLINE_TAIL_215\t\"quoted\"\\中文",
		Links: []string{"https://example.invalid:8443?left=right", "TOKEN:synthetic-secret-216\r\nREQUIRED_CRLF_TAIL_215"},
		Count: 9007199254740993}
	encoded, err := MarshalSpecialistDeliveryContext(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-secret-") || redact.String(string(encoded)) != string(encoded) {
		t.Fatal("encoded context leaked a secret or changed at a text-redaction boundary")
	}
	decoded := value
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Text != redact.String(value.Text) ||
		len(decoded.Links) != 2 || decoded.Links[0] != value.Links[0] || decoded.Links[1] != redact.String(value.Links[1]) ||
		decoded.Count != value.Count {
		t.Fatalf("safe JSON encoding changed string/number meaning: %v", err)
	}
}
