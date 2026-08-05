package analyzer

import (
	"encoding/json"
	"reflect"
)

const (
	AnalyzerEmbeddedWASIOwnershipProtocolVersion       = "analyzer_embedded_wasi_invocation_ownership.v1"
	AnalyzerEmbeddedWASIReleaseDecisionProtocolVersion = "analyzer_embedded_wasi_release_decision.v1"
	AnalyzerEmbeddedWASIReleaseGateStatusRequired      = "required_unimplemented"
	MaxAnalyzerEmbeddedWASIOwnershipEnvelopeBytes      = 16 * 1024
	MaxAnalyzerEmbeddedWASIReleaseEnvelopeBytes        = 24 * 1024
)

// AnalyzerEmbeddedWASIOwnership fixes future lifecycle and recovery ownership
// without creating a guest instance or a product route.
type AnalyzerEmbeddedWASIOwnership struct {
	ProtocolVersion             string                          `json:"protocol_version"`
	ProfileSHA256               string                          `json:"profile_sha256"`
	AssessmentSHA256            string                          `json:"assessment_sha256"`
	ModuleSHA256                string                          `json:"module_sha256"`
	RuntimeOwner                string                          `json:"runtime_owner"`
	CompiledModuleOwner         string                          `json:"compiled_module_owner"`
	GuestInstanceOwner          string                          `json:"guest_instance_owner"`
	DeadlineOwner               string                          `json:"deadline_owner"`
	RecoveryOwner               string                          `json:"recovery_owner"`
	InputOwnership              string                          `json:"input_ownership"`
	OutputOwnership             string                          `json:"output_ownership"`
	CloseOrder                  []string                        `json:"close_order"`
	RuntimePerInvocation        bool                            `json:"runtime_per_invocation"`
	CompiledModulePerInvocation bool                            `json:"compiled_module_per_invocation"`
	GuestInstancePerInvocation  bool                            `json:"guest_instance_per_invocation"`
	CrossRunReuse               bool                            `json:"cross_run_reuse"`
	NativeProcessPresent        bool                            `json:"native_process_present"`
	PIDPresent                  bool                            `json:"pid_present"`
	ProcessTreePresent          bool                            `json:"process_tree_present"`
	BackgroundGuestAllowed      bool                            `json:"background_guest_allowed"`
	AutomaticRestartAllowed     bool                            `json:"automatic_restart_allowed"`
	ForeignResourceCleanup      bool                            `json:"foreign_resource_cleanup"`
	ContextDeadlineRequired     bool                            `json:"context_deadline_required"`
	ContextCancellationCloses   bool                            `json:"context_cancellation_closes"`
	HostCrashLeavesGuest        bool                            `json:"host_crash_leaves_guest"`
	ConsumedRequestAutoReplay   bool                            `json:"consumed_request_auto_replay"`
	RetryRequiresSignedRequest  bool                            `json:"retry_requires_signed_request"`
	RecoveryMetadataOnly        bool                            `json:"recovery_metadata_only"`
	ArtifactCommitAuthorized    bool                            `json:"artifact_commit_authorized"`
	CandidateOnly               bool                            `json:"candidate_only"`
	DefaultDeny                 bool                            `json:"default_deny"`
	StartBlocked                bool                            `json:"start_blocked"`
	ProductInvocationAuthorized bool                            `json:"product_invocation_authorized"`
	Authority                   AnalyzerProductAdapterAuthority `json:"authority"`
	Fingerprint                 string                          `json:"fingerprint"`
}

type AnalyzerEmbeddedWASIReleaseGate struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	Required           bool   `json:"required"`
	Implemented        bool   `json:"implemented"`
	Verified           bool   `json:"verified"`
	BlocksProductStart bool   `json:"blocks_product_start"`
}

