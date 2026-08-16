package runner

import "time"

// OnceCommandProposal is the immutable structured request recorded by the
// one_shot_command_propose tool. Environment values are never stored; only
// sorted key names and their digest are durable.
type OnceCommandProposal struct {
	ID                          string
	ProtocolVersion             string
	OperationKeyDigest          string
	RequestFingerprint          string
	RunID                       string
	RootAgentID                 string
	SessionID                   string
	WorkspaceID                 string
	ExecutablePath              string
	Argv                        []string
	WorkingDirectory            string
	EnvironmentKeys             []string
	EnvironmentSHA256           string
	TimeoutMilliseconds         int64
	Purpose                     string
	SpecFingerprint             string
	Status                      string
	Reviewer                    string
	ReviewReason                string
	ReviewedAt                  *time.Time
	ApprovalFingerprint         string
	ExecutionRequestFingerprint string
	CreatedAt                   time.Time
}

type OnceCommandProposalOperation struct {
	KeyDigest          string
	RequestFingerprint string
	ProposalID         string
}
