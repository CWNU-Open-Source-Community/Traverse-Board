// Package drydock defines the durable, non-authorizing contract for Standard
// Code's product-managed Git worktrees. A Drydock is an ownership and recovery
// boundary; it is not a process, filesystem, or network sandbox.
package drydock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	WorkspaceProtocolVersion = "drydock-workspace.v1"
	TrustProtocolVersion     = "drydock-workspace-trust.v1"
	ReceiptProtocolVersion   = "drydock-lifecycle-receipt.v1"
	DeliveryProtocolVersion  = "drydock-delivery-proposal.v1"

	MaxActiveTotal         = 64
	MaxActivePerRepository = 8
	MaxList                = 1_000
	MaxChangedPaths        = 3_000
	MaxPatchBytes          = 16 * 1024 * 1024
	DefaultLifetime        = 7 * 24 * time.Hour
	MaximumLifetime        = 30 * 24 * time.Hour
)

type State string

const (
	StatePreparing        State = "preparing"
	StateReady            State = "ready"
	StateRecoveryRequired State = "recovery_required"
	StateDelivered        State = "delivered"
	StateCleaned          State = "cleaned"
)

func (s State) Valid() bool {
	switch s {
	case StatePreparing, StateReady, StateRecoveryRequired, StateDelivered, StateCleaned:
		return true
	default:
		return false
	}
}

func (s State) Active() bool { return s.Valid() && s != StateCleaned }

type Operation string

const (
	OperationCreate     Operation = "create"
	OperationUse        Operation = "use"
	OperationCheckpoint Operation = "checkpoint"
	OperationRewind     Operation = "rewind"
	OperationUndo       Operation = "undo"
	OperationFork       Operation = "fork"
	OperationDeliver    Operation = "deliver"
	OperationCleanup    Operation = "cleanup"
	OperationRecover    Operation = "recover"
)

func (o Operation) Valid() bool {
	switch o {
	case OperationCreate, OperationUse, OperationCheckpoint, OperationRewind, OperationUndo,
		OperationFork, OperationDeliver, OperationCleanup, OperationRecover:
		return true
	default:
		return false
	}
}

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomePreserved Outcome = "preserved"
	OutcomeFailed    Outcome = "failed"
)

func (o Outcome) Valid() bool {
	return o == OutcomeSucceeded || o == OutcomePreserved || o == OutcomeFailed
}

// SourceIdentity binds the registered source Workspace to a real directory,
// one Git common repository, one branch, and one exact base commit. RootPath is
// application-private and deliberately omitted from serialized projections.
type SourceIdentity struct {
	WorkspaceID      string `json:"workspace_id"`
	RootPath         string `json:"-"`
	RootPathSHA256   string `json:"root_path_sha256"`
	RootFingerprint  string `json:"root_fingerprint"`
	RepositorySHA256 string `json:"repository_sha256"`
	CommonDirSHA256  string `json:"common_dir_sha256"`
	Branch           string `json:"branch"`
	BaseCommit       string `json:"base_commit"`
	ObjectFormat     string `json:"object_format"`
}

func (s SourceIdentity) Fingerprint() string {
	return Fingerprint("source-identity", s.WorkspaceID, s.RootPathSHA256,
		s.RootFingerprint, s.RepositorySHA256, s.CommonDirSHA256, s.Branch,
		s.BaseCommit, s.ObjectFormat)
}

func (s SourceIdentity) Validate() error {
	if !validIdentity(s.WorkspaceID) || !filepath.IsAbs(s.RootPath) ||
		filepath.Clean(s.RootPath) != s.RootPath || strings.ContainsRune(s.RootPath, 0) ||
		!ValidDigest(s.RootPathSHA256) || !ValidDigest(s.RootFingerprint) ||
		!ValidDigest(s.RepositorySHA256) || !ValidDigest(s.CommonDirSHA256) ||
		!validBranch(s.Branch) || !ValidObjectID(s.BaseCommit) ||
		(s.ObjectFormat != "sha1" && s.ObjectFormat != "sha256") {
		return errors.New("Drydock source identity is invalid")
	}
	return nil
}

