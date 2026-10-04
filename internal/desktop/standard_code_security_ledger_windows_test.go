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
	for _, scenario := range []string{"completed", "denied", "cancelled_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
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
			call, err := run.prepareCommand(ctx, plane, caps, toolgateway.CommandRuntimeInput{Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionList})
			if err != nil {
				t.Fatal(err)
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
			if (invokeErr == nil) != (scenario == "completed") {
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
			if scenario == "completed" {
				if err := run.completeTurn(ctx, plane); err != nil {
					t.Fatal(err)
				}
				if run.turn.Checkpoint.Phase != domain.SupervisorIdle {
					t.Fatalf("successful turn remains active: %+v", run.turn.Checkpoint)
				}
			}
		})
	}
}
