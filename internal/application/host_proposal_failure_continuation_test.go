package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
)

func rejectedHostFixture(t *testing.T) (*store.SQLiteStore, domain.Run, application.ExecuteThreadTurnRequest, *llm.ChatResponse) {
	t.Helper()
	st, run, root, input := toolBoundaryFixture(t, domain.Budget{MaxTurns: 8, MaxToolCalls: 20})
	if _, err := application.NewRunExecutionProfileService(st).Change(t.Context(), application.ChangeRunExecutionProfileRequest{
		RunID: run.ID, Profile: "local", OperationKey: "rejected-host-local", RequestedBy: "test_operator", Reason: "local proposal fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunExecutionInteractionService(st).Change(t.Context(), application.ChangeRunExecutionInteractionRequest{
		RunID: run.ID, Mode: "controlled", Trust: "trusted", ConfirmWorkspaceTrust: true,
		OperationKey: "rejected-host-controlled", RequestedBy: "test_operator", Reason: "separately reviewed local commands",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewThreadExecutionPermissionService(st, domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true}).Change(t.Context(), application.ChangeThreadExecutionPermissionRequest{
		ThreadID: input.ThreadID, Mode: "approval", ConfirmUserApproval: true, OperationKey: "rejected-host-approval", RequestedBy: "test_operator", Reason: "review each command",
	}); err != nil {
		t.Fatal(err)
	}
	input.Content = "Propose a local command; preserve policy errors and do not run a denied proposal"
	input.OperationKey = "rejected-host-original"
	payload, err := json.Marshal(map[string]any{"version": "host_command_proposal.v1", "transport": "process",
		"executable_path": filepath.Join(t.TempDir(), "untrusted-tool.exe"), "argv": []string{"inspect"},
		"working_directory": root, "timeout_milliseconds": 1000, "purpose": "Inspect the local project under review"})
	if err != nil {
		t.Fatal(err)
	}
	return st, run, input, toolResponse("rejected-host", "host_command_propose", string(payload))
}

func rejectedHostService(st application.RunExecutionHandoffStore, threadStore application.ThreadStore, provider llm.Provider) *application.ThreadTurnService {
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	return application.NewThreadTurnServiceWithExecutionCapabilities(threadStore, application.NewRunLifecycleControlService(st.(application.RunLifecycleControlStore)),
		application.NewRunExecutionHandoffService(st, router, policy.NewDefaultChecker()), domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
}

// Reproduce the old supervisor failure without editing SQLite: the exact
// rejected result is returned as a fatal error before its ledger write, and
// old M has not sealed it yet. A fresh service then exercises ordinary upgrade.
type historicalRejectedHostStore struct {
	*store.SQLiteStore
	code                apperror.Code
	failCreatedProposal bool
}

func (s *historicalRejectedHostStore) CreateHostCommandProposal(ctx context.Context, operation runner.HostCommandProposalOperation, proposal runner.HostCommandProposal) (runner.HostCommandProposal, bool, error) {
	stored, replayed, err := s.SQLiteStore.CreateHostCommandProposal(ctx, operation, proposal)
	if err == nil && s.failCreatedProposal {
		return stored, replayed, apperror.New(apperror.CodePolicyDenied, "fixture: failure after proposal was recorded")
	}
	return stored, replayed, err
}

func (s *historicalRejectedHostStore) RecordSupervisorToolResult(ctx context.Context, cp domain.SupervisorCheckpoint, result domain.SupervisorToolResult) (domain.SupervisorToolCall, bool, error) {
	if result.ErrorCode == string(apperror.CodePolicyDenied) && strings.Contains(result.ResultJSON, "host_command_propose") {
		return domain.SupervisorToolCall{}, false, apperror.New(s.code, "fixture: historical proposal rejection omitted its result record")
	}
	return s.SQLiteStore.RecordSupervisorToolResult(ctx, cp, result)
}
func (s *historicalRejectedHostStore) EndFailedThreadTurn(context.Context, string, string, string) (domain.ThreadTurnFailure, bool, error) {
	return domain.ThreadTurnFailure{}, false, nil
}

func TestHostProposalPolicyRejectionIsARecordedToolResult(t *testing.T) {
	st, run, input, rejected := rejectedHostFixture(t)
	provider := &boundaryJourneyProvider{}
	provider.respond = func(ctx context.Context, request llm.ChatRequest, index int) (*llm.ChatResponse, error) {
		switch index {
		case 1:
			return rejected, nil
		case 2:
			if !hasToolResult(request, "POLICY_DENIED") || !hasToolResult(request, "trusted program roots") {
				t.Fatalf("model did not receive actual proposal policy rejection: metadata=%v second_message=%s", request.Metadata, request.Messages[1].Content)
			}
			return boundaryRead("safe-read-after-denial", 1), nil
		default:
			return textResponse(rootActionResponse(domain.RootActionFinish, "Read the file; the command proposal was denied and was not executed", "done", "")), nil
		}
	}
	result, err := rejectedHostService(st, st, provider).Execute(t.Context(), input)
	if err != nil || len(provider.Requests()) != 3 || result.Submission.Message.Status != domain.OperatorSteeringCommitted {
		t.Fatalf("model could not handle the rejected proposal: %#v %v", result, err)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 10)
	if err != nil || len(rounds) != 2 || rounds[1].Calls[0].Status != domain.SupervisorToolFailed || rounds[1].Calls[0].ErrorCode != "POLICY_DENIED" {
		t.Fatalf("rejection not settled: %#v %v", rounds, err)
	}
	proposals, err := st.ListHostCommandProposals(t.Context(), run.ID, 10)
	if err != nil || len(proposals) != 0 {
		t.Fatalf("denied command created a proposal: %#v %v", proposals, err)
	}
}

func TestHistoricalRejectedHostProposalContinuesThroughOrdinaryMessage(t *testing.T) {
	st, run, input, rejected := rejectedHostFixture(t)
	provider := &boundaryJourneyProvider{}
	provider.respond = func(ctx context.Context, request llm.ChatRequest, index int) (*llm.ChatResponse, error) {
		if index == 1 {
			return rejected, nil
		}
		if index == 2 {
			seen := false
			for _, message := range request.Messages {
				seen = seen || strings.Contains(message.Content, "POLICY_DENIED") && strings.Contains(message.Content, "original detailed policy reason was not retained")
			}
			if !seen {
				t.Fatal("new ordinary message lost the observed failure or invented a detailed cause")
			}
			return boundaryRead("read-after-historical-denial", 1), nil
		}
		return textResponse(rootActionResponse(domain.RootActionFinish, "Read the original file without rerunning the denied command", "done", "")), nil
	}
	legacy := &historicalRejectedHostStore{SQLiteStore: st, code: apperror.CodePolicyDenied}
	first, err := rejectedHostService(legacy, legacy, provider).Execute(t.Context(), input)
	if err == nil || first.Execution == nil || first.Execution.Handoff.Result == nil || first.Execution.Handoff.Result.ErrorCode != "policy_denied" {
		t.Fatalf("legacy fixture did not preserve a failed handoff: %#v %v", first, err)
	}
	original := first.Execution.Handoff
	before, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 10)
	if err != nil || len(before) != 1 || before[0].Calls[0].Status != domain.SupervisorToolPending {
		t.Fatalf("legacy fixture is not the actual pending state: %#v %v", before, err)
	}
	next := input
	next.Content, next.OperationKey = "Read the file instead. Preserve the denied proposal result", "rejected-host-new-input"
	current := rejectedHostService(st, st, provider)
	second, err := current.Execute(t.Context(), next)
	if err != nil || second.Submission.Run.ID != run.ID || len(provider.Requests()) != 3 {
		t.Fatalf("ordinary message did not continue: %#v %v", second, err)
	}
	after, handoffFound, err := st.GetRunExecutionHandoff(t.Context(), original.Operation.KeyDigest)
	if err != nil || !handoffFound || !reflect.DeepEqual(after, original) {
		t.Fatalf("old failed handoff was rewritten: %#v %v", after, err)
	}
	sealed, found, err := st.GetThreadTurnFailure(t.Context(), run.ID, first.Submission.Message.ID)
	if err != nil || !found || sealed.ErrorCode != "POLICY_DENIED" {
		t.Fatalf("M did not retain the original failure: %#v %v", sealed, err)
	}
	_, err = current.Execute(t.Context(), input)
	var failed *application.ThreadTurnFailedError
	if !errors.As(err, &failed) || len(provider.Requests()) != 3 {
		t.Fatalf("old-key replay reran the rejected proposal or hid failure: %v", err)
	}
	proposals, err := st.ListHostCommandProposals(t.Context(), run.ID, 10)
	if err != nil || len(proposals) != 0 {
		t.Fatalf("historical settlement created a proposal: %#v %v", proposals, err)
	}
}

func TestHistoricalHostProposalSettlementKeepsUncertainAndLeasedCallsPending(t *testing.T) {
	for _, activeLease := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncertain_error", true: "another_lease"}[activeLease], func(t *testing.T) {
			st, run, input, rejected := rejectedHostFixture(t)
			provider := &scriptedToolProvider{responses: []*llm.ChatResponse{rejected}}
			code := apperror.CodeUnavailable
			if activeLease {
				code = apperror.CodePolicyDenied
			}
			legacy := &historicalRejectedHostStore{SQLiteStore: st, code: code}
			first, err := rejectedHostService(legacy, legacy, provider).Execute(t.Context(), input)
			if err == nil || first.Execution == nil {
				t.Fatalf("legacy failure missing: %v", err)
			}
			if activeLease {
				if _, err := st.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "another-worker", TTL: time.Minute}); err != nil {
					t.Fatal(err)
				}
			}
			if _, closed, err := st.EndFailedThreadTurn(t.Context(), input.ThreadID, run.ID, first.Execution.Handoff.Operation.ID); err == nil || closed {
				t.Fatalf("uncertain/active work was claimed settled: closed=%v err=%v", closed, err)
			}
			rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 10)
			if err != nil || rounds[0].Calls[0].Status != domain.SupervisorToolPending || len(provider.Requests()) != 1 {
				t.Fatalf("uncertain result changed or replayed: %#v %v", rounds, err)
			}
		})
	}
}

