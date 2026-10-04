package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/tools"
)

type commandApprovalProductProvider struct {
	llm.MockProvider
	payload        json.RawMessage
	requests       int
	observedResult bool
}

func (p *commandApprovalProductProvider) Name() string            { return "command-approval-product" }
func (*commandApprovalProductProvider) SupportsTools(string) bool { return true }
func (p *commandApprovalProductProvider) Chat(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests++
	response := &llm.ChatResponse{Provider: p.Name(), Model: "fixture", Usage: llm.Usage{InputTokens: 4, OutputTokens: 4, TotalTokens: 8}}
	if p.requests == 1 {
		found := false
		for _, tool := range request.Tools {
			if tool.Name == string(toolgateway.CommandRuntimeTool) {
				found = true
			}
		}
		if !found {
			return nil, errors.New("command tool missing from Code/Deliver model request")
		}
		response.ToolCalls = []llm.ToolCall{{ID: "http-command-call", Name: string(toolgateway.CommandRuntimeTool), Arguments: p.payload}}
	} else if p.requests == 2 {
		for _, message := range request.Messages {
			for _, result := range message.ToolResults {
				if strings.Contains(result.Content, "exact command fixture response") {
					p.observedResult = true
				}
			}
		}
		if !p.observedResult {
			return nil, errors.New("same-turn continuation omitted approved command result")
		}
		raw, _ := json.Marshal(domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionContinue, Message: "Observed the approved local command result."})
		response.Text = string(raw)
	} else {
		return nil, errors.New("unexpected repeated model continuation")
	}
	return response, nil
}
func (p *commandApprovalProductProvider) StreamChat(ctx context.Context, request llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	response, err := p.Chat(ctx, request)
	if err != nil {
		return nil, err
	}
	out := make(chan llm.ChatChunk, 2)
	if response.Text != "" {
		out <- llm.ChatChunk{Text: response.Text}
	}
	out <- llm.FinalChatChunk(response)
	close(out)
	return out, nil
}

type commandApprovalHTTPChecker struct{ policy.Checker }

func (c commandApprovalHTTPChecker) CheckToolCall(call tools.Call) policy.Decision {
	if call.Name == string(toolgateway.CommandRuntimeTool) {
		return policy.Decision{Allowed: true, NeedsApproval: true, Risk: "high", Reason: "HTTP host requires exact review even in Full"}
	}
	return c.Checker.CheckToolCall(call)
}

func TestCommandApprovalHTTPProductSameTurnAndReplay(t *testing.T) {
	testCommandApprovalHTTPProduct(t, false, false)
}

func TestCommandBoundedApprovalHTTPProductSameTurnAndReplay(t *testing.T) {
	testCommandApprovalHTTPProduct(t, true, false)
}

func TestCommandBoundedApprovalHTTPPreviewFindsOlderActiveScope(t *testing.T) {
	testCommandApprovalHTTPProduct(t, true, true)
}

