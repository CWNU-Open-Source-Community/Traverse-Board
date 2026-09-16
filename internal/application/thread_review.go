package application

import (
	"context"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
)

const (
	MaxThreadReviewRuns      = 50
	MaxThreadReviewChanges   = 500
	MaxThreadReviewChecks    = 200
	MaxThreadReviewDiffBytes = 512 * 1024
)

// ThreadReview is a read-only projection of existing records and a current
// filesystem observation. It does not attribute arbitrary shell/user changes
// to this Thread, create a checkpoint, or authorize a mutation.
type ThreadReview struct {
	ThreadID         string               `json:"thread_id"`
	ThreadVersion    int64                `json:"thread_version"`
	CurrentRunID     string               `json:"current_run_id"`
	ObservedAt       time.Time            `json:"observed_at"`
	ChangeScope      string               `json:"change_scope"`
	TotalRuns        int                  `json:"total_runs"`
	Runs             []ThreadReviewRun    `json:"runs"`
	Target           ThreadReviewTarget   `json:"target"`
	Revision         ThreadReviewRevision `json:"revision"`
	AppliedChanges   []ThreadReviewChange `json:"applied_changes"`
	UnappliedChanges []ThreadReviewChange `json:"unapplied_changes"`
	Checks           []ThreadReviewCheck  `json:"checks"`
	Partial          bool                 `json:"partial"`
	Reasons          []string             `json:"reasons"`
}

type ThreadReviewRun struct {
	RunID               string `json:"run_id"`
	SessionID           string `json:"session_id"`
	Ordinal             int64  `json:"ordinal"`
	WorkspaceID         string `json:"workspace_id,omitempty"`
	SourceEventSequence int64  `json:"source_event_sequence"`
	HandoffURL          string `json:"handoff_url"`
}

type ThreadReviewTarget struct {
	State             string `json:"state"` // available | unavailable
	SourceWorkspaceID string `json:"source_workspace_id"`
	WorkspaceID       string `json:"workspace_id,omitempty"`
	Kind              string `json:"kind"` // source | drydock | unknown
	DrydockID         string `json:"drydock_id,omitempty"`
	RootPath          string `json:"root_path,omitempty"`
	RootFingerprint   string `json:"root_fingerprint,omitempty"`
}

type ThreadReviewRevision struct {
	State          string   `json:"state"`           // available | partial | unavailable
	RepositoryKind string   `json:"repository_kind"` // git | none | unknown
	Head           string   `json:"head,omitempty"`
	Branch         string   `json:"branch,omitempty"`
	Dirty          *bool    `json:"dirty,omitempty"`
	IndexSHA256    string   `json:"index_sha256,omitempty"`
	ManifestSHA256 string   `json:"manifest_sha256,omitempty"`
	RevisionSHA256 string   `json:"revision_sha256,omitempty"`
	Reasons        []string `json:"reasons"`
}

type ThreadReviewChange struct {
	RunID                     string    `json:"run_id"`
	SessionID                 string    `json:"session_id"`
	WorkspaceID               string    `json:"workspace_id"`
	EditID                    string    `json:"edit_id"`
	Operation                 string    `json:"operation"`
	Path                      string    `json:"path"`
	DestinationPath           string    `json:"destination_path,omitempty"`
	Status                    string    `json:"status"`
	OriginalSHA256            string    `json:"original_sha256"`
	ProposedSHA256            string    `json:"proposed_sha256"`
	DestinationOriginalSHA256 string    `json:"destination_original_sha256,omitempty"`
	DestinationProposedSHA256 string    `json:"destination_proposed_sha256,omitempty"`
	CurrentSHA256             string    `json:"current_sha256,omitempty"`
	CurrentMatch              string    `json:"current_match"` // matches | changed | missing | unavailable | other_target
	Diff                      string    `json:"diff"`
	DiffTruncated             bool      `json:"diff_truncated"`
	Redacted                  bool      `json:"redacted"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type ThreadReviewCheck struct {
	RunID                  string    `json:"run_id"`
	ID                     string    `json:"id"`
	SourceKind             string    `json:"source_kind"` // host_command | verification_evidence | standard_code
	Title                  string    `json:"title"`
	Outcome                string    `json:"outcome"` // recorded outcome, never inferred from revision state
	ExitCode               *int      `json:"exit_code,omitempty"`
	RevisionState          string    `json:"revision_state"` // current | stale | unbound | unavailable
	RecordedRevisionSHA256 string    `json:"recorded_revision_sha256,omitempty"`
	Reason                 string    `json:"reason"`
	RecordedAt             time.Time `json:"recorded_at"`
	HandoffURL             string    `json:"handoff_url"`
}

type ThreadReviewStore interface {
	RunFileWorkspaceStore
	GetThread(context.Context, string) (domain.Thread, error)
	ListThreadRuns(context.Context, string) ([]domain.ThreadRun, error)
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	LatestRunEventSequence(context.Context, string) (int64, error)
	ListFileEditPreviewsPage(context.Context, fileedit.ListFilter, int, int) ([]fileedit.Preview, error)
}

type ThreadReviewHandoffReader interface {
	Build(context.Context, string) (CodeHandoff, error)
}

type ThreadReviewService struct {
	store    ThreadReviewStore
	drydocks *DrydockService
	handoffs ThreadReviewHandoffReader
}

func NewThreadReviewService(store ThreadReviewStore) *ThreadReviewService {
	return &ThreadReviewService{store: store}
}

func (s *ThreadReviewService) WithDrydock(value *DrydockService) *ThreadReviewService {
	s.drydocks = value
	return s
}

func (s *ThreadReviewService) WithCodeHandoff(value ThreadReviewHandoffReader) *ThreadReviewService {
	s.handoffs = value
	return s
}
