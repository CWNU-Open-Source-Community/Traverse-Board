package analyzer

import (
	"crypto/ed25519"
	"encoding/json"
	"reflect"
)

const (
	AnalyzerOperatorStartCapabilityRequestProtocolVersion   = "analyzer_operator_start_capability_request.v1"
	AnalyzerOperatorStartCapabilityContractProtocolVersion  = "analyzer_operator_start_capability_contract.v1"
	AnalyzerOperatorStartCapabilityNonceBytes               = 32
	AnalyzerOperatorStartCapabilityMinValidityMillis        = 30_000
	AnalyzerOperatorStartCapabilityMaxValidityMillis        = 15 * 60 * 1000
	MaxAnalyzerOperatorStartCapabilityRequestEnvelopeBytes  = 12 * 1024
	MaxAnalyzerOperatorStartCapabilityContractEnvelopeBytes = 12 * 1024
)

var analyzerOperatorStartCapabilitySigningDomain = []byte(
	"Prayu/analyzer-operator-start-capability-request/v1\x00",
)

// AnalyzerOperatorStartCapabilityRequest is a signable, metadata-only request.
// It binds one operator, nonce, validity interval, and exact admission matrix.
// It is not a bearer token and cannot start a process.
type AnalyzerOperatorStartCapabilityRequest struct {
	ProtocolVersion            string                          `json:"protocol_version"`
	AdmissionMatrixSHA256      string                          `json:"admission_matrix_sha256"`
	ScopeApprovalSHA256        string                          `json:"scope_approval_sha256"`
	LaunchPlanSHA256           string                          `json:"launch_plan_sha256"`
	ReleaseCandidateSHA256     string                          `json:"release_candidate_sha256"`
	RequestID                  string                          `json:"request_id"`
	Analyzer                   string                          `json:"analyzer"`
	TargetGOOS                 string                          `json:"target_goos"`
	TargetGOARCH               string                          `json:"target_goarch"`
	ExecutableSHA256           string                          `json:"executable_sha256"`
	OperatorIdentitySHA256     string                          `json:"operator_identity_sha256"`
	NonceSHA256                string                          `json:"nonce_sha256"`
	IssuedAtUnixMillis         int64                           `json:"issued_at_unix_ms"`
	ExpiresAtUnixMillis        int64                           `json:"expires_at_unix_ms"`
	ExactAdmissionBound        bool                            `json:"exact_admission_bound"`
	OneShotRequired            bool                            `json:"one_shot_required"`
	DurableReplayGuardRequired bool                            `json:"durable_replay_guard_required"`
	CapabilityRequestOnly      bool                            `json:"capability_request_only"`
	StartBlocked               bool                            `json:"start_blocked"`
	MetadataOnly               bool                            `json:"metadata_only"`
	PathIncluded               bool                            `json:"path_included"`
	CommandIncluded            bool                            `json:"command_included"`
	ArgvIncluded               bool                            `json:"argv_included"`
	EnvironmentIncluded        bool                            `json:"environment_included"`
	InputBodyIncluded          bool                            `json:"input_body_included"`
	Authority                  AnalyzerProductAdapterAuthority `json:"authority"`
}

// AnalyzerOperatorStartCapabilityContract verifies one signed request while
// keeping issuance and execution blocked. Durable replay storage and atomic
// consumption are requirements for a later product batch, not facts here.
type AnalyzerOperatorStartCapabilityContract struct {
	ProtocolVersion            string                          `json:"protocol_version"`
	CapabilityRequestSHA256    string                          `json:"capability_request_sha256"`
	AdmissionMatrixSHA256      string                          `json:"admission_matrix_sha256"`
	ScopeApprovalSHA256        string                          `json:"scope_approval_sha256"`
	OperatorIdentitySHA256     string                          `json:"operator_identity_sha256"`
	PublicKeySHA256            string                          `json:"public_key_sha256"`
	DetachedSignatureSHA256    string                          `json:"detached_signature_sha256"`
	NonceSHA256                string                          `json:"nonce_sha256"`
	IssuedAtUnixMillis         int64                           `json:"issued_at_unix_ms"`
	ExpiresAtUnixMillis        int64                           `json:"expires_at_unix_ms"`
	SignatureScheme            string                          `json:"signature_scheme"`
	RequestCanonical           bool                            `json:"request_canonical"`
	OperatorIdentityBound      bool                            `json:"operator_identity_bound"`
	DetachedSignatureVerified  bool                            `json:"detached_signature_verified"`
	ExactAdmissionBound        bool                            `json:"exact_admission_bound"`
	ValidityIntervalBounded    bool                            `json:"validity_interval_bounded"`
	ClockValidityVerified      bool                            `json:"clock_validity_verified"`
	OneShotRequired            bool                            `json:"one_shot_required"`
	DurableReplayGuardRequired bool                            `json:"durable_replay_guard_required"`
	DurableReplayGuardPresent  bool                            `json:"durable_replay_guard_present"`
	AtomicConsumptionPresent   bool                            `json:"atomic_consumption_present"`
	CapabilityIssued           bool                            `json:"capability_issued"`
	CapabilityConsumed         bool                            `json:"capability_consumed"`
	StartBlocked               bool                            `json:"start_blocked"`
	MetadataOnly               bool                            `json:"metadata_only"`
	ProcessStarterPresent      bool                            `json:"process_starter_present"`
	Authority                  AnalyzerProductAdapterAuthority `json:"authority"`
}

