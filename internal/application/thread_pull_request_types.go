package application

import (
	"context"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/githubreview"
)

const ThreadPullRequestVersion = "thread_pull_request.v1"
const ThreadPullRequestApprovalTool = "github.pull_request"

type ThreadPullRequestGitReader interface {
	CaptureThreadGitContext(context.Context, string) (ThreadGitContext, error)
}

type ThreadPullRequestDiscovery struct {
	Version                 string                          `json:"version"`
	Context                 ThreadGitContext                `json:"context"`
	ConnectionID            string                          `json:"connection_id"`
	Repository              githubreview.RepositoryIdentity `json:"repository"`
	BaseBranch              string                          `json:"base_branch"`
	BaseSHA                 string                          `json:"base_sha"`
	RemoteHeadSHA           string                          `json:"remote_head_sha,omitempty"`
	HeadPublished           bool                            `json:"head_published"`
	PullRequests            []githubreview.PullRequest      `json:"pull_requests"`
	WriteEnabled            bool                            `json:"write_enabled"`
	WritePermissionVerified bool                            `json:"write_permission_verified"`
	Diagnostics             []githubreview.Diagnostic       `json:"diagnostics"`
	CheckedAt               time.Time                       `json:"checked_at"`
}

type ThreadPullRequestPreviewRequest struct {
	Version         string `json:"version"`
	ConnectionID    string `json:"connection_id"`
	BaseBranch      string `json:"base_branch"`
	Title           string `json:"title"`
	Body            string `json:"body"`
	ExpectedRunID   string `json:"expected_run_id"`
	ExpectedHeadSHA string `json:"expected_head_sha"`
	OperationKey    string `json:"operation_key"`
}

// Preview is immutable user-visible intent; credentials are references only.
// The original operation key is not persisted or returned in this value.
type ThreadPullRequestPreview struct {
	Version              string                        `json:"version"`
	OperationID          string                        `json:"operation_id"`
	ThreadID             string                        `json:"thread_id"`
	RunID                string                        `json:"run_id"`
	SessionID            string                        `json:"session_id"`
	WorkspaceID          string                        `json:"workspace_id"`
	SourceWorkspaceID    string                        `json:"source_workspace_id"`
	ConnectionID         string                        `json:"connection_id"`
	ConnectionGeneration int64                         `json:"connection_generation"`
	BindingFingerprint   string                        `json:"binding_fingerprint"`
	Draft                githubreview.PullRequestDraft `json:"draft"`
	DraftOnly            bool                          `json:"draft_only"`
	ApprovalFingerprint  string                        `json:"approval_fingerprint"`
	CreatedAt            time.Time                     `json:"created_at"`
}

type ThreadPullRequestPreviewResult struct {
	Version              string                     `json:"version"`
	Preview              *ThreadPullRequestPreview  `json:"preview,omitempty"`
	Approval             *approval.Record           `json:"approval,omitempty"`
	ExistingPullRequests []githubreview.PullRequest `json:"existing_pull_requests"`
	Replayed             bool                       `json:"replayed"`
}

type ThreadPullRequestCreateRequest struct {
	Version     string `json:"version"`
	OperationID string `json:"operation_id"`
	ApprovalID  string `json:"approval_id"`
}

type ThreadPullRequestResult struct {
	Version             string                    `json:"version"`
	ThreadID            string                    `json:"thread_id"`
	RunID               string                    `json:"run_id,omitempty"`
	OperationID         string                    `json:"operation_id,omitempty"`
	State               string                    `json:"state"` // not_received, proposed, unknown, created, failed
	Preview             *ThreadPullRequestPreview `json:"preview,omitempty"`
	Approval            *approval.Record          `json:"approval,omitempty"`
	PullRequest         *githubreview.PullRequest `json:"pull_request,omitempty"`
	HeadMatchesReviewed bool                      `json:"head_matches_reviewed"`
	ReceiptSaved        bool                      `json:"receipt_saved"`
	Replayed            bool                      `json:"replayed"`
	ErrorCode           string                    `json:"error_code,omitempty"`
	ErrorMessage        string                    `json:"error_message,omitempty"`
	CheckedAt           time.Time                 `json:"checked_at"`
}

type ThreadPullRequestRefreshRequest struct {
	Version      string `json:"version"`
	ConnectionID string `json:"connection_id"`
	PullRequest  int64  `json:"pull_request"`
}

type ThreadPullRequestRefreshResult struct {
	Version          string                       `json:"version"`
	ThreadID         string                       `json:"thread_id"`
	RunID            string                       `json:"run_id"`
	Snapshot         githubreview.Snapshot        `json:"snapshot"`
	Evidence         *githubreview.EvidenceRecord `json:"evidence,omitempty"`
	LocalHeadSHA     string                       `json:"local_head_sha"`
	HeadMatchesLocal bool                         `json:"head_matches_local"`
	Stale            bool                         `json:"stale"`
	Omissions        []string                     `json:"omissions"`
}

type ThreadPullRequestCredentialRequest struct {
	Version      string `json:"version"`
	ConnectionID string `json:"connection_id"`
	Token        string `json:"token"`
}
