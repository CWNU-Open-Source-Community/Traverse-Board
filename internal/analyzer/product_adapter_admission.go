package analyzer

import (
	"encoding/json"
	"reflect"
)

const (
	AnalyzerProductAdapterAdmissionProtocolVersion  = "analyzer_product_adapter_admission_matrix.v1"
	AnalyzerAdmissionStatusCandidateMetadata        = "candidate_metadata_only"
	AnalyzerAdmissionStatusTestConformance          = "test_conformance_only"
	AnalyzerAdmissionEvidenceSourceMissing          = "none"
	MaxAnalyzerProductAdapterAdmissionEnvelopeBytes = 32 * 1024
)

// AnalyzerProductAdapterEvidenceInput is transient caller-owned validation
// material. It is never serialized by this package and grants no authority.
// Raw request and executable bytes remain necessary so every derived record can
// be rebuilt instead of being trusted by digest alone.
type AnalyzerProductAdapterEvidenceInput struct {
	Candidate              InvocationCandidate            `json:"-"`
	RawRequest             []byte                         `json:"-"`
	Executable             []byte                         `json:"-"`
	Identity               ExecutableIdentity             `json:"-"`
	Preflight              InvocationPreflight            `json:"-"`
	FormatEvidence         ExecutableFormatEvidence       `json:"-"`
	Manifest               AnalyzerReleaseManifest        `json:"-"`
	Allowlist              AnalyzerReleaseAllowlist       `json:"-"`
	Release                AnalyzerReleaseCandidate       `json:"-"`
	ProvenanceStatement    []byte                         `json:"-"`
	ProvenancePublicKey    []byte                         `json:"-"`
	ProvenanceSignature    []byte                         `json:"-"`
	ProvenanceVerification AnalyzerProvenanceVerification `json:"-"`
	LaunchPlan             AnalyzerLaunchPlan             `json:"-"`
	LaunchPlanReview       AnalyzerLaunchPlanReview       `json:"-"`
	ScopeApproval          AnalyzerScopeLimitsApproval    `json:"-"`
	ThreatModel            ProductAdapterThreatModel      `json:"-"`
}

// AnalyzerProductAdapterAuthority is shared by product-admission contracts.
// The only valid value in P10-I is the all-false zero value.
type AnalyzerProductAdapterAuthority struct {
	ProductInvocation bool `json:"product_invocation"`
	ProcessStart      bool `json:"process_start"`
	Execution         bool `json:"execution"`
	CapabilityIssue   bool `json:"capability_issue"`
	RecoveryApply     bool `json:"recovery_apply"`
	Persistence       bool `json:"persistence"`
	ArtifactCommit    bool `json:"artifact_commit"`
	Network           bool `json:"network"`
	HostFilesystem    bool `json:"host_filesystem"`
	SecretAccess      bool `json:"secret_access"`
	OperatorOverride  bool `json:"operator_override"`
}

// AnalyzerProductAdapterAdmissionControl maps one threat-model control to the
// strongest evidence currently available. Candidate and test evidence never
// count as production verification.
type AnalyzerProductAdapterAdmissionControl struct {
	ControlID                  string `json:"control_id"`
	EvidenceSource             string `json:"evidence_source"`
	Status                     string `json:"status"`
	CandidateEvidencePresent   bool   `json:"candidate_evidence_present"`
	TestConformanceOnly        bool   `json:"test_conformance_only"`
	ProductionEvidenceRequired bool   `json:"production_evidence_required"`
	ProductionEvidenceVerified bool   `json:"production_evidence_verified"`
	BlocksProductStart         bool   `json:"blocks_product_start"`
}

