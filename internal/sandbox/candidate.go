package sandbox

import (
	"errors"
	"fmt"
	"time"
)

const (
	ExecutionCandidateProtocolVersion       = "sandbox_execution_candidate.v2"
	ExecutionCandidateLegacyProtocolVersion = "sandbox_execution_candidate.v1"
)

type ExecutionCandidate struct {
	ID                       string
	PreparationID            string
	RunID                    string
	MissionID                string
	WorkspaceID              string
	ProtocolVersion          string
	ManifestFingerprint      string
	AuthorizationFingerprint string
	WorkspaceFingerprint     string
	ScopeFingerprint         string
	PolicyFingerprint        string
	MountBindingFingerprint  string
	ApprovalID               string
	ApprovalStatus           ApprovalStatus
	MountCount               int
	RegularFileMountCount    int
	DirectoryMountCount      int
	TokensUsed               int64
	ExecutionMillisUsed      int64
	ToolCallsUsed            int64
	BudgetChecked            bool
	LeaseQuiescent           bool
	RunLeaseID               string
	RunLeaseGeneration       int64
	RunLeaseOwnerID          string
	BackendEnabled           bool
	ExecutionAuthorized      bool
	RequestedBy              string
	ValidatedAt              time.Time
}

type CandidateOperation struct {
	KeyDigest          string
	RequestFingerprint string
	CandidateID        string
	PreparationID      string
	RunID              string
	RequestedBy        string
	CreatedAt          time.Time
}

type ValidatedExecutionCandidate struct {
	Candidate ExecutionCandidate
	Replayed  bool
}

func (c ExecutionCandidate) Validate() error {
	for label, value := range map[string]string{
		"candidate id": c.ID, "preparation id": c.PreparationID, "Run id": c.RunID,
		"Mission id": c.MissionID, "workspace id": c.WorkspaceID,
		"requester": c.RequestedBy,
	} {
		if err := validateStoredIdentity(label, value); err != nil {
			return err
		}
	}
	if c.ProtocolVersion != ExecutionCandidateProtocolVersion &&
		c.ProtocolVersion != ExecutionCandidateLegacyProtocolVersion {
		return fmt.Errorf("unsupported sandbox execution candidate protocol %q", c.ProtocolVersion)
	}
	for label, digest := range map[string]string{
		"manifest": c.ManifestFingerprint, "authorization": c.AuthorizationFingerprint,
		"workspace": c.WorkspaceFingerprint, "scope": c.ScopeFingerprint,
		"policy": c.PolicyFingerprint, "mount binding": c.MountBindingFingerprint,
	} {
		if !validDigest(digest) {
			return fmt.Errorf("sandbox execution candidate %s fingerprint is invalid", label)
		}
	}
	if c.MountCount < 1 || c.MountCount > MaxMounts ||
		c.RegularFileMountCount < 0 || c.DirectoryMountCount < 0 ||
		c.RegularFileMountCount+c.DirectoryMountCount != c.MountCount {
		return errors.New("sandbox execution candidate mount counts are invalid")
	}
	if c.TokensUsed < 0 || c.ExecutionMillisUsed < 0 || c.ToolCallsUsed < 0 {
		return errors.New("sandbox execution candidate usage counters cannot be negative")
	}
	if !c.BudgetChecked || c.BackendEnabled || c.ExecutionAuthorized {
		return errors.New("sandbox execution candidate must remain budget-checked and execution-disabled")
	}
	leaseBound := c.RunLeaseID != "" || c.RunLeaseOwnerID != "" ||
		c.RunLeaseGeneration != 0
	if c.ProtocolVersion == ExecutionCandidateLegacyProtocolVersion {
		if !c.LeaseQuiescent || leaseBound {
			return errors.New("legacy sandbox execution candidate must remain quiescent")
		}
	} else if c.LeaseQuiescent == leaseBound {
		return errors.New("sandbox execution candidate must bind either quiescence or one Run lease")
	} else if leaseBound {
		if validateStoredIdentity("candidate Run lease id", c.RunLeaseID) != nil ||
			validateStoredIdentity("candidate Run lease owner", c.RunLeaseOwnerID) != nil ||
			c.RunLeaseGeneration < 1 {
			return errors.New("sandbox execution candidate Run lease binding is invalid")
		}
	}
	if c.ApprovalID == "" {
		if c.ApprovalStatus != ApprovalNotRequired {
			return errors.New("sandbox execution candidate without approval must record not_required")
		}
	} else {
		if err := validateStoredIdentity("candidate approval id", c.ApprovalID); err != nil {
			return err
		}
		if c.ApprovalStatus != ApprovalApproved {
			return errors.New("sandbox execution candidate approval must be approved")
		}
	}
	if c.ValidatedAt.IsZero() {
		return errors.New("sandbox execution candidate timestamp is required")
	}
	return nil
}

func (o CandidateOperation) Validate() error {
	for label, value := range map[string]string{
		"candidate operation candidate id":   o.CandidateID,
		"candidate operation preparation id": o.PreparationID,
		"candidate operation Run id":         o.RunID,
		"candidate operation requester":      o.RequestedBy,
	} {
		if err := validateStoredIdentity(label, value); err != nil {
			return err
		}
	}
	if !validDigest(o.KeyDigest) || !validDigest(o.RequestFingerprint) {
		return errors.New("sandbox execution candidate operation digests are invalid")
	}
	if o.CreatedAt.IsZero() {
		return errors.New("sandbox execution candidate operation timestamp is required")
	}
	return nil
}

func CandidateRequestFingerprint(preparationID, manifestFingerprint, approvalID,
	requestedBy string,
) string {
	return fingerprint("sandbox_execution_candidate_request.v1", preparationID,
		manifestFingerprint, approvalID, requestedBy)
}

// CandidateLeaseRequestFingerprint binds the v2 candidate mode. A Run-owned
// command-runtime candidate carries the exact active Run lease; ordinary
// operator candidates retain the historical quiescent mode without gaining
// execution authority.
func CandidateLeaseRequestFingerprint(preparationID, manifestFingerprint, approvalID,
	requestedBy string, leaseQuiescent bool, runLeaseID string,
	runLeaseGeneration int64, runLeaseOwnerID string,
) string {
	return fingerprint("sandbox_execution_candidate_request.v2",
		CandidateRequestFingerprint(preparationID, manifestFingerprint, approvalID,
			requestedBy), fmt.Sprintf("%t", leaseQuiescent), runLeaseID,
		fmt.Sprint(runLeaseGeneration), runLeaseOwnerID)
}

func CandidateOperationRequestFingerprint(candidate ExecutionCandidate) string {
	if candidate.ProtocolVersion == ExecutionCandidateLegacyProtocolVersion {
		return CandidateRequestFingerprint(candidate.PreparationID,
			candidate.ManifestFingerprint, candidate.ApprovalID, candidate.RequestedBy)
	}
	return CandidateLeaseRequestFingerprint(candidate.PreparationID,
		candidate.ManifestFingerprint, candidate.ApprovalID, candidate.RequestedBy,
		candidate.LeaseQuiescent, candidate.RunLeaseID,
		candidate.RunLeaseGeneration, candidate.RunLeaseOwnerID)
}
