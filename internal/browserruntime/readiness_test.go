package browserruntime

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestBrowserSafeWebReadinessReady(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	readiness, err := BuildBrowserSafeWebReadiness(facts.networkEvidence,
		facts.networkReview, facts.identity, facts.acceptance, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.Ready || readiness.BlockingReason != "" {
		t.Fatalf("expected a ready receipt, got %#v", readiness)
	}
	if !readiness.ExpiresAt.Equal(facts.networkEvidence.ExpiresAt) {
		t.Fatalf("readiness expiry drifted from evidence: %v != %v",
			readiness.ExpiresAt, facts.networkEvidence.ExpiresAt)
	}
	if err := ValidateBrowserSafeWebReadiness(readiness, facts.networkEvidence,
		facts.networkReview, facts.identity, facts.acceptance); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserSafeWebReadinessFailsClosedOnEvidence(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	tests := []struct {
		name   string
		mutate func(*BrowserNetworkContainmentEvidence)
		want   string
	}{
		{"missing", func(e *BrowserNetworkContainmentEvidence) {
			*e = BrowserNetworkContainmentEvidence{}
		}, BrowserSafeWebBlockedEvidenceMissing},
		{"version", func(e *BrowserNetworkContainmentEvidence) {
			e.ProtocolVersion = "browser_network_containment_evidence.v0"
		}, BrowserSafeWebBlockedEvidenceVersionMismatch},
		{"policy", func(e *BrowserNetworkContainmentEvidence) {
			e.PolicyVersion = "browser_network_containment_policy.v1"
		}, BrowserSafeWebBlockedPolicyVersionMismatch},
		{"adapter", func(e *BrowserNetworkContainmentEvidence) {
			e.Adapter = FakeBrowserContainmentAdapterName
		}, BrowserSafeWebBlockedAdapterMismatch},
		{"platform", func(e *BrowserNetworkContainmentEvidence) {
			e.OperatingSystem = "plan9"
		}, BrowserSafeWebBlockedPlatformMismatch},
		{"not_passed", func(e *BrowserNetworkContainmentEvidence) {
			e.Passed = false
		}, BrowserSafeWebBlockedEvidenceNotPassed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := facts.networkEvidence
			test.mutate(&evidence)
			readiness, err := BuildBrowserSafeWebReadiness(evidence,
				facts.networkReview, facts.identity, facts.acceptance, facts.now)
			if err != nil {
				t.Fatal(err)
			}
			if readiness.Ready || readiness.BlockingReason != test.want {
				t.Fatalf("ready=%t reason=%q, want reason=%q",
					readiness.Ready, readiness.BlockingReason, test.want)
			}
		})
	}
}

func TestBrowserSafeWebReadinessFailsClosedOnIdentityAndAcceptance(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	_, otherIdentity, otherAcceptance, _ := browserLaunchFixture(t)
	readiness, err := BuildBrowserSafeWebReadiness(facts.networkEvidence,
		facts.networkReview, otherIdentity, otherAcceptance, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Ready || readiness.BlockingReason != BrowserSafeWebBlockedExecutableMismatch {
		t.Fatalf("identity mismatch ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
	}

	driftedAcceptance, err := buildBrowserAcceptanceCandidate(facts.identity,
		func(*os.File, string) (AuthenticodeEvidence, error) {
			return AuthenticodeEvidence{
				Source: AuthenticodeSourceWindows, Publisher: "Google LLC",
				CertificateSHA256: strings.Repeat("b", 64), SignatureVerified: true,
				SameOpenHandleVerified: true, CacheOnlyVerification: true,
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	readiness, err = BuildBrowserSafeWebReadiness(facts.networkEvidence,
		facts.networkReview, facts.identity, driftedAcceptance, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Ready || readiness.BlockingReason != BrowserSafeWebBlockedAcceptanceMismatch {
		t.Fatalf("acceptance mismatch ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
	}
}

func TestBrowserSafeWebReadinessFailsClosedOnReview(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)

	t.Run("missing", func(t *testing.T) {
		readiness, err := BuildBrowserSafeWebReadiness(facts.networkEvidence,
			BrowserNetworkContainmentReview{}, facts.identity, facts.acceptance, facts.now)
		if err != nil {
			t.Fatal(err)
		}
		if readiness.Ready || readiness.BlockingReason != BrowserSafeWebBlockedReviewMissing {
			t.Fatalf("ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
		}
	})

	t.Run("binding", func(t *testing.T) {
		review := facts.networkReview
		review.EvidenceFingerprint = strings.Repeat("c", 64)
		readiness, err := BuildBrowserSafeWebReadiness(facts.networkEvidence,
			review, facts.identity, facts.acceptance, facts.now)
		if err != nil {
			t.Fatal(err)
		}
		if readiness.Ready || readiness.BlockingReason != BrowserSafeWebBlockedReviewBindingMismatch {
			t.Fatalf("ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		rejected, err := BuildBrowserNetworkContainmentReview(facts.networkEvidence,
			facts.identity, facts.acceptance, "browser-network-review-rejected",
			"independent-network-reviewer", false,
			facts.networkEvidence.CompletedAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		readiness, err := BuildBrowserSafeWebReadiness(facts.networkEvidence,
			rejected, facts.identity, facts.acceptance, facts.now)
		if err != nil {
			t.Fatal(err)
		}
		if readiness.Ready || readiness.BlockingReason != BrowserSafeWebBlockedReviewNotAccepted {
			t.Fatalf("ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
		}
	})
}

func TestBrowserSafeWebReadinessFailsClosedOnExpiry(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	readiness, err := BuildBrowserSafeWebReadiness(facts.networkEvidence,
		facts.networkReview, facts.identity, facts.acceptance,
		facts.networkEvidence.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Ready || readiness.BlockingReason != BrowserSafeWebBlockedEvidenceExpired {
		t.Fatalf("ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
	}
}

func TestBrowserSafeWebReadinessRejectsTampering(t *testing.T) {
	facts := newLoopbackBrowserRuntimeFacts(t)
	readiness, err := BuildBrowserSafeWebReadiness(facts.networkEvidence,
		facts.networkReview, facts.identity, facts.acceptance, facts.now)
	if err != nil {
		t.Fatal(err)
	}
	for name, tamper := range map[string]func(*BrowserSafeWebReadiness){
		"ready_flip":      func(r *BrowserSafeWebReadiness) { r.Ready = !r.Ready },
		"reason_forged":   func(r *BrowserSafeWebReadiness) { r.BlockingReason = BrowserSafeWebBlockedEvidenceExpired },
		"identity_forged": func(r *BrowserSafeWebReadiness) { r.ExecutableIdentityFingerprint = strings.Repeat("d", 64) },
		"expiry_forged":   func(r *BrowserSafeWebReadiness) { r.ExpiresAt = r.ExpiresAt.Add(time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			receipt := readiness
			tamper(&receipt)
			if err := ValidateBrowserSafeWebReadiness(receipt, facts.networkEvidence,
				facts.networkReview, facts.identity, facts.acceptance); err == nil {
				t.Fatal("tampered readiness receipt was accepted")
			}
		})
	}
}
