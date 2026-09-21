package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type cliApprovalProvider struct {
	mu        sync.Mutex
	responses []*llm.ChatResponse
	requests  int
}

func (*cliApprovalProvider) Name() string { return "cli-approval-test" }

func (*cliApprovalProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", Provider: "cli-approval-test",
		Capabilities: []string{"chat", "tools"}}}, nil
}

func (p *cliApprovalProvider) Chat(_ context.Context,
	request llm.ChatRequest,
) (*llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++
	if len(p.responses) == 0 {
		return nil, errors.New("unexpected CLI approval model request")
	}
	response := *p.responses[0]
	p.responses = p.responses[1:]
	response.ToolCalls = append([]llm.ToolCall(nil), response.ToolCalls...)
	return &response, nil
}

func (p *cliApprovalProvider) StreamChat(ctx context.Context,
	request llm.ChatRequest,
) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	chunks := make(chan llm.ChatChunk, 2)
	if response.Text != "" {
		chunks <- llm.ChatChunk{Text: response.Text}
	}
	chunks <- llm.FinalChatChunk(response)
	close(chunks)
	return chunks, nil
}

func (*cliApprovalProvider) SupportsTools(string) bool    { return true }
func (*cliApprovalProvider) SupportsVision(string) bool   { return false }
func (*cliApprovalProvider) SupportsJSONMode(string) bool { return true }