// SourceState records what was deliberately excluded from the new Worktree.
// Dirty source content is never copied silently into a Drydock.
type SourceState struct {
	IndexSHA256      string    `json:"index_sha256"`
	WorktreeSHA256   string    `json:"worktree_sha256"`
	StatusSHA256     string    `json:"status_sha256"`
	DirtyTracked     bool      `json:"dirty_tracked"`
	DirtyUntracked   bool      `json:"dirty_untracked"`
	DirtyIgnored     bool      `json:"dirty_ignored"`
	SymlinkEntries   int       `json:"symlink_entries"`
	SubmoduleEntries int       `json:"submodule_entries"`
	CapturedAt       time.Time `json:"captured_at"`
}

func (s SourceState) Fingerprint() string {
	return Fingerprint("source-state", s.IndexSHA256, s.WorktreeSHA256,
		s.StatusSHA256, fmt.Sprintf("%t", s.DirtyTracked),
		fmt.Sprintf("%t", s.DirtyUntracked), fmt.Sprintf("%t", s.DirtyIgnored),
		fmt.Sprintf("%d", s.SymlinkEntries), fmt.Sprintf("%d", s.SubmoduleEntries))
}

func TrustConfirmationDigest(source SourceIdentity, state SourceState) string {
	return Fingerprint("workspace-trust-confirmation", source.Fingerprint(),
		state.Fingerprint())
}

func (s SourceState) Validate() error {
	if !ValidDigest(s.IndexSHA256) || !ValidDigest(s.WorktreeSHA256) ||
		!ValidDigest(s.StatusSHA256) || s.SymlinkEntries < 0 ||
		s.SubmoduleEntries < 0 || s.CapturedAt.IsZero() {
		return errors.New("Drydock source state is invalid")
	}
	return nil
}

// Trust is an immutable operator receipt for one exact Run/source identity.
// GrantsProcessAuthority is required to remain false in every representation.
type Trust struct {
	ID                     string         `json:"id"`
	ProtocolVersion        string         `json:"protocol_version"`
	RunID                  string         `json:"run_id"`
	WorkspaceID            string         `json:"workspace_id"`
	Source                 SourceIdentity `json:"source"`
	SourceState            SourceState    `json:"source_state"`
	ConfirmedBy            string         `json:"confirmed_by"`
	GrantsProcessAuthority bool           `json:"grants_process_authority"`
	ConfirmedAt            time.Time      `json:"confirmed_at"`
}

func (t Trust) Validate() error {
	if !validIdentity(t.ID) || !validIdentity(t.RunID) ||
		!validIdentity(t.WorkspaceID) || !validIdentity(t.ConfirmedBy) ||
		t.ProtocolVersion != TrustProtocolVersion || t.WorkspaceID != t.Source.WorkspaceID ||
		t.GrantsProcessAuthority || t.ConfirmedAt.IsZero() ||
		t.Source.Validate() != nil || t.SourceState.Validate() != nil {
		return errors.New("Drydock Workspace Trust receipt is invalid")
	}
	return nil
}

type Workspace struct {
	ID                         string         `json:"id"`
	ProtocolVersion            string         `json:"protocol_version"`
	RunID                      string         `json:"run_id"`
	MissionID                  string         `json:"mission_id"`
	SessionID                  string         `json:"session_id"`
	SourceWorkspaceID          string         `json:"source_workspace_id"`
	WorkspaceID                string         `json:"workspace_id"`
	TrustID                    string         `json:"trust_id"`
	Source                     SourceIdentity `json:"source"`
	Name                       string         `json:"name"`
	Path                       string         `json:"-"`
	PathSHA256                 string         `json:"path_sha256"`
	Branch                     string         `json:"branch"`
	BaseCommit                 string         `json:"base_commit"`
	RootFingerprint            string         `json:"root_fingerprint,omitempty"`
	ExpectedHead               string         `json:"expected_head,omitempty"`
	ExpectedBindingFingerprint string         `json:"expected_binding_fingerprint,omitempty"`
	CreatePreviewID            string         `json:"create_preview_id"`
	CreateGitReceiptID         string         `json:"create_git_receipt_id,omitempty"`
	ManagedWorktreeID          string         `json:"managed_worktree_id,omitempty"`
	State                      State          `json:"state"`
	Generation                 int64          `json:"generation"`
	LastCheckpointID           string         `json:"last_checkpoint_id,omitempty"`
	LastDeliveryID             string         `json:"last_delivery_id,omitempty"`
	RecoveryReason             string         `json:"recovery_reason,omitempty"`
	ExpiresAt                  time.Time      `json:"expires_at"`
	CreatedAt                  time.Time      `json:"created_at"`
	UpdatedAt                  time.Time      `json:"updated_at"`
	CleanedAt                  *time.Time     `json:"cleaned_at,omitempty"`
}