// AnalyzerProductAdapterAdmissionMatrix is a fail-closed production gate. It
// binds the exact F/G evidence chain and classifies H observations as test-only.
// It contains no path, command, argv, environment, input body, starter, or grant.
type AnalyzerProductAdapterAdmissionMatrix struct {
	ProtocolVersion               string                                   `json:"protocol_version"`
	ThreatModelSHA256             string                                   `json:"threat_model_sha256"`
	CandidateSHA256               string                                   `json:"candidate_sha256"`
	ReleaseCandidateSHA256        string                                   `json:"release_candidate_sha256"`
	ProvenanceVerificationSHA256  string                                   `json:"provenance_verification_sha256"`
	LaunchPlanSHA256              string                                   `json:"launch_plan_sha256"`
	LaunchPlanReviewSHA256        string                                   `json:"launch_plan_review_sha256"`
	ScopeApprovalSHA256           string                                   `json:"scope_approval_sha256"`
	RequestID                     string                                   `json:"request_id"`
	Analyzer                      string                                   `json:"analyzer"`
	TargetGOOS                    string                                   `json:"target_goos"`
	TargetGOARCH                  string                                   `json:"target_goarch"`
	ExecutableSHA256              string                                   `json:"executable_sha256"`
	Controls                      []AnalyzerProductAdapterAdmissionControl `json:"controls"`
	RequiredControlCount          int                                      `json:"required_control_count"`
	CandidateEvidenceCount        int                                      `json:"candidate_evidence_count"`
	TestConformanceCount          int                                      `json:"test_conformance_count"`
	ProductionVerifiedCount       int                                      `json:"production_verified_count"`
	OpenRequirementCount          int                                      `json:"open_requirement_count"`
	ExactEvidenceBound            bool                                     `json:"exact_evidence_bound"`
	AllControlsRequired           bool                                     `json:"all_controls_required"`
	AllProductionEvidenceVerified bool                                     `json:"all_production_evidence_verified"`
	AdmissionReady                bool                                     `json:"admission_ready"`
	StartBlocked                  bool                                     `json:"start_blocked"`
	MetadataOnly                  bool                                     `json:"metadata_only"`
	ProductAdapterPresent         bool                                     `json:"product_adapter_present"`
	ProcessStarterPresent         bool                                     `json:"process_starter_present"`
	Authority                     AnalyzerProductAdapterAuthority          `json:"authority"`
}

func ValidateAnalyzerProductAdapterEvidenceInput(input AnalyzerProductAdapterEvidenceInput) ErrorCode {
	if code := ValidateProductAdapterThreatModel(input.ThreatModel); code != "" {
		return CodeInvalidResult
	}
	if code := ValidateAnalyzerScopeLimitsApproval(input.ScopeApproval, input.Candidate,
		input.RawRequest, input.Executable, input.Identity, input.Preflight,
		input.FormatEvidence, input.Manifest, input.Allowlist, input.Release,
		input.ProvenanceStatement, input.ProvenancePublicKey, input.ProvenanceSignature,
		input.ProvenanceVerification, input.LaunchPlan, input.LaunchPlanReview,
		input.ScopeApproval.OperatorIdentitySHA256); code != "" {
		return CodeInvalidResult
	}
	return ""
}