// AnalyzerEmbeddedWASIReleaseDecision is deliberately non-starting. Passing
// compile-time assessment does not satisfy execution or product-route gates.
type AnalyzerEmbeddedWASIReleaseDecision struct {
	ProtocolVersion             string                            `json:"protocol_version"`
	ProfileSHA256               string                            `json:"profile_sha256"`
	AssessmentSHA256            string                            `json:"assessment_sha256"`
	OwnershipSHA256             string                            `json:"ownership_sha256"`
	ModuleSHA256                string                            `json:"module_sha256"`
	Gates                       []AnalyzerEmbeddedWASIReleaseGate `json:"gates"`
	RequiredGateCount           int                               `json:"required_gate_count"`
	ImplementedGateCount        int                               `json:"implemented_gate_count"`
	VerifiedGateCount           int                               `json:"verified_gate_count"`
	OpenGateCount               int                               `json:"open_gate_count"`
	NonStartingDecision         bool                              `json:"non_starting_decision"`
	Ready                       bool                              `json:"ready"`
	CandidateOnly               bool                              `json:"candidate_only"`
	DefaultDeny                 bool                              `json:"default_deny"`
	StartBlocked                bool                              `json:"start_blocked"`
	ProductInvocationAuthorized bool                              `json:"product_invocation_authorized"`
	Authority                   AnalyzerProductAdapterAuthority   `json:"authority"`
	Fingerprint                 string                            `json:"fingerprint"`
}

func BuildAnalyzerEmbeddedWASIOwnership(
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
) (AnalyzerEmbeddedWASIOwnership, ErrorCode) {
	if ValidateAnalyzerEmbeddedWASIProfile(profile) != "" ||
		ValidateAnalyzerEmbeddedWASIAssessment(assessment, profile) != "" {
		return AnalyzerEmbeddedWASIOwnership{}, CodeInvalidResult
	}
	if !assessment.Passed {
		return AnalyzerEmbeddedWASIOwnership{}, CodeCapabilityDenied
	}
	profileDigest, profileOK := canonicalSHA256(profile)
	assessmentDigest, assessmentOK := canonicalSHA256(assessment)
	if !profileOK || !assessmentOK {
		return AnalyzerEmbeddedWASIOwnership{}, CodeInternal
	}
	value := AnalyzerEmbeddedWASIOwnership{
		ProtocolVersion:             AnalyzerEmbeddedWASIOwnershipProtocolVersion,
		ProfileSHA256:               profileDigest,
		AssessmentSHA256:            assessmentDigest,
		ModuleSHA256:                assessment.ModuleSHA256,
		RuntimeOwner:                "go_invocation_scope",
		CompiledModuleOwner:         "go_invocation_scope",
		GuestInstanceOwner:          "go_invocation_scope_future_gate",
		DeadlineOwner:               "go_run_supervisor",
		RecoveryOwner:               "go_metadata_reconciler",
		InputOwnership:              "caller_owned_bounded_memory",
		OutputOwnership:             "go_owned_bounded_memory",
		CloseOrder:                  []string{"guest_instance", "compiled_module", "runtime"},
		RuntimePerInvocation:        true,
		CompiledModulePerInvocation: true,
		GuestInstancePerInvocation:  true,
		ContextDeadlineRequired:     true,
		ContextCancellationCloses:   true,
		RetryRequiresSignedRequest:  true,
		RecoveryMetadataOnly:        true,
		CandidateOnly:               true,
		DefaultDeny:                 true,
		StartBlocked:                true,
	}
	value.Fingerprint = analyzerStartFingerprint(value)
	return value, ""
}

func ValidateAnalyzerEmbeddedWASIOwnership(
	value AnalyzerEmbeddedWASIOwnership,
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
) ErrorCode {
	expected, code := BuildAnalyzerEmbeddedWASIOwnership(profile, assessment)
	if code != "" || !reflect.DeepEqual(value, expected) {
		return CodeInvalidResult
	}
	return ""
}

func BuildAnalyzerEmbeddedWASIReleaseDecision(
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
	ownership AnalyzerEmbeddedWASIOwnership,
) (AnalyzerEmbeddedWASIReleaseDecision, ErrorCode) {
	if ValidateAnalyzerEmbeddedWASIOwnership(ownership, profile, assessment) != "" {
		return AnalyzerEmbeddedWASIReleaseDecision{}, CodeInvalidResult
	}
	profileDigest, profileOK := canonicalSHA256(profile)
	assessmentDigest, assessmentOK := canonicalSHA256(assessment)
	ownershipDigest, ownershipOK := canonicalSHA256(ownership)
	if !profileOK || !assessmentOK || !ownershipOK {
		return AnalyzerEmbeddedWASIReleaseDecision{}, CodeInternal
	}
	gateIDs := []string{
		"runtime_execution_conformance",
		"bounded_stdio_execution",
		"deadline_close_observation",
		"result_validation_handoff",
		"capability_issue_and_consume",
		"production_evidence_acceptance",
		"product_route_review",
	}
	gates := make([]AnalyzerEmbeddedWASIReleaseGate, len(gateIDs))
	for index, id := range gateIDs {
		gates[index] = AnalyzerEmbeddedWASIReleaseGate{
			ID: id, Status: AnalyzerEmbeddedWASIReleaseGateStatusRequired,
			Required: true, BlocksProductStart: true,
		}
	}
	value := AnalyzerEmbeddedWASIReleaseDecision{
		ProtocolVersion:     AnalyzerEmbeddedWASIReleaseDecisionProtocolVersion,
		ProfileSHA256:       profileDigest,
		AssessmentSHA256:    assessmentDigest,
		OwnershipSHA256:     ownershipDigest,
		ModuleSHA256:        assessment.ModuleSHA256,
		Gates:               gates,
		RequiredGateCount:   len(gates),
		OpenGateCount:       len(gates),
		NonStartingDecision: true,
		CandidateOnly:       true,
		DefaultDeny:         true,
		StartBlocked:        true,
	}
	value.Fingerprint = analyzerStartFingerprint(value)
	return value, ""
}

