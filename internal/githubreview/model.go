// Package githubreview implements the bounded GitHub review evidence boundary.
// Remote GitHub content is always untrusted and credentials never appear in
// any public value defined by this package.
package githubreview

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

const (
	ProtocolVersion           = "github-review-provider.v1"
	CapabilityProtocolVersion = "github-review-capability.v1"
	SnapshotProtocolVersion   = "github-review-snapshot.v1"
	EvidenceProtocolVersion   = "github-review-evidence.v1"
	WriteProtocolVersion      = "github-review-write.v1"
	ReceiptProtocolVersion    = "github-review-receipt.v1"
	ConnectionProtocolVersion = "github-review-connection.v1"
	DeviceFlowProtocolVersion = "github-review-device-flow.v1"
	RESTAPIVersion            = "2026-03-10"
	ApprovalToolName          = "github.review"
	ApprovalActionClass       = "github_review_write"

	MaxPages                  = 30
	MaxItemsPerPage           = 100
	MaxChangedFiles           = 3000
	MaxReviews                = 500
	MaxThreads                = 500
	MaxComments               = 1000
	MaxCheckSuites            = 500
	MaxCheckRuns              = 1000
	MaxWorkflowRuns           = 20
	MaxJobs                   = 500
	MaxFailedJobLogs          = 20
	MaxArtifacts              = 500
	MaxTextBytes              = 64 * 1024
	MaxPatchBytes             = 256 * 1024
	MaxLogExcerptBytes        = 256 * 1024
	MaxResponseBytes          = 4 * 1024 * 1024
	MaxSnapshotBytes          = 16 * 1024 * 1024
	MaxCompressedLogBytes     = 8 * 1024 * 1024
	MaxUncompressedLogBytes   = 16 * 1024 * 1024
	MaxLogArchiveEntries      = 256
	MaxIdentityRunes          = 256
	MaxRepositoryNameRunes    = 100
	MaxBodySummaryRunes       = 600
	MaxValidationSummaryRunes = 4000
)

type AuthKind string

const (
	AuthGitHubAppDevice AuthKind = "github_app_device"
	AuthOAuthUser       AuthKind = "oauth_user"
	AuthFineGrainedPAT  AuthKind = "fine_grained_pat"
)

func (k AuthKind) Valid() bool {
	return k == AuthGitHubAppDevice || k == AuthOAuthUser || k == AuthFineGrainedPAT
}

type EvidenceState string

const (
	EvidenceVerified    EvidenceState = "verified"
	EvidencePartial     EvidenceState = "partial"
	EvidenceStale       EvidenceState = "stale"
	EvidenceUnavailable EvidenceState = "unavailable"
	EvidenceNotRun      EvidenceState = "not_run"
)

func (s EvidenceState) Valid() bool {
	switch s {
	case EvidenceVerified, EvidencePartial, EvidenceStale, EvidenceUnavailable, EvidenceNotRun:
		return true
	default:
		return false
	}
}

type CredentialReference struct {
	Name string   `json:"name"`
	Kind AuthKind `json:"kind"`
}

func (r CredentialReference) Validate() error {
	if !r.Kind.Valid() || !validCredentialName(r.Name) {
		return errors.New("GitHub credential reference is invalid")
	}
	return nil
}

type NetworkScope struct {
	Host            string   `json:"host"`
	APIHost         string   `json:"api_host"`
	OAuthHost       string   `json:"oauth_host"`
	AllowedLogHosts []string `json:"allowed_log_hosts"`
	ReadEnabled     bool     `json:"read_enabled"`
	WriteEnabled    bool     `json:"write_enabled"`
}

func DefaultNetworkScope() NetworkScope {
	return NetworkScope{Host: "github.com", APIHost: "api.github.com",
		OAuthHost: "github.com", AllowedLogHosts: []string{}, ReadEnabled: true}
}

func (s NetworkScope) Validate() error {
	if s.Host != "github.com" || s.APIHost != "api.github.com" || s.OAuthHost != "github.com" {
		return errors.New("production GitHub network scope must remain on github.com")
	}
	if !s.ReadEnabled || len(s.AllowedLogHosts) > 16 {
		return errors.New("GitHub network scope is invalid")
	}
	seen := map[string]struct{}{}
	for _, host := range s.AllowedLogHosts {
		if !validHost(host) || host == s.APIHost || host == s.OAuthHost {
			return errors.New("GitHub log download host is invalid")
		}
		lower := strings.ToLower(host)
		if _, exists := seen[lower]; exists {
			return errors.New("GitHub log download hosts contain a duplicate")
		}
		seen[lower] = struct{}{}
	}
	return nil
}

type RepositoryIdentity struct {
	Host     string `json:"host"`
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	NodeID   string `json:"node_id,omitempty"`
	Private  bool   `json:"private"`
}

