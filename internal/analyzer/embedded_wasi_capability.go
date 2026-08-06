package analyzer

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
)

const (
	AnalyzerExecutionCapabilityProtocolVersion   = "analyzer_execution_capability.v1"
	AnalyzerExecutionConsumptionProtocolVersion  = "analyzer_execution_consumption.v1"
	AnalyzerExecutionCapabilityTokenBytes        = 32
	AnalyzerExecutionCapabilityMaxLifetime       = 5 * time.Minute
	MaxAnalyzerExecutionCapabilityEnvelopeBytes  = 8 * 1024
	MaxAnalyzerExecutionConsumptionEnvelopeBytes = 8 * 1024
)

// AnalyzerExecutionCapability is the durable, secret-free half of one
// embedded analyzer authorization. The bearer token itself is never stored.
type AnalyzerExecutionCapability struct {
	ProtocolVersion       string    `json:"protocol_version"`
	ID                    string    `json:"id"`
	RunID                 string    `json:"run_id"`
	WorkspaceID           string    `json:"workspace_id"`
	RequestID             string    `json:"request_id"`
	Analyzer              string    `json:"analyzer"`
	RequestSHA256         string    `json:"request_sha256"`
	CandidateSHA256       string    `json:"candidate_sha256"`
	ModuleSHA256          string    `json:"module_sha256"`
	BearerTokenSHA256     string    `json:"bearer_token_sha256"`
	IssuedAt              time.Time `json:"issued_at"`
	ExpiresAt             time.Time `json:"expires_at"`
	ExactRunBound         bool      `json:"exact_run_bound"`
	ExactWorkspaceBound   bool      `json:"exact_workspace_bound"`
	ExactRequestBound     bool      `json:"exact_request_bound"`
	ExactModuleBound      bool      `json:"exact_module_bound"`
	OneShot               bool      `json:"one_shot"`
	MetadataOnly          bool      `json:"metadata_only"`
	FilesystemAuthorized  bool      `json:"filesystem_authorized"`
	NetworkAuthorized     bool      `json:"network_authorized"`
	SubprocessAuthorized  bool      `json:"subprocess_authorized"`
	HostProcessAuthorized bool      `json:"host_process_authorized"`
	Fingerprint           string    `json:"fingerprint"`
}

// AnalyzerExecutionConsumption proves the capability was atomically consumed
// before guest execution. It is append-only and contains no bearer token.
type AnalyzerExecutionConsumption struct {
	ProtocolVersion       string    `json:"protocol_version"`
	ID                    string    `json:"id"`
	CapabilityID          string    `json:"capability_id"`
	CapabilityFingerprint string    `json:"capability_fingerprint"`
	RunID                 string    `json:"run_id"`
	WorkspaceID           string    `json:"workspace_id"`
	RequestID             string    `json:"request_id"`
	RequestSHA256         string    `json:"request_sha256"`
	CandidateSHA256       string    `json:"candidate_sha256"`
	ModuleSHA256          string    `json:"module_sha256"`
	ConsumedAt            time.Time `json:"consumed_at"`
	Atomic                bool      `json:"atomic"`
	ReplayGuardEnforced   bool      `json:"replay_guard_enforced"`
	BearerTokenIncluded   bool      `json:"bearer_token_included"`
	RawRequestIncluded    bool      `json:"raw_request_included"`
	RawResultIncluded     bool      `json:"raw_result_included"`
	Fingerprint           string    `json:"fingerprint"`
}

func BuildAnalyzerExecutionCapability(id, runID, workspaceID string,
	candidate InvocationCandidate, bearerToken []byte, issuedAt, expiresAt time.Time,
) (AnalyzerExecutionCapability, ErrorCode) {
	if len(bearerToken) != AnalyzerExecutionCapabilityTokenBytes ||
		!validRequestID(strings.TrimSpace(id)) || !validRequestID(strings.TrimSpace(runID)) ||
		!validRequestID(strings.TrimSpace(workspaceID)) {
		return AnalyzerExecutionCapability{}, CodeInvalidRequest
	}
	issuedAt, expiresAt = issuedAt.UTC(), expiresAt.UTC()
	if issuedAt.IsZero() || !expiresAt.After(issuedAt) ||
		expiresAt.Sub(issuedAt) > AnalyzerExecutionCapabilityMaxLifetime {
		return AnalyzerExecutionCapability{}, CodeInvalidRequest
	}
	candidateDigest, ok := invocationCandidateSHA256(candidate)
	if !ok {
		return AnalyzerExecutionCapability{}, CodeInvalidResult
	}
	moduleDigest := sha256.Sum256(embeddedAnalyzerFixtureWASM)
	tokenDigest := sha256.Sum256(bearerToken)
	value := AnalyzerExecutionCapability{
		ProtocolVersion: AnalyzerExecutionCapabilityProtocolVersion,
		ID:              strings.TrimSpace(id), RunID: strings.TrimSpace(runID),
		WorkspaceID: strings.TrimSpace(workspaceID), RequestID: candidate.RequestID,
		Analyzer: candidate.Analyzer, RequestSHA256: candidate.RequestSHA256,
		CandidateSHA256: candidateDigest, ModuleSHA256: hex.EncodeToString(moduleDigest[:]),
		BearerTokenSHA256: hex.EncodeToString(tokenDigest[:]), IssuedAt: issuedAt,
		ExpiresAt: expiresAt, ExactRunBound: true, ExactWorkspaceBound: true,
		ExactRequestBound: true, ExactModuleBound: true, OneShot: true, MetadataOnly: true,
	}
	value.Fingerprint = analyzerStartFingerprint(value)
	if code := ValidateAnalyzerExecutionCapability(value); code != "" {
		return AnalyzerExecutionCapability{}, CodeInternal
	}
	return value, ""
}

