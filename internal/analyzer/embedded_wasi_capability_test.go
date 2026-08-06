package analyzer

import (
	"bytes"
	"testing"
	"time"
)

func TestAnalyzerExecutionCapabilityBindsAndConsumesExactRequest(t *testing.T) {
	candidate, code := BuildInvocationCandidate(testRequestJSON(t))
	if code != "" {
		t.Fatal(code)
	}
	token := bytes.Repeat([]byte{0x42}, AnalyzerExecutionCapabilityTokenBytes)
	now := time.Now().UTC().Round(time.Millisecond)
	capability, code := BuildAnalyzerExecutionCapability("analyzer-capability-1", "run-1",
		"workspace-1", candidate, token, now, now.Add(time.Minute))
	if code != "" {
		t.Fatal(code)
	}
	consumption, code := BuildAnalyzerExecutionConsumption("analyzer-consumption-1",
		capability, token, candidate, now.Add(time.Second))
	if code != "" {
		t.Fatal(code)
	}
	if code := ValidateAnalyzerExecutionConsumption(consumption, capability); code != "" {
		t.Fatal(code)
	}
	encoded, code := EncodeAnalyzerExecutionCapability(capability)
	if code != "" {
		t.Fatal(code)
	}
	decoded, code := DecodeAnalyzerExecutionCapability(encoded)
	if code != "" || !AnalyzerExecutionCapabilityEqual(decoded, capability) {
		t.Fatalf("capability round trip failed: code=%s value=%+v", code, decoded)
	}
	encodedConsumption, code := EncodeAnalyzerExecutionConsumption(consumption, capability)
	if code != "" {
		t.Fatal(code)
	}
	if _, code := DecodeAnalyzerExecutionConsumption(encodedConsumption, capability); code != "" {
		t.Fatal(code)
	}
}

func TestAnalyzerExecutionCapabilityRejectsWrongBearerRequestAndExpiry(t *testing.T) {
	raw := testRequestJSON(t)
	candidate, _ := BuildInvocationCandidate(raw)
	token := bytes.Repeat([]byte{0x22}, AnalyzerExecutionCapabilityTokenBytes)
	now := time.Now().UTC().Round(time.Millisecond)
	capability, code := BuildAnalyzerExecutionCapability("analyzer-capability-2", "run-1",
		"workspace-1", candidate, token, now, now.Add(time.Minute))
	if code != "" {
		t.Fatal(code)
	}
	wrongToken := bytes.Repeat([]byte{0x23}, AnalyzerExecutionCapabilityTokenBytes)
	if _, code := BuildAnalyzerExecutionConsumption("analyzer-consumption-wrong-token",
		capability, wrongToken, candidate, now.Add(time.Second)); code != CodeCapabilityDenied {
		t.Fatalf("wrong bearer returned %q", code)
	}
	changed := candidate
	changed.RequestSHA256 = string(bytes.Repeat([]byte{'a'}, 64))
	if _, code := BuildAnalyzerExecutionConsumption("analyzer-consumption-wrong-request",
		capability, token, changed, now.Add(time.Second)); code != CodeCapabilityDenied {
		t.Fatalf("wrong request returned %q", code)
	}
	if _, code := BuildAnalyzerExecutionConsumption("analyzer-consumption-expired",
		capability, token, candidate, capability.ExpiresAt); code != CodeDeadlineExceeded {
		t.Fatalf("expired capability returned %q", code)
	}
}

func TestAnalyzerExecutionBearerEncodingIsCanonical(t *testing.T) {
	token := bytes.Repeat([]byte{0x17}, AnalyzerExecutionCapabilityTokenBytes)
	encoded, err := EncodeAnalyzerExecutionBearerToken(token)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAnalyzerExecutionBearerToken(encoded)
	if err != nil || !bytes.Equal(decoded, token) {
		t.Fatalf("bearer round trip failed: %v", err)
	}
	if _, err := DecodeAnalyzerExecutionBearerToken(encoded + "="); err == nil {
		t.Fatal("non-canonical bearer encoding passed")
	}
}