func (w Workspace) Validate() error {
	for _, value := range []string{w.ID, w.RunID, w.MissionID, w.SessionID,
		w.SourceWorkspaceID, w.WorkspaceID, w.TrustID, w.Name, w.CreatePreviewID} {
		if !validIdentity(value) {
			return errors.New("Drydock identity is invalid")
		}
	}
	for _, value := range []string{w.CreateGitReceiptID, w.ManagedWorktreeID,
		w.LastCheckpointID, w.LastDeliveryID} {
		if value != "" && !validIdentity(value) {
			return errors.New("Drydock optional identity is invalid")
		}
	}
	if w.ProtocolVersion != WorkspaceProtocolVersion || w.Source.Validate() != nil ||
		w.SourceWorkspaceID != w.Source.WorkspaceID {
		return errors.New("Drydock source binding is invalid")
	}
	if !filepath.IsAbs(w.Path) || filepath.Clean(w.Path) != w.Path ||
		strings.ContainsRune(w.Path, 0) || !ValidDigest(w.PathSHA256) {
		return errors.New("Drydock path identity is invalid")
	}
	if !validBranch(w.Branch) {
		return errors.New("Drydock branch is invalid")
	}
	if w.BaseCommit != w.Source.BaseCommit || !ValidObjectID(w.BaseCommit) {
		return errors.New("Drydock base commit is invalid")
	}
	if !w.State.Valid() || w.Generation < 1 || !validReason(w.RecoveryReason) {
		return errors.New("Drydock lifecycle metadata is invalid")
	}
	if w.CreatedAt.IsZero() || w.UpdatedAt.Before(w.CreatedAt) ||
		w.ExpiresAt.Before(w.CreatedAt) || w.ExpiresAt.After(w.CreatedAt.Add(MaximumLifetime)) {
		return errors.New("Drydock lifetime metadata is invalid")
	}
	hasNoMaterializationEvidence := w.RootFingerprint == "" && w.ExpectedHead == "" &&
		w.ExpectedBindingFingerprint == "" && w.ManagedWorktreeID == ""
	hasCompleteMaterializationEvidence := ValidDigest(w.RootFingerprint) &&
		ValidObjectID(w.ExpectedHead) && ValidDigest(w.ExpectedBindingFingerprint) &&
		validIdentity(w.ManagedWorktreeID)
	if w.State == StatePreparing {
		if !hasNoMaterializationEvidence || w.CleanedAt != nil {
			return errors.New("preparing Drydock has terminal materialization evidence")
		}
	} else if w.State == StateReady || w.State == StateDelivered {
		if !hasCompleteMaterializationEvidence {
			return errors.New("materialized Drydock identity is incomplete")
		}
	} else if !hasNoMaterializationEvidence && !hasCompleteMaterializationEvidence {
		return errors.New("Drydock materialization evidence is partial")
	}
	if w.State == StateRecoveryRequired && w.RecoveryReason == "" {
		return errors.New("recovery-required Drydock has no reason")
	}
	if w.State != StateRecoveryRequired && w.RecoveryReason != "" {
		return errors.New("non-recovery Drydock has a recovery reason")
	}
	if w.State == StateCleaned {
		if w.CleanedAt == nil || w.CleanedAt.Before(w.UpdatedAt) {
			return errors.New("cleaned Drydock timestamp is invalid")
		}
	} else if w.CleanedAt != nil {
		return errors.New("active Drydock has a cleanup timestamp")
	}
	return nil
}

