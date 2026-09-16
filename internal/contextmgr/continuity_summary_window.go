package contextmgr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ContinuitySummaryWindowVersion = "thread_summary_window.v1"

// ContinuitySummarySource identifies exact stored text, not permission to read
// it. The caller and history reader must enforce Thread/Workspace membership.
type ContinuitySummarySource struct {
	SourceID      string `json:"source_id"`
	Part          string `json:"part"`
	ContentSHA256 string `json:"content_sha256"`
}

type continuitySummaryWindow struct {
	Version               string                    `json:"version"`
	WorkspaceID           string                    `json:"workspace_id"`
	InstructionAuthorized bool                      `json:"instruction_authorized"`
	Lossy                 bool                      `json:"lossy"`
	Sources               []ContinuitySummarySource `json:"sources"`
	Projection            handoffMemoryEnvelope     `json:"projection"`
}

type continuityWindowRef struct {
	Run         string `json:"r"`
	Fingerprint string `json:"f"`
}

// RollContinuitySummaries replaces accumulated full summaries with a bounded
// extractive window. It never writes history or grants authority. Only the
// immediate previous snapshot and current stored summary are linked; older
// sources remain reachable through that immutable previous snapshot.
// Projection counters describe candidates considered in THIS window, not a
// cumulative count of distinct messages across overlapping summary chains.
func RollContinuitySummaries(previous string, current Summary, previousSource ContinuitySummarySource) (string, error) {
	if current.ID <= 0 || !validMemoryIdentity(current.TaskID) || !validMemoryIdentity(current.WorkspaceID) ||
		current.ContentSHA256 != handoffContentSHA256(current.Content) ||
		(current.ProtocolVersion != HandoffMemoryProtocolVersion && current.ProtocolVersion != LegacyHandoffProtocolVersion) {
		return "", invalidContinuityWindow("current stored summary binding")
	}
	if err := ValidateStoredSummary(current); err != nil {
		return "", fmt.Errorf("current continuity summary: %w", err)
	}
	window := continuitySummaryWindow{
		Version: ContinuitySummaryWindowVersion, WorkspaceID: current.WorkspaceID,
		Lossy: true, Sources: []ContinuitySummarySource{},
	}
	var records []handoffMemoryRecord
	if previous != "" {
		if previousSource.Part != "summary" || !validContinuityWindowSource(previousSource) ||
			previousSource.ContentSHA256 != handoffContentSHA256(previous) {
			return "", invalidContinuityWindow("previous snapshot binding")
		}
		var err error
		records, err = continuityWindowRecords(previous, current.WorkspaceID, 0)
		if err != nil {
			return "", err
		}
		if err := continuityWindowCurrentBinding(previous, current); err != nil {
			return "", err
		}
		window.Sources = append(window.Sources, previousSource)
	} else if previousSource != (ContinuitySummarySource{}) {
		return "", invalidContinuityWindow("source without previous content")
	}
	currentRecords, err := continuityWindowRecords(current.Content, current.WorkspaceID, 0)
	if err != nil {
		return "", err
	}
	records, err = mergeContinuityWindowRecords(records, currentRecords)
	if err != nil {
		return "", err
	}
	window.Sources = append(window.Sources, ContinuitySummarySource{
		SourceID: "summary:" + strconv.FormatInt(current.ID, 10), Part: "content", ContentSHA256: current.ContentSHA256,
	})
	window.Projection = handoffMemoryEnvelope{
		Version: HandoffMemoryProtocolVersion, TaskID: current.TaskID, WorkspaceID: current.WorkspaceID,
		CompactedMessageCount: len(records), LastOrdinal: len(records), Records: []handoffMemoryRecord{},
		Generated:             generatedFromSummaryContent(current.Content),
		preferOperatorRecords: true,
	}
	if window.Projection.Generated == nil {
		window.Projection.Generated = continuityGeneratedSummary(previous)
	}
	for index := range records {
		records[index].Ordinal = index + 1
		if records[index].SourceMessageID > window.Projection.SourceThroughMessageID {
			window.Projection.SourceThroughMessageID = records[index].SourceMessageID
		}
	}
	// Reserve exact outer metadata before fitting the projection. The projection
	// is a JSON object, not a JSON string; its content is never sliced as bytes.
	outer, err := json.Marshal(window)
	if err != nil {
		return "", err
	}
	inner, err := json.Marshal(window.Projection)
	if err != nil {
		return "", err
	}
	budget := MaxHandoffMemoryChars - (utf8.RuneCount(outer) - utf8.RuneCount(inner))
	projection, err := fitHandoffRecords(context.Background(), window.Projection, records, budget, nil)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(projection), &window.Projection); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(window)
	if err != nil {
		return "", err
	}
	if err := validateContinuityWindow(window, current.WorkspaceID); err != nil {
		return "", err
	}
	if len(encoded) > MaxContinuitySummaryBytes || utf8.RuneCount(encoded) > MaxHandoffMemoryChars {
		return "", invalidContinuityWindow("encoded window exceeds budget")
	}
	return string(encoded), nil
}

