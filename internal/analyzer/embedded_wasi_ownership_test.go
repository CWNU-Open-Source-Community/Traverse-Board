package analyzer

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

func TestAnalyzerEmbeddedWASIOwnershipAndReleaseRemainNonStarting(t *testing.T) {
	profile := BuildAnalyzerEmbeddedWASIProfile()
	assessment, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), minimalAnalyzerWASM(), profile)
	if code != "" {
		t.Fatalf("assess module: %s", code)
	}
	ownership, code := BuildAnalyzerEmbeddedWASIOwnership(profile, assessment)
	if code != "" {
		t.Fatalf("build ownership: %s", code)
	}
	if !ownership.RuntimePerInvocation || !ownership.CompiledModulePerInvocation ||
		!ownership.GuestInstancePerInvocation || ownership.CrossRunReuse ||
		ownership.NativeProcessPresent || ownership.PIDPresent || ownership.ProcessTreePresent ||
		ownership.BackgroundGuestAllowed || ownership.AutomaticRestartAllowed ||
		ownership.ForeignResourceCleanup || ownership.HostCrashLeavesGuest ||
		ownership.ConsumedRequestAutoReplay || !ownership.RetryRequiresSignedRequest ||
		!ownership.RecoveryMetadataOnly || !ownership.StartBlocked ||
		ownership.ProductInvocationAuthorized || ownership.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("ownership widened runtime authority: %#v", ownership)
	}

	decision, code := BuildAnalyzerEmbeddedWASIReleaseDecision(profile, assessment, ownership)
	if code != "" {
		t.Fatalf("build release decision: %s", code)
	}
	if len(decision.Gates) != 7 || decision.RequiredGateCount != 7 || decision.OpenGateCount != 7 ||
		!decision.NonStartingDecision || decision.Ready || !decision.StartBlocked ||
		decision.ProductInvocationAuthorized || decision.Authority != (AnalyzerProductAdapterAuthority{}) {
		t.Fatalf("release decision widened authority: %#v", decision)
	}
	for _, gate := range decision.Gates {
		if !gate.Required || gate.Implemented || gate.Verified || !gate.BlocksProductStart {
			t.Fatalf("release gate is not fail closed: %#v", gate)
		}
	}

	ownershipRaw, code := EncodeAnalyzerEmbeddedWASIOwnership(ownership, profile, assessment)
	if code != "" {
		t.Fatalf("encode ownership: %s", code)
	}
	decodedOwnership, code := DecodeAnalyzerEmbeddedWASIOwnership(ownershipRaw, profile, assessment)
	if code != "" || !reflect.DeepEqual(decodedOwnership, ownership) {
		t.Fatalf("ownership round trip drifted: code=%s value=%#v", code, decodedOwnership)
	}
	decisionRaw, code := EncodeAnalyzerEmbeddedWASIReleaseDecision(decision, profile, assessment, ownership)
	if code != "" {
		t.Fatalf("encode release decision: %s", code)
	}
	decodedDecision, code := DecodeAnalyzerEmbeddedWASIReleaseDecision(decisionRaw, profile, assessment, ownership)
	if code != "" || !reflect.DeepEqual(decodedDecision, decision) {
		t.Fatalf("release round trip drifted: code=%s value=%#v", code, decodedDecision)
	}

	widened := bytes.Replace(ownershipRaw, []byte(`"background_guest_allowed":false`), []byte(`"background_guest_allowed":true`), 1)
	if _, code = DecodeAnalyzerEmbeddedWASIOwnership(widened, profile, assessment); code != CodeInvalidResult {
		t.Fatalf("background guest widening accepted: %s", code)
	}
	unknown := bytes.Replace(decisionRaw, []byte(`"ready":false`), []byte(`"unknown":false,"ready":false`), 1)
	if _, code = DecodeAnalyzerEmbeddedWASIReleaseDecision(unknown, profile, assessment, ownership); code != CodeInvalidResult {
		t.Fatalf("unknown release field accepted: %s", code)
	}
}

func TestAnalyzerEmbeddedWASIOwnershipRejectsFailedAssessment(t *testing.T) {
	profile := BuildAnalyzerEmbeddedWASIProfile()
	assessment, code := AssessAnalyzerEmbeddedWASIModule(context.Background(), analyzerWASMWithEvilFunctionImport(), profile)
	if code != CodeCapabilityDenied || assessment.Passed {
		t.Fatalf("expected rejected assessment: code=%s assessment=%#v", code, assessment)
	}
	if _, code = BuildAnalyzerEmbeddedWASIOwnership(profile, assessment); code != CodeCapabilityDenied {
		t.Fatalf("rejected assessment gained ownership: %s", code)
	}
}
