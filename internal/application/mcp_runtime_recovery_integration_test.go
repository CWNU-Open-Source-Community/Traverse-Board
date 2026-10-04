package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolcontract"
)

type mcpReceiptFaultStore struct {
	*store.SQLiteStore
	fail bool
}

func (s *mcpReceiptFaultStore) RecordSupervisorToolResult(ctx context.Context, checkpoint domain.SupervisorCheckpoint, result domain.SupervisorToolResult) (domain.SupervisorToolCall, bool, error) {
	if s.fail {
		s.fail = false
		return domain.SupervisorToolCall{}, false, apperror.New(apperror.CodeInternal, "injected MCP receipt persistence failure")
	}
	return s.SQLiteStore.RecordSupervisorToolResult(ctx, checkpoint, result)
}

type mcpReceiptFixtureClient struct {
	epochMCPClient
	scenario string
}

func (c *mcpReceiptFixtureClient) Invoke(_ context.Context, request mcp.InvokeRequest) (mcp.ClientCallResult, error) {
	if request.Authorizer == nil || request.Subject.ActorID == "" || request.OperationID == "" {
		return mcp.ClientCallResult{}, errors.New("production gateway omitted common host authority")
	}
	c.calls++
	if c.scenario == "outcome_unknown" {
		return mcp.ClientCallResult{}, mcp.NewInvocationError(toolcontract.Receipt{OperationID: request.OperationID, State: toolcontract.ReceiptOutcomeUnknown}, errors.New("fixture response lost after dispatch"))
	}
	return mcp.ClientCallResult{Content: `{"content":[{"type":"text","text":"fixture result"}]}`, IsError: c.scenario == "remote_error"}, nil
}

// The existing SQLite ledger, model checkpoint, Supervisor and gateway are real.
// The remote client is a counted fixture; actual guarded TLS sends are covered
// separately by TestMCPRuntimeCommonAuthorityAtRealTLSSends.
func TestMCPRuntimeSupervisorRecoveryNeverRepeatsUncertainDispatch(t *testing.T) {
	for _, scenario := range []string{"lost_receipt", "outcome_unknown", "remote_error"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			f := newMCPOperationApprovalFixture(t, domain.RunExecutionPermissionFull, false, false)
			state, turn, capabilities := f.st, f.turn, f.capabilities
			client := &mcpReceiptFixtureClient{scenario: scenario}
			fault := &mcpReceiptFaultStore{SQLiteStore: state, fail: scenario == "lost_receipt"}
			supervisor := NewRunSupervisor(fault, nil, policy.NewDefaultChecker()).WithExecutionPermissionCapabilities(capabilities).WithMCPClient(client)
			rounds, err := state.ListSupervisorToolRounds(ctx, turn.Checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			_, waiting, err := supervisor.resumeSupervisorTools(ctx, turn, rounds)
			if waiting || client.calls != 1 || (scenario == "lost_receipt" && apperror.CodeOf(err) != apperror.CodeInternal) || (scenario != "lost_receipt" && err != nil) {
				t.Fatalf("first dispatch calls=%d waiting=%t err=%v", client.calls, waiting, err)
			}
			// Construct a new Supervisor and reload existing rows, rather than
			// reusing an in-memory result or creating another recovery ledger.
			supervisor = NewRunSupervisor(state, nil, policy.NewDefaultChecker()).WithExecutionPermissionCapabilities(capabilities).WithMCPClient(client)
			for resume := 0; resume < 2; resume++ {
				rounds, err = state.ListSupervisorToolRounds(ctx, turn.Checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				rounds, waiting, err = supervisor.resumeSupervisorTools(ctx, turn, rounds)
				if err != nil || waiting || client.calls != 1 || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
					t.Fatalf("recovery repeated uncertain action: calls=%d waiting=%t err=%v", client.calls, waiting, err)
				}
				call := rounds[0].Calls[0]
				var result supervisorToolResultEnvelope
				if err := json.Unmarshal([]byte(call.ResultJSON), &result); err != nil {
					t.Fatal(err)
				}
				wantCode, wantReceipt := "outcome_unknown", "outcome_unknown"
				if scenario == "remote_error" {
					wantCode, wantReceipt = "remote_tool_error", "result_received"
				}
				if call.Status != domain.SupervisorToolFailed || call.ErrorCode != wantCode || result.Metadata["execution_receipt"] != wantReceipt {
					t.Fatalf("recovery lost receipt/error distinction: %+v", call)
				}
				if scenario != "remote_error" && (result.Metadata["automatic_retry"] != "forbidden" || result.Stdout != "") {
					t.Fatalf("uncertain action exposed success or automatic retry: %+v", result)
				}
			}
		})
	}
}