type Receipt struct {
	ID                     string    `json:"id"`
	ProtocolVersion        string    `json:"protocol_version"`
	OperationKeySHA256     string    `json:"operation_key_sha256"`
	RequestFingerprint     string    `json:"request_fingerprint"`
	DrydockID              string    `json:"drydock_id"`
	RunID                  string    `json:"run_id"`
	Operation              Operation `json:"operation"`
	Outcome                Outcome   `json:"outcome"`
	GenerationBefore       int64     `json:"generation_before"`
	GenerationAfter        int64     `json:"generation_after"`
	SourceIdentitySHA256   string    `json:"source_identity_sha256"`
	RootFingerprint        string    `json:"root_fingerprint,omitempty"`
	BindingBeforeSHA256    string    `json:"binding_before_sha256,omitempty"`
	BindingAfterSHA256     string    `json:"binding_after_sha256,omitempty"`
	GitReceiptID           string    `json:"git_receipt_id,omitempty"`
	CheckpointID           string    `json:"checkpoint_id,omitempty"`
	DeliveryID             string    `json:"delivery_id,omitempty"`
	ReasonCode             string    `json:"reason_code,omitempty"`
	Summary                string    `json:"summary"`
	GrantsProcessAuthority bool      `json:"grants_process_authority"`
	CreatedAt              time.Time `json:"created_at"`
}

func (r Receipt) Validate() error {
	for _, value := range []string{r.ID, r.DrydockID, r.RunID} {
		if !validIdentity(value) {
			return errors.New("Drydock receipt identity is invalid")
		}
	}
	for _, value := range []string{r.GitReceiptID, r.CheckpointID, r.DeliveryID} {
		if value != "" && !validIdentity(value) {
			return errors.New("Drydock receipt optional identity is invalid")
		}
	}
	if r.ProtocolVersion != ReceiptProtocolVersion || !ValidDigest(r.OperationKeySHA256) ||
		!ValidDigest(r.RequestFingerprint) || !r.Operation.Valid() || !r.Outcome.Valid() ||
		r.GenerationBefore < 1 || r.GenerationAfter != r.GenerationBefore+1 ||
		!ValidDigest(r.SourceIdentitySHA256) || r.GrantsProcessAuthority ||
		!validReason(r.ReasonCode) || !validSummary(r.Summary) || r.CreatedAt.IsZero() {
		return errors.New("Drydock receipt metadata is invalid")
	}
	for _, digest := range []string{r.RootFingerprint, r.BindingBeforeSHA256,
		r.BindingAfterSHA256} {
		if digest != "" && !ValidDigest(digest) {
			return errors.New("Drydock receipt digest is invalid")
		}
	}
	if r.Outcome != OutcomeSucceeded && r.ReasonCode == "" {
		return errors.New("non-success Drydock receipt has no reason code")
	}
	return nil
}

type DeliveryProposal struct {
	ID                     string    `json:"id"`
	ProtocolVersion        string    `json:"protocol_version"`
	OperationKeySHA256     string    `json:"operation_key_sha256"`
	RequestFingerprint     string    `json:"request_fingerprint"`
	DrydockID              string    `json:"drydock_id"`
	RunID                  string    `json:"run_id"`
	Generation             int64     `json:"generation"`
	SourceIdentitySHA256   string    `json:"source_identity_sha256"`
	RootFingerprint        string    `json:"root_fingerprint"`
	BaseCommit             string    `json:"base_commit"`
	HeadCommit             string    `json:"head_commit"`
	MergeBaseCommit        string    `json:"merge_base_commit"`
	BindingFingerprint     string    `json:"binding_fingerprint"`
	DiffSHA256             string    `json:"diff_sha256"`
	DiffBytes              int       `json:"diff_bytes"`
	DiffStat               string    `json:"diff_stat"`
	ChangedPaths           []string  `json:"changed_paths"`
	CheckpointID           string    `json:"checkpoint_id"`
	CreatedBy              string    `json:"created_by"`
	AutomaticMerge         bool      `json:"automatic_merge"`
	PushAuthorized         bool      `json:"push_authorized"`
	ForceAuthorized        bool      `json:"force_authorized"`
	SourceOverwriteAllowed bool      `json:"source_overwrite_allowed"`
	CreatedAt              time.Time `json:"created_at"`
}

