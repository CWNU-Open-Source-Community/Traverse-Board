package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const (
	AnalyzerDurableStartRequestProtocolVersion   = "analyzer_durable_start_request.v1"
	AnalyzerStartIntentProtocolVersion           = "analyzer_start_intent.v1"
	AnalyzerStartLifecycleReceiptProtocolVersion = "analyzer_start_lifecycle_receipt.v1"
	MaxAnalyzerStartControlEnvelopeBytes         = 32 * 1024
)

type AnalyzerStartAdapter string

const (
	AnalyzerStartAdapterDisabled AnalyzerStartAdapter = "disabled"
	AnalyzerStartAdapterFake     AnalyzerStartAdapter = "fake"
)

type AnalyzerStartIntentState string

const (
	AnalyzerStartIntentDisabled         AnalyzerStartIntentState = "disabled"
	AnalyzerStartIntentPrepared         AnalyzerStartIntentState = "prepared"
	AnalyzerStartIntentConsumed         AnalyzerStartIntentState = "consumed"
	AnalyzerStartIntentExpired          AnalyzerStartIntentState = "expired"
	AnalyzerStartIntentCancelled        AnalyzerStartIntentState = "cancelled"
	AnalyzerStartIntentFakeSucceeded    AnalyzerStartIntentState = "fake_succeeded"
	AnalyzerStartIntentFakeFailed       AnalyzerStartIntentState = "fake_failed"
	AnalyzerStartIntentRecoveryRequired AnalyzerStartIntentState = "recovery_required"
)

const (
	analyzerStartReasonDisabled         = "adapter_disabled"
	analyzerStartReasonPrepared         = "fake_write_ahead_prepared"
	analyzerStartReasonConsumed         = "fake_request_atomically_consumed"
	analyzerStartReasonExpired          = "signed_request_expired"
	analyzerStartReasonCancelled        = "operator_cancelled"
	analyzerStartReasonFakeSucceeded    = "fake_execution_succeeded"
	analyzerStartReasonFakeFailed       = "fake_execution_failed"
	analyzerStartReasonRecoveryRequired = "restart_recovery_required"
)

// AnalyzerDurableStartRequest is a redacted, signed-evidence projection. It is
// a replay ledger entry, not a bearer capability and not a process start spec.
type AnalyzerDurableStartRequest struct {
	ProtocolVersion           string                          `json:"protocol_version"`
	ID                        string                          `json:"id"`
	RunID                     string                          `json:"run_id"`
	WorkspaceID               string                          `json:"workspace_id"`
	SignedRequestID           string                          `json:"signed_request_id"`
	Analyzer                  string                          `json:"analyzer"`
	TargetGOOS                string                          `json:"target_goos"`
	TargetGOARCH              string                          `json:"target_goarch"`
	ExecutableSHA256          string                          `json:"executable_sha256"`
	OperatorIdentitySHA256    string                          `json:"operator_identity_sha256"`
	AdmissionMatrixSHA256     string                          `json:"admission_matrix_sha256"`
	ScopeApprovalSHA256       string                          `json:"scope_approval_sha256"`
	CapabilityRequestSHA256   string                          `json:"capability_request_sha256"`
	CapabilityContractSHA256  string                          `json:"capability_contract_sha256"`
	NonceSHA256               string                          `json:"nonce_sha256"`
	Adapter                   AnalyzerStartAdapter            `json:"adapter"`
	IssuedAt                  time.Time                       `json:"issued_at"`
	ExpiresAt                 time.Time                       `json:"expires_at"`
	RegisteredAt              time.Time                       `json:"registered_at"`
	ExactRunWorkspaceBound    bool                            `json:"exact_run_workspace_bound"`
	SignatureVerified         bool                            `json:"signature_verified"`
	ClockValidityVerified     bool                            `json:"clock_validity_verified"`
	DurableReplayGuardPresent bool                            `json:"durable_replay_guard_present"`
	AtomicConsumptionPresent  bool                            `json:"atomic_consumption_present"`
	CapabilityIssued          bool                            `json:"capability_issued"`
	CapabilityConsumed        bool                            `json:"capability_consumed"`
	StartBlocked              bool                            `json:"start_blocked"`
	MetadataOnly              bool                            `json:"metadata_only"`
	PathIncluded              bool                            `json:"path_included"`
	CommandIncluded           bool                            `json:"command_included"`
	ArgvIncluded              bool                            `json:"argv_included"`
	EnvironmentIncluded       bool                            `json:"environment_included"`
	InputBodyIncluded         bool                            `json:"input_body_included"`
	ProcessStarterPresent     bool                            `json:"process_starter_present"`
	Authority                 AnalyzerProductAdapterAuthority `json:"authority"`
	Fingerprint               string                          `json:"fingerprint"`
}

