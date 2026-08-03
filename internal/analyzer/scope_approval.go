package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	AnalyzerScopeLimitsApprovalProtocolVersion  = "analyzer_scope_limits_approval.v1"
	AnalyzerScopeLimitsApprovalConfirmation     = "APPROVE-ANALYZER-EXACT-SCOPE-AND-LIMITS"
	MaxAnalyzerScopeLimitsApprovalEnvelopeBytes = 12 * 1024
)

// AnalyzerScopeLimitsApproval records one operator's exact acknowledgement of
// a request scope, signed release, resource plan, and sandbox requirements. It
// is neither an execution capability nor an authenticated durable grant.
type AnalyzerScopeLimitsApproval struct {
	ProtocolVersion              string               `json:"protocol_version"`
	RequestID                    string               `json:"request_id"`
	Analyzer                     string               `json:"analyzer"`
	TargetGOOS                   string               `json:"target_goos"`
	TargetGOARCH                 string               `json:"target_goarch"`
	ExecutableSHA256             string               `json:"executable_sha256"`
	CandidateSHA256              string               `json:"candidate_sha256"`
	ReleaseCandidateSHA256       string               `json:"release_candidate_sha256"`
	ProvenanceVerificationSHA256 string               `json:"provenance_verification_sha256"`
	LaunchPlanSHA256             string               `json:"launch_plan_sha256"`
	LaunchPlanReviewSHA256       string               `json:"launch_plan_review_sha256"`
	ResourcePlanSHA256           string               `json:"resource_plan_sha256"`
	SandboxPlanSHA256            string               `json:"sandbox_plan_sha256"`
	Resources                    AnalyzerResourcePlan `json:"resources"`
	Sandbox                      AnalyzerSandboxPlan  `json:"sandbox"`
	OperatorIdentitySHA256       string               `json:"operator_identity_sha256"`
	ConfirmationSHA256           string               `json:"confirmation_sha256"`
	Decision                     string               `json:"decision"`
	RequestScopeBound            bool                 `json:"request_scope_bound"`
	ExecutableScopeBound         bool                 `json:"executable_scope_bound"`
	ReleaseScopeBound            bool                 `json:"release_scope_bound"`
	ProvenanceScopeBound         bool                 `json:"provenance_scope_bound"`
	ResourceLimitsBound          bool                 `json:"resource_limits_bound"`
	SandboxRequirementsBound     bool                 `json:"sandbox_requirements_bound"`
	DesignReviewBound            bool                 `json:"design_review_bound"`
	OperatorScopeLimitsApproved  bool                 `json:"operator_scope_limits_approved"`
	ApprovalAuthenticated        bool                 `json:"approval_authenticated"`
	DurableGrant                 bool                 `json:"durable_grant"`
	CapabilityGrantIssued        bool                 `json:"capability_grant_issued"`
	ExecutionAuthorized          bool                 `json:"execution_authorized"`
	ProcessStartAuthorized       bool                 `json:"process_start_authorized"`
	ProductInvocationAuthorized  bool                 `json:"product_invocation_authorized"`
	NetworkAuthorized            bool                 `json:"network_authorized"`
	HostFilesystemAuthorized     bool                 `json:"host_filesystem_authorized"`
	ResultPersistenceAuthorized  bool                 `json:"result_persistence_authorized"`
	ArtifactCommitAuthorized     bool                 `json:"artifact_commit_authorized"`
	OperatorOverrideAllowed      bool                 `json:"operator_override_allowed"`
}