func (p *cliApprovalProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

type cliApprovalFetchBackend struct {
	mu    sync.Mutex
	calls int
}

func (b *cliApprovalFetchBackend) Fetch(_ context.Context, rawURL string,
	_ webevidence.NetworkAuthority, _ webevidence.RobotsPolicy,
) (webevidence.FetchedContent, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return webevidence.FetchedContent{RequestedURL: rawURL, FinalURL: rawURL,
		HTTPStatus: http.StatusOK,
		RawDigest:  webevidence.DigestBytes([]byte("approval continuation evidence")),
		Robots:     "allowed", Parsed: webevidence.ParsedDocument{
			Title: "Evidence", Body: "bounded evidence", MIME: "text/html",
			Charset: "utf-8",
		}}, nil
}

func (b *cliApprovalFetchBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

type cliApprovalFixture struct {
	state      *store.SQLiteStore
	run        domain.Run
	approvalID string
	auth       domain.WebFetchAuthorization
	provider   *cliApprovalProvider
	backend    *cliApprovalFetchBackend
	router     *llm.Router
	handoff    *application.RunExecutionHandoffService
}

func newCLIApprovalFixture(t *testing.T, home string,
	continuation *llm.ChatResponse,
) cliApprovalFixture {
	t.Helper()
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(t.Context(),
		application.CreateRunRequest{
			Goal: "review one public source", Profile: "review", Surface: "code",
			Phase: "deliver", ModelRoute: "cli-approval-test/model", Interactive: true,
			NetworkMode: "disabled",
			Budget:      domain.Budget{MaxTurns: 4, MaxToolCalls: 4},
		})
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	responses := []*llm.ChatResponse{{
		Provider: "cli-approval-test", Model: "model",
		Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		ToolCalls: []llm.ToolCall{{ID: "web-fetch-review",
			Name: string(toolgateway.WebFetchTool), Arguments: json.RawMessage(
				`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`)}},
	}}
	if continuation != nil {
		responses = append(responses, continuation)
	}
	provider := &cliApprovalProvider{responses: responses}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	checker := policy.NewDefaultChecker()
	backend := &cliApprovalFetchBackend{}
	handoff := application.NewRunExecutionHandoffService(state, router, checker).
		WithWebEvidence(webevidence.NewService(state, nil, backend)).
		WithWebFetchAuthorizationScheduler(true)
	turns := application.NewThreadTurnService(state,
		application.NewRunLifecycleControlService(state), handoff)
	first, err := turns.Execute(t.Context(), application.ExecuteThreadTurnRequest{
		Version:  domain.ThreadMessageProtocolVersion,
		ThreadID: domain.InitialThreadID(run.ID), Content: "Read the reviewed source",
		OperationKey: "cli-approval-original-input", RequestedBy: "test_operator",
	})
	if err != nil || first.Submission.Run.Status != domain.RunWaitingApproval {
		_ = state.Close()
		t.Fatalf("initial approval boundary=%#v err=%v", first, err)
	}
	records, err := state.ListApprovals(t.Context(), approval.ListFilter{
		RunID: run.ID, Status: approval.StatusPending, Limit: 10,
	})
	if err != nil || len(records) != 1 {
		_ = state.Close()
		t.Fatalf("pending approvals=%#v err=%v", records, err)
	}
	auth, err := state.GetWebFetchAuthorizationByApproval(t.Context(), records[0].ID)
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	return cliApprovalFixture{state: state, run: run, approvalID: records[0].ID,
		auth: auth, provider: provider, backend: backend, router: router, handoff: handoff}
}

func cliApprovalTextResponse(t *testing.T, message string) *llm.ChatResponse {
	t.Helper()
	encoded, err := json.Marshal(domain.RootAction{Version: domain.RootLifecycleVersion,
		Kind: domain.RootActionFinish, Message: message, Summary: "done"})
	if err != nil {
		t.Fatal(err)
	}
	return &llm.ChatResponse{Text: string(encoded), Provider: "cli-approval-test",
		Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}
}

func executeCLIApproval(t *testing.T, router *llm.Router,
	action, approvalID string,
) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{"approval", action, approvalID}
	if action == "deny" {
		args = append(args, "--reason", "operator review")
	}
	code := executeContextWithConfig(t.Context(), args, &out, &errOut, func(app *App) {
		app.router = router
	})
	return out.String(), errOut.String(), code
}

func decideCLIApprovalFixture(t *testing.T, fixture cliApprovalFixture,
	action application.ApprovalControlAction,
) {
	t.Helper()
	_, err := application.NewApprovalControlService(fixture.state,
		toolgateway.New(fixture.state, policy.NewDefaultChecker()), policy.NewDefaultChecker()).
		Decide(t.Context(), application.DecideApprovalControlRequest{
			Version: application.ApprovalControlProtocolVersion, RunID: fixture.run.ID,
			ApprovalID: fixture.approvalID, Action: action,
			OperationKey: "cli-approval-" + fixture.approvalID + "-approve-once",
			ReviewedBy:   "cli_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCLIApprovalCompletedContinuationReplaysWithoutModelOrTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	fixture := newCLIApprovalFixture(t, home,
		cliApprovalTextResponse(t, "reviewed source observed"))
	defer fixture.state.Close()
	decideCLIApprovalFixture(t, fixture, application.ApprovalControlApproveOnce)
	result, replayed, err := fixture.handoff.ResumeWebFetchAuthorization(t.Context(),
		fixture.run.ID, fixture.auth.ID)
	if err != nil || replayed || result.Turn == 0 || fixture.backend.callCount() != 1 {
		t.Fatalf("initial continuation=%#v replayed=%t fetches=%d err=%v",
			result, replayed, fixture.backend.callCount(), err)
	}
	modelsBefore, fetchesBefore := fixture.provider.requestCount(), fixture.backend.callCount()
	for attempt := 0; attempt < 2; attempt++ {
		stdout, stderr, code := executeCLIApproval(t, fixture.router, "approve-once",
			fixture.approvalID)
		if code != 0 || stderr != "" ||
			!strings.Contains(stdout, "decision_replayed: true") ||
			!strings.Contains(stdout, "continuation: completed") ||
			!strings.Contains(stdout, "continuation_replayed: true") {
			t.Fatalf("replay %d stdout=%q stderr=%q code=%d", attempt, stdout, stderr, code)
		}
	}
	if fixture.provider.requestCount() != modelsBefore ||
		fixture.backend.callCount() != fetchesBefore {
		t.Fatalf("replay executed work: models=%d/%d fetches=%d/%d",
			fixture.provider.requestCount(), modelsBefore,
			fixture.backend.callCount(), fetchesBefore)
	}
}

func TestCLIApprovalDenyContinuesOnceAndReplaysAcrossApps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	fixture := newCLIApprovalFixture(t, home,
		cliApprovalTextResponse(t, "denial observed"))
	defer fixture.state.Close()
	first, stderr, code := executeCLIApproval(t, fixture.router, "deny", fixture.approvalID)
	if code != 0 || stderr != "" || !strings.Contains(first, "continuation: completed") ||
		fixture.provider.requestCount() != 2 || fixture.backend.callCount() != 0 {
		t.Fatalf("first deny stdout=%q stderr=%q code=%d models=%d fetches=%d",
			first, stderr, code, fixture.provider.requestCount(), fixture.backend.callCount())
	}
	second, stderr, code := executeCLIApproval(t, fixture.router, "deny", fixture.approvalID)
	if code != 0 || stderr != "" ||
		!strings.Contains(second, "decision_replayed: true") ||
		!strings.Contains(second, "continuation_replayed: true") ||
		fixture.provider.requestCount() != 2 || fixture.backend.callCount() != 0 {
		t.Fatalf("replayed deny stdout=%q stderr=%q code=%d models=%d fetches=%d",
			second, stderr, code, fixture.provider.requestCount(), fixture.backend.callCount())
	}
}

func TestCLIApprovalDoesNotWakePausedOrCancelledRun(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunPaused, domain.RunCancelled} {
		t.Run(string(status), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CYBERAGENT_HOME", home)
			continuation := cliApprovalTextResponse(t, "must not be used")
			if status == domain.RunPaused {
				continuation = nil
			}
			fixture := newCLIApprovalFixture(t, home, continuation)
			defer fixture.state.Close()
			decideCLIApprovalFixture(t, fixture, application.ApprovalControlApproveOnce)
			if status == domain.RunPaused {
				if _, _, err := fixture.handoff.ResumeWebFetchAuthorization(t.Context(),
					fixture.run.ID, fixture.auth.ID); err == nil {
					t.Fatal("expected the injected provider failure to pause the Run")
				}
			} else if _, err := application.NewRunService(fixture.state).Cancel(t.Context(),
				fixture.run.ID); err != nil {
				t.Fatal(err)
			}
			modelsBefore := fixture.provider.requestCount()
			fetchesBefore := fixture.backend.callCount()
			stdout, stderr, code := executeCLIApproval(t, fixture.router, "approve-once",
				fixture.approvalID)
			if code != 0 || stderr != "" ||
				!strings.Contains(stdout, "continuation: not_started") ||
				!strings.Contains(stdout, "continuation_replayed: false") {
				t.Fatalf("status=%s stdout=%q stderr=%q code=%d", status, stdout, stderr, code)
			}
			stored, err := fixture.state.GetRun(t.Context(), fixture.run.ID)
			if err != nil || stored.Status != status ||
				fixture.provider.requestCount() != modelsBefore ||
				fixture.backend.callCount() != fetchesBefore {
				t.Fatalf("status=%s stored=%#v models=%d/%d fetches=%d err=%v",
					status, stored, fixture.provider.requestCount(), modelsBefore,
					fixture.backend.callCount(), err)
			}
		})
	}
}
