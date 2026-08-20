package githubreview

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/gitadvanced"
)

// SemanticResult is the package-neutral evidence projection consumed by the
// GitHub graph builder. Keeping the Provider model independent of the LSP
// runtime prevents persistence packages from importing Workspace code through
// a transitive dependency.
type SemanticResult struct {
	Valid                 bool
	State                 EvidenceState
	Tool                  string
	Commit                string
	DocumentSHA256        string
	ServerGeneration      string
	CapabilityFingerprint string
	QueryFingerprint      string
	Warnings              []string
	Items                 []SemanticItem
}

type SemanticItem struct {
	Path           string
	Name           string
	Relationship   string
	HasRange       bool
	StartLine      int
	StartCharacter int
}

// BuildEvidenceGraph binds sanitized remote comments to the exact #117
// merge-base/hunk state and optionally to #116 semantic results. A mismatch is
// evidence staleness, never an instruction to mutate or re-run a tool.
func BuildEvidenceGraph(snapshot Snapshot, diff gitadvanced.ReviewDiffEvidence,
	semantic map[string][]SemanticResult, now time.Time,
) (EvidenceGraph, error) {
	if snapshot.Validate() != nil || diff.Validate() != nil || now.IsZero() {
		return EvidenceGraph{}, errors.New("GitHub review evidence graph input is invalid")
	}
	fileHashes := make(map[string]string)
	hunkIDs := make([]string, 0, len(diff.Hunks))
	for _, hunk := range diff.Hunks {
		hunkIDs = append(hunkIDs, hunk.ID)
		if hunk.WorktreeSHA256 != "" {
			fileHashes[hunk.Path] = hunk.WorktreeSHA256
		}
	}
	sort.Strings(hunkIDs)
	graph := EvidenceGraph{ProtocolVersion: EvidenceProtocolVersion,
		SnapshotID: snapshot.ID, SnapshotFingerprint: snapshot.Fingerprint,
		Local: LocalBinding{RepositorySHA256: diff.Binding.RepositorySHA256,
			HeadSHA: diff.Binding.Head, MergeBaseSHA: diff.MergeBaseSHA,
			IndexSHA256:    diff.Binding.IndexSHA256,
			WorktreeSHA256: diff.Binding.WorktreeSHA256,
			StatusSHA256:   diff.Binding.StatusSHA256,
			FileSHA256:     fileHashes, CapturedAt: diff.Binding.CapturedAt},
		Git: GitEvidence{ProtocolVersion: diff.ProtocolVersion,
			DiffSHA256: diff.DiffSHA256, CallChainSHA256: diff.CallChainSHA256,
			DiffBytes: diff.DiffBytes, DiffStat: diff.DiffStat,
			ChangedFiles: append([]string(nil), diff.ChangedFiles...), HunkIDs: hunkIDs,
			ConflictActive: diff.Conflict.Active, Complete: diff.Complete,
			Omissions: append([]string(nil), diff.Omissions...)},
		State: EvidenceVerified, Mappings: []PositionMapping{}, Omissions: []string{},
		CreatedAt: now.UTC()}
	globalState := EvidenceVerified
	globalReasons := make([]string, 0)
	if snapshot.State != EvidenceVerified {
		globalState = mergeEvidenceState(globalState, snapshot.State)
		globalReasons = append(globalReasons, "remote snapshot is "+string(snapshot.State))
	}
	if !diff.Complete {
		globalState = mergeEvidenceState(globalState, EvidencePartial)
		globalReasons = append(globalReasons, diff.Omissions...)
	}
	if diff.BaseSHA != snapshot.Identity.BaseSHA {
		globalState = EvidenceStale
		globalReasons = append(globalReasons, "base commit drifted")
	}
	if diff.HeadSHA != snapshot.Identity.HeadSHA || diff.Binding.Head != snapshot.Identity.HeadSHA {
		globalState = EvidenceStale
		globalReasons = append(globalReasons, "head commit drifted")
	}
	if diff.MergeBaseSHA != snapshot.Identity.MergeBaseSHA {
		globalState = EvidenceStale
		globalReasons = append(globalReasons, "merge-base drifted")
	}
	if snapshot.Identity.Merged || snapshot.Identity.State != "open" {
		globalState = EvidenceStale
		globalReasons = append(globalReasons, "pull request is closed or merged")
	}
	if diff.Conflict.Active {
		globalState = mergeEvidenceState(globalState, EvidencePartial)
		globalReasons = append(globalReasons, "local repository has unresolved conflicts")
	}

	seen := map[string]struct{}{}
	for _, thread := range snapshot.Threads {
		for _, comment := range thread.Comments {
			if _, exists := seen[comment.NodeID]; exists {
				continue
			}
			seen[comment.NodeID] = struct{}{}
			graph.Mappings = append(graph.Mappings,
				mapComment(snapshot, diff, semantic, thread.ID, thread.Outdated, comment,
					globalState, globalReasons))
		}
	}
	for _, comment := range snapshot.LooseComments {
		if _, exists := seen[comment.NodeID]; exists {
			continue
		}
		seen[comment.NodeID] = struct{}{}
		graph.Mappings = append(graph.Mappings,
			mapComment(snapshot, diff, semantic, comment.ThreadID, false, comment,
				globalState, globalReasons))
	}
	graph.State = globalState
	for _, mapping := range graph.Mappings {
		graph.State = mergeEvidenceState(graph.State, mapping.State)
		if mapping.State != EvidenceVerified {
			graph.Omissions = append(graph.Omissions, mapping.Reasons...)
		}
	}
	graph.Omissions = uniqueSorted(append(graph.Omissions, globalReasons...))
	parts := []string{"evidence-graph", graph.SnapshotFingerprint,
		graph.Local.RepositorySHA256, graph.Local.HeadSHA, graph.Local.MergeBaseSHA,
		graph.Local.IndexSHA256, graph.Local.WorktreeSHA256, graph.Local.StatusSHA256,
		graph.Git.DiffSHA256, graph.Git.CallChainSHA256, string(graph.State)}
	for _, mapping := range graph.Mappings {
		parts = append(parts, mapping.CommentID, mapping.Path, fmt.Sprint(mapping.Line),
			mapping.RemoteCommitSHA, mapping.LocalFileSHA256, mapping.HunkID,
			string(mapping.State), strings.Join(mapping.Semantic.QueryFingerprints, ","))
	}
	graph.Fingerprint = Fingerprint(parts...)
	if err := graph.Validate(); err != nil {
		return EvidenceGraph{}, err
	}
	return graph, nil
}

