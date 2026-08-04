package analyzer

import (
	"encoding/json"
	"reflect"
)

const (
	AnalyzerProductAdapterRecoveryAcceptanceProtocolVersion  = "analyzer_product_adapter_recovery_acceptance.v1"
	MaxAnalyzerProductAdapterRecoveryAcceptanceEnvelopeBytes = 24 * 1024
)

// AnalyzerProductAdapterRecoveryScenario defines one mandatory restart or
// failure outcome. It is an acceptance requirement, not an applied cleanup.
type AnalyzerProductAdapterRecoveryScenario struct {
	ID                            string `json:"id"`
	Trigger                       string `json:"trigger"`
	RequiredDisposition           string `json:"required_disposition"`
	WriteAheadIntentRequired      bool   `json:"write_ahead_intent_required"`
	GenerationFenceRequired       bool   `json:"generation_fence_required"`
	ExactProcessIdentityRequired  bool   `json:"exact_process_identity_required"`
	ProcessTreeQuiescenceRequired bool   `json:"process_tree_quiescence_required"`
	NoReplaceHandoffRequired      bool   `json:"no_replace_handoff_required"`
	ForeignResourcesProtected     bool   `json:"foreign_resources_protected"`
	IdempotentReplayRequired      bool   `json:"idempotent_replay_required"`
	ProductionEvidenceVerified    bool   `json:"production_evidence_verified"`
	BlocksProductStart            bool   `json:"blocks_product_start"`
}

// AnalyzerProductAdapterRecoveryAcceptance binds recovery requirements to one
// authenticated but unissued start-capability contract. No lifecycle store,
// cleanup executor, product starter, or recovery authority is introduced.
type AnalyzerProductAdapterRecoveryAcceptance struct {
	ProtocolVersion                 string                                   `json:"protocol_version"`
	AdmissionMatrixSHA256           string                                   `json:"admission_matrix_sha256"`
	CapabilityRequestSHA256         string                                   `json:"capability_request_sha256"`
	CapabilityContractSHA256        string                                   `json:"capability_contract_sha256"`
	ScopeApprovalSHA256             string                                   `json:"scope_approval_sha256"`
	RequestID                       string                                   `json:"request_id"`
	Analyzer                        string                                   `json:"analyzer"`
	TargetGOOS                      string                                   `json:"target_goos"`
	TargetGOARCH                    string                                   `json:"target_goarch"`
	ExecutableSHA256                string                                   `json:"executable_sha256"`
	Scenarios                       []AnalyzerProductAdapterRecoveryScenario `json:"scenarios"`
	RequiredScenarioCount           int                                      `json:"required_scenario_count"`
	ProductionVerifiedCount         int                                      `json:"production_verified_count"`
	OpenScenarioCount               int                                      `json:"open_scenario_count"`
	ExactContractsBound             bool                                     `json:"exact_contracts_bound"`
	AllScenariosRequired            bool                                     `json:"all_scenarios_required"`
	ProductionAcceptanceComplete    bool                                     `json:"production_acceptance_complete"`
	WriteAheadIntentPresent         bool                                     `json:"write_ahead_intent_present"`
	DurableGenerationFencePresent   bool                                     `json:"durable_generation_fence_present"`
	PersistentLifecycleStorePresent bool                                     `json:"persistent_lifecycle_store_present"`
	CleanupExecutorPresent          bool                                     `json:"cleanup_executor_present"`
	RecoveryReady                   bool                                     `json:"recovery_ready"`
	StartBlocked                    bool                                     `json:"start_blocked"`
	ApplyBlocked                    bool                                     `json:"apply_blocked"`
	MetadataOnly                    bool                                     `json:"metadata_only"`
	ProductAdapterPresent           bool                                     `json:"product_adapter_present"`
	ProcessStarterPresent           bool                                     `json:"process_starter_present"`
	Authority                       AnalyzerProductAdapterAuthority          `json:"authority"`
}

