package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/webevidence"
)

func TestWebEvidenceCLIListsSamePublicCitationPresentation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	if _, stderr, code := executeTestCommand(t, "workspace", "init", "web-evidence-cli"); code != 0 || stderr != "" {
		t.Fatalf("workspace init stderr=%q code=%d", stderr, code)
	}
	created, stderr, code := executeTestCommand(t, "run", "create",
		"Web evidence CLI contract", "--workspace", "web-evidence-cli", "--profile", "code")
	if code != 0 || stderr != "" {
		t.Fatalf("run create output=%q stderr=%q code=%d", created, stderr, code)
	}
	runID := runIDPattern.FindString(created)
	if runID == "" {
		t.Fatalf("run identity missing: %s", created)
	}
	seedCLIWebEvidence(t, home, runID)

	listed, stderr, code := executeTestCommand(t, "web-evidence", "list",
		"--run", runID, "--limit", "10")
	if code != 0 || stderr != "" {
		t.Fatalf("list output=%q stderr=%q code=%d", listed, stderr, code)
	}
	var inventory webevidence.Inventory
	if err := json.Unmarshal([]byte(listed), &inventory); err != nil ||
		len(inventory.Citations) != 1 ||
		inventory.Citations[0].URL != "https://docs.example.com/cli-report" ||
		inventory.Citations[0].Title != "CLI report" ||
		inventory.Citations[0].Status != "partial" || !inventory.Citations[0].Untrusted ||
		inventory.Citations[0].InstructionAuthorized {
		t.Fatalf("inventory=%#v err=%v", inventory, err)
	}
	for _, private := range []string{"private CLI page body", "provider-only snippet", "CLI claim"} {
		if strings.Contains(listed, private) {
			t.Fatalf("CLI leaked %q: %s", private, listed)
		}
	}
}

func TestRunCreateCLIRequiresExplicitValidWebNetworkScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	if _, stderr, code := executeTestCommand(t, "workspace", "init", "web-network-cli"); code != 0 || stderr != "" {
		t.Fatalf("workspace init stderr=%q code=%d", stderr, code)
	}
	created, stderr, code := executeTestCommand(t, "run", "create",
		"Opt in to bounded Web evidence", "--workspace", "web-network-cli", "--profile", "review",
		"--network", "allowlist", "--allow-target", "search.example.org",
		"--allow-target", "docs.example.com")
	if code != 0 || stderr != "" {
		t.Fatalf("run create output=%q stderr=%q code=%d", created, stderr, code)
	}
	runID := runIDPattern.FindString(created)
	if runID == "" {
		t.Fatalf("run identity missing: %s", created)
	}
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	mode, err := state.GetRunMode(context.Background(), runID)
	closeErr := state.Close()
	if err != nil || closeErr != nil || mode.Scope.NetworkMode != "allowlist" ||
		strings.Join(mode.Scope.AllowedTargets, ",") != "search.example.org,docs.example.com" {
		t.Fatalf("mode=%#v err=%v close_err=%v", mode, err, closeErr)
	}

	if output, stderr, code := executeTestCommand(t, "run", "create",
		"Invalid disabled target", "--workspace", "web-network-cli",
		"--network", "disabled", "--allow-target", "docs.example.com"); code == 0 || !strings.Contains(stderr, "disabled Run network mode") {
		t.Fatalf("invalid scope output=%q stderr=%q code=%d", output, stderr, code)
	}
}

func seedCLIWebEvidence(t *testing.T, home, runID string) {
	t.Helper()
	ctx := context.Background()
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runRecord, err := state.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := state.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	canonical := "https://docs.example.com/cli-report"
	source, err := webevidence.SealSource(webevidence.Source{
		ID: webevidence.StableSourceID(runID, canonical), RunID: runID,
		MissionID: mission.ID, WorkspaceID: mission.WorkspaceID, CanonicalURL: canonical,
		Title: "CLI report", Snippet: "provider-only snippet", Provider: "test",
		State: webevidence.SourceDiscovered, DiscoveredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := func(toolName, key string) webevidence.Operation {
		keyDigest, digestErr := webevidence.ScopedOperationKeyDigest(runID, key)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		fingerprint, fingerprintErr := webevidence.RequestFingerprint(map[string]string{"key": key})
		if fingerprintErr != nil {
			t.Fatal(fingerprintErr)
		}
		return webevidence.Operation{ProtocolVersion: webevidence.OperationProtocolVersion,
			KeyDigest: keyDigest, RequestFingerprint: fingerprint, RunID: runID,
			ToolName: toolName, Response: json.RawMessage(`{"stored":true}`), CreatedAt: now}
	}
	if _, _, err := state.SaveWebSearch(ctx, []webevidence.Source{source},
		operation("web_search", "cli-search")); err != nil {
		t.Fatal(err)
	}
	digest := webevidence.DigestBytes([]byte("CLI raw body"))
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{
		ID: webevidence.StableSnapshotID(source.ID, digest, now), SourceID: source.ID,
		RunID: runID, MissionID: mission.ID, RequestedURL: canonical, FinalURL: canonical,
		Title: "CLI report", FetchedAt: now, StaleAt: now.Add(24 * time.Hour),
		Digest: digest, MIME: "text/html", Charset: "utf-8", Body: "private CLI page body",
		State: webevidence.SourcePartial, Truncated: true, Robots: "allowed", Provider: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.SaveWebFetch(ctx, source, snapshot,
		operation("web_fetch", "cli-fetch")); err != nil {
		t.Fatal(err)
	}
	citationKey, _ := webevidence.ScopedOperationKeyDigest(runID, "cli-cite")
	citation, err := webevidence.SealCitation(webevidence.Citation{
		ID: webevidence.StableCitationID(runID, citationKey), RunID: runID,
		SourceID: source.ID, SnapshotID: snapshot.ID, Claim: "CLI claim", URL: canonical,
		Title: "CLI report", FetchedAt: now, StaleAt: snapshot.StaleAt, Digest: digest,
		Partial: true, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.SaveWebCitation(ctx, citation,
		operation("web_citation", "cli-cite")); err != nil {
		t.Fatal(err)
	}
}
