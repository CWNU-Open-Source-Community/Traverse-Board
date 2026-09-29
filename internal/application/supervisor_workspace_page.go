package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

// A page is a prior, untrusted observation. Keeping it does not validate the
// current file or authorize an edit. The ordinary exact-hash write gate still
// checks current bytes. Never join pages or use a result digest as a file hash.
func supervisorWorkspaceReadPage(call domain.SupervisorToolCall) (map[string]any, string, bool) {
	if call.ToolName != "workspace_read" || call.Status != domain.SupervisorToolCompleted || call.ErrorCode != "" {
		return nil, "", false
	}
	var envelope supervisorToolResultEnvelope
	if json.Unmarshal([]byte(call.ResultJSON), &envelope) != nil || envelope.Version != supervisorToolResultVersion ||
		envelope.Tool != call.ToolName || envelope.Status != "completed" || envelope.Truncated {
		return nil, "", false
	}
	var page map[string]any
	if json.Unmarshal([]byte(envelope.Stdout), &page) != nil || page["protocol_version"] != "agent-code-tools.v1" {
		return nil, "", false
	}
	for _, key := range []string{"workspace_id", "root_fingerprint", "path", "content_sha256", "encoding", "newline"} {
		value, ok := page[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, "", false
		}
		if observed, exists := envelope.Metadata[key]; exists && observed != value {
			return nil, "", false
		}
	}
	if hash, ok := page["content_sha256"].(string); !ok || len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return nil, "", false
	}
	if _, ok := page["content"].(string); !ok {
		return nil, "", false
	}
	if _, ok := page["truncated"].(bool); !ok {
		return nil, "", false
	}
	for _, key := range []string{"start_line", "end_line", "total_lines", "total_bytes", "redaction_count"} {
		n, ok := page[key].(float64)
		if !ok || n < 0 || n != float64(int64(n)) {
			return nil, "", false
		}
	}
	if page["start_line"].(float64) < 1 || page["end_line"].(float64) < page["start_line"].(float64) ||
		page["end_line"].(float64) > page["total_lines"].(float64) {
		return nil, "", false
	}
	// Canonical whole-page identity includes scope, version, range, redaction and
	// the actual model-visible body. Equal path/hash alone is never sufficient.
	encoded, err := json.Marshal(page)
	if err != nil {
		return nil, "", false
	}
	return page, session.ContentSHA256(string(encoded)), true
}

func workspacePageReceiptValue(call domain.SupervisorToolCall, value any) any {
	projected := toolBoundaryReceiptValue(value, "", 0)
	if call.ToolName == "workspace_read" {
		if object, ok := projected.(map[string]any); ok {
			object["content_omitted"] = true
		}
	}
	return projected
}

// Only an exact observation that survived the FINAL boundary budget can replace
// its duplicate in the independent recovery context. Candidate membership and
// a matching edit ID are insufficient.
func retainedBoundaryFileEffects(content string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		var entry struct {
			Original domain.HistoryReadRequest    `json:"original_result"`
			Effect   *domain.SupervisorToolEffect `json:"observation"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Effect == nil || entry.Original.SourceID == "" {
			continue
		}
		encoded, _ := json.Marshal(entry.Effect)
		result[workspaceReceiptReferenceKey(entry.Original)] = string(encoded)
	}
	return result
}

func workspaceReceiptReferenceKey(ref domain.HistoryReadRequest) string {
	return fmt.Sprintf("%s\x00%s\x00%s", ref.SourceID, ref.Part, ref.ExpectedSHA256)
}