// AnalyzerStartIntent is a generation-fenced write-ahead state record. The
// fake adapter proves lifecycle semantics only; every real authority remains
// false in every state.
type AnalyzerStartIntent struct {
	ProtocolVersion           string                          `json:"protocol_version"`
	ID                        string                          `json:"id"`
	RequestID                 string                          `json:"request_id"`
	RunID                     string                          `json:"run_id"`
	WorkspaceID               string                          `json:"workspace_id"`
	RequestFingerprint        string                          `json:"request_fingerprint"`
	NonceSHA256               string                          `json:"nonce_sha256"`
	OperatorIdentitySHA256    string                          `json:"operator_identity_sha256"`
	Adapter                   AnalyzerStartAdapter            `json:"adapter"`
	Generation                uint64                          `json:"generation"`
	PreviousIntentFingerprint string                          `json:"previous_intent_fingerprint"`
	State                     AnalyzerStartIntentState        `json:"state"`
	ReasonCode                string                          `json:"reason_code"`
	RequestConsumed           bool                            `json:"request_consumed"`
	Terminal                  bool                            `json:"terminal"`
	RecoveryRequired          bool                            `json:"recovery_required"`
	WriteAheadRecorded        bool                            `json:"write_ahead_recorded"`
	AtomicConsumptionEnforced bool                            `json:"atomic_consumption_enforced"`
	FakeExecutionOnly         bool                            `json:"fake_execution_only"`
	ProcessStartAuthorized    bool                            `json:"process_start_authorized"`
	ProcessObserved           bool                            `json:"process_observed"`
	NetworkAuthorized         bool                            `json:"network_authorized"`
	ArtifactCommitAuthorized  bool                            `json:"artifact_commit_authorized"`
	RawOutputIncluded         bool                            `json:"raw_output_included"`
	Authority                 AnalyzerProductAdapterAuthority `json:"authority"`
	ExpiresAt                 time.Time                       `json:"expires_at"`
	TransitionedAt            time.Time                       `json:"transitioned_at"`
	Fingerprint               string                          `json:"fingerprint"`
}

// AnalyzerStartLifecycleReceipt is an append-only, redacted projection of one
// intent generation. It contains no command, output, path, or process handle.
type AnalyzerStartLifecycleReceipt struct {
	ProtocolVersion            string                          `json:"protocol_version"`
	ID                         string                          `json:"id"`
	RequestID                  string                          `json:"request_id"`
	RunID                      string                          `json:"run_id"`
	WorkspaceID                string                          `json:"workspace_id"`
	RequestFingerprint         string                          `json:"request_fingerprint"`
	IntentFingerprint          string                          `json:"intent_fingerprint"`
	PreviousReceiptFingerprint string                          `json:"previous_receipt_fingerprint"`
	Generation                 uint64                          `json:"generation"`
	State                      AnalyzerStartIntentState        `json:"state"`
	ReasonCode                 string                          `json:"reason_code"`
	Terminal                   bool                            `json:"terminal"`
	RecoveryRequired           bool                            `json:"recovery_required"`
	Redacted                   bool                            `json:"redacted"`
	RawRequestIncluded         bool                            `json:"raw_request_included"`
	RawOutputIncluded          bool                            `json:"raw_output_included"`
	CommandIncluded            bool                            `json:"command_included"`
	ProcessHandleIncluded      bool                            `json:"process_handle_included"`
	ArtifactCommitted          bool                            `json:"artifact_committed"`
	Authority                  AnalyzerProductAdapterAuthority `json:"authority"`
	RecordedAt                 time.Time                       `json:"recorded_at"`
	Fingerprint                string                          `json:"fingerprint"`
}