func BuildAnalyzerProductAdapterAdmissionMatrix(input AnalyzerProductAdapterEvidenceInput) (
	AnalyzerProductAdapterAdmissionMatrix, ErrorCode,
) {
	if code := ValidateAnalyzerProductAdapterEvidenceInput(input); code != "" {
		return AnalyzerProductAdapterAdmissionMatrix{}, code
	}
	threatDigest, threatOK := canonicalSHA256(input.ThreatModel)
	candidateDigest, candidateOK := invocationCandidateSHA256(input.Candidate)
	releaseDigest, releaseOK := canonicalSHA256(input.Release)
	verificationDigest, verificationOK := canonicalSHA256(input.ProvenanceVerification)
	planDigest, planOK := canonicalSHA256(input.LaunchPlan)
	reviewDigest, reviewOK := canonicalSHA256(input.LaunchPlanReview)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	if !threatOK || !candidateOK || !releaseOK || !verificationOK || !planOK ||
		!reviewOK || !approvalOK {
		return AnalyzerProductAdapterAdmissionMatrix{}, CodeInternal
	}

	controls := make([]AnalyzerProductAdapterAdmissionControl, 0, len(input.ThreatModel.Controls))
	candidateCount := 0
	testCount := 0
	for _, required := range input.ThreatModel.Controls {
		source, present, testOnly := analyzerAdmissionEvidence(required.ID)
		status := ProductAdapterControlStatusRequired
		if testOnly {
			status = AnalyzerAdmissionStatusTestConformance
		} else if present {
			status = AnalyzerAdmissionStatusCandidateMetadata
		}
		if present {
			candidateCount++
		}
		if testOnly {
			testCount++
		}
		controls = append(controls, AnalyzerProductAdapterAdmissionControl{
			ControlID: required.ID, EvidenceSource: source, Status: status,
			CandidateEvidencePresent: present, TestConformanceOnly: testOnly,
			ProductionEvidenceRequired: true, BlocksProductStart: true,
		})
	}
	matrix := AnalyzerProductAdapterAdmissionMatrix{
		ProtocolVersion:   AnalyzerProductAdapterAdmissionProtocolVersion,
		ThreatModelSHA256: threatDigest, CandidateSHA256: candidateDigest,
		ReleaseCandidateSHA256:       releaseDigest,
		ProvenanceVerificationSHA256: verificationDigest,
		LaunchPlanSHA256:             planDigest, LaunchPlanReviewSHA256: reviewDigest,
		ScopeApprovalSHA256: approvalDigest, RequestID: input.Candidate.RequestID,
		Analyzer: input.Candidate.Analyzer, TargetGOOS: input.FormatEvidence.TargetGOOS,
		TargetGOARCH:     input.FormatEvidence.TargetGOARCH,
		ExecutableSHA256: input.FormatEvidence.ExecutableSHA256, Controls: controls,
		RequiredControlCount: len(controls), CandidateEvidenceCount: candidateCount,
		TestConformanceCount: testCount, OpenRequirementCount: len(controls),
		ExactEvidenceBound: true, AllControlsRequired: true, StartBlocked: true,
		MetadataOnly: true,
	}
	if !validateAnalyzerProductAdapterAdmissionStructure(matrix, input) {
		return AnalyzerProductAdapterAdmissionMatrix{}, CodeInternal
	}
	return matrix, ""
}

