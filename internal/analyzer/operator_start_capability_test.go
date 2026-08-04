package analyzer

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzerOperatorStartCapabilityAuthenticatesButDoesNotIssue(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	request := chain.request
	contract := chain.contract
	if request.ProtocolVersion != AnalyzerOperatorStartCapabilityRequestProtocolVersion ||
		!request.ExactAdmissionBound || !request.OneShotRequired ||
		!request.DurableReplayGuardRequired || !request.CapabilityRequestOnly ||
		!request.StartBlocked || !request.MetadataOnly || request.PathIncluded ||
		request.CommandIncluded || request.ArgvIncluded || request.EnvironmentIncluded ||
		request.InputBodyIncluded || request.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("unexpected request: %+v", request)
	}
	if contract.ProtocolVersion != AnalyzerOperatorStartCapabilityContractProtocolVersion ||
		!contract.RequestCanonical || !contract.OperatorIdentityBound ||
		!contract.DetachedSignatureVerified || !contract.ExactAdmissionBound ||
		!contract.ValidityIntervalBounded || contract.ClockValidityVerified ||
		!contract.OneShotRequired || !contract.DurableReplayGuardRequired ||
		contract.DurableReplayGuardPresent || contract.AtomicConsumptionPresent ||
		contract.CapabilityIssued || contract.CapabilityConsumed || !contract.StartBlocked ||
		!contract.MetadataOnly || contract.ProcessStarterPresent ||
		contract.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("unexpected contract: %+v", contract)
	}
	payload, code := AnalyzerOperatorStartCapabilitySigningPayload(request, chain.input,
		chain.matrix, chain.nonce)
	if code != "" || !ed25519.Verify(chain.operatorPublicKey, payload, chain.detachedSignature) {
		t.Fatalf("payload verification code=%s", code)
	}
	if bytes.Contains(payload, chain.nonce) {
		t.Fatal("signing payload retained raw nonce")
	}
}

func TestAnalyzerOperatorStartCapabilityStrictRoundTrip(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	rawRequest, code := EncodeAnalyzerOperatorStartCapabilityRequest(chain.request, chain.input,
		chain.matrix, chain.nonce)
	if code != "" {
		t.Fatal(code)
	}
	assertExactObjectKeys(t, rawRequest, []string{
		"admission_matrix_sha256", "analyzer", "argv_included", "authority",
		"capability_request_only", "command_included", "durable_replay_guard_required",
		"environment_included", "exact_admission_bound", "executable_sha256",
		"expires_at_unix_ms", "input_body_included", "issued_at_unix_ms", "launch_plan_sha256",
		"metadata_only", "nonce_sha256", "one_shot_required", "operator_identity_sha256",
		"path_included", "protocol_version", "release_candidate_sha256", "request_id",
		"scope_approval_sha256", "start_blocked", "target_goarch", "target_goos",
	})
	decodedRequest, code := DecodeAnalyzerOperatorStartCapabilityRequest(rawRequest, chain.input,
		chain.matrix, chain.nonce)
	if code != "" || decodedRequest.NonceSHA256 != chain.request.NonceSHA256 {
		t.Fatalf("request round trip code=%s value=%+v", code, decodedRequest)
	}

	rawContract, code := EncodeAnalyzerOperatorStartCapabilityContract(chain.contract,
		chain.request, chain.input, chain.matrix, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" {
		t.Fatal(code)
	}
	assertExactObjectKeys(t, rawContract, []string{
		"admission_matrix_sha256", "atomic_consumption_present", "authority",
		"capability_consumed", "capability_issued", "capability_request_sha256",
		"clock_validity_verified", "detached_signature_sha256", "detached_signature_verified",
		"durable_replay_guard_present", "durable_replay_guard_required", "exact_admission_bound",
		"expires_at_unix_ms", "issued_at_unix_ms", "metadata_only", "nonce_sha256",
		"one_shot_required", "operator_identity_bound", "operator_identity_sha256",
		"process_starter_present", "protocol_version", "public_key_sha256", "request_canonical",
		"scope_approval_sha256", "signature_scheme", "start_blocked", "validity_interval_bounded",
	})
	decodedContract, code := DecodeAnalyzerOperatorStartCapabilityContract(rawContract,
		chain.request, chain.input, chain.matrix, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" || decodedContract.DetachedSignatureSHA256 != chain.contract.DetachedSignatureSHA256 {
		t.Fatalf("contract round trip code=%s value=%+v", code, decodedContract)
	}
	if bytes.Contains(rawContract, chain.detachedSignature) || bytes.Contains(rawContract, chain.nonce) {
		t.Fatal("contract retained raw credential material")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawContract, &fields); err != nil {
		t.Fatal(err)
	}
	assertExactObjectKeys(t, fields["authority"], []string{
		"artifact_commit", "capability_issue", "execution", "host_filesystem", "network",
		"operator_override", "persistence", "process_start", "product_invocation",
		"recovery_apply", "secret_access",
	})
}