func continuityGeneratedSummary(content string) *GeneratedSummary {
	if value := generatedFromSummaryContent(content); value != nil {
		return value
	}
	var window continuitySummaryWindow
	if json.Unmarshal([]byte(content), &window) == nil && window.Version == ContinuitySummaryWindowVersion {
		return window.Projection.Generated
	}
	var bundle struct {
		Kind      string `json:"kind"`
		Summaries []struct {
			Content string `json:"content"`
		} `json:"summaries"`
	}
	if json.Unmarshal([]byte(content), &bundle) == nil && bundle.Kind == "thread_summary_bundle" {
		for index := len(bundle.Summaries) - 1; index >= 0; index-- {
			if value := generatedFromSummaryContent(bundle.Summaries[index].Content); value != nil {
				return value
			}
		}
	}
	return nil
}

func continuityWindowRecords(content, workspaceID string, depth int) ([]handoffMemoryRecord, error) {
	if content == "" || !utf8.ValidString(content) || len(content) > MaxContinuitySummaryBytes || depth > 1 {
		return nil, invalidContinuityWindow("source size or nesting")
	}
	var marker struct {
		Version string `json:"version"`
		Kind    string `json:"kind"`
	}
	if json.Unmarshal([]byte(content), &marker) == nil {
		switch {
		case marker.Version == ContinuitySummaryWindowVersion:
			var window continuitySummaryWindow
			if strictContinuityWindowJSON(content, &window) != nil || utf8.RuneCountInString(content) > MaxHandoffMemoryChars {
				return nil, invalidContinuityWindow("window encoding")
			}
			if err := validateContinuityWindow(window, workspaceID); err != nil {
				return nil, err
			}
			return window.Projection.Records, nil
		case marker.Version == HandoffMemoryProtocolVersion:
			var envelope handoffMemoryEnvelope
			if strictContinuityWindowJSON(content, &envelope) != nil || !validMemoryIdentity(envelope.TaskID) ||
				utf8.RuneCountInString(content) > MaxHandoffMemoryChars ||
				validateHandoffEnvelope(envelope, envelope.TaskID, workspaceID) != nil {
				return nil, invalidContinuityWindow("handoff encoding or workspace")
			}
			return envelope.Records, nil
		case marker.Kind == "thread_summary_bundle":
			if depth != 0 {
				return nil, invalidContinuityWindow("nested summary bundle")
			}
			var bundle struct {
				Kind      string `json:"kind"`
				Summaries []struct {
					SummaryID     int64  `json:"summary_id"`
					ContentSHA256 string `json:"content_sha256"`
					Content       string `json:"content"`
				} `json:"summaries"`
			}
			if strictContinuityWindowJSON(content, &bundle) != nil || len(bundle.Summaries) < 2 {
				return nil, invalidContinuityWindow("bundle encoding")
			}
			seen := make(map[int64]string)
			var records []handoffMemoryRecord
			for _, entry := range bundle.Summaries {
				if entry.SummaryID < 0 || entry.ContentSHA256 != handoffContentSHA256(entry.Content) {
					return nil, invalidContinuityWindow("bundle entry hash")
				}
				if digest, ok := seen[entry.SummaryID]; ok {
					if digest != entry.ContentSHA256 {
						return nil, invalidContinuityWindow("conflicting summary identity")
					}
					continue
				}
				seen[entry.SummaryID] = entry.ContentSHA256
				part, err := continuityWindowRecords(entry.Content, workspaceID, depth+1)
				if err != nil {
					return nil, err
				}
				records, err = mergeContinuityWindowRecords(records, part)
				if err != nil {
					return nil, err
				}
			}
			return records, nil
		case (strings.HasPrefix(marker.Version, "handoff_memory.") && marker.Version != LegacyHandoffProtocolVersion) ||
			strings.HasPrefix(marker.Version, "thread_summary_window."):
			return nil, invalidContinuityWindow("unsupported version")
		}
	}
	// Opaque legacy text is evidence of prior context only. Its precise original
	// remains in the outer source; no role or permission is inferred from text.
	excerpt := excerptHandoffContent(content, MaxHandoffRecordChars)
	if excerpt == "" {
		return nil, invalidContinuityWindow("empty opaque source")
	}
	return []handoffMemoryRecord{{
		Category: "prior_handoff", Role: "tool", SourceKind: "compacted_transcript",
		SourceContentSHA256: handoffContentSHA256(content), ContentSHA256: handoffContentSHA256(excerpt), Content: excerpt,
	}}, nil
}