func NewRepositoryIdentity(owner, name string) (RepositoryIdentity, error) {
	owner = strings.TrimSpace(owner)
	name = strings.TrimSpace(strings.TrimSuffix(name, ".git"))
	value := RepositoryIdentity{Host: "github.com", Owner: owner, Name: name,
		FullName: owner + "/" + name}
	if err := value.Validate(); err != nil {
		return RepositoryIdentity{}, err
	}
	return value, nil
}

func ParseRepository(value string) (RepositoryIdentity, error) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 {
		return RepositoryIdentity{}, errors.New("GitHub repository must be owner/name")
	}
	return NewRepositoryIdentity(parts[0], parts[1])
}

func (r RepositoryIdentity) Validate() error {
	if r.Host != "github.com" || !validSlug(r.Owner, MaxRepositoryNameRunes) ||
		!validSlug(r.Name, MaxRepositoryNameRunes) || r.FullName != r.Owner+"/"+r.Name ||
		(r.NodeID != "" && !validIdentity(r.NodeID)) {
		return errors.New("GitHub repository identity is invalid")
	}
	return nil
}

type PullRequestIdentity struct {
	Repository   RepositoryIdentity `json:"repository"`
	Number       int64              `json:"number"`
	NodeID       string             `json:"node_id"`
	State        string             `json:"state"`
	Draft        bool               `json:"draft"`
	Merged       bool               `json:"merged"`
	Fork         bool               `json:"fork"`
	BaseRef      string             `json:"base_ref"`
	BaseSHA      string             `json:"base_sha"`
	HeadRef      string             `json:"head_ref"`
	HeadSHA      string             `json:"head_sha"`
	MergeBaseSHA string             `json:"merge_base_sha"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

func (p PullRequestIdentity) Validate() error {
	if p.Repository.Validate() != nil || p.Number <= 0 || !validIdentity(p.NodeID) ||
		(p.State != "open" && p.State != "closed") || !validRef(p.BaseRef) ||
		!validRef(p.HeadRef) || !validObjectID(p.BaseSHA) || !validObjectID(p.HeadSHA) ||
		!validObjectID(p.MergeBaseSHA) || p.UpdatedAt.IsZero() {
		return errors.New("GitHub pull request identity is invalid")
	}
	if p.Merged && p.State != "closed" {
		return errors.New("merged GitHub pull request must be closed")
	}
	return nil
}

type RateLimit struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Used      int       `json:"used"`
	ResetAt   time.Time `json:"reset_at"`
	Resource  string    `json:"resource,omitempty"`
	RetryAt   time.Time `json:"retry_at,omitempty"`
}

type DiagnosticLevel string

const (
	DiagnosticInfo    DiagnosticLevel = "info"
	DiagnosticWarning DiagnosticLevel = "warning"
	DiagnosticError   DiagnosticLevel = "error"
)

type Diagnostic struct {
	Code        string          `json:"code"`
	Level       DiagnosticLevel `json:"level"`
	Message     string          `json:"message"`
	Remediation string          `json:"remediation,omitempty"`
}

type CapabilitySnapshot struct {
	ProtocolVersion string              `json:"protocol_version"`
	Generation      string              `json:"generation"`
	APIHost         string              `json:"api_host"`
	APIVersion      string              `json:"api_version"`
	AccountLogin    string              `json:"account_login"`
	InstallationID  int64               `json:"installation_id,omitempty"`
	Repository      RepositoryIdentity  `json:"repository"`
	Credential      CredentialReference `json:"credential"`
	Permissions     map[string]string   `json:"permissions"`
	Read            bool                `json:"read"`
	Reply           bool                `json:"reply"`
	Resolve         bool                `json:"resolve"`
	Review          bool                `json:"review"`
	RequestReviewer bool                `json:"request_reviewer"`
	Push            bool                `json:"push"`
	Logs            bool                `json:"logs"`
	CapturedAt      time.Time           `json:"captured_at"`
}

func (c CapabilitySnapshot) Validate() error {
	if c.ProtocolVersion != CapabilityProtocolVersion || !validDigest(c.Generation) ||
		c.APIHost != "api.github.com" || c.APIVersion != RESTAPIVersion ||
		!validIdentity(c.AccountLogin) || c.Repository.Validate() != nil ||
		c.Credential.Validate() != nil || !c.Read || c.CapturedAt.IsZero() ||
		len(c.Permissions) > 64 {
		return errors.New("GitHub capability snapshot is invalid")
	}
	for name, value := range c.Permissions {
		if !validPermission(name) || (value != "read" && value != "write" && value != "admin") {
			return errors.New("GitHub capability contains an invalid permission")
		}
	}
	return nil
}

type Qualification struct {
	ProtocolVersion       string             `json:"protocol_version"`
	Eligible              bool               `json:"eligible"`
	HostReachable         bool               `json:"host_reachable"`
	CredentialConfigured  bool               `json:"credential_configured"`
	Authenticated         bool               `json:"authenticated"`
	SSOAuthorized         bool               `json:"sso_authorized"`
	RepositoryAccessible  bool               `json:"repository_accessible"`
	PullRequestAccessible bool               `json:"pull_request_accessible"`
	NetworkAllowed        bool               `json:"network_allowed"`
	Capability            CapabilitySnapshot `json:"capability"`
	RateLimit             RateLimit          `json:"rate_limit"`
	Diagnostics           []Diagnostic       `json:"diagnostics"`
	CheckedAt             time.Time          `json:"checked_at"`
}

type TextEvidence struct {
	Text          string `json:"text"`
	SHA256        string `json:"sha256"`
	OriginalBytes int    `json:"original_bytes"`
	StoredBytes   int    `json:"stored_bytes"`
	Truncated     bool   `json:"truncated"`
	Redacted      bool   `json:"redacted"`
	Untrusted     bool   `json:"untrusted"`
}

type ChangedFile struct {
	Path         string       `json:"path"`
	PreviousPath string       `json:"previous_path,omitempty"`
	Status       string       `json:"status"`
	SHA          string       `json:"sha"`
	Additions    int          `json:"additions"`
	Deletions    int          `json:"deletions"`
	Changes      int          `json:"changes"`
	BlobURL      string       `json:"blob_url,omitempty"`
	RawURL       string       `json:"raw_url,omitempty"`
	Patch        TextEvidence `json:"patch"`
}

type Position struct {
	Path              string `json:"path"`
	Side              string `json:"side,omitempty"`
	Line              int    `json:"line,omitempty"`
	StartSide         string `json:"start_side,omitempty"`
	StartLine         int    `json:"start_line,omitempty"`
	OriginalPosition  int    `json:"original_position,omitempty"`
	OriginalLine      int    `json:"original_line,omitempty"`
	CommitSHA         string `json:"commit_sha"`
	OriginalCommitSHA string `json:"original_commit_sha,omitempty"`
}

type Comment struct {
	ID        int64        `json:"id,omitempty"`
	NodeID    string       `json:"node_id"`
	ThreadID  string       `json:"thread_id,omitempty"`
	Author    string       `json:"author"`
	Body      TextEvidence `json:"body"`
	Position  Position     `json:"position"`
	URL       string       `json:"url,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type ReviewThread struct {
	ID        string    `json:"id"`
	Resolved  bool      `json:"resolved"`
	Outdated  bool      `json:"outdated"`
	Path      string    `json:"path"`
	Side      string    `json:"side,omitempty"`
	Line      int       `json:"line,omitempty"`
	StartSide string    `json:"start_side,omitempty"`
	StartLine int       `json:"start_line,omitempty"`
	Comments  []Comment `json:"comments"`
}

