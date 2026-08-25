package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/threadtranscript"
	"cyberagent-workbench/internal/webevidence"
)

func apiWebOperation(t *testing.T, runID, toolName, key string,
	createdAt time.Time,
) webevidence.Operation {
	t.Helper()
	digest, err := webevidence.ScopedOperationKeyDigest(runID, key)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := webevidence.RequestFingerprint(map[string]string{"key": key})
	if err != nil {
		t.Fatal(err)
	}
	return webevidence.Operation{ProtocolVersion: webevidence.OperationProtocolVersion,
		KeyDigest: digest, RequestFingerprint: fingerprint, RunID: runID,
		ToolName: toolName, Response: json.RawMessage(`{"status":"stored"}`),
		CreatedAt: createdAt}
}

func TestRunWebEvidenceAPIUsesBoundedPublicPresentation(t *testing.T) {
	fixture := newAPIFixture(t)
	ctx := context.Background()
	mission, err := fixture.store.GetMission(ctx, fixture.run.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	now := fixture.run.CreatedAt.Add(time.Second)
	canonical := "https://docs.example.com/api-report"
	source, err := webevidence.SealSource(webevidence.Source{
		ID: webevidence.StableSourceID(fixture.run.ID, canonical), RunID: fixture.run.ID,
		MissionID: mission.ID, WorkspaceID: mission.WorkspaceID, CanonicalURL: canonical,
		Title: "API report", Provider: "direct", State: webevidence.SourceDiscovered,
		DiscoveredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.store.SaveWebSearch(ctx, []webevidence.Source{source},
		apiWebOperation(t, fixture.run.ID, "web_search", "api-search", now)); err != nil {
		t.Fatal(err)
	}
	digest := webevidence.DigestBytes([]byte("API raw evidence"))
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{
		ID: webevidence.StableSnapshotID(source.ID, digest, now), SourceID: source.ID,
		RunID: fixture.run.ID, MissionID: mission.ID, RequestedURL: canonical,
		FinalURL: canonical, Title: "API report", FetchedAt: now,
		StaleAt: now.Add(24 * time.Hour), Digest: digest, MIME: "text/html",
		Charset: "utf-8", Body: "private bounded page body", State: webevidence.SourcePartial,
		Truncated: true, Robots: "allowed", Provider: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.store.SaveWebFetch(ctx, source, snapshot,
		apiWebOperation(t, fixture.run.ID, "web_fetch", "api-fetch", now)); err != nil {
		t.Fatal(err)
	}
	citationDigest, _ := webevidence.ScopedOperationKeyDigest(fixture.run.ID, "api-cite")
	citation, err := webevidence.SealCitation(webevidence.Citation{
		ID:    webevidence.StableCitationID(fixture.run.ID, citationDigest),
		RunID: fixture.run.ID, SourceID: source.ID, SnapshotID: snapshot.ID,
		Claim: "API claim", URL: canonical, Title: "API report", FetchedAt: now,
		StaleAt: snapshot.StaleAt, Digest: digest, Partial: true, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.store.SaveWebCitation(ctx, citation,
		apiWebOperation(t, fixture.run.ID, "web_citation", "api-cite", now)); err != nil {
		t.Fatal(err)
	}

	response := fixture.get(t, "/api/v1/runs/"+fixture.run.ID+"/web-evidence?limit=10")
	var inventory webevidence.Inventory
	decodeData(t, response, &inventory)
	if len(inventory.Citations) != 1 || inventory.Citations[0].URL != canonical ||
		inventory.Citations[0].Title != "API report" ||
		inventory.Citations[0].Status != "partial" || !inventory.Citations[0].Untrusted ||
		inventory.Citations[0].InstructionAuthorized {
		t.Fatalf("inventory=%#v", inventory)
	}
	if strings.Contains(response.Body.String(), "private bounded page body") ||
		strings.Contains(response.Body.String(), "API claim") {
		t.Fatalf("API leaked private page or claim content: %s", response.Body.String())
	}
	invalid := fixture.get(t, "/api/v1/runs/"+fixture.run.ID+"/web-evidence?limit=0")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestThreadWebEvidenceViewDerivesOnlyCiteableStaleStatus(t *testing.T) {
	now := time.Now().UTC()
	base := threadtranscript.Item{WebEvidence: &threadtranscript.WebEvidencePresentation{
		Version: "web_evidence_presentation.v1", SourceID: "source-view",
		SnapshotID: "snapshot-view", URL: "https://docs.example.com/view",
		State: "fetched", FetchedAt: now.Add(-48 * time.Hour),
		StaleAt: now.Add(-24 * time.Hour), Digest: strings.Repeat("a", 64),
		Citeable: true, Untrusted: true,
	}}
	view := threadTranscriptItemView(base)
	if view.WebEvidence == nil || view.WebEvidence.State != "stale" ||
		!view.WebEvidence.Stale {
		t.Fatalf("citeable view=%#v", view.WebEvidence)
	}
	base.WebEvidence.State = "blocked"
	base.WebEvidence.Citeable = false
	view = threadTranscriptItemView(base)
	if view.WebEvidence == nil || view.WebEvidence.State != "blocked" ||
		view.WebEvidence.Stale {
		t.Fatalf("blocked view=%#v", view.WebEvidence)
	}
}
