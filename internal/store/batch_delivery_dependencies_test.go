package store

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/waitgraph"
)

func TestBatchDependencyAcceptanceSettlesOnlyDeclaredCoreWaitAndExactReceipt(t *testing.T) {
	s, plan, children := newBatchDeliveryStoreFixture(t)
	ctx := t.Context()
	if _, _, _, err := s.CreateBatchDeliveryPlan(ctx, plan, children); err != nil {
		t.Fatal(err)
	}
	now := plan.CreatedAt.Add(time.Second)
	if _, _, _, err := s.ActivateBatchDeliveryWorkspace(ctx, batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxDispatch,
		"root", "dispatch", "dependency-dispatch-001", now), plan.BaseCommit); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.AppendBatchDeliveryMailbox(ctx, batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxAck,
		children[0].AgentID, "ack", "dependency-ack-0000001", now.Add(time.Second)), children[0].OwnerTokenDigest, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	unrelated, _, err := s.RecordDependencyWait(ctx, domain.DependencyEdge{ID: idgen.New("depedge"), RunID: plan.RunID,
		SourceKind: waitgraph.KindAgent, SourceID: plan.RootAgentID, TargetKind: waitgraph.KindAgent, TargetID: children[0].AgentID,
		Reason: "wait for a separate analysis report", State: domain.AgentDependencyWait, FailurePolicy: domain.DependencyPolicyFail,
		Generation: 1, Deadline: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}, "dependency-unrelated-001")
	if err != nil {
		t.Fatal(err)
	}
	receipt := batchReceiptFixture(plan, children[0], now.Add(2*time.Second))
	if _, _, err := s.RecordBatchDeliveryReceipt(ctx, receipt, children[0].OwnerTokenDigest,
		batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxReadyForReview, children[0].AgentID, "ready", "dependency-ready-00001", receipt.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	review := domain.BatchDeliveryReview{ID: idgen.New("batchreview"), PlanID: plan.ID, Ordinal: 1, Generation: 1,
		ProtocolVersion: domain.BatchDeliveryReviewVersion, ReceiptID: receipt.ID, Reviewer: "independent-reviewer",
		Verdict: domain.BatchReviewAccepted, Summary: "full delivery accepted", BaseCommit: receipt.BaseCommit,
		HeadCommit: receipt.HeadCommit, DiffSHA256: receipt.DiffSHA256, CallChainSHA256: receipt.CallChainSHA256,
		FullDiffReviewed: true, CallChainReviewed: true, TestsReviewed: true,
		OperationDigest: runmutation.Fingerprint("dependency-review", "operation"), RequestFingerprint: runmutation.Fingerprint("dependency-review", "request"), CreatedAt: receipt.CreatedAt.Add(time.Second)}
	if _, err := s.SettleBatchDeliveryDependencies(ctx, plan.ID, 1, 1, receipt.ID, review.ID); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("pending generation accepted: %v", err)
	}
	if _, _, err := s.RecordBatchDeliveryReview(ctx, review, batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxAccepted,
		review.Reviewer, review.Summary, "dependency-review-msg01", review.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	var satisfied, waiting, wakeCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_dependency_edges WHERE run_id=? AND reason='child task dependency' AND state='satisfied'`, plan.RunID).Scan(&satisfied); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_dependency_edges WHERE id=? AND state='wait'`, unrelated.ID).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if satisfied != 1 || waiting != 1 {
		t.Fatalf("settled=%d unrelated-waiting=%d", satisfied, waiting)
	}
	for i := 0; i < 2; i++ {
		wakes, err := s.SettleBatchDeliveryDependencies(ctx, plan.ID, 1, 1, receipt.ID, review.ID)
		if err != nil || len(wakes) != 0 {
			t.Fatalf("recovery repeated wake: %v %v", wakes, err)
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_dependency_wakes WHERE run_id=?`, plan.RunID).Scan(&wakeCount); err != nil || wakeCount != 1 {
		t.Fatalf("wakeCount=%d err=%v", wakeCount, err)
	}
	for _, input := range []struct {
		plan, receipt, review string
		generation            int64
	}{
		{plan.ID, receipt.ID, review.ID, 2}, {plan.ID, "foreign-receipt", review.ID, 1}, {plan.ID, receipt.ID, "foreign-review", 1},
	} {
		if _, err := s.SettleBatchDeliveryDependencies(ctx, input.plan, 1, input.generation, input.receipt, input.review); apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatalf("wrong binding accepted: %#v %v", input, err)
		}
	}
	if _, err := s.SettleBatchDeliveryDependencies(ctx, "foreign-plan", 1, 1, receipt.ID, review.ID); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("foreign plan accepted: %v", err)
	}
}

func TestBatchDependencyChangesRequestedDoesNotWake(t *testing.T) {
	s, plan, children := newBatchDeliveryStoreFixture(t)
	ctx := t.Context()
	if _, _, _, err := s.CreateBatchDeliveryPlan(ctx, plan, children); err != nil {
		t.Fatal(err)
	}
	now := plan.CreatedAt.Add(time.Second)
	if _, _, _, err := s.ActivateBatchDeliveryWorkspace(ctx, batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxDispatch, "root", "dispatch", "changes-dispatch-00001", now), plan.BaseCommit); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.AppendBatchDeliveryMailbox(ctx, batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxAck, children[0].AgentID, "ack", "changes-ack-000000001", now.Add(time.Second)), children[0].OwnerTokenDigest, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	receipt := batchReceiptFixture(plan, children[0], now.Add(2*time.Second))
	if _, _, err := s.RecordBatchDeliveryReceipt(ctx, receipt, children[0].OwnerTokenDigest, batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxReadyForReview, children[0].AgentID, "ready", "changes-ready-0000001", receipt.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	review := domain.BatchDeliveryReview{ID: idgen.New("batchreview"), PlanID: plan.ID, Ordinal: 1, Generation: 1, ProtocolVersion: domain.BatchDeliveryReviewVersion,
		ReceiptID: receipt.ID, Reviewer: "independent-reviewer", Verdict: domain.BatchReviewChangesRequested, Summary: "add evidence", BaseCommit: receipt.BaseCommit,
		HeadCommit: receipt.HeadCommit, DiffSHA256: receipt.DiffSHA256, CallChainSHA256: receipt.CallChainSHA256, FullDiffReviewed: true, CallChainReviewed: true, TestsReviewed: true,
		OperationDigest: runmutation.Fingerprint("changes-review", "operation"), RequestFingerprint: runmutation.Fingerprint("changes-review", "request"), CreatedAt: receipt.CreatedAt.Add(time.Second)}
	if _, _, err := s.RecordBatchDeliveryReview(ctx, review, batchMailboxFixture(plan.ID, 1, 1, domain.BatchMailboxChangesRequested, review.Reviewer, review.Summary, "changes-review-msg001", review.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleBatchDeliveryDependencies(ctx, plan.ID, 1, 1, receipt.ID, review.ID); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changes-requested accepted: %v", err)
	}
	var wakes int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_dependency_wakes WHERE run_id=?`, plan.RunID).Scan(&wakes); err != nil || wakes != 0 {
		t.Fatalf("changes woke=%d err=%v", wakes, err)
	}
}
