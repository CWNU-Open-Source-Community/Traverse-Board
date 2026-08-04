package analyzer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzerProductAdapterRecoveryAcceptanceDefinesBlockedScenarios(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	acceptance, code := BuildAnalyzerProductAdapterRecoveryAcceptance(chain.input, chain.matrix,
		chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" {
		t.Fatal(code)
	}
	if acceptance.ProtocolVersion != AnalyzerProductAdapterRecoveryAcceptanceProtocolVersion ||
		acceptance.RequiredScenarioCount != 10 || acceptance.ProductionVerifiedCount != 0 ||
		acceptance.OpenScenarioCount != 10 || !acceptance.ExactContractsBound ||
		!acceptance.AllScenariosRequired || acceptance.ProductionAcceptanceComplete ||
		acceptance.WriteAheadIntentPresent || acceptance.DurableGenerationFencePresent ||
		acceptance.PersistentLifecycleStorePresent || acceptance.CleanupExecutorPresent ||
		acceptance.RecoveryReady || !acceptance.StartBlocked || !acceptance.ApplyBlocked ||
		!acceptance.MetadataOnly || acceptance.ProductAdapterPresent ||
		acceptance.ProcessStarterPresent ||
		acceptance.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("unexpected recovery acceptance: %+v", acceptance)
	}

	seen := make(map[string]AnalyzerProductAdapterRecoveryScenario, len(acceptance.Scenarios))
	for _, scenario := range acceptance.Scenarios {
		if _, duplicate := seen[scenario.ID]; duplicate {
			t.Fatalf("duplicate scenario %q", scenario.ID)
		}
		if scenario.ProductionEvidenceVerified || !scenario.BlocksProductStart ||
			!scenario.ForeignResourcesProtected || !scenario.IdempotentReplayRequired {
			t.Fatalf("scenario widened recovery authority: %+v", scenario)
		}
		seen[scenario.ID] = scenario
	}
	if got := seen["orphan_process_tree"]; !got.ExactProcessIdentityRequired ||
		!got.ProcessTreeQuiescenceRequired || got.RequiredDisposition == "" {
		t.Fatalf("orphan scenario = %+v", got)
	}
	if got := seen["foreign_staging_collision"]; got.RequiredDisposition !=
		"preserve_foreign_resources_and_record_terminal_failure" {
		t.Fatalf("foreign collision scenario = %+v", got)
	}
	if got := seen["replay_after_terminal"]; !strings.Contains(got.RequiredDisposition,
		"without_side_effect") {
		t.Fatalf("terminal replay scenario = %+v", got)
	}
}

func TestAnalyzerProductAdapterRecoveryAcceptanceStrictRoundTrip(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	acceptance, code := BuildAnalyzerProductAdapterRecoveryAcceptance(chain.input, chain.matrix,
		chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" {
		t.Fatal(code)
	}
	raw, code := EncodeAnalyzerProductAdapterRecoveryAcceptance(acceptance, chain.input,
		chain.matrix, chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" {
		t.Fatal(code)
	}
	assertExactObjectKeys(t, raw, []string{
		"admission_matrix_sha256", "all_scenarios_required", "analyzer", "apply_blocked",
		"authority", "capability_contract_sha256", "capability_request_sha256",
		"cleanup_executor_present", "durable_generation_fence_present", "exact_contracts_bound",
		"executable_sha256", "metadata_only", "open_scenario_count",
		"persistent_lifecycle_store_present", "process_starter_present", "product_adapter_present",
		"production_acceptance_complete", "production_verified_count", "protocol_version",
		"recovery_ready", "request_id", "required_scenario_count", "scenarios",
		"scope_approval_sha256", "start_blocked", "target_goarch", "target_goos",
		"write_ahead_intent_present",
	})
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var scenarios []json.RawMessage
	if err := json.Unmarshal(fields["scenarios"], &scenarios); err != nil || len(scenarios) != 10 {
		t.Fatalf("scenarios: %v len=%d", err, len(scenarios))
	}
	assertExactObjectKeys(t, scenarios[0], []string{
		"blocks_product_start", "exact_process_identity_required", "foreign_resources_protected",
		"generation_fence_required", "id", "idempotent_replay_required",
		"no_replace_handoff_required", "process_tree_quiescence_required",
		"production_evidence_verified", "required_disposition", "trigger",
		"write_ahead_intent_required",
	})
	decoded, code := DecodeAnalyzerProductAdapterRecoveryAcceptance(raw, chain.input,
		chain.matrix, chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" || decoded.CapabilityContractSHA256 != acceptance.CapabilityContractSHA256 {
		t.Fatalf("round trip code=%s value=%+v", code, decoded)
	}
}

func TestAnalyzerProductAdapterRecoveryAcceptanceRejectsWideningAndDrift(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	acceptance, code := BuildAnalyzerProductAdapterRecoveryAcceptance(chain.input, chain.matrix,
		chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" {
		t.Fatal(code)
	}

	widened := acceptance
	widened.RecoveryReady = true
	if got := ValidateAnalyzerProductAdapterRecoveryAcceptance(widened, chain.input, chain.matrix,
		chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature); got != CodeInvalidResult {
		t.Fatalf("recovery widening code = %s", got)
	}
	widened = acceptance
	widened.Scenarios = append([]AnalyzerProductAdapterRecoveryScenario(nil), acceptance.Scenarios...)
	widened.Scenarios[0].ProductionEvidenceVerified = true
	if got := ValidateAnalyzerProductAdapterRecoveryAcceptance(widened, chain.input, chain.matrix,
		chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature); got != CodeInvalidResult {
		t.Fatalf("scenario widening code = %s", got)
	}
	forgedContract := chain.contract
	forgedContract.CapabilityIssued = true
	if _, got := BuildAnalyzerProductAdapterRecoveryAcceptance(chain.input, chain.matrix,
		chain.request, forgedContract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature); got != CodeInvalidResult {
		t.Fatalf("forged contract code = %s", got)
	}

	raw, code := EncodeAnalyzerProductAdapterRecoveryAcceptance(acceptance, chain.input,
		chain.matrix, chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
		chain.detachedSignature)
	if code != "" {
		t.Fatal(code)
	}
	text := string(raw)
	cases := map[string]string{
		"unknown":   strings.Replace(text, `"protocol_version":`, `"unknown":false,"protocol_version":`, 1),
		"missing":   strings.Replace(text, `"recovery_ready":false,`, "", 1),
		"duplicate": strings.Replace(text, `"apply_blocked":true`, `"apply_blocked":true,"apply_blocked":true`, 1),
		"future": strings.Replace(text, AnalyzerProductAdapterRecoveryAcceptanceProtocolVersion,
			"analyzer_product_adapter_recovery_acceptance.v2", 1),
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeAnalyzerProductAdapterRecoveryAcceptance([]byte(candidate), chain.input,
				chain.matrix, chain.request, chain.contract, chain.nonce, chain.operatorPublicKey,
				chain.detachedSignature); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}
}