func BuildAnalyzerDurableStartRequest(id, runID, workspaceID string,
	request AnalyzerOperatorStartCapabilityRequest,
	contract AnalyzerOperatorStartCapabilityContract,
	input AnalyzerProductAdapterEvidenceInput,
	matrix AnalyzerProductAdapterAdmissionMatrix,
	nonce, publicKey, detachedSignature []byte,
	adapter AnalyzerStartAdapter, registeredAt time.Time,
) (AnalyzerDurableStartRequest, ErrorCode) {
	if ValidateAnalyzerOperatorStartCapabilityContract(contract, request, input, matrix,
		nonce, publicKey, detachedSignature) != "" || !validAnalyzerStartAdapter(adapter) {
		return AnalyzerDurableStartRequest{}, CodeInvalidResult
	}
	registeredAt = registeredAt.UTC()
	issuedAt := time.UnixMilli(request.IssuedAtUnixMillis).UTC()
	expiresAt := time.UnixMilli(request.ExpiresAtUnixMillis).UTC()
	if registeredAt.Before(issuedAt) || !registeredAt.Before(expiresAt) {
		return AnalyzerDurableStartRequest{}, CodeInvalidContent
	}
	requestDigest, requestOK := canonicalSHA256(request)
	contractDigest, contractOK := canonicalSHA256(contract)
	if !requestOK || !contractOK {
		return AnalyzerDurableStartRequest{}, CodeInternal
	}
	record := AnalyzerDurableStartRequest{
		ProtocolVersion: AnalyzerDurableStartRequestProtocolVersion,
		ID:              strings.TrimSpace(id), RunID: strings.TrimSpace(runID),
		WorkspaceID: strings.TrimSpace(workspaceID), SignedRequestID: request.RequestID,
		Analyzer: request.Analyzer, TargetGOOS: request.TargetGOOS,
		TargetGOARCH: request.TargetGOARCH, ExecutableSHA256: request.ExecutableSHA256,
		OperatorIdentitySHA256:   request.OperatorIdentitySHA256,
		AdmissionMatrixSHA256:    request.AdmissionMatrixSHA256,
		ScopeApprovalSHA256:      request.ScopeApprovalSHA256,
		CapabilityRequestSHA256:  requestDigest,
		CapabilityContractSHA256: contractDigest, NonceSHA256: request.NonceSHA256,
		Adapter: adapter, IssuedAt: issuedAt, ExpiresAt: expiresAt,
		RegisteredAt: registeredAt, ExactRunWorkspaceBound: true,
		SignatureVerified: true, ClockValidityVerified: true,
		DurableReplayGuardPresent: true, StartBlocked: true, MetadataOnly: true,
	}
	record.Fingerprint = analyzerStartFingerprint(record)
	if err := ValidateStoredAnalyzerDurableStartRequest(record); err != nil {
		return AnalyzerDurableStartRequest{}, CodeInvalidResult
	}
	return record, ""
}

