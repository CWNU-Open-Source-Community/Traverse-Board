package contextmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ContinuitySnapshotProtocolVersion = "continuity_snapshot.v1"
	ContinuityNodeProtocolVersion     = "session_continuity_node.v1"
	SessionTreeProtocolVersion        = "session_tree.v1"
	MaxContinuitySummaryBytes         = 16 * 1024
	MaxContinuityRecentMessages       = 20
	MaxContinuityMessageBytes         = 4 * 1024
	MaxContinuityMemoryReferences     = 200
	MaxContinuityInheritedContexts    = 256
)

type ContinuityNodeKind string

const (
	ContinuityNodeRoot       ContinuityNodeKind = "root"
	ContinuityNodeCheckpoint ContinuityNodeKind = "checkpoint"
	ContinuityNodeFork       ContinuityNodeKind = "fork"
	ContinuityNodeResume     ContinuityNodeKind = "resume"
)

type ContinuityAuthority struct {
	Capability       bool `json:"capability"`
	Approval         bool `json:"approval"`
	Process          bool `json:"process"`
	TerminalLease    bool `json:"terminal_lease"`
	Network          bool `json:"network"`
	Credential       bool `json:"credential"`
	Secret           bool `json:"secret"`
	ExecutionProfile bool `json:"execution_profile"`
}

type ContinuityMessage struct {
	ID                    int64  `json:"id"`
	Role                  string `json:"role"`
	SourceKind            string `json:"source_kind"`
	SourceRef             string `json:"source_ref,omitempty"`
	ContentSHA256         string `json:"content_sha256"`
	InstructionAuthorized bool   `json:"instruction_authorized"`
	Content               string `json:"content"`
}

type ContinuityMemoryReference struct {
	ID            string      `json:"id"`
	Scope         MemoryScope `json:"scope"`
	ScopeID       string      `json:"scope_id"`
	Version       int64       `json:"version"`
	ContentSHA256 string      `json:"content_sha256"`
}

type ContinuitySnapshot struct {
	ProtocolVersion                string                      `json:"protocol_version"`
	SourceRunID                    string                      `json:"source_run_id"`
	SourceSessionID                string                      `json:"source_session_id"`
	WorkspaceID                    string                      `json:"workspace_id"`
	SummaryID                      int64                       `json:"summary_id,omitempty"`
	SummaryContentSHA256           string                      `json:"summary_content_sha256,omitempty"`
	SummaryContent                 string                      `json:"summary_content,omitempty"`
	ThroughMessageID               int64                       `json:"through_message_id,omitempty"`
	RecentMessages                 []ContinuityMessage         `json:"recent_messages"`
	Memories                       []ContinuityMemoryReference `json:"memories"`
	ProjectConfigFingerprint       string                      `json:"project_config_fingerprint,omitempty"`
	ProjectInstructionsFingerprint string                      `json:"project_instructions_fingerprint,omitempty"`
	GitBranch                      string                      `json:"git_branch,omitempty"`
	GitHead                        string                      `json:"git_head,omitempty"`
	InheritedContext               []string                    `json:"inherited_context"`
	Authority                      ContinuityAuthority         `json:"authority"`
	Fingerprint                    string                      `json:"fingerprint"`
	CreatedAt                      time.Time                   `json:"created_at"`
}

type ContinuityNode struct {
	ID                             string             `json:"id"`
	ProtocolVersion                string             `json:"protocol_version"`
	Kind                           ContinuityNodeKind `json:"kind"`
	SessionID                      string             `json:"session_id"`
	RunID                          string             `json:"run_id"`
	WorkspaceID                    string             `json:"workspace_id"`
	ParentID                       string             `json:"parent_id,omitempty"`
	SourceNodeID                   string             `json:"source_node_id,omitempty"`
	Title                          string             `json:"title"`
	Summary                        string             `json:"summary,omitempty"`
	Snapshot                       ContinuitySnapshot `json:"snapshot"`
	ContextSHA256                  string             `json:"context_sha256"`
	ProjectConfigFingerprint       string             `json:"project_config_fingerprint,omitempty"`
	ProjectInstructionsFingerprint string             `json:"project_instructions_fingerprint,omitempty"`
	GitBranch                      string             `json:"git_branch,omitempty"`
	GitHead                        string             `json:"git_head,omitempty"`
	CreatedBy                      string             `json:"created_by"`
	CreatedAt                      time.Time          `json:"created_at"`
}