func TestHistoricalHostProposalSettlementDoesNotAbandonCreatedProposal(t *testing.T) {
	st, run, input, response := rejectedHostFixture(t)
	var payload map[string]any
	if err := json.Unmarshal(response.ToolCalls[0].Arguments, &payload); err != nil {
		t.Fatal(err)
	}
	// This file is never launched. It gives proposal creation a real, immutable
	// executable identity so the negative case has an actual durable proposal.
	executable := filepath.Join(payload["working_directory"].(string), "reviewed-tool.exe")
	if err := os.WriteFile(executable, []byte(strings.Repeat("fixture only; never execute\n", 30)), 0600); err != nil {
		t.Fatal(err)
	}
	payload["executable_path"] = executable
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response.ToolCalls[0].Arguments = encoded
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{response}}
	legacy := &historicalRejectedHostStore{SQLiteStore: st, code: apperror.CodePolicyDenied, failCreatedProposal: true}
	first, err := rejectedHostService(legacy, legacy, provider).Execute(t.Context(), input)
	if err == nil || first.Execution == nil {
		t.Fatalf("created proposal fixture did not fail: %v", err)
	}
	proposals, err := st.ListHostCommandProposals(t.Context(), run.ID, 10)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("fixture needs one actually persisted proposal: %#v %v", proposals, err)
	}
	if _, closed, err := st.EndFailedThreadTurn(t.Context(), input.ThreadID, run.ID, first.Execution.Handoff.Operation.ID); err == nil || closed {
		t.Fatalf("existing proposal was abandoned: closed=%v err=%v", closed, err)
	}
	rounds, err := st.ListRunSupervisorToolRoundsPage(t.Context(), run.ID, 0, 10)
	if err != nil || len(rounds) != 1 || rounds[0].Calls[0].Status != domain.SupervisorToolPending {
		t.Fatalf("existing proposal result was invented: %#v %v", rounds, err)
	}
}
