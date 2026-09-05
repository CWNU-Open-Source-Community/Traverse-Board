package webevidence

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPublicPresentationsRedactBoundedMetadata(t *testing.T) {
	now := time.Date(2026, time.August, 25, 8, 45, 0, 0, time.UTC)
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	source, err := SealSource(Source{ID: "web-source-public-redaction",
		RunID: "run-public-redaction", MissionID: "mission-public-redaction",
		CanonicalURL: "https://docs.example.com/report", Title: "Report " + secret,
		Provider: "provider token=supersecret", State: SourceDiscovered,
		DiscoveredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := SealSnapshot(Snapshot{ID: "web-snapshot-public-redaction",
		SourceID: source.ID, RunID: source.RunID, MissionID: source.MissionID,
		RequestedURL: source.CanonicalURL, FinalURL: source.CanonicalURL,
		Title: source.Title, FetchedAt: now, StaleAt: now.Add(DefaultStaleAfter),
		Digest: DigestBytes([]byte("raw evidence")), MIME: "text/plain", Charset: "utf-8",
		Body: "private snapshot body", State: SourceFetched, Robots: "allowed",
		Provider: source.Provider})
	if err != nil {
		t.Fatal(err)
	}
	citation, err := SealCitation(Citation{ID: "web-citation-public-redaction",
		RunID: source.RunID, SourceID: source.ID, SnapshotID: snapshot.ID,
		Claim: "bounded claim", URL: snapshot.FinalURL, Title: snapshot.Title,
		FetchedAt: now, StaleAt: snapshot.StaleAt, Digest: snapshot.Digest,
		CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]string{
		"source title":      PresentSource(source).Title,
		"source provider":   PresentSource(source).Provider,
		"snapshot title":    PresentSnapshot(snapshot, now).Title,
		"snapshot provider": PresentSnapshot(snapshot, now).Provider,
		"citation title":    PresentCitation(citation, now).Title,
	} {
		if strings.Contains(value, secret) || strings.Contains(value, "supersecret") ||
			!strings.Contains(value, "[REDACTED:") {
			t.Fatalf("%s was not redacted: %q", label, value)
		}
	}
}

func TestSnapshotHTTPStatusIsDurableAndLegacyCompatible(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 3, 6, 0, 0, 0, time.UTC)
	base := Snapshot{ID: "snapshot-http-status", SourceID: "source-http-status",
		RunID: "run-http-status", MissionID: "mission-http-status",
		RequestedURL: "https://docs.example.com/status",
		FinalURL:     "https://docs.example.com/status", FetchedAt: now,
		StaleAt: now.Add(DefaultStaleAfter), Digest: DigestBytes([]byte("body")),
		MIME: "text/plain", Body: "body", State: SourceFetched,
		Robots: "allowed", Provider: "direct"}
	legacy, err := SealSnapshot(base)
	if err != nil || legacy.HTTPStatus != 0 {
		t.Fatalf("legacy snapshot=%#v err=%v", legacy, err)
	}
	base.HTTPStatus = http.StatusOK
	current, err := SealSnapshot(base)
	if err != nil || current.HTTPStatus != http.StatusOK ||
		current.Fingerprint == legacy.Fingerprint {
		t.Fatalf("current snapshot=%#v legacy=%#v err=%v", current, legacy, err)
	}
	base.HTTPStatus = http.StatusNotFound
	if _, err := SealSnapshot(base); err == nil {
		t.Fatal("successful snapshot accepted a non-success HTTP status")
	}
}

func TestSealedEvidenceRejectsUnsafeMetadataAndBodyControls(t *testing.T) {
	now := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	if _, err := SealSource(Source{ID: "source\x01control", RunID: "run-control",
		MissionID: "mission-control", CanonicalURL: "https://docs.example.com/",
		Provider: "direct", State: SourceDiscovered, DiscoveredAt: now}); err == nil {
		t.Fatal("source identity with a control character was accepted")
	}
	if _, err := SealSnapshot(Snapshot{ID: "snapshot-control", SourceID: "source-control",
		RunID: "run-control", MissionID: "mission-control",
		RequestedURL: "https://docs.example.com/", FinalURL: "https://docs.example.com/",
		FetchedAt: now, StaleAt: now.Add(DefaultStaleAfter), Digest: DigestBytes(nil),
		MIME: "text/plain", Body: "unsafe\x01body", State: SourceFetched,
		Robots: "allowed", Provider: "direct"}); err == nil {
		t.Fatal("snapshot body with an unsafe control character was accepted")
	}
	if _, err := SealSnapshot(Snapshot{ID: "snapshot-inconsistent", SourceID: "source-control",
		RunID: "run-control", MissionID: "mission-control",
		RequestedURL: "https://docs.example.com/", FinalURL: "https://docs.example.com/",
		FetchedAt: now, StaleAt: now.Add(DefaultStaleAfter), Digest: DigestBytes(nil),
		MIME: "text/plain", Body: "body", State: SourcePartial, Robots: "allowed",
		Provider: "direct"}); err == nil {
		t.Fatal("partial snapshot without a truncation/partial marker was accepted")
	}
}

func TestProviderGroundedCitationCannotMasqueradeAsLocalVerification(t *testing.T) {
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	base := ProviderGroundedCitation{ID: "provider-citation-grounded-model",
		RunID: "run-grounded-model", SourceID: "source-grounded-model",
		URL: "https://docs.example.com/grounded", Title: "Grounded",
		Provider: "provider_native:responses", ProviderBinding: strings.Repeat("a", 64),
		Provenance: ProviderGroundedProvenance, SearchedAt: now,
		ProviderQualified: true, Untrusted: true}
	sealed, err := SealProviderGroundedCitation(base)
	if err != nil || sealed.Validate() != nil {
		t.Fatalf("seal provider-grounded citation=%#v err=%v", sealed, err)
	}
	base.LocallyVerified = true
	if _, err := SealProviderGroundedCitation(base); err == nil {
		t.Fatal("provider-grounded citation claimed a local verification snapshot")
	}
	base.LocallyVerified = false
	base.InstructionAuthorized = true
	if _, err := SealProviderGroundedCitation(base); err == nil {
		t.Fatal("provider-grounded citation granted instruction authority")
	}
}