type SessionTreeNode struct {
	ID                             string    `json:"id"`
	ParentID                       string    `json:"parent_id,omitempty"`
	SourceNodeID                   string    `json:"source_node_id,omitempty"`
	Kind                           string    `json:"kind"`
	RunID                          string    `json:"run_id"`
	SessionID                      string    `json:"session_id"`
	Title                          string    `json:"title"`
	Summary                        string    `json:"summary,omitempty"`
	Fingerprint                    string    `json:"fingerprint,omitempty"`
	ProjectConfigFingerprint       string    `json:"project_config_fingerprint,omitempty"`
	ProjectInstructionsFingerprint string    `json:"project_instructions_fingerprint,omitempty"`
	GitBranch                      string    `json:"git_branch,omitempty"`
	GitHead                        string    `json:"git_head,omitempty"`
	Status                         string    `json:"status"`
	Warnings                       []string  `json:"warnings"`
	Derived                        bool      `json:"derived"`
	CreatedAt                      time.Time `json:"created_at"`
}

type SessionTree struct {
	ProtocolVersion string            `json:"protocol_version"`
	SessionID       string            `json:"session_id"`
	WorkspaceID     string            `json:"workspace_id"`
	Nodes           []SessionTreeNode `json:"nodes"`
	CapabilityGrant bool              `json:"capability_grant"`
	GeneratedAt     time.Time         `json:"generated_at"`
}

func SealContinuitySnapshot(snapshot ContinuitySnapshot) (ContinuitySnapshot, error) {
	snapshot.ProtocolVersion = ContinuitySnapshotProtocolVersion
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	} else {
		snapshot.CreatedAt = snapshot.CreatedAt.UTC()
	}
	if snapshot.RecentMessages == nil {
		snapshot.RecentMessages = []ContinuityMessage{}
	}
	if snapshot.Memories == nil {
		snapshot.Memories = []ContinuityMemoryReference{}
	}
	if snapshot.InheritedContext == nil {
		snapshot.InheritedContext = []string{}
	}
	sort.Strings(snapshot.InheritedContext)
	snapshot.Fingerprint = snapshot.continuityFingerprint()
	if err := snapshot.Validate(); err != nil {
		return ContinuitySnapshot{}, err
	}
	return snapshot, nil
}

