package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
)

const (
	RiskEscalationProtocolVersion = "risk_escalation.v1"
	RiskEscalationPolicyVersion   = "risk_escalation_policy.v1"

	MaxRiskEscalationItems       = 16
	MaxRiskEscalationTargetRunes = 512
	MaxRiskEscalationReasonRunes = 1200
	MaxRiskEscalationGrantTTL    = 15 * time.Minute
	MaxRiskEscalationGrantUses   = 8
)

type RiskEscalationKind string

const (
	RiskEscalationNetwork            RiskEscalationKind = "network"
	RiskEscalationCredential         RiskEscalationKind = "credential"
	RiskEscalationHostPath           RiskEscalationKind = "host_path"
	RiskEscalationPolicyDenial       RiskEscalationKind = "policy_denial"
	RiskEscalationNonWhitelistedTool RiskEscalationKind = "non_whitelisted_tool"
	RiskEscalationOtherHighRisk      RiskEscalationKind = "other_high_risk"
)

func (k RiskEscalationKind) Validate() error {
	switch k {
	case RiskEscalationNetwork, RiskEscalationCredential,
		RiskEscalationHostPath, RiskEscalationPolicyDenial,
		RiskEscalationNonWhitelistedTool, RiskEscalationOtherHighRisk:
		return nil
	default:
		return ErrHostCommandBoundary
	}
}

// RiskEscalationScope is declarative review metadata. It never contains a
// credential value or a capability bearer. The exact command remains the
// enforceable unit; these fields let the operator review why that command is
// outside Workspace Access without widening any unrelated capability.
type RiskEscalationScope struct {
	ProtocolVersion string
	Kinds           []RiskEscalationKind
	NetworkTargets  []string
	NetworkPurpose  string
	CredentialKinds []string
	HostPaths       []string
	PolicyCode      string
	PolicyReason    string
	RequestedTool   string
	OtherReason     string
	Fingerprint     string
}

type RiskEscalationScopeRequest struct {
	Kinds           []RiskEscalationKind
	NetworkTargets  []string
	NetworkPurpose  string
	CredentialKinds []string
	HostPaths       []string
	PolicyCode      string
	PolicyReason    string
	RequestedTool   string
	OtherReason     string
}

func NewRiskEscalationScope(request RiskEscalationScopeRequest) (
	RiskEscalationScope, error,
) {
	kinds, err := normalizeRiskKinds(request.Kinds)
	if err != nil {
		return RiskEscalationScope{}, err
	}
	targets, err := normalizeRiskLabels(request.NetworkTargets, false)
	if err != nil {
		return RiskEscalationScope{}, err
	}
	credentials, err := normalizeRiskLabels(request.CredentialKinds, true)
	if err != nil {
		return RiskEscalationScope{}, err
	}
	hostPaths, err := normalizeRiskPaths(request.HostPaths)
	if err != nil {
		return RiskEscalationScope{}, err
	}
	scope := RiskEscalationScope{
		ProtocolVersion: RiskEscalationProtocolVersion,
		Kinds:           kinds, NetworkTargets: targets,
		NetworkPurpose:  normalizeRiskText(request.NetworkPurpose),
		CredentialKinds: credentials, HostPaths: hostPaths,
		PolicyCode:    normalizeRiskIdentity(request.PolicyCode),
		PolicyReason:  normalizeRiskText(request.PolicyReason),
		RequestedTool: normalizeRiskIdentity(request.RequestedTool),
		OtherReason:   normalizeRiskText(request.OtherReason),
	}
	scope.Fingerprint = RiskEscalationScopeFingerprint(scope)
	if err := scope.Validate(); err != nil {
		return RiskEscalationScope{}, err
	}
	return scope, nil
}