func BuildAnalyzerOperatorStartCapabilityRequest(input AnalyzerProductAdapterEvidenceInput,
	matrix AnalyzerProductAdapterAdmissionMatrix, nonce []byte, issuedAtUnixMillis,
	expiresAtUnixMillis int64,
) (AnalyzerOperatorStartCapabilityRequest, ErrorCode) {
	if code := ValidateAnalyzerProductAdapterAdmissionMatrix(matrix, input); code != "" {
		return AnalyzerOperatorStartCapabilityRequest{}, CodeInvalidResult
	}
	if !validAnalyzerOperatorStartNonce(nonce) ||
		!validAnalyzerOperatorStartValidity(issuedAtUnixMillis, expiresAtUnixMillis) {
		return AnalyzerOperatorStartCapabilityRequest{}, CodeInvalidContent
	}
	matrixDigest, matrixOK := canonicalSHA256(matrix)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	planDigest, planOK := canonicalSHA256(input.LaunchPlan)
	releaseDigest, releaseOK := canonicalSHA256(input.Release)
	if !matrixOK || !approvalOK || !planOK || !releaseOK {
		return AnalyzerOperatorStartCapabilityRequest{}, CodeInternal
	}
	request := AnalyzerOperatorStartCapabilityRequest{
		ProtocolVersion:       AnalyzerOperatorStartCapabilityRequestProtocolVersion,
		AdmissionMatrixSHA256: matrixDigest, ScopeApprovalSHA256: approvalDigest,
		LaunchPlanSHA256: planDigest, ReleaseCandidateSHA256: releaseDigest,
		RequestID: input.Candidate.RequestID, Analyzer: input.Candidate.Analyzer,
		TargetGOOS: input.FormatEvidence.TargetGOOS, TargetGOARCH: input.FormatEvidence.TargetGOARCH,
		ExecutableSHA256:       input.FormatEvidence.ExecutableSHA256,
		OperatorIdentitySHA256: input.ScopeApproval.OperatorIdentitySHA256,
		NonceSHA256:            analyzerProvenanceBytesSHA256(nonce),
		IssuedAtUnixMillis:     issuedAtUnixMillis, ExpiresAtUnixMillis: expiresAtUnixMillis,
		ExactAdmissionBound: true, OneShotRequired: true, DurableReplayGuardRequired: true,
		CapabilityRequestOnly: true, StartBlocked: true, MetadataOnly: true,
	}
	if !validateAnalyzerOperatorStartCapabilityRequestStructure(request, input, matrix, nonce) {
		return AnalyzerOperatorStartCapabilityRequest{}, CodeInternal
	}
	return request, ""
}

