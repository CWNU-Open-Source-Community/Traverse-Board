package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const (
	SpecialistInstructionOperationVersion = "specialist_instruction.v2"
	SpecialistTaskBriefVersion            = "specialist_task_brief.v1"
	MaxSpecialistBriefSources             = 256
	MaxSpecialistBriefInstructions        = 32
	MaxSpecialistBriefWorkItems           = 20
	MaxSpecialistBriefBytes               = 128 * 1024
)

type SpecialistBriefSource struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Version  string `json:"version"`
	Sequence int64  `json:"sequence"`
	SHA256   string `json:"sha256"`
}

type SpecialistBriefInstruction struct {
	SourceID    string `json:"source_id"`
	Instruction string `json:"instruction"`
}

type SpecialistTaskWorkContext struct {
	ID                 string           `json:"id"`
	Status             WorkItemStatus   `json:"status"`
	Priority           WorkItemPriority `json:"priority"`
	Title              string           `json:"title"`
	Description        string           `json:"description,omitempty"`
	AcceptanceCriteria []string         `json:"acceptance_criteria,omitempty"`
	Dependencies       []string         `json:"dependencies,omitempty"`
	BlockedReason      string           `json:"blocked_reason,omitempty"`
	Version            int64            `json:"item_version"`
}

func SpecialistTaskWorkProjection(item WorkItem) SpecialistTaskWorkContext {
	return SpecialistTaskWorkContext{ID: item.ID, Status: item.Status, Priority: item.Priority, Title: item.Title,
		Description: item.Description, AcceptanceCriteria: append([]string(nil), item.AcceptanceCriteria...),
		Dependencies: append([]string(nil), item.Dependencies...), BlockedReason: item.BlockedReason, Version: item.Version}
}

// Task truth survives attempts; this value contains no lease or execution grant.
// A Store snapshot row separately pins it to one active attempt.
type SpecialistTaskBrief struct {
	Version       string                       `json:"version"`
	RunID         string                       `json:"run_id"`
	AgentID       string                       `json:"agent_id"`
	ParentAgentID string                       `json:"parent_agent_id"`
	Instructions  []SpecialistBriefInstruction `json:"instructions"`
	WorkItems     []WorkItem                   `json:"work_items"`
	Sources       []SpecialistBriefSource      `json:"sources"`
	Fingerprint   string                       `json:"fingerprint"`
}

func SpecialistInstructionPayloadSHA256(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func validBriefHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == hex.EncodeToString(decoded)
}

func validateInstructionObjectShape(raw string, p AgentInstructionPayload) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return err
	}
	expected := []string{"version", "instruction"}
	if p.Version == SpecialistInstructionOperationVersion {
		expected = append(expected, "operation")
		if p.Operation == "replace" || p.Operation == "withdraw" {
			expected = append(expected, "target_message_id", "target_payload_sha256")
		}
	}
	if len(fields) != len(expected) {
		return errors.New("Specialist instruction has an invalid field set")
	}
	for _, field := range expected {
		value, ok := fields[field]
		if !ok {
			return errors.New("Specialist instruction is missing a field")
		}
		if value = bytes.TrimSpace(value); len(value) == 0 || value[0] != '"' {
			return fmt.Errorf("Specialist instruction field %s must be a string", field)
		}
	}
	return nil
}

// Reduce messages in sequence order. Callers decide the preparation cutoff;
// only already consumed sources and the current selected pending batch enter.
func BuildSpecialistTaskBrief(runID, childID, parentID string, messages []AgentMessage,
	work []WorkItem,
) (SpecialistTaskBrief, error) {
	brief := SpecialistTaskBrief{Version: SpecialistTaskBriefVersion, RunID: runID,
		AgentID: childID, ParentAgentID: parentID, Instructions: []SpecialistBriefInstruction{},
		WorkItems: append([]WorkItem{}, work...), Sources: []SpecialistBriefSource{}}
	if len(messages) > MaxSpecialistBriefSources || len(work) > MaxSpecialistBriefWorkItems {
		return brief, errors.New("Specialist task source count exceeds its bound")
	}
	active := make(map[string]AgentMessage)
	order := make([]string, 0, len(messages))
	previous := int64(0)
	for _, message := range messages {
		if message.RunID != runID || message.RecipientAgentID != childID || message.SenderAgentID != parentID ||
			message.Sequence <= previous || (message.Status != AgentMessagePending && message.Status != AgentMessageConsumed) ||
			!EligibleSpecialistContextMessage(message) {
			return brief, errors.New("Specialist task instruction scope or sequence is invalid")
		}
		previous = message.Sequence
		p, err := DecodeAgentInstructionPayload(message.PayloadJSON)
		if err != nil {
			return brief, err
		}
		if _, exists := active[message.ID]; exists {
			return brief, errors.New("duplicate Specialist task source")
		}
		if p.Operation == "replace" || p.Operation == "withdraw" {
			target, exists := active[p.TargetMessageID]
			if !exists || target.Sequence >= message.Sequence || SpecialistInstructionPayloadSHA256(target.PayloadJSON) != p.TargetPayloadSHA256 {
				return brief, errors.New("Specialist retirement target is not an earlier active source with the required hash")
			}
			delete(active, p.TargetMessageID)
		}
		brief.Sources = append(brief.Sources, SpecialistBriefSource{Kind: "parent_instruction", ID: message.ID,
			Version: p.Version, Sequence: message.Sequence, SHA256: SpecialistInstructionPayloadSHA256(message.PayloadJSON)})
		if p.Operation != "withdraw" {
			active[message.ID] = message
			order = append(order, message.ID)
		}
	}
	for _, id := range order {
		if message, ok := active[id]; ok {
			p, _ := DecodeAgentInstructionPayload(message.PayloadJSON)
			brief.Instructions = append(brief.Instructions, SpecialistBriefInstruction{SourceID: id, Instruction: p.Instruction})
		}
	}
	if len(brief.Instructions) > MaxSpecialistBriefInstructions {
		return brief, errors.New("active Specialist instructions exceed their bound")
	}
	// Work priority/order is preserved from the Store's deterministic query.
	for _, item := range work {
		if item.RunID != runID || item.OwnerAgentID != childID || item.Terminal() {
			return brief, errors.New("Specialist task work is not active child-owned work")
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return brief, err
		}
		brief.Sources = append(brief.Sources, SpecialistBriefSource{Kind: "child_work_item", ID: item.ID,
			Version: fmt.Sprint(item.Version), SHA256: SpecialistInstructionPayloadSHA256(string(encoded))})
	}
	brief.Fingerprint, _ = brief.fingerprint()
	return brief, brief.Validate()
}

