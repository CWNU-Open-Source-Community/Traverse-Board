package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	AnalyzerLaunchPlanProtocolVersion        = "analyzer_launch_plan.v1"
	AnalyzerLaunchPlanReviewProtocolVersion  = "analyzer_launch_plan_review.v1"
	AnalyzerLaunchPlanReviewConfirmation     = "REVIEW-ANALYZER-LAUNCH-PLAN-CANDIDATE"
	MaxAnalyzerLaunchPlanEnvelopeBytes       = 8 * 1024
	MaxAnalyzerLaunchPlanReviewEnvelopeBytes = 4 * 1024
	AnalyzerLaunchPlanMemoryBytes            = 256 * 1024 * 1024
	AnalyzerLaunchPlanMaxProcesses           = 1
)

type AnalyzerResourcePlan struct {
	WallClockMilliseconds int  `json:"wall_clock_ms"`
	CPUTimeMilliseconds   int  `json:"cpu_time_ms"`
	MemoryBytes           int  `json:"memory_bytes"`
	ProcessCount          int  `json:"process_count"`
	StdoutBytes           int  `json:"stdout_bytes"`
	StderrBytes           int  `json:"stderr_bytes"`
	CombinedOutputBytes   int  `json:"combined_output_bytes"`
	CleanupReserveMillis  int  `json:"cleanup_reserve_ms"`
	HardLimitsRequired    bool `json:"hard_limits_required"`
	HardLimitsVerified    bool `json:"hard_limits_verified"`
}

type AnalyzerSandboxPlan struct {
	BackendCandidate             string `json:"backend_candidate"`
	IdentityPolicy               string `json:"identity_policy"`
	FilesystemPolicy             string `json:"filesystem_policy"`
	NetworkPolicy                string `json:"network_policy"`
	EnvironmentPolicy            string `json:"environment_policy"`
	ProcessTreePolicy            string `json:"process_tree_policy"`
	ResultHandoffPolicy          string `json:"result_handoff_policy"`
	ImmutableHandleRequired      bool   `json:"immutable_handle_required"`
	DedicatedIdentityRequired    bool   `json:"dedicated_identity_required"`
	ReadOnlyInputRequired        bool   `json:"read_only_input_required"`
	PrivateStagingRequired       bool   `json:"private_staging_required"`
	NetworkDenyRequired          bool   `json:"network_deny_required"`
	EnvironmentScrubbingRequired bool   `json:"environment_scrubbing_required"`
	ProcessTreeReapRequired      bool   `json:"process_tree_reap_required"`
	NoReplaceHandoffRequired     bool   `json:"no_replace_handoff_required"`
	EnforcementRequired          bool   `json:"enforcement_required"`
	EnforcementVerified          bool   `json:"enforcement_verified"`
}

// AnalyzerLaunchPlan is an operator-reviewable design object. It contains no
// path, command, argv, environment, input body, process starter, or authority.
type AnalyzerLaunchPlan struct {
	ProtocolVersion                string               `json:"protocol_version"`
	CandidateSHA256                string               `json:"candidate_sha256"`
	InvocationPreflightSHA256      string               `json:"invocation_preflight_sha256"`
	ExecutableFormatEvidenceSHA256 string               `json:"executable_format_evidence_sha256"`
	ReleaseCandidateSHA256         string               `json:"release_candidate_sha256"`
	RequestID                      string               `json:"request_id"`
	Analyzer                       string               `json:"analyzer"`
	TargetGOOS                     string               `json:"target_goos"`
	TargetGOARCH                   string               `json:"target_goarch"`
	ExecutableSHA256               string               `json:"executable_sha256"`
	Resources                      AnalyzerResourcePlan `json:"resources"`
	Sandbox                        AnalyzerSandboxPlan  `json:"sandbox"`
	RequestBound                   bool                 `json:"request_bound"`
	ExecutableBound                bool                 `json:"executable_bound"`
	ReleasePolicyBound             bool                 `json:"release_policy_bound"`
	OperatorReviewRequired         bool                 `json:"operator_review_required"`
	OperatorReviewed               bool                 `json:"operator_reviewed"`
	DesignCandidateOnly            bool                 `json:"design_candidate_only"`
	EnforcementReady               bool                 `json:"enforcement_ready"`
	StartBlocked                   bool                 `json:"start_blocked"`
	PathIncluded                   bool                 `json:"path_included"`
	CommandIncluded                bool                 `json:"command_included"`
	ArgvIncluded                   bool                 `json:"argv_included"`
	EnvironmentIncluded            bool                 `json:"environment_included"`
	InputBodyIncluded              bool                 `json:"input_body_included"`
	ProcessStarterPresent          bool                 `json:"process_starter_present"`
	ExecutionAuthorized            bool                 `json:"execution_authorized"`
	ProductInvocationAuthorized    bool                 `json:"product_invocation_authorized"`
	ResultPersistenceAuthorized    bool                 `json:"result_persistence_authorized"`
	ArtifactCommitAuthorized       bool                 `json:"artifact_commit_authorized"`
}