func ValidateAnalyzerOperatorStartCapabilityRequest(request AnalyzerOperatorStartCapabilityRequest,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	nonce []byte,
) ErrorCode {
	expected, code := BuildAnalyzerOperatorStartCapabilityRequest(input, matrix, nonce,
		request.IssuedAtUnixMillis, request.ExpiresAtUnixMillis)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(request, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerOperatorStartCapabilityRequest(request AnalyzerOperatorStartCapabilityRequest,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	nonce []byte,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerOperatorStartCapabilityRequest(request, input, matrix, nonce); code != "" {
		return nil, code
	}
	return encodeAnalyzerOperatorStartValue(request,
		MaxAnalyzerOperatorStartCapabilityRequestEnvelopeBytes)
}

func DecodeAnalyzerOperatorStartCapabilityRequest(raw []byte,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	nonce []byte,
) (AnalyzerOperatorStartCapabilityRequest, ErrorCode) {
	var wire analyzerOperatorStartCapabilityRequestWire
	if !strictDecode(raw, MaxAnalyzerOperatorStartCapabilityRequestEnvelopeBytes, &wire) ||
		!wire.complete() {
		return AnalyzerOperatorStartCapabilityRequest{}, CodeInvalidResult
	}
	request := wire.value()
	if code := ValidateAnalyzerOperatorStartCapabilityRequest(request, input, matrix, nonce); code != "" {
		return AnalyzerOperatorStartCapabilityRequest{}, CodeInvalidResult
	}
	return request, ""
}

// AnalyzerOperatorStartCapabilitySigningPayload returns a domain-separated
// canonical request. It retains no key, signature, or raw nonce.
func AnalyzerOperatorStartCapabilitySigningPayload(request AnalyzerOperatorStartCapabilityRequest,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	nonce []byte,
) ([]byte, ErrorCode) {
	raw, code := EncodeAnalyzerOperatorStartCapabilityRequest(request, input, matrix, nonce)
	if code != "" {
		return nil, code
	}
	payload := make([]byte, 0, len(analyzerOperatorStartCapabilitySigningDomain)+len(raw))
	payload = append(payload, analyzerOperatorStartCapabilitySigningDomain...)
	payload = append(payload, raw...)
	return payload, ""
}

func BuildAnalyzerOperatorStartCapabilityContract(request AnalyzerOperatorStartCapabilityRequest,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	nonce, publicKey, detachedSignature []byte,
) (AnalyzerOperatorStartCapabilityContract, ErrorCode) {
	if code := ValidateAnalyzerOperatorStartCapabilityRequest(request, input, matrix, nonce); code != "" {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInvalidResult
	}
	if len(publicKey) != ed25519.PublicKeySize || len(detachedSignature) != ed25519.SignatureSize {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInvalidContent
	}
	publicKeyDigest := analyzerProvenanceBytesSHA256(publicKey)
	if publicKeyDigest != request.OperatorIdentitySHA256 ||
		publicKeyDigest != input.ScopeApproval.OperatorIdentitySHA256 {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInvalidResult
	}
	payload, code := AnalyzerOperatorStartCapabilitySigningPayload(request, input, matrix, nonce)
	if code != "" || !ed25519.Verify(ed25519.PublicKey(publicKey), payload, detachedSignature) {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInvalidResult
	}
	requestDigest, requestOK := canonicalSHA256(request)
	matrixDigest, matrixOK := canonicalSHA256(matrix)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	if !requestOK || !matrixOK || !approvalOK {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInternal
	}
	contract := AnalyzerOperatorStartCapabilityContract{
		ProtocolVersion:         AnalyzerOperatorStartCapabilityContractProtocolVersion,
		CapabilityRequestSHA256: requestDigest, AdmissionMatrixSHA256: matrixDigest,
		ScopeApprovalSHA256: approvalDigest, OperatorIdentitySHA256: publicKeyDigest,
		PublicKeySHA256:         publicKeyDigest,
		DetachedSignatureSHA256: analyzerProvenanceBytesSHA256(detachedSignature),
		NonceSHA256:             request.NonceSHA256, IssuedAtUnixMillis: request.IssuedAtUnixMillis,
		ExpiresAtUnixMillis: request.ExpiresAtUnixMillis,
		SignatureScheme:     AnalyzerProvenanceSignatureScheme, RequestCanonical: true,
		OperatorIdentityBound: true, DetachedSignatureVerified: true,
		ExactAdmissionBound: true, ValidityIntervalBounded: true, OneShotRequired: true,
		DurableReplayGuardRequired: true, StartBlocked: true, MetadataOnly: true,
	}
	if !validateAnalyzerOperatorStartCapabilityContractStructure(contract, request, input,
		matrix, publicKey, detachedSignature) {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInternal
	}
	return contract, ""
}

func ValidateAnalyzerOperatorStartCapabilityContract(contract AnalyzerOperatorStartCapabilityContract,
	request AnalyzerOperatorStartCapabilityRequest, input AnalyzerProductAdapterEvidenceInput,
	matrix AnalyzerProductAdapterAdmissionMatrix, nonce, publicKey, detachedSignature []byte,
) ErrorCode {
	expected, code := BuildAnalyzerOperatorStartCapabilityContract(request, input, matrix, nonce,
		publicKey, detachedSignature)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(contract, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerOperatorStartCapabilityContract(contract AnalyzerOperatorStartCapabilityContract,
	request AnalyzerOperatorStartCapabilityRequest, input AnalyzerProductAdapterEvidenceInput,
	matrix AnalyzerProductAdapterAdmissionMatrix, nonce, publicKey, detachedSignature []byte,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerOperatorStartCapabilityContract(contract, request, input, matrix,
		nonce, publicKey, detachedSignature); code != "" {
		return nil, code
	}
	return encodeAnalyzerOperatorStartValue(contract,
		MaxAnalyzerOperatorStartCapabilityContractEnvelopeBytes)
}

func DecodeAnalyzerOperatorStartCapabilityContract(raw []byte,
	request AnalyzerOperatorStartCapabilityRequest, input AnalyzerProductAdapterEvidenceInput,
	matrix AnalyzerProductAdapterAdmissionMatrix, nonce, publicKey, detachedSignature []byte,
) (AnalyzerOperatorStartCapabilityContract, ErrorCode) {
	var wire analyzerOperatorStartCapabilityContractWire
	if !strictDecode(raw, MaxAnalyzerOperatorStartCapabilityContractEnvelopeBytes, &wire) ||
		!wire.complete() {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInvalidResult
	}
	contract := wire.value()
	if code := ValidateAnalyzerOperatorStartCapabilityContract(contract, request, input, matrix,
		nonce, publicKey, detachedSignature); code != "" {
		return AnalyzerOperatorStartCapabilityContract{}, CodeInvalidResult
	}
	return contract, ""
}

func validAnalyzerOperatorStartNonce(nonce []byte) bool {
	if len(nonce) != AnalyzerOperatorStartCapabilityNonceBytes {
		return false
	}
	var nonzero byte
	for _, value := range nonce {
		nonzero |= value
	}
	return nonzero != 0
}

func validAnalyzerOperatorStartValidity(issuedAtUnixMillis, expiresAtUnixMillis int64) bool {
	if issuedAtUnixMillis <= 0 || expiresAtUnixMillis <= issuedAtUnixMillis {
		return false
	}
	duration := expiresAtUnixMillis - issuedAtUnixMillis
	return duration >= AnalyzerOperatorStartCapabilityMinValidityMillis &&
		duration <= AnalyzerOperatorStartCapabilityMaxValidityMillis
}

func validateAnalyzerOperatorStartCapabilityRequestStructure(
	request AnalyzerOperatorStartCapabilityRequest, input AnalyzerProductAdapterEvidenceInput,
	matrix AnalyzerProductAdapterAdmissionMatrix, nonce []byte,
) bool {
	matrixDigest, matrixOK := canonicalSHA256(matrix)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	planDigest, planOK := canonicalSHA256(input.LaunchPlan)
	releaseDigest, releaseOK := canonicalSHA256(input.Release)
	return matrixOK && approvalOK && planOK && releaseOK && validAnalyzerOperatorStartNonce(nonce) &&
		validAnalyzerOperatorStartValidity(request.IssuedAtUnixMillis, request.ExpiresAtUnixMillis) &&
		request.ProtocolVersion == AnalyzerOperatorStartCapabilityRequestProtocolVersion &&
		request.AdmissionMatrixSHA256 == matrixDigest && request.ScopeApprovalSHA256 == approvalDigest &&
		request.LaunchPlanSHA256 == planDigest && request.ReleaseCandidateSHA256 == releaseDigest &&
		request.RequestID == input.Candidate.RequestID && request.Analyzer == input.Candidate.Analyzer &&
		request.TargetGOOS == input.FormatEvidence.TargetGOOS &&
		request.TargetGOARCH == input.FormatEvidence.TargetGOARCH &&
		request.ExecutableSHA256 == input.FormatEvidence.ExecutableSHA256 &&
		request.OperatorIdentitySHA256 == input.ScopeApproval.OperatorIdentitySHA256 &&
		request.NonceSHA256 == analyzerProvenanceBytesSHA256(nonce) && request.ExactAdmissionBound &&
		request.OneShotRequired && request.DurableReplayGuardRequired && request.CapabilityRequestOnly &&
		request.StartBlocked && request.MetadataOnly && !request.PathIncluded && !request.CommandIncluded &&
		!request.ArgvIncluded && !request.EnvironmentIncluded && !request.InputBodyIncluded &&
		request.Authority == (AnalyzerProductAdapterAuthority{}) && matrix.StartBlocked &&
		!matrix.AdmissionReady
}

func validateAnalyzerOperatorStartCapabilityContractStructure(
	contract AnalyzerOperatorStartCapabilityContract, request AnalyzerOperatorStartCapabilityRequest,
	input AnalyzerProductAdapterEvidenceInput, matrix AnalyzerProductAdapterAdmissionMatrix,
	publicKey, detachedSignature []byte,
) bool {
	requestDigest, requestOK := canonicalSHA256(request)
	matrixDigest, matrixOK := canonicalSHA256(matrix)
	approvalDigest, approvalOK := canonicalSHA256(input.ScopeApproval)
	publicKeyDigest := analyzerProvenanceBytesSHA256(publicKey)
	return requestOK && matrixOK && approvalOK &&
		contract.ProtocolVersion == AnalyzerOperatorStartCapabilityContractProtocolVersion &&
		contract.CapabilityRequestSHA256 == requestDigest &&
		contract.AdmissionMatrixSHA256 == matrixDigest &&
		contract.ScopeApprovalSHA256 == approvalDigest &&
		contract.OperatorIdentitySHA256 == request.OperatorIdentitySHA256 &&
		contract.PublicKeySHA256 == publicKeyDigest &&
		contract.DetachedSignatureSHA256 == analyzerProvenanceBytesSHA256(detachedSignature) &&
		contract.NonceSHA256 == request.NonceSHA256 &&
		contract.IssuedAtUnixMillis == request.IssuedAtUnixMillis &&
		contract.ExpiresAtUnixMillis == request.ExpiresAtUnixMillis &&
		contract.SignatureScheme == AnalyzerProvenanceSignatureScheme && contract.RequestCanonical &&
		contract.OperatorIdentityBound && contract.DetachedSignatureVerified &&
		contract.ExactAdmissionBound && contract.ValidityIntervalBounded &&
		!contract.ClockValidityVerified && contract.OneShotRequired &&
		contract.DurableReplayGuardRequired && !contract.DurableReplayGuardPresent &&
		!contract.AtomicConsumptionPresent && !contract.CapabilityIssued &&
		!contract.CapabilityConsumed && contract.StartBlocked && contract.MetadataOnly &&
		!contract.ProcessStarterPresent && contract.Authority == (AnalyzerProductAdapterAuthority{})
}

func encodeAnalyzerOperatorStartValue(value any, maximum int) ([]byte, ErrorCode) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximum {
		return nil, CodeInternal
	}
	return encoded, ""
}

type analyzerOperatorStartCapabilityRequestWire struct {
	ProtocolVersion            *string                              `json:"protocol_version"`
	AdmissionMatrixSHA256      *string                              `json:"admission_matrix_sha256"`
	ScopeApprovalSHA256        *string                              `json:"scope_approval_sha256"`
	LaunchPlanSHA256           *string                              `json:"launch_plan_sha256"`
	ReleaseCandidateSHA256     *string                              `json:"release_candidate_sha256"`
	RequestID                  *string                              `json:"request_id"`
	Analyzer                   *string                              `json:"analyzer"`
	TargetGOOS                 *string                              `json:"target_goos"`
	TargetGOARCH               *string                              `json:"target_goarch"`
	ExecutableSHA256           *string                              `json:"executable_sha256"`
	OperatorIdentitySHA256     *string                              `json:"operator_identity_sha256"`
	NonceSHA256                *string                              `json:"nonce_sha256"`
	IssuedAtUnixMillis         *int64                               `json:"issued_at_unix_ms"`
	ExpiresAtUnixMillis        *int64                               `json:"expires_at_unix_ms"`
	ExactAdmissionBound        *bool                                `json:"exact_admission_bound"`
	OneShotRequired            *bool                                `json:"one_shot_required"`
	DurableReplayGuardRequired *bool                                `json:"durable_replay_guard_required"`
	CapabilityRequestOnly      *bool                                `json:"capability_request_only"`
	StartBlocked               *bool                                `json:"start_blocked"`
	MetadataOnly               *bool                                `json:"metadata_only"`
	PathIncluded               *bool                                `json:"path_included"`
	CommandIncluded            *bool                                `json:"command_included"`
	ArgvIncluded               *bool                                `json:"argv_included"`
	EnvironmentIncluded        *bool                                `json:"environment_included"`
	InputBodyIncluded          *bool                                `json:"input_body_included"`
	Authority                  *analyzerProductAdapterAuthorityWire `json:"authority"`
}

func (wire analyzerOperatorStartCapabilityRequestWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.AdmissionMatrixSHA256 != nil &&
		wire.ScopeApprovalSHA256 != nil && wire.LaunchPlanSHA256 != nil &&
		wire.ReleaseCandidateSHA256 != nil && wire.RequestID != nil && wire.Analyzer != nil &&
		wire.TargetGOOS != nil && wire.TargetGOARCH != nil && wire.ExecutableSHA256 != nil &&
		wire.OperatorIdentitySHA256 != nil && wire.NonceSHA256 != nil &&
		wire.IssuedAtUnixMillis != nil && wire.ExpiresAtUnixMillis != nil &&
		wire.ExactAdmissionBound != nil && wire.OneShotRequired != nil &&
		wire.DurableReplayGuardRequired != nil && wire.CapabilityRequestOnly != nil &&
		wire.StartBlocked != nil && wire.MetadataOnly != nil && wire.PathIncluded != nil &&
		wire.CommandIncluded != nil && wire.ArgvIncluded != nil && wire.EnvironmentIncluded != nil &&
		wire.InputBodyIncluded != nil && wire.Authority != nil && wire.Authority.complete()
}

func (wire analyzerOperatorStartCapabilityRequestWire) value() AnalyzerOperatorStartCapabilityRequest {
	return AnalyzerOperatorStartCapabilityRequest{
		ProtocolVersion: *wire.ProtocolVersion, AdmissionMatrixSHA256: *wire.AdmissionMatrixSHA256,
		ScopeApprovalSHA256: *wire.ScopeApprovalSHA256, LaunchPlanSHA256: *wire.LaunchPlanSHA256,
		ReleaseCandidateSHA256: *wire.ReleaseCandidateSHA256, RequestID: *wire.RequestID,
		Analyzer: *wire.Analyzer, TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableSHA256:       *wire.ExecutableSHA256,
		OperatorIdentitySHA256: *wire.OperatorIdentitySHA256, NonceSHA256: *wire.NonceSHA256,
		IssuedAtUnixMillis: *wire.IssuedAtUnixMillis, ExpiresAtUnixMillis: *wire.ExpiresAtUnixMillis,
		ExactAdmissionBound: *wire.ExactAdmissionBound, OneShotRequired: *wire.OneShotRequired,
		DurableReplayGuardRequired: *wire.DurableReplayGuardRequired,
		CapabilityRequestOnly:      *wire.CapabilityRequestOnly, StartBlocked: *wire.StartBlocked,
		MetadataOnly: *wire.MetadataOnly, PathIncluded: *wire.PathIncluded,
		CommandIncluded: *wire.CommandIncluded, ArgvIncluded: *wire.ArgvIncluded,
		EnvironmentIncluded: *wire.EnvironmentIncluded, InputBodyIncluded: *wire.InputBodyIncluded,
		Authority: wire.Authority.value(),
	}
}

type analyzerOperatorStartCapabilityContractWire struct {
	ProtocolVersion            *string                              `json:"protocol_version"`
	CapabilityRequestSHA256    *string                              `json:"capability_request_sha256"`
	AdmissionMatrixSHA256      *string                              `json:"admission_matrix_sha256"`
	ScopeApprovalSHA256        *string                              `json:"scope_approval_sha256"`
	OperatorIdentitySHA256     *string                              `json:"operator_identity_sha256"`
	PublicKeySHA256            *string                              `json:"public_key_sha256"`
	DetachedSignatureSHA256    *string                              `json:"detached_signature_sha256"`
	NonceSHA256                *string                              `json:"nonce_sha256"`
	IssuedAtUnixMillis         *int64                               `json:"issued_at_unix_ms"`
	ExpiresAtUnixMillis        *int64                               `json:"expires_at_unix_ms"`
	SignatureScheme            *string                              `json:"signature_scheme"`
	RequestCanonical           *bool                                `json:"request_canonical"`
	OperatorIdentityBound      *bool                                `json:"operator_identity_bound"`
	DetachedSignatureVerified  *bool                                `json:"detached_signature_verified"`
	ExactAdmissionBound        *bool                                `json:"exact_admission_bound"`
	ValidityIntervalBounded    *bool                                `json:"validity_interval_bounded"`
	ClockValidityVerified      *bool                                `json:"clock_validity_verified"`
	OneShotRequired            *bool                                `json:"one_shot_required"`
	DurableReplayGuardRequired *bool                                `json:"durable_replay_guard_required"`
	DurableReplayGuardPresent  *bool                                `json:"durable_replay_guard_present"`
	AtomicConsumptionPresent   *bool                                `json:"atomic_consumption_present"`
	CapabilityIssued           *bool                                `json:"capability_issued"`
	CapabilityConsumed         *bool                                `json:"capability_consumed"`
	StartBlocked               *bool                                `json:"start_blocked"`
	MetadataOnly               *bool                                `json:"metadata_only"`
	ProcessStarterPresent      *bool                                `json:"process_starter_present"`
	Authority                  *analyzerProductAdapterAuthorityWire `json:"authority"`
}

func (wire analyzerOperatorStartCapabilityContractWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.CapabilityRequestSHA256 != nil &&
		wire.AdmissionMatrixSHA256 != nil && wire.ScopeApprovalSHA256 != nil &&
		wire.OperatorIdentitySHA256 != nil && wire.PublicKeySHA256 != nil &&
		wire.DetachedSignatureSHA256 != nil && wire.NonceSHA256 != nil &&
		wire.IssuedAtUnixMillis != nil && wire.ExpiresAtUnixMillis != nil &&
		wire.SignatureScheme != nil && wire.RequestCanonical != nil &&
		wire.OperatorIdentityBound != nil && wire.DetachedSignatureVerified != nil &&
		wire.ExactAdmissionBound != nil && wire.ValidityIntervalBounded != nil &&
		wire.ClockValidityVerified != nil && wire.OneShotRequired != nil &&
		wire.DurableReplayGuardRequired != nil && wire.DurableReplayGuardPresent != nil &&
		wire.AtomicConsumptionPresent != nil && wire.CapabilityIssued != nil &&
		wire.CapabilityConsumed != nil && wire.StartBlocked != nil && wire.MetadataOnly != nil &&
		wire.ProcessStarterPresent != nil && wire.Authority != nil && wire.Authority.complete()
}

func (wire analyzerOperatorStartCapabilityContractWire) value() AnalyzerOperatorStartCapabilityContract {
	return AnalyzerOperatorStartCapabilityContract{
		ProtocolVersion:         *wire.ProtocolVersion,
		CapabilityRequestSHA256: *wire.CapabilityRequestSHA256,
		AdmissionMatrixSHA256:   *wire.AdmissionMatrixSHA256,
		ScopeApprovalSHA256:     *wire.ScopeApprovalSHA256,
		OperatorIdentitySHA256:  *wire.OperatorIdentitySHA256,
		PublicKeySHA256:         *wire.PublicKeySHA256,
		DetachedSignatureSHA256: *wire.DetachedSignatureSHA256,
		NonceSHA256:             *wire.NonceSHA256, IssuedAtUnixMillis: *wire.IssuedAtUnixMillis,
		ExpiresAtUnixMillis: *wire.ExpiresAtUnixMillis, SignatureScheme: *wire.SignatureScheme,
		RequestCanonical: *wire.RequestCanonical, OperatorIdentityBound: *wire.OperatorIdentityBound,
		DetachedSignatureVerified: *wire.DetachedSignatureVerified,
		ExactAdmissionBound:       *wire.ExactAdmissionBound,
		ValidityIntervalBounded:   *wire.ValidityIntervalBounded,
		ClockValidityVerified:     *wire.ClockValidityVerified, OneShotRequired: *wire.OneShotRequired,
		DurableReplayGuardRequired: *wire.DurableReplayGuardRequired,
		DurableReplayGuardPresent:  *wire.DurableReplayGuardPresent,
		AtomicConsumptionPresent:   *wire.AtomicConsumptionPresent,
		CapabilityIssued:           *wire.CapabilityIssued, CapabilityConsumed: *wire.CapabilityConsumed,
		StartBlocked: *wire.StartBlocked, MetadataOnly: *wire.MetadataOnly,
		ProcessStarterPresent: *wire.ProcessStarterPresent, Authority: wire.Authority.value(),
	}
}
