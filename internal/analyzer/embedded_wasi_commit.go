package analyzer

import (
	"errors"
	"strings"
	"time"
)

const AnalyzerExecutionRecordProtocolVersion = "analyzer_execution_record.v1"

// AnalyzerExecutionCommitRequest contains the in-memory values needed to
// atomically commit a validated embedded analyzer result. RawResult is never
// encoded into the durable execution receipt; the artifact store owns it.
type AnalyzerExecutionCommitRequest struct {
	ID            string
	RunID         string
	SessionID     string
	WorkspaceID   string
	CapabilityID  string
	ConsumptionID string
	RequestedBy   string
	Candidate     InvocationCandidate
	Execution     AnalyzerEmbeddedWASIExecution
	RawResult     []byte
	CreatedAt     time.Time
}

// AnalyzerExecutionRecord is the append-only, secret-free receipt that links
// an exact capability consumption to one committed Run Artifact.
type AnalyzerExecutionRecord struct {
	ProtocolVersion       string                        `json:"protocol_version"`
	ID                    string                        `json:"id"`
	RunID                 string                        `json:"run_id"`
	SessionID             string                        `json:"session_id"`
	WorkspaceID           string                        `json:"workspace_id"`
	CapabilityID          string                        `json:"capability_id"`
	ConsumptionID         string                        `json:"consumption_id"`
	RequestedBy           string                        `json:"requested_by"`
	RequestID             string                        `json:"request_id"`
	RequestSHA256         string                        `json:"request_sha256"`
	CandidateSHA256       string                        `json:"candidate_sha256"`
	ModuleSHA256          string                        `json:"module_sha256"`
	Execution             AnalyzerEmbeddedWASIExecution `json:"execution"`
	ResultSHA256          string                        `json:"result_sha256"`
	ResultBytes           int                           `json:"result_bytes"`
	ArtifactID            string                        `json:"artifact_id"`
	CreatedAt             time.Time                     `json:"created_at"`
	CapabilityConsumed    bool                          `json:"capability_consumed"`
	ArtifactAtomic        bool                          `json:"artifact_atomic"`
	RawRequestIncluded    bool                          `json:"raw_request_included"`
	BearerTokenIncluded   bool                          `json:"bearer_token_included"`
	FilesystemMounted     bool                          `json:"filesystem_mounted"`
	NetworkEnabled        bool                          `json:"network_enabled"`
	SubprocessEnabled     bool                          `json:"subprocess_enabled"`
	HostProcessAuthorized bool                          `json:"host_process_authorized"`
	Fingerprint           string                        `json:"fingerprint"`
}

func ValidateAnalyzerExecutionCommitRequest(value AnalyzerExecutionCommitRequest) error {
	for _, identity := range []string{value.ID, value.RunID, value.SessionID, value.WorkspaceID,
		value.CapabilityID, value.ConsumptionID, value.RequestedBy} {
		if !validRequestID(strings.TrimSpace(identity)) || strings.TrimSpace(identity) != identity {
			return errors.New("analyzer execution commit identities must be normalized")
		}
	}
	if value.CreatedAt.IsZero() || len(value.RawResult) == 0 ||
		len(value.RawResult) > value.Candidate.Limits.MaxOutputBytes ||
		ValidateAnalyzerEmbeddedWASIExecution(value.Execution, value.Candidate) != "" {
		return errors.New("analyzer execution commit payload is invalid")
	}
	result, code := DecodeResult(value.RawResult)
	if code != "" || result.RequestID != value.Candidate.RequestID ||
		result.Analyzer != value.Candidate.Analyzer {
		return errors.New("analyzer execution result does not match its candidate")
	}
	return nil
}

func BuildAnalyzerExecutionRecord(request AnalyzerExecutionCommitRequest, artifactID,
	resultSHA256 string, resultBytes int,
) (AnalyzerExecutionRecord, ErrorCode) {
	if ValidateAnalyzerExecutionCommitRequest(request) != nil || !validRequestID(artifactID) ||
		!validDigest(resultSHA256) || resultBytes != len(request.RawResult) {
		return AnalyzerExecutionRecord{}, CodeInvalidRequest
	}
	candidateDigest, ok := invocationCandidateSHA256(request.Candidate)
	if !ok {
		return AnalyzerExecutionRecord{}, CodeInvalidResult
	}
	value := AnalyzerExecutionRecord{
		ProtocolVersion:    AnalyzerExecutionRecordProtocolVersion,
		ID:                 request.ID,
		RunID:              request.RunID,
		SessionID:          request.SessionID,
		WorkspaceID:        request.WorkspaceID,
		CapabilityID:       request.CapabilityID,
		ConsumptionID:      request.ConsumptionID,
		RequestedBy:        request.RequestedBy,
		RequestID:          request.Candidate.RequestID,
		RequestSHA256:      request.Candidate.RequestSHA256,
		CandidateSHA256:    candidateDigest,
		ModuleSHA256:       request.Execution.ModuleSHA256,
		Execution:          request.Execution,
		ResultSHA256:       resultSHA256,
		ResultBytes:        resultBytes,
		ArtifactID:         artifactID,
		CreatedAt:          request.CreatedAt.UTC(),
		CapabilityConsumed: true,
		ArtifactAtomic:     true,
	}
	value.Fingerprint = analyzerStartFingerprint(value)
	if code := ValidateAnalyzerExecutionRecord(value); code != "" {
		return AnalyzerExecutionRecord{}, code
	}
	return value, ""
}

func ValidateAnalyzerExecutionRecord(value AnalyzerExecutionRecord) ErrorCode {
	for _, identity := range []string{value.ID, value.RunID, value.SessionID, value.WorkspaceID,
		value.CapabilityID, value.ConsumptionID, value.RequestedBy, value.RequestID, value.ArtifactID} {
		if !validRequestID(identity) {
			return CodeInvalidResult
		}
	}
	if value.ProtocolVersion != AnalyzerExecutionRecordProtocolVersion ||
		!validDigest(value.RequestSHA256) || !validDigest(value.CandidateSHA256) ||
		!validDigest(value.ModuleSHA256) || !validDigest(value.ResultSHA256) ||
		value.ResultBytes <= 0 || value.ResultBytes > MaxResultEnvelopeBytes ||
		value.CreatedAt.IsZero() || !value.CapabilityConsumed || !value.ArtifactAtomic ||
		value.RawRequestIncluded || value.BearerTokenIncluded || value.FilesystemMounted ||
		value.NetworkEnabled || value.SubprocessEnabled || value.HostProcessAuthorized ||
		value.Execution.RequestID != value.RequestID ||
		value.Execution.RequestSHA256 != value.RequestSHA256 ||
		value.Execution.CandidateSHA256 != value.CandidateSHA256 ||
		value.Execution.ModuleSHA256 != value.ModuleSHA256 ||
		value.Execution.Status != InvocationSucceeded || !validDigest(value.Fingerprint) {
		return CodeInvalidResult
	}
	expected := value
	expected.Fingerprint = ""
	if analyzerStartFingerprint(expected) != value.Fingerprint {
		return CodeInvalidResult
	}
	return ""
}