func ValidateAnalyzerExecutionCapability(value AnalyzerExecutionCapability) ErrorCode {
	if value.ProtocolVersion != AnalyzerExecutionCapabilityProtocolVersion ||
		!validRequestID(value.ID) || !validRequestID(value.RunID) ||
		!validRequestID(value.WorkspaceID) || !validRequestID(value.RequestID) ||
		strings.TrimSpace(value.Analyzer) == "" || !validDigest(value.RequestSHA256) ||
		!validDigest(value.CandidateSHA256) || !validDigest(value.ModuleSHA256) ||
		!validDigest(value.BearerTokenSHA256) || value.IssuedAt.IsZero() ||
		!value.ExpiresAt.After(value.IssuedAt) ||
		value.ExpiresAt.Sub(value.IssuedAt) > AnalyzerExecutionCapabilityMaxLifetime ||
		!value.ExactRunBound || !value.ExactWorkspaceBound || !value.ExactRequestBound ||
		!value.ExactModuleBound || !value.OneShot || !value.MetadataOnly ||
		value.FilesystemAuthorized || value.NetworkAuthorized || value.SubprocessAuthorized ||
		value.HostProcessAuthorized || !validDigest(value.Fingerprint) {
		return CodeInvalidResult
	}
	expected := value
	expected.Fingerprint = ""
	if analyzerStartFingerprint(expected) != value.Fingerprint {
		return CodeInvalidResult
	}
	return ""
}

func BuildAnalyzerExecutionConsumption(id string, capability AnalyzerExecutionCapability,
	bearerToken []byte, candidate InvocationCandidate, consumedAt time.Time,
) (AnalyzerExecutionConsumption, ErrorCode) {
	if ValidateAnalyzerExecutionCapability(capability) != "" ||
		len(bearerToken) != AnalyzerExecutionCapabilityTokenBytes || !validRequestID(id) {
		return AnalyzerExecutionConsumption{}, CodeInvalidRequest
	}
	consumedAt = consumedAt.UTC()
	if consumedAt.Before(capability.IssuedAt) || !consumedAt.Before(capability.ExpiresAt) {
		return AnalyzerExecutionConsumption{}, CodeDeadlineExceeded
	}
	tokenDigest := sha256.Sum256(bearerToken)
	expectedDigest, err := hex.DecodeString(capability.BearerTokenSHA256)
	if err != nil || subtle.ConstantTimeCompare(tokenDigest[:], expectedDigest) != 1 {
		return AnalyzerExecutionConsumption{}, CodeCapabilityDenied
	}
	candidateDigest, ok := invocationCandidateSHA256(candidate)
	if !ok || candidate.RequestID != capability.RequestID ||
		candidate.Analyzer != capability.Analyzer ||
		candidate.RequestSHA256 != capability.RequestSHA256 ||
		candidateDigest != capability.CandidateSHA256 ||
		EmbeddedAnalyzerModuleSHA256() != capability.ModuleSHA256 {
		return AnalyzerExecutionConsumption{}, CodeCapabilityDenied
	}
	value := AnalyzerExecutionConsumption{
		ProtocolVersion: AnalyzerExecutionConsumptionProtocolVersion,
		ID:              id, CapabilityID: capability.ID, CapabilityFingerprint: capability.Fingerprint,
		RunID: capability.RunID, WorkspaceID: capability.WorkspaceID,
		RequestID: capability.RequestID, RequestSHA256: capability.RequestSHA256,
		CandidateSHA256: capability.CandidateSHA256, ModuleSHA256: capability.ModuleSHA256,
		ConsumedAt: consumedAt, Atomic: true, ReplayGuardEnforced: true,
	}
	value.Fingerprint = analyzerStartFingerprint(value)
	if code := ValidateAnalyzerExecutionConsumption(value, capability); code != "" {
		return AnalyzerExecutionConsumption{}, CodeInternal
	}
	return value, ""
}

