package application

import (
	"time"

	"cyberagent-workbench/internal/repository"
)

const ThreadGitProtocolVersion = "thread_git.v1"

// ThreadGitSpec is a closed operator action, never raw Git arguments.
type ThreadGitSpec struct {
	Operation      string   `json:"operation"`
	Paths          []string `json:"paths,omitempty"`
	Message        string   `json:"message,omitempty"`
	Branch         string   `json:"branch,omitempty"`
	RemoteURL      string   `json:"remote_url,omitempty"`
	CredentialName string   `json:"credential_name,omitempty"`
	WorktreeName   string   `json:"worktree_name,omitempty"`
}

type ThreadGitRemote struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

type ThreadGitContext struct {
	ThreadID          string            `json:"thread_id"`
	RunID             string            `json:"run_id"`
	SessionID         string            `json:"session_id"`
	WorkspaceID       string            `json:"workspace_id"`
	SourceWorkspaceID string            `json:"source_workspace_id"`
	RootPath          string            `json:"repository_root"`
	HeadSHA           string            `json:"head_oid"`
	Branch            string            `json:"branch"`
	StatusFingerprint string            `json:"binding_fingerprint"`
	RemoteURLs        []ThreadGitRemote `json:"remotes"`
}

type ThreadGitState struct {
	Version string `json:"version"`
	ThreadGitContext
	Branches      []string            `json:"branches"`
	Changes       []repository.Change `json:"changes"`
	Truncated     bool                `json:"truncated"`
	CanExecute    bool                `json:"can_execute"`
	BlockedReason string              `json:"blocked_reason,omitempty"`
}

type ThreadGitPreviewRequest struct {
	Version string        `json:"version"`
	RunID   string        `json:"run_id"`
	Spec    ThreadGitSpec `json:"spec"`
}

type ThreadGitPreview struct {
	Version string `json:"version"`
	ThreadGitContext
	Spec               ThreadGitSpec               `json:"spec"`
	PreviewFingerprint string                      `json:"preview_fingerprint"`
	Diff               string                      `json:"diff"`
	ExpectedRemoteOID  string                      `json:"expected_remote_oid,omitempty"`
	TargetCommitOID    string                      `json:"target_commit_oid,omitempty"`
	CanExecute         bool                        `json:"can_execute"`
	BlockedReason      string                      `json:"blocked_reason,omitempty"`
	CommitAuthor       *repository.GitCommitAuthor `json:"commit_author,omitempty"`
}

type ThreadGitExecuteRequest struct {
	Version                    string        `json:"version"`
	RunID                      string        `json:"run_id"`
	Spec                       ThreadGitSpec `json:"spec"`
	OperationKey               string        `json:"operation_key"`
	ExpectedPreviewFingerprint string        `json:"expected_preview_fingerprint"`
	RequestedBy                string        `json:"requested_by"`
}

// A completed result has either a durable receipt or exact, read-only proof of
// the original intent. Observing never starts or repeats an operation.
type ThreadGitResult struct {
	Version      string         `json:"version"`
	ThreadID     string         `json:"thread_id"`
	RunID        string         `json:"run_id"`
	WorkspaceID  string         `json:"workspace_id,omitempty"`
	OperationID  string         `json:"operation_id,omitempty"`
	State        string         `json:"state"` // not_received, completed, unknown
	Spec         *ThreadGitSpec `json:"spec,omitempty"`
	Replayed     bool           `json:"replayed"`
	ReceiptSaved bool           `json:"receipt_saved"`
	Observed     bool           `json:"observed"`
	CommitOID    string         `json:"commit_oid,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	RemoteOID    string         `json:"remote_oid,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	WorktreePath string         `json:"worktree_path,omitempty"`
}