func ValidateStoredAnalyzerDurableStartRequest(record AnalyzerDurableStartRequest) error {
	if record.ProtocolVersion != AnalyzerDurableStartRequestProtocolVersion ||
		!validRequestID(record.ID) || !validRequestID(record.RunID) ||
		!validRequestID(record.WorkspaceID) || !validRequestID(record.SignedRequestID) ||
		strings.TrimSpace(record.Analyzer) == "" || strings.TrimSpace(record.TargetGOOS) == "" ||
		strings.TrimSpace(record.TargetGOARCH) == "" || !validAnalyzerStartAdapter(record.Adapter) {
		return errors.New("invalid analyzer durable start request identity")
	}
	digests := []string{record.ExecutableSHA256, record.OperatorIdentitySHA256,
		record.AdmissionMatrixSHA256, record.ScopeApprovalSHA256,
		record.CapabilityRequestSHA256, record.CapabilityContractSHA256,
		record.NonceSHA256, record.Fingerprint}
	for _, digest := range digests {
		if !validDigest(digest) {
			return errors.New("invalid analyzer durable start request digest")
		}
	}
	if record.IssuedAt.IsZero() || record.ExpiresAt.IsZero() || record.RegisteredAt.IsZero() ||
		!record.ExpiresAt.After(record.IssuedAt) || record.RegisteredAt.Before(record.IssuedAt) ||
		!record.RegisteredAt.Before(record.ExpiresAt) || !record.ExactRunWorkspaceBound ||
		!record.SignatureVerified || !record.ClockValidityVerified ||
		!record.DurableReplayGuardPresent || record.AtomicConsumptionPresent ||
		record.CapabilityIssued || record.CapabilityConsumed || !record.StartBlocked ||
		!record.MetadataOnly || record.PathIncluded || record.CommandIncluded ||
		record.ArgvIncluded || record.EnvironmentIncluded || record.InputBodyIncluded ||
		record.ProcessStarterPresent || record.Authority != (AnalyzerProductAdapterAuthority{}) {
		return errors.New("analyzer durable start request widens authority")
	}
	if analyzerStartFingerprint(record) != record.Fingerprint {
		return errors.New("analyzer durable start request fingerprint mismatch")
	}
	return nil
}

func BuildInitialAnalyzerStartIntent(request AnalyzerDurableStartRequest,
	at time.Time,
) (AnalyzerStartIntent, error) {
	if err := ValidateStoredAnalyzerDurableStartRequest(request); err != nil {
		return AnalyzerStartIntent{}, err
	}
	at = at.UTC()
	if at.Before(request.RegisteredAt) || !at.Before(request.ExpiresAt) {
		return AnalyzerStartIntent{}, errors.New("analyzer start intent time is outside signed validity")
	}
	state, reason := AnalyzerStartIntentPrepared, analyzerStartReasonPrepared
	if request.Adapter == AnalyzerStartAdapterDisabled {
		state, reason = AnalyzerStartIntentDisabled, analyzerStartReasonDisabled
	}
	intent := AnalyzerStartIntent{
		ProtocolVersion: AnalyzerStartIntentProtocolVersion,
		ID:              fmt.Sprintf("%s-intent-1", request.ID), RequestID: request.ID,
		RunID: request.RunID, WorkspaceID: request.WorkspaceID,
		RequestFingerprint: request.Fingerprint, NonceSHA256: request.NonceSHA256,
		OperatorIdentitySHA256: request.OperatorIdentitySHA256, Adapter: request.Adapter,
		Generation: 1, State: state, ReasonCode: reason,
		Terminal: state == AnalyzerStartIntentDisabled, WriteAheadRecorded: true,
		AtomicConsumptionEnforced: true,
		FakeExecutionOnly:         request.Adapter == AnalyzerStartAdapterFake,
		ExpiresAt:                 request.ExpiresAt, TransitionedAt: at,
	}
	intent.Fingerprint = analyzerStartFingerprint(intent)
	return intent, ValidateStoredAnalyzerStartIntent(intent)
}