func (s ContinuitySnapshot) Validate() error {
	if s.ProtocolVersion != ContinuitySnapshotProtocolVersion ||
		!validMemoryIdentity(s.SourceRunID) || !validMemoryIdentity(s.SourceSessionID) ||
		!validMemoryIdentity(s.WorkspaceID) || s.CreatedAt.IsZero() {
		return errors.New("continuity snapshot identity is invalid")
	}
	if len([]byte(s.SummaryContent)) > MaxContinuitySummaryBytes ||
		(s.SummaryContent != "" && memoryContentDigest(s.SummaryContent) != s.SummaryContentSHA256) ||
		(s.SummaryContent == "" && (s.SummaryID != 0 || s.SummaryContentSHA256 != "")) {
		return errors.New("continuity summary binding is invalid")
	}
	if s.ThroughMessageID < 0 || len(s.RecentMessages) > MaxContinuityRecentMessages {
		return errors.New("continuity message range is invalid")
	}
	previousMessage := int64(0)
	for _, message := range s.RecentMessages {
		if message.ID <= previousMessage || message.ID > s.ThroughMessageID ||
			len([]byte(message.Content)) > MaxContinuityMessageBytes || !utf8.ValidString(message.Content) ||
			memoryContentDigest(message.Content) != message.ContentSHA256 ||
			message.SourceKind == "" || len([]byte(message.SourceKind)) > MaxMemoryReferenceBytes ||
			len([]byte(message.SourceRef)) > MaxMemoryReferenceBytes ||
			!validMemoryText(message.SourceKind) || !validMemoryText(message.SourceRef) {
			return errors.New("continuity recent message is invalid")
		}
		if message.Role != "user" && message.Role != "assistant" && message.Role != "tool" &&
			message.Role != "system" {
			return errors.New("continuity recent message role is invalid")
		}
		if message.InstructionAuthorized &&
			(message.Role != "user" || message.SourceKind != "operator_message") {
			return errors.New("continuity message has invalid historical authority provenance")
		}
		previousMessage = message.ID
	}
	if len(s.Memories) > MaxContinuityMemoryReferences {
		return errors.New("continuity memory reference count is invalid")
	}
	previousMemory := ""
	for _, memory := range s.Memories {
		key := string(memory.Scope) + "\x00" + memory.ScopeID + "\x00" + memory.ID
		if !validMemoryIdentity(memory.ID) || !validMemoryIdentity(memory.ScopeID) ||
			(memory.Scope != MemoryScopeUser && memory.Scope != MemoryScopeProject) || memory.Version < 1 ||
			!validMemoryDigest(memory.ContentSHA256) || (previousMemory != "" && previousMemory >= key) {
			return errors.New("continuity memory reference is invalid or unsorted")
		}
		if memory.Scope == MemoryScopeUser && memory.ScopeID != LocalUserMemoryScope {
			return errors.New("continuity user memory reference has an invalid scope")
		}
		previousMemory = key
	}
	for _, digest := range []string{s.ProjectConfigFingerprint, s.ProjectInstructionsFingerprint} {
		if digest != "" && !validMemoryDigest(digest) {
			return errors.New("continuity project fingerprint is invalid")
		}
	}
	if s.GitHead != "" && (len(s.GitHead) < 40 || len(s.GitHead) > 64 || !validHex(s.GitHead)) {
		return errors.New("continuity Git head is invalid")
	}
	if len([]byte(s.GitBranch)) > MaxMemoryReferenceBytes || !validMemoryText(s.GitBranch) {
		return errors.New("continuity Git branch is invalid")
	}
	if len(s.InheritedContext) > MaxContinuityInheritedContexts {
		return errors.New("continuity inherited context list is too large")
	}
	previousContext := ""
	for _, item := range s.InheritedContext {
		if item == "" || len([]byte(item)) > MaxMemoryReferenceBytes || !validMemoryText(item) ||
			(previousContext != "" && previousContext >= item) {
			return errors.New("continuity inherited context list is invalid or unsorted")
		}
		previousContext = item
	}
	if s.Authority != (ContinuityAuthority{}) {
		return errors.New("continuity snapshot attempted to inherit authority")
	}
	if !validMemoryDigest(s.Fingerprint) || s.Fingerprint != s.continuityFingerprint() {
		return errors.New("continuity snapshot fingerprint mismatch")
	}
	return nil
}

func NewContinuityNode(id string, kind ContinuityNodeKind, sessionID, runID,
	workspaceID, parentID, sourceNodeID, title, summary, createdBy string,
	snapshot ContinuitySnapshot, at time.Time,
) (ContinuityNode, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	node := ContinuityNode{ID: strings.TrimSpace(id),
		ProtocolVersion: ContinuityNodeProtocolVersion, Kind: kind,
		SessionID: strings.TrimSpace(sessionID), RunID: strings.TrimSpace(runID),
		WorkspaceID: strings.TrimSpace(workspaceID), ParentID: strings.TrimSpace(parentID),
		SourceNodeID: strings.TrimSpace(sourceNodeID), Title: strings.TrimSpace(title),
		Summary: strings.TrimSpace(summary), Snapshot: snapshot,
		ContextSHA256:                  snapshot.Fingerprint,
		ProjectConfigFingerprint:       snapshot.ProjectConfigFingerprint,
		ProjectInstructionsFingerprint: snapshot.ProjectInstructionsFingerprint,
		GitBranch:                      snapshot.GitBranch, GitHead: snapshot.GitHead,
		CreatedBy: strings.TrimSpace(createdBy), CreatedAt: at}
	if err := node.Validate(); err != nil {
		return ContinuityNode{}, err
	}
	return node, nil
}