func BuildAnalyzerScopeLimitsApproval(candidate InvocationCandidate, rawRequest,
	executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
	rawStatement, publicKey, detachedSignature []byte,
	verification AnalyzerProvenanceVerification, plan AnalyzerLaunchPlan,
	review AnalyzerLaunchPlanReview, operatorIdentitySHA256, confirmation string,
) (AnalyzerScopeLimitsApproval, ErrorCode) {
	if code := ValidateAnalyzerProvenanceVerification(verification, candidate, rawRequest,
		executable, identity, preflight, evidence, manifest, allowlist, release, rawStatement,
		publicKey, detachedSignature); code != "" {
		return AnalyzerScopeLimitsApproval{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerLaunchPlan(plan, candidate, rawRequest, executable, identity,
		preflight, evidence, manifest, allowlist, release); code != "" {
		return AnalyzerScopeLimitsApproval{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerLaunchPlanReview(review, plan, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist, release,
		review.ReviewerIdentitySHA256); code != "" {
		return AnalyzerScopeLimitsApproval{}, CodeInvalidResult
	}
	if !validDigest(operatorIdentitySHA256) ||
		operatorIdentitySHA256 != review.ReviewerIdentitySHA256 ||
		confirmation != AnalyzerScopeLimitsApprovalConfirmation {
		return AnalyzerScopeLimitsApproval{}, CodeInvalidContent
	}
	candidateDigest, candidateOK := invocationCandidateSHA256(candidate)
	releaseDigest, releaseOK := canonicalSHA256(release)
	verificationDigest, verificationOK := canonicalSHA256(verification)
	planDigest, planOK := canonicalSHA256(plan)
	reviewDigest, reviewOK := canonicalSHA256(review)
	resourceDigest, resourceOK := canonicalSHA256(plan.Resources)
	sandboxDigest, sandboxOK := canonicalSHA256(plan.Sandbox)
	if !candidateOK || !releaseOK || !verificationOK || !planOK || !reviewOK ||
		!resourceOK || !sandboxOK {
		return AnalyzerScopeLimitsApproval{}, CodeInternal
	}
	confirmationDigest := sha256.Sum256([]byte(confirmation))
	approval := AnalyzerScopeLimitsApproval{
		ProtocolVersion: AnalyzerScopeLimitsApprovalProtocolVersion,
		RequestID:       candidate.RequestID, Analyzer: candidate.Analyzer,
		TargetGOOS: evidence.TargetGOOS, TargetGOARCH: evidence.TargetGOARCH,
		ExecutableSHA256: evidence.ExecutableSHA256, CandidateSHA256: candidateDigest,
		ReleaseCandidateSHA256:       releaseDigest,
		ProvenanceVerificationSHA256: verificationDigest, LaunchPlanSHA256: planDigest,
		LaunchPlanReviewSHA256: reviewDigest, ResourcePlanSHA256: resourceDigest,
		SandboxPlanSHA256: sandboxDigest, Resources: plan.Resources, Sandbox: plan.Sandbox,
		OperatorIdentitySHA256: operatorIdentitySHA256,
		ConfirmationSHA256:     hex.EncodeToString(confirmationDigest[:]),
		Decision:               "approved_exact_scope_and_limits_candidate", RequestScopeBound: true,
		ExecutableScopeBound: true, ReleaseScopeBound: true, ProvenanceScopeBound: true,
		ResourceLimitsBound: true, SandboxRequirementsBound: true, DesignReviewBound: true,
		OperatorScopeLimitsApproved: true,
	}
	if !validateAnalyzerScopeLimitsApprovalStructure(approval, candidate, evidence, release,
		verification, plan, review, operatorIdentitySHA256) {
		return AnalyzerScopeLimitsApproval{}, CodeInternal
	}
	return approval, ""
}

func ValidateAnalyzerScopeLimitsApproval(approval AnalyzerScopeLimitsApproval,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate, rawStatement, publicKey, detachedSignature []byte,
	verification AnalyzerProvenanceVerification, plan AnalyzerLaunchPlan,
	review AnalyzerLaunchPlanReview, operatorIdentitySHA256 string,
) ErrorCode {
	expected, code := BuildAnalyzerScopeLimitsApproval(candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist, release, rawStatement, publicKey,
		detachedSignature, verification, plan, review, operatorIdentitySHA256,
		AnalyzerScopeLimitsApprovalConfirmation)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(approval, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerScopeLimitsApproval(approval AnalyzerScopeLimitsApproval,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate, rawStatement, publicKey, detachedSignature []byte,
	verification AnalyzerProvenanceVerification, plan AnalyzerLaunchPlan,
	review AnalyzerLaunchPlanReview, operatorIdentitySHA256 string,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerScopeLimitsApproval(approval, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist, release, rawStatement, publicKey,
		detachedSignature, verification, plan, review, operatorIdentitySHA256); code != "" {
		return nil, code
	}
	return encodeAnalyzerScopeApprovalValue(approval,
		MaxAnalyzerScopeLimitsApprovalEnvelopeBytes)
}

func DecodeAnalyzerScopeLimitsApproval(raw []byte, candidate InvocationCandidate,
	rawRequest, executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
	rawStatement, publicKey, detachedSignature []byte,
	verification AnalyzerProvenanceVerification, plan AnalyzerLaunchPlan,
	review AnalyzerLaunchPlanReview, operatorIdentitySHA256 string,
) (AnalyzerScopeLimitsApproval, ErrorCode) {
	var wire analyzerScopeLimitsApprovalWire
	if !strictDecode(raw, MaxAnalyzerScopeLimitsApprovalEnvelopeBytes, &wire) ||
		!wire.complete() {
		return AnalyzerScopeLimitsApproval{}, CodeInvalidResult
	}
	approval := wire.value()
	if code := ValidateAnalyzerScopeLimitsApproval(approval, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist, release, rawStatement, publicKey,
		detachedSignature, verification, plan, review, operatorIdentitySHA256); code != "" {
		return AnalyzerScopeLimitsApproval{}, CodeInvalidResult
	}
	return approval, ""
}

func encodeAnalyzerScopeApprovalValue(value any, maximum int) ([]byte, ErrorCode) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximum {
		return nil, CodeInternal
	}
	return encoded, ""
}

func validateAnalyzerScopeLimitsApprovalStructure(approval AnalyzerScopeLimitsApproval,
	candidate InvocationCandidate, evidence ExecutableFormatEvidence,
	release AnalyzerReleaseCandidate, verification AnalyzerProvenanceVerification,
	plan AnalyzerLaunchPlan, review AnalyzerLaunchPlanReview, operatorIdentitySHA256 string,
) bool {
	candidateDigest, candidateOK := invocationCandidateSHA256(candidate)
	releaseDigest, releaseOK := canonicalSHA256(release)
	verificationDigest, verificationOK := canonicalSHA256(verification)
	planDigest, planOK := canonicalSHA256(plan)
	reviewDigest, reviewOK := canonicalSHA256(review)
	resourceDigest, resourceOK := canonicalSHA256(plan.Resources)
	sandboxDigest, sandboxOK := canonicalSHA256(plan.Sandbox)
	confirmationDigest := sha256.Sum256([]byte(AnalyzerScopeLimitsApprovalConfirmation))
	return candidateOK && releaseOK && verificationOK && planOK && reviewOK && resourceOK &&
		sandboxOK && approval.ProtocolVersion == AnalyzerScopeLimitsApprovalProtocolVersion &&
		approval.RequestID == candidate.RequestID && approval.Analyzer == candidate.Analyzer &&
		approval.TargetGOOS == evidence.TargetGOOS && approval.TargetGOARCH == evidence.TargetGOARCH &&
		approval.ExecutableSHA256 == evidence.ExecutableSHA256 &&
		approval.CandidateSHA256 == candidateDigest &&
		approval.ReleaseCandidateSHA256 == releaseDigest &&
		approval.ProvenanceVerificationSHA256 == verificationDigest &&
		approval.LaunchPlanSHA256 == planDigest && approval.LaunchPlanReviewSHA256 == reviewDigest &&
		approval.ResourcePlanSHA256 == resourceDigest && approval.SandboxPlanSHA256 == sandboxDigest &&
		reflect.DeepEqual(approval.Resources, plan.Resources) && reflect.DeepEqual(approval.Sandbox, plan.Sandbox) &&
		approval.OperatorIdentitySHA256 == operatorIdentitySHA256 &&
		operatorIdentitySHA256 == review.ReviewerIdentitySHA256 &&
		approval.ConfirmationSHA256 == hex.EncodeToString(confirmationDigest[:]) &&
		approval.Decision == "approved_exact_scope_and_limits_candidate" &&
		approval.RequestScopeBound && approval.ExecutableScopeBound && approval.ReleaseScopeBound &&
		approval.ProvenanceScopeBound && approval.ResourceLimitsBound &&
		approval.SandboxRequirementsBound && approval.DesignReviewBound &&
		approval.OperatorScopeLimitsApproved && !approval.ApprovalAuthenticated &&
		!approval.DurableGrant && !approval.CapabilityGrantIssued && !approval.ExecutionAuthorized &&
		!approval.ProcessStartAuthorized && !approval.ProductInvocationAuthorized &&
		!approval.NetworkAuthorized && !approval.HostFilesystemAuthorized &&
		!approval.ResultPersistenceAuthorized && !approval.ArtifactCommitAuthorized &&
		!approval.OperatorOverrideAllowed
}

type analyzerScopeLimitsApprovalWire struct {
	ProtocolVersion              *string                   `json:"protocol_version"`
	RequestID                    *string                   `json:"request_id"`
	Analyzer                     *string                   `json:"analyzer"`
	TargetGOOS                   *string                   `json:"target_goos"`
	TargetGOARCH                 *string                   `json:"target_goarch"`
	ExecutableSHA256             *string                   `json:"executable_sha256"`
	CandidateSHA256              *string                   `json:"candidate_sha256"`
	ReleaseCandidateSHA256       *string                   `json:"release_candidate_sha256"`
	ProvenanceVerificationSHA256 *string                   `json:"provenance_verification_sha256"`
	LaunchPlanSHA256             *string                   `json:"launch_plan_sha256"`
	LaunchPlanReviewSHA256       *string                   `json:"launch_plan_review_sha256"`
	ResourcePlanSHA256           *string                   `json:"resource_plan_sha256"`
	SandboxPlanSHA256            *string                   `json:"sandbox_plan_sha256"`
	Resources                    *analyzerResourcePlanWire `json:"resources"`
	Sandbox                      *analyzerSandboxPlanWire  `json:"sandbox"`
	OperatorIdentitySHA256       *string                   `json:"operator_identity_sha256"`
	ConfirmationSHA256           *string                   `json:"confirmation_sha256"`
	Decision                     *string                   `json:"decision"`
	RequestScopeBound            *bool                     `json:"request_scope_bound"`
	ExecutableScopeBound         *bool                     `json:"executable_scope_bound"`
	ReleaseScopeBound            *bool                     `json:"release_scope_bound"`
	ProvenanceScopeBound         *bool                     `json:"provenance_scope_bound"`
	ResourceLimitsBound          *bool                     `json:"resource_limits_bound"`
	SandboxRequirementsBound     *bool                     `json:"sandbox_requirements_bound"`
	DesignReviewBound            *bool                     `json:"design_review_bound"`
	OperatorScopeLimitsApproved  *bool                     `json:"operator_scope_limits_approved"`
	ApprovalAuthenticated        *bool                     `json:"approval_authenticated"`
	DurableGrant                 *bool                     `json:"durable_grant"`
	CapabilityGrantIssued        *bool                     `json:"capability_grant_issued"`
	ExecutionAuthorized          *bool                     `json:"execution_authorized"`
	ProcessStartAuthorized       *bool                     `json:"process_start_authorized"`
	ProductInvocationAuthorized  *bool                     `json:"product_invocation_authorized"`
	NetworkAuthorized            *bool                     `json:"network_authorized"`
	HostFilesystemAuthorized     *bool                     `json:"host_filesystem_authorized"`
	ResultPersistenceAuthorized  *bool                     `json:"result_persistence_authorized"`
	ArtifactCommitAuthorized     *bool                     `json:"artifact_commit_authorized"`
	OperatorOverrideAllowed      *bool                     `json:"operator_override_allowed"`
}

func (wire analyzerScopeLimitsApprovalWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.RequestID != nil && wire.Analyzer != nil &&
		wire.TargetGOOS != nil && wire.TargetGOARCH != nil && wire.ExecutableSHA256 != nil &&
		wire.CandidateSHA256 != nil && wire.ReleaseCandidateSHA256 != nil &&
		wire.ProvenanceVerificationSHA256 != nil && wire.LaunchPlanSHA256 != nil &&
		wire.LaunchPlanReviewSHA256 != nil && wire.ResourcePlanSHA256 != nil &&
		wire.SandboxPlanSHA256 != nil && wire.Resources != nil && wire.Resources.complete() &&
		wire.Sandbox != nil && wire.Sandbox.complete() && wire.OperatorIdentitySHA256 != nil &&
		wire.ConfirmationSHA256 != nil && wire.Decision != nil && wire.RequestScopeBound != nil &&
		wire.ExecutableScopeBound != nil && wire.ReleaseScopeBound != nil &&
		wire.ProvenanceScopeBound != nil && wire.ResourceLimitsBound != nil &&
		wire.SandboxRequirementsBound != nil && wire.DesignReviewBound != nil &&
		wire.OperatorScopeLimitsApproved != nil && wire.ApprovalAuthenticated != nil &&
		wire.DurableGrant != nil && wire.CapabilityGrantIssued != nil &&
		wire.ExecutionAuthorized != nil && wire.ProcessStartAuthorized != nil &&
		wire.ProductInvocationAuthorized != nil && wire.NetworkAuthorized != nil &&
		wire.HostFilesystemAuthorized != nil && wire.ResultPersistenceAuthorized != nil &&
		wire.ArtifactCommitAuthorized != nil && wire.OperatorOverrideAllowed != nil
}

func (wire analyzerScopeLimitsApprovalWire) value() AnalyzerScopeLimitsApproval {
	return AnalyzerScopeLimitsApproval{
		ProtocolVersion: *wire.ProtocolVersion, RequestID: *wire.RequestID,
		Analyzer: *wire.Analyzer, TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableSHA256: *wire.ExecutableSHA256, CandidateSHA256: *wire.CandidateSHA256,
		ReleaseCandidateSHA256:       *wire.ReleaseCandidateSHA256,
		ProvenanceVerificationSHA256: *wire.ProvenanceVerificationSHA256,
		LaunchPlanSHA256:             *wire.LaunchPlanSHA256,
		LaunchPlanReviewSHA256:       *wire.LaunchPlanReviewSHA256,
		ResourcePlanSHA256:           *wire.ResourcePlanSHA256, SandboxPlanSHA256: *wire.SandboxPlanSHA256,
		Resources: wire.Resources.value(), Sandbox: wire.Sandbox.value(),
		OperatorIdentitySHA256: *wire.OperatorIdentitySHA256,
		ConfirmationSHA256:     *wire.ConfirmationSHA256, Decision: *wire.Decision,
		RequestScopeBound:           *wire.RequestScopeBound,
		ExecutableScopeBound:        *wire.ExecutableScopeBound,
		ReleaseScopeBound:           *wire.ReleaseScopeBound,
		ProvenanceScopeBound:        *wire.ProvenanceScopeBound,
		ResourceLimitsBound:         *wire.ResourceLimitsBound,
		SandboxRequirementsBound:    *wire.SandboxRequirementsBound,
		DesignReviewBound:           *wire.DesignReviewBound,
		OperatorScopeLimitsApproved: *wire.OperatorScopeLimitsApproved,
		ApprovalAuthenticated:       *wire.ApprovalAuthenticated, DurableGrant: *wire.DurableGrant,
		CapabilityGrantIssued:       *wire.CapabilityGrantIssued,
		ExecutionAuthorized:         *wire.ExecutionAuthorized,
		ProcessStartAuthorized:      *wire.ProcessStartAuthorized,
		ProductInvocationAuthorized: *wire.ProductInvocationAuthorized,
		NetworkAuthorized:           *wire.NetworkAuthorized,
		HostFilesystemAuthorized:    *wire.HostFilesystemAuthorized,
		ResultPersistenceAuthorized: *wire.ResultPersistenceAuthorized,
		ArtifactCommitAuthorized:    *wire.ArtifactCommitAuthorized,
		OperatorOverrideAllowed:     *wire.OperatorOverrideAllowed,
	}
}
