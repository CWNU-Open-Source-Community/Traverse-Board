package llm

import (
	"crypto/sha256"
	"encoding/json"
)

// StoredHistoryToolResult preserves an already source-verified durable history
// envelope across provider preparation. Only application code reconstructing
// stored tool rounds should call it. The private marker cannot arrive through
// JSON, and changing the content invalidates it. Ordinary model/user text and
// all other tool results still take the normal secret-redaction path.
func StoredHistoryToolResult(result ToolResult) ToolResult {
	normalized, err := NormalizeToolResult(result)
	if err != nil || result.IsError {
		return result
	}
	var envelope struct {
		Version string `json:"version"`
		Tool    string `json:"tool"`
		Status  string `json:"status"`
		Stdout  string `json:"stdout"`
	}
	if json.Unmarshal([]byte(normalized.Content), &envelope) != nil || envelope.Version != "supervisor_tool_result.v1" ||
		envelope.Status != "completed" || (envelope.Tool != "history_search" && envelope.Tool != "history_read") {
		return result
	}
	var source struct {
		Version               string `json:"version"`
		InstructionAuthorized *bool  `json:"instruction_authorized"`
	}
	if json.Unmarshal([]byte(envelope.Stdout), &source) != nil || source.Version != "history_recall.v1" ||
		source.InstructionAuthorized == nil || *source.InstructionAuthorized {
		return result
	}
	normalized.storedHistoryDigest = sha256.Sum256([]byte(normalized.Content))
	return normalized
}

func (r ToolResult) preservesStoredHistory() bool {
	return r.storedHistoryDigest != ([32]byte{}) && r.storedHistoryDigest == sha256.Sum256([]byte(r.Content))
}
