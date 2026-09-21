package fileedit

// AutoAuthorization is the immutable source of an automatic file edit decision.
// RuntimeEpoch and RuntimeGeneration identify the live process grant that the
// caller checked; the store also binds the durable Run, permission, mode, root
// Agent, and execution lease before creating the approved edit.
type AutoAuthorization struct {
	RunID                   string
	SessionID               string
	WorkspaceID             string
	OperationKeyDigest      string
	ProposalFingerprint     string
	Operation               string
	Path                    string
	DestinationPath         string
	OriginalHash            string
	ProposedHash            string
	DestinationOriginalHash string
	DestinationProposedHash string
	PermissionSnapshotID    string
	PermissionRevision      int64
	ModeRevision            int64
	RuntimeEpoch            string
	RuntimeGeneration       uint64
	AgentID                 string
	CapabilityGeneration    string
	LeaseID                 string
	LeaseGeneration         int64
}