func mapComment(snapshot Snapshot, diff gitadvanced.ReviewDiffEvidence,
	semantic map[string][]SemanticResult, threadID string, outdated bool,
	comment Comment, global EvidenceState, globalReasons []string,
) PositionMapping {
	position := comment.Position
	remoteCommit := position.CommitSHA
	if !validObjectID(remoteCommit) {
		remoteCommit = snapshot.Identity.HeadSHA
	}
	mapping := PositionMapping{ThreadID: threadID, CommentID: comment.NodeID,
		Path: position.Path, Side: position.Side, Line: position.Line,
		OriginalLine: position.OriginalLine, RemoteCommitSHA: remoteCommit,
		State: global, Reasons: append([]string(nil), globalReasons...),
		Semantic: SemanticEvidence{State: EvidenceNotRun,
			QueryFingerprints: []string{}, Definitions: []string{}, References: []string{},
			Callers: []string{}, Callees: []string{}, Diagnostics: []string{}, Omissions: []string{}}}
	for _, hunk := range diff.Hunks {
		if hunk.Path != mapping.Path || !lineInsideHunk(mapping.Side, mapping.Line,
			mapping.OriginalLine, hunk) {
			continue
		}
		mapping.HunkID = hunk.ID
		mapping.LocalFileSHA256 = hunk.WorktreeSHA256
		break
	}
	if mapping.LocalFileSHA256 == "" {
		mapping.LocalFileSHA256 = localFileHash(diff.Hunks, mapping.Path)
	}
	if outdated || remoteCommit != snapshot.Identity.HeadSHA {
		mapping.State = EvidenceStale
		mapping.Reasons = append(mapping.Reasons,
			"comment position is outdated or bound to a previous commit")
	}
	if mapping.Path == "" || mapping.Line <= 0 {
		mapping.State = mergeEvidenceState(mapping.State, EvidencePartial)
		mapping.Reasons = append(mapping.Reasons, "comment has no current file/line position")
	} else if mapping.HunkID == "" {
		mapping.State = mergeEvidenceState(mapping.State, EvidencePartial)
		mapping.Reasons = append(mapping.Reasons, "comment position did not match a stable merge-base hunk")
	}
	mapping.Semantic = collectSemantic(semantic[mapping.Path], diff.Binding.Head,
		mapping.LocalFileSHA256)
	if mapping.Semantic.State == EvidenceStale {
		mapping.State = EvidenceStale
		mapping.Reasons = append(mapping.Reasons, "semantic evidence binding is stale")
	} else if mapping.Semantic.State == EvidencePartial ||
		mapping.Semantic.State == EvidenceUnavailable {
		mapping.State = mergeEvidenceState(mapping.State, EvidencePartial)
		mapping.Reasons = append(mapping.Reasons,
			"semantic evidence is "+string(mapping.Semantic.State))
	}
	mapping.Reasons = uniqueSorted(mapping.Reasons)
	return mapping
}

