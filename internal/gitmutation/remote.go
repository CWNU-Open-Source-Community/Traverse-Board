package gitmutation

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