func ValidateAnalyzerExecutionConsumption(value AnalyzerExecutionConsumption,
	capability AnalyzerExecutionCapability,
) ErrorCode {
	if ValidateAnalyzerExecutionCapability(capability) != "" ||
		value.ProtocolVersion != AnalyzerExecutionConsumptionProtocolVersion ||
		!validRequestID(value.ID) || value.CapabilityID != capability.ID ||
		value.CapabilityFingerprint != capability.Fingerprint || value.RunID != capability.RunID ||
		value.WorkspaceID != capability.WorkspaceID || value.RequestID != capability.RequestID ||
		value.RequestSHA256 != capability.RequestSHA256 ||
		value.CandidateSHA256 != capability.CandidateSHA256 ||
		value.ModuleSHA256 != capability.ModuleSHA256 || value.ConsumedAt.IsZero() ||
		value.ConsumedAt.Before(capability.IssuedAt) || !value.ConsumedAt.Before(capability.ExpiresAt) ||
		!value.Atomic || !value.ReplayGuardEnforced || value.BearerTokenIncluded ||
		value.RawRequestIncluded || value.RawResultIncluded || !validDigest(value.Fingerprint) {
		return CodeInvalidResult
	}
	expected := value
	expected.Fingerprint = ""
	if analyzerStartFingerprint(expected) != value.Fingerprint {
		return CodeInvalidResult
	}
	return ""
}

func EmbeddedAnalyzerModuleSHA256() string {
	digest := sha256.Sum256(embeddedAnalyzerFixtureWASM)
	return hex.EncodeToString(digest[:])
}

func EncodeAnalyzerExecutionBearerToken(value []byte) (string, error) {
	if len(value) != AnalyzerExecutionCapabilityTokenBytes {
		return "", errors.New("analyzer capability token must be 32 bytes")
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func DecodeAnalyzerExecutionBearerToken(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != AnalyzerExecutionCapabilityTokenBytes {
		return nil, errors.New("invalid analyzer capability token")
	}
	if canonical, _ := EncodeAnalyzerExecutionBearerToken(decoded); canonical != value {
		return nil, errors.New("non-canonical analyzer capability token")
	}
	return decoded, nil
}

func AnalyzerExecutionCapabilityEqual(left, right AnalyzerExecutionCapability) bool {
	return reflect.DeepEqual(left, right)
}

func EncodeAnalyzerExecutionCapability(value AnalyzerExecutionCapability) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerExecutionCapability(value); code != "" {
		return nil, code
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > MaxAnalyzerExecutionCapabilityEnvelopeBytes {
		return nil, CodeInternal
	}
	return encoded, ""
}

func DecodeAnalyzerExecutionCapability(raw []byte) (AnalyzerExecutionCapability, ErrorCode) {
	var value AnalyzerExecutionCapability
	if !strictDecode(raw, MaxAnalyzerExecutionCapabilityEnvelopeBytes, &value) {
		return AnalyzerExecutionCapability{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerExecutionCapability(value); code != "" {
		return AnalyzerExecutionCapability{}, code
	}
	expected, err := json.Marshal(value)
	if err != nil || !sameAnalyzerExecutionJSONShape(raw, expected) {
		return AnalyzerExecutionCapability{}, CodeInvalidResult
	}
	return value, ""
}

func EncodeAnalyzerExecutionConsumption(value AnalyzerExecutionConsumption,
	capability AnalyzerExecutionCapability,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerExecutionConsumption(value, capability); code != "" {
		return nil, code
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > MaxAnalyzerExecutionConsumptionEnvelopeBytes {
		return nil, CodeInternal
	}
	return encoded, ""
}

func DecodeAnalyzerExecutionConsumption(raw []byte,
	capability AnalyzerExecutionCapability,
) (AnalyzerExecutionConsumption, ErrorCode) {
	var value AnalyzerExecutionConsumption
	if !strictDecode(raw, MaxAnalyzerExecutionConsumptionEnvelopeBytes, &value) {
		return AnalyzerExecutionConsumption{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerExecutionConsumption(value, capability); code != "" {
		return AnalyzerExecutionConsumption{}, code
	}
	expected, err := json.Marshal(value)
	if err != nil || !sameAnalyzerExecutionJSONShape(raw, expected) {
		return AnalyzerExecutionConsumption{}, CodeInvalidResult
	}
	return value, ""
}

func sameAnalyzerExecutionJSONShape(actualRaw, expectedRaw []byte) bool {
	var actual, expected any
	return json.Unmarshal(actualRaw, &actual) == nil && json.Unmarshal(expectedRaw, &expected) == nil &&
		reflect.DeepEqual(actual, expected)
}