func BuildAnalyzerProductAdapterRecoveryAcceptance(input AnalyzerProductAdapterEvidenceInput,
	matrix AnalyzerProductAdapterAdmissionMatrix,
	request AnalyzerOperatorStartCapabilityRequest,
	contract AnalyzerOperatorStartCapabilityContract,
	nonce, operatorPublicKey, detachedSignature []byte,
) (AnalyzerProductAdapterRecoveryAcceptance, ErrorCode) {
	if code := ValidateAnalyzerOperatorStartCapabilityContract(contract, request, input, matrix,
		nonce, operatorPublicKey, detachedSignature); code != "" {
		return AnalyzerProductAdapterRecoveryAcceptance{}, CodeInvalidResult
	}
	matrixDigest, matrixOK := canonicalSHA256(matrix)
	requestDigest, requestOK := canonicalSHA256(request)
	contractDigest, contractOK := canonicalSHA256(contract)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	if !matrixOK || !requestOK || !contractOK || !approvalOK {
		return AnalyzerProductAdapterRecoveryAcceptance{}, CodeInternal
	}
	scenarios := analyzerProductAdapterRecoveryScenarios()
	acceptance := AnalyzerProductAdapterRecoveryAcceptance{
		ProtocolVersion:       AnalyzerProductAdapterRecoveryAcceptanceProtocolVersion,
		AdmissionMatrixSHA256: matrixDigest, CapabilityRequestSHA256: requestDigest,
		CapabilityContractSHA256: contractDigest, ScopeApprovalSHA256: approvalDigest,
		RequestID: input.Candidate.RequestID, Analyzer: input.Candidate.Analyzer,
		TargetGOOS: input.FormatEvidence.TargetGOOS, TargetGOARCH: input.FormatEvidence.TargetGOARCH,
		ExecutableSHA256: input.FormatEvidence.ExecutableSHA256, Scenarios: scenarios,
		RequiredScenarioCount: len(scenarios), OpenScenarioCount: len(scenarios),
		ExactContractsBound: true, AllScenariosRequired: true, StartBlocked: true,
		ApplyBlocked: true, MetadataOnly: true,
	}
	if !validateAnalyzerProductAdapterRecoveryAcceptanceStructure(acceptance, input, matrix,
		request, contract) {
		return AnalyzerProductAdapterRecoveryAcceptance{}, CodeInternal
	}
	return acceptance, ""
}

