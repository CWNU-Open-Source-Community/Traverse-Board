package analyzer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzerProductAdapterAdmissionMatrixClassifiesEvidenceWithoutAuthority(t *testing.T) {
	input := mustAnalyzerProductAdapterEvidenceInput(t)
	matrix, code := BuildAnalyzerProductAdapterAdmissionMatrix(input)
	if code != "" {
		t.Fatal(code)
	}
	if matrix.ProtocolVersion != AnalyzerProductAdapterAdmissionProtocolVersion ||
		matrix.RequiredControlCount != 20 || matrix.CandidateEvidenceCount != 18 ||
		matrix.TestConformanceCount != 13 || matrix.ProductionVerifiedCount != 0 ||
		matrix.OpenRequirementCount != 20 || !matrix.ExactEvidenceBound ||
		!matrix.AllControlsRequired || matrix.AllProductionEvidenceVerified ||
		matrix.AdmissionReady || !matrix.StartBlocked || !matrix.MetadataOnly ||
		matrix.ProductAdapterPresent || matrix.ProcessStarterPresent ||
		matrix.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("unexpected admission matrix: %+v", matrix)
	}

	seen := make(map[string]AnalyzerProductAdapterAdmissionControl, len(matrix.Controls))
	for _, control := range matrix.Controls {
		if _, duplicate := seen[control.ControlID]; duplicate {
			t.Fatalf("duplicate control %q", control.ControlID)
		}
		if !control.ProductionEvidenceRequired || control.ProductionEvidenceVerified ||
			!control.BlocksProductStart {
			t.Fatalf("control widened production authority: %+v", control)
		}
		seen[control.ControlID] = control
	}
	if got := seen["executable_format"]; got.Status != AnalyzerAdmissionStatusCandidateMetadata ||
		!got.CandidateEvidencePresent || got.TestConformanceOnly {
		t.Fatalf("format classification = %+v", got)
	}
	if got := seen["least_privilege_identity"]; got.Status != AnalyzerAdmissionStatusTestConformance ||
		!got.CandidateEvidencePresent || !got.TestConformanceOnly {
		t.Fatalf("identity classification = %+v", got)
	}
	if got := seen["durable_intent_recovery"]; got.Status != ProductAdapterControlStatusRequired ||
		got.CandidateEvidencePresent || got.TestConformanceOnly ||
		got.EvidenceSource != AnalyzerAdmissionEvidenceSourceMissing {
		t.Fatalf("durable recovery classification = %+v", got)
	}
}

func TestAnalyzerProductAdapterAdmissionMatrixStrictRoundTrip(t *testing.T) {
	input := mustAnalyzerProductAdapterEvidenceInput(t)
	transient, err := json.Marshal(input)
	if err != nil || string(transient) != "{}" {
		t.Fatalf("transient evidence serialized: %s err=%v", transient, err)
	}
	matrix, code := BuildAnalyzerProductAdapterAdmissionMatrix(input)
	if code != "" {
		t.Fatal(code)
	}
	raw, code := EncodeAnalyzerProductAdapterAdmissionMatrix(matrix, input)
	if code != "" {
		t.Fatal(code)
	}
	assertExactObjectKeys(t, raw, []string{
		"admission_ready", "all_controls_required", "all_production_evidence_verified",
		"analyzer", "authority", "candidate_evidence_count", "candidate_sha256", "controls",
		"exact_evidence_bound", "executable_sha256", "launch_plan_review_sha256",
		"launch_plan_sha256", "metadata_only", "open_requirement_count",
		"process_starter_present", "product_adapter_present", "production_verified_count",
		"protocol_version", "provenance_verification_sha256", "release_candidate_sha256",
		"request_id", "required_control_count", "scope_approval_sha256", "start_blocked",
		"target_goarch", "target_goos", "test_conformance_count", "threat_model_sha256",
	})
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	assertExactObjectKeys(t, fields["authority"], []string{
		"artifact_commit", "capability_issue", "execution", "host_filesystem", "network",
		"operator_override", "persistence", "process_start", "product_invocation",
		"recovery_apply", "secret_access",
	})
	var controls []json.RawMessage
	if err := json.Unmarshal(fields["controls"], &controls); err != nil || len(controls) != 20 {
		t.Fatalf("controls: %v len=%d", err, len(controls))
	}
	assertExactObjectKeys(t, controls[0], []string{
		"blocks_product_start", "candidate_evidence_present", "control_id", "evidence_source",
		"production_evidence_required", "production_evidence_verified", "status",
		"test_conformance_only",
	})
	decoded, code := DecodeAnalyzerProductAdapterAdmissionMatrix(raw, input)
	if code != "" || decoded.ScopeApprovalSHA256 != matrix.ScopeApprovalSHA256 {
		t.Fatalf("round trip code=%s decoded=%+v", code, decoded)
	}
}