func BuildAnalyzerStartIntentSuccessor(previous AnalyzerStartIntent,
	target AnalyzerStartIntentState, at time.Time,
) (AnalyzerStartIntent, error) {
	if err := ValidateStoredAnalyzerStartIntent(previous); err != nil {
		return AnalyzerStartIntent{}, err
	}
	at = at.UTC()
	if at.Before(previous.TransitionedAt) {
		return AnalyzerStartIntent{}, errors.New("analyzer start intent time moved backwards")
	}
	consumed := previous.RequestConsumed
	switch {
	case previous.State == AnalyzerStartIntentPrepared && target == AnalyzerStartIntentConsumed:
		if previous.Adapter != AnalyzerStartAdapterFake || !at.Before(previous.ExpiresAt) {
			return AnalyzerStartIntent{}, errors.New("expired or non-fake analyzer request cannot be consumed")
		}
		consumed = true
	case previous.State == AnalyzerStartIntentPrepared && target == AnalyzerStartIntentExpired:
		if at.Before(previous.ExpiresAt) {
			return AnalyzerStartIntent{}, errors.New("analyzer request has not expired")
		}
	case previous.State == AnalyzerStartIntentPrepared && target == AnalyzerStartIntentCancelled:
	case previous.State == AnalyzerStartIntentConsumed &&
		(target == AnalyzerStartIntentFakeSucceeded || target == AnalyzerStartIntentFakeFailed ||
			target == AnalyzerStartIntentRecoveryRequired || target == AnalyzerStartIntentCancelled):
	default:
		return AnalyzerStartIntent{}, fmt.Errorf("invalid analyzer start transition %s -> %s",
			previous.State, target)
	}
	next := previous
	next.ID = fmt.Sprintf("%s-intent-%d", previous.RequestID, previous.Generation+1)
	next.Generation++
	next.PreviousIntentFingerprint = previous.Fingerprint
	next.State = target
	next.ReasonCode = analyzerStartReason(target)
	next.RequestConsumed = consumed
	next.Terminal = analyzerStartTerminal(target)
	next.RecoveryRequired = target == AnalyzerStartIntentRecoveryRequired
	next.TransitionedAt = at
	next.Fingerprint = ""
	next.Fingerprint = analyzerStartFingerprint(next)
	if err := ValidateStoredAnalyzerStartIntentSuccessor(next, previous); err != nil {
		return AnalyzerStartIntent{}, err
	}
	return next, nil
}

func ValidateStoredAnalyzerStartIntent(intent AnalyzerStartIntent) error {
	if intent.ProtocolVersion != AnalyzerStartIntentProtocolVersion ||
		!validRequestID(intent.ID) || !validRequestID(intent.RequestID) ||
		!validRequestID(intent.RunID) || !validRequestID(intent.WorkspaceID) ||
		!validDigest(intent.RequestFingerprint) || !validDigest(intent.NonceSHA256) ||
		!validDigest(intent.OperatorIdentitySHA256) || !validAnalyzerStartAdapter(intent.Adapter) ||
		intent.Generation == 0 || analyzerStartReason(intent.State) == "" ||
		intent.ReasonCode != analyzerStartReason(intent.State) ||
		intent.Terminal != analyzerStartTerminal(intent.State) || intent.ExpiresAt.IsZero() ||
		intent.TransitionedAt.IsZero() || !intent.WriteAheadRecorded ||
		!intent.AtomicConsumptionEnforced ||
		intent.FakeExecutionOnly != (intent.Adapter == AnalyzerStartAdapterFake) ||
		intent.ProcessStartAuthorized || intent.ProcessObserved || intent.NetworkAuthorized ||
		intent.ArtifactCommitAuthorized || intent.RawOutputIncluded ||
		intent.Authority != (AnalyzerProductAdapterAuthority{}) {
		return errors.New("invalid or authority-widening analyzer start intent")
	}
	if intent.Generation == 1 {
		if intent.PreviousIntentFingerprint != "" || intent.RequestConsumed ||
			!((intent.Adapter == AnalyzerStartAdapterDisabled && intent.State == AnalyzerStartIntentDisabled) ||
				(intent.Adapter == AnalyzerStartAdapterFake && intent.State == AnalyzerStartIntentPrepared)) {
			return errors.New("invalid initial analyzer start intent")
		}
	} else {
		if !validDigest(intent.PreviousIntentFingerprint) ||
			intent.State == AnalyzerStartIntentDisabled ||
			intent.State == AnalyzerStartIntentPrepared ||
			intent.Adapter != AnalyzerStartAdapterFake {
			return errors.New("analyzer start intent predecessor or successor state is invalid")
		}
	}
	if (intent.State == AnalyzerStartIntentConsumed || intent.State == AnalyzerStartIntentFakeSucceeded ||
		intent.State == AnalyzerStartIntentFakeFailed || intent.State == AnalyzerStartIntentRecoveryRequired) &&
		!intent.RequestConsumed {
		return errors.New("consumed analyzer start state lacks consumption proof")
	}
	if analyzerStartFingerprint(intent) != intent.Fingerprint {
		return errors.New("analyzer start intent fingerprint mismatch")
	}
	return nil
}

