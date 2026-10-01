package application

import (
	"encoding/json"
	pathpkg "path"
	"sort"
	"strings"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
)

type supervisorReceiptProposal struct {
	Version     string          `json:"version"`
	Status      string          `json:"status"`
	EditID      string          `json:"edit_id"`
	Path        string          `json:"path"`
	Destination string          `json:"destination_path"`
	Operation   string          `json:"operation"`
	Original    string          `json:"original_sha256"`
	Proposed    string          `json:"proposed_sha256"`
	Authorized  *bool           `json:"apply_authorized"`
	Review      *bool           `json:"review_required"`
	Arguments   json.RawMessage `json:"apply_arguments"`
}

// Choose only the latest relevant observation per edit and affected path.
// New denied/review/applied observations block older approved priority. Native
// observations also block duplicate priority in the withdrawn portion.
type supervisorReceiptApplySelection struct {
	Priority   map[string]bool
	Superseded map[string]bool
}

func supervisorReceiptApplyPriority(receipted, native []domain.SupervisorToolRound) supervisorReceiptApplySelection {
	var candidates, blockers []domain.SupervisorToolCall
	for _, round := range receipted {
		candidates = append(candidates, round.Calls...)
	}
	for _, round := range native {
		blockers = append(blockers, round.Calls...)
	}
	full := supervisorReceiptApplyPriorityCalls(candidates, blockers)
	selection := supervisorReceiptApplySelection{Priority: map[string]bool{}, Superseded: map[string]bool{}}
	for _, call := range candidates {
		key := supervisorReceiptCallKey(call)
		if full.Priority[key] {
			selection.Priority[call.CallID] = true
		}
		if full.Superseded[key] {
			selection.Superseded[call.CallID] = true
		}
	}
	return selection
}

func supervisorReceiptCallKey(call domain.SupervisorToolCall) string {
	return domain.SupervisorToolResultReference(call).SourceID
}

// Boundary lookups may contain partial rounds and reused provider call IDs.
// Use exact provenance and actual coordinates, never reverse the SQL ordering.
func supervisorReceiptApplyPriorityCalls(candidates, blockers []domain.SupervisorToolCall, completedSources ...map[int]string) supervisorReceiptApplySelection {
	eligible := map[string]bool{}
	for _, call := range candidates {
		eligible[supervisorReceiptCallKey(call)] = true
	}
	all := append(append([]domain.SupervisorToolCall(nil), candidates...), blockers...)
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Turn != b.Turn {
			return a.Turn > b.Turn
		}
		if len(completedSources) > 0 {
			completed := completedSources[0][a.Turn]
			aCompleted, bCompleted := a.AttemptID == completed, b.AttemptID == completed
			if aCompleted != bCompleted {
				return aCompleted
			}
			// Other attempts contribute blockers only. Keep their stable source
			// order as one equivalence class, including within an old attempt.
			if !aCompleted {
				return false
			}
		}
		if a.Round != b.Round {
			return a.Round > b.Round
		}
		return a.Position > b.Position
	})
	seenCalls := map[string]bool{}
	seenEdit, seenPath, priority := map[string]bool{}, map[string]bool{}, map[string]bool{}
	seenUnknownPath, seenAnyPath, superseded := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, call := range all {
		key := supervisorReceiptCallKey(call)
		if seenCalls[key] {
			continue
		}
		seenCalls[key] = true
		if call.ToolName != "workspace_change" && call.ToolName != "workspace_apply" && call.ToolName != "workspace_delete" {
			continue
		}
		var envelope supervisorToolResultEnvelope
		if json.Unmarshal([]byte(call.ResultJSON), &envelope) != nil {
			continue
		}
		var result supervisorReceiptProposal
		_ = json.Unmarshal([]byte(envelope.Stdout), &result)
		var input supervisorReceiptProposal
		_ = json.Unmarshal([]byte(call.PayloadJSON), &input)
		if result.EditID == "" {
			result.EditID = input.EditID
		}
		if result.Path == "" {
			result.Path = input.Path
		}
		if result.Destination == "" {
			result.Destination = input.Destination
		}
		scope := ""
		if authority, err := toolgateway.DecodeAgentCodeCallAuthority(json.RawMessage(call.AuthorityJSON)); err == nil && authority.RunID == call.RunID {
			scope = authority.WorkspaceID + "\x00" + authority.RootFingerprint + "\x00"
		} else if envelope.Metadata["workspace_id"] != "" && envelope.Metadata["root_fingerprint"] != "" {
			scope = envelope.Metadata["workspace_id"] + "\x00" + envelope.Metadata["root_fingerprint"] + "\x00"
		}
		blocked := result.EditID != "" && seenEdit[result.EditID]
		for _, path := range []string{result.Path, result.Destination} {
			if path == "" {
				continue
			}
			// Failed payloads need not have normalized paths. This key only
			// suppresses old optional context; it never becomes an apply input.
			keyPath := pathpkg.Clean(strings.ReplaceAll(path, "\\", "/"))
			if scope == "" {
				blocked = blocked || seenAnyPath[keyPath]
				seenUnknownPath[keyPath] = true
			} else {
				key := scope + keyPath
				blocked = blocked || seenPath[key] || seenUnknownPath[keyPath]
				seenPath[key] = true
			}
			seenAnyPath[keyPath] = true
		}
		if result.EditID != "" {
			seenEdit[result.EditID] = true
		}
		if blocked && call.ToolName == "workspace_change" {
			superseded[key] = true
		}
		if blocked || !eligible[key] || call.ToolName != "workspace_change" || call.Status != domain.SupervisorToolCompleted || call.ErrorCode != "" ||
			envelope.Truncated || envelope.Version != supervisorToolResultVersion || envelope.Tool != call.ToolName || envelope.Status != "completed" {
			continue
		}
		effect := domain.ObservedSupervisorToolEffect(call)
		if effect == nil || effect.Effect != "proposal_only" || effect.EditStatus != "approved" || result.Authorized == nil || !*result.Authorized || result.Review == nil || *result.Review {
			continue
		}
		normalized, err := toolgateway.NormalizeAgentCodePayload(toolgateway.WorkspaceApplyTool, result.Arguments)
		if err != nil {
			continue
		}
		var arguments toolgateway.WorkspaceApplyPayload
		if json.Unmarshal(normalized, &arguments) != nil {
			continue
		}
		action := arguments.ExpectedAction
		if action == "propose_patch" {
			action = "replace"
		}
		if (action != "replace" && action != "create") || action != result.Operation || arguments.EditID != effect.EditID || arguments.EditID != result.EditID ||
			arguments.ExpectedOriginalSHA256 != result.Original || arguments.ExpectedProposedSHA256 != result.Proposed {
			continue
		}
		priority[key] = true
	}
	return supervisorReceiptApplySelection{Priority: priority, Superseded: superseded}
}
