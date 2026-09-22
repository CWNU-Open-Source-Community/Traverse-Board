package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/toolgateway"
)

type agentBrowserProductProvider struct {
	llm.MockProvider
	url      string
	requests []llm.ChatRequest
	pixels   bool
	scenario string
	terminal bool
}

func (p *agentBrowserProductProvider) Name() string               { return "agent-browser-product" }
func (p *agentBrowserProductProvider) SupportsTools(string) bool  { return true }
func (p *agentBrowserProductProvider) SupportsVision(string) bool { return true }
func (p *agentBrowserProductProvider) DescribeVision(string) llm.VisionCapability {
	return llm.VisionCapability{State: llm.VisionSupported, Source: "operator_declared"}
}
func (p *agentBrowserProductProvider) Chat(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, request)
	response := &llm.ChatResponse{Provider: p.Name(), Model: "fixture", Usage: llm.Usage{InputTokens: 4, OutputTokens: 4, TotalTokens: 8}}
	tool := func(name string, payload any) {
		b, _ := json.Marshal(payload)
		response.ToolCalls = []llm.ToolCall{{ID: fmt.Sprintf("provider-browser-%d", len(p.requests)), Name: name, Arguments: b}}
	}
	switch len(p.requests) {
	case 1:
		found := false
		for _, d := range request.Tools {
			if d.Name == "browser_navigate" && strings.Contains(string(d.Parameters), "browser_navigate.v2") {
				found = true
			}
		}
		if !found {
			return nil, errors.New("ordinary Supervisor did not advertise Agent browser v2")
		}
		tool("browser_navigate", map[string]any{"version": "browser_navigate.v2", "url": p.url})
	case 2:
		tool("browser_snapshot", map[string]any{"version": "browser_snapshot.v2"})
	case 3:
		var snapshot browserruntime.AgentBrowserSnapshot
		for _, m := range request.Messages {
			for _, result := range m.ToolResults {
				var envelope struct {
					Tool   string `json:"tool"`
					Stdout string `json:"stdout"`
				}
				if json.Unmarshal([]byte(result.Content), &envelope) == nil && envelope.Tool == "browser_snapshot" {
					_ = json.Unmarshal([]byte(envelope.Stdout), &snapshot)
				}
			}
		}
		ref := ""
		for _, element := range snapshot.Elements {
			if element.Name == "Publish fixture" {
				ref = element.Ref
			}
		}
		if snapshot.SnapshotID == "" || ref == "" {
			return nil, fmt.Errorf("real snapshot did not expose fixture button: %+v", snapshot)
		}
		tool("browser_click", map[string]any{"version": "browser_click.v2", "snapshot_id": snapshot.SnapshotID, "element_ref": ref, "sensitive_intent": map[string]any{"version": "browser_sensitive_intent.v1", "effect": "external_write", "target": snapshot.CanonicalURL, "description": "Publish one synthetic draft to the test-owned fixture", "document_epoch": snapshot.DocumentEpoch}})
		if p.scenario == "" {
			response.ToolCalls = append(response.ToolCalls, llm.ToolCall{ID: "provider-browser-shot", Name: "browser_screenshot", Arguments: json.RawMessage(`{"version":"browser_screenshot.v2"}`)})
		}
	case 4:
		for _, m := range request.Messages {
			if len(m.Images) > 0 && len(m.ToolResults) > 0 {
				p.pixels = true
			}
		}
		if p.scenario != "" {
			want := "browser_authority_expired"
			if p.scenario == "deny_closed" {
				want = "policy_denied"
			}
			for _, m := range request.Messages {
				for _, tr := range m.ToolResults {
					var env struct{ Tool, Code string }
					if json.Unmarshal([]byte(tr.Content), &env) == nil && env.Tool == "browser_click" && env.Code == want {
						p.terminal = true
					}
				}
			}
			if !p.terminal {
				return nil, errors.New("non-dispatch terminal result did not reach model")
			}
		} else if !p.pixels {
			return nil, errors.New("saved real PNG never entered model image input")
		}
		b, _ := json.Marshal(domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionContinue, Message: "Observed the approved synthetic submission and its screenshot."})
		response.Text = string(b)
	default:
		return nil, errors.New("unexpected provider retry or repeated continuation")
	}
	return response, nil
}
func (p *agentBrowserProductProvider) StreamChat(ctx context.Context, r llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, e := p.Chat(ctx, r)
	if e != nil {
		return nil, e
	}
	out := make(chan llm.ChatChunk, 2)
	if response.Text != "" {
		out <- llm.ChatChunk{Text: response.Text}
	}
	out <- llm.FinalChatChunk(response)
	close(out)
	return out, nil
}