func ValidateStoredAnalyzerStartIntentSuccessor(next, previous AnalyzerStartIntent) error {
	if err := ValidateStoredAnalyzerStartIntent(next); err != nil {
		return err
	}
	if err := ValidateStoredAnalyzerStartIntent(previous); err != nil {
		return err
	}
	if next.RequestID != previous.RequestID || next.RunID != previous.RunID ||
		next.WorkspaceID != previous.WorkspaceID ||
		next.RequestFingerprint != previous.RequestFingerprint ||
		next.NonceSHA256 != previous.NonceSHA256 ||
		next.OperatorIdentitySHA256 != previous.OperatorIdentitySHA256 ||
		next.Adapter != previous.Adapter || !next.ExpiresAt.Equal(previous.ExpiresAt) ||
		next.Generation != previous.Generation+1 ||
		next.PreviousIntentFingerprint != previous.Fingerprint {
		return errors.New("analyzer start intent successor binding mismatch")
	}
	expected, err := buildAnalyzerStartIntentSuccessorUnchecked(previous, next.State,
		next.TransitionedAt)
	if err != nil || !reflect.DeepEqual(next, expected) {
		return errors.New("invalid analyzer start intent successor")
	}
	return nil
}

func BuildAnalyzerStartLifecycleReceipt(intent AnalyzerStartIntent,
	previous *AnalyzerStartLifecycleReceipt,
) (AnalyzerStartLifecycleReceipt, error) {
	if err := ValidateStoredAnalyzerStartIntent(intent); err != nil {
		return AnalyzerStartLifecycleReceipt{}, err
	}
	previousFingerprint := ""
	if previous != nil {
		if err := ValidateStoredAnalyzerStartLifecycleReceipt(*previous); err != nil ||
			previous.RequestID != intent.RequestID || previous.RunID != intent.RunID ||
			previous.WorkspaceID != intent.WorkspaceID ||
			previous.RequestFingerprint != intent.RequestFingerprint ||
			previous.IntentFingerprint != intent.PreviousIntentFingerprint ||
			previous.Generation+1 != intent.Generation {
			return AnalyzerStartLifecycleReceipt{}, errors.New("invalid analyzer receipt predecessor")
		}
		previousFingerprint = previous.Fingerprint
	} else if intent.Generation != 1 {
		return AnalyzerStartLifecycleReceipt{}, errors.New("analyzer receipt predecessor is required")
	}
	receipt := AnalyzerStartLifecycleReceipt{
		ProtocolVersion: AnalyzerStartLifecycleReceiptProtocolVersion,
		ID:              intent.ID + "-receipt", RequestID: intent.RequestID,
		RunID: intent.RunID, WorkspaceID: intent.WorkspaceID,
		RequestFingerprint: intent.RequestFingerprint, IntentFingerprint: intent.Fingerprint,
		PreviousReceiptFingerprint: previousFingerprint, Generation: intent.Generation,
		State: intent.State, ReasonCode: intent.ReasonCode, Terminal: intent.Terminal,
		RecoveryRequired: intent.RecoveryRequired, Redacted: true,
		RecordedAt: intent.TransitionedAt,
	}
	receipt.Fingerprint = analyzerStartFingerprint(receipt)
	return receipt, ValidateStoredAnalyzerStartLifecycleReceipt(receipt)
}