func ValidateAnalyzerProductAdapterAdmissionMatrix(matrix AnalyzerProductAdapterAdmissionMatrix,
	input AnalyzerProductAdapterEvidenceInput,
) ErrorCode {
	expected, code := BuildAnalyzerProductAdapterAdmissionMatrix(input)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(matrix, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerProductAdapterAdmissionMatrix(matrix AnalyzerProductAdapterAdmissionMatrix,
	input AnalyzerProductAdapterEvidenceInput,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerProductAdapterAdmissionMatrix(matrix, input); code != "" {
		return nil, code
	}
	encoded, err := json.Marshal(matrix)
	if err != nil || len(encoded) == 0 || len(encoded) > MaxAnalyzerProductAdapterAdmissionEnvelopeBytes {
		return nil, CodeInternal
	}
	return encoded, ""
}

func DecodeAnalyzerProductAdapterAdmissionMatrix(raw []byte,
	input AnalyzerProductAdapterEvidenceInput,
) (AnalyzerProductAdapterAdmissionMatrix, ErrorCode) {
	var wire analyzerProductAdapterAdmissionMatrixWire
	if !strictDecode(raw, MaxAnalyzerProductAdapterAdmissionEnvelopeBytes, &wire) || !wire.complete() {
		return AnalyzerProductAdapterAdmissionMatrix{}, CodeInvalidResult
	}
	matrix := wire.value()
	if code := ValidateAnalyzerProductAdapterAdmissionMatrix(matrix, input); code != "" {
		return AnalyzerProductAdapterAdmissionMatrix{}, CodeInvalidResult
	}
	return matrix, ""
}

func analyzerAdmissionEvidence(controlID string) (string, bool, bool) {
	switch controlID {
	case "executable_handle_identity":
		return "analyzer_immutable_handle_conformance.test.v1", true, true
	case "executable_format", "target_architecture":
		return ExecutableFormatEvidenceProtocolVersion, true, false
	case "provenance_signature":
		return AnalyzerProvenanceVerificationProtocolVersion, true, false
	case "version_allowlist":
		return AnalyzerReleaseCandidateProtocolVersion, true, false
	case "least_privilege_identity":
		return "analyzer_low_privilege_context_conformance.test.v1", true, true
	case "filesystem_sandbox":
		return "analyzer_filesystem_boundary_conformance.test.v1", true, true
	case "network_isolation", "environment_scrubbing", "cpu_limit", "memory_limit",
		"process_count_limit", "wall_clock_deadline", "process_tree_termination":
		return "analyzer_sandbox_enforcement_conformance.test.v1", true, true
	case "bounded_stdio_redaction":
		return "analyzer_subprocess_conformance.test.v1", true, true
	case "operator_scope_approval":
		return AnalyzerScopeLimitsApprovalProtocolVersion, true, false
	case "atomic_result_handoff":
		return "analyzer_result_staging_conformance.test.v1", true, true
	case "orphan_rollback_reconciliation":
		return "analyzer_sandbox_recovery_conformance.test.v1", true, true
	case "durable_intent_recovery", "append_only_audit":
		return AnalyzerAdmissionEvidenceSourceMissing, false, false
	default:
		return AnalyzerAdmissionEvidenceSourceMissing, false, false
	}
}

func validateAnalyzerProductAdapterAdmissionStructure(matrix AnalyzerProductAdapterAdmissionMatrix,
	input AnalyzerProductAdapterEvidenceInput,
) bool {
	threatDigest, threatOK := canonicalSHA256(input.ThreatModel)
	candidateDigest, candidateOK := invocationCandidateSHA256(input.Candidate)
	releaseDigest, releaseOK := canonicalSHA256(input.Release)
	verificationDigest, verificationOK := canonicalSHA256(input.ProvenanceVerification)
	planDigest, planOK := canonicalSHA256(input.LaunchPlan)
	reviewDigest, reviewOK := canonicalSHA256(input.LaunchPlanReview)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	return threatOK && candidateOK && releaseOK && verificationOK && planOK && reviewOK &&
		approvalOK && matrix.ProtocolVersion == AnalyzerProductAdapterAdmissionProtocolVersion &&
		matrix.ThreatModelSHA256 == threatDigest && matrix.CandidateSHA256 == candidateDigest &&
		matrix.ReleaseCandidateSHA256 == releaseDigest &&
		matrix.ProvenanceVerificationSHA256 == verificationDigest &&
		matrix.LaunchPlanSHA256 == planDigest && matrix.LaunchPlanReviewSHA256 == reviewDigest &&
		matrix.ScopeApprovalSHA256 == approvalDigest && matrix.RequestID == input.Candidate.RequestID &&
		matrix.Analyzer == input.Candidate.Analyzer && matrix.TargetGOOS == input.FormatEvidence.TargetGOOS &&
		matrix.TargetGOARCH == input.FormatEvidence.TargetGOARCH &&
		matrix.ExecutableSHA256 == input.FormatEvidence.ExecutableSHA256 &&
		len(matrix.Controls) == len(input.ThreatModel.Controls) &&
		matrix.RequiredControlCount == len(matrix.Controls) && matrix.CandidateEvidenceCount == 18 &&
		matrix.TestConformanceCount == 13 && matrix.ProductionVerifiedCount == 0 &&
		matrix.OpenRequirementCount == len(matrix.Controls) && matrix.ExactEvidenceBound &&
		matrix.AllControlsRequired && !matrix.AllProductionEvidenceVerified && !matrix.AdmissionReady &&
		matrix.StartBlocked && matrix.MetadataOnly && !matrix.ProductAdapterPresent &&
		!matrix.ProcessStarterPresent && matrix.Authority == (AnalyzerProductAdapterAuthority{})
}

type analyzerProductAdapterAuthorityWire struct {
	ProductInvocation *bool `json:"product_invocation"`
	ProcessStart      *bool `json:"process_start"`
	Execution         *bool `json:"execution"`
	CapabilityIssue   *bool `json:"capability_issue"`
	RecoveryApply     *bool `json:"recovery_apply"`
	Persistence       *bool `json:"persistence"`
	ArtifactCommit    *bool `json:"artifact_commit"`
	Network           *bool `json:"network"`
	HostFilesystem    *bool `json:"host_filesystem"`
	SecretAccess      *bool `json:"secret_access"`
	OperatorOverride  *bool `json:"operator_override"`
}

func (wire analyzerProductAdapterAuthorityWire) complete() bool {
	return wire.ProductInvocation != nil && wire.ProcessStart != nil && wire.Execution != nil &&
		wire.CapabilityIssue != nil && wire.RecoveryApply != nil && wire.Persistence != nil &&
		wire.ArtifactCommit != nil && wire.Network != nil && wire.HostFilesystem != nil &&
		wire.SecretAccess != nil && wire.OperatorOverride != nil
}

func (wire analyzerProductAdapterAuthorityWire) value() AnalyzerProductAdapterAuthority {
	return AnalyzerProductAdapterAuthority{
		ProductInvocation: *wire.ProductInvocation, ProcessStart: *wire.ProcessStart,
		Execution: *wire.Execution, CapabilityIssue: *wire.CapabilityIssue,
		RecoveryApply: *wire.RecoveryApply, Persistence: *wire.Persistence,
		ArtifactCommit: *wire.ArtifactCommit, Network: *wire.Network,
		HostFilesystem: *wire.HostFilesystem, SecretAccess: *wire.SecretAccess,
		OperatorOverride: *wire.OperatorOverride,
	}
}

type analyzerProductAdapterAdmissionControlWire struct {
	ControlID                  *string `json:"control_id"`
	EvidenceSource             *string `json:"evidence_source"`
	Status                     *string `json:"status"`
	CandidateEvidencePresent   *bool   `json:"candidate_evidence_present"`
	TestConformanceOnly        *bool   `json:"test_conformance_only"`
	ProductionEvidenceRequired *bool   `json:"production_evidence_required"`
	ProductionEvidenceVerified *bool   `json:"production_evidence_verified"`
	BlocksProductStart         *bool   `json:"blocks_product_start"`
}

func (wire analyzerProductAdapterAdmissionControlWire) complete() bool {
	return wire.ControlID != nil && wire.EvidenceSource != nil && wire.Status != nil &&
		wire.CandidateEvidencePresent != nil && wire.TestConformanceOnly != nil &&
		wire.ProductionEvidenceRequired != nil && wire.ProductionEvidenceVerified != nil &&
		wire.BlocksProductStart != nil
}

func (wire analyzerProductAdapterAdmissionControlWire) value() AnalyzerProductAdapterAdmissionControl {
	return AnalyzerProductAdapterAdmissionControl{
		ControlID: *wire.ControlID, EvidenceSource: *wire.EvidenceSource, Status: *wire.Status,
		CandidateEvidencePresent:   *wire.CandidateEvidencePresent,
		TestConformanceOnly:        *wire.TestConformanceOnly,
		ProductionEvidenceRequired: *wire.ProductionEvidenceRequired,
		ProductionEvidenceVerified: *wire.ProductionEvidenceVerified,
		BlocksProductStart:         *wire.BlocksProductStart,
	}
}

type analyzerProductAdapterAdmissionMatrixWire struct {
	ProtocolVersion               *string                                       `json:"protocol_version"`
	ThreatModelSHA256             *string                                       `json:"threat_model_sha256"`
	CandidateSHA256               *string                                       `json:"candidate_sha256"`
	ReleaseCandidateSHA256        *string                                       `json:"release_candidate_sha256"`
	ProvenanceVerificationSHA256  *string                                       `json:"provenance_verification_sha256"`
	LaunchPlanSHA256              *string                                       `json:"launch_plan_sha256"`
	LaunchPlanReviewSHA256        *string                                       `json:"launch_plan_review_sha256"`
	ScopeApprovalSHA256           *string                                       `json:"scope_approval_sha256"`
	RequestID                     *string                                       `json:"request_id"`
	Analyzer                      *string                                       `json:"analyzer"`
	TargetGOOS                    *string                                       `json:"target_goos"`
	TargetGOARCH                  *string                                       `json:"target_goarch"`
	ExecutableSHA256              *string                                       `json:"executable_sha256"`
	Controls                      *[]analyzerProductAdapterAdmissionControlWire `json:"controls"`
	RequiredControlCount          *int                                          `json:"required_control_count"`
	CandidateEvidenceCount        *int                                          `json:"candidate_evidence_count"`
	TestConformanceCount          *int                                          `json:"test_conformance_count"`
	ProductionVerifiedCount       *int                                          `json:"production_verified_count"`
	OpenRequirementCount          *int                                          `json:"open_requirement_count"`
	ExactEvidenceBound            *bool                                         `json:"exact_evidence_bound"`
	AllControlsRequired           *bool                                         `json:"all_controls_required"`
	AllProductionEvidenceVerified *bool                                         `json:"all_production_evidence_verified"`
	AdmissionReady                *bool                                         `json:"admission_ready"`
	StartBlocked                  *bool                                         `json:"start_blocked"`
	MetadataOnly                  *bool                                         `json:"metadata_only"`
	ProductAdapterPresent         *bool                                         `json:"product_adapter_present"`
	ProcessStarterPresent         *bool                                         `json:"process_starter_present"`
	Authority                     *analyzerProductAdapterAuthorityWire          `json:"authority"`
}

func (wire analyzerProductAdapterAdmissionMatrixWire) complete() bool {
	if wire.ProtocolVersion == nil || wire.ThreatModelSHA256 == nil || wire.CandidateSHA256 == nil ||
		wire.ReleaseCandidateSHA256 == nil || wire.ProvenanceVerificationSHA256 == nil ||
		wire.LaunchPlanSHA256 == nil || wire.LaunchPlanReviewSHA256 == nil ||
		wire.ScopeApprovalSHA256 == nil || wire.RequestID == nil || wire.Analyzer == nil ||
		wire.TargetGOOS == nil || wire.TargetGOARCH == nil || wire.ExecutableSHA256 == nil ||
		wire.Controls == nil || wire.RequiredControlCount == nil || wire.CandidateEvidenceCount == nil ||
		wire.TestConformanceCount == nil || wire.ProductionVerifiedCount == nil ||
		wire.OpenRequirementCount == nil || wire.ExactEvidenceBound == nil ||
		wire.AllControlsRequired == nil || wire.AllProductionEvidenceVerified == nil ||
		wire.AdmissionReady == nil || wire.StartBlocked == nil || wire.MetadataOnly == nil ||
		wire.ProductAdapterPresent == nil || wire.ProcessStarterPresent == nil ||
		wire.Authority == nil || !wire.Authority.complete() {
		return false
	}
	for _, control := range *wire.Controls {
		if !control.complete() {
			return false
		}
	}
	return true
}

func (wire analyzerProductAdapterAdmissionMatrixWire) value() AnalyzerProductAdapterAdmissionMatrix {
	controls := make([]AnalyzerProductAdapterAdmissionControl, len(*wire.Controls))
	for index, control := range *wire.Controls {
		controls[index] = control.value()
	}
	return AnalyzerProductAdapterAdmissionMatrix{
		ProtocolVersion: *wire.ProtocolVersion, ThreatModelSHA256: *wire.ThreatModelSHA256,
		CandidateSHA256: *wire.CandidateSHA256, ReleaseCandidateSHA256: *wire.ReleaseCandidateSHA256,
		ProvenanceVerificationSHA256: *wire.ProvenanceVerificationSHA256,
		LaunchPlanSHA256:             *wire.LaunchPlanSHA256, LaunchPlanReviewSHA256: *wire.LaunchPlanReviewSHA256,
		ScopeApprovalSHA256: *wire.ScopeApprovalSHA256, RequestID: *wire.RequestID,
		Analyzer: *wire.Analyzer, TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableSHA256: *wire.ExecutableSHA256, Controls: controls,
		RequiredControlCount:    *wire.RequiredControlCount,
		CandidateEvidenceCount:  *wire.CandidateEvidenceCount,
		TestConformanceCount:    *wire.TestConformanceCount,
		ProductionVerifiedCount: *wire.ProductionVerifiedCount,
		OpenRequirementCount:    *wire.OpenRequirementCount, ExactEvidenceBound: *wire.ExactEvidenceBound,
		AllControlsRequired:           *wire.AllControlsRequired,
		AllProductionEvidenceVerified: *wire.AllProductionEvidenceVerified,
		AdmissionReady:                *wire.AdmissionReady, StartBlocked: *wire.StartBlocked,
		MetadataOnly: *wire.MetadataOnly, ProductAdapterPresent: *wire.ProductAdapterPresent,
		ProcessStarterPresent: *wire.ProcessStarterPresent, Authority: wire.Authority.value(),
	}
}
