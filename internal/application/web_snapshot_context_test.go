package application

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type snapshotPageStore struct {
	WebEvidenceToolStore
	source   webevidence.Source
	snapshot webevidence.Snapshot
}

func TestSavedWebSnapshotPageRetainsConnectorContinuationFailure(t *testing.T) {
	state, scope := savedSnapshotFixture(t, "Saved root and first comment")
	state.snapshot.Connector = "github"
	state.snapshot.ConnectorVersion = "github-public.v1"
	state.snapshot.ContentKind = "github_issue_thread"
	state.snapshot.Coverage = "body_and_issue_comments"
	state.snapshot.RawDigest = webevidence.DigestBytes([]byte("original JSON"))
	state.snapshot.RequestEndpoints = []string{"https://api.github.com/repos/example/project/issues/1/comments"}
	state.snapshot.ItemsIncluded, state.snapshot.ItemsAvailable = 2, 4
	state.snapshot.TruncationReason = "comment_read_failed"
	state.snapshot.ContinuationFailure = &webevidence.ConnectorFailure{Connector: "github", Code: "rate_limited", HTTPStatus: 429,
		Endpoint: state.snapshot.RequestEndpoints[0], RetryAfter: "60", RemoteRequestID: "partial-request"}
	var err error
	state.snapshot, err = webevidence.SealSnapshot(state.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	before := state.snapshot
	offset, limit := 0, 5
	executor := &WebEvidenceToolExecutor{store: state}
	result, err := executor.readWebSnapshotPage(t.Context(), scope, toolgateway.WebFetchPayload{Version: "web_fetch.v1",
		SourceID: state.source.ID, SnapshotID: state.snapshot.ID, Offset: &offset, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	var output webFetchToolOutput
	if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output.Snapshot.ContinuationFailure, before.ContinuationFailure) || output.Snapshot.State != webevidence.SourcePartial ||
		output.Snapshot.TruncationReason != "comment_read_failed" || output.Snapshot.Coverage != before.Coverage || output.Snapshot.Body != "Saved" ||
		!reflect.DeepEqual(state.snapshot, before) || result.Metadata["network_called"] != "false" {
		t.Fatalf("saved page lost partial coverage/diagnostics: %+v", output)
	}
}

func TestSupervisorWebFetchMultilingualPagesReassembleOriginalAtTokenLimit(t *testing.T) {
	body := strings.Repeat("中文证据🙂e\u0301。", 310) + "THE_END"
	state, scope := savedSnapshotFixture(t, body)
	executor := &WebEvidenceToolExecutor{store: state}
	before := state.snapshot
	var assembled strings.Builder
	offset, limit, pages := 0, toolgateway.MaxWebSnapshotPageRunes, 0
	for {
		result, err := executor.readWebSnapshotPage(t.Context(), scope, toolgateway.WebFetchPayload{
			Version: "web_fetch.v1", SourceID: state.source.ID, SnapshotID: state.snapshot.ID,
			Offset: &offset, Limit: &limit})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
			Version: supervisorToolResultVersion, Tool: "web_fetch", Status: "completed",
			Stdout: result.Content, Metadata: result.Metadata, Truncated: result.Truncated})
		if err != nil {
			t.Fatal(err)
		}
		call := domain.SupervisorToolCall{ToolName: "web_fetch", Status: domain.SupervisorToolCompleted, ResultJSON: string(raw)}
		projected, err := supervisorWebFetchContextResult(call)
		if err != nil {
			t.Fatal(err)
		}
		var envelope supervisorToolResultEnvelope
		var output webFetchToolOutput
		if json.Unmarshal([]byte(projected), &envelope) != nil || json.Unmarshal([]byte(envelope.Stdout), &output) != nil {
			t.Fatal("invalid projected page")
		}
		page := output.Snapshot
		if !utf8.ValidString(page.Body) || contextmgr.EstimateTokens(page.Body) > webSnapshotContextBodyTokens ||
			utf8.RuneCountInString(page.Body) > limit || page.BodyOffset != offset ||
			page.BodyRunes != utf8.RuneCountInString(body) || page.Digest != before.Digest ||
			page.SourceID != before.SourceID || page.SnapshotID != before.ID ||
			!page.SnapshotTruncated || page.State != webevidence.SourcePartial ||
			!page.Untrusted || page.InstructionAuthorized || result.Metadata["network_called"] != "false" || call.ResultJSON != string(raw) {
			t.Fatal("projection changed source, provenance, body budget or original result")
		}
		assembled.WriteString(page.Body)
		pages++
		if page.NextOffset == nil {
			break
		}
		next := offset + utf8.RuneCountInString(page.Body)
		if !page.BodyExcerptTruncated || *page.NextOffset != next || next <= offset || pages > 20 {
			t.Fatal("projected page continuation skipped/repeated characters or stalled")
		}
		offset = next
	}
	if assembled.String() != body || pages < 3 || !reflect.DeepEqual(state.snapshot, before) {
		t.Fatal("following projected next_offset did not recover the exact original Unicode snapshot")
	}
}