func ValidateStoredAnalyzerStartLifecycleReceipt(receipt AnalyzerStartLifecycleReceipt) error {
	if receipt.ProtocolVersion != AnalyzerStartLifecycleReceiptProtocolVersion ||
		!validRequestID(receipt.ID) || !validRequestID(receipt.RequestID) ||
		!validRequestID(receipt.RunID) || !validRequestID(receipt.WorkspaceID) ||
		!validDigest(receipt.RequestFingerprint) || !validDigest(receipt.IntentFingerprint) ||
		receipt.Generation == 0 || analyzerStartReason(receipt.State) == "" ||
		receipt.ReasonCode != analyzerStartReason(receipt.State) ||
		receipt.Terminal != analyzerStartTerminal(receipt.State) ||
		receipt.RecoveryRequired != (receipt.State == AnalyzerStartIntentRecoveryRequired) ||
		!receipt.Redacted || receipt.RawRequestIncluded || receipt.RawOutputIncluded ||
		receipt.CommandIncluded || receipt.ProcessHandleIncluded || receipt.ArtifactCommitted ||
		receipt.Authority != (AnalyzerProductAdapterAuthority{}) || receipt.RecordedAt.IsZero() {
		return errors.New("invalid or authority-widening analyzer lifecycle receipt")
	}
	if receipt.Generation == 1 {
		if receipt.PreviousReceiptFingerprint != "" {
			return errors.New("initial analyzer receipt has a predecessor")
		}
	} else if !validDigest(receipt.PreviousReceiptFingerprint) {
		return errors.New("analyzer receipt predecessor is invalid")
	}
	if analyzerStartFingerprint(receipt) != receipt.Fingerprint {
		return errors.New("analyzer lifecycle receipt fingerprint mismatch")
	}
	return nil
}

func DecodeStoredAnalyzerDurableStartRequest(raw []byte) (AnalyzerDurableStartRequest, error) {
	var value AnalyzerDurableStartRequest
	if err := strictAnalyzerStartDecode(raw, &value); err != nil {
		return AnalyzerDurableStartRequest{}, err
	}
	return value, ValidateStoredAnalyzerDurableStartRequest(value)
}

func DecodeStoredAnalyzerStartIntent(raw []byte) (AnalyzerStartIntent, error) {
	var value AnalyzerStartIntent
	if err := strictAnalyzerStartDecode(raw, &value); err != nil {
		return AnalyzerStartIntent{}, err
	}
	return value, ValidateStoredAnalyzerStartIntent(value)
}

func DecodeStoredAnalyzerStartLifecycleReceipt(raw []byte) (AnalyzerStartLifecycleReceipt, error) {
	var value AnalyzerStartLifecycleReceipt
	if err := strictAnalyzerStartDecode(raw, &value); err != nil {
		return AnalyzerStartLifecycleReceipt{}, err
	}
	return value, ValidateStoredAnalyzerStartLifecycleReceipt(value)
}

func buildAnalyzerStartIntentSuccessorUnchecked(previous AnalyzerStartIntent,
	target AnalyzerStartIntentState, at time.Time,
) (AnalyzerStartIntent, error) {
	// Avoid recursive successor validation while retaining the public builder's
	// exact transition semantics.
	at = at.UTC()
	if at.Before(previous.TransitionedAt) {
		return AnalyzerStartIntent{}, errors.New("analyzer start intent time moved backwards")
	}
	consumed := previous.RequestConsumed
	switch {
	case previous.State == AnalyzerStartIntentPrepared && target == AnalyzerStartIntentConsumed:
		if previous.Adapter != AnalyzerStartAdapterFake || !at.Before(previous.ExpiresAt) {
			return AnalyzerStartIntent{}, errors.New("expired or non-fake analyzer request cannot be consumed")
		}
		consumed = true
	case previous.State == AnalyzerStartIntentPrepared && target == AnalyzerStartIntentExpired:
		if at.Before(previous.ExpiresAt) {
			return AnalyzerStartIntent{}, errors.New("analyzer request has not expired")
		}
	case previous.State == AnalyzerStartIntentPrepared && target == AnalyzerStartIntentCancelled:
	case previous.State == AnalyzerStartIntentConsumed &&
		(target == AnalyzerStartIntentFakeSucceeded || target == AnalyzerStartIntentFakeFailed ||
			target == AnalyzerStartIntentRecoveryRequired || target == AnalyzerStartIntentCancelled):
	default:
		return AnalyzerStartIntent{}, errors.New("invalid analyzer start transition")
	}
	next := previous
	next.ID = fmt.Sprintf("%s-intent-%d", previous.RequestID, previous.Generation+1)
	next.Generation++
	next.PreviousIntentFingerprint = previous.Fingerprint
	next.State = target
	next.ReasonCode = analyzerStartReason(target)
	next.RequestConsumed = consumed
	next.Terminal = analyzerStartTerminal(target)
	next.RecoveryRequired = target == AnalyzerStartIntentRecoveryRequired
	next.TransitionedAt = at
	next.Fingerprint = ""
	next.Fingerprint = analyzerStartFingerprint(next)
	return next, nil
}

