package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func boundedCommandInput(t *testing.T, marker string) toolgateway.CommandRuntimeInput {
	t.Helper()
	input := commandApprovalNativeInput(t, false)
	input.Commands[0].Arguments[1] = `require('fs').appendFileSync('count.txt','` + marker + `');process.stdout.write('bounded-command-result');`
	input.ReviewScope = &toolgateway.CommandReviewScope{RiskKinds: []string{"other_high_risk"}, OtherRiskReason: "write local acceptance markers"}
	return input
}

func TestCommandBoundedApprovalRevokeAtNativeBoundary(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCommandApprovalFixture(t, mode, false)
			f.record(t, boundedCommandInput(t, "1"), 1)
			if waiting, err := f.resume(t); err != nil || !waiting {
				t.Fatal(waiting, err)
			}
			control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
			result, err := control.Decide(t.Context(), boundedCommandRequest(t, f, 1))
			if err != nil {
				t.Fatal(err)
			}
			revoked := false
			f.preparedStore.afterPrepare = func() {
				_, err := f.st.RevokeSessionGrant(t.Context(), approval.RevokeGrantRequest{GrantID: result.Grant.ID, Reason: "revoke after native intent before dispatch", RevokedBy: "operator", IdempotencyKey: "native-revoke"})
				if err != nil {
					t.Fatal(err)
				}
				revoked = true
			}
			if waiting, err := f.resume(t); err != nil || waiting {
				t.Fatal(waiting, err)
			}
			if !revoked {
				t.Fatal("native intent boundary was not reached")
			}
			f.assertNoMarker(t, "count.txt")
		})
	}
}