// AnalyzerLaunchPlanReview acknowledges one exact design candidate. It is not
// an approval to run a process and cannot be upgraded into one by mutation.
type AnalyzerLaunchPlanReview struct {
	ProtocolVersion             string `json:"protocol_version"`
	LaunchPlanSHA256            string `json:"launch_plan_sha256"`
	ReleaseCandidateSHA256      string `json:"release_candidate_sha256"`
	ReviewerIdentitySHA256      string `json:"reviewer_identity_sha256"`
	ConfirmationSHA256          string `json:"confirmation_sha256"`
	Decision                    string `json:"decision"`
	OperatorReviewed            bool   `json:"operator_reviewed"`
	DesignReviewOnly            bool   `json:"design_review_only"`
	ExecutionApproved           bool   `json:"execution_approved"`
	ProcessStartAuthorized      bool   `json:"process_start_authorized"`
	ProductInvocationAuthorized bool   `json:"product_invocation_authorized"`
	OperatorOverrideAllowed     bool   `json:"operator_override_allowed"`
}

func BuildAnalyzerLaunchPlan(candidate InvocationCandidate, rawRequest, executable []byte,
	identity ExecutableIdentity, preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate,
) (AnalyzerLaunchPlan, ErrorCode) {
	if code := ValidateAnalyzerReleaseCandidate(release, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist); code != "" {
		return AnalyzerLaunchPlan{}, CodeInvalidResult
	}
	candidateDigest, candidateOK := invocationCandidateSHA256(candidate)
	preflightDigest, preflightOK := canonicalSHA256(preflight)
	evidenceDigest, evidenceOK := canonicalSHA256(evidence)
	releaseDigest, releaseOK := canonicalSHA256(release)
	if !candidateOK || !preflightOK || !evidenceOK || !releaseOK {
		return AnalyzerLaunchPlan{}, CodeInternal
	}
	cleanupReserve := candidate.Limits.TimeoutMilliseconds / 10
	if cleanupReserve < 10 {
		cleanupReserve = 10
	}
	if cleanupReserve > 1_000 {
		cleanupReserve = 1_000
	}
	resources := AnalyzerResourcePlan{
		WallClockMilliseconds: candidate.Limits.TimeoutMilliseconds,
		CPUTimeMilliseconds:   candidate.Limits.TimeoutMilliseconds - cleanupReserve,
		MemoryBytes:           AnalyzerLaunchPlanMemoryBytes,
		ProcessCount:          AnalyzerLaunchPlanMaxProcesses,
		StdoutBytes:           candidate.Limits.MaxOutputBytes,
		StderrBytes:           candidate.Limits.MaxOutputBytes,
		CombinedOutputBytes:   candidate.Limits.MaxOutputBytes,
		CleanupReserveMillis:  cleanupReserve, HardLimitsRequired: true,
	}
	backend := analyzerSandboxBackendCandidate(evidence.TargetGOOS)
	if backend == "" {
		return AnalyzerLaunchPlan{}, CodeInvalidContent
	}
	sandbox := AnalyzerSandboxPlan{
		BackendCandidate: backend, IdentityPolicy: "dedicated_non_administrator",
		FilesystemPolicy: "read_only_input_private_staging",
		NetworkPolicy:    "deny_all", EnvironmentPolicy: "explicit_minimal_allowlist",
		ProcessTreePolicy:       "single_process_tree_reap",
		ResultHandoffPolicy:     "digest_bound_no_replace",
		ImmutableHandleRequired: true, DedicatedIdentityRequired: true,
		ReadOnlyInputRequired: true, PrivateStagingRequired: true, NetworkDenyRequired: true,
		EnvironmentScrubbingRequired: true, ProcessTreeReapRequired: true,
		NoReplaceHandoffRequired: true, EnforcementRequired: true,
	}
	plan := AnalyzerLaunchPlan{
		ProtocolVersion: AnalyzerLaunchPlanProtocolVersion, CandidateSHA256: candidateDigest,
		InvocationPreflightSHA256:      preflightDigest,
		ExecutableFormatEvidenceSHA256: evidenceDigest,
		ReleaseCandidateSHA256:         releaseDigest, RequestID: candidate.RequestID,
		Analyzer: candidate.Analyzer, TargetGOOS: evidence.TargetGOOS,
		TargetGOARCH: evidence.TargetGOARCH, ExecutableSHA256: evidence.ExecutableSHA256,
		Resources: resources, Sandbox: sandbox, RequestBound: true, ExecutableBound: true,
		ReleasePolicyBound: true, OperatorReviewRequired: true, DesignCandidateOnly: true,
		StartBlocked: true,
	}
	if !validateAnalyzerLaunchPlanStructure(plan, candidate, preflight, evidence, release) {
		return AnalyzerLaunchPlan{}, CodeInternal
	}
	return plan, ""
}