func mergeContinuityWindowRecords(previous, next []handoffMemoryRecord) ([]handoffMemoryRecord, error) {
	merged := append([]handoffMemoryRecord(nil), previous...)
	for _, candidate := range next {
		duplicate := false
		for index, existing := range merged {
			if candidate.SourceMessageID == 0 || existing.SourceMessageID != candidate.SourceMessageID {
				continue
			}
			if existing.Role != candidate.Role || existing.SourceKind != candidate.SourceKind ||
				existing.SourceRef != candidate.SourceRef || existing.InstructionAuthorized != candidate.InstructionAuthorized ||
				existing.SourceContentSHA256 != candidate.SourceContentSHA256 ||
				(existing.SourceContentSHA256 == "" && existing.ContentSHA256 != candidate.ContentSHA256) {
				return nil, invalidContinuityWindow("conflicting message identity")
			}
			if utf8.RuneCountInString(candidate.Content) > utf8.RuneCountInString(existing.Content) {
				merged[index] = candidate
			}
			duplicate = true
			break
		}
		if duplicate {
			continue
		}
		// A newer summary may restore an older record omitted in the previous
		// window. Order actual source IDs without changing their identities.
		at := len(merged)
		if candidate.SourceMessageID > 0 {
			for index, existing := range merged {
				if existing.SourceMessageID > candidate.SourceMessageID {
					at = index
					break
				}
			}
		}
		merged = append(merged, handoffMemoryRecord{})
		copy(merged[at+1:], merged[at:])
		merged[at] = candidate
	}
	return merged, nil
}

func continuityWindowCurrentBinding(previous string, current Summary) error {
	// These are already parsed/validated representations. Compare known stored
	// summary identities as well as the per-message identities checked above.
	var identity struct {
		Sources   []ContinuitySummarySource `json:"sources"`
		Summaries []struct {
			SummaryID     int64  `json:"summary_id"`
			ContentSHA256 string `json:"content_sha256"`
		} `json:"summaries"`
	}
	if json.Unmarshal([]byte(previous), &identity) != nil {
		return nil // Valid legacy opaque text need not be JSON.
	}
	for _, source := range identity.Sources {
		if source.SourceID == "summary:"+strconv.FormatInt(current.ID, 10) && source.ContentSHA256 != current.ContentSHA256 {
			return invalidContinuityWindow("conflicting current summary identity")
		}
	}
	for _, summary := range identity.Summaries {
		if summary.SummaryID == current.ID && summary.ContentSHA256 != current.ContentSHA256 {
			return invalidContinuityWindow("conflicting current summary identity")
		}
	}
	return nil
}

func validateContinuityWindow(window continuitySummaryWindow, workspaceID string) error {
	if window.Version != ContinuitySummaryWindowVersion || window.WorkspaceID != workspaceID ||
		!validMemoryIdentity(workspaceID) || window.InstructionAuthorized || !window.Lossy ||
		len(window.Sources) < 1 || len(window.Sources) > 2 || !validMemoryIdentity(window.Projection.TaskID) ||
		validateHandoffEnvelope(window.Projection, window.Projection.TaskID, workspaceID) != nil {
		return invalidContinuityWindow("window metadata or projection")
	}
	for index, source := range window.Sources {
		if !validContinuityWindowSource(source) ||
			(index == len(window.Sources)-1 && source.Part != "content") ||
			(index != len(window.Sources)-1 && source.Part != "summary") {
			return invalidContinuityWindow("window source")
		}
	}
	return nil
}

func validContinuityWindowSource(source ContinuitySummarySource) bool {
	if len(source.SourceID) > 2048 || !validHandoffDigest(source.ContentSHA256) {
		return false
	}
	switch source.Part {
	case "content":
		raw := strings.TrimPrefix(source.SourceID, "summary:")
		id, err := strconv.ParseInt(raw, 10, 64)
		return strings.HasPrefix(source.SourceID, "summary:") && err == nil && id > 0 && strconv.FormatInt(id, 10) == raw
	case "summary":
		if !strings.HasPrefix(source.SourceID, "continuity:") {
			return false
		}
		raw := strings.TrimPrefix(source.SourceID, "continuity:")
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		var ref continuityWindowRef
		if err != nil || strictContinuityWindowJSON(string(decoded), &ref) != nil ||
			!validMemoryIdentity(ref.Run) || !validHandoffDigest(ref.Fingerprint) {
			return false
		}
		canonical, err := json.Marshal(ref)
		return err == nil && base64.RawURLEncoding.EncodeToString(canonical) == raw
	default:
		return false
	}
}

func strictContinuityWindowJSON(content string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing continuity JSON")
	}
	return nil
}

func invalidContinuityWindow(reason string) error {
	return fmt.Errorf("continuity summary window: %s is invalid", reason)
}
