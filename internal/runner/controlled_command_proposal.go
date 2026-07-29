package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
)

const (
	ControlledCommandProposalProtocolVersion = "controlled_command_proposal.v1"
	ControlledCommandProposalPolicyVersion   = "controlled_command_proposal_policy.v1"
	ControlledCommandReviewProtocolVersion   = "controlled_command_proposal_review.v1"
	ControlledCommandResultProtocolVersion   = "controlled_command_proposal_result.v1"

	MaxControlledCommandPurposeRunes      = 1200
	MaxControlledCommandReviewReasonRunes = 1024
)

var ErrControlledCommandProposalBoundary = errors.New(
	"controlled command proposal boundary is invalid")

type ControlledCommandProposalRequest struct {
	ID          string
	Plan        ControlledCommandPlan
	MissionID   string
	SessionID   string
	RootAgentID string
	Permission  domain.RunExecutionPermissionSnapshot
	Purpose     string
	RequestedBy string
	CreatedAt   time.Time
}

// ControlledCommandProposal is a non-authorizing request for one Go-owned
// command template. It intentionally contains no executable, argv, shell text,
// environment, network, or persistence field.
type ControlledCommandProposal struct {
	ID                       string
	ProtocolVersion          string
	PolicyVersion            string
	RunID                    string
	MissionID                string
	SessionID                string
	WorkspaceID              string
	RootAgentID              string
	InteractionSnapshotID    string
	InteractionRevision      int64
	ExecutionProfileRevision int64
	PermissionSnapshotID     string
	PermissionRevision       int64
	PermissionMode           domain.RunExecutionPermissionMode
	PlanID                   string
	PlanFingerprint          string
	Kind                     ControlledCommandKind
	RelativePath             string
	TimeoutMilliseconds      int64
	Purpose                  string
	RequestedBy              string
	InstructionAuthorized    bool
	ExecutionAuthorized      bool
	CapabilityGrant          bool
	Fingerprint              string
	CreatedAt                time.Time
}