func ValidateAnalyzerLaunchPlan(plan AnalyzerLaunchPlan, candidate InvocationCandidate,
	rawRequest, executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
) ErrorCode {
	expected, code := BuildAnalyzerLaunchPlan(candidate, rawRequest, executable, identity,
		preflight, evidence, manifest, allowlist, release)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(plan, expected) {
		return CodeInvalidResult
	}
	return ""
}

func ReviewAnalyzerLaunchPlan(plan AnalyzerLaunchPlan, candidate InvocationCandidate,
	rawRequest, executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
	reviewerIdentitySHA256, confirmation string,
) (AnalyzerLaunchPlanReview, ErrorCode) {
	if code := ValidateAnalyzerLaunchPlan(plan, candidate, rawRequest, executable, identity,
		preflight, evidence, manifest, allowlist, release); code != "" {
		return AnalyzerLaunchPlanReview{}, CodeInvalidResult
	}
	if !validDigest(reviewerIdentitySHA256) || confirmation != AnalyzerLaunchPlanReviewConfirmation {
		return AnalyzerLaunchPlanReview{}, CodeInvalidContent
	}
	planDigest, planOK := canonicalSHA256(plan)
	releaseDigest, releaseOK := canonicalSHA256(release)
	if !planOK || !releaseOK {
		return AnalyzerLaunchPlanReview{}, CodeInternal
	}
	confirmationDigest := sha256.Sum256([]byte(confirmation))
	review := AnalyzerLaunchPlanReview{
		ProtocolVersion: AnalyzerLaunchPlanReviewProtocolVersion, LaunchPlanSHA256: planDigest,
		ReleaseCandidateSHA256: releaseDigest, ReviewerIdentitySHA256: reviewerIdentitySHA256,
		ConfirmationSHA256: hex.EncodeToString(confirmationDigest[:]),
		Decision:           "accepted_as_design_candidate", OperatorReviewed: true,
		DesignReviewOnly: true,
	}
	if !validateAnalyzerLaunchPlanReviewStructure(review, plan, release,
		reviewerIdentitySHA256) {
		return AnalyzerLaunchPlanReview{}, CodeInternal
	}
	return review, ""
}

