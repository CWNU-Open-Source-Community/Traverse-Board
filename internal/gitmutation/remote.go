package gitmutation

import "time"

// RemoteOperation is the closed typed remote set. Force push, remote branch
// deletion, and protected-branch mutation are not expressible.
type RemoteOperation string

const (
	RemoteFetch      RemoteOperation = "fetch"
	RemotePullFF     RemoteOperation = "pull_ff"
	RemotePushBranch RemoteOperation = "push_branch"
	RemoteCreatePR   RemoteOperation = "create_pr"
	RemoteUpdatePR   RemoteOperation = "update_pr"
)

func (o RemoteOperation) Valid() bool {
	switch o {
	case RemoteFetch, RemotePullFF, RemotePushBranch, RemoteCreatePR, RemoteUpdatePR:
		return true
	default:
		return false
	}
}

// RemoteRecord is the durable network-scoped ledger row. The credential name
// is never stored; only redacted evidence is.
type RemoteRecord struct {
	ID                 string
	ProtocolVersion    string
	OperationKeyDigest string
	RequestFingerprint string
	RunID              string
	WorkspaceID        string
	Operation          RemoteOperation
	SpecJSON           string
	RemoteHost         string
	RemotePort         string
	Protocol           string
	Branch             string
	PreHead            string
	PostHead           string
	CommitID           string
	PullRequestURL     string
	PullRequestNumber  int64
	StderrPrefix       string
	CompletedAt        *time.Time
	CreatedAt          time.Time
}
