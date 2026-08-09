package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
)

const (
	HostCommandProtocolVersion         = "host_command.v1"
	HostCommandPolicyVersion           = "host_command_policy.v1"
	HostCommandProposalProtocolVersion = "host_command_proposal.v1"
	HostCommandReviewProtocolVersion   = "host_command_review.v1"
	HostCommandResultProtocolVersion   = "host_command_proposal_result.v1"
	HostCommandIntentProtocolVersion   = "host_command_execution_intent.v1"
	HostCommandReceiptProtocolVersion  = "host_command_execution_receipt.v1"
	HostEnvironmentPolicy              = "sanitized_host_environment.v1"

	MaxHostCommandArguments          = 64
	MaxHostCommandArgumentBytes      = 16 * 1024
	MaxHostCommandArgumentsBytes     = 64 * 1024
	MaxHostCommandEnvironmentEntries = 48
	MaxHostCommandEnvironmentBytes   = 64 * 1024
	MaxHostCommandPurposeRunes       = 1200
	MaxHostCommandReviewReasonRunes  = 1024
	MaxHostCommandTimeout            = 10 * time.Minute
	MaxHostActiveProcesses           = 32
	MaxHostProcessMemoryBytes        = 2 * 1024 * 1024 * 1024
)

var (
	ErrHostCommandBoundary = errors.New("host command boundary is invalid")
	ErrHostCommandDenied   = errors.New("host command execution is denied")
	ErrHostCommandPlatform = errors.New("host command execution platform is unavailable")
)

type HostNetworkIntent string

const (
	// HostNetworkIntentHost explicitly records that no network sandbox is
	// present. It is intentionally not named "allow", because this contract
	// cannot prove that a command will actually use the network.
	HostNetworkIntentHost HostNetworkIntent = "host"
)

func (n HostNetworkIntent) Validate() error {
	if n != HostNetworkIntentHost {
		return ErrHostCommandBoundary
	}
	return nil
}

// HostCommandSpec is the exact, non-shell transport request reviewed by the
// operator or accepted by the danger-full-access gate. Environment values are
// process-local; only their sorted names and digest are durable.
type HostCommandSpec struct {
	ProtocolVersion     string
	PolicyVersion       string
	ExecutablePath      string
	ExecutableSHA256    string
	Argv                []string
	WorkingDirectory    string
	EnvironmentPolicy   string
	EnvironmentKeys     []string
	EnvironmentSHA256   string
	NetworkIntent       HostNetworkIntent
	TimeoutMilliseconds int64
	Purpose             string
	Fingerprint         string
}

type HostCommandSpecRequest struct {
	ExecutablePath      string
	ExecutableSHA256    string
	Argv                []string
	WorkingDirectory    string
	Environment         []string
	NetworkIntent       HostNetworkIntent
	TimeoutMilliseconds int64
	Purpose             string
}

func NewHostCommandSpec(request HostCommandSpecRequest) (HostCommandSpec, error) {
	executablePath, err := normalizeHostAbsolutePath(request.ExecutablePath)
	if err != nil {
		return HostCommandSpec{}, err
	}
	workingDirectory, err := normalizeHostAbsolutePath(request.WorkingDirectory)
	if err != nil {
		return HostCommandSpec{}, err
	}
	argv, err := normalizeHostArguments(request.Argv)
	if err != nil {
		return HostCommandSpec{}, err
	}
	environment, keys, environmentDigest, err :=
		normalizeHostEnvironment(request.Environment)
	if err != nil {
		return HostCommandSpec{}, err
	}
	_ = environment
	purpose := strings.TrimSpace(redact.String(request.Purpose))
	if purpose == "" || !utf8.ValidString(purpose) ||
		strings.ContainsRune(purpose, 0) ||
		utf8.RuneCountInString(purpose) > MaxHostCommandPurposeRunes {
		return HostCommandSpec{}, fmt.Errorf(
			"%w: purpose must be normalized and bounded", ErrHostCommandBoundary)
	}
	spec := HostCommandSpec{
		ProtocolVersion: HostCommandProtocolVersion,
		PolicyVersion:   HostCommandPolicyVersion,
		ExecutablePath:  executablePath,
		ExecutableSHA256: strings.ToLower(strings.TrimSpace(
			request.ExecutableSHA256)),
		Argv:                argv,
		WorkingDirectory:    workingDirectory,
		EnvironmentPolicy:   HostEnvironmentPolicy,
		EnvironmentKeys:     keys,
		EnvironmentSHA256:   environmentDigest,
		NetworkIntent:       request.NetworkIntent,
		TimeoutMilliseconds: request.TimeoutMilliseconds,
		Purpose:             purpose,
	}
	spec.Fingerprint = HostCommandSpecFingerprint(spec)
	if err := spec.Validate(); err != nil {
		return HostCommandSpec{}, err
	}
	return spec, nil
}