func NewControlledCommandProposal(
	request ControlledCommandProposalRequest,
) (ControlledCommandProposal, error) {
	request.ID = strings.TrimSpace(request.ID)
	request.MissionID = strings.TrimSpace(request.MissionID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.RootAgentID = strings.TrimSpace(request.RootAgentID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	request.Purpose = strings.TrimSpace(redact.String(request.Purpose))
	request.CreatedAt = request.CreatedAt.UTC()
	if err := request.Plan.Validate(); err != nil {
		return ControlledCommandProposal{}, err
	}
	if err := request.Permission.Validate(); err != nil {
		return ControlledCommandProposal{}, err
	}
	if request.Permission.RunID != request.Plan.RunID ||
		request.Permission.MissionID != request.MissionID ||
		!domain.ValidAgentID(request.SessionID) ||
		!domain.ValidAgentID(request.RootAgentID) ||
		request.RequestedBy != "run_supervisor" {
		return ControlledCommandProposal{}, ErrControlledCommandProposalBoundary
	}
	proposal := ControlledCommandProposal{
		ID: request.ID, ProtocolVersion: ControlledCommandProposalProtocolVersion,
		PolicyVersion: ControlledCommandProposalPolicyVersion,
		RunID:         request.Plan.RunID, MissionID: request.MissionID,
		SessionID: request.SessionID, WorkspaceID: request.Plan.WorkspaceID,
		RootAgentID:              request.RootAgentID,
		InteractionSnapshotID:    request.Plan.InteractionSnapshotID,
		InteractionRevision:      request.Plan.InteractionRevision,
		ExecutionProfileRevision: request.Plan.ExecutionProfileRevision,
		PermissionSnapshotID:     request.Permission.ID,
		PermissionRevision:       request.Permission.Revision,
		PermissionMode:           request.Permission.Mode,
		PlanID:                   request.Plan.ID,
		PlanFingerprint:          request.Plan.Fingerprint,
		Kind:                     request.Plan.Kind,
		RelativePath:             request.Plan.RelativePath,
		TimeoutMilliseconds:      request.Plan.TimeoutMilliseconds,
		Purpose:                  request.Purpose,
		RequestedBy:              request.RequestedBy,
		CreatedAt:                request.CreatedAt,
	}
	proposal.Fingerprint = ControlledCommandProposalFingerprint(proposal)
	if err := proposal.Validate(); err != nil {
		return ControlledCommandProposal{}, err
	}
	return proposal, nil
}

func (p ControlledCommandProposal) Validate() error {
	for _, value := range []string{
		p.ID, p.RunID, p.MissionID, p.SessionID, p.WorkspaceID, p.RootAgentID,
		p.InteractionSnapshotID, p.PermissionSnapshotID, p.PlanID, p.RequestedBy,
	} {
		if !validIdentity(value) {
			return ErrControlledCommandProposalBoundary
		}
	}
	if p.ProtocolVersion != ControlledCommandProposalProtocolVersion ||
		p.PolicyVersion != ControlledCommandProposalPolicyVersion ||
		p.RequestedBy != "run_supervisor" ||
		p.InteractionRevision <= 0 || p.ExecutionProfileRevision <= 0 ||
		p.PermissionRevision <= 0 || !p.PermissionMode.Valid() ||
		!validSHA256(p.PlanFingerprint) || !validSHA256(p.Fingerprint) ||
		p.TimeoutMilliseconds < time.Millisecond.Milliseconds() ||
		p.TimeoutMilliseconds > MaxControlledCommandTimeout.Milliseconds() ||
		p.InstructionAuthorized || p.ExecutionAuthorized || p.CapabilityGrant ||
		p.CreatedAt.IsZero() {
		return ErrControlledCommandProposalBoundary
	}
	if !utf8.ValidString(p.Purpose) || strings.TrimSpace(p.Purpose) != p.Purpose ||
		p.Purpose == "" || strings.ContainsRune(p.Purpose, 0) ||
		utf8.RuneCountInString(p.Purpose) > MaxControlledCommandPurposeRunes {
		return fmt.Errorf("%w: purpose must be normalized and bounded",
			ErrControlledCommandProposalBoundary)
	}
	if _, err := ParseControlledCommandKind(string(p.Kind)); err != nil {
		return err
	}
	if p.Kind == ControlledCommandPowerShellWorkspaceList {
		if validateControlledRelativePath(p.RelativePath) != nil {
			return ErrControlledCommandProposalBoundary
		}
	} else if p.RelativePath != "" {
		return ErrControlledCommandProposalBoundary
	}
	if ControlledCommandProposalFingerprint(p) != p.Fingerprint {
		return fmt.Errorf("%w: proposal fingerprint mismatch",
			ErrControlledCommandProposalBoundary)
	}
	return nil
}

func ControlledCommandProposalFingerprint(
	proposal ControlledCommandProposal,
) string {
	proposal.Fingerprint = ""
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func ControlledCommandProposalRequestFingerprint(
	proposal ControlledCommandProposal,
) string {
	semantic := struct {
		ProtocolVersion          string
		PolicyVersion            string
		RunID                    string
		MissionID                string
		SessionID                string
		WorkspaceID              string
		RootAgentID              string
		InteractionSnapshotID    string
		InteractionRevision      int64
		ExecutionProfileRevision int64
		PermissionSnapshotID     string
		PermissionRevision       int64
		PermissionMode           domain.RunExecutionPermissionMode
		PlanFingerprint          string
		Kind                     ControlledCommandKind
		RelativePath             string
		TimeoutMilliseconds      int64
		Purpose                  string
		RequestedBy              string
	}{
		ProtocolVersion: proposal.ProtocolVersion, PolicyVersion: proposal.PolicyVersion,
		RunID: proposal.RunID, MissionID: proposal.MissionID,
		SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		RootAgentID:              proposal.RootAgentID,
		InteractionSnapshotID:    proposal.InteractionSnapshotID,
		InteractionRevision:      proposal.InteractionRevision,
		ExecutionProfileRevision: proposal.ExecutionProfileRevision,
		PermissionSnapshotID:     proposal.PermissionSnapshotID,
		PermissionRevision:       proposal.PermissionRevision,
		PermissionMode:           proposal.PermissionMode,
		PlanFingerprint:          proposal.PlanFingerprint, Kind: proposal.Kind,
		RelativePath:        proposal.RelativePath,
		TimeoutMilliseconds: proposal.TimeoutMilliseconds,
		Purpose:             proposal.Purpose, RequestedBy: proposal.RequestedBy,
	}
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type ControlledCommandProposalOperation struct {
	KeyDigest          string
	RequestFingerprint string
	InvocationID       string
	ProposalID         string
	RunID              string
	SessionID          string
	WorkspaceID        string
	RootAgentID        string
	LeaseID            string
	LeaseGeneration    int64
	RequestedBy        string
	CreatedAt          time.Time
}

func (o ControlledCommandProposalOperation) Validate() error {
	for _, value := range []string{
		o.InvocationID, o.ProposalID, o.RunID, o.SessionID, o.WorkspaceID,
		o.RootAgentID, o.LeaseID, o.RequestedBy,
	} {
		if !validIdentity(value) {
			return ErrControlledCommandProposalBoundary
		}
	}
	if !validSHA256(o.KeyDigest) || !validSHA256(o.RequestFingerprint) ||
		o.LeaseGeneration <= 0 || o.RequestedBy != "run_supervisor" ||
		o.CreatedAt.IsZero() {
		return ErrControlledCommandProposalBoundary
	}
	return nil
}

type ControlledCommandReviewDecision string

const (
	ControlledCommandReviewApprove ControlledCommandReviewDecision = "approve"
	ControlledCommandReviewDeny    ControlledCommandReviewDecision = "deny"
)

func (d ControlledCommandReviewDecision) Valid() bool {
	return d == ControlledCommandReviewApprove || d == ControlledCommandReviewDeny
}

type ControlledCommandProposalReview struct {
	ID                           string
	ProtocolVersion              string
	PolicyVersion                string
	ProposalID                   string
	ProposalFingerprint          string
	RunID                        string
	MissionID                    string
	SessionID                    string
	WorkspaceID                  string
	Decision                     ControlledCommandReviewDecision
	ReviewedBy                   string
	Reason                       string
	OperationKeyDigest           string
	RequestFingerprint           string
	SingleUseExecutionAuthorized bool
	CapabilityGrant              bool
	CreatedAt                    time.Time
}

func NewControlledCommandProposalReview(
	id string,
	proposal ControlledCommandProposal,
	decision ControlledCommandReviewDecision,
	reviewedBy string,
	reason string,
	operationKeyDigest string,
	at time.Time,
) (ControlledCommandProposalReview, error) {
	if err := proposal.Validate(); err != nil {
		return ControlledCommandProposalReview{}, err
	}
	reviewedBy = strings.TrimSpace(reviewedBy)
	reason = strings.TrimSpace(redact.String(reason))
	if reason == "" {
		if decision == ControlledCommandReviewApprove {
			reason = "operator approved the exact fixed command proposal"
		} else {
			reason = "operator denied the fixed command proposal"
		}
	}
	review := ControlledCommandProposalReview{
		ID:              strings.TrimSpace(id),
		ProtocolVersion: ControlledCommandReviewProtocolVersion,
		PolicyVersion:   ControlledCommandProposalPolicyVersion,
		ProposalID:      proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		RunID: proposal.RunID, MissionID: proposal.MissionID,
		SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		Decision: decision, ReviewedBy: reviewedBy, Reason: reason,
		OperationKeyDigest:           operationKeyDigest,
		SingleUseExecutionAuthorized: decision == ControlledCommandReviewApprove,
		CreatedAt:                    at.UTC(),
	}
	review.RequestFingerprint = ControlledCommandReviewRequestFingerprint(review)
	if err := review.Validate(); err != nil {
		return ControlledCommandProposalReview{}, err
	}
	return review, nil
}

func (r ControlledCommandProposalReview) Validate() error {
	for _, value := range []string{
		r.ID, r.ProposalID, r.RunID, r.MissionID, r.SessionID, r.WorkspaceID,
		r.ReviewedBy,
	} {
		if !validIdentity(value) {
			return ErrControlledCommandProposalBoundary
		}
	}
	if r.ProtocolVersion != ControlledCommandReviewProtocolVersion ||
		r.PolicyVersion != ControlledCommandProposalPolicyVersion ||
		!validSHA256(r.ProposalFingerprint) ||
		!validSHA256(r.OperationKeyDigest) ||
		!validSHA256(r.RequestFingerprint) || !r.Decision.Valid() ||
		!validExecutionOperator(r.ReviewedBy) ||
		r.SingleUseExecutionAuthorized !=
			(r.Decision == ControlledCommandReviewApprove) ||
		r.CapabilityGrant || r.CreatedAt.IsZero() {
		return ErrControlledCommandProposalBoundary
	}
	if !utf8.ValidString(r.Reason) || strings.TrimSpace(r.Reason) != r.Reason ||
		r.Reason == "" || strings.ContainsRune(r.Reason, 0) ||
		utf8.RuneCountInString(r.Reason) >
			MaxControlledCommandReviewReasonRunes {
		return fmt.Errorf("%w: review reason must be normalized and bounded",
			ErrControlledCommandProposalBoundary)
	}
	if ControlledCommandReviewRequestFingerprint(r) != r.RequestFingerprint {
		return fmt.Errorf("%w: review request fingerprint mismatch",
			ErrControlledCommandProposalBoundary)
	}
	return nil
}

func ControlledCommandReviewRequestFingerprint(
	review ControlledCommandProposalReview,
) string {
	semantic := struct {
		ProtocolVersion     string
		PolicyVersion       string
		ProposalID          string
		ProposalFingerprint string
		RunID               string
		Decision            ControlledCommandReviewDecision
		ReviewedBy          string
		Reason              string
	}{
		ProtocolVersion: review.ProtocolVersion, PolicyVersion: review.PolicyVersion,
		ProposalID: review.ProposalID, ProposalFingerprint: review.ProposalFingerprint,
		RunID: review.RunID, Decision: review.Decision,
		ReviewedBy: review.ReviewedBy, Reason: review.Reason,
	}
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type ControlledCommandProposalResultStatus string

const (
	ControlledCommandProposalResultCompleted ControlledCommandProposalResultStatus = "completed"
	ControlledCommandProposalResultFailed    ControlledCommandProposalResultStatus = "failed"
)

func (s ControlledCommandProposalResultStatus) Valid() bool {
	return s == ControlledCommandProposalResultCompleted ||
		s == ControlledCommandProposalResultFailed
}

type ControlledCommandProposalResult struct {
	ID                    string
	ProtocolVersion       string
	PolicyVersion         string
	ProposalID            string
	ProposalFingerprint   string
	ReviewID              string
	RequestID             string
	RunID                 string
	MissionID             string
	SessionID             string
	WorkspaceID           string
	SessionMessageID      int64
	Status                ControlledCommandProposalResultStatus
	SourceKind            string
	SourceRef             string
	ContentSHA256         string
	InstructionAuthorized bool
	RawOutputPersisted    bool
	AutomaticRetryAllowed bool
	CreatedAt             time.Time
}

func NewControlledCommandProposalResult(
	id string,
	proposal ControlledCommandProposal,
	review ControlledCommandProposalReview,
	execution ControlledExecutionResult,
	sessionMessageID int64,
	sourceKind string,
	sourceRef string,
	contentSHA256 string,
	at time.Time,
) (ControlledCommandProposalResult, error) {
	status := ControlledCommandProposalResultCompleted
	if execution.ExitCode != 0 || execution.TimedOut || execution.Cancelled ||
		execution.OutputLimitExceeded {
		status = ControlledCommandProposalResultFailed
	}
	result := ControlledCommandProposalResult{
		ID:              strings.TrimSpace(id),
		ProtocolVersion: ControlledCommandResultProtocolVersion,
		PolicyVersion:   ControlledCommandProposalPolicyVersion,
		ProposalID:      proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		ReviewID: review.ID, RequestID: execution.RequestID,
		RunID: proposal.RunID, MissionID: proposal.MissionID,
		SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		SessionMessageID: sessionMessageID, Status: status,
		SourceKind:    strings.TrimSpace(sourceKind),
		SourceRef:     strings.TrimSpace(sourceRef),
		ContentSHA256: strings.TrimSpace(contentSHA256),
		CreatedAt:     at.UTC(),
	}
	if err := result.Validate(); err != nil {
		return ControlledCommandProposalResult{}, err
	}
	return result, nil
}

func (r ControlledCommandProposalResult) Validate() error {
	for _, value := range []string{
		r.ID, r.ProposalID, r.ReviewID, r.RequestID, r.RunID, r.MissionID,
		r.SessionID, r.WorkspaceID, r.SourceRef,
	} {
		if !validIdentity(value) {
			return ErrControlledCommandProposalBoundary
		}
	}
	if r.ProtocolVersion != ControlledCommandResultProtocolVersion ||
		r.PolicyVersion != ControlledCommandProposalPolicyVersion ||
		!validSHA256(r.ProposalFingerprint) ||
		!validSHA256(r.ContentSHA256) || r.SessionMessageID <= 0 ||
		!r.Status.Valid() || r.SourceKind != "go_command_result" ||
		r.InstructionAuthorized || r.RawOutputPersisted ||
		r.AutomaticRetryAllowed || r.CreatedAt.IsZero() {
		return ErrControlledCommandProposalBoundary
	}
	return nil
}
