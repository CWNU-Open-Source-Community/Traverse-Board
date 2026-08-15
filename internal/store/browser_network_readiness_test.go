package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/browserruntime"
)

func TestBrowserNetworkEvidenceAndReviewAreDurableAndIdempotent(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "browser-network-readiness.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, identity, acceptance, _ := browserLaunchStoreFixture(t, state)

	now := time.Now().UTC().Round(time.Millisecond)
	evidence, err := browserruntime.BuildBrowserNetworkContainmentEvidence(identity, acceptance,
		browserruntime.BrowserNetworkProbeReport{
			ID: "browser-network-evidence-store", CollectorIdentity: "network-probe-operator",
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
		identity, acceptance, "browser-network-review-store", "independent-network-reviewer",
		true, evidence.CompletedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	recorded, err := state.RecordBrowserNetworkEvidence(ctx, evidence, "evidence-op-001")
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Replayed {
		t.Fatal("fresh evidence was reported as replayed")
	}
	replayedEvidence, err := state.RecordBrowserNetworkEvidence(ctx, evidence, "evidence-op-001")
	if err != nil {
		t.Fatal(err)
	}
	if !replayedEvidence.Replayed || replayedEvidence.Evidence.Fingerprint != evidence.Fingerprint {
		t.Fatalf("evidence replay did not return the exact stored payload: %#v", replayedEvidence)
	}

	laterEvidence, err := browserruntime.BuildBrowserNetworkContainmentEvidence(identity, acceptance,
		browserruntime.BrowserNetworkProbeReport{
			ID: "browser-network-evidence-store-later", CollectorIdentity: "network-probe-operator",
			Adapter:                browserruntime.WindowsWFPBrowserContainmentAdapterName,
			DynamicSessionObserved: true, AtomicInstallObserved: true,
			ExactTargetObserved: true, WrongPortDenied: true,
			WrongLoopbackAddressDenied: true, NonLoopbackAddressDenied: true,
			IPv6Denied: true, RuleCleanupObserved: true, Production: true,
			StartedAt: now.Add(2 * time.Minute), CompletedAt: now.Add(2*time.Minute + time.Second),
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordBrowserNetworkEvidence(ctx, laterEvidence, "evidence-op-002"); err != nil {
		t.Fatal(err)
	}

	if _, err := state.RecordBrowserNetworkReview(ctx, review, "review-op-001"); err != nil {
		t.Fatal(err)
	}
	replayedReview, err := state.RecordBrowserNetworkReview(ctx, review, "review-op-001")
	if err != nil {
		t.Fatal(err)
	}
	if !replayedReview.Replayed || replayedReview.Review.Fingerprint != review.Fingerprint {
		t.Fatalf("review replay did not return the exact stored payload: %#v", replayedReview)
	}

	loadedEvidence, err := state.LoadLatestBrowserNetworkEvidence(ctx, identity.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if loadedEvidence.Fingerprint != laterEvidence.Fingerprint {
		t.Fatalf("loaded evidence fingerprint = %q, want latest %q", loadedEvidence.Fingerprint, laterEvidence.Fingerprint)
	}
	loadedReview, err := state.LoadBrowserNetworkReview(ctx, evidence.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if loadedReview.Fingerprint != review.Fingerprint {
		t.Fatalf("loaded review fingerprint = %q, want %q", loadedReview.Fingerprint, review.Fingerprint)
	}
}

func TestBrowserNetworkReviewRequiresStoredEvidence(t *testing.T) {
	ctx := t.Context()
	state, err := Open(filepath.Join(t.TempDir(), "browser-network-review-fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	_, identity, acceptance, _ := browserLaunchStoreFixture(t, state)

	now := time.Now().UTC().Round(time.Millisecond)
	evidence, err := browserruntime.BuildBrowserNetworkContainmentEvidence(identity, acceptance,
		browserruntime.BrowserNetworkProbeReport{
			ID: "browser-network-evidence-fk", CollectorIdentity: "network-probe-operator",
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
		identity, acceptance, "browser-network-review-fk", "independent-network-reviewer",
		true, evidence.CompletedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordBrowserNetworkReview(ctx, review, "review-op-fk"); err == nil {
		t.Fatal("review referencing an unrecorded evidence was accepted")
	}
	if _, err := state.LoadBrowserNetworkReview(ctx, evidence.Fingerprint); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("load review for absent evidence err = %v, want sql.ErrNoRows", err)
	}
	if _, err := state.LoadLatestBrowserNetworkEvidence(ctx, identity.Fingerprint); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("load evidence for absent identity err = %v, want sql.ErrNoRows", err)
	}
}

func TestSchemaV103BrowserNetworkReadinessReapplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v102-browser-network-readiness.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_, identity, acceptance, _ := browserLaunchStoreFixture(t, state)
	now := time.Now().UTC().Round(time.Millisecond)
	evidence, err := browserruntime.BuildBrowserNetworkContainmentEvidence(identity, acceptance,
		browserruntime.BrowserNetworkProbeReport{
			ID: "browser-network-evidence-downgrade", CollectorIdentity: "network-probe-operator",
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
	for _, statement := range removeSchemaV103ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v103 fixture with %q: %v", statement, err)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()
	if _, err := upgraded.RecordBrowserNetworkEvidence(ctx, evidence, "evidence-op-downgrade"); err != nil {
		t.Fatal(err)
	}
	loaded, err := upgraded.LoadLatestBrowserNetworkEvidence(ctx, identity.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint != evidence.Fingerprint {
		t.Fatalf("reapplied evidence fingerprint = %q, want %q", loaded.Fingerprint, evidence.Fingerprint)
	}
}

func removeSchemaV103ForTestStatements() []string {
	return []string{
		`DROP TABLE browser_network_review_operations`,
		`DROP TABLE browser_network_reviews`,
		`DROP TABLE browser_network_evidence_operations`,
		`DROP TABLE browser_network_evidences`,
		`DELETE FROM schema_migrations WHERE version = 103`,
	}
}