type Review struct {
	ID          int64        `json:"id,omitempty"`
	NodeID      string       `json:"node_id"`
	Author      string       `json:"author"`
	State       string       `json:"state"`
	CommitSHA   string       `json:"commit_sha,omitempty"`
	Body        TextEvidence `json:"body"`
	SubmittedAt time.Time    `json:"submitted_at,omitempty"`
}

type CheckSuite struct {
	ID         int64     `json:"id"`
	NodeID     string    `json:"node_id,omitempty"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion,omitempty"`
	HeadSHA    string    `json:"head_sha"`
	App        string    `json:"app,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

type CheckRun struct {
	ID          int64        `json:"id"`
	NodeID      string       `json:"node_id,omitempty"`
	Name        string       `json:"name"`
	Status      string       `json:"status"`
	Conclusion  string       `json:"conclusion,omitempty"`
	HeadSHA     string       `json:"head_sha"`
	DetailsURL  string       `json:"details_url,omitempty"`
	Title       TextEvidence `json:"title"`
	Summary     TextEvidence `json:"summary"`
	Text        TextEvidence `json:"text"`
	StartedAt   time.Time    `json:"started_at,omitempty"`
	CompletedAt time.Time    `json:"completed_at,omitempty"`
}

type JobStep struct {
	Number      int       `json:"number"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

type WorkflowJob struct {
	ID          int64         `json:"id"`
	RunID       int64         `json:"run_id"`
	Name        string        `json:"name"`
	Status      string        `json:"status"`
	Conclusion  string        `json:"conclusion,omitempty"`
	HeadSHA     string        `json:"head_sha"`
	URL         string        `json:"url,omitempty"`
	Steps       []JobStep     `json:"steps"`
	FailedLog   TextEvidence  `json:"failed_log"`
	LogState    EvidenceState `json:"log_state"`
	LogReason   string        `json:"log_reason,omitempty"`
	StartedAt   time.Time     `json:"started_at,omitempty"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
}

type ArtifactMetadata struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	Expired   bool      `json:"expired"`
	Digest    string    `json:"digest,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type PageEvidence struct {
	Resource       string `json:"resource"`
	PagesRead      int    `json:"pages_read"`
	ItemsRead      int    `json:"items_read"`
	Complete       bool   `json:"complete"`
	NextCursorHash string `json:"next_cursor_hash,omitempty"`
	OmittedReason  string `json:"omitted_reason,omitempty"`
}

type Snapshot struct {
	ProtocolVersion    string              `json:"protocol_version"`
	ID                 string              `json:"id"`
	Identity           PullRequestIdentity `json:"identity"`
	Capability         CapabilitySnapshot  `json:"capability"`
	Title              TextEvidence        `json:"title"`
	Body               TextEvidence        `json:"body"`
	Author             string              `json:"author"`
	RequestedReviewers []string            `json:"requested_reviewers"`
	Files              []ChangedFile       `json:"files"`
	Reviews            []Review            `json:"reviews"`
	Threads            []ReviewThread      `json:"threads"`
	LooseComments      []Comment           `json:"loose_comments"`
	CheckSuites        []CheckSuite        `json:"check_suites"`
	CheckRuns          []CheckRun          `json:"check_runs"`
	Jobs               []WorkflowJob       `json:"jobs"`
	Artifacts          []ArtifactMetadata  `json:"artifacts"`
	Pagination         []PageEvidence      `json:"pagination"`
	RateLimit          RateLimit           `json:"rate_limit"`
	State              EvidenceState       `json:"state"`
	Omissions          []string            `json:"omissions"`
	Fingerprint        string              `json:"fingerprint"`
	FetchedAt          time.Time           `json:"fetched_at"`
}

func (s *Snapshot) Finalize() {
	s.RequestedReviewers = uniqueSorted(s.RequestedReviewers)
	s.Omissions = uniqueSorted(s.Omissions)
	parts := []string{s.Identity.Repository.FullName, fmt.Sprint(s.Identity.Number),
		s.Identity.BaseSHA, s.Identity.HeadSHA, s.Identity.MergeBaseSHA,
		s.Capability.Generation, string(s.State), s.Title.SHA256, s.Body.SHA256,
		s.Author, s.Identity.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	for _, reviewer := range s.RequestedReviewers {
		parts = append(parts, "requested-reviewer", reviewer)
	}
	for _, file := range s.Files {
		parts = append(parts, "file", file.Path, file.PreviousPath, file.Status,
			file.SHA, file.Patch.SHA256)
	}
	for _, review := range s.Reviews {
		parts = append(parts, "review", review.NodeID, review.State,
			review.CommitSHA, review.Body.SHA256)
	}
	for _, thread := range s.Threads {
		parts = append(parts, "thread", thread.ID, fmt.Sprint(thread.Resolved),
			fmt.Sprint(thread.Outdated), thread.Path, thread.Side, fmt.Sprint(thread.Line))
		for _, comment := range thread.Comments {
			parts = append(parts, "thread-comment", comment.NodeID,
				comment.Position.CommitSHA, comment.Body.SHA256)
		}
	}
	for _, comment := range s.LooseComments {
		parts = append(parts, "loose-comment", comment.NodeID,
			comment.Position.CommitSHA, comment.Body.SHA256)
	}
	for _, check := range s.CheckRuns {
		parts = append(parts, "check", fmt.Sprint(check.ID), check.Status,
			check.Conclusion, check.HeadSHA, check.Summary.SHA256, check.Text.SHA256)
	}
	for _, job := range s.Jobs {
		parts = append(parts, "job", fmt.Sprint(job.ID), job.Status,
			job.Conclusion, job.HeadSHA, string(job.LogState), job.FailedLog.SHA256)
	}
	for _, artifact := range s.Artifacts {
		parts = append(parts, "artifact", fmt.Sprint(artifact.ID), artifact.Name,
			fmt.Sprint(artifact.SizeBytes), artifact.Digest)
	}
	for _, omission := range s.Omissions {
		parts = append(parts, "omission", omission)
	}
	for _, page := range s.Pagination {
		parts = append(parts, page.Resource, fmt.Sprint(page.PagesRead),
			fmt.Sprint(page.ItemsRead), fmt.Sprint(page.Complete), page.NextCursorHash)
	}
	s.Fingerprint = Fingerprint(append([]string{"snapshot"}, parts...)...)
	if s.ID == "" {
		s.ID = "ghs-" + s.Fingerprint[:32]
	}
}

func (s Snapshot) Validate() error {
	if s.ProtocolVersion != SnapshotProtocolVersion || !validIdentity(s.ID) ||
		s.Identity.Validate() != nil || s.Capability.Validate() != nil ||
		!s.State.Valid() || !validDigest(s.Fingerprint) || s.FetchedAt.IsZero() ||
		len(s.Files) > MaxChangedFiles || len(s.Reviews) > MaxReviews ||
		len(s.Threads) > MaxThreads || len(s.LooseComments) > MaxComments ||
		len(s.CheckSuites) > MaxCheckSuites || len(s.CheckRuns) > MaxCheckRuns ||
		len(s.Jobs) > MaxJobs || len(s.Artifacts) > MaxArtifacts {
		return errors.New("GitHub review snapshot is invalid")
	}
	copy := s
	copy.ID = ""
	copy.Fingerprint = ""
	copy.Finalize()
	if copy.ID != s.ID || copy.Fingerprint != s.Fingerprint {
		return errors.New("GitHub review snapshot fingerprint is invalid")
	}
	return nil
}

type LocalBinding struct {
	RepositorySHA256 string            `json:"repository_sha256"`
	HeadSHA          string            `json:"head_sha"`
	MergeBaseSHA     string            `json:"merge_base_sha"`
	IndexSHA256      string            `json:"index_sha256"`
	WorktreeSHA256   string            `json:"worktree_sha256"`
	StatusSHA256     string            `json:"status_sha256"`
	FileSHA256       map[string]string `json:"file_sha256"`
	CapturedAt       time.Time         `json:"captured_at"`
}

type GitEvidence struct {
	ProtocolVersion string   `json:"protocol_version"`
	DiffSHA256      string   `json:"diff_sha256"`
	CallChainSHA256 string   `json:"call_chain_sha256"`
	DiffBytes       int64    `json:"diff_bytes"`
	DiffStat        string   `json:"diff_stat"`
	ChangedFiles    []string `json:"changed_files"`
	HunkIDs         []string `json:"hunk_ids"`
	ConflictActive  bool     `json:"conflict_active"`
	Complete        bool     `json:"complete"`
	Omissions       []string `json:"omissions"`
}

type SemanticEvidence struct {
	State                 EvidenceState `json:"state"`
	ServerGeneration      string        `json:"server_generation,omitempty"`
	CapabilityFingerprint string        `json:"capability_fingerprint,omitempty"`
	QueryFingerprints     []string      `json:"query_fingerprints"`
	Definitions           []string      `json:"definitions"`
	References            []string      `json:"references"`
	Callers               []string      `json:"callers"`
	Callees               []string      `json:"callees"`
	Diagnostics           []string      `json:"diagnostics"`
	Omissions             []string      `json:"omissions"`
}

type PositionMapping struct {
	ThreadID        string           `json:"thread_id,omitempty"`
	CommentID       string           `json:"comment_id"`
	Path            string           `json:"path"`
	Side            string           `json:"side,omitempty"`
	Line            int              `json:"line,omitempty"`
	OriginalLine    int              `json:"original_line,omitempty"`
	RemoteCommitSHA string           `json:"remote_commit_sha"`
	LocalFileSHA256 string           `json:"local_file_sha256,omitempty"`
	HunkID          string           `json:"hunk_id,omitempty"`
	State           EvidenceState    `json:"state"`
	Reasons         []string         `json:"reasons"`
	Semantic        SemanticEvidence `json:"semantic"`
}

type EvidenceGraph struct {
	ProtocolVersion     string            `json:"protocol_version"`
	SnapshotID          string            `json:"snapshot_id"`
	SnapshotFingerprint string            `json:"snapshot_fingerprint"`
	Local               LocalBinding      `json:"local"`
	Git                 GitEvidence       `json:"git"`
	State               EvidenceState     `json:"state"`
	Mappings            []PositionMapping `json:"mappings"`
	Omissions           []string          `json:"omissions"`
	Fingerprint         string            `json:"fingerprint"`
	CreatedAt           time.Time         `json:"created_at"`
}

type EvidenceRecord struct {
	ID          string        `json:"id"`
	RunID       string        `json:"run_id"`
	WorkspaceID string        `json:"workspace_id"`
	Graph       EvidenceGraph `json:"graph"`
}

func (g EvidenceGraph) Validate() error {
	if g.ProtocolVersion != EvidenceProtocolVersion || !validIdentity(g.SnapshotID) ||
		!validDigest(g.SnapshotFingerprint) || !validDigest(g.Local.RepositorySHA256) ||
		!validObjectID(g.Local.HeadSHA) || !validObjectID(g.Local.MergeBaseSHA) ||
		!validDigest(g.Local.IndexSHA256) || !validDigest(g.Local.WorktreeSHA256) ||
		!validDigest(g.Local.StatusSHA256) || g.Local.CapturedAt.IsZero() ||
		g.Git.ProtocolVersion != "git-review-diff-evidence.v1" ||
		!validDigest(g.Git.DiffSHA256) || !validDigest(g.Git.CallChainSHA256) ||
		!g.State.Valid() || !validDigest(g.Fingerprint) || g.CreatedAt.IsZero() ||
		len(g.Mappings) > MaxComments || len(g.Omissions) > 128 {
		return errors.New("GitHub review evidence graph is invalid")
	}
	for _, mapping := range g.Mappings {
		if !validIdentity(mapping.CommentID) || !mapping.State.Valid() ||
			!validObjectID(mapping.RemoteCommitSHA) ||
			(mapping.LocalFileSHA256 != "" && !validDigest(mapping.LocalFileSHA256)) ||
			(mapping.HunkID != "" && !validDigest(mapping.HunkID)) {
			return errors.New("GitHub review evidence mapping is invalid")
		}
		if mapping.Path == "" &&
			(mapping.Line != 0 || mapping.OriginalLine != 0 || mapping.HunkID != "" ||
				mapping.LocalFileSHA256 != "" ||
				(mapping.State != EvidencePartial && mapping.State != EvidenceStale &&
					mapping.State != EvidenceUnavailable)) {
			return errors.New("GitHub review evidence mapping without a position is invalid")
		}
	}
	return nil
}

type WriteOperation string

const (
	WriteReply           WriteOperation = "reply"
	WriteResolve         WriteOperation = "resolve"
	WriteUnresolve       WriteOperation = "unresolve"
	WriteSubmitReview    WriteOperation = "submit_review"
	WriteRequestReviewer WriteOperation = "request_reviewer"
)

func (o WriteOperation) Valid() bool {
	switch o {
	case WriteReply, WriteResolve, WriteUnresolve, WriteSubmitReview, WriteRequestReviewer:
		return true
	default:
		return false
	}
}

type WriteSpec struct {
	ProtocolVersion      string              `json:"protocol_version"`
	Operation            WriteOperation      `json:"operation"`
	Identity             PullRequestIdentity `json:"identity"`
	Credential           CredentialReference `json:"credential"`
	CapabilityGeneration string              `json:"capability_generation"`
	TargetID             string              `json:"target_id,omitempty"`
	Body                 string              `json:"body,omitempty"`
	ReviewEvent          string              `json:"review_event,omitempty"`
	Reviewers            []string            `json:"reviewers"`
	LocalChangeSummary   string              `json:"local_change_summary,omitempty"`
	ValidationSummary    string              `json:"validation_summary,omitempty"`
}

func (s *WriteSpec) Normalize() {
	s.ProtocolVersion = strings.TrimSpace(s.ProtocolVersion)
	s.Operation = WriteOperation(strings.TrimSpace(string(s.Operation)))
	s.TargetID = strings.TrimSpace(s.TargetID)
	s.Body = strings.TrimSpace(s.Body)
	s.ReviewEvent = strings.ToUpper(strings.TrimSpace(s.ReviewEvent))
	s.CapabilityGeneration = strings.TrimSpace(s.CapabilityGeneration)
	s.LocalChangeSummary = boundedPlainText(s.LocalChangeSummary, MaxValidationSummaryRunes)
	s.ValidationSummary = boundedPlainText(s.ValidationSummary, MaxValidationSummaryRunes)
	for index := range s.Reviewers {
		s.Reviewers[index] = strings.TrimSpace(s.Reviewers[index])
	}
	s.Reviewers = uniqueSorted(s.Reviewers)
}

func (s WriteSpec) Validate() error {
	if s.ProtocolVersion != WriteProtocolVersion || !s.Operation.Valid() ||
		s.Identity.Validate() != nil || s.Credential.Validate() != nil ||
		!validDigest(s.CapabilityGeneration) || len([]byte(s.Body)) > MaxTextBytes ||
		len([]rune(s.LocalChangeSummary)) > MaxValidationSummaryRunes ||
		len([]rune(s.ValidationSummary)) > MaxValidationSummaryRunes || len(s.Reviewers) > 32 {
		return errors.New("GitHub review write specification is invalid")
	}
	if strings.Contains(s.Body, "<!-- prayu-github-review:") ||
		redact.String(s.Body) != s.Body || redact.String(s.LocalChangeSummary) != s.LocalChangeSummary ||
		redact.String(s.ValidationSummary) != s.ValidationSummary {
		return errors.New("GitHub review write text contains a reserved marker or secret-shaped material")
	}
	switch s.Operation {
	case WriteReply:
		if !validIdentity(s.TargetID) || s.Body == "" || s.ReviewEvent != "" || len(s.Reviewers) != 0 {
			return errors.New("GitHub review reply requires one thread and body")
		}
	case WriteResolve, WriteUnresolve:
		if !validIdentity(s.TargetID) || s.Body != "" || s.ReviewEvent != "" || len(s.Reviewers) != 0 {
			return errors.New("GitHub thread state write is invalid")
		}
	case WriteSubmitReview:
		if s.TargetID != "" || len(s.Reviewers) != 0 ||
			(s.ReviewEvent != "COMMENT" && s.ReviewEvent != "APPROVE" && s.ReviewEvent != "REQUEST_CHANGES") ||
			(s.ReviewEvent == "REQUEST_CHANGES" && s.Body == "") {
			return errors.New("GitHub submitted review is invalid")
		}
	case WriteRequestReviewer:
		if s.TargetID != "" || s.Body != "" || s.ReviewEvent != "" || len(s.Reviewers) == 0 {
			return errors.New("GitHub reviewer request is invalid")
		}
	}
	for _, reviewer := range s.Reviewers {
		if !validSlug(reviewer, 100) {
			return errors.New("GitHub reviewer login is invalid")
		}
	}
	return nil
}

type WritePreview struct {
	ProtocolVersion      string              `json:"protocol_version"`
	ID                   string              `json:"id"`
	Operation            WriteOperation      `json:"operation"`
	Identity             PullRequestIdentity `json:"identity"`
	Credential           CredentialReference `json:"credential"`
	CapabilityGeneration string              `json:"capability_generation"`
	TargetID             string              `json:"target_id,omitempty"`
	BodySummary          string              `json:"body_summary,omitempty"`
	BodySHA256           string              `json:"body_sha256,omitempty"`
	ReviewEvent          string              `json:"review_event,omitempty"`
	Reviewers            []string            `json:"reviewers"`
	LocalChangeSummary   string              `json:"local_change_summary,omitempty"`
	ValidationSummary    string              `json:"validation_summary,omitempty"`
	IdempotencyMarker    string              `json:"idempotency_marker"`
	ApprovalFingerprint  string              `json:"approval_fingerprint"`
	CreatedAt            time.Time           `json:"created_at"`
}

func (p WritePreview) Validate() error {
	if p.ProtocolVersion != WriteProtocolVersion || !validIdentity(p.ID) ||
		!p.Operation.Valid() || p.Identity.Validate() != nil || p.Credential.Validate() != nil ||
		!validDigest(p.CapabilityGeneration) ||
		(p.BodySHA256 != "" && !validDigest(p.BodySHA256)) ||
		!validIdentity(p.IdempotencyMarker) || !validDigest(p.ApprovalFingerprint) ||
		p.CreatedAt.IsZero() || len([]rune(p.BodySummary)) > MaxBodySummaryRunes ||
		len(p.Reviewers) > 32 {
		return errors.New("GitHub review write preview is invalid")
	}
	return nil
}

func NewWritePreview(spec WriteSpec, now time.Time) (WritePreview, error) {
	spec.Normalize()
	if err := spec.Validate(); err != nil {
		return WritePreview{}, err
	}
	bodyDigest := ""
	if spec.Body != "" {
		bodyDigest = Fingerprint("body", spec.Body)
	}
	intent := Fingerprint("github-review-write", string(spec.Operation),
		spec.Identity.Repository.FullName, fmt.Sprint(spec.Identity.Number),
		spec.Identity.BaseSHA, spec.Identity.HeadSHA, spec.Identity.MergeBaseSHA,
		spec.TargetID, bodyDigest, spec.ReviewEvent, strings.Join(spec.Reviewers, "\x00"),
		spec.CapabilityGeneration, spec.Credential.Name)
	preview := WritePreview{ProtocolVersion: WriteProtocolVersion,
		ID: "ghw-" + intent[:32], Operation: spec.Operation, Identity: spec.Identity,
		Credential: spec.Credential, CapabilityGeneration: spec.CapabilityGeneration,
		TargetID: spec.TargetID, BodySummary: boundedPlainText(spec.Body, MaxBodySummaryRunes),
		BodySHA256: bodyDigest, ReviewEvent: spec.ReviewEvent,
		Reviewers:          append([]string(nil), spec.Reviewers...),
		LocalChangeSummary: spec.LocalChangeSummary,
		ValidationSummary:  spec.ValidationSummary,
		IdempotencyMarker:  "prayu-" + intent[:32], CreatedAt: now.UTC()}
	preview.ApprovalFingerprint = Fingerprint("github-review-approval", preview.ID,
		preview.IdempotencyMarker, spec.CapabilityGeneration, spec.Identity.HeadSHA,
		bodyDigest, spec.LocalChangeSummary, spec.ValidationSummary)
	return preview, nil
}

type ReceiptStatus string

const (
	ReceiptSucceeded ReceiptStatus = "succeeded"
	ReceiptRecovered ReceiptStatus = "recovered"
	ReceiptFailed    ReceiptStatus = "failed"
)

type WriteReceipt struct {
	ProtocolVersion   string              `json:"protocol_version"`
	ID                string              `json:"id"`
	PreviewID         string              `json:"preview_id"`
	Operation         WriteOperation      `json:"operation"`
	Status            ReceiptStatus       `json:"status"`
	Identity          PullRequestIdentity `json:"identity"`
	TargetID          string              `json:"target_id,omitempty"`
	ResultID          string              `json:"result_id,omitempty"`
	ResultURL         string              `json:"result_url,omitempty"`
	IdempotencyMarker string              `json:"idempotency_marker"`
	Recovered         bool                `json:"recovered"`
	ErrorCode         string              `json:"error_code,omitempty"`
	ErrorSummary      string              `json:"error_summary,omitempty"`
	StartedAt         time.Time           `json:"started_at"`
	CompletedAt       time.Time           `json:"completed_at"`
}

type Connection struct {
	ProtocolVersion string              `json:"protocol_version"`
	ID              string              `json:"id"`
	Repository      RepositoryIdentity  `json:"repository"`
	Credential      CredentialReference `json:"credential"`
	ClientID        string              `json:"client_id,omitempty"`
	Network         NetworkScope        `json:"network"`
	Enabled         bool                `json:"enabled"`
	Generation      int64               `json:"generation"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

func (c *Connection) Normalize() {
	c.ID = strings.TrimSpace(c.ID)
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.Network.AllowedLogHosts = uniqueSorted(c.Network.AllowedLogHosts)
}

func (c Connection) Validate() error {
	if c.ProtocolVersion != ConnectionProtocolVersion || !validIdentity(c.ID) ||
		c.Repository.Validate() != nil || c.Credential.Validate() != nil ||
		c.Network.Validate() != nil || c.Generation <= 0 || c.CreatedAt.IsZero() ||
		c.UpdatedAt.Before(c.CreatedAt) || len(c.ClientID) > 128 {
		return errors.New("GitHub review connection is invalid")
	}
	if c.Credential.Kind == AuthGitHubAppDevice && !validClientID(c.ClientID) {
		return errors.New("GitHub App device connection requires a public client id")
	}
	if c.Credential.Kind != AuthGitHubAppDevice && c.ClientID != "" {
		return errors.New("only GitHub App device connections may declare a client id")
	}
	return nil
}

type OperationStatus string

const (
	OperationProposed  OperationStatus = "proposed"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationRecovered OperationStatus = "recovered"
	OperationFailed    OperationStatus = "failed"
)

func (s OperationStatus) Terminal() bool {
	return s == OperationSucceeded || s == OperationRecovered || s == OperationFailed
}

type WriteRecord struct {
	ID                  string          `json:"id"`
	ProtocolVersion     string          `json:"protocol_version"`
	OperationKeySHA256  string          `json:"operation_key_sha256"`
	RequestFingerprint  string          `json:"request_fingerprint"`
	ApprovalFingerprint string          `json:"approval_fingerprint"`
	ApprovalID          string          `json:"approval_id,omitempty"`
	RunID               string          `json:"run_id"`
	SessionID           string          `json:"session_id"`
	WorkspaceID         string          `json:"workspace_id"`
	ConnectionID        string          `json:"connection_id"`
	Preview             WritePreview    `json:"preview"`
	Spec                WriteSpec       `json:"-"`
	Status              OperationStatus `json:"status"`
	Receipt             WriteReceipt    `json:"receipt"`
	ErrorCode           string          `json:"error_code,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	StartedAt           time.Time       `json:"started_at,omitempty"`
	CompletedAt         time.Time       `json:"completed_at,omitempty"`
}

type FailureCode string

const (
	FailureOffline         FailureCode = "offline"
	FailureAuthentication  FailureCode = "authentication"
	FailurePermission      FailureCode = "permission"
	FailureSSO             FailureCode = "sso"
	FailureRateLimit       FailureCode = "rate_limit"
	FailureNotFound        FailureCode = "not_found"
	FailureClosed          FailureCode = "pull_request_closed"
	FailureMerged          FailureCode = "pull_request_merged"
	FailureDrift           FailureCode = "remote_drift"
	FailurePaginationDrift FailureCode = "pagination_drift"
	FailureMalformed       FailureCode = "malformed_response"
	FailureResponseBound   FailureCode = "response_bound"
	FailureNetworkPolicy   FailureCode = "network_policy"
	FailureCredential      FailureCode = "credential"
	FailureCancelled       FailureCode = "cancelled"
	FailureConflict        FailureCode = "conflict"
	FailureUnavailable     FailureCode = "unavailable"
	FailureInterrupted     FailureCode = "interrupted_no_receipt"
)

type Error struct {
	Code       FailureCode
	Message    string
	RetryAt    time.Time
	StatusCode int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + boundedPlainText(e.Message, 500)
}

func Fingerprint(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len([]byte(part)))
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{'|'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, current := range value {
		if !strings.ContainsRune("0123456789abcdef", current) {
			return false
		}
	}
	return true
}

func validIdentity(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		len([]rune(value)) > MaxIdentityRunes {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return false
		}
	}
	return true
}

func validCredentialName(value string) bool {
	if !validIdentity(value) || len(value) > 64 {
		return false
	}
	for _, current := range value {
		if !(unicode.IsLetter(current) || unicode.IsDigit(current) || current == '-' || current == '_') {
			return false
		}
	}
	return true
}

func validSlug(value string, max int) bool {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		len([]rune(value)) > max || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, current := range value {
		if !(unicode.IsLetter(current) || unicode.IsDigit(current) || current == '-' || current == '_' || current == '.') {
			return false
		}
	}
	return true
}

func validRef(value string) bool {
	return validIdentity(value) && len([]rune(value)) <= 255 &&
		!strings.Contains(value, "..") && !strings.ContainsAny(value, "~^:?*[\\")
}

func validHost(value string) bool {
	if value == "" || value != strings.ToLower(strings.TrimSpace(value)) || len(value) > 253 ||
		strings.ContainsAny(value, "/:@[]") {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if !validSlug(part, 63) || strings.Contains(part, "_") ||
			strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
	}
	return true
}

func validPermission(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for _, current := range value {
		if !(current >= 'a' && current <= 'z') && current != '_' {
			return false
		}
	}
	return true
}

func validClientID(value string) bool {
	return validIdentity(value) && len(value) <= 128
}

func boundedPlainText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxRunes {
		value = string(runes[:maxRunes])
	}
	return value
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