func (s HostCommandSpec) Validate() error {
	if s.ProtocolVersion != HostCommandProtocolVersion ||
		s.PolicyVersion != HostCommandPolicyVersion ||
		s.EnvironmentPolicy != HostEnvironmentPolicy ||
		!validSHA256(s.ExecutableSHA256) ||
		!validSHA256(s.EnvironmentSHA256) ||
		!validSHA256(s.Fingerprint) ||
		s.NetworkIntent.Validate() != nil ||
		s.TimeoutMilliseconds < time.Millisecond.Milliseconds() ||
		s.TimeoutMilliseconds > MaxHostCommandTimeout.Milliseconds() {
		return ErrHostCommandBoundary
	}
	if normalized, err := normalizeHostAbsolutePath(s.ExecutablePath); err != nil || normalized != s.ExecutablePath {
		return ErrHostCommandBoundary
	}
	if normalized, err := normalizeHostAbsolutePath(s.WorkingDirectory); err != nil || normalized != s.WorkingDirectory {
		return ErrHostCommandBoundary
	}
	argv, err := normalizeHostArguments(s.Argv)
	if err != nil || !equalStrings(argv, s.Argv) {
		return ErrHostCommandBoundary
	}
	if err := validateHostEnvironmentKeys(s.EnvironmentKeys); err != nil {
		return err
	}
	if !utf8.ValidString(s.Purpose) ||
		strings.TrimSpace(s.Purpose) != s.Purpose ||
		s.Purpose == "" || strings.ContainsRune(s.Purpose, 0) ||
		utf8.RuneCountInString(s.Purpose) > MaxHostCommandPurposeRunes ||
		redact.String(s.Purpose) != s.Purpose {
		return ErrHostCommandBoundary
	}
	if HostCommandSpecFingerprint(s) != s.Fingerprint {
		return fmt.Errorf("%w: command fingerprint mismatch",
			ErrHostCommandBoundary)
	}
	return nil
}