func ValidateAnalyzerProductAdapterRecoveryAcceptance(
	acceptance AnalyzerProductAdapterRecoveryAcceptance,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	request AnalyzerOperatorStartCapabilityRequest,
	contract AnalyzerOperatorStartCapabilityContract,
	nonce, operatorPublicKey, detachedSignature []byte,
) ErrorCode {
	expected, code := BuildAnalyzerProductAdapterRecoveryAcceptance(input, matrix, request,
		contract, nonce, operatorPublicKey, detachedSignature)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(acceptance, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerProductAdapterRecoveryAcceptance(
	acceptance AnalyzerProductAdapterRecoveryAcceptance,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	request AnalyzerOperatorStartCapabilityRequest,
	contract AnalyzerOperatorStartCapabilityContract,
	nonce, operatorPublicKey, detachedSignature []byte,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerProductAdapterRecoveryAcceptance(acceptance, input, matrix,
		request, contract, nonce, operatorPublicKey, detachedSignature); code != "" {
		return nil, code
	}
	encoded, err := json.Marshal(acceptance)
	if err != nil || len(encoded) == 0 ||
		len(encoded) > MaxAnalyzerProductAdapterRecoveryAcceptanceEnvelopeBytes {
		return nil, CodeInternal
	}
	return encoded, ""
}

func DecodeAnalyzerProductAdapterRecoveryAcceptance(raw []byte,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	request AnalyzerOperatorStartCapabilityRequest,
	contract AnalyzerOperatorStartCapabilityContract,
	nonce, operatorPublicKey, detachedSignature []byte,
) (AnalyzerProductAdapterRecoveryAcceptance, ErrorCode) {
	var wire analyzerProductAdapterRecoveryAcceptanceWire
	if !strictDecode(raw, MaxAnalyzerProductAdapterRecoveryAcceptanceEnvelopeBytes, &wire) ||
		!wire.complete() {
		return AnalyzerProductAdapterRecoveryAcceptance{}, CodeInvalidResult
	}
	acceptance := wire.value()
	if code := ValidateAnalyzerProductAdapterRecoveryAcceptance(acceptance, input, matrix,
		request, contract, nonce, operatorPublicKey, detachedSignature); code != "" {
		return AnalyzerProductAdapterRecoveryAcceptance{}, CodeInvalidResult
	}
	return acceptance, ""
}

func analyzerProductAdapterRecoveryScenarios() []AnalyzerProductAdapterRecoveryScenario {
	return []AnalyzerProductAdapterRecoveryScenario{
		analyzerRecoveryScenario("intent_committed_before_start", "durable_intent_without_start_submission",
			"cleanup_exact_owned_staging_then_record_terminal_failure", true, true, false, false, false),
		analyzerRecoveryScenario("start_submitted_identity_unknown", "start_submission_without_exact_process_identity",
			"quarantine_and_fail_closed_without_foreign_cleanup", true, true, true, true, false),
		analyzerRecoveryScenario("running_deadline", "deadline_reached_while_process_tree_may_run",
			"terminate_exact_tree_prove_quiescence_then_fail", true, true, true, true, false),
		analyzerRecoveryScenario("operator_cancellation", "operator_cancellation_while_process_tree_may_run",
			"terminate_exact_tree_prove_quiescence_then_cancel", true, true, true, true, false),
		analyzerRecoveryScenario("crash_before_result_publish", "restart_with_private_staging_and_no_published_result",
			"reconcile_private_staging_and_exact_process_tree", true, true, true, true, true),
		analyzerRecoveryScenario("crash_after_result_publish", "restart_after_digest_bound_result_publish",
			"preserve_published_result_and_close_exact_intent", true, true, false, false, true),
		analyzerRecoveryScenario("orphan_process_tree", "owned_process_tree_survives_coordinator",
			"terminate_exact_owned_tree_and_prove_quiescence", true, true, true, true, false),
		analyzerRecoveryScenario("foreign_staging_collision", "resource_identity_or_generation_does_not_match",
			"preserve_foreign_resources_and_record_terminal_failure", true, true, false, false, true),
		analyzerRecoveryScenario("replay_after_terminal", "same_request_nonce_and_generation_after_terminal_receipt",
			"return_same_terminal_receipt_without_side_effect", true, true, false, false, true),
		analyzerRecoveryScenario("stale_generation_worker", "worker_generation_is_not_current",
			"reject_stale_worker_without_side_effect", true, true, false, false, false),
	}
}

func analyzerRecoveryScenario(id, trigger, disposition string, writeAhead, generation,
	exactProcess, treeQuiescence, noReplace bool,
) AnalyzerProductAdapterRecoveryScenario {
	return AnalyzerProductAdapterRecoveryScenario{
		ID: id, Trigger: trigger, RequiredDisposition: disposition,
		WriteAheadIntentRequired: writeAhead, GenerationFenceRequired: generation,
		ExactProcessIdentityRequired:  exactProcess,
		ProcessTreeQuiescenceRequired: treeQuiescence,
		NoReplaceHandoffRequired:      noReplace, ForeignResourcesProtected: true,
		IdempotentReplayRequired: true, BlocksProductStart: true,
	}
}

func validateAnalyzerProductAdapterRecoveryAcceptanceStructure(
	acceptance AnalyzerProductAdapterRecoveryAcceptance,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	request AnalyzerOperatorStartCapabilityRequest,
	contract AnalyzerOperatorStartCapabilityContract,
) bool {
	matrixDigest, matrixOK := canonicalSHA256(matrix)
	requestDigest, requestOK := canonicalSHA256(request)
	contractDigest, contractOK := canonicalSHA256(contract)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	return matrixOK && requestOK && contractOK && approvalOK &&
		acceptance.ProtocolVersion == AnalyzerProductAdapterRecoveryAcceptanceProtocolVersion &&
		acceptance.AdmissionMatrixSHA256 == matrixDigest &&
		acceptance.CapabilityRequestSHA256 == requestDigest &&
		acceptance.CapabilityContractSHA256 == contractDigest &&
		acceptance.ScopeApprovalSHA256 == approvalDigest &&
		acceptance.RequestID == input.Candidate.RequestID &&
		acceptance.Analyzer == input.Candidate.Analyzer &&
		acceptance.TargetGOOS == input.FormatEvidence.TargetGOOS &&
		acceptance.TargetGOARCH == input.FormatEvidence.TargetGOARCH &&
		acceptance.ExecutableSHA256 == input.FormatEvidence.ExecutableSHA256 &&
		reflect.DeepEqual(acceptance.Scenarios, analyzerProductAdapterRecoveryScenarios()) &&
		acceptance.RequiredScenarioCount == 10 && acceptance.ProductionVerifiedCount == 0 &&
		acceptance.OpenScenarioCount == 10 && acceptance.ExactContractsBound &&
		acceptance.AllScenariosRequired && !acceptance.ProductionAcceptanceComplete &&
		!acceptance.WriteAheadIntentPresent && !acceptance.DurableGenerationFencePresent &&
		!acceptance.PersistentLifecycleStorePresent && !acceptance.CleanupExecutorPresent &&
		!acceptance.RecoveryReady && acceptance.StartBlocked && acceptance.ApplyBlocked &&
		acceptance.MetadataOnly && !acceptance.ProductAdapterPresent &&
		!acceptance.ProcessStarterPresent &&
		acceptance.Authority == (AnalyzerProductAdapterAuthority{})
}

type analyzerProductAdapterRecoveryScenarioWire struct {
	ID                            *string `json:"id"`
	Trigger                       *string `json:"trigger"`
	RequiredDisposition           *string `json:"required_disposition"`
	WriteAheadIntentRequired      *bool   `json:"write_ahead_intent_required"`
	GenerationFenceRequired       *bool   `json:"generation_fence_required"`
	ExactProcessIdentityRequired  *bool   `json:"exact_process_identity_required"`
	ProcessTreeQuiescenceRequired *bool   `json:"process_tree_quiescence_required"`
	NoReplaceHandoffRequired      *bool   `json:"no_replace_handoff_required"`
	ForeignResourcesProtected     *bool   `json:"foreign_resources_protected"`
	IdempotentReplayRequired      *bool   `json:"idempotent_replay_required"`
	ProductionEvidenceVerified    *bool   `json:"production_evidence_verified"`
	BlocksProductStart            *bool   `json:"blocks_product_start"`
}

func (wire analyzerProductAdapterRecoveryScenarioWire) complete() bool {
	return wire.ID != nil && wire.Trigger != nil && wire.RequiredDisposition != nil &&
		wire.WriteAheadIntentRequired != nil && wire.GenerationFenceRequired != nil &&
		wire.ExactProcessIdentityRequired != nil && wire.ProcessTreeQuiescenceRequired != nil &&
		wire.NoReplaceHandoffRequired != nil && wire.ForeignResourcesProtected != nil &&
		wire.IdempotentReplayRequired != nil && wire.ProductionEvidenceVerified != nil &&
		wire.BlocksProductStart != nil
}

func (wire analyzerProductAdapterRecoveryScenarioWire) value() AnalyzerProductAdapterRecoveryScenario {
	return AnalyzerProductAdapterRecoveryScenario{
		ID: *wire.ID, Trigger: *wire.Trigger, RequiredDisposition: *wire.RequiredDisposition,
		WriteAheadIntentRequired:      *wire.WriteAheadIntentRequired,
		GenerationFenceRequired:       *wire.GenerationFenceRequired,
		ExactProcessIdentityRequired:  *wire.ExactProcessIdentityRequired,
		ProcessTreeQuiescenceRequired: *wire.ProcessTreeQuiescenceRequired,
		NoReplaceHandoffRequired:      *wire.NoReplaceHandoffRequired,
		ForeignResourcesProtected:     *wire.ForeignResourcesProtected,
		IdempotentReplayRequired:      *wire.IdempotentReplayRequired,
		ProductionEvidenceVerified:    *wire.ProductionEvidenceVerified,
		BlocksProductStart:            *wire.BlocksProductStart,
	}
}

type analyzerProductAdapterRecoveryAcceptanceWire struct {
	ProtocolVersion                 *string                                       `json:"protocol_version"`
	AdmissionMatrixSHA256           *string                                       `json:"admission_matrix_sha256"`
	CapabilityRequestSHA256         *string                                       `json:"capability_request_sha256"`
	CapabilityContractSHA256        *string                                       `json:"capability_contract_sha256"`
	ScopeApprovalSHA256             *string                                       `json:"scope_approval_sha256"`
	RequestID                       *string                                       `json:"request_id"`
	Analyzer                        *string                                       `json:"analyzer"`
	TargetGOOS                      *string                                       `json:"target_goos"`
	TargetGOARCH                    *string                                       `json:"target_goarch"`
	ExecutableSHA256                *string                                       `json:"executable_sha256"`
	Scenarios                       *[]analyzerProductAdapterRecoveryScenarioWire `json:"scenarios"`
	RequiredScenarioCount           *int                                          `json:"required_scenario_count"`
	ProductionVerifiedCount         *int                                          `json:"production_verified_count"`
	OpenScenarioCount               *int                                          `json:"open_scenario_count"`
	ExactContractsBound             *bool                                         `json:"exact_contracts_bound"`
	AllScenariosRequired            *bool                                         `json:"all_scenarios_required"`
	ProductionAcceptanceComplete    *bool                                         `json:"production_acceptance_complete"`
	WriteAheadIntentPresent         *bool                                         `json:"write_ahead_intent_present"`
	DurableGenerationFencePresent   *bool                                         `json:"durable_generation_fence_present"`
	PersistentLifecycleStorePresent *bool                                         `json:"persistent_lifecycle_store_present"`
	CleanupExecutorPresent          *bool                                         `json:"cleanup_executor_present"`
	RecoveryReady                   *bool                                         `json:"recovery_ready"`
	StartBlocked                    *bool                                         `json:"start_blocked"`
	ApplyBlocked                    *bool                                         `json:"apply_blocked"`
	MetadataOnly                    *bool                                         `json:"metadata_only"`
	ProductAdapterPresent           *bool                                         `json:"product_adapter_present"`
	ProcessStarterPresent           *bool                                         `json:"process_starter_present"`
	Authority                       *analyzerProductAdapterAuthorityWire          `json:"authority"`
}

func (wire analyzerProductAdapterRecoveryAcceptanceWire) complete() bool {
	if wire.ProtocolVersion == nil || wire.AdmissionMatrixSHA256 == nil ||
		wire.CapabilityRequestSHA256 == nil || wire.CapabilityContractSHA256 == nil ||
		wire.ScopeApprovalSHA256 == nil || wire.RequestID == nil || wire.Analyzer == nil ||
		wire.TargetGOOS == nil || wire.TargetGOARCH == nil || wire.ExecutableSHA256 == nil ||
		wire.Scenarios == nil || wire.RequiredScenarioCount == nil ||
		wire.ProductionVerifiedCount == nil || wire.OpenScenarioCount == nil ||
		wire.ExactContractsBound == nil || wire.AllScenariosRequired == nil ||
		wire.ProductionAcceptanceComplete == nil || wire.WriteAheadIntentPresent == nil ||
		wire.DurableGenerationFencePresent == nil || wire.PersistentLifecycleStorePresent == nil ||
		wire.CleanupExecutorPresent == nil || wire.RecoveryReady == nil || wire.StartBlocked == nil ||
		wire.ApplyBlocked == nil || wire.MetadataOnly == nil || wire.ProductAdapterPresent == nil ||
		wire.ProcessStarterPresent == nil || wire.Authority == nil || !wire.Authority.complete() {
		return false
	}
	for _, scenario := range *wire.Scenarios {
		if !scenario.complete() {
			return false
		}
	}
	return true
}

func (wire analyzerProductAdapterRecoveryAcceptanceWire) value() AnalyzerProductAdapterRecoveryAcceptance {
	scenarios := make([]AnalyzerProductAdapterRecoveryScenario, len(*wire.Scenarios))
	for index, scenario := range *wire.Scenarios {
		scenarios[index] = scenario.value()
	}
	return AnalyzerProductAdapterRecoveryAcceptance{
		ProtocolVersion:          *wire.ProtocolVersion,
		AdmissionMatrixSHA256:    *wire.AdmissionMatrixSHA256,
		CapabilityRequestSHA256:  *wire.CapabilityRequestSHA256,
		CapabilityContractSHA256: *wire.CapabilityContractSHA256,
		ScopeApprovalSHA256:      *wire.ScopeApprovalSHA256, RequestID: *wire.RequestID,
		Analyzer: *wire.Analyzer, TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableSHA256: *wire.ExecutableSHA256, Scenarios: scenarios,
		RequiredScenarioCount:   *wire.RequiredScenarioCount,
		ProductionVerifiedCount: *wire.ProductionVerifiedCount,
		OpenScenarioCount:       *wire.OpenScenarioCount, ExactContractsBound: *wire.ExactContractsBound,
		AllScenariosRequired:            *wire.AllScenariosRequired,
		ProductionAcceptanceComplete:    *wire.ProductionAcceptanceComplete,
		WriteAheadIntentPresent:         *wire.WriteAheadIntentPresent,
		DurableGenerationFencePresent:   *wire.DurableGenerationFencePresent,
		PersistentLifecycleStorePresent: *wire.PersistentLifecycleStorePresent,
		CleanupExecutorPresent:          *wire.CleanupExecutorPresent, RecoveryReady: *wire.RecoveryReady,
		StartBlocked: *wire.StartBlocked, ApplyBlocked: *wire.ApplyBlocked,
		MetadataOnly: *wire.MetadataOnly, ProductAdapterPresent: *wire.ProductAdapterPresent,
		ProcessStarterPresent: *wire.ProcessStarterPresent, Authority: wire.Authority.value(),
	}
}
