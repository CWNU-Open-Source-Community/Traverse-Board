package application

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/webevidence"
)

func searchContextFixture(t *testing.T) (domain.SupervisorToolCall, webevidence.SearchResult) {
	t.Helper()
	at := time.Date(2026, 9, 13, 15, 0, 0, 0, time.UTC)
	result := webevidence.SearchResult{ProtocolVersion: webevidence.SearchProtocolVersion,
		Query: "精确搜索 query", Provider: "test-search", SearchPolicy: "web", SearchedAt: at,
		Sources: []webevidence.SearchStub{{SourceID: "source-context", CanonicalURL: "https://example.com/paper",
			Provider: "test-search", Rank: 1, Untrusted: true,
			Title: strings.Repeat("论文🙂e\u0301", 30), Snippet: strings.Repeat("原文摘要 保留完整回读。", 100)}}}
	call := domain.SupervisorToolCall{RunID: "run-context", AttemptID: "attempt-context", Turn: 1,
		CallID: "call-context", Round: 1, Position: 1, ModelAttempt: 1, ToolName: "web_search",
		PayloadJSON:   `{"version":"web_search.v1","query":"精确搜索 query","limit":1}`,
		AuthorityJSON: `{}`, Status: domain.SupervisorToolCompleted, CreatedAt: at, CompletedAt: &at}
	call.ResultJSON = searchContextEnvelope(t, result)
	return call, result
}

func searchContextEnvelope(t *testing.T, result webevidence.SearchResult) string {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{
		Version: supervisorToolResultVersion, Tool: "web_search", Status: "completed", Stdout: string(encoded),
		Metadata: map[string]string{"provider": result.Provider, "untrusted_output": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	return string(envelope)
}

func TestSupervisorWebSearchContextPreservesIdentitiesAndBoundedUnicode(t *testing.T) {
	call, original := searchContextFixture(t)
	before := call.ResultJSON
	projected, err := supervisorWebSearchContextResult(call)
	if err != nil {
		t.Fatal(err)
	}
	var outer supervisorToolResultEnvelope
	var output webSearchContextOutput
	if json.Unmarshal([]byte(projected), &outer) != nil || json.Unmarshal([]byte(outer.Stdout), &output) != nil {
		t.Fatal("invalid projected JSON")
	}
	if call.ResultJSON != before || outer.Status != string(call.Status) || outer.Tool != call.ToolName ||
		outer.Metadata["result_sha256"] != session.ContentSHA256(before) || outer.Metadata["arguments_sha256"] != session.ContentSHA256(call.PayloadJSON) ||
		output.OriginalResult.ExpectedSHA256 != session.ContentSHA256(before) || output.OriginalResult.Part != "result" ||
		output.Query != original.Query || output.SearchedAt != original.SearchedAt || len(output.Sources) != 1 ||
		!output.Untrusted || output.InstructionAuthorized || output.Citeable || output.Fetched || output.LocallyVerified ||
		!strings.Contains(output.SourceStateAt, "this_search_operation") || !output.ContextExcerpt || !outer.Truncated {
		t.Fatal("projection changed durable evidence, identity, status or discovery qualification")
	}
	source := output.Sources[0]
	if source.SourceID != original.Sources[0].SourceID || source.URL != original.Sources[0].CanonicalURL || source.Rank != 1 ||
		!source.TitleTruncated || !source.SnippetTruncated || !utf8.ValidString(source.Title+source.Snippet) ||
		!strings.HasPrefix(original.Sources[0].Title, source.Title) || !strings.HasPrefix(original.Sources[0].Snippet, source.Snippet) ||
		contextmgr.EstimateTokens(source.Title) > webSearchContextTitleTokens || contextmgr.EstimateTokens(source.Snippet) > webSearchContextSnippetTokens {
		t.Fatal("source identity or explicit Unicode excerpt bounds changed")
	}
	if contextmgr.EstimateTokens(projected) >= contextmgr.EstimateTokens(before) {
		t.Fatal("large discovery result did not become smaller")
	}
}

func TestSupervisorWebSearchContextKeepsQualifiedUnknownAndFailureResults(t *testing.T) {
	call, result := searchContextFixture(t)
	citation, err := webevidence.SealProviderGroundedCitation(webevidence.ProviderGroundedCitation{
		ID: "grounded-context", RunID: call.RunID, SourceID: result.Sources[0].SourceID, URL: result.Sources[0].CanonicalURL,
		Provider: result.Provider, ProviderBinding: strings.Repeat("a", 64), Provenance: webevidence.ProviderGroundedProvenance,
		SearchedAt: result.SearchedAt, ProviderQualified: true, Untrusted: true})
	if err != nil || citation.Validate() != nil {
		t.Fatal("invalid qualified fixture", err)
	}
	for _, kind := range []string{"grounded", "mixed_provider", "citeable", "authorized", "unknown_outer", "unknown_source", "failed"} {
		t.Run(kind, func(t *testing.T) {
			changed := call
			copyResult := result
			copyResult.Sources = append([]webevidence.SearchStub(nil), result.Sources...)
			switch kind {
			case "grounded":
				copyResult.Sources[0].ProviderGroundedCitation = &citation
				copyResult.Sources[0].Citeable = true
				copyResult.Sources[0].Provenance = webevidence.ProviderGroundedProvenance
			case "mixed_provider":
				copyResult.Sources[0].Provider = "old-provider"
			case "citeable":
				copyResult.Sources[0].Citeable = true
			case "authorized":
				copyResult.Sources[0].InstructionAuthorized = true
			case "failed":
				changed.Status = domain.SupervisorToolFailed
			}
			changed.ResultJSON = searchContextEnvelope(t, copyResult)
			if kind == "unknown_outer" {
				changed.ResultJSON = strings.TrimSuffix(changed.ResultJSON, "}") + `,"future_qualification":true}`
			}
			if kind == "unknown_source" {
				var envelope supervisorToolResultEnvelope
				_ = json.Unmarshal([]byte(changed.ResultJSON), &envelope)
				envelope.Stdout = strings.Replace(envelope.Stdout, `"rank":1`, `"rank":1,"future_evidence":"unknown"`, 1)
				encoded, _ := marshalSupervisorToolResultEnvelope(envelope)
				changed.ResultJSON = string(encoded)
			}
			got, err := supervisorWebSearchContextResult(changed)
			if err != nil || got != changed.ResultJSON {
				t.Fatal("unsupported/qualified result was rewritten", err)
			}
		})
	}
}
