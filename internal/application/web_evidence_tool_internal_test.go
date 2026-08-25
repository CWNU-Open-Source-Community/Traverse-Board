package application

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

func TestEncodeWebFetchToolOutputPreservesValidJSONWithinGatewayLimit(t *testing.T) {
	fetchedAt := time.Date(2026, time.August, 25, 8, 30, 0, 0, time.UTC)
	source, err := webevidence.SealSource(webevidence.Source{
		ID:           "web-source-bounded-output",
		RunID:        "run-web-output",
		MissionID:    "mission-web-output",
		CanonicalURL: "https://docs.example.com/report",
		Title:        "Bounded output",
		Provider:     "direct",
		State:        webevidence.SourceFetched,
		DiscoveredAt: fetchedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{
		ID:           "web-snapshot-bounded-output",
		SourceID:     source.ID,
		RunID:        source.RunID,
		MissionID:    source.MissionID,
		RequestedURL: source.CanonicalURL,
		FinalURL:     source.CanonicalURL,
		Title:        source.Title,
		FetchedAt:    fetchedAt,
		StaleAt:      fetchedAt.Add(24 * time.Hour),
		Digest:       webevidence.DigestBytes([]byte("raw bounded output")),
		MIME:         "text/plain",
		Charset:      "utf-8",
		Body:         strings.Repeat("<", webevidence.MaxBodyBytes),
		State:        webevidence.SourceFetched,
		Robots:       "allowed",
		Provider:     "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, excerptTruncated, err := encodeWebFetchToolOutput(
		webevidence.FetchResult{ProtocolVersion: webevidence.FetchProtocolVersion,
			Source: source, Snapshot: snapshot},
		webevidence.PresentSnapshot(snapshot, fetchedAt),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !excerptTruncated || !json.Valid(encoded) ||
		len(encoded) > toolgateway.MaxResultStdoutBytes-1024 {
		t.Fatalf("truncated=%t valid=%t bytes=%d", excerptTruncated,
			json.Valid(encoded), len(encoded))
	}
	var output webFetchToolOutput
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if output.ProtocolVersion != webevidence.FetchProtocolVersion ||
		output.Snapshot.SourceID != source.ID ||
		output.Snapshot.SnapshotID != snapshot.ID ||
		output.Snapshot.Digest != snapshot.Digest ||
		!output.Snapshot.BodyExcerptTruncated || output.Snapshot.SnapshotTruncated ||
		output.Snapshot.InstructionAuthorized || !output.Snapshot.Untrusted ||
		output.Snapshot.Body == "" || len(output.Snapshot.Body) >= len(snapshot.Body) {
		t.Fatalf("unexpected bounded output: %#v", output)
	}
	envelope, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
		Version: supervisorToolResultVersion, Tool: string(toolgateway.WebFetchTool),
		Status: "completed", Stdout: string(encoded), Metadata: map[string]string{
			"title": strings.Repeat("<", 1024),
		}, Truncated: true,
	})
	if err != nil || !json.Valid(envelope) || len(envelope) > domain.MaxSupervisorToolResultBytes {
		t.Fatalf("durable envelope bytes=%d valid=%t err=%v", len(envelope),
			json.Valid(envelope), err)
	}
	if strings.Contains(string(envelope), `\u003c`) {
		t.Fatal("durable Web result envelope unexpectedly HTML-escaped evidence")
	}
}

func TestEncodeWebFetchToolOutputKeepsSmallBody(t *testing.T) {
	fetchedAt := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	source, err := webevidence.SealSource(webevidence.Source{
		ID: "web-source-small-output", RunID: "run-web-output",
		MissionID: "mission-web-output", CanonicalURL: "https://docs.example.com/small",
		Provider: "direct", State: webevidence.SourceFetched, DiscoveredAt: fetchedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{
		ID: "web-snapshot-small-output", SourceID: source.ID, RunID: source.RunID,
		MissionID: source.MissionID, RequestedURL: source.CanonicalURL,
		FinalURL: source.CanonicalURL, FetchedAt: fetchedAt,
		StaleAt: fetchedAt.Add(24 * time.Hour),
		Digest:  webevidence.DigestBytes([]byte("small body")), MIME: "text/plain",
		Charset: "utf-8", Body: "small body", State: webevidence.SourceFetched,
		Robots: "allowed", Provider: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, excerptTruncated, err := encodeWebFetchToolOutput(
		webevidence.FetchResult{ProtocolVersion: webevidence.FetchProtocolVersion,
			Source: source, Snapshot: snapshot, Replayed: true},
		webevidence.PresentSnapshot(snapshot, fetchedAt),
	)
	if err != nil || excerptTruncated {
		t.Fatalf("truncated=%t err=%v", excerptTruncated, err)
	}
	var output webFetchToolOutput
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if output.Snapshot.Body != snapshot.Body || output.Snapshot.BodyExcerptTruncated ||
		output.Replayed {
		t.Fatalf("unexpected small output: %#v", output)
	}
}