func TestAnalyzerOperatorStartCapabilityRejectsForgeryReplayWideningAndSchemaDrift(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)

	zeroNonce := make([]byte, AnalyzerOperatorStartCapabilityNonceBytes)
	if _, code := BuildAnalyzerOperatorStartCapabilityRequest(chain.input, chain.matrix, zeroNonce,
		chain.request.IssuedAtUnixMillis, chain.request.ExpiresAtUnixMillis); code != CodeInvalidContent {
		t.Fatalf("zero nonce code = %s", code)
	}
	if _, code := BuildAnalyzerOperatorStartCapabilityRequest(chain.input, chain.matrix, chain.nonce,
		chain.request.IssuedAtUnixMillis,
		chain.request.IssuedAtUnixMillis+AnalyzerOperatorStartCapabilityMaxValidityMillis+1); code != CodeInvalidContent {
		t.Fatalf("unbounded validity code = %s", code)
	}

	forgedSignature := append([]byte(nil), chain.detachedSignature...)
	forgedSignature[0] ^= 0xff
	if _, code := BuildAnalyzerOperatorStartCapabilityContract(chain.request, chain.input,
		chain.matrix, chain.nonce, chain.operatorPublicKey, forgedSignature); code != CodeInvalidResult {
		t.Fatalf("forged signature code = %s", code)
	}
	otherPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x13}, ed25519.SeedSize))
	otherPublic := otherPrivate.Public().(ed25519.PublicKey)
	if _, code := BuildAnalyzerOperatorStartCapabilityContract(chain.request, chain.input,
		chain.matrix, chain.nonce, otherPublic, chain.detachedSignature); code != CodeInvalidResult {
		t.Fatalf("foreign operator key code = %s", code)
	}

	widened := chain.contract
	widened.CapabilityIssued = true
	if code := ValidateAnalyzerOperatorStartCapabilityContract(widened, chain.request, chain.input,
		chain.matrix, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature); code != CodeInvalidResult {
		t.Fatalf("capability widening code = %s", code)
	}
	mutatedRequest := chain.request
	mutatedRequest.NonceSHA256 = strings.Repeat("a", 64)
	if code := ValidateAnalyzerOperatorStartCapabilityRequest(mutatedRequest, chain.input,
		chain.matrix, chain.nonce); code != CodeInvalidResult {
		t.Fatalf("request mutation code = %s", code)
	}

	raw, code := EncodeAnalyzerOperatorStartCapabilityContract(chain.contract, chain.request,
		chain.input, chain.matrix, chain.nonce, chain.operatorPublicKey, chain.detachedSignature)
	if code != "" {
		t.Fatal(code)
	}
	text := string(raw)
	cases := map[string]string{
		"unknown":   strings.Replace(text, `"protocol_version":`, `"unknown":false,"protocol_version":`, 1),
		"missing":   strings.Replace(text, `"capability_issued":false,`, "", 1),
		"duplicate": strings.Replace(text, `"start_blocked":true`, `"start_blocked":true,"start_blocked":true`, 1),
		"future": strings.Replace(text, AnalyzerOperatorStartCapabilityContractProtocolVersion,
			"analyzer_operator_start_capability_contract.v2", 1),
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeAnalyzerOperatorStartCapabilityContract([]byte(candidate), chain.request,
				chain.input, chain.matrix, chain.nonce, chain.operatorPublicKey,
				chain.detachedSignature); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}
}