func (n ContinuityNode) Validate() error {
	if n.ProtocolVersion != ContinuityNodeProtocolVersion || !validMemoryIdentity(n.ID) ||
		!validMemoryIdentity(n.SessionID) || !validMemoryIdentity(n.RunID) ||
		!validMemoryIdentity(n.WorkspaceID) || !validMemoryActor(n.CreatedBy) || n.CreatedAt.IsZero() {
		return errors.New("continuity node identity or audit metadata is invalid")
	}
	switch n.Kind {
	case ContinuityNodeRoot:
		if n.ParentID != "" || n.SourceNodeID != "" {
			return errors.New("continuity root cannot have a parent or source")
		}
	case ContinuityNodeCheckpoint:
		if !validMemoryIdentity(n.ParentID) || n.SourceNodeID != "" {
			return errors.New("continuity checkpoint requires one parent")
		}
	case ContinuityNodeFork, ContinuityNodeResume:
		if n.ParentID != "" || !validMemoryIdentity(n.SourceNodeID) {
			return errors.New("continuity branch requires one source node")
		}
	default:
		return errors.New("continuity node kind is invalid")
	}
	if n.Title == "" || len([]byte(n.Title)) > 1024 || len([]byte(n.Summary)) > 4096 ||
		!validMemoryText(n.Title) || !validMemoryText(n.Summary) {
		return errors.New("continuity node title or summary is invalid")
	}
	if err := n.Snapshot.Validate(); err != nil {
		return err
	}
	if n.WorkspaceID != n.Snapshot.WorkspaceID {
		return errors.New("continuity node Workspace does not match its snapshot")
	}
	if (n.Kind == ContinuityNodeRoot || n.Kind == ContinuityNodeCheckpoint) &&
		(n.RunID != n.Snapshot.SourceRunID || n.SessionID != n.Snapshot.SourceSessionID) {
		return errors.New("continuity root or checkpoint does not match its source Run and Session")
	}
	if n.ContextSHA256 != n.Snapshot.Fingerprint ||
		n.ProjectConfigFingerprint != n.Snapshot.ProjectConfigFingerprint ||
		n.ProjectInstructionsFingerprint != n.Snapshot.ProjectInstructionsFingerprint ||
		n.GitBranch != n.Snapshot.GitBranch || n.GitHead != n.Snapshot.GitHead {
		return errors.New("continuity node snapshot projection is inconsistent")
	}
	return nil
}

func (s ContinuitySnapshot) continuityFingerprint() string {
	copy := s
	copy.Fingerprint = ""
	copy.CreatedAt = time.Time{}
	raw, _ := json.Marshal(copy)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validMemoryDigest(value string) bool {
	return len(value) == 64 && validHex(value)
}

func validHex(value string) bool {
	if value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ContinuityMemoryReferenceOf(memory Memory) ContinuityMemoryReference {
	return ContinuityMemoryReference{ID: memory.ID, Scope: memory.Scope,
		ScopeID: memory.ScopeID, Version: memory.Version, ContentSHA256: memory.ContentSHA256}
}

func SortContinuityMemoryReferences(values []ContinuityMemoryReference) {
	sort.Slice(values, func(left, right int) bool {
		leftKey := fmt.Sprintf("%s\x00%s\x00%s", values[left].Scope, values[left].ScopeID, values[left].ID)
		rightKey := fmt.Sprintf("%s\x00%s\x00%s", values[right].Scope, values[right].ScopeID, values[right].ID)
		return leftKey < rightKey
	})
}