func TestAgentBrowserProductApprovalContinuationRealChromium(t *testing.T) {
	runAgentBrowserProductFixture(t, "")
}
func TestAgentBrowserProductClosedApprovalContinuationRealChromium(t *testing.T) {
	for _, scenario := range []string{"deny_closed", "approve_closed"} {
		t.Run(scenario, func(t *testing.T) { runAgentBrowserProductFixture(t, scenario) })
	}
}
func runAgentBrowserProductFixture(t *testing.T, scenario string) {
	if os.Getenv("AGENT_BROWSER_PRODUCT_TEST") != "1" || runtime.GOOS != "windows" {
		t.Skip("set AGENT_BROWSER_PRODUCT_TEST=1 for owned real Windows Chromium product execution")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var writes atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/publish" {
			writes.Add(1)
			fmt.Fprint(w, "published")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Agent browser product fixture</title><h1>Dynamic owned fixture</h1><span id="clock"></span><button onclick="fetch('/publish',{method:'POST'}).then(()=>document.getElementById('result').textContent='Published exactly once')">Publish fixture</button><p id="result">Draft ready</p><script>setInterval(()=>document.getElementById('clock').textContent=Date.now(),25)</script>`)
	}))
	defer fixture.Close()
	home, e := os.MkdirTemp("D:/TraverseAgentTemp", "product-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(home)
	st, e := Open(filepath.Join(home, "store.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	workspace := WorkspaceRecord{ID: "workspace-browser-product", Name: "browser-product", RootPath: home}
	if e = st.SaveWorkspace(ctx, workspace); e != nil {
		t.Fatal(e)
	}
	_, run, e := application.NewRunService(st).Create(ctx, application.CreateRunRequest{Goal: "Read the owned browser fixture, propose its exact synthetic publish action, then inspect a screenshot after approval", Profile: "review", Surface: "code", Phase: "deliver", WorkspaceID: workspace.ID, Interactive: true, ModelRoute: "agent-browser-product/fixture", Budget: domain.Budget{MaxTurns: 8, MaxTokens: 50000, MaxToolCalls: 20}})
	if e != nil {
		t.Fatal(e)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}
	if _, e = application.NewRunExecutionPermissionService(st, capabilities).Change(ctx, application.ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: "full_access", OperationKey: "browser-product-full-access", RequestedBy: "test_operator", Reason: "test-owned real browser integration", ConfirmDangerFullAccess: true}); e != nil {
		t.Fatal(e)
	}
	browser := application.NewAgentBrowserService(st, application.AgentBrowserOptions{HomePath: home, Capabilities: capabilities, Headless: true, SessionLifetime: time.Minute})
	defer browser.Shutdown(context.Background())
	provider := &agentBrowserProductProvider{url: fixture.URL, scenario: scenario}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "fixture"})
	router.RegisterProvider(provider)
	checker := policy.NewDefaultChecker()
	supervisor := application.NewRunSupervisor(st, router, checker).WithExecutionPermissionCapabilities(capabilities).WithAgentBrowser(browser)
	if _, e = application.NewRunService(st).Start(ctx, run.ID); e != nil {
		t.Fatal(e)
	}
	result, e := supervisor.Step(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if result.RunStatus != domain.RunWaitingApproval || len(provider.requests) != 3 || writes.Load() != 0 {
		t.Fatalf("approval preflight dispatch/result=%+v requests=%d writes=%d", result, len(provider.requests), writes.Load())
	}
	records, e := st.ListApprovals(ctx, approval.ListFilter{RunID: run.ID, ToolName: toolgateway.AgentBrowserApprovalTool})
	if e != nil || len(records) != 1 {
		t.Fatalf("approvals %v %+v", e, records)
	}
	call, started, e := st.GetAgentBrowserCall(ctx, run.ID, records[0].ProposalID)
	if e != nil || started || call.Status != domain.SupervisorToolPending {
		t.Fatalf("approval already started %v %t %+v", e, started, call)
	}
	control := application.NewApprovalControlService(st, toolgateway.New(st, checker), checker)
	if _, e = control.Decide(ctx, application.DecideApprovalControlRequest{Version: application.ApprovalControlProtocolVersion, RunID: run.ID, ApprovalID: records[0].ID, Action: application.ApprovalControlApproveForThread, OperationKey: "browser-product-thread-denied", ReviewedBy: "test_operator"}); e == nil {
		t.Fatal("sensitive browser accepted site/thread grant")
	}
	decision := application.ApprovalControlApproveOnce
	reason := ""
	if scenario != "" {
		view, _ := browser.GetStatus(ctx, run.ID)
		if _, e := browser.Close(ctx, run.ID, view.SessionID); e != nil {
			t.Fatal(e)
		}
	}
	if scenario == "deny_closed" {
		decision = application.ApprovalControlDeny
		reason = "operator declined fixture"
	}
	if _, e = control.Decide(ctx, application.DecideApprovalControlRequest{Version: application.ApprovalControlProtocolVersion, RunID: run.ID, ApprovalID: records[0].ID, Action: decision, Reason: reason, OperationKey: "browser-product-approve", ReviewedBy: "test_operator"}); e != nil {
		t.Fatal(e)
	}
	handoff := application.NewRunExecutionHandoffService(st, router, checker).WithExecutionPermissionCapabilities(capabilities).WithAgentBrowser(browser)
	threads := application.NewThreadTurnServiceWithExecutionCapabilities(st, application.NewRunLifecycleControlService(st), handoff, capabilities)
	resumed := threads.ResumeApproval(ctx, application.ApprovalContinuationRequest{RunID: run.ID, Kind: "agent_browser", ProposalID: call.CallID})
	wantWrites := int32(1)
	wantStarts := 1
	if scenario != "" {
		wantWrites = 0
		wantStarts = 0
	}
	if resumed.State != "completed" || len(provider.requests) != 4 || (scenario == "" && !provider.pixels) || (scenario != "" && !provider.terminal) || writes.Load() != wantWrites {
		t.Fatalf("real pending continuation=%+v requests=%d pixels=%t writes=%d", resumed, len(provider.requests), provider.pixels, writes.Load())
	}
	again := threads.ResumeApproval(ctx, application.ApprovalContinuationRequest{RunID: run.ID, Kind: "agent_browser", ProposalID: call.CallID})
	if again.State != "completed" || !again.Replayed || writes.Load() != wantWrites || len(provider.requests) != 4 {
		t.Fatalf("repeat approval redispatched %+v writes=%d requests=%d", again, writes.Load(), len(provider.requests))
	}
	var starts int
	if e = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events WHERE run_id=? AND subject_id=? AND type=?`, run.ID, call.CallID, events.SupervisorToolExecutionStartedEvent).Scan(&starts); e != nil || starts != wantStarts {
		t.Fatalf("dispatch marker count=%d e=%v", starts, e)
	}

	if scenario != "" {
		done, started, e := st.GetAgentBrowserCall(ctx, run.ID, call.CallID)
		if e != nil || started || done.Status == domain.SupervisorToolPending {
			t.Fatalf("unsettled %v %+v", e, done)
		}
		t.Logf("real non-dispatch scenario=%s status=%s model_rounds=%d starts=%d writes=%d paired=%t", scenario, done.Status, len(provider.requests), starts, writes.Load(), provider.terminal)
		return
	}
	view, e := browser.GetStatus(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	png, hash, e := browser.ReadScreenshot(ctx, run.ID, view.SessionID, view.ArtifactLocator)
	if e != nil || len(png) == 0 || hash != view.ScreenshotSHA256 {
		t.Fatalf("real screenshot read %v", e)
	}
	if output := os.Getenv("AGENT_BROWSER_PRODUCT_EVIDENCE"); output != "" {
		if e = os.WriteFile(filepath.Join(output, "agent-browser-product.png"), png, 0600); e != nil {
			t.Fatal(e)
		}
		b, _ := json.MarshalIndent(map[string]any{"run_id": run.ID, "view": view, "provider_requests": len(provider.requests), "writes": writes.Load(), "started_events": starts, "pixels": provider.pixels, "approval_continuation": resumed}, "", "  ")
		if e = os.WriteFile(filepath.Join(output, "agent-browser-product.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	closed, e := browser.Close(ctx, run.ID, view.SessionID)
	if e != nil || closed.Cleanup == nil || !closed.Cleanup.TreeReaped || !closed.Cleanup.ProfileRemoved || closed.Cleanup.CleanupPending {
		t.Fatalf("real cleanup %+v %v", closed, e)
	}
	t.Logf("real product session=%s URL=%s PNG=%d SHA256=%s model rounds=%d exact writes=%d tree reaped=%t profile removed=%t", view.SessionID, view.CanonicalURL, len(png), hash, len(provider.requests), writes.Load(), closed.Cleanup.TreeReaped, closed.Cleanup.ProfileRemoved)
}
