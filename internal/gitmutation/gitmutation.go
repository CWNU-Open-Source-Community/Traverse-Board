package gitmutation

import "time"

// ProtocolVersion is the typed local Git mutation protocol.
const ProtocolVersion = "repository_mutation.v1"

// Operation is the closed typed-operation set. Destructive or history-
// rewriting git commands cannot be expressed.
type Operation string

const (
	Stage        Operation = "stage"
	Unstage      Operation = "unstage"
	Commit       Operation = "commit"
	CreateBranch Operation = "create_branch"
	SwitchBranch Operation = "switch_branch"
)

func (o Operation) Valid() bool {
	switch o {
	case Stage, Unstage, Commit, CreateBranch, SwitchBranch:
		return true
	default:
		return false
	}
}

// Record is the durable operation ledger row. Receipts are metadata-only;
// stderr prefixes are bounded and never carry raw diffs.
type Record struct {
	ID                 string
	ProtocolVersion    string
	OperationKeyDigest string
	RequestFingerprint string
	RunID              string
	WorkspaceID        string
	Operation          Operation
	SpecJSON           string
	PreHead            string
	PostHead           string
	Branch             string
	CommitID           string
	Conflicted         bool
	Clean              bool
	StderrPrefix       string
	CompletedAt        *time.Time
	CreatedAt          time.Time
}
