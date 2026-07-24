package browserruntime

import (
	"testing"
	"time"
)

func TestBrowserLaunchReviewAcceptsEvidenceWithoutAuthorizingStart(t *testing.T) {
	session, identity, acceptance, ownership := browserLaunchFixture(t)
	now := time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC)
	attempt, err := BuildBrowserLaunchAttempt(session, identity, acceptance, ownership,
		"browser-attempt-review-001", now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BuildBrowserLaunchLease(attempt, "browser-lease-review-001",
		"browser-worker-001", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	review, err := BuildBrowserLaunchReview(session, identity, acceptance, ownership,
		attempt, lease, "browser-review-001", BrowserLaunchReviewAcceptCandidate,
		"independent-operator", "browser-review-operation-001", "", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !review.AcceptedForFutureAdapter || !review.IndependentReviewerVerified ||
		!review.PublisherEvidenceConfirmed || !review.ExactScopeConfirmed ||
		!review.SandboxBackendConfirmed || !review.BudgetConfirmed ||
		!review.ProfileGenerationConfirmed || !review.ProcessTreeContractConfirmed ||
		!review.AppendOnlyAuditRequired || review.StartAuthorized ||
		review.ProcessExecutionAuthorized || review.NetworkAuthorized ||
		review.ProfileWriteAuthorized || review.ProcessTerminationAuthorized ||
		review.FilesystemCleanupAuthorized || review.ArtifactCommitAuthorized ||
		review.Authority != (RuntimeAuthority{}) {
		t.Fatalf("browser launch review widened authority: %#v", review)
	}
}

func TestBrowserLaunchReviewRequiresIndependentOperatorAndRejectsTampering(t *testing.T) {
	session, identity, acceptance, ownership := browserLaunchFixture(t)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	attempt, err := BuildBrowserLaunchAttempt(session, identity, acceptance, ownership,
		"browser-attempt-review-002", now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BuildBrowserLaunchLease(attempt, "browser-lease-review-002",
		"same-operator", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildBrowserLaunchReview(session, identity, acceptance, ownership,
		attempt, lease, "browser-review-002", BrowserLaunchReviewAcceptCandidate,
		"same-operator", "browser-review-operation-002", "", now.Add(time.Second)); err == nil {
		t.Fatal("lease owner unexpectedly reviewed its own launch attempt")
	}
	review, err := BuildBrowserLaunchReview(session, identity, acceptance, ownership,
		attempt, lease, "browser-review-003", BrowserLaunchReviewRejectCandidate,
		"different-operator", "browser-review-operation-003", "", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if review.AcceptedForFutureAdapter ||
		review.ReasonCode != BrowserLaunchReviewReasonOperatorRejected {
		t.Fatalf("rejected launch review became acceptable: %#v", review)
	}
	review.StartAuthorized = true
	if err := ValidateBrowserLaunchReview(review, session, identity, acceptance,
		ownership, attempt, lease); err == nil {
		t.Fatal("authorizing browser review mutation unexpectedly passed")
	}
}
