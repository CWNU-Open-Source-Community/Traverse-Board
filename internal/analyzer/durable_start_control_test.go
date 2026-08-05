package analyzer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAnalyzerDurableStartControlBuildsFakeWriteAheadLifecycle(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	registeredAt := time.UnixMilli(chain.request.IssuedAtUnixMillis + 1_000).UTC()
	request, code := BuildAnalyzerDurableStartRequest("analyzer-start-request", "run-start",
		"ws-start", chain.request, chain.contract, chain.input, chain.matrix, chain.nonce,
		chain.operatorPublicKey, chain.detachedSignature, AnalyzerStartAdapterFake, registeredAt)
	if code != "" {
		t.Fatal(code)
	}
	if request.AtomicConsumptionPresent || request.CapabilityIssued ||
		!request.DurableReplayGuardPresent || !request.StartBlocked {
		t.Fatalf("unexpected durable request authority: %+v", request)
	}
	prepared, err := BuildInitialAnalyzerStartIntent(request, registeredAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := BuildAnalyzerStartIntentSuccessor(prepared,
		AnalyzerStartIntentConsumed, prepared.TransitionedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := BuildAnalyzerStartIntentSuccessor(consumed,
		AnalyzerStartIntentFakeSucceeded, consumed.TransitionedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Terminal || !completed.RequestConsumed || completed.ProcessObserved ||
		completed.ProcessStartAuthorized || completed.ArtifactCommitAuthorized {
		t.Fatalf("fake lifecycle widened authority: %+v", completed)
	}
	first, err := BuildAnalyzerStartLifecycleReceipt(prepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildAnalyzerStartLifecycleReceipt(consumed, &first)
	if err != nil {
		t.Fatal(err)
	}
	third, err := BuildAnalyzerStartLifecycleReceipt(completed, &second)
	if err != nil {
		t.Fatal(err)
	}
	if third.PreviousReceiptFingerprint != second.Fingerprint || !third.Redacted ||
		third.RawRequestIncluded || third.RawOutputIncluded || third.ProcessHandleIncluded {
		t.Fatalf("receipt chain mismatch: %+v", third)
	}
}

func TestAnalyzerDurableStartControlRejectsReplayWideningAndSchemaDrift(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	registeredAt := time.UnixMilli(chain.request.IssuedAtUnixMillis + 1_000).UTC()
	request, code := BuildAnalyzerDurableStartRequest("analyzer-disabled-request", "run-disabled",
		"ws-disabled", chain.request, chain.contract, chain.input, chain.matrix, chain.nonce,
		chain.operatorPublicKey, chain.detachedSignature, AnalyzerStartAdapterDisabled, registeredAt)
	if code != "" {
		t.Fatal(code)
	}
	disabled, err := BuildInitialAnalyzerStartIntent(request, registeredAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildAnalyzerStartIntentSuccessor(disabled,
		AnalyzerStartIntentConsumed, registeredAt.Add(2*time.Millisecond)); err == nil {
		t.Fatal("disabled adapter was consumed")
	}
	widened := disabled
	widened.ProcessStartAuthorized = true
	widened.Fingerprint = ""
	widened.Fingerprint = analyzerStartFingerprint(widened)
	if err := ValidateStoredAnalyzerStartIntent(widened); err == nil {
		t.Fatal("process authority widening passed validation")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(raw), `"protocol_version":`,
		`"unknown":false,"protocol_version":`, 1)
	if _, err := DecodeStoredAnalyzerDurableStartRequest([]byte(unknown)); err == nil {
		t.Fatal("unknown durable request field passed strict decoding")
	}
	if _, code := BuildAnalyzerDurableStartRequest("expired-request", "run-expired", "ws-expired",
		chain.request, chain.contract, chain.input, chain.matrix, chain.nonce,
		chain.operatorPublicKey, chain.detachedSignature, AnalyzerStartAdapterFake,
		time.UnixMilli(chain.request.ExpiresAtUnixMillis).UTC()); code == "" {
		t.Fatal("expired signed request was accepted")
	}
}

func TestAnalyzerStartControlRejectsImpossibleStoredStateAndReceiptAncestry(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	registeredAt := time.UnixMilli(chain.request.IssuedAtUnixMillis + 1_000).UTC()
	request, code := BuildAnalyzerDurableStartRequest("analyzer-ancestry-request",
		"run-ancestry", "ws-ancestry", chain.request, chain.contract, chain.input,
		chain.matrix, chain.nonce, chain.operatorPublicKey, chain.detachedSignature,
		AnalyzerStartAdapterFake, registeredAt)
	if code != "" {
		t.Fatal(code)
	}
	prepared, err := BuildInitialAnalyzerStartIntent(request, registeredAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := BuildAnalyzerStartIntentSuccessor(prepared,
		AnalyzerStartIntentConsumed, prepared.TransitionedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	impossible := consumed
	impossible.State = AnalyzerStartIntentState("invented")
	impossible.ReasonCode = ""
	impossible.Terminal = false
	impossible.Fingerprint = ""
	impossible.Fingerprint = analyzerStartFingerprint(impossible)
	if err := ValidateStoredAnalyzerStartIntent(impossible); err == nil {
		t.Fatal("invented stored state passed validation")
	}

	firstReceipt, err := BuildAnalyzerStartLifecycleReceipt(prepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	forged := firstReceipt
	forged.RunID += "-other"
	forged.Fingerprint = ""
	forged.Fingerprint = analyzerStartFingerprint(forged)
	if _, err := BuildAnalyzerStartLifecycleReceipt(consumed, &forged); err == nil {
		t.Fatal("cross-run receipt predecessor passed validation")
	}
}

func TestAnalyzerStartIntentExpiryAndRecoveryTransitionsAreClosed(t *testing.T) {
	chain := mustAnalyzerAuthenticatedStartCapabilityChain(t)
	registeredAt := time.UnixMilli(chain.request.IssuedAtUnixMillis + 1_000).UTC()
	request, code := BuildAnalyzerDurableStartRequest("analyzer-recovery-request", "run-recovery",
		"ws-recovery", chain.request, chain.contract, chain.input, chain.matrix, chain.nonce,
		chain.operatorPublicKey, chain.detachedSignature, AnalyzerStartAdapterFake, registeredAt)
	if code != "" {
		t.Fatal(code)
	}
	prepared, err := BuildInitialAnalyzerStartIntent(request, registeredAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := BuildAnalyzerStartIntentSuccessor(prepared, AnalyzerStartIntentExpired,
		request.ExpiresAt)
	if err != nil || !expired.Terminal || expired.RequestConsumed {
		t.Fatalf("expiry transition mismatch: %+v err=%v", expired, err)
	}
	consumed, err := BuildAnalyzerStartIntentSuccessor(prepared, AnalyzerStartIntentConsumed,
		prepared.TransitionedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := BuildAnalyzerStartIntentSuccessor(consumed,
		AnalyzerStartIntentRecoveryRequired, consumed.TransitionedAt.Add(time.Millisecond))
	if err != nil || !recovery.Terminal || !recovery.RecoveryRequired ||
		recovery.ProcessObserved {
		t.Fatalf("recovery transition mismatch: %+v err=%v", recovery, err)
	}
}