func HostCommandSpecFingerprint(spec HostCommandSpec) string {
	spec.Fingerprint = ""
	encoded, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// ValidateHostCommandProposalTransport rejects command interpreters and
// inline-code switches that would turn exact argv review back into shell-text
// approval. Danger-full-access execution intentionally uses the broader
// HostCommandSpec contract instead.
func ValidateHostCommandProposalTransport(spec HostCommandSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	base := strings.ToLower(filepath.Base(spec.ExecutablePath))
	switch base {
	case "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe",
		"sh", "sh.exe", "bash", "bash.exe", "dash", "dash.exe", "zsh", "zsh.exe",
		"wscript", "wscript.exe", "cscript", "cscript.exe", "mshta", "mshta.exe",
		"rundll32", "rundll32.exe", "regsvr32", "regsvr32.exe", "env", "env.exe":
		return fmt.Errorf("%w: command interpreters and wrappers are not accepted by approval mode",
			ErrHostCommandDenied)
	}
	inlineSwitches := map[string]map[string]struct{}{
		"python": {"-c": {}}, "python.exe": {"-c": {}},
		"python3": {"-c": {}}, "python3.exe": {"-c": {}},
		"node": {"-e": {}, "--eval": {}}, "node.exe": {"-e": {}, "--eval": {}},
		"deno": {"eval": {}}, "deno.exe": {"eval": {}},
		"bun": {"-e": {}, "--eval": {}}, "bun.exe": {"-e": {}, "--eval": {}},
		"perl": {"-e": {}}, "perl.exe": {"-e": {}},
		"ruby": {"-e": {}}, "ruby.exe": {"-e": {}},
	}
	blocked := inlineSwitches[base]
	for _, argument := range spec.Argv {
		if _, found := blocked[strings.ToLower(strings.TrimSpace(argument))]; found {
			return fmt.Errorf("%w: inline interpreter code is not accepted by approval mode",
				ErrHostCommandDenied)
		}
	}
	return nil
}

func HostEnvironmentDigest(environment []string) (string, error) {
	normalized, _, digest, err := normalizeHostEnvironment(environment)
	if err != nil {
		return "", err
	}
	_ = normalized
	return digest, nil
}

type HostCommandProposalRequest struct {
	ID                       string
	RunID                    string
	MissionID                string
	SessionID                string
	WorkspaceID              string
	RootAgentID              string
	InteractionSnapshotID    string
	InteractionRevision      int64
	ExecutionProfileRevision int64
	Permission               domain.RunExecutionPermissionSnapshot
	Spec                     HostCommandSpec
	RequestedBy              string
	CreatedAt                time.Time
}

// HostCommandProposal is deliberately distinct from
// controlled_command_proposal.v1. It can exist only for approval mode and
// never carries execution authority.
type HostCommandProposal struct {
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
	Spec                     HostCommandSpec
	RequestedBy              string
	InstructionAuthorized    bool
	ExecutionAuthorized      bool
	CapabilityGrant          bool
	Fingerprint              string
	CreatedAt                time.Time
}

func NewHostCommandProposal(
	request HostCommandProposalRequest,
) (HostCommandProposal, error) {
	if err := request.Spec.Validate(); err != nil ||
		request.Permission.Validate() != nil {
		return HostCommandProposal{}, ErrHostCommandBoundary
	}
	request.ID = strings.TrimSpace(request.ID)
	request.RunID = strings.TrimSpace(request.RunID)
	request.MissionID = strings.TrimSpace(request.MissionID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.RootAgentID = strings.TrimSpace(request.RootAgentID)
	request.InteractionSnapshotID =
		strings.TrimSpace(request.InteractionSnapshotID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	if request.Permission.Mode != domain.RunExecutionPermissionApproval ||
		request.Permission.RunID != request.RunID ||
		request.Permission.MissionID != request.MissionID ||
		request.RequestedBy != "run_supervisor" {
		return HostCommandProposal{}, ErrHostCommandBoundary
	}
	proposal := HostCommandProposal{
		ID: request.ID, ProtocolVersion: HostCommandProposalProtocolVersion,
		PolicyVersion: HostCommandPolicyVersion,
		RunID:         request.RunID, MissionID: request.MissionID,
		SessionID: request.SessionID, WorkspaceID: request.WorkspaceID,
		RootAgentID:              request.RootAgentID,
		InteractionSnapshotID:    request.InteractionSnapshotID,
		InteractionRevision:      request.InteractionRevision,
		ExecutionProfileRevision: request.ExecutionProfileRevision,
		PermissionSnapshotID:     request.Permission.ID,
		PermissionRevision:       request.Permission.Revision,
		PermissionMode:           request.Permission.Mode,
		Spec:                     request.Spec,
		RequestedBy:              request.RequestedBy,
		CreatedAt:                request.CreatedAt.UTC(),
	}
	proposal.Fingerprint = HostCommandProposalFingerprint(proposal)
	if err := proposal.Validate(); err != nil {
		return HostCommandProposal{}, err
	}
	return proposal, nil
}

func (p HostCommandProposal) Validate() error {
	for _, value := range []string{
		p.ID, p.RunID, p.MissionID, p.SessionID, p.WorkspaceID, p.RootAgentID,
		p.InteractionSnapshotID, p.PermissionSnapshotID, p.RequestedBy,
	} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if p.ProtocolVersion != HostCommandProposalProtocolVersion ||
		p.PolicyVersion != HostCommandPolicyVersion ||
		p.PermissionMode != domain.RunExecutionPermissionApproval ||
		p.InteractionRevision <= 0 || p.ExecutionProfileRevision <= 0 ||
		p.PermissionRevision <= 0 || p.RequestedBy != "run_supervisor" ||
		p.InstructionAuthorized || p.ExecutionAuthorized || p.CapabilityGrant ||
		!validSHA256(p.Fingerprint) || p.CreatedAt.IsZero() ||
		p.Spec.Validate() != nil {
		return ErrHostCommandBoundary
	}
	if HostCommandProposalFingerprint(p) != p.Fingerprint {
		return fmt.Errorf("%w: proposal fingerprint mismatch",
			ErrHostCommandBoundary)
	}
	return nil
}

func HostCommandProposalFingerprint(proposal HostCommandProposal) string {
	proposal.Fingerprint = ""
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func HostCommandProposalRequestFingerprint(
	proposal HostCommandProposal,
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
		SpecFingerprint          string
		RequestedBy              string
	}{
		ProtocolVersion: proposal.ProtocolVersion,
		PolicyVersion:   proposal.PolicyVersion,
		RunID:           proposal.RunID, MissionID: proposal.MissionID,
		SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		RootAgentID:              proposal.RootAgentID,
		InteractionSnapshotID:    proposal.InteractionSnapshotID,
		InteractionRevision:      proposal.InteractionRevision,
		ExecutionProfileRevision: proposal.ExecutionProfileRevision,
		PermissionSnapshotID:     proposal.PermissionSnapshotID,
		PermissionRevision:       proposal.PermissionRevision,
		PermissionMode:           proposal.PermissionMode,
		SpecFingerprint:          proposal.Spec.Fingerprint,
		RequestedBy:              proposal.RequestedBy,
	}
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type HostCommandProposalOperation struct {
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

func (o HostCommandProposalOperation) Validate() error {
	for _, value := range []string{
		o.InvocationID, o.ProposalID, o.RunID, o.SessionID, o.WorkspaceID,
		o.RootAgentID, o.LeaseID, o.RequestedBy,
	} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if !validSHA256(o.KeyDigest) || !validSHA256(o.RequestFingerprint) ||
		o.LeaseGeneration <= 0 || o.RequestedBy != "run_supervisor" ||
		o.CreatedAt.IsZero() {
		return ErrHostCommandBoundary
	}
	return nil
}

type HostCommandReviewDecision string

const (
	HostCommandReviewApprove HostCommandReviewDecision = "approve"
	HostCommandReviewDeny    HostCommandReviewDecision = "deny"
)

func (d HostCommandReviewDecision) Validate() error {
	if d != HostCommandReviewApprove && d != HostCommandReviewDeny {
		return ErrHostCommandBoundary
	}
	return nil
}

type HostCommandReview struct {
	ID                           string
	ProtocolVersion              string
	PolicyVersion                string
	ProposalID                   string
	ProposalFingerprint          string
	RunID                        string
	Decision                     HostCommandReviewDecision
	ReviewedBy                   string
	Reason                       string
	OperationKeyDigest           string
	RequestFingerprint           string
	SingleUseExecutionAuthorized bool
	CapabilityGrant              bool
	Fingerprint                  string
	CreatedAt                    time.Time
}

func NewHostCommandReview(id string, proposal HostCommandProposal,
	decision HostCommandReviewDecision, reviewedBy string, reason string,
	operationKeyDigest string, createdAt time.Time,
) (HostCommandReview, error) {
	if proposal.Validate() != nil || decision.Validate() != nil {
		return HostCommandReview{}, ErrHostCommandBoundary
	}
	reason = strings.TrimSpace(redact.String(reason))
	if reason == "" {
		if decision == HostCommandReviewApprove {
			reason = "operator approved this exact one-shot host command"
		} else {
			reason = "operator denied this one-shot host command"
		}
	}
	review := HostCommandReview{
		ID:              strings.TrimSpace(id),
		ProtocolVersion: HostCommandReviewProtocolVersion,
		PolicyVersion:   HostCommandPolicyVersion,
		ProposalID:      proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		RunID: proposal.RunID, Decision: decision,
		ReviewedBy: strings.TrimSpace(reviewedBy), Reason: reason,
		OperationKeyDigest:           strings.ToLower(strings.TrimSpace(operationKeyDigest)),
		SingleUseExecutionAuthorized: decision == HostCommandReviewApprove,
		CreatedAt:                    createdAt.UTC(),
	}
	review.RequestFingerprint = HostCommandReviewRequestFingerprint(review)
	review.Fingerprint = HostCommandReviewFingerprint(review)
	if err := review.Validate(); err != nil {
		return HostCommandReview{}, err
	}
	return review, nil
}

func (r HostCommandReview) Validate() error {
	for _, value := range []string{
		r.ID, r.ProposalID, r.RunID, r.ReviewedBy,
	} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if r.ProtocolVersion != HostCommandReviewProtocolVersion ||
		r.PolicyVersion != HostCommandPolicyVersion ||
		r.Decision.Validate() != nil ||
		!validExecutionOperator(r.ReviewedBy) ||
		!validSHA256(r.ProposalFingerprint) ||
		!validSHA256(r.OperationKeyDigest) ||
		!validSHA256(r.RequestFingerprint) ||
		!validSHA256(r.Fingerprint) ||
		r.SingleUseExecutionAuthorized !=
			(r.Decision == HostCommandReviewApprove) ||
		r.CapabilityGrant || r.CreatedAt.IsZero() {
		return ErrHostCommandBoundary
	}
	if !utf8.ValidString(r.Reason) ||
		strings.TrimSpace(r.Reason) != r.Reason || r.Reason == "" ||
		strings.ContainsRune(r.Reason, 0) ||
		utf8.RuneCountInString(r.Reason) > MaxHostCommandReviewReasonRunes ||
		redact.String(r.Reason) != r.Reason {
		return ErrHostCommandBoundary
	}
	if HostCommandReviewFingerprint(r) != r.Fingerprint {
		return fmt.Errorf("%w: review fingerprint mismatch",
			ErrHostCommandBoundary)
	}
	return nil
}

func HostCommandReviewRequestFingerprint(review HostCommandReview) string {
	semantic := struct {
		ProtocolVersion     string
		PolicyVersion       string
		ProposalID          string
		ProposalFingerprint string
		RunID               string
		Decision            HostCommandReviewDecision
		ReviewedBy          string
		Reason              string
		OperationKeyDigest  string
	}{
		ProtocolVersion:     review.ProtocolVersion,
		PolicyVersion:       review.PolicyVersion,
		ProposalID:          review.ProposalID,
		ProposalFingerprint: review.ProposalFingerprint,
		RunID:               review.RunID, Decision: review.Decision,
		ReviewedBy: review.ReviewedBy, Reason: review.Reason,
		OperationKeyDigest: review.OperationKeyDigest,
	}
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func HostCommandReviewFingerprint(review HostCommandReview) string {
	review.Fingerprint = ""
	encoded, err := json.Marshal(review)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type HostCommandProposalResult struct {
	ID                    string
	ProtocolVersion       string
	PolicyVersion         string
	ProposalID            string
	ProposalFingerprint   string
	ReviewID              string
	ReviewFingerprint     string
	RequestID             string
	RunID                 string
	SessionID             string
	Status                string
	SourceKind            string
	SourceRef             string
	ContentSHA256         string
	InstructionAuthorized bool
	RawOutputPersisted    bool
	AutomaticRetryAllowed bool
	Fingerprint           string
	CreatedAt             time.Time
}

func NewHostCommandProposalResult(id string, proposal HostCommandProposal,
	review HostCommandReview, requestID string, status string,
	sourceKind string, sourceRef string, contentSHA256 string,
	createdAt time.Time,
) (HostCommandProposalResult, error) {
	result := HostCommandProposalResult{
		ID: strings.TrimSpace(id), ProtocolVersion: HostCommandResultProtocolVersion,
		PolicyVersion: HostCommandPolicyVersion,
		ProposalID:    proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		ReviewID: review.ID, ReviewFingerprint: review.Fingerprint,
		RequestID: strings.TrimSpace(requestID), RunID: proposal.RunID,
		SessionID: proposal.SessionID, Status: strings.TrimSpace(status),
		SourceKind: strings.TrimSpace(sourceKind), SourceRef: strings.TrimSpace(sourceRef),
		ContentSHA256: strings.ToLower(strings.TrimSpace(contentSHA256)),
		CreatedAt:     createdAt.UTC(),
	}
	result.Fingerprint = HostCommandProposalResultFingerprint(result)
	if proposal.Validate() != nil || review.Validate() != nil ||
		review.ProposalID != proposal.ID ||
		review.Decision != HostCommandReviewApprove || result.Validate() != nil {
		return HostCommandProposalResult{}, ErrHostCommandBoundary
	}
	return result, nil
}

func (r HostCommandProposalResult) Validate() error {
	for _, value := range []string{
		r.ID, r.ProposalID, r.ReviewID, r.RequestID, r.RunID, r.SessionID,
		r.SourceKind, r.SourceRef,
	} {
		if !validIdentity(value) {
			return ErrHostCommandBoundary
		}
	}
	if r.ProtocolVersion != HostCommandResultProtocolVersion ||
		r.PolicyVersion != HostCommandPolicyVersion ||
		!validSHA256(r.ProposalFingerprint) ||
		!validSHA256(r.ReviewFingerprint) ||
		!validSHA256(r.ContentSHA256) || !validSHA256(r.Fingerprint) ||
		(r.Status != "completed" && r.Status != "failed") ||
		r.InstructionAuthorized || r.RawOutputPersisted ||
		r.AutomaticRetryAllowed || r.CreatedAt.IsZero() ||
		HostCommandProposalResultFingerprint(r) != r.Fingerprint {
		return ErrHostCommandBoundary
	}
	return nil
}

func HostCommandProposalResultFingerprint(result HostCommandProposalResult) string {
	result.Fingerprint = ""
	encoded, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func normalizeHostAbsolutePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) ||
		strings.ContainsRune(value, 0) || !filepath.IsAbs(value) {
		return "", ErrHostCommandBoundary
	}
	clean := filepath.Clean(value)
	if clean != value {
		return "", ErrHostCommandBoundary
	}
	return clean, nil
}

func normalizeHostArguments(values []string) ([]string, error) {
	if len(values) > MaxHostCommandArguments {
		return nil, ErrHostCommandBoundary
	}
	result := make([]string, len(values))
	total := 0
	for index, value := range values {
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
			len([]byte(value)) > MaxHostCommandArgumentBytes ||
			redact.String(value) != value {
			return nil, ErrHostCommandBoundary
		}
		total += len([]byte(value))
		if total > MaxHostCommandArgumentsBytes {
			return nil, ErrHostCommandBoundary
		}
		result[index] = value
	}
	return result, nil
}

func normalizeHostEnvironment(values []string) (
	[]string, []string, string, error,
) {
	if len(values) == 0 || len(values) > MaxHostCommandEnvironmentEntries {
		return nil, nil, "", ErrHostCommandBoundary
	}
	result := append([]string(nil), values...)
	sort.Slice(result, func(left int, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	keys := make([]string, 0, len(result))
	seen := make(map[string]struct{}, len(result))
	total := 0
	for _, entry := range result {
		if !utf8.ValidString(entry) || strings.ContainsRune(entry, 0) ||
			redact.String(entry) != entry {
			return nil, nil, "", ErrHostCommandBoundary
		}
		index := strings.IndexByte(entry, '=')
		if index < 1 {
			return nil, nil, "", ErrHostCommandBoundary
		}
		key := entry[:index]
		if !validHostEnvironmentKey(key) {
			return nil, nil, "", ErrHostCommandBoundary
		}
		folded := strings.ToLower(key)
		if _, exists := seen[folded]; exists {
			return nil, nil, "", ErrHostCommandBoundary
		}
		seen[folded] = struct{}{}
		keys = append(keys, key)
		total += len([]byte(entry)) + 1
		if total > MaxHostCommandEnvironmentBytes {
			return nil, nil, "", ErrHostCommandBoundary
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, nil, "", ErrHostCommandBoundary
	}
	digest := sha256.Sum256(encoded)
	return result, keys, hex.EncodeToString(digest[:]), nil
}

func validateHostEnvironmentKeys(keys []string) error {
	if len(keys) == 0 || len(keys) > MaxHostCommandEnvironmentEntries {
		return ErrHostCommandBoundary
	}
	previous := ""
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !validHostEnvironmentKey(key) {
			return ErrHostCommandBoundary
		}
		folded := strings.ToLower(key)
		if _, exists := seen[folded]; exists || previous > folded {
			return ErrHostCommandBoundary
		}
		seen[folded] = struct{}{}
		previous = folded
	}
	return nil
}

func validHostEnvironmentKey(value string) bool {
	if value == "" || len(value) > 128 ||
		!utf8.ValidString(value) || strings.ContainsAny(value, "=\x00") {
		return false
	}
	for index, current := range []byte(value) {
		if (current >= 'A' && current <= 'Z') ||
			(current >= 'a' && current <= 'z') ||
			(index > 0 && current >= '0' && current <= '9') ||
			current == '_' {
			continue
		}
		return false
	}
	lower := strings.ToLower(value)
	for _, fragment := range []string{
		"api_key", "apikey", "auth", "cookie", "credential", "password",
		"passwd", "private_key", "secret", "token",
	} {
		if strings.Contains(lower, fragment) {
			return false
		}
	}
	return true
}