func TestAnalyzerProductAdapterAdmissionMatrixRejectsDrift(t *testing.T) {
	input := mustAnalyzerProductAdapterEvidenceInput(t)
	matrix, code := BuildAnalyzerProductAdapterAdmissionMatrix(input)
	if code != "" {
		t.Fatal(code)
	}

	widened := matrix
	widened.AdmissionReady = true
	if got := ValidateAnalyzerProductAdapterAdmissionMatrix(widened, input); got != CodeInvalidResult {
		t.Fatalf("widened matrix code = %s", got)
	}
	widened = matrix
	widened.Controls = append([]AnalyzerProductAdapterAdmissionControl(nil), matrix.Controls...)
	widened.Controls[0].ProductionEvidenceVerified = true
	if got := ValidateAnalyzerProductAdapterAdmissionMatrix(widened, input); got != CodeInvalidResult {
		t.Fatalf("widened control code = %s", got)
	}
	tamperedInput := input
	tamperedInput.Executable = append([]byte(nil), input.Executable...)
	tamperedInput.Executable[0] ^= 0xff
	if _, got := BuildAnalyzerProductAdapterAdmissionMatrix(tamperedInput); got != CodeInvalidResult {
		t.Fatalf("tampered evidence code = %s", got)
	}

	raw, code := EncodeAnalyzerProductAdapterAdmissionMatrix(matrix, input)
	if code != "" {
		t.Fatal(code)
	}
	text := string(raw)
	cases := map[string]string{
		"unknown":   strings.Replace(text, `"protocol_version":`, `"unknown":false,"protocol_version":`, 1),
		"missing":   strings.Replace(text, `"admission_ready":false,`, "", 1),
		"duplicate": strings.Replace(text, `"start_blocked":true`, `"start_blocked":true,"start_blocked":true`, 1),
		"future": strings.Replace(text, AnalyzerProductAdapterAdmissionProtocolVersion,
			"analyzer_product_adapter_admission_matrix.v2", 1),
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeAnalyzerProductAdapterAdmissionMatrix([]byte(candidate), input); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}
}

func mustAnalyzerProductAdapterEvidenceInput(t *testing.T) AnalyzerProductAdapterEvidenceInput {
	t.Helper()
	chain := mustAnalyzerScopeApprovalChain(t)
	return AnalyzerProductAdapterEvidenceInput{
		Candidate: chain.candidate, RawRequest: chain.raw, Executable: chain.executable,
		Identity: chain.identity, Preflight: chain.preflight, FormatEvidence: chain.evidence,
		Manifest: chain.manifest, Allowlist: chain.allowlist, Release: chain.release,
		ProvenanceStatement: chain.rawStatement, ProvenancePublicKey: chain.publicKey,
		ProvenanceSignature: chain.signature, ProvenanceVerification: chain.verification,
		LaunchPlan: chain.plan, LaunchPlanReview: chain.review, ScopeApproval: chain.approval,
		ThreatModel: BuildProductAdapterThreatModel(),
	}
}