func collectSemantic(results []SemanticResult, head, fileSHA string) SemanticEvidence {
	evidence := SemanticEvidence{State: EvidenceNotRun, QueryFingerprints: []string{},
		Definitions: []string{}, References: []string{}, Callers: []string{},
		Callees: []string{}, Diagnostics: []string{}, Omissions: []string{}}
	if len(results) == 0 {
		return evidence
	}
	evidence.State = EvidenceVerified
	for _, result := range results {
		if !result.Valid || !result.State.Valid() {
			evidence.State = EvidenceStale
			evidence.Omissions = append(evidence.Omissions, "invalid semantic result")
			continue
		}
		state := result.State
		if result.Commit != "" && result.Commit != head {
			state = EvidenceStale
		}
		if fileSHA != "" && result.DocumentSHA256 != "" && result.DocumentSHA256 != fileSHA {
			state = EvidenceStale
		}
		evidence.State = mergeEvidenceState(evidence.State, state)
		if evidence.ServerGeneration == "" {
			evidence.ServerGeneration = result.ServerGeneration
			evidence.CapabilityFingerprint = result.CapabilityFingerprint
		} else if evidence.ServerGeneration != result.ServerGeneration ||
			evidence.CapabilityFingerprint != result.CapabilityFingerprint {
			evidence.State = EvidenceStale
			evidence.Omissions = append(evidence.Omissions, "semantic server generation changed")
		}
		evidence.QueryFingerprints = append(evidence.QueryFingerprints, result.QueryFingerprint)
		for _, warning := range result.Warnings {
			evidence.Omissions = append(evidence.Omissions, boundedPlainText(warning, 300))
		}
		for _, item := range result.Items {
			value := semanticItemLabel(item)
			switch result.Tool {
			case "code_definition", "code_implementation", "code_document_symbols":
				evidence.Definitions = append(evidence.Definitions, value)
			case "code_references", "code_type_hierarchy":
				evidence.References = append(evidence.References, value)
			case "code_call_hierarchy":
				if item.Relationship == "incoming" {
					evidence.Callers = append(evidence.Callers, value)
				} else {
					evidence.Callees = append(evidence.Callees, value)
				}
			case "code_diagnostics":
				evidence.Diagnostics = append(evidence.Diagnostics, value)
			}
		}
	}
	evidence.QueryFingerprints = uniqueSorted(evidence.QueryFingerprints)
	evidence.Definitions = uniqueSorted(evidence.Definitions)
	evidence.References = uniqueSorted(evidence.References)
	evidence.Callers = uniqueSorted(evidence.Callers)
	evidence.Callees = uniqueSorted(evidence.Callees)
	evidence.Diagnostics = uniqueSorted(evidence.Diagnostics)
	evidence.Omissions = uniqueSorted(evidence.Omissions)
	return evidence
}

func semanticItemLabel(item SemanticItem) string {
	value := item.Path
	if item.HasRange {
		value += fmt.Sprintf(":%d:%d", item.StartLine+1, item.StartCharacter+1)
	}
	if item.Name != "" {
		value += " " + item.Name
	}
	return boundedPlainText(value, 500)
}

func lineInsideHunk(side string, line, originalLine int, hunk gitadvanced.Hunk) bool {
	if strings.EqualFold(side, "LEFT") {
		candidate := originalLine
		if candidate <= 0 {
			candidate = line
		}
		return candidate >= hunk.OldStart && candidate < hunk.OldStart+maxInt(hunk.OldLines, 1)
	}
	return line >= hunk.NewStart && line < hunk.NewStart+maxInt(hunk.NewLines, 1)
}

func localFileHash(hunks []gitadvanced.Hunk, path string) string {
	for _, hunk := range hunks {
		if hunk.Path == path && hunk.WorktreeSHA256 != "" {
			return hunk.WorktreeSHA256
		}
	}
	return ""
}

func mergeEvidenceState(left, right EvidenceState) EvidenceState {
	weight := func(value EvidenceState) int {
		switch value {
		case EvidenceStale:
			return 5
		case EvidenceUnavailable:
			return 4
		case EvidencePartial:
			return 3
		case EvidenceNotRun:
			return 2
		case EvidenceVerified:
			return 1
		default:
			return 4
		}
	}
	if weight(right) > weight(left) {
		return right
	}
	return left
}

func maxInt(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}