type analyzerAuthenticatedStartCapabilityChain struct {
	input              AnalyzerProductAdapterEvidenceInput
	matrix             AnalyzerProductAdapterAdmissionMatrix
	operatorPrivateKey ed25519.PrivateKey
	operatorPublicKey  ed25519.PublicKey
	nonce              []byte
	request            AnalyzerOperatorStartCapabilityRequest
	detachedSignature  []byte
	contract           AnalyzerOperatorStartCapabilityContract
}

func mustAnalyzerAuthenticatedStartCapabilityChain(t *testing.T) analyzerAuthenticatedStartCapabilityChain {
	t.Helper()
	chain := mustAnalyzerSignedReleaseChain(t)
	operatorPrivateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x73}, ed25519.SeedSize))
	operatorPublicKey := append(ed25519.PublicKey(nil),
		operatorPrivateKey.Public().(ed25519.PublicKey)...)
	operatorIdentity := analyzerProvenanceBytesSHA256(operatorPublicKey)
	plan, code := BuildAnalyzerLaunchPlan(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release)
	if code != "" {
		t.Fatal(code)
	}
	review, code := ReviewAnalyzerLaunchPlan(plan, chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, chain.manifest, chain.allowlist,
		chain.release, operatorIdentity, AnalyzerLaunchPlanReviewConfirmation)
	if code != "" {
		t.Fatal(code)
	}
	approval, code := BuildAnalyzerScopeLimitsApproval(chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release, chain.rawStatement, chain.publicKey, chain.signature,
		chain.verification, plan, review, operatorIdentity,
		AnalyzerScopeLimitsApprovalConfirmation)
	if code != "" {
		t.Fatal(code)
	}
	input := AnalyzerProductAdapterEvidenceInput{
		Candidate: chain.candidate, RawRequest: chain.raw, Executable: chain.executable,
		Identity: chain.identity, Preflight: chain.preflight, FormatEvidence: chain.evidence,
		Manifest: chain.manifest, Allowlist: chain.allowlist, Release: chain.release,
		ProvenanceStatement: chain.rawStatement, ProvenancePublicKey: chain.publicKey,
		ProvenanceSignature: chain.signature, ProvenanceVerification: chain.verification,
		LaunchPlan: plan, LaunchPlanReview: review, ScopeApproval: approval,
		ThreatModel: BuildProductAdapterThreatModel(),
	}
	matrix, code := BuildAnalyzerProductAdapterAdmissionMatrix(input)
	if code != "" {
		t.Fatal(code)
	}
	nonce := bytes.Repeat([]byte{0xa5}, AnalyzerOperatorStartCapabilityNonceBytes)
	issuedAt := int64(1_800_000_000_000)
	request, code := BuildAnalyzerOperatorStartCapabilityRequest(input, matrix, nonce, issuedAt,
		issuedAt+60_000)
	if code != "" {
		t.Fatal(code)
	}
	payload, code := AnalyzerOperatorStartCapabilitySigningPayload(request, input, matrix, nonce)
	if code != "" {
		t.Fatal(code)
	}
	detachedSignature := ed25519.Sign(operatorPrivateKey, payload)
	contract, code := BuildAnalyzerOperatorStartCapabilityContract(request, input, matrix, nonce,
		operatorPublicKey, detachedSignature)
	if code != "" {
		t.Fatal(code)
	}
	return analyzerAuthenticatedStartCapabilityChain{
		input: input, matrix: matrix, operatorPrivateKey: operatorPrivateKey,
		operatorPublicKey: operatorPublicKey, nonce: nonce, request: request,
		detachedSignature: detachedSignature, contract: contract,
	}
}
