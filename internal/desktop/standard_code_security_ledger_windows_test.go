//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

// The fixed list operation needs no executable pins or native process. Keep
// the real Store/Gateway while controlling only the adapter's dispatch result.
type securityLedgerRuntime struct {
	application.CommandRuntimeRuntime
	adapter     commandruntimeadapter.Identity
	dispatchErr error
	calls       int
	scope       toolgateway.CommandRuntimeContext
}

func (r *securityLedgerRuntime) AdvertisedCommandRuntimeAdapter(context.Context, string, domain.RunExecutionPermissionMode) (commandruntimeadapter.Identity, bool, error) {
	return r.adapter, true, nil
}
func (r *securityLedgerRuntime) BindCommandRuntimeAuthority(_ context.Context, raw, payload json.RawMessage) (json.RawMessage, error) {
	input, _, err := toolgateway.NormalizeCommandRuntimePayload(payload)
	if err != nil || input.Action != toolgateway.CommandRuntimeActionList {
		return nil, errors.New("ledger fixture accepts list only")
	}
	return raw, nil
}
func (r *securityLedgerRuntime) ExecuteCommandRuntime(_ context.Context, scope toolgateway.CommandRuntimeContext, input toolgateway.CommandRuntimeInput) (toolgateway.CommandRuntimeExecutionResult, error) {
	r.calls++
	r.scope = scope
	return toolgateway.CommandRuntimeExecutionResult{Backend: r.adapter.Backend, Adapter: r.adapter, Action: input.Action}, r.dispatchErr
}

