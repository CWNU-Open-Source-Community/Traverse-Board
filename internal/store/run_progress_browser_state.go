package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/toolgateway"
)

// A sealed snapshot can establish new observed control state, including
// deletions. This is neither an effect/acceptance verdict nor renewed authority.
// Deduplicate the whole sorted multiset across the Run. Session/boot, URL,
// epochs, refs, timestamps, layout animation and call counts cannot reward loops.
func progressBrowserStateObservation(call domain.SupervisorToolCall, envelope progressToolEnvelope) (string, bool) {
	if call.ToolName != "browser_snapshot" || call.Status != domain.SupervisorToolCompleted ||
		call.ErrorCode != "" || call.CompletedAt == nil || call.CompletedAt.IsZero() ||
		envelope.Version != "supervisor_tool_result.v1" || envelope.Tool != call.ToolName ||
		envelope.Status != "completed" || envelope.Truncated {
		return "", false
	}
	if _, err := toolgateway.NormalizeAgentBrowserPayload(toolgateway.BrowserSnapshotTool, json.RawMessage(call.PayloadJSON)); err != nil {
		return "", false
	}
	authority, err := toolgateway.DecodeAgentBrowserAuthority(json.RawMessage(call.AuthorityJSON))
	if err != nil || authority.RunID != call.RunID ||
		envelope.Metadata["agent_browser_session_id"] != authority.BrowserSessionID ||
		envelope.Metadata["manager_boot_id"] != authority.ManagerBootID {
		return "", false
	}
	var snapshot struct {
		Version           string    `json:"version"`
		SessionID         string    `json:"session_id"`
		SnapshotID        string    `json:"snapshot_id"`
		CanonicalURL      string    `json:"canonical_url"`
		DocumentEpoch     uint64    `json:"document_epoch"`
		Truncated         *bool     `json:"truncated"`
		UntrustedEvidence *bool     `json:"untrusted_evidence"`
		CompletedAt       time.Time `json:"completed_at"`
		Elements          *[]struct {
			Ref      string `json:"ref"`
			Role     string `json:"role"`
			Name     string `json:"name"`
			Type     string `json:"type"`
			Disabled *bool  `json:"disabled"`
		} `json:"elements"`
	}
	if json.Unmarshal([]byte(envelope.Stdout), &snapshot) != nil || snapshot.Version != "agent-browser-runtime.v1" ||
		snapshot.SessionID != authority.BrowserSessionID || !progressIdentity(snapshot.SnapshotID) ||
		len(snapshot.SnapshotID) > 256 || snapshot.DocumentEpoch == 0 || snapshot.CompletedAt.IsZero() ||
		snapshot.Truncated == nil || *snapshot.Truncated || snapshot.UntrustedEvidence == nil || !*snapshot.UntrustedEvidence ||
		snapshot.Elements == nil || len(*snapshot.Elements) > 128 || !progressBrowserPublicText(snapshot.CanonicalURL, 4096) {
		return "", false
	}
	parsed, err := url.Parse(snapshot.CanonicalURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	controls := make([]string, 0, len(*snapshot.Elements))
	refs := make(map[string]bool, len(*snapshot.Elements))
	for _, element := range *snapshot.Elements {
		if !progressIdentity(element.Ref) || len(element.Ref) > 256 || refs[element.Ref] ||
			!progressIdentity(element.Role) || len(element.Role) > 64 ||
			!progressBrowserPublicText(element.Name, 512) || !progressBrowserPublicText(element.Type, 64) ||
			element.Disabled == nil {
			return "", false
		}
		refs[element.Ref] = true
		controls = append(controls, progressTuple(element.Role, element.Name, element.Type, *element.Disabled))
	}
	sort.Strings(controls) // Preserve duplicate names/roles and their multiplicity.
	encoded, _ := json.Marshal(controls)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), true
}

func progressBrowserPublicText(value string, maximumRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximumRunes &&
		!strings.ContainsRune(value, '\x00') && !strings.Contains(value, "[REDACTED") && redact.String(value) == value
}