func (s RiskEscalationScope) Validate() error {
	if s.ProtocolVersion != RiskEscalationProtocolVersion ||
		len(s.Kinds) == 0 || len(s.Kinds) > MaxRiskEscalationItems ||
		!validSHA256(s.Fingerprint) ||
		RiskEscalationScopeFingerprint(s) != s.Fingerprint {
		return ErrHostCommandBoundary
	}
	if normalized, err := normalizeRiskKinds(s.Kinds); err != nil ||
		!equalRiskKinds(normalized, s.Kinds) {
		return ErrHostCommandBoundary
	}
	if normalized, err := normalizeRiskLabels(s.NetworkTargets, false); err != nil ||
		!equalStrings(normalized, s.NetworkTargets) {
		return ErrHostCommandBoundary
	}
	if normalized, err := normalizeRiskLabels(s.CredentialKinds, true); err != nil ||
		!equalStrings(normalized, s.CredentialKinds) {
		return ErrHostCommandBoundary
	}
	if normalized, err := normalizeRiskPaths(s.HostPaths); err != nil ||
		!equalStrings(normalized, s.HostPaths) {
		return ErrHostCommandBoundary
	}
	for _, value := range []string{s.NetworkPurpose, s.PolicyReason, s.OtherReason} {
		if value != normalizeRiskText(value) || utf8.RuneCountInString(value) > MaxRiskEscalationReasonRunes {
			return ErrHostCommandBoundary
		}
	}
	for _, value := range []string{s.PolicyCode, s.RequestedTool} {
		if value != normalizeRiskIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	kindSet := make(map[RiskEscalationKind]bool, len(s.Kinds))
	for _, kind := range s.Kinds {
		kindSet[kind] = true
	}
	if kindSet[RiskEscalationNetwork] != (len(s.NetworkTargets) > 0 && s.NetworkPurpose != "") ||
		kindSet[RiskEscalationCredential] != (len(s.CredentialKinds) > 0) ||
		kindSet[RiskEscalationHostPath] != (len(s.HostPaths) > 0) ||
		kindSet[RiskEscalationPolicyDenial] != (s.PolicyCode != "" && s.PolicyReason != "") ||
		kindSet[RiskEscalationNonWhitelistedTool] != (s.RequestedTool != "") ||
		kindSet[RiskEscalationOtherHighRisk] != (s.OtherReason != "") {
		return ErrHostCommandBoundary
	}
	return nil
}

func RiskEscalationScopeFingerprint(scope RiskEscalationScope) string {
	scope.Fingerprint = ""
	encoded, err := json.Marshal(scope)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type RiskEscalationResourceBudget struct {
	TimeoutMilliseconds int64
	MaxOutputBytes      int64
	ActiveProcessLimit  int
	ProcessMemoryBytes  int64
}

func NewRiskEscalationResourceBudget(spec HostCommandSpec) RiskEscalationResourceBudget {
	return RiskEscalationResourceBudget{
		TimeoutMilliseconds: spec.TimeoutMilliseconds,
		MaxOutputBytes:      MaxControlledOutputObservedBytes,
		ActiveProcessLimit:  MaxHostActiveProcesses,
		ProcessMemoryBytes:  MaxHostProcessMemoryBytes,
	}
}

func (b RiskEscalationResourceBudget) Validate(spec HostCommandSpec) error {
	if b.TimeoutMilliseconds != spec.TimeoutMilliseconds ||
		b.MaxOutputBytes != MaxControlledOutputObservedBytes ||
		b.ActiveProcessLimit != MaxHostActiveProcesses ||
		b.ProcessMemoryBytes != MaxHostProcessMemoryBytes {
		return ErrHostCommandBoundary
	}
	return nil
}

type RiskEscalationProposalRequest struct {
	ID                         string
	RunID                      string
	MissionID                  string
	SessionID                  string
	WorkspaceID                string
	RootAgentID                string
	SupervisorTurn             int
	SupervisorToolCallID       string
	ToolInvocationID           string
	ModeSnapshotID             string
	ModeRevision               int64
	InteractionSnapshotID      string
	InteractionRevision        int64
	ExecutionProfileSnapshotID string
	ExecutionProfileRevision   int64
	Permission                 domain.RunExecutionPermissionSnapshot
	WorkspaceRootFingerprint   string
	CapabilityGeneration       string
	Spec                       HostCommandSpec
	Scope                      RiskEscalationScope
	RequestedBy                string
	CreatedAt                  time.Time
}

type RiskEscalationProposal struct {
	ID                         string
	ProtocolVersion            string
	PolicyVersion              string
	RunID                      string
	MissionID                  string
	SessionID                  string
	WorkspaceID                string
	RootAgentID                string
	SupervisorTurn             int
	SupervisorToolCallID       string
	ToolInvocationID           string
	ModeSnapshotID             string
	ModeRevision               int64
	InteractionSnapshotID      string
	InteractionRevision        int64
	ExecutionProfileSnapshotID string
	ExecutionProfileRevision   int64
	PermissionSnapshotID       string
	PermissionRevision         int64
	PermissionMode             domain.RunExecutionPermissionMode
	WorkspaceRootFingerprint   string
	CapabilityGeneration       string
	Spec                       HostCommandSpec
	Scope                      RiskEscalationScope
	ResourceBudget             RiskEscalationResourceBudget
	RequestedBy                string
	InstructionAuthorized      bool
	ExecutionAuthorized        bool
	CapabilityBearer           bool
	Fingerprint                string
	CreatedAt                  time.Time
}

func NewRiskEscalationProposal(request RiskEscalationProposalRequest) (
	RiskEscalationProposal, error,
) {
	proposal := RiskEscalationProposal{
		ID:              strings.TrimSpace(request.ID),
		ProtocolVersion: RiskEscalationProtocolVersion,
		PolicyVersion:   RiskEscalationPolicyVersion,
		RunID:           strings.TrimSpace(request.RunID), MissionID: strings.TrimSpace(request.MissionID),
		SessionID: strings.TrimSpace(request.SessionID), WorkspaceID: strings.TrimSpace(request.WorkspaceID),
		RootAgentID: strings.TrimSpace(request.RootAgentID), SupervisorTurn: request.SupervisorTurn,
		SupervisorToolCallID: strings.TrimSpace(request.SupervisorToolCallID),
		ToolInvocationID:     strings.TrimSpace(request.ToolInvocationID),
		ModeSnapshotID:       strings.TrimSpace(request.ModeSnapshotID), ModeRevision: request.ModeRevision,
		InteractionSnapshotID:      strings.TrimSpace(request.InteractionSnapshotID),
		InteractionRevision:        request.InteractionRevision,
		ExecutionProfileSnapshotID: strings.TrimSpace(request.ExecutionProfileSnapshotID),
		ExecutionProfileRevision:   request.ExecutionProfileRevision,
		PermissionSnapshotID:       request.Permission.ID,
		PermissionRevision:         request.Permission.Revision,
		PermissionMode:             request.Permission.Mode,
		WorkspaceRootFingerprint:   strings.ToLower(strings.TrimSpace(request.WorkspaceRootFingerprint)),
		CapabilityGeneration:       strings.ToLower(strings.TrimSpace(request.CapabilityGeneration)),
		Spec:                       request.Spec, Scope: request.Scope,
		ResourceBudget: NewRiskEscalationResourceBudget(request.Spec),
		RequestedBy:    strings.TrimSpace(request.RequestedBy), CreatedAt: request.CreatedAt.UTC(),
	}
	proposal.Fingerprint = RiskEscalationProposalFingerprint(proposal)
	if request.Permission.Validate() != nil || request.Permission.RunID != proposal.RunID ||
		request.Permission.MissionID != proposal.MissionID || proposal.Validate() != nil {
		return RiskEscalationProposal{}, ErrHostCommandBoundary
	}
	return proposal, nil
}

func (p RiskEscalationProposal) Validate() error {
	for _, value := range []string{p.ID, p.RunID, p.MissionID, p.SessionID,
		p.WorkspaceID, p.RootAgentID, p.SupervisorToolCallID, p.ToolInvocationID,
		p.ModeSnapshotID, p.InteractionSnapshotID, p.ExecutionProfileSnapshotID,
		p.PermissionSnapshotID, p.RequestedBy} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if p.ProtocolVersion != RiskEscalationProtocolVersion ||
		p.PolicyVersion != RiskEscalationPolicyVersion ||
		p.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
		p.SupervisorTurn <= 0 || p.ModeRevision <= 0 || p.InteractionRevision <= 0 ||
		p.ExecutionProfileRevision <= 0 || p.PermissionRevision <= 0 ||
		!validSHA256(p.WorkspaceRootFingerprint) || !validSHA256(p.CapabilityGeneration) ||
		p.Spec.Validate() != nil || p.Scope.Validate() != nil ||
		p.ResourceBudget.Validate(p.Spec) != nil || p.RequestedBy != "run_supervisor" ||
		p.InstructionAuthorized || p.ExecutionAuthorized || p.CapabilityBearer ||
		!validSHA256(p.Fingerprint) || p.CreatedAt.IsZero() ||
		RiskEscalationProposalFingerprint(p) != p.Fingerprint {
		return ErrHostCommandBoundary
	}
	return nil
}

func RiskEscalationProposalFingerprint(proposal RiskEscalationProposal) string {
	proposal.Fingerprint = ""
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func RiskEscalationProposalRequestFingerprint(proposal RiskEscalationProposal) string {
	// ToolInvocationID identifies one gateway attempt, not the durable semantic
	// call. Resuming the same Supervisor call allocates a fresh attempt identity;
	// the immutable proposal still retains the original invocation for audit.
	semantic := struct {
		ProtocolVersion            string
		PolicyVersion              string
		RunID                      string
		MissionID                  string
		SessionID                  string
		WorkspaceID                string
		RootAgentID                string
		SupervisorTurn             int
		SupervisorToolCallID       string
		ModeSnapshotID             string
		ModeRevision               int64
		InteractionSnapshotID      string
		InteractionRevision        int64
		ExecutionProfileSnapshotID string
		ExecutionProfileRevision   int64
		PermissionSnapshotID       string
		PermissionRevision         int64
		PermissionMode             domain.RunExecutionPermissionMode
		WorkspaceRootFingerprint   string
		CapabilityGeneration       string
		SpecFingerprint            string
		ScopeFingerprint           string
		ResourceBudget             RiskEscalationResourceBudget
		RequestedBy                string
	}{
		ProtocolVersion: proposal.ProtocolVersion, PolicyVersion: proposal.PolicyVersion,
		RunID: proposal.RunID, MissionID: proposal.MissionID, SessionID: proposal.SessionID,
		WorkspaceID: proposal.WorkspaceID, RootAgentID: proposal.RootAgentID,
		SupervisorTurn:       proposal.SupervisorTurn,
		SupervisorToolCallID: proposal.SupervisorToolCallID,
		ModeSnapshotID:       proposal.ModeSnapshotID, ModeRevision: proposal.ModeRevision,
		InteractionSnapshotID:      proposal.InteractionSnapshotID,
		InteractionRevision:        proposal.InteractionRevision,
		ExecutionProfileSnapshotID: proposal.ExecutionProfileSnapshotID,
		ExecutionProfileRevision:   proposal.ExecutionProfileRevision,
		PermissionSnapshotID:       proposal.PermissionSnapshotID,
		PermissionRevision:         proposal.PermissionRevision, PermissionMode: proposal.PermissionMode,
		WorkspaceRootFingerprint: proposal.WorkspaceRootFingerprint,
		CapabilityGeneration:     proposal.CapabilityGeneration,
		SpecFingerprint:          proposal.Spec.Fingerprint, ScopeFingerprint: proposal.Scope.Fingerprint,
		ResourceBudget: proposal.ResourceBudget, RequestedBy: proposal.RequestedBy,
	}
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type RiskEscalationOperation struct {
	KeyDigest            string
	RequestFingerprint   string
	InvocationID         string
	ProposalID           string
	RunID                string
	SessionID            string
	WorkspaceID          string
	RootAgentID          string
	SupervisorTurn       int
	SupervisorToolCallID string
	LeaseID              string
	LeaseGeneration      int64
	RequestedBy          string
	CreatedAt            time.Time
}

func (o RiskEscalationOperation) Validate() error {
	for _, value := range []string{o.InvocationID, o.ProposalID, o.RunID,
		o.SessionID, o.WorkspaceID, o.RootAgentID, o.SupervisorToolCallID,
		o.LeaseID, o.RequestedBy} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if !validSHA256(o.KeyDigest) || !validSHA256(o.RequestFingerprint) ||
		o.SupervisorTurn <= 0 || o.LeaseGeneration <= 0 ||
		o.RequestedBy != "run_supervisor" || o.CreatedAt.IsZero() {
		return ErrHostCommandBoundary
	}
	return nil
}

type RiskEscalationAuthorization struct {
	ProtocolVersion     string
	ProposalID          string
	ProposalFingerprint string
	ApprovalID          string
	ApprovalVersion     int64
	ApprovalFingerprint string
	GrantID             string
	GrantGeneration     int64
	GrantConsumptionID  string
	ScopeFingerprint    string
	ReviewedBy          string
	AuthorizedAt        time.Time
}

func NewRiskEscalationAuthorization(proposal RiskEscalationProposal,
	approvalID string, approvalVersion int64, approvalFingerprint string,
	grantID string, grantGeneration int64, grantConsumptionID string,
	reviewedBy string, authorizedAt time.Time,
) (RiskEscalationAuthorization, error) {
	authorization := RiskEscalationAuthorization{
		ProtocolVersion: RiskEscalationProtocolVersion,
		ProposalID:      proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		ApprovalID: strings.TrimSpace(approvalID), ApprovalVersion: approvalVersion,
		ApprovalFingerprint: strings.ToLower(strings.TrimSpace(approvalFingerprint)),
		GrantID:             strings.TrimSpace(grantID), GrantGeneration: grantGeneration,
		GrantConsumptionID: strings.TrimSpace(grantConsumptionID),
		ScopeFingerprint:   proposal.Scope.Fingerprint,
		ReviewedBy:         strings.TrimSpace(reviewedBy), AuthorizedAt: authorizedAt.UTC(),
	}
	if proposal.Validate() != nil || authorization.Validate() != nil {
		return RiskEscalationAuthorization{}, ErrHostCommandBoundary
	}
	return authorization, nil
}

func (a RiskEscalationAuthorization) Validate() error {
	for _, value := range []string{a.ProposalID, a.ApprovalID, a.ReviewedBy} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if a.ProtocolVersion != RiskEscalationProtocolVersion ||
		a.ApprovalVersion <= 0 || !validSHA256(a.ProposalFingerprint) ||
		!validSHA256(a.ApprovalFingerprint) || !validSHA256(a.ScopeFingerprint) ||
		!validExecutionOperator(a.ReviewedBy) || a.AuthorizedAt.IsZero() {
		return ErrHostCommandBoundary
	}
	if (a.GrantID == "") != (a.GrantGeneration == 0) ||
		(a.GrantID == "") != (a.GrantConsumptionID == "") {
		return ErrHostCommandBoundary
	}
	if a.GrantID != "" && (!validIdentity(a.GrantID) ||
		!validIdentity(a.GrantConsumptionID) || a.GrantGeneration <= 0) {
		return ErrHostCommandBoundary
	}
	return nil
}

func RiskEscalationAuthorizationFingerprint(value RiskEscalationAuthorization) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type RiskEscalationResult struct {
	ID                    string
	ProtocolVersion       string
	ProposalID            string
	ProposalFingerprint   string
	ApprovalID            string
	ApprovalFingerprint   string
	GrantID               string
	GrantConsumptionID    string
	RequestID             string
	RunID                 string
	SessionID             string
	Status                string
	ErrorCode             string
	SourceKind            string
	SourceRef             string
	ContentSHA256         string
	InstructionAuthorized bool
	RawOutputPersisted    bool
	AutomaticRetryAllowed bool
	Uncertain             bool
	Fingerprint           string
	CreatedAt             time.Time
}

type RiskEscalationInvalidation struct {
	ID          string
	ProposalID  string
	GrantID     string
	ReasonCode  string
	Detail      string
	Fingerprint string
	CreatedAt   time.Time
}

func NewRiskEscalationInvalidation(id string, proposalID string, grantID string,
	reasonCode string, detail string, createdAt time.Time,
) (RiskEscalationInvalidation, error) {
	value := RiskEscalationInvalidation{ID: strings.TrimSpace(id),
		ProposalID: strings.TrimSpace(proposalID), GrantID: strings.TrimSpace(grantID),
		ReasonCode: strings.TrimSpace(reasonCode), Detail: normalizeRiskText(detail),
		CreatedAt: createdAt.UTC()}
	value.Fingerprint = RiskEscalationInvalidationFingerprint(value)
	if value.Validate() != nil {
		return RiskEscalationInvalidation{}, ErrHostCommandBoundary
	}
	return value, nil
}

func (i RiskEscalationInvalidation) Validate() error {
	if !validIdentity(i.ID) || !validIdentity(i.ProposalID) ||
		(i.GrantID != "" && !validIdentity(i.GrantID)) || i.Detail == "" ||
		i.Detail != normalizeRiskText(i.Detail) || !validSHA256(i.Fingerprint) ||
		i.CreatedAt.IsZero() || RiskEscalationInvalidationFingerprint(i) != i.Fingerprint {
		return ErrHostCommandBoundary
	}
	switch i.ReasonCode {
	case "expired", "revoked", "permission_drift", "profile_drift", "mode_drift",
		"workspace_drift", "root_drift", "capability_drift", "uses_exhausted",
		"execution_uncertain":
		return nil
	default:
		return ErrHostCommandBoundary
	}
}

func RiskEscalationInvalidationFingerprint(value RiskEscalationInvalidation) string {
	value.Fingerprint = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func NewRiskEscalationResult(id string, proposal RiskEscalationProposal,
	authorization RiskEscalationAuthorization, requestID string, status string,
	errorCode string, sourceKind string, sourceRef string, contentSHA256 string,
	uncertain bool, createdAt time.Time,
) (RiskEscalationResult, error) {
	result := RiskEscalationResult{
		ID: strings.TrimSpace(id), ProtocolVersion: RiskEscalationProtocolVersion,
		ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		ApprovalID:          authorization.ApprovalID,
		ApprovalFingerprint: authorization.ApprovalFingerprint,
		GrantID:             authorization.GrantID,
		GrantConsumptionID:  authorization.GrantConsumptionID,
		RequestID:           strings.TrimSpace(requestID), RunID: proposal.RunID,
		SessionID: proposal.SessionID, Status: strings.TrimSpace(status),
		ErrorCode: strings.TrimSpace(errorCode), SourceKind: strings.TrimSpace(sourceKind),
		SourceRef:     strings.TrimSpace(sourceRef),
		ContentSHA256: strings.ToLower(strings.TrimSpace(contentSHA256)),
		Uncertain:     uncertain, CreatedAt: createdAt.UTC(),
	}
	result.Fingerprint = RiskEscalationResultFingerprint(result)
	if proposal.Validate() != nil || authorization.Validate() != nil ||
		authorization.ProposalID != proposal.ID || result.Validate() != nil {
		return RiskEscalationResult{}, ErrHostCommandBoundary
	}
	return result, nil
}

func (r RiskEscalationResult) Validate() error {
	for _, value := range []string{r.ID, r.ProposalID, r.ApprovalID, r.RequestID,
		r.RunID, r.SessionID, r.SourceKind, r.SourceRef} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if r.ProtocolVersion != RiskEscalationProtocolVersion ||
		!validSHA256(r.ProposalFingerprint) || !validSHA256(r.ApprovalFingerprint) ||
		!validSHA256(r.ContentSHA256) || !validSHA256(r.Fingerprint) ||
		(r.Status != "completed" && r.Status != "failed") ||
		(r.Status == "completed" && r.ErrorCode != "") ||
		(r.Status == "failed" && r.ErrorCode == "") ||
		r.InstructionAuthorized || r.RawOutputPersisted || r.AutomaticRetryAllowed ||
		r.CreatedAt.IsZero() || RiskEscalationResultFingerprint(r) != r.Fingerprint {
		return ErrHostCommandBoundary
	}
	if (r.GrantID == "") != (r.GrantConsumptionID == "") ||
		(r.GrantID != "" && (!validIdentity(r.GrantID) ||
			!validIdentity(r.GrantConsumptionID))) {
		return ErrHostCommandBoundary
	}
	if r.Uncertain && r.Status != "failed" {
		return ErrHostCommandBoundary
	}
	return nil
}

func RiskEscalationResultFingerprint(result RiskEscalationResult) string {
	result.Fingerprint = ""
	encoded, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func normalizeRiskKinds(values []RiskEscalationKind) ([]RiskEscalationKind, error) {
	if len(values) == 0 || len(values) > MaxRiskEscalationItems {
		return nil, ErrHostCommandBoundary
	}
	result := append([]RiskEscalationKind(nil), values...)
	for _, value := range result {
		if value.Validate() != nil {
			return nil, ErrHostCommandBoundary
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, ErrHostCommandBoundary
		}
	}
	return result, nil
}

func normalizeRiskLabels(values []string, identifiers bool) ([]string, error) {
	if len(values) > MaxRiskEscalationItems {
		return nil, ErrHostCommandBoundary
	}
	result := make([]string, len(values))
	for index, value := range values {
		value = strings.TrimSpace(value)
		if redact.String(value) != value {
			return nil, ErrHostCommandBoundary
		}
		if identifiers {
			value = strings.ToLower(value)
		}
		if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
			utf8.RuneCountInString(value) > MaxRiskEscalationTargetRunes ||
			redact.String(value) != value {
			return nil, ErrHostCommandBoundary
		}
		for _, current := range value {
			if unicode.IsControl(current) || (identifiers && !(unicode.IsLetter(current) ||
				unicode.IsDigit(current) || strings.ContainsRune("._-:/", current))) {
				return nil, ErrHostCommandBoundary
			}
		}
		result[index] = value
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, ErrHostCommandBoundary
		}
	}
	return result, nil
}

func normalizeRiskPaths(values []string) ([]string, error) {
	if len(values) > MaxRiskEscalationItems {
		return nil, ErrHostCommandBoundary
	}
	result := make([]string, len(values))
	for index, value := range values {
		value = strings.TrimSpace(value)
		if !filepath.IsAbs(value) || !utf8.ValidString(value) ||
			strings.ContainsRune(value, 0) || redact.String(value) != value {
			return nil, ErrHostCommandBoundary
		}
		clean := filepath.Clean(value)
		if clean != value || utf8.RuneCountInString(clean) > MaxRiskEscalationTargetRunes {
			return nil, ErrHostCommandBoundary
		}
		result[index] = clean
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, ErrHostCommandBoundary
		}
	}
	return result, nil
}

func normalizeRiskText(value string) string {
	value = strings.TrimSpace(value)
	if redact.String(value) != value {
		return ""
	}
	if value == "" {
		return ""
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
		utf8.RuneCountInString(value) > MaxRiskEscalationReasonRunes ||
		redact.String(value) != value {
		return ""
	}
	for _, current := range value {
		if unicode.IsControl(current) && current != '\t' {
			return ""
		}
	}
	return value
}

func normalizeRiskIdentity(value string) string {
	values, err := normalizeRiskLabels([]string{value}, true)
	if value == "" {
		return ""
	}
	if err != nil || len(values) != 1 {
		return ""
	}
	return values[0]
}

func equalRiskKinds(left, right []RiskEscalationKind) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var errRiskEscalationUncertain = errors.New("risk escalation execution result is uncertain")

func IsRiskEscalationUncertain(err error) bool {
	return errors.Is(err, errRiskEscalationUncertain)
}
