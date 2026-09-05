package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

func webOperation(t *testing.T, runID, toolName, key string, response any,
	createdAt time.Time,
) webevidence.Operation {
	t.Helper()
	keyDigest, err := webevidence.ScopedOperationKeyDigest(runID, key)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := webevidence.RequestFingerprint(map[string]string{
		"tool": toolName, "key": key,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return webevidence.Operation{ProtocolVersion: webevidence.OperationProtocolVersion,
		KeyDigest: keyDigest, RequestFingerprint: fingerprint, RunID: runID,
		ToolName: toolName, Response: raw, CreatedAt: createdAt}
}

func TestWebEvidenceLedgerIsImmutableReplayableAndRunScoped(t *testing.T) {
	ctx := context.Background()
	state := openStructuredToolTestStore(t)
	mission, run := createStructuredToolTestRun(t, ctx, state, "web evidence ledger")
	_, otherRun := createStructuredToolTestRun(t, ctx, state, "other web evidence ledger")
	now := run.CreatedAt.Add(time.Second)
	canonical := "https://docs.example.com/report"
	source, err := webevidence.SealSource(webevidence.Source{
		ID: webevidence.StableSourceID(run.ID, canonical), RunID: run.ID,
		MissionID: mission.ID, WorkspaceID: mission.WorkspaceID, CanonicalURL: canonical,
		Title: "Report", Provider: "direct", State: webevidence.SourceDiscovered,
		DiscoveredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	searchOperation := webOperation(t, run.ID, "web_search", "search", map[string]any{
		"protocol_version": webevidence.SearchProtocolVersion,
	}, now)
	if _, replayed, err := state.SaveWebSearch(ctx, []webevidence.Source{source},
		searchOperation); err != nil || replayed {
		t.Fatalf("save search replayed=%t err=%v", replayed, err)
	}
	if _, replayed, err := state.SaveWebSearch(ctx, []webevidence.Source{source},
		searchOperation); err != nil || !replayed {
		t.Fatalf("replay search replayed=%t err=%v", replayed, err)
	}

	digest := webevidence.DigestBytes([]byte("bounded raw body"))
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{
		ID: webevidence.StableSnapshotID(source.ID, digest, now), SourceID: source.ID,
		RunID: run.ID, MissionID: mission.ID, RequestedURL: canonical, FinalURL: canonical,
		Title: "Report", FetchedAt: now, StaleAt: now.Add(24 * time.Hour), Digest: digest,
		MIME: "text/html", Charset: "utf-8", Body: "verified body",
		State: webevidence.SourceFetched, Robots: "allowed", Provider: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	fetchOperation := webOperation(t, run.ID, "web_fetch", "fetch", map[string]any{
		"protocol_version": webevidence.FetchProtocolVersion,
	}, now)
	if _, replayed, err := state.SaveWebFetch(ctx, source, snapshot,
		fetchOperation); err != nil || replayed {
		t.Fatalf("save fetch replayed=%t err=%v", replayed, err)
	}

	citationKey, _ := webevidence.ScopedOperationKeyDigest(run.ID, "cite")
	citation, err := webevidence.SealCitation(webevidence.Citation{
		ID: webevidence.StableCitationID(run.ID, citationKey), RunID: run.ID,
		SourceID: source.ID, SnapshotID: snapshot.ID, Claim: "Verified claim",
		URL: canonical, Title: "Report", FetchedAt: now, StaleAt: snapshot.StaleAt,
		Digest: digest, CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	citeOperation := webOperation(t, run.ID, "web_citation", "cite", map[string]any{
		"protocol_version": webevidence.CitationProtocolVersion,
	}, citation.CreatedAt)
	if _, replayed, err := state.SaveWebCitation(ctx, citation,
		citeOperation); err != nil || replayed {
		t.Fatalf("save citation replayed=%t err=%v", replayed, err)
	}
	inventory, err := webevidence.LoadInventory(ctx, state, run.ID, 10, now.Add(time.Hour))
	if err != nil || len(inventory.Sources) != 1 || len(inventory.Snapshots) != 1 ||
		len(inventory.Citations) != 1 || inventory.Citations[0].URL != canonical ||
		inventory.Citations[0].Status != "fetched" || !inventory.Untrusted ||
		inventory.InstructionAuthorized {
		t.Fatalf("inventory=%#v err=%v", inventory, err)
	}

	crossRun := citation
	crossRun.RunID = otherRun.ID
	crossRun.ID = "web-citation-cross-run"
	crossRun, err = webevidence.SealCitation(crossRun)
	if err != nil {
		t.Fatal(err)
	}
	crossOperation := webOperation(t, otherRun.ID, "web_citation", "cross-cite",
		map[string]string{"status": "invalid"}, now.Add(2*time.Second))
	if _, _, err := state.SaveWebCitation(ctx, crossRun, crossOperation); err == nil {
		t.Fatal("cross-Run snapshot citation was accepted")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE web_evidence_sources
		SET canonical_url = 'https://evil.example.net/' WHERE id = ?`, source.ID); err == nil {
		t.Fatal("immutable web source was updated")
	}
	if _, err := state.db.ExecContext(ctx, `DELETE FROM web_evidence_snapshots
		WHERE id = ?`, snapshot.ID); err == nil {
		t.Fatal("immutable web snapshot was deleted")
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestSupervisorWebEvidenceEventProjectionWhitelistsOnlyPublicMetadata(t *testing.T) {
	metadata := map[string]string{
		"source_id": "source-web", "snapshot_id": "snapshot-web",
		"citation_id": "citation-web", "url": "https://docs.example.com/report",
		"title": "Report", "state": "partial", "fetched_at": "2026-08-25T00:00:00Z",
		"stale_at": "2026-08-26T00:00:00Z", "digest": strings.Repeat("a", 64),
		"partial": "true", "stale": "false", "citeable": "true",
		"robots": "not_checked",
	}
	result, _ := json.Marshal(map[string]any{"version": "supervisor_tool_result.v1",
		"tool": string(toolgateway.WebCitationTool), "status": "completed",
		"metadata": metadata, "stdout": "private fetched body"})
	call := domain.SupervisorToolCall{ToolName: string(toolgateway.WebCitationTool),
		Status: domain.SupervisorToolCompleted, ResultJSON: string(result)}
	payload := supervisorToolStreamEventPayload(call, call.Status, llm.StreamEventType(""))
	presentation, ok := payload["web_evidence"].(map[string]any)
	if !ok || presentation["url"] != metadata["url"] || presentation["untrusted"] != true ||
		presentation["instruction_authorized"] != false ||
		presentation["robots"] != "not_checked" {
		t.Fatalf("presentation=%#v", presentation)
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "private fetched body") {
		t.Fatalf("event leaked tool output: %s", encoded)
	}
	for _, robots := range []string{"not_present", "bypassed_disallow", "bypassed_unknown"} {
		metadata["robots"] = robots
		result, _ = json.Marshal(map[string]any{"version": "supervisor_tool_result.v1",
			"tool": string(toolgateway.WebCitationTool), "status": "completed",
			"metadata": metadata})
		call.ResultJSON = string(result)
		payload = supervisorToolStreamEventPayload(call, call.Status, llm.StreamEventType(""))
		presentation, ok = payload["web_evidence"].(map[string]any)
		if !ok || presentation["robots"] != robots {
			t.Fatalf("robots=%q presentation=%#v", robots, presentation)
		}
	}
	metadata["url"] = "https://127.0.0.1/private"
	result, _ = json.Marshal(map[string]any{"version": "supervisor_tool_result.v1",
		"tool": string(toolgateway.WebCitationTool), "status": "completed", "metadata": metadata})
	call.ResultJSON = string(result)
	payload = supervisorToolStreamEventPayload(call, call.Status, llm.StreamEventType(""))
	if _, exists := payload["web_evidence"]; exists {
		t.Fatal("unsafe Web evidence metadata reached the public event")
	}
}

func TestWebEvidenceStoreRejectsOperationKeyInputConflict(t *testing.T) {
	ctx := context.Background()
	state := openStructuredToolTestStore(t)
	mission, run := createStructuredToolTestRun(t, ctx, state, "web operation conflict")
	now := run.CreatedAt.Add(time.Second)
	canonical := "https://docs.example.com/conflict"
	source, err := webevidence.SealSource(webevidence.Source{ID: webevidence.StableSourceID(run.ID, canonical),
		RunID: run.ID, MissionID: mission.ID, WorkspaceID: mission.WorkspaceID,
		CanonicalURL: canonical, Provider: "direct", State: webevidence.SourceDiscovered,
		DiscoveredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	operation := webOperation(t, run.ID, "web_search", "conflict", map[string]string{"query": "one"}, now)
	if _, _, err := state.SaveWebSearch(ctx, []webevidence.Source{source}, operation); err != nil {
		t.Fatal(err)
	}
	operation.RequestFingerprint = strings.Repeat("b", 64)
	if _, _, err := state.SaveWebSearch(ctx, []webevidence.Source{source}, operation); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("conflict code=%s err=%v", apperror.CodeOf(err), err)
	}
}