func validAnalyzerStartAdapter(adapter AnalyzerStartAdapter) bool {
	return adapter == AnalyzerStartAdapterDisabled || adapter == AnalyzerStartAdapterFake
}

func analyzerStartReason(state AnalyzerStartIntentState) string {
	switch state {
	case AnalyzerStartIntentDisabled:
		return analyzerStartReasonDisabled
	case AnalyzerStartIntentPrepared:
		return analyzerStartReasonPrepared
	case AnalyzerStartIntentConsumed:
		return analyzerStartReasonConsumed
	case AnalyzerStartIntentExpired:
		return analyzerStartReasonExpired
	case AnalyzerStartIntentCancelled:
		return analyzerStartReasonCancelled
	case AnalyzerStartIntentFakeSucceeded:
		return analyzerStartReasonFakeSucceeded
	case AnalyzerStartIntentFakeFailed:
		return analyzerStartReasonFakeFailed
	case AnalyzerStartIntentRecoveryRequired:
		return analyzerStartReasonRecoveryRequired
	default:
		return ""
	}
}

func analyzerStartTerminal(state AnalyzerStartIntentState) bool {
	switch state {
	case AnalyzerStartIntentDisabled, AnalyzerStartIntentExpired, AnalyzerStartIntentCancelled,
		AnalyzerStartIntentFakeSucceeded, AnalyzerStartIntentFakeFailed,
		AnalyzerStartIntentRecoveryRequired:
		return true
	default:
		return false
	}
}

func analyzerStartFingerprint(value any) string {
	raw, _ := json.Marshal(value)
	var canonical map[string]any
	_ = json.Unmarshal(raw, &canonical)
	delete(canonical, "fingerprint")
	raw, _ = json.Marshal(canonical)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func strictAnalyzerStartDecode(raw []byte, target any) error {
	if !strictDecode(raw, MaxAnalyzerStartControlEnvelopeBytes, target) {
		return errors.New("analyzer start record is not strict JSON")
	}
	expectedRaw, err := json.Marshal(target)
	if err != nil {
		return err
	}
	var actual, expected any
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(expectedRaw, &expected) != nil ||
		!sameAnalyzerStartJSONShape(actual, expected) {
		return errors.New("analyzer start record has missing or unknown fields")
	}
	return nil
}

func sameAnalyzerStartJSONShape(actual, expected any) bool {
	switch expectedValue := expected.(type) {
	case map[string]any:
		actualValue, ok := actual.(map[string]any)
		if !ok || len(actualValue) != len(expectedValue) {
			return false
		}
		for key, expectedChild := range expectedValue {
			actualChild, found := actualValue[key]
			if !found || !sameAnalyzerStartJSONShape(actualChild, expectedChild) {
				return false
			}
		}
		return true
	case []any:
		actualValue, ok := actual.([]any)
		if !ok || len(actualValue) != len(expectedValue) {
			return false
		}
		for index := range expectedValue {
			if !sameAnalyzerStartJSONShape(actualValue[index], expectedValue[index]) {
				return false
			}
		}
		return true
	default:
		return true
	}
}