func (b SpecialistTaskBrief) fingerprint() (string, error) {
	b.Fingerprint = ""
	encoded, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return SpecialistInstructionPayloadSHA256(string(encoded)), nil
}

func (b SpecialistTaskBrief) Validate() error {
	if b.Version != SpecialistTaskBriefVersion || !validAgentIdentity(b.RunID, false) ||
		!validAgentIdentity(b.AgentID, false) || !validAgentIdentity(b.ParentAgentID, false) {
		return errors.New("Specialist task brief identity is invalid")
	}
	if len(b.Instructions) > MaxSpecialistBriefInstructions || len(b.WorkItems) > MaxSpecialistBriefWorkItems ||
		len(b.Sources) > MaxSpecialistBriefSources+MaxSpecialistBriefWorkItems {
		return errors.New("Specialist task brief exceeds its source bounds")
	}
	ids := make(map[string]SpecialistBriefSource, len(b.Sources))
	previousSequence := int64(0)
	for _, source := range b.Sources {
		key := source.Kind + "\x00" + source.ID
		if !validAgentIdentity(source.ID, false) || !validBriefHash(source.SHA256) || source.Version == "" {
			return errors.New("Specialist task source binding is invalid")
		}
		if _, exists := ids[key]; exists {
			return errors.New("duplicate Specialist task source binding")
		}
		switch source.Kind {
		case "parent_instruction":
			if source.Sequence <= previousSequence || (source.Version != SpecialistInstructionVersion && source.Version != SpecialistInstructionOperationVersion) {
				return errors.New("Specialist instruction source protocol or sequence is invalid")
			}
			previousSequence = source.Sequence
		case "child_work_item":
			if source.Sequence != 0 {
				return errors.New("Specialist work source cannot carry message sequence")
			}
		default:
			return errors.New("unsupported Specialist task source kind")
		}
		ids[key] = source
	}
	active := []string{}
	for _, instruction := range b.Instructions {
		if _, ok := ids["parent_instruction\x00"+instruction.SourceID]; !ok {
			return errors.New("Specialist instruction lacks a source binding")
		}
		if err := (AgentInstructionPayload{Version: SpecialistInstructionVersion, Instruction: instruction.Instruction}).Validate(); err != nil {
			return err
		}
		active = append(active, instruction.SourceID)
	}
	unique := append([]string{}, active...)
	slices.Sort(unique)
	if len(slices.Compact(unique)) != len(active) {
		return errors.New("duplicate active Specialist instruction")
	}
	workIDs := make(map[string]struct{}, len(b.WorkItems))
	for _, item := range b.WorkItems {
		if err := item.Validate(); err != nil {
			return err
		}
		if _, exists := workIDs[item.ID]; exists {
			return errors.New("duplicate Specialist task work")
		}
		workIDs[item.ID] = struct{}{}
		if item.RunID != b.RunID || item.OwnerAgentID != b.AgentID || item.Terminal() {
			return errors.New("Specialist task work scope is invalid")
		}
		source, ok := ids["child_work_item\x00"+item.ID]
		encoded, _ := json.Marshal(item)
		if !ok || source.Version != fmt.Sprint(item.Version) || source.SHA256 != SpecialistInstructionPayloadSHA256(string(encoded)) {
			return errors.New("Specialist task work version binding is invalid")
		}
	}
	hash, err := b.fingerprint()
	if err != nil {
		return err
	}
	if !validBriefHash(b.Fingerprint) || hash != b.Fingerprint {
		return errors.New("Specialist task brief fingerprint mismatch")
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if len(encoded) > MaxSpecialistBriefBytes {
		return errors.New("Specialist task brief exceeds its byte bound")
	}
	return nil
}