func ValidateAnalyzerEmbeddedWASIReleaseDecision(
	value AnalyzerEmbeddedWASIReleaseDecision,
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
	ownership AnalyzerEmbeddedWASIOwnership,
) ErrorCode {
	expected, code := BuildAnalyzerEmbeddedWASIReleaseDecision(profile, assessment, ownership)
	if code != "" || !reflect.DeepEqual(value, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerEmbeddedWASIOwnership(
	value AnalyzerEmbeddedWASIOwnership,
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
) ([]byte, ErrorCode) {
	if ValidateAnalyzerEmbeddedWASIOwnership(value, profile, assessment) != "" {
		return nil, CodeInvalidResult
	}
	return encodeAnalyzerEmbeddedWASIValue(value, MaxAnalyzerEmbeddedWASIOwnershipEnvelopeBytes)
}

func DecodeAnalyzerEmbeddedWASIOwnership(
	raw []byte,
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
) (AnalyzerEmbeddedWASIOwnership, ErrorCode) {
	var value AnalyzerEmbeddedWASIOwnership
	if code := decodeAnalyzerEmbeddedWASIValue(raw, MaxAnalyzerEmbeddedWASIOwnershipEnvelopeBytes, &value); code != "" ||
		ValidateAnalyzerEmbeddedWASIOwnership(value, profile, assessment) != "" {
		return AnalyzerEmbeddedWASIOwnership{}, CodeInvalidResult
	}
	return value, ""
}

func EncodeAnalyzerEmbeddedWASIReleaseDecision(
	value AnalyzerEmbeddedWASIReleaseDecision,
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
	ownership AnalyzerEmbeddedWASIOwnership,
) ([]byte, ErrorCode) {
	if ValidateAnalyzerEmbeddedWASIReleaseDecision(value, profile, assessment, ownership) != "" {
		return nil, CodeInvalidResult
	}
	return encodeAnalyzerEmbeddedWASIValue(value, MaxAnalyzerEmbeddedWASIReleaseEnvelopeBytes)
}

func DecodeAnalyzerEmbeddedWASIReleaseDecision(
	raw []byte,
	profile AnalyzerEmbeddedWASIProfile,
	assessment AnalyzerEmbeddedWASIAssessment,
	ownership AnalyzerEmbeddedWASIOwnership,
) (AnalyzerEmbeddedWASIReleaseDecision, ErrorCode) {
	var value AnalyzerEmbeddedWASIReleaseDecision
	if code := decodeAnalyzerEmbeddedWASIValue(raw, MaxAnalyzerEmbeddedWASIReleaseEnvelopeBytes, &value); code != "" ||
		ValidateAnalyzerEmbeddedWASIReleaseDecision(value, profile, assessment, ownership) != "" {
		return AnalyzerEmbeddedWASIReleaseDecision{}, CodeInvalidResult
	}
	return value, ""
}

func encodeAnalyzerEmbeddedWASIValue(value any, maximum int) ([]byte, ErrorCode) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximum {
		return nil, CodeInternal
	}
	return encoded, ""
}

func decodeAnalyzerEmbeddedWASIValue(raw []byte, maximum int, target any) ErrorCode {
	if !strictDecode(raw, maximum, target) {
		return CodeInvalidResult
	}
	expectedRaw, err := json.Marshal(target)
	if err != nil {
		return CodeInternal
	}
	var actual, expected any
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(expectedRaw, &expected) != nil ||
		!sameAnalyzerStartJSONShape(actual, expected) {
		return CodeInvalidResult
	}
	return ""
}