func (p DeliveryProposal) Validate() error {
	for _, value := range []string{p.ID, p.DrydockID, p.RunID, p.CheckpointID,
		p.CreatedBy} {
		if !validIdentity(value) {
			return errors.New("Drydock delivery identity is invalid")
		}
	}
	if p.ProtocolVersion != DeliveryProtocolVersion || !ValidDigest(p.OperationKeySHA256) ||
		!ValidDigest(p.RequestFingerprint) || p.Generation < 1 ||
		!ValidDigest(p.SourceIdentitySHA256) || !ValidDigest(p.RootFingerprint) ||
		!ValidObjectID(p.BaseCommit) || !ValidObjectID(p.HeadCommit) ||
		!ValidObjectID(p.MergeBaseCommit) || !ValidDigest(p.BindingFingerprint) ||
		!ValidDigest(p.DiffSHA256) || p.DiffBytes < 0 || p.DiffBytes > MaxPatchBytes ||
		len(p.ChangedPaths) > MaxChangedPaths || len([]byte(p.DiffStat)) > 4096 ||
		!utf8.ValidString(p.DiffStat) || strings.ContainsRune(p.DiffStat, 0) ||
		p.AutomaticMerge || p.PushAuthorized || p.ForceAuthorized ||
		p.SourceOverwriteAllowed || p.CreatedAt.IsZero() {
		return errors.New("Drydock delivery proposal is invalid")
	}
	seen := make(map[string]struct{}, len(p.ChangedPaths))
	for _, path := range p.ChangedPaths {
		if !validRelativePath(path) {
			return errors.New("Drydock delivery path is invalid")
		}
		if _, duplicate := seen[path]; duplicate {
			return errors.New("Drydock delivery contains a duplicate path")
		}
		seen[path] = struct{}{}
	}
	return nil
}

type Review struct {
	Proposal DeliveryProposal `json:"proposal"`
	Patch    string           `json:"patch"`
}

func (r Review) Validate() error {
	if r.Proposal.Validate() != nil || !utf8.ValidString(r.Patch) ||
		strings.ContainsRune(r.Patch, 0) || len([]byte(r.Patch)) != r.Proposal.DiffBytes ||
		FingerprintBytes([]byte(r.Patch)) != r.Proposal.DiffSHA256 {
		return errors.New("Drydock delivery review is invalid")
	}
	return nil
}

type ListFilter struct {
	RunID            string
	RepositorySHA256 string
	State            State
	ExpiredBefore    *time.Time
	IncludeCleaned   bool
	Limit            int
}

func Fingerprint(domain string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	for _, value := range values {
		_, _ = fmt.Fprintf(hash, "\x00%d:", len(value))
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func FingerprintBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func ValidDigest(value string) bool {
	return len(value) == 64 && value == strings.ToLower(value) && validHex(value)
}

func ValidObjectID(value string) bool {
	return (len(value) == 40 || len(value) == 64) && value == strings.ToLower(value) && validHex(value)
}

func validHex(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == len(value)
}

func validIdentity(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		len([]rune(value)) <= 256 && !strings.ContainsRune(value, 0)
}

func validReason(value string) bool {
	return value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		len([]rune(value)) <= 512 && !strings.ContainsRune(value, 0)
}

func validSummary(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		len([]rune(value)) <= 4096 && !strings.ContainsRune(value, 0)
}

func validBranch(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		len([]byte(value)) <= 255 && !strings.ContainsRune(value, 0) &&
		!strings.HasPrefix(value, "-") && !strings.ContainsAny(value, " ~^:?*[\\") &&
		!strings.Contains(value, "..") && !strings.Contains(value, "@{") &&
		!strings.HasSuffix(value, ".") && !strings.HasSuffix(value, "/") &&
		!strings.HasSuffix(value, ".lock") && !strings.Contains(value, "//")
}

func validRelativePath(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		strings.ContainsRune(value, 0) || strings.ContainsAny(value, `\:`) ||
		strings.HasPrefix(value, "/") || value == "." || value == ".." ||
		strings.HasPrefix(value, "../") || filepath.ToSlash(filepath.Clean(value)) != value {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component != strings.TrimSpace(component) ||
			strings.EqualFold(component, ".git") {
			return false
		}
	}
	for _, current := range value {
		if current < 0x20 || current == 0x7f {
			return false
		}
	}
	return true
}

func CanonicalChangedPathsJSON(paths []string) (string, error) {
	value, err := json.Marshal(paths)
	if err != nil {
		return "", err
	}
	return string(value), nil
}
