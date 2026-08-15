package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/browserruntime"
)

type fakeSafeWebRuntimeStore struct {
	evidence    browserruntime.BrowserNetworkContainmentEvidence
	review      browserruntime.BrowserNetworkContainmentReview
	evidenceErr error
	reviewErr   error
}

func (f *fakeSafeWebRuntimeStore) LoadLatestBrowserNetworkEvidence(context.Context,
	string,
) (browserruntime.BrowserNetworkContainmentEvidence, error) {
	if f.evidenceErr != nil {
		return browserruntime.BrowserNetworkContainmentEvidence{}, f.evidenceErr
	}
	return f.evidence, nil
}

func (f *fakeSafeWebRuntimeStore) LoadBrowserNetworkReview(context.Context,
	string,
) (browserruntime.BrowserNetworkContainmentReview, error) {
	if f.reviewErr != nil {
		return browserruntime.BrowserNetworkContainmentReview{}, f.reviewErr
	}
	return f.review, nil
}

func safeWebRuntimeFixture(t *testing.T) (browserruntime.BrowserExecutableIdentity,
	browserruntime.BrowserAcceptanceCandidate, browserruntime.BrowserNetworkContainmentEvidence,
	browserruntime.BrowserNetworkContainmentReview,
) {
	t.Helper()
	relative := filepath.ToSlash(filepath.Join("Google", "Chrome", "Application", "chrome.exe"))
	identity := browserruntime.BrowserExecutableIdentity{
		ProtocolVersion: browserruntime.BrowserExecutableIdentityProtocolVersion,
		Product:         browserruntime.BrowserProductChrome, Channel: browserruntime.BrowserChannelStable,
		Vendor: "Google", RootID: browserruntime.DiscoveryRootProgramFiles,
		CanonicalPath: filepath.Join(t.TempDir(), filepath.FromSlash(relative)),
		RelativePath:  relative, HostGOOS: runtime.GOOS, HostGOARCH: runtime.GOARCH,
		TargetGOARCH: "amd64", ExecutableBytes: 1024,
		ExecutableSHA256: strings.Repeat("a", 64),
		VersionSource:    browserruntime.VersionSourceUnavailable,
		PEFormatVerified: true, RegularFileVerified: true, SymlinkRejected: true,
		MetadataOnly: true,
	}
	identity.Fingerprint = safeWebRuntimeFingerprint(t, identity)
	if err := browserruntime.ValidateBrowserExecutableIdentity(identity); err != nil {
		t.Fatal(err)
	}
	acceptance := browserruntime.BrowserAcceptanceCandidate{
		ProtocolVersion:               browserruntime.BrowserAcceptanceProtocolVersion,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		Product:                       identity.Product, Channel: identity.Channel, RootID: identity.RootID,
		ExecutableSHA256: identity.ExecutableSHA256, ExecutableBytes: identity.ExecutableBytes,
		TargetGOARCH: identity.TargetGOARCH,
		Decision:     browserruntime.BrowserAcceptanceAccepted,
		ReasonCode:   browserruntime.BrowserAcceptanceReasonPublisherVerified,
		Evidence: browserruntime.AuthenticodeEvidence{
			Source: browserruntime.AuthenticodeSourceWindows, Publisher: "Google LLC",
			CertificateSHA256: strings.Repeat("b", 64), SignatureVerified: true,
			SameOpenHandleVerified: true, CacheOnlyVerification: true,
			PublisherPolicyMatched:    true,
			PublisherPolicyVersion:    browserruntime.BrowserPublisherPolicyVersion,
			PublisherEvidenceComplete: true,
		},
		SameHandleBytesRevalidated: true, SameFilePathRevalidated: true,
		PERevalidated: true, ReviewEligible: true, StartBlocked: true, MetadataOnly: true,
	}
	acceptance.Fingerprint = safeWebRuntimeFingerprint(t, acceptance)
	if err := browserruntime.ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Round(time.Millisecond)
	evidence, err := browserruntime.BuildBrowserNetworkContainmentEvidence(identity, acceptance,
		browserruntime.BrowserNetworkProbeReport{
			ID: "browser-network-evidence-app", CollectorIdentity: "network-probe-operator",
			Adapter:                browserruntime.WindowsWFPBrowserContainmentAdapterName,
			DynamicSessionObserved: true, AtomicInstallObserved: true,
			ExactTargetObserved: true, WrongPortDenied: true,
			WrongLoopbackAddressDenied: true, NonLoopbackAddressDenied: true,
			IPv6Denied: true, RuleCleanupObserved: true, Production: true,
			StartedAt: now, CompletedAt: now.Add(time.Second),
		})
	if err != nil {
		t.Fatal(err)
	}
	review, err := browserruntime.BuildBrowserNetworkContainmentReview(evidence,
		identity, acceptance, "browser-network-review-app", "independent-network-reviewer",
		true, evidence.CompletedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return identity, acceptance, evidence, review
}

func safeWebRuntimeFingerprint(t *testing.T, value any) string {
	t.Helper()
	switch typed := value.(type) {
	case browserruntime.BrowserExecutableIdentity:
		typed.Fingerprint = ""
		value = typed
	case browserruntime.BrowserAcceptanceCandidate:
		typed.Fingerprint = ""
		value = typed
	default:
		t.Fatalf("unsupported safe-web runtime fixture fingerprint type %T", value)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func TestBrowserSafeWebRuntimeServiceReadiness(t *testing.T) {
	identity, acceptance, evidence, review := safeWebRuntimeFixture(t)

	t.Run("ready", func(t *testing.T) {
		service := NewBrowserSafeWebRuntimeService(&fakeSafeWebRuntimeStore{
			evidence: evidence, review: review,
		})
		readiness, err := service.Readiness(t.Context(), identity, acceptance)
		if err != nil {
			t.Fatal(err)
		}
		if !readiness.Ready || readiness.BlockingReason != "" {
			t.Fatalf("expected ready receipt, got %#v", readiness)
		}
	})

	t.Run("missing evidence", func(t *testing.T) {
		service := NewBrowserSafeWebRuntimeService(&fakeSafeWebRuntimeStore{
			evidenceErr: sql.ErrNoRows,
		})
		readiness, err := service.Readiness(t.Context(), identity, acceptance)
		if err != nil {
			t.Fatal(err)
		}
		if readiness.Ready || readiness.BlockingReason != browserruntime.BrowserSafeWebBlockedEvidenceMissing {
			t.Fatalf("ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
		}
	})

	t.Run("missing review", func(t *testing.T) {
		service := NewBrowserSafeWebRuntimeService(&fakeSafeWebRuntimeStore{
			evidence: evidence, reviewErr: sql.ErrNoRows,
		})
		readiness, err := service.Readiness(t.Context(), identity, acceptance)
		if err != nil {
			t.Fatal(err)
		}
		if readiness.Ready || readiness.BlockingReason != browserruntime.BrowserSafeWebBlockedReviewMissing {
			t.Fatalf("ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
		}
	})

	t.Run("rejected review", func(t *testing.T) {
		rejected, err := browserruntime.BuildBrowserNetworkContainmentReview(evidence,
			identity, acceptance, "browser-network-review-rejected-app",
			"independent-network-reviewer", false, evidence.CompletedAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		service := NewBrowserSafeWebRuntimeService(&fakeSafeWebRuntimeStore{
			evidence: evidence, review: rejected,
		})
		readiness, err := service.Readiness(t.Context(), identity, acceptance)
		if err != nil {
			t.Fatal(err)
		}
		if readiness.Ready || readiness.BlockingReason != browserruntime.BrowserSafeWebBlockedReviewNotAccepted {
			t.Fatalf("ready=%t reason=%q", readiness.Ready, readiness.BlockingReason)
		}
	})

	t.Run("store error surfaces", func(t *testing.T) {
		service := NewBrowserSafeWebRuntimeService(&fakeSafeWebRuntimeStore{
			evidenceErr: errors.New("store unavailable"),
		})
		if _, err := service.Readiness(t.Context(), identity, acceptance); err == nil {
			t.Fatal("store error was swallowed")
		}
	})
}