func TestCommandBoundedApprovalExpiryPreventsDispatch(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionFull, false)
	f.record(t, boundedCommandInput(t, "1"), 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	request := boundedCommandRequest(t, f, 1)
	request.GrantTTLSeconds = 1
	control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
	result, err := control.Decide(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-time.After(time.Until(*result.Grant.ExpiresAt) + 10*time.Millisecond):
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	f.assertNoMarker(t, "count.txt")
}

func TestCommandBoundedApprovalLimitsAndScopeRemainExplicit(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	f.record(t, boundedCommandInput(t, "1"), 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
	request := boundedCommandRequest(t, f, 2)
	for _, limits := range [][2]int{{0, 2}, {901, 2}, {120, 0}, {120, 9}} {
		invalid := request
		invalid.GrantTTLSeconds, invalid.GrantMaxUses = limits[0], limits[1]
		if _, err := control.Decide(t.Context(), invalid); err == nil {
			t.Fatalf("accepted invalid limits: %v", limits)
		}
	}
	first, err := control.Decide(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.GrantMaxUses = 3
	if _, err := control.Decide(t.Context(), changed); err == nil {
		t.Fatal("replay expanded grant limits")
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	input := boundedCommandInput(t, "2")
	input.ReviewScope.OtherRiskReason = "a different local acceptance purpose"
	f.record(t, input, 2)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	secondRequest := boundedCommandRequest(t, f, 2)
	if _, err := f.st.AuthorizeCommandApprovalWithSessionGrant(t.Context(), approval.DecisionRequest{ProposalID: f.call.CallID, IdempotencyKey: secondRequest.OperationKey, Action: approval.ActionApprove, ReviewedBy: "operator"}, first.Grant.ID); err == nil {
		t.Fatal("consumed a grant from another purpose")
	}
	second, err := control.Decide(t.Context(), secondRequest)
	if err != nil || second.Grant.ID == first.Grant.ID || second.Consumption.UseOrdinal != 1 {
		t.Fatalf("new purpose did not get independent scope: %+v %v", second, err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
	if err != nil || string(data) != "12" {
		t.Fatal(string(data), err)
	}
}

func TestCommandBoundedApprovalRevokeStopsRunningBackground(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionFull, false)
	input := commandApprovalNativeInput(t, true)
	input.ReviewScope = boundedCommandInput(t, "1").ReviewScope
	f.record(t, input, 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
	result, err := control.Decide(t.Context(), boundedCommandRequest(t, f, 1))
	if err != nil {
		t.Fatal(err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	jobs, err := f.st.ListCommandRuntimeJobs(t.Context(), runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10})
	if err != nil || len(jobs) != 1 || jobs[0].State != runner.CommandRuntimeJobRunning {
		t.Fatalf("background native job not running: %+v %v", jobs, err)
	}
	if _, err := f.st.RevokeSessionGrant(t.Context(), approval.RevokeGrantRequest{GrantID: result.Grant.ID, Reason: "stop running grant", RevokedBy: "operator", IdempotencyKey: "stop-running"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	for {
		jobs, err = f.st.ListCommandRuntimeJobs(ctx, runner.CommandRuntimeListFilter{RunID: f.call.RunID, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if jobs[0].State.Terminal() {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("revoked background process remained running", ctx.Err())
		}
	}
	if jobs[0].State == runner.CommandRuntimeJobCompleted {
		t.Fatal("revoked background job reported success")
	}
	f.assertNoMarker(t, "stdin.txt")
}

func boundedCommandRequest(t *testing.T, f *commandApprovalFixture, uses int) DecideApprovalControlRequest {
	t.Helper()
	record, err := f.st.GetApprovalByProposal(t.Context(), f.call.CallID)
	if err != nil {
		t.Fatal(err)
	}
	return DecideApprovalControlRequest{Version: ApprovalControlProtocolVersion, RunID: f.call.RunID, ApprovalID: record.ID,
		Action: ApprovalControlApproveForRun, OperationKey: "bounded-review-" + f.call.CallID, ReviewedBy: "operator", Reason: "review each local command separately", GrantTTLSeconds: 120, GrantMaxUses: uses}
}

func TestCommandBoundedApprovalThreeModesRequireEachExactReview(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCommandApprovalFixture(t, mode, false)
			control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
			grantID := ""
			for round, marker := range []string{"1", "2"} {
				f.record(t, boundedCommandInput(t, marker), round+1)
				if waiting, err := f.resume(t); err != nil || !waiting {
					t.Fatalf("separate exact review missing: %v %t", err, waiting)
				}
				if round == 0 {
					f.assertNoMarker(t, "count.txt")
				} else if data, err := os.ReadFile(filepath.Join(f.root, "count.txt")); err != nil || string(data) != "1" {
					t.Fatalf("later command auto-executed: %q %v", data, err)
				}
				request := boundedCommandRequest(t, f, 2)
				decision, err := control.Decide(t.Context(), request)
				if err != nil || decision.Grant == nil || decision.Consumption == nil {
					t.Fatalf("bounded decision failed: %+v %v", decision, err)
				}
				if round == 0 {
					grantID = decision.Grant.ID
				}
				if decision.Grant.ID != grantID || decision.Grant.UsesRemaining != 1-round || decision.Consumption.UseOrdinal != round+1 || decision.Approval.ReviewedBy != "operator" {
					t.Fatalf("scope/consumption changed: %+v", decision)
				}
				for i := 0; i < 2; i++ {
					replayed, err := control.Decide(t.Context(), request)
					if err != nil || !replayed.Replayed || replayed.Grant.UsesRemaining != 1-round || replayed.Consumption.ID != decision.Consumption.ID {
						t.Fatalf("replay consumed again: %+v %v", replayed, err)
					}
				}
				if waiting, err := f.resume(t); err != nil || waiting {
					t.Fatalf("approved exact command failed: %v %t", err, waiting)
				}
			}
			data, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
			if err != nil || string(data) != "12" {
				t.Fatalf("different exact argv did not share declared scope: %q %v", data, err)
			}
			grant, err := f.st.GetSessionGrant(t.Context(), grantID)
			if err != nil || grant.UsesRemaining != 0 || grant.Status != approval.GrantRevoked {
				t.Fatalf("last legal use was not exhausted: %+v %v", grant, err)
			}
			f.record(t, boundedCommandInput(t, "3"), 3)
			if waiting, err := f.resume(t); err != nil || !waiting {
				t.Fatalf("exhausted scope bypassed new review: %v %t", err, waiting)
			}
			if _, err = f.st.AuthorizeApprovalWithSessionGrant(t.Context(), f.call.CallID, grant.ID); err == nil {
				t.Fatal("generic automatic grant route authorized a new command")
			}
			if data, err = os.ReadFile(filepath.Join(f.root, "count.txt")); err != nil || string(data) != "12" {
				t.Fatalf("unreviewed third effect: %q %v", data, err)
			}
		})
	}
}

func TestCommandBoundedApprovalExplicitRevokeAfterLastUsePreventsDispatch(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCommandApprovalFixture(t, mode, false)
			f.record(t, boundedCommandInput(t, "1"), 1)
			if waiting, err := f.resume(t); err != nil || !waiting {
				t.Fatal(waiting, err)
			}
			control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
			result, err := control.Decide(t.Context(), boundedCommandRequest(t, f, 1))
			if err != nil || result.Grant == nil || result.Grant.Status != approval.GrantRevoked {
				t.Fatalf("missing last-use consumption: %+v %v", result, err)
			}
			if _, err = f.st.RevokeSessionGrant(t.Context(), approval.RevokeGrantRequest{GrantID: result.Grant.ID, Reason: "stop before dispatch", RevokedBy: "operator", IdempotencyKey: "revoke-after-last-use"}); err != nil {
				t.Fatal(err)
			}
			if waiting, err := f.resume(t); err != nil || waiting {
				t.Fatalf("revocation did not resolve exact denied call: %v %t", err, waiting)
			}
			f.assertNoMarker(t, "count.txt")
			call, _, err := f.st.GetSupervisorApprovalCall(t.Context(), f.call.RunID, f.call.CallID)
			if err != nil || call.Status != domain.SupervisorToolDenied {
				t.Fatalf("revoked command not denied: %+v %v", call, err)
			}
		})
	}
}

func TestCommandBoundedApprovalConcurrentReviewConsumesOnce(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	f.record(t, boundedCommandInput(t, "1"), 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
	request := boundedCommandRequest(t, f, 2)
	results := make(chan DecideApprovalControlResult, 4)
	errors := make(chan error, 4)
	ready := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() { <-ready; result, err := control.Decide(t.Context(), request); results <- result; errors <- err }()
	}
	close(ready)
	consumptionID, grantID := "", ""
	for i := 0; i < 4; i++ {
		result, err := <-results, <-errors
		if err != nil || result.Consumption == nil || result.Grant == nil {
			t.Fatalf("concurrent exact review failed: %+v %v", result, err)
		}
		if i == 0 {
			consumptionID, grantID = result.Consumption.ID, result.Grant.ID
		}
		if result.Consumption.ID != consumptionID || result.Grant.ID != grantID || result.Grant.UsesRemaining != 1 {
			t.Fatalf("concurrent review consumed twice: %+v", result)
		}
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
	if err != nil || string(data) != "1" {
		t.Fatal(string(data), err)
	}
}

func TestCommandBoundedApprovalExpiredConsumptionDoesNotSealDecision(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	f.record(t, boundedCommandInput(t, "1"), 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
	request := boundedCommandRequest(t, f, 2)
	request.GrantTTLSeconds = 3
	first, err := control.Decide(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if waiting, err := f.resume(t); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	f.record(t, boundedCommandInput(t, "2"), 2)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	select {
	case <-time.After(time.Until(*first.Grant.ExpiresAt) + 10*time.Millisecond):
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	second := boundedCommandRequest(t, f, 2)
	if _, err := f.st.AuthorizeCommandApprovalWithSessionGrant(t.Context(), approval.DecisionRequest{
		ProposalID: f.call.CallID, IdempotencyKey: second.OperationKey,
		Action: approval.ActionApprove, ReviewedBy: "operator",
	}, first.Grant.ID); err == nil {
		t.Fatal("consumed an expired grant")
	}
	record, err := f.st.GetApproval(t.Context(), second.ApprovalID)
	if err != nil || record.Status != approval.StatusPending || record.GrantID != "" {
		t.Fatalf("failed consumption decided approval: %+v %v", record, err)
	}
	grant, err := f.st.GetSessionGrant(t.Context(), first.Grant.ID)
	if err != nil || grant.Status != approval.GrantRevoked || grant.RevocationReason != "bounded risk escalation grant expired" || grant.UsesRemaining != 1 {
		t.Fatalf("expiry was not durably recorded without consumption: %+v %v", grant, err)
	}
	// A rejected attempt has no successful decision to replay. The operator
	// can still deny the pending command with the uncommitted decision key.
	second.Action = ApprovalControlDeny
	second.GrantTTLSeconds, second.GrantMaxUses = 0, 0
	denied, err := control.Decide(t.Context(), second)
	if err != nil || denied.Approval.Status != approval.StatusDenied {
		t.Fatalf("failed expired consumption sealed a successful decision: %+v %v", denied, err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
	if err != nil || string(data) != "1" {
		t.Fatalf("unapproved second command executed: %q %v", data, err)
	}
}

func TestCommandBoundedApprovalColdHistoryNeverRenewsOrResends(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		for _, state := range []string{"approved", "started_unknown", "completed"} {
			t.Run(string(mode)+"/"+state, func(t *testing.T) {
				f := newCommandApprovalFixture(t, mode, false)
				f.record(t, boundedCommandInput(t, "1"), 1)
				if waiting, err := f.resume(t); err != nil || !waiting {
					t.Fatal(waiting, err)
				}
				control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
				request := boundedCommandRequest(t, f, 2)
				original, err := control.Decide(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				if state == "started_unknown" {
					if _, err := f.st.RecordSupervisorToolExecutionStarted(t.Context(), f.turn.Checkpoint, f.call.CallID); err != nil {
						t.Fatal(err)
					}
				}
				if state == "completed" {
					if waiting, err := f.resume(t); err != nil || waiting {
						t.Fatal(waiting, err)
					}
				}
				if err := f.manager.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := f.st.Close(); err != nil {
					t.Fatal(err)
				}
				f.st, err = store.Open(f.path)
				if err != nil {
					t.Fatal(err)
				}
				f.preparedStore.SQLiteStore = f.st
				f.caps.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
				f.manager, err = runner.NewPlatformCommandRuntimeManager(f.preparedStore, idgen.New("bounded-cold-owner"))
				if err != nil {
					t.Fatal(err)
				}
				f.service, err = NewCommandRuntimeService(f.st, f.manager, f.caps)
				if err != nil {
					t.Fatal(err)
				}
				f.supervisor = NewAgentRunner(f.st, nil, f.checker).WithExecutionPermissionCapabilities(f.caps).WithCommandRuntime(f.service)
				control = NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
				for i := 0; i < 2; i++ {
					replay, err := control.Decide(t.Context(), request)
					if err != nil || !replay.Replayed || replay.Consumption.ID != original.Consumption.ID || replay.Grant.UsesRemaining != 1 || !replay.Grant.ExpiresAt.Equal(*original.Grant.ExpiresAt) {
						t.Fatalf("cold review renewed grant: %+v %v", replay, err)
					}
					if waiting, err := f.resume(t); err != nil || waiting {
						t.Fatal(waiting, err)
					}
				}
				call, _, err := f.st.GetSupervisorApprovalCall(t.Context(), f.call.RunID, f.call.CallID)
				if err != nil {
					t.Fatal(err)
				}
				want := "command_authority_expired"
				if state == "started_unknown" {
					want = "outcome_unknown"
				}
				if state == "completed" {
					want = ""
				}
				if call.ErrorCode != want {
					t.Fatalf("unexpected cold result: %+v", call)
				}
				if state == "completed" {
					data, err := os.ReadFile(filepath.Join(f.root, "count.txt"))
					if err != nil || string(data) != "1" {
						t.Fatal(string(data), err)
					}
				} else {
					f.assertNoMarker(t, "count.txt")
				}
			})
		}
	}
}