func (s *snapshotPageStore) GetWebSource(context.Context, string, string) (webevidence.Source, error) {
	return s.source, nil
}

func (s *snapshotPageStore) GetWebSnapshot(context.Context, string, string) (webevidence.Snapshot, error) {
	return s.snapshot, nil
}

func savedSnapshotFixture(t *testing.T, body string) (*snapshotPageStore, toolgateway.WebEvidenceExecutionScope) {
	t.Helper()
	at := time.Date(2026, 9, 10, 4, 6, 0, 0, time.UTC)
	source, err := webevidence.SealSource(webevidence.Source{ID: "source-page", RunID: "run-page",
		MissionID: "mission-page", WorkspaceID: "workspace-page", CanonicalURL: "https://docs.example.com/large",
		Title: "Large source", Provider: "direct", State: webevidence.SourcePartial, DiscoveredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{ID: "snapshot-page", SourceID: source.ID,
		RunID: source.RunID, MissionID: source.MissionID, RequestedURL: source.CanonicalURL,
		FinalURL: source.CanonicalURL, FetchedAt: at, StaleAt: at.Add(24 * time.Hour),
		Digest: webevidence.DigestBytes([]byte(body)), MIME: "text/plain", Charset: "utf-8",
		Body: body, State: webevidence.SourcePartial, Truncated: true, Robots: "allowed", Provider: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	return &snapshotPageStore{source: source, snapshot: snapshot}, toolgateway.WebEvidenceExecutionScope{
		RunID: source.RunID, MissionID: source.MissionID, WorkspaceID: source.WorkspaceID}
}

func TestSavedWebSnapshotPagePreservesUnicodeAndPartialProvenance(t *testing.T) {
	state, scope := savedSnapshotFixture(t, "甲🙂e\u0301乙")
	executor := &WebEvidenceToolExecutor{store: state}
	before := state.snapshot
	for _, test := range []struct {
		offset, limit int
		body          string
		next          int
	}{{0, 2, "甲🙂", 2}, {2, 2048, "e\u0301乙", -1}, {5, 1, "", -1}} {
		request := toolgateway.WebFetchPayload{Version: "web_fetch.v1", SourceID: state.source.ID,
			SnapshotID: state.snapshot.ID, Offset: &test.offset, Limit: &test.limit}
		result, err := executor.readWebSnapshotPage(t.Context(), scope, request)
		if err != nil {
			t.Fatal(err)
		}
		var output webFetchToolOutput
		if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
			t.Fatal(err)
		}
		page := output.Snapshot
		if !utf8.ValidString(page.Body) || page.Body != test.body || page.BodyOffset != test.offset ||
			page.BodyRunes != 5 || page.Digest != before.Digest || !page.SnapshotTruncated ||
			page.State != webevidence.SourcePartial || !page.Untrusted || page.InstructionAuthorized ||
			result.Metadata["network_called"] != "false" || !result.Truncated ||
			(test.next == -1 && page.NextOffset != nil) ||
			(test.next >= 0 && (page.NextOffset == nil || *page.NextOffset != test.next)) {
			t.Fatalf("wrong page or provenance: %+v", output)
		}
	}
	if !reflect.DeepEqual(state.snapshot, before) {
		t.Fatal("paging mutated the saved source snapshot")
	}
}

func TestSavedWebSnapshotPageRejectsScopeAndRangeDrift(t *testing.T) {
	state, scope := savedSnapshotFixture(t, "saved body")
	executor := &WebEvidenceToolExecutor{store: state}
	offset, limit := 0, 2
	request := toolgateway.WebFetchPayload{Version: "web_fetch.v1", SourceID: state.source.ID,
		SnapshotID: state.snapshot.ID, Offset: &offset, Limit: &limit}
	for _, field := range []string{"run", "mission", "workspace", "source", "snapshot", "offset", "limit", "url"} {
		t.Run(field, func(t *testing.T) {
			changedScope, changedRequest := scope, request
			beyond, excessive := 11, toolgateway.MaxWebSnapshotPageRunes+1
			switch field {
			case "run":
				changedScope.RunID = "other-run"
			case "mission":
				changedScope.MissionID = "other-mission"
			case "workspace":
				changedScope.WorkspaceID = "other-workspace"
			case "source":
				changedRequest.SourceID = "other-source"
			case "snapshot":
				changedRequest.SnapshotID = "other-snapshot"
			case "offset":
				changedRequest.Offset = &beyond
			case "limit":
				changedRequest.Limit = &excessive
			case "url":
				changedRequest.URL = "https://docs.example.com/new"
			}
			if _, err := executor.readWebSnapshotPage(t.Context(), changedScope, changedRequest); err == nil {
				t.Fatal("accepted changed source scope or invalid page")
			}
		})
	}
}

type historicalSnapshotPageStore struct {
	*snapshotPageStore
	reads int
}

func (s *historicalSnapshotPageStore) GetWebSource(context.Context, string, string) (webevidence.Source, error) {
	return webevidence.Source{}, apperror.New(apperror.CodeNotFound, "not in current Run")
}

func (s *historicalSnapshotPageStore) GetThreadPredecessorWebSnapshot(_ context.Context,
	runID, missionID, workspaceID, sourceID, snapshotID string,
) (webevidence.Source, webevidence.Snapshot, error) {
	s.reads++
	if runID != "run-successor" || missionID != s.source.MissionID || workspaceID != s.source.WorkspaceID ||
		sourceID != s.source.ID || snapshotID != s.snapshot.ID {
		return webevidence.Source{}, webevidence.Snapshot{}, apperror.New(apperror.CodeNotFound, "wrong history")
	}
	return s.source, s.snapshot, nil
}

func TestSavedWebSnapshotPageKeepsHistoricalIdentityWithoutNetwork(t *testing.T) {
	state, scope := savedSnapshotFixture(t, "甲🙂historic body")
	history := &historicalSnapshotPageStore{snapshotPageStore: state}
	scope.RunID = "run-successor"
	// There is deliberately no network service or approval store to fall back to.
	executor := &WebEvidenceToolExecutor{store: history}
	offset, limit := 1, 2
	result, err := executor.readWebSnapshotPage(t.Context(), scope, toolgateway.WebFetchPayload{
		Version: "web_fetch.v1", SourceID: state.source.ID, SnapshotID: state.snapshot.ID,
		Offset: &offset, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	var output webFetchToolOutput
	if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
		t.Fatal(err)
	}
	if history.reads != 1 || !output.Historical || output.SourceRunID != state.source.RunID ||
		output.Snapshot.SourceID != state.source.ID || output.Snapshot.SnapshotID != state.snapshot.ID ||
		output.Snapshot.Digest != state.snapshot.Digest || output.Snapshot.Body != "🙂h" ||
		result.Metadata["source_run_id"] != state.source.RunID || result.Metadata["historical"] != "true" ||
		result.Metadata["network_called"] != "false" || result.Metadata["instruction_authorized"] != "false" {
		t.Fatalf("historical pagination lost its original identity or read-only semantics: %+v", output)
	}
}

func TestSupervisorWebFetchContextFitsWindowWithoutChangingDurableEvidenceOrToolPairs(t *testing.T) {
	body := strings.Repeat("Node test runner documentation. ", 3500) + "末尾保留"
	state, _ := savedSnapshotFixture(t, body)
	at := state.snapshot.FetchedAt
	output, _, err := encodeWebFetchToolOutput(webevidence.FetchResult{Source: state.source,
		Snapshot: state.snapshot}, webevidence.PresentSnapshot(state.snapshot, at))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
		Version: supervisorToolResultVersion, Tool: "web_fetch", Status: "completed", Stdout: string(output),
		Truncated: true, Metadata: map[string]string{"snapshot_id": state.snapshot.ID, "partial": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	call := domain.SupervisorToolCall{RunID: state.source.RunID, AttemptID: "attempt-page", Turn: 1,
		Round: 1, Position: 1, ModelAttempt: 1, CallID: "call-page", ToolName: "web_fetch",
		PayloadJSON:   `{"version":"web_fetch.v1","url":"https://docs.example.com/large"}`,
		AuthorityJSON: `{}`, Status: domain.SupervisorToolCompleted, ResultJSON: string(envelope),
		CreatedAt: at, CompletedAt: &at}
	round := domain.SupervisorToolRound{RunID: call.RunID, AttemptID: call.AttemptID, Turn: 1,
		Round: 1, ModelAttempt: 1, Calls: []domain.SupervisorToolCall{call}, CreatedAt: at, CompletedAt: &at}
	request := llm.ChatRequest{Messages: []llm.Message{
		{Role: "system", Content: strings.Repeat("必须保留的原始约束。", 250)},
		{Role: "user", Content: "Read the official docs; no files or commands yet."}}}
	unbounded := request
	unbounded.Messages = append(append([]llm.Message(nil), request.Messages...),
		llm.Message{Role: "user", ToolResults: []llm.ToolResult{{ToolCallID: call.CallID, Content: call.ResultJSON}}})
	if _, _, err := constrainRequestToModelWindow(unbounded, llm.DefaultContextWindow(), modelContextLayout{}); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("large actual-shape evidence did not reproduce the context overflow: %v", err)
	}
	projected, err := supervisorRequestWithToolRounds(request, []domain.SupervisorToolRound{round})
	if err != nil {
		t.Fatal(err)
	}
	bounded, _, err := constrainRequestToModelWindow(projected, llm.DefaultContextWindow(), modelContextLayout{})
	if err != nil {
		t.Fatal(err)
	}
	if bounded.Messages[0].Content != request.Messages[0].Content || bounded.Messages[1].Content != request.Messages[1].Content ||
		len(bounded.Messages) != 4 || bounded.Messages[2].ToolCalls[0].ID != call.CallID ||
		bounded.Messages[3].ToolResults[0].ToolCallID != call.CallID || round.Calls[0].ResultJSON != call.ResultJSON ||
		state.snapshot.Body != body || state.snapshot.Fingerprint == "" {
		t.Fatal("context fitting changed constraints, pairing or durable evidence")
	}
	var contextEnvelope supervisorToolResultEnvelope
	if err := json.Unmarshal([]byte(bounded.Messages[3].ToolResults[0].Content), &contextEnvelope); err != nil {
		t.Fatal(err)
	}
	var contextOutput webFetchToolOutput
	if err := json.Unmarshal([]byte(contextEnvelope.Stdout), &contextOutput); err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(contextOutput.Snapshot.Body) != toolgateway.MaxWebSnapshotPageRunes ||
		!contextOutput.Snapshot.BodyExcerptTruncated || !contextOutput.Snapshot.SnapshotTruncated ||
		contextOutput.Snapshot.Digest != state.snapshot.Digest || contextOutput.Snapshot.NextOffset == nil ||
		*contextOutput.Snapshot.NextOffset != toolgateway.MaxWebSnapshotPageRunes {
		t.Fatalf("context excerpt lost its exact continuation or source identity: %+v", contextOutput.Snapshot)
	}
}