func testCommandApprovalHTTPProduct(t *testing.T, bounded, olderActiveScope bool) {
	for _, mode := range []string{"ask", "auto", "full"} {
		if olderActiveScope && mode != "ask" {
			continue
		}
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			st, err := store.Open(filepath.Join(t.TempDir(), "command-http.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			workspace := store.WorkspaceRecord{ID: "command-http-workspace", Name: "Owned fixture", RootPath: t.TempDir()}
			if err = st.SaveWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			provider := &commandApprovalProductProvider{}
			_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{Goal: "Run the exact reviewed local command fixture", Profile: "code", Surface: "code", Phase: "deliver", WorkspaceID: workspace.ID, Interactive: true, ModelRoute: provider.Name() + "/fixture", Budget: domain.Budget{MaxTurns: 8, MaxTokens: 50000, MaxToolCalls: 20}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = application.NewRunExecutionProfileService(st).Change(ctx, application.ChangeRunExecutionProfileRequest{
				RunID: run.ID, Profile: "local", OperationKey: "http-command-profile", RequestedBy: "operator"}); err != nil {
				t.Fatal(err)
			}
			caps := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}
			if mode != "ask" {
				if _, err = application.NewRunExecutionPermissionService(st, caps).Change(ctx, application.ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: mode, ConfirmFull: mode == "full", OperationKey: "http-command-mode-0001", RequestedBy: "operator"}); err != nil {
					t.Fatal(err)
				}
			}
			executable, err := exec.LookPath("node")
			if err != nil {
				t.Fatal("native Node fixture required", err)
			}
			manager, err := runner.NewPlatformCommandRuntimeManager(st, idgen.New("http-command-owner"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				defer cancel()
				if err := manager.Shutdown(closeCtx); err != nil {
					t.Error(err)
				}
			}()
			service, err := application.NewCommandRuntimeService(st, manager, caps)
			if err != nil {
				t.Fatal(err)
			}
			maxBytes := 4096
			provider.payload, _ = json.Marshal(toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion,
				Action: toolgateway.CommandRuntimeActionRun, FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
				Commands: []runner.CommandRuntimeSpec{{Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimeProcess,
					Executable: executable, Arguments: []string{"-e", "require('fs').appendFileSync('count.txt','1');process.stdout.write('exact command fixture response')"},
					WorkingDirectory: ".", Environment: []runner.CommandRuntimeEnvironment{}, StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
					TimeoutMilliseconds: 10000, Output: runner.CommandRuntimeOutputPolicy{InlineBytes: 4096, ArtifactBytes: 4096},
					Network: runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone, Purpose: "exact review intent"}}})
			if bounded {
				var input toolgateway.CommandRuntimeInput
				if err := json.Unmarshal(provider.payload, &input); err != nil {
					t.Fatal(err)
				}
				input.ReviewScope = &toolgateway.CommandReviewScope{RiskKinds: []string{"other_high_risk"}, OtherRiskReason: "HTTP local acceptance markers"}
				provider.payload, _ = json.Marshal(input)
			}
			router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "fixture"})
			router.RegisterProvider(provider)
			checker := commandApprovalHTTPChecker{Checker: policy.NewDefaultChecker()}
			dependencies := application.RunRuntimeDependencies{ExecutionCapabilities: caps, CommandRuntime: service}
			supervisor := application.NewRunSupervisorWithRuntime(st, router, checker, dependencies)
			execution := application.NewRunExecutionHandoffWithRuntime(st, router, checker, dependencies)
			lifecycle := application.NewRunLifecycleControlService(st)
			threads := application.NewThreadTurnServiceWithExecutionCapabilities(st, lifecycle, execution, caps)
			controller := application.NewApprovalControlService(st, toolgateway.New(st, checker), checker)
			api, err := New(st, Config{AccessToken: testAccessToken, ControlToken: testControlToken, RunCreationEnabled: true, SessionMessageEnabled: true, RunLifecycleEnabled: true, RunLifecycleController: lifecycle, RunExecutionEnabled: true, RunExecutionController: execution, ThreadTurnController: threads, ApprovalControlEnabled: true, ApprovalController: controller})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = application.NewRunService(st).Start(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			result, err := supervisor.Step(ctx, run.ID)
			if err != nil || result.RunStatus != domain.RunWaitingApproval || provider.requests != 1 {
				t.Fatalf("preflight %+v err=%v model=%d", result, err, provider.requests)
			}
			if _, err := os.Stat(filepath.Join(workspace.RootPath, "count.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("process ran before review", err)
			}
			if jobs, err := st.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: run.ID, Limit: 10}); err != nil || len(jobs) != 0 {
				t.Fatal("preflight created process jobs", jobs, err)
			}
			approvals, err := st.ListApprovals(ctx, approval.ListFilter{RunID: run.ID, ToolName: string(toolgateway.CommandRuntimeTool)})
			if err != nil || len(approvals) != 1 {
				t.Fatalf("approvals %+v %v", approvals, err)
			}
			ttl, uses := 120, 2
			var seeded approval.SessionGrant
			if olderActiveScope {
				ttl, uses = 211, 3
				seeded = seedOlderActiveCommandGrant(t, st, approvals[0])
			}
			base := "/api/v1/runs/" + run.ID + "/approvals/" + approvals[0].ID
			queueResponse := performRequest(t, api, http.MethodGet, "/api/v1/runs/"+run.ID+"/approvals", testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", nil)
			var queue ApprovalQueueView
			decodeDataStatus(t, queueResponse, http.StatusOK, &queue)
			wantActions := 2
			if bounded {
				wantActions = 3
			}
			if len(queue.Items) != 1 || len(queue.Items[0].AllowedActions) != wantActions {
				t.Fatalf("command pending queue omitted one-call review: %+v", queue)
			}
			previewResponse := performRequest(t, api, http.MethodGet, base+"/preview", testAccessToken, "127.0.0.1:8765", "127.0.0.1:45000", nil)
			var preview ApprovalPreviewView
			decodeDataStatus(t, previewResponse, http.StatusOK, &preview)
			if !preview.SourceCurrent || preview.Effect != "command_process" || !strings.Contains(previewResponse.Body.String(), "exact review intent") {
				t.Fatalf("preview %+v", preview)
			}
			if olderActiveScope {
				fields := map[string]string{}
				for _, field := range preview.Fields {
					fields[field.Name] = field.Value
				}
				if fields["grant_ttl_seconds"] != "211" || fields["grant_max_uses"] != "3" || fields["grant_uses_remaining"] != "3" || fields["grant_expires_at"] != seeded.ExpiresAt.Format(time.RFC3339) {
					t.Fatalf("preview omitted original limits beyond the 500-row list: %+v", fields)
				}
			}
			var decision ApprovalDecisionControlView
			for replay := 0; replay < 2; replay++ {
				body := `{"version":"approval_control.v1","action":"approve_once"}`
				if bounded {
					body = fmt.Sprintf(`{"version":"approval_control.v1","action":"approve_for_run","grant_ttl_seconds":%d,"grant_max_uses":%d}`, ttl, uses)
				}
				response := performControlPathRequest(t, api, base+"/decision", "http-command-approve-once", strings.NewReader(body))
				decodeDataStatus(t, response, http.StatusAccepted, &decision)
				if bounded && (decision.BoundedGrant == nil || decision.BoundedGrant.UsesRemaining != uses-1 || decision.BoundedGrant.UseOrdinal != 1 || !decision.BoundedGrant.EachCommandRequiresReview || decision.SessionGrantCreated != (replay == 0 && !olderActiveScope)) {
					t.Fatalf("invalid bounded projection: %+v", decision)
				}
				if !bounded && (decision.BoundedGrant != nil || decision.SessionGrantCreated) {
					t.Fatal("one-call review gained a grant")
				}
				if decision.Continuation == nil || decision.Continuation.State != "completed" || provider.requests != 2 || !provider.observedResult {
					t.Fatalf("HTTP same-turn continuation %+v models=%d", decision, provider.requests)
				}
				value, err := os.ReadFile(filepath.Join(workspace.RootPath, "count.txt"))
				if err != nil || string(value) != "1" {
					t.Fatalf("review/replay dispatched incorrect process count: %q %v", value, err)
				}
				if jobs, err := st.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: run.ID, Limit: 10}); err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobCompleted {
					t.Fatal("missing original completed process receipt", jobs, err)
				}
				if replay == 1 && (!decision.Replayed || !decision.Continuation.Replayed) {
					t.Fatal("review replay not idempotent")
				}
				if olderActiveScope {
					grant, err := st.GetSessionGrant(ctx, seeded.ID)
					if err != nil || grant.UsesRemaining != 2 || !grant.ExpiresAt.Equal(*seeded.ExpiresAt) || !grant.CreatedAt.Equal(seeded.CreatedAt) {
						t.Fatalf("HTTP decision renewed an older active scope: %+v %v", grant, err)
					}
				}
			}
			if output := os.Getenv("UC_COMMAND_HTTP_EVIDENCE"); output != "" {
				raw, _ := json.MarshalIndent(map[string]any{"queue": queue, "preview": preview, "decision": decision, "process_dispatches": 1, "model_calls": provider.requests}, "", "  ")
				if err = os.WriteFile(filepath.Join(output, "command-approval-http-"+mode+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func seedOlderActiveCommandGrant(t *testing.T, st *store.SQLiteStore, record approval.Record) approval.SessionGrant {
	t.Helper()
	query, err := st.GetCommandApprovalGrantScope(t.Context(), record.ProposalID)
	if err != nil {
		t.Fatal(err)
	}
	request := approval.CreateGrantRequest{SessionID: query.SessionID, WorkspaceID: query.WorkspaceID, ToolName: query.ToolName, ActionClass: query.ActionClass,
		Reason: "retain the original operator limits", GrantedBy: "operator", ScopeFingerprint: query.ScopeFingerprint,
		MaxUses: 3, TTL: 211 * time.Second, ModeSnapshotID: query.ModeSnapshotID, ModeRevision: query.ModeRevision,
		InteractionSnapshotID: query.InteractionSnapshotID, InteractionRevision: query.InteractionRevision,
		ExecutionProfileSnapshotID: query.ExecutionProfileSnapshotID, ExecutionProfileRevision: query.ExecutionProfileRevision,
		PermissionSnapshotID: query.PermissionSnapshotID, PermissionRevision: query.PermissionRevision, PermissionMode: query.PermissionMode,
		WorkspaceRootFingerprint: query.WorkspaceRootFingerprint, CapabilityGeneration: query.CapabilityGeneration}
	var original approval.SessionGrant
	for i := 0; i <= 500; i++ {
		request.Generation = int64(i + 1)
		request.IdempotencyKey = fmt.Sprintf("http-history-scope-%d", i)
		if i > 0 {
			request.ScopeFingerprint = approval.Fingerprint("http-unrelated-scope", fmt.Sprint(i))
		}
		result, err := st.CreateSessionGrant(t.Context(), request)
		if err != nil {
			t.Fatal(i, err)
		}
		if i == 0 {
			original = result.Grant
		}
	}
	listed, err := st.ListSessionGrants(t.Context(), approval.GrantListFilter{RunID: record.RunID, ToolName: record.ToolName, Limit: 500})
	if err != nil || len(listed) != 500 {
		t.Fatal(len(listed), err)
	}
	for _, grant := range listed {
		if grant.ID == original.ID {
			t.Fatal("fixture did not place the active scope beyond the list limit")
		}
	}
	return original
}
