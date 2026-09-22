package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/webevidence"
)

func TestWebRetrievalAnnotationsStayBoundAcrossSaveAndRead(t *testing.T) {
	state := openStructuredToolTestStore(t)
	mission, run := createStructuredToolTestRun(t, t.Context(), state, "search annotations")
	at := run.CreatedAt.Add(time.Second)
	canonical := "https://docs.example.com/report"
	source, err := webevidence.SealSource(webevidence.Source{ID: webevidence.StableSourceID(run.ID, canonical),
		RunID: run.ID, MissionID: mission.ID, WorkspaceID: mission.WorkspaceID, CanonicalURL: canonical,
		Provider: "direct", State: webevidence.SourceDiscovered, DiscoveredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	search := webevidence.SearchResult{ProtocolVersion: webevidence.SearchProtocolVersion, Query: "release date",
		Provider: "direct", AllowedDomains: []string{"example.com"}, FilterPolicy: "allowed_domains", FilteredOutCount: 1,
		Sources: []webevidence.SearchStub{{SourceID: source.ID, CanonicalURL: canonical, Rank: 1, Provider: "direct", Untrusted: true}}}
	operation := webOperation(t, run.ID, "web_search", "filtered-ok", search, at)
	if _, replayed, err := state.SaveWebSearch(t.Context(), []webevidence.Source{source}, operation); err != nil || replayed {
		t.Fatal("valid filtered write", err)
	}
	if got, found, err := state.GetWebEvidenceOperation(t.Context(), run.ID, operation.KeyDigest); err != nil || !found || string(got.Response) != string(operation.Response) {
		t.Fatal("filtered read changed", err)
	}
	invalidSearch := search
	invalidSearch.AllowedDomains = []string{"other.example.net"}
	badSearch := webOperation(t, run.ID, "web_search", "filtered-invalid", invalidSearch, at)
	if _, _, err := state.SaveWebSearch(t.Context(), []webevidence.Source{source}, badSearch); err == nil {
		t.Fatal("saved source outside declared domains")
	}
	// Simulate an invalid stored operation through the internal raw writer;
	// the normal write rejected it and the public reader must reject it too.
	tx, err := state.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertWebOperation(t.Context(), tx, badSearch); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.GetWebEvidenceOperation(t.Context(), run.ID, badSearch.KeyDigest); err == nil {
		t.Fatal("read invalid stored filters")
	}

	body := strings.Repeat("中文材料🙂", 300) + "Release date: September 22."
	digest := webevidence.DigestBytes([]byte("original transport bytes"))
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{ID: webevidence.StableSnapshotID(source.ID, digest, at),
		SourceID: source.ID, RunID: run.ID, MissionID: mission.ID, RequestedURL: canonical, FinalURL: canonical,
		Provider: "direct", FetchedAt: at, StaleAt: at.Add(time.Hour), Digest: digest, MIME: "text/html", Charset: "utf-8",
		Body: body, State: webevidence.SourcePartial, Truncated: true, Robots: "allowed"})
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := webevidence.ExtractSnapshot(snapshot, "Release date")
	if err != nil {
		t.Fatal(err)
	}
	fetch := webevidence.FetchResult{ProtocolVersion: webevidence.FetchProtocolVersion, Source: source, Snapshot: snapshot, Extraction: &extraction}
	fetchOp := webOperation(t, run.ID, "web_fetch", "question-ok", fetch, at)
	if _, replayed, err := state.SaveWebFetch(t.Context(), source, snapshot, fetchOp); err != nil || replayed {
		t.Fatal("valid question write", err)
	}
	if _, replayed, err := state.SaveWebFetch(t.Context(), source, snapshot, fetchOp); err != nil || !replayed {
		t.Fatal("question replay", err)
	}
	got, found, err := state.GetWebEvidenceOperation(t.Context(), run.ID, fetchOp.KeyDigest)
	if err != nil || !found || string(got.Response) != string(fetchOp.Response) {
		t.Fatal("question read changed", err)
	}
	for _, kind := range []string{"digest", "coverage", "span", "question", "different_snapshot"} {
		bad := fetch
		changed := extraction
		bad.Extraction = &changed
		switch kind {
		case "digest":
			changed.BodySHA256 = strings.Repeat("0", 64)
		case "coverage":
			changed.Coverage = webevidence.ExtractionCoverageSaved
		case "span":
			changed.SpanEnd = len([]rune(body)) + 1
		case "question":
			changed.Question = " Release date "
		case "different_snapshot":
			bad.Snapshot.Body += " changed"
			bad.Snapshot, err = webevidence.SealSnapshot(bad.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			changed, err = webevidence.ExtractSnapshot(bad.Snapshot, "Release date")
			if err != nil {
				t.Fatal(err)
			}
		}
		badOp := webOperation(t, run.ID, "web_fetch", "question-invalid-"+kind, bad, at)
		if _, _, err := state.SaveWebFetch(t.Context(), source, snapshot, badOp); err == nil {
			t.Errorf("saved invalid question %s", kind)
		}
	}
	var roundTrip webevidence.FetchResult
	if json.Unmarshal(got.Response, &roundTrip) != nil || roundTrip.Snapshot.Body != body || roundTrip.Extraction.Validate(roundTrip.Snapshot) != nil {
		t.Fatal("lost exact original body")
	}
}
