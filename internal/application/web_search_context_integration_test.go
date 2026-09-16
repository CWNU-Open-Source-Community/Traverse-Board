package application_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/webevidence"
)

type searchContextBackend struct{ calls int }

func (*searchContextBackend) Name() string     { return "context-search" }
func (*searchContextBackend) Endpoint() string { return "https://docs.example.com/search" }
func (p *searchContextBackend) Search(context.Context, string, int, webevidence.NetworkAuthority) ([]webevidence.ProviderResult, error) {
	p.calls++
	return []webevidence.ProviderResult{{URL: "https://docs.example.com/actual-paper", Rank: 1,
		Title: "中文来源的完整标题必须仍可读取", Snippet: strings.Repeat("这是搜索时发现的原始摘要，尚未读取页面。", 45) + "RAW_SEARCH_END_CANARY"}}, nil
}

func TestThreadSearchContextReadsOriginalByProjectedIdentityWithoutReplay(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "search-context.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("search-model-id", "web_search", `{"version":"web_search.v1","query":"context-discovery-source","limit":1}`),
		textResponse(rootActionResponse(domain.RootActionFinish, "搜索记录已取得。", "本轮答复结束。", "")),
	}}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "Find one source and preserve its original discovery record", Profile: "review", Interactive: true,
		ModelRoute: provider.Name() + "/model", NetworkMode: "allowlist", AllowedTargets: []string{"docs.example.com"},
		Budget: domain.Budget{MaxTurns: 8, MaxToolCalls: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	search := &searchContextBackend{}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	turns := application.NewThreadTurnService(st, application.NewRunLifecycleControlService(st),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()).WithWebEvidence(webevidence.NewService(st, search, nil)))
	input := application.ExecuteThreadTurnRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID),
		Content: "Find the source.", OperationKey: "search-context-original-input", RequestedBy: "test_operator"}
	result, err := turns.Execute(t.Context(), input)
	if err != nil || result.Submission.Message.Status != domain.OperatorSteeringCommitted || search.calls != 1 || len(provider.Requests()) != 2 {
		t.Fatalf("ordinary Thread failed: search=%d model=%d err=%v", search.calls, len(provider.Requests()), err)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 20)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatal("missing real tool round", err)
	}
	call := rounds[0].Calls[0]
	var projected string
	for _, message := range provider.Requests()[1].Messages {
		for _, toolResult := range message.ToolResults {
			if toolResult.ToolCallID == call.CallID {
				projected = toolResult.Content
			}
		}
	}
	var outer struct {
		Status   string
		Stdout   string
		Metadata map[string]string
	}
	var output struct {
		OriginalResult domain.HistoryReadRequest `json:"original_result"`
		Sources        []struct {
			SourceID string `json:"source_id"`
			URL      string `json:"canonical_url"`
			Snippet  string `json:"snippet"`
		}
	}
	if json.Unmarshal([]byte(projected), &outer) != nil || json.Unmarshal([]byte(outer.Stdout), &output) != nil ||
		outer.Status != "completed" || len(output.Sources) != 1 || output.OriginalResult.SourceID == "" ||
		outer.Metadata["result_sha256"] != session.ContentSHA256(call.ResultJSON) ||
		strings.Contains(projected, "RAW_SEARCH_END_CANARY") || !strings.Contains(call.ResultJSON, "RAW_SEARCH_END_CANARY") {
		t.Fatal("model projection did not preserve a verifiable original receipt")
	}
	source, err := st.GetWebSource(t.Context(), run.ID, output.Sources[0].SourceID)
	if err != nil || source.CanonicalURL != output.Sources[0].URL || !strings.Contains(source.Snippet, "RAW_SEARCH_END_CANARY") {
		t.Fatal("projected source ID cannot recover exact saved source", err)
	}
	read := output.OriginalResult
	read.Limit = 256
	var recovered strings.Builder
	for pages := 0; pages < 100; pages++ {
		page, err := st.ReadThreadHistory(t.Context(), run.ID, read)
		if err != nil || page.Record.CallID != call.CallID || page.Record.Status != "completed" ||
			page.Record.ArgumentsSHA256 != session.ContentSHA256(call.PayloadJSON) || page.ContentSHA256 != session.ContentSHA256(call.ResultJSON) {
			t.Fatal("projected opaque ID failed original scoped read", err)
		}
		recovered.WriteString(page.Content)
		if !page.HasMore {
			break
		}
		read.Offset = page.NextOffset
	}
	if recovered.String() != call.ResultJSON {
		t.Fatal("paged original result changed bytes")
	}
	if _, err := turns.Execute(t.Context(), input); err != nil || search.calls != 1 || len(provider.Requests()) != 2 {
		t.Fatal("original input key replay executed again", err)
	}
	current, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 20)
	if err != nil || current[0].Calls[0].ResultJSON != call.ResultJSON {
		t.Fatal("read/model projection mutated durable result", err)
	}
}