func ValidateAnalyzerLaunchPlanReview(review AnalyzerLaunchPlanReview, plan AnalyzerLaunchPlan,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate, reviewerIdentitySHA256 string,
) ErrorCode {
	expected, code := ReviewAnalyzerLaunchPlan(plan, candidate, rawRequest, executable, identity,
		preflight, evidence, manifest, allowlist, release, reviewerIdentitySHA256,
		AnalyzerLaunchPlanReviewConfirmation)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(review, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerLaunchPlan(plan AnalyzerLaunchPlan, candidate InvocationCandidate,
	rawRequest, executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerLaunchPlan(plan, candidate, rawRequest, executable, identity,
		preflight, evidence, manifest, allowlist, release); code != "" {
		return nil, code
	}
	return encodeAnalyzerLaunchValue(plan, MaxAnalyzerLaunchPlanEnvelopeBytes)
}

func DecodeAnalyzerLaunchPlan(raw []byte, candidate InvocationCandidate, rawRequest,
	executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
) (AnalyzerLaunchPlan, ErrorCode) {
	var wire analyzerLaunchPlanWire
	if !strictDecode(raw, MaxAnalyzerLaunchPlanEnvelopeBytes, &wire) || !wire.complete() {
		return AnalyzerLaunchPlan{}, CodeInvalidResult
	}
	plan := wire.value()
	if code := ValidateAnalyzerLaunchPlan(plan, candidate, rawRequest, executable, identity,
		preflight, evidence, manifest, allowlist, release); code != "" {
		return AnalyzerLaunchPlan{}, CodeInvalidResult
	}
	return plan, ""
}

func EncodeAnalyzerLaunchPlanReview(review AnalyzerLaunchPlanReview, plan AnalyzerLaunchPlan,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate, reviewerIdentitySHA256 string,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerLaunchPlanReview(review, plan, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist, release,
		reviewerIdentitySHA256); code != "" {
		return nil, code
	}
	return encodeAnalyzerLaunchValue(review, MaxAnalyzerLaunchPlanReviewEnvelopeBytes)
}

func DecodeAnalyzerLaunchPlanReview(raw []byte, plan AnalyzerLaunchPlan,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate, reviewerIdentitySHA256 string,
) (AnalyzerLaunchPlanReview, ErrorCode) {
	var wire analyzerLaunchPlanReviewWire
	if !strictDecode(raw, MaxAnalyzerLaunchPlanReviewEnvelopeBytes, &wire) || !wire.complete() {
		return AnalyzerLaunchPlanReview{}, CodeInvalidResult
	}
	review := wire.value()
	if code := ValidateAnalyzerLaunchPlanReview(review, plan, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist, release,
		reviewerIdentitySHA256); code != "" {
		return AnalyzerLaunchPlanReview{}, CodeInvalidResult
	}
	return review, ""
}

func encodeAnalyzerLaunchValue(value any, maximum int) ([]byte, ErrorCode) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximum {
		return nil, CodeInternal
	}
	return encoded, ""
}

func validateAnalyzerLaunchPlanStructure(plan AnalyzerLaunchPlan,
	candidate InvocationCandidate, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, release AnalyzerReleaseCandidate,
) bool {
	candidateDigest, candidateOK := invocationCandidateSHA256(candidate)
	preflightDigest, preflightOK := canonicalSHA256(preflight)
	evidenceDigest, evidenceOK := canonicalSHA256(evidence)
	releaseDigest, releaseOK := canonicalSHA256(release)
	return candidateOK && preflightOK && evidenceOK && releaseOK &&
		plan.ProtocolVersion == AnalyzerLaunchPlanProtocolVersion &&
		plan.CandidateSHA256 == candidateDigest &&
		plan.InvocationPreflightSHA256 == preflightDigest &&
		plan.ExecutableFormatEvidenceSHA256 == evidenceDigest &&
		plan.ReleaseCandidateSHA256 == releaseDigest && plan.RequestID == candidate.RequestID &&
		plan.Analyzer == candidate.Analyzer && plan.TargetGOOS == evidence.TargetGOOS &&
		plan.TargetGOARCH == evidence.TargetGOARCH &&
		plan.ExecutableSHA256 == evidence.ExecutableSHA256 &&
		validAnalyzerResourcePlan(plan.Resources, candidate) &&
		validAnalyzerSandboxPlan(plan.Sandbox, evidence.TargetGOOS) && plan.RequestBound &&
		plan.ExecutableBound && plan.ReleasePolicyBound && plan.OperatorReviewRequired &&
		!plan.OperatorReviewed && plan.DesignCandidateOnly && !plan.EnforcementReady &&
		plan.StartBlocked && !plan.PathIncluded && !plan.CommandIncluded && !plan.ArgvIncluded &&
		!plan.EnvironmentIncluded && !plan.InputBodyIncluded && !plan.ProcessStarterPresent &&
		!plan.ExecutionAuthorized && !plan.ProductInvocationAuthorized &&
		!plan.ResultPersistenceAuthorized && !plan.ArtifactCommitAuthorized
}

func validAnalyzerResourcePlan(resources AnalyzerResourcePlan,
	candidate InvocationCandidate,
) bool {
	reserve := candidate.Limits.TimeoutMilliseconds / 10
	if reserve < 10 {
		reserve = 10
	}
	if reserve > 1_000 {
		reserve = 1_000
	}
	return resources.WallClockMilliseconds == candidate.Limits.TimeoutMilliseconds &&
		resources.CPUTimeMilliseconds == candidate.Limits.TimeoutMilliseconds-reserve &&
		resources.CPUTimeMilliseconds > 0 &&
		resources.MemoryBytes == AnalyzerLaunchPlanMemoryBytes &&
		resources.ProcessCount == AnalyzerLaunchPlanMaxProcesses &&
		resources.StdoutBytes == candidate.Limits.MaxOutputBytes &&
		resources.StderrBytes == candidate.Limits.MaxOutputBytes &&
		resources.CombinedOutputBytes == candidate.Limits.MaxOutputBytes &&
		resources.CleanupReserveMillis == reserve && resources.HardLimitsRequired &&
		!resources.HardLimitsVerified
}

func validAnalyzerSandboxPlan(sandbox AnalyzerSandboxPlan, targetGOOS string) bool {
	return sandbox.BackendCandidate == analyzerSandboxBackendCandidate(targetGOOS) &&
		sandbox.BackendCandidate != "" && sandbox.IdentityPolicy == "dedicated_non_administrator" &&
		sandbox.FilesystemPolicy == "read_only_input_private_staging" &&
		sandbox.NetworkPolicy == "deny_all" &&
		sandbox.EnvironmentPolicy == "explicit_minimal_allowlist" &&
		sandbox.ProcessTreePolicy == "single_process_tree_reap" &&
		sandbox.ResultHandoffPolicy == "digest_bound_no_replace" &&
		sandbox.ImmutableHandleRequired && sandbox.DedicatedIdentityRequired &&
		sandbox.ReadOnlyInputRequired && sandbox.PrivateStagingRequired &&
		sandbox.NetworkDenyRequired &&
		sandbox.EnvironmentScrubbingRequired && sandbox.ProcessTreeReapRequired &&
		sandbox.NoReplaceHandoffRequired && sandbox.EnforcementRequired &&
		!sandbox.EnforcementVerified
}

func validateAnalyzerLaunchPlanReviewStructure(review AnalyzerLaunchPlanReview,
	plan AnalyzerLaunchPlan, release AnalyzerReleaseCandidate, reviewerIdentitySHA256 string,
) bool {
	planDigest, planOK := canonicalSHA256(plan)
	releaseDigest, releaseOK := canonicalSHA256(release)
	confirmationDigest := sha256.Sum256([]byte(AnalyzerLaunchPlanReviewConfirmation))
	return planOK && releaseOK && review.ProtocolVersion == AnalyzerLaunchPlanReviewProtocolVersion &&
		review.LaunchPlanSHA256 == planDigest && review.ReleaseCandidateSHA256 == releaseDigest &&
		review.ReviewerIdentitySHA256 == reviewerIdentitySHA256 &&
		review.ConfirmationSHA256 == hex.EncodeToString(confirmationDigest[:]) &&
		review.Decision == "accepted_as_design_candidate" && review.OperatorReviewed &&
		review.DesignReviewOnly && !review.ExecutionApproved && !review.ProcessStartAuthorized &&
		!review.ProductInvocationAuthorized && !review.OperatorOverrideAllowed
}

func analyzerSandboxBackendCandidate(goos string) string {
	switch goos {
	case "windows":
		return "windows_restricted_token_job_candidate.v1"
	case "linux":
		return "linux_namespace_seccomp_candidate.v1"
	default:
		return ""
	}
}

type analyzerResourcePlanWire struct {
	WallClockMilliseconds *int  `json:"wall_clock_ms"`
	CPUTimeMilliseconds   *int  `json:"cpu_time_ms"`
	MemoryBytes           *int  `json:"memory_bytes"`
	ProcessCount          *int  `json:"process_count"`
	StdoutBytes           *int  `json:"stdout_bytes"`
	StderrBytes           *int  `json:"stderr_bytes"`
	CombinedOutputBytes   *int  `json:"combined_output_bytes"`
	CleanupReserveMillis  *int  `json:"cleanup_reserve_ms"`
	HardLimitsRequired    *bool `json:"hard_limits_required"`
	HardLimitsVerified    *bool `json:"hard_limits_verified"`
}

func (wire analyzerResourcePlanWire) complete() bool {
	return wire.WallClockMilliseconds != nil && wire.CPUTimeMilliseconds != nil &&
		wire.MemoryBytes != nil && wire.ProcessCount != nil && wire.StdoutBytes != nil &&
		wire.StderrBytes != nil && wire.CombinedOutputBytes != nil &&
		wire.CleanupReserveMillis != nil && wire.HardLimitsRequired != nil &&
		wire.HardLimitsVerified != nil
}

func (wire analyzerResourcePlanWire) value() AnalyzerResourcePlan {
	return AnalyzerResourcePlan{
		WallClockMilliseconds: *wire.WallClockMilliseconds,
		CPUTimeMilliseconds:   *wire.CPUTimeMilliseconds, MemoryBytes: *wire.MemoryBytes,
		ProcessCount: *wire.ProcessCount, StdoutBytes: *wire.StdoutBytes,
		StderrBytes: *wire.StderrBytes, CombinedOutputBytes: *wire.CombinedOutputBytes,
		CleanupReserveMillis: *wire.CleanupReserveMillis,
		HardLimitsRequired:   *wire.HardLimitsRequired,
		HardLimitsVerified:   *wire.HardLimitsVerified,
	}
}

type analyzerSandboxPlanWire struct {
	BackendCandidate             *string `json:"backend_candidate"`
	IdentityPolicy               *string `json:"identity_policy"`
	FilesystemPolicy             *string `json:"filesystem_policy"`
	NetworkPolicy                *string `json:"network_policy"`
	EnvironmentPolicy            *string `json:"environment_policy"`
	ProcessTreePolicy            *string `json:"process_tree_policy"`
	ResultHandoffPolicy          *string `json:"result_handoff_policy"`
	ImmutableHandleRequired      *bool   `json:"immutable_handle_required"`
	DedicatedIdentityRequired    *bool   `json:"dedicated_identity_required"`
	ReadOnlyInputRequired        *bool   `json:"read_only_input_required"`
	PrivateStagingRequired       *bool   `json:"private_staging_required"`
	NetworkDenyRequired          *bool   `json:"network_deny_required"`
	EnvironmentScrubbingRequired *bool   `json:"environment_scrubbing_required"`
	ProcessTreeReapRequired      *bool   `json:"process_tree_reap_required"`
	NoReplaceHandoffRequired     *bool   `json:"no_replace_handoff_required"`
	EnforcementRequired          *bool   `json:"enforcement_required"`
	EnforcementVerified          *bool   `json:"enforcement_verified"`
}

func (wire analyzerSandboxPlanWire) complete() bool {
	return wire.BackendCandidate != nil && wire.IdentityPolicy != nil &&
		wire.FilesystemPolicy != nil && wire.NetworkPolicy != nil &&
		wire.EnvironmentPolicy != nil && wire.ProcessTreePolicy != nil &&
		wire.ResultHandoffPolicy != nil && wire.ImmutableHandleRequired != nil &&
		wire.DedicatedIdentityRequired != nil && wire.ReadOnlyInputRequired != nil &&
		wire.PrivateStagingRequired != nil && wire.NetworkDenyRequired != nil &&
		wire.EnvironmentScrubbingRequired != nil && wire.ProcessTreeReapRequired != nil &&
		wire.NoReplaceHandoffRequired != nil && wire.EnforcementRequired != nil &&
		wire.EnforcementVerified != nil
}

func (wire analyzerSandboxPlanWire) value() AnalyzerSandboxPlan {
	return AnalyzerSandboxPlan{
		BackendCandidate: *wire.BackendCandidate, IdentityPolicy: *wire.IdentityPolicy,
		FilesystemPolicy: *wire.FilesystemPolicy, NetworkPolicy: *wire.NetworkPolicy,
		EnvironmentPolicy:            *wire.EnvironmentPolicy,
		ProcessTreePolicy:            *wire.ProcessTreePolicy,
		ResultHandoffPolicy:          *wire.ResultHandoffPolicy,
		ImmutableHandleRequired:      *wire.ImmutableHandleRequired,
		DedicatedIdentityRequired:    *wire.DedicatedIdentityRequired,
		ReadOnlyInputRequired:        *wire.ReadOnlyInputRequired,
		PrivateStagingRequired:       *wire.PrivateStagingRequired,
		NetworkDenyRequired:          *wire.NetworkDenyRequired,
		EnvironmentScrubbingRequired: *wire.EnvironmentScrubbingRequired,
		ProcessTreeReapRequired:      *wire.ProcessTreeReapRequired,
		NoReplaceHandoffRequired:     *wire.NoReplaceHandoffRequired,
		EnforcementRequired:          *wire.EnforcementRequired,
		EnforcementVerified:          *wire.EnforcementVerified,
	}
}

type analyzerLaunchPlanWire struct {
	ProtocolVersion                *string                   `json:"protocol_version"`
	CandidateSHA256                *string                   `json:"candidate_sha256"`
	InvocationPreflightSHA256      *string                   `json:"invocation_preflight_sha256"`
	ExecutableFormatEvidenceSHA256 *string                   `json:"executable_format_evidence_sha256"`
	ReleaseCandidateSHA256         *string                   `json:"release_candidate_sha256"`
	RequestID                      *string                   `json:"request_id"`
	Analyzer                       *string                   `json:"analyzer"`
	TargetGOOS                     *string                   `json:"target_goos"`
	TargetGOARCH                   *string                   `json:"target_goarch"`
	ExecutableSHA256               *string                   `json:"executable_sha256"`
	Resources                      *analyzerResourcePlanWire `json:"resources"`
	Sandbox                        *analyzerSandboxPlanWire  `json:"sandbox"`
	RequestBound                   *bool                     `json:"request_bound"`
	ExecutableBound                *bool                     `json:"executable_bound"`
	ReleasePolicyBound             *bool                     `json:"release_policy_bound"`
	OperatorReviewRequired         *bool                     `json:"operator_review_required"`
	OperatorReviewed               *bool                     `json:"operator_reviewed"`
	DesignCandidateOnly            *bool                     `json:"design_candidate_only"`
	EnforcementReady               *bool                     `json:"enforcement_ready"`
	StartBlocked                   *bool                     `json:"start_blocked"`
	PathIncluded                   *bool                     `json:"path_included"`
	CommandIncluded                *bool                     `json:"command_included"`
	ArgvIncluded                   *bool                     `json:"argv_included"`
	EnvironmentIncluded            *bool                     `json:"environment_included"`
	InputBodyIncluded              *bool                     `json:"input_body_included"`
	ProcessStarterPresent          *bool                     `json:"process_starter_present"`
	ExecutionAuthorized            *bool                     `json:"execution_authorized"`
	ProductInvocationAuthorized    *bool                     `json:"product_invocation_authorized"`
	ResultPersistenceAuthorized    *bool                     `json:"result_persistence_authorized"`
	ArtifactCommitAuthorized       *bool                     `json:"artifact_commit_authorized"`
}

func (wire analyzerLaunchPlanWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.CandidateSHA256 != nil &&
		wire.InvocationPreflightSHA256 != nil && wire.ExecutableFormatEvidenceSHA256 != nil &&
		wire.ReleaseCandidateSHA256 != nil && wire.RequestID != nil && wire.Analyzer != nil &&
		wire.TargetGOOS != nil && wire.TargetGOARCH != nil && wire.ExecutableSHA256 != nil &&
		wire.Resources != nil && wire.Resources.complete() && wire.Sandbox != nil &&
		wire.Sandbox.complete() && wire.RequestBound != nil && wire.ExecutableBound != nil &&
		wire.ReleasePolicyBound != nil && wire.OperatorReviewRequired != nil &&
		wire.OperatorReviewed != nil && wire.DesignCandidateOnly != nil &&
		wire.EnforcementReady != nil && wire.StartBlocked != nil && wire.PathIncluded != nil &&
		wire.CommandIncluded != nil && wire.ArgvIncluded != nil && wire.EnvironmentIncluded != nil &&
		wire.InputBodyIncluded != nil && wire.ProcessStarterPresent != nil &&
		wire.ExecutionAuthorized != nil && wire.ProductInvocationAuthorized != nil &&
		wire.ResultPersistenceAuthorized != nil && wire.ArtifactCommitAuthorized != nil
}

func (wire analyzerLaunchPlanWire) value() AnalyzerLaunchPlan {
	return AnalyzerLaunchPlan{
		ProtocolVersion: *wire.ProtocolVersion, CandidateSHA256: *wire.CandidateSHA256,
		InvocationPreflightSHA256:      *wire.InvocationPreflightSHA256,
		ExecutableFormatEvidenceSHA256: *wire.ExecutableFormatEvidenceSHA256,
		ReleaseCandidateSHA256:         *wire.ReleaseCandidateSHA256, RequestID: *wire.RequestID,
		Analyzer: *wire.Analyzer, TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableSHA256: *wire.ExecutableSHA256, Resources: wire.Resources.value(),
		Sandbox: wire.Sandbox.value(), RequestBound: *wire.RequestBound,
		ExecutableBound: *wire.ExecutableBound, ReleasePolicyBound: *wire.ReleasePolicyBound,
		OperatorReviewRequired: *wire.OperatorReviewRequired,
		OperatorReviewed:       *wire.OperatorReviewed, DesignCandidateOnly: *wire.DesignCandidateOnly,
		EnforcementReady: *wire.EnforcementReady, StartBlocked: *wire.StartBlocked,
		PathIncluded: *wire.PathIncluded, CommandIncluded: *wire.CommandIncluded,
		ArgvIncluded: *wire.ArgvIncluded, EnvironmentIncluded: *wire.EnvironmentIncluded,
		InputBodyIncluded:           *wire.InputBodyIncluded,
		ProcessStarterPresent:       *wire.ProcessStarterPresent,
		ExecutionAuthorized:         *wire.ExecutionAuthorized,
		ProductInvocationAuthorized: *wire.ProductInvocationAuthorized,
		ResultPersistenceAuthorized: *wire.ResultPersistenceAuthorized,
		ArtifactCommitAuthorized:    *wire.ArtifactCommitAuthorized,
	}
}

type analyzerLaunchPlanReviewWire struct {
	ProtocolVersion             *string `json:"protocol_version"`
	LaunchPlanSHA256            *string `json:"launch_plan_sha256"`
	ReleaseCandidateSHA256      *string `json:"release_candidate_sha256"`
	ReviewerIdentitySHA256      *string `json:"reviewer_identity_sha256"`
	ConfirmationSHA256          *string `json:"confirmation_sha256"`
	Decision                    *string `json:"decision"`
	OperatorReviewed            *bool   `json:"operator_reviewed"`
	DesignReviewOnly            *bool   `json:"design_review_only"`
	ExecutionApproved           *bool   `json:"execution_approved"`
	ProcessStartAuthorized      *bool   `json:"process_start_authorized"`
	ProductInvocationAuthorized *bool   `json:"product_invocation_authorized"`
	OperatorOverrideAllowed     *bool   `json:"operator_override_allowed"`
}

func (wire analyzerLaunchPlanReviewWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.LaunchPlanSHA256 != nil &&
		wire.ReleaseCandidateSHA256 != nil && wire.ReviewerIdentitySHA256 != nil &&
		wire.ConfirmationSHA256 != nil && wire.Decision != nil && wire.OperatorReviewed != nil &&
		wire.DesignReviewOnly != nil && wire.ExecutionApproved != nil &&
		wire.ProcessStartAuthorized != nil && wire.ProductInvocationAuthorized != nil &&
		wire.OperatorOverrideAllowed != nil
}

func (wire analyzerLaunchPlanReviewWire) value() AnalyzerLaunchPlanReview {
	return AnalyzerLaunchPlanReview{
		ProtocolVersion: *wire.ProtocolVersion, LaunchPlanSHA256: *wire.LaunchPlanSHA256,
		ReleaseCandidateSHA256: *wire.ReleaseCandidateSHA256,
		ReviewerIdentitySHA256: *wire.ReviewerIdentitySHA256,
		ConfirmationSHA256:     *wire.ConfirmationSHA256, Decision: *wire.Decision,
		OperatorReviewed: *wire.OperatorReviewed, DesignReviewOnly: *wire.DesignReviewOnly,
		ExecutionApproved:           *wire.ExecutionApproved,
		ProcessStartAuthorized:      *wire.ProcessStartAuthorized,
		ProductInvocationAuthorized: *wire.ProductInvocationAuthorized,
		OperatorOverrideAllowed:     *wire.OperatorOverrideAllowed,
	}
}