func TestStandardCodeSecurityLedgerPreservesSettledAndUnknownDispatch(t *testing.T) {
	for _, scenario := range []string{"completed", "completed_existing_input", "denied", "cancelled_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			completed := scenario == "completed" || scenario == "completed_existing_input"
			state, err := store.Open(filepath.Join(t.TempDir(), "security-ledger.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = state.Close() })
			workspace := store.WorkspaceRecord{ID: "workspace-packaged-ledger", Name: "packaged ledger", RootPath: t.TempDir()}
			if err := state.SaveWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			runs := application.NewRunService(state)
			_, record, err := runs.Create(ctx, application.CreateRunRequest{Goal: "fixed packaged ledger observation", Profile: "code", Surface: "code", Phase: "deliver", WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 4, MaxTokens: 10000, MaxToolCalls: 12}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := application.NewRunExecutionProfileService(state).Change(ctx, application.ChangeRunExecutionProfileRequest{RunID: record.ID, Profile: "local", OperationKey: "packaged-ledger-profile", RequestedBy: "test_operator"}); err != nil {
				t.Fatal(err)
			}
			record, err = runs.Start(ctx, record.ID)
			if err != nil {
				t.Fatal(err)
			}
			acquired, err := state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: record.ID, OwnerID: "packaged-ledger-test", TTL: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			caps := domain.ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}
			runtime := &securityLedgerRuntime{adapter: commandruntimeadapter.SandboxedWorkspace(application.CommandRuntimeLocalSandboxBackend, "windows-local-sandbox.v1", strings.Repeat("a", 64))}
			if scenario == "denied" {
				runtime.dispatchErr = apperror.New(apperror.CodePolicyDenied, "fixture native dispatch rejected")
			}
			if scenario == "cancelled_unknown" {
				runtime.dispatchErr = context.Canceled
			}
			plane := &ControlPlane{stateStore: state, commandRuntime: runtime}
			run := &standardCodeSecurityRun{run: record, lease: acquired.Lease, adapter: runtime.adapter}
			wantInput := "fixed packaged ledger observation"
			if scenario == "completed_existing_input" {
				wantInput = "preserve the current operator probe input"
				if _, err := state.BeginSupervisorTurn(ctx, run.lease, wantInput); err != nil {
					t.Fatal(err)
				}
			}
			call, err := run.prepareCommand(ctx, plane, caps, toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionList})
			if err != nil {
				t.Fatal(err)
			}
			bound := run.turn.Checkpoint
			persisted, found, err := state.GetSupervisorCheckpoint(ctx, record.ID)
			if err != nil || !found || bound.PendingInput != wantInput || persisted.PendingInput != wantInput || persisted.AttemptID != bound.AttemptID {
				t.Fatalf("current probe input was not bound: memory=%+v stored=%+v found=%t err=%v", bound, persisted, found, err)
			}
			source, started, err := state.GetSupervisorApprovalCall(ctx, record.ID, call.SupervisorToolCallID)
			if err != nil || started || source.Status != domain.SupervisorToolPending {
				t.Fatalf("preparation executed or lost source: %+v %v", source, err)
			}
			authority, err := commandruntimeadapter.DecodeAuthority(json.RawMessage(source.AuthorityJSON))
			if err != nil {
				t.Fatal(err)
			}
			if authority.PermissionMode != domain.RunExecutionPermissionAsk || authority.PermissionSnapshotID != call.PermissionSnapshotID || authority.PermissionRuntimeEpoch != caps.RuntimeAuthority.RuntimeEpoch() || authority.RunAuthorizationFence != call.RunAuthorizationFence || call.AgentAttemptID != source.AgentAttemptID {
				t.Fatal("dispatch authority did not come from durable call")
			}
			gateway := toolgateway.New(state, policy.NewDefaultChecker()).WithCommandRuntimeExecutor(runtime)
			_, invokeErr := run.invokeCommand(ctx, plane, gateway, call)
			if (invokeErr == nil) != completed {
				t.Fatalf("dispatch result: %v", invokeErr)
			}
			source, started, err = state.GetSupervisorApprovalCall(ctx, record.ID, call.SupervisorToolCallID)
			if err != nil || !started || runtime.calls != 1 || runtime.scope.SupervisorToolCallID != source.CallID {
				t.Fatalf("dispatch lost exact started source: %+v %v calls=%d", source, err, runtime.calls)
			}
			if scenario == "cancelled_unknown" {
				if source.Status != domain.SupervisorToolPending || source.ResultJSON != "" {
					t.Fatalf("unknown dispatch was falsely settled: %+v", source)
				}
				if err := run.completeTurn(ctx, plane); err == nil {
					t.Fatal("unknown dispatch completed its turn")
				}
				persisted, found, err := state.GetSupervisorCheckpoint(ctx, record.ID)
				if err != nil || !found || persisted.Phase != domain.SupervisorTurnStarted || persisted.AttemptID != bound.AttemptID || persisted.PendingInput != wantInput {
					t.Fatalf("unknown dispatch lost its original turn input: %+v found=%t err=%v", persisted, found, err)
				}
			} else {
				want := domain.SupervisorToolCompleted
				if scenario == "denied" {
					want = domain.SupervisorToolFailed
				}
				if source.Status != want || source.ResultJSON == "" {
					t.Fatalf("settled receipt missing: %+v", source)
				}
				var envelope struct {
					Metadata map[string]string `json:"metadata"`
				}
				if err := json.Unmarshal([]byte(source.ResultJSON), &envelope); err != nil {
					t.Fatal(err)
				}
				if _, present := envelope.Metadata["replayed"]; present {
					t.Fatal("transient replay flag persisted as evidence")
				}
			}
			if _, err := run.invokeCommand(ctx, plane, gateway, call); err == nil || runtime.calls != 1 {
				t.Fatal("repeated dispatch reached adapter")
			}
			if completed {
				if err := run.completeTurn(ctx, plane); err != nil {
					t.Fatal(err)
				}
				checkpoint := run.turn.Checkpoint
				if checkpoint.Phase != domain.SupervisorIdle || checkpoint.NextTurn != bound.NextTurn+1 || checkpoint.AttemptID != "" || checkpoint.HasPendingInput() {
					t.Fatalf("successful turn remains active: %+v", checkpoint)
				}
				history, err := state.ListSessionMessages(ctx, record.SessionID, true)
				if err != nil {
					t.Fatal(err)
				}
				userMessages := 0
				for _, message := range history {
					if message.Role == "user" {
						userMessages++
						if message.Content != wantInput {
							t.Fatalf("completed turn saved another input: %q", message.Content)
						}
					}
				}
				if userMessages != 1 {
					t.Fatalf("saved user input count=%d want=1", userMessages)
				}
				if err := run.completeTurn(ctx, plane); err != nil || run.turn.Checkpoint != checkpoint || runtime.calls != 1 {
					t.Fatalf("repeated completion changed the settled turn: %+v err=%v calls=%d", run.turn.Checkpoint, err, runtime.calls)
				}
				replayedHistory, err := state.ListSessionMessages(ctx, record.SessionID, true)
				if err != nil || len(replayedHistory) != len(history) {
					t.Fatalf("repeated completion appended messages: before=%d after=%d err=%v", len(history), len(replayedHistory), err)
				}
			}
		})
	}
}
