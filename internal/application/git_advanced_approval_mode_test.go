package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolcontract"
)

type gitAdvancedAfterStartStore struct {
	*store.SQLiteStore
	afterStart func()
}

func (s *gitAdvancedAfterStartStore) StartGitAdvancedOperation(ctx context.Context,
	id, approvalID, fingerprint string, at time.Time,
) (gitadvanced.OperationRecord, bool, error) {
	record, replayed, err := s.SQLiteStore.StartGitAdvancedOperation(ctx, id, approvalID, fingerprint, at)
	if err == nil && !replayed {
		s.afterStart()
	}
	return record, replayed, err
}

func reviewApprovalModeGitHunk(t *testing.T, f gitAdvancedApplicationFixture) GitAdvancedReviewResult {
	t.Helper()
	path := filepath.Join(f.root, "base.txt")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(original), "two\n", "CHANGED\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := gitadvanced.Spec{ProtocolVersion: gitadvanced.ProtocolVersion, Operation: gitadvanced.HunkStage}
	discovery, err := f.service.DiscoverHunks(t.Context(), f.run.ID, spec)
	if err != nil || len(discovery.Preview.Hunks) != 1 {
		t.Fatalf("discover exact hunk: %v %#v", err, discovery)
	}
	spec.HunkIDs = []string{discovery.Preview.Hunks[0].ID}
	review, err := f.service.Review(t.Context(), GitAdvancedReviewRequest{
		ProtocolVersion: GitAdvancedAPIProtocolVersion, RunID: f.run.ID, Scope: f.scope(),
		OperationKey: "three-mode-git-review", RequestedBy: "operator", Spec: spec})
	if err != nil || review.Operation == nil || review.Approval == nil {
		t.Fatalf("review: %v %#v", err, review)
	}
	return review
}

func approveModeGitHunk(t *testing.T, f gitAdvancedApplicationFixture, review GitAdvancedReviewResult) GitAdvancedExecuteRequest {
	t.Helper()
	approved, err := f.state.DecideApproval(t.Context(), approval.DecisionRequest{
		ProposalID: review.Operation.ID, IdempotencyKey: "three-mode-git-approve",
		Action: approval.ActionApprove, ReviewedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	return GitAdvancedExecuteRequest{ProtocolVersion: GitAdvancedAPIProtocolVersion,
		RunID: f.run.ID, Scope: f.scope(), RequestedBy: "operator",
		OperationID: review.Operation.ID, ApprovalID: approved.Approval.ID}
}

func TestGitAdvancedApprovalModesRequireExactConsentAndReplayWithoutMutation(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGitAdvancedApplicationFixture(t, mode)
			review := reviewApprovalModeGitHunk(t, f)
			pending := GitAdvancedExecuteRequest{ProtocolVersion: GitAdvancedAPIProtocolVersion,
				RunID: f.run.ID, Scope: f.scope(), RequestedBy: "operator",
				OperationID: review.Operation.ID, ApprovalID: review.Approval.ID}
			if _, err := f.service.Execute(t.Context(), pending); err == nil {
				t.Fatal("permission mode executed without exact consent")
			}
			if got := runFixtureGit(t, "-C", f.root, "diff", "--cached", "--name-only"); got != "" {
				t.Fatalf("unapproved operation mutated index: %s", got)
			}
			request := approveModeGitHunk(t, f, review)
			result, err := f.service.Execute(t.Context(), request)
			if err != nil || result.Receipt.Status != gitadvanced.ReceiptSucceeded {
				t.Fatalf("approved operation: %v %#v", err, result)
			}
			before := runFixtureGit(t, "-C", f.root, "diff", "--cached")
			if !strings.Contains(before, "+CHANGED") {
				t.Fatalf("approved hunk was not staged: %s", before)
			}
			replayed, err := f.service.Execute(t.Context(), request)
			if err != nil || !replayed.Replayed || replayed.Operation.ID != result.Operation.ID ||
				runFixtureGit(t, "-C", f.root, "diff", "--cached") != before {
				t.Fatalf("terminal replay changed the original operation: %v %#v", err, replayed)
			}
		})
	}
}

func TestGitAdvancedApprovalModesRecheckRevocationAfterDurableStart(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGitAdvancedApplicationFixture(t, mode)
			review := reviewApprovalModeGitHunk(t, f)
			request := approveModeGitHunk(t, f, review)
			if _, found := f.capabilities.RuntimeAuthority.RunAuthorizationFence(f.run.ID); !found {
				t.Fatal("explicit review did not bind a runtime fence")
			}
			f.service.store = &gitAdvancedAfterStartStore{SQLiteStore: f.state, afterStart: func() {
				f.capabilities.RuntimeAuthority.RevokeRun(f.run.ID)
			}}
			result, err := f.service.Execute(t.Context(), request)
			if err == nil || result.Operation.Status != gitadvanced.OperationFailed ||
				result.Receipt.Status != gitadvanced.ReceiptFailed || result.Receipt.PostBinding.RepositorySHA256 == "" {
				t.Fatalf("revoked dispatch must retain terminal failure and observed state: %v %#v", err, result)
			}
			if got := runFixtureGit(t, "-C", f.root, "diff", "--cached", "--name-only"); got != "" {
				t.Fatalf("revoked operation mutated index: %s", got)
			}
			if _, found := f.capabilities.RuntimeAuthority.RunAuthorizationFence(f.run.ID); found {
				t.Fatal("executing old approval recreated a revoked epoch")
			}
		})
	}
}

func TestGitAdvancedApprovalModesRejectColdRuntimeApprovalWithoutRegrant(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGitAdvancedApplicationFixture(t, mode)
			review := reviewApprovalModeGitHunk(t, f)
			request := approveModeGitHunk(t, f, review)
			for _, state := range []string{"fresh", "missing"} {
				t.Run(state, func(t *testing.T) {
					cold := f.capabilities
					cold.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
					if state == "missing" {
						cold.RuntimeAuthority = nil
						cold.FullAccessRequiresRuntimeGrant = false
					}
					service, err := NewGitAdvancedService(f.state, f.executor, cold)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := service.Execute(t.Context(), request); err == nil {
						t.Fatal("cold runtime executed an old approval")
					}
					if _, found := cold.RuntimeAuthority.RunAuthorizationFence(f.run.ID); found {
						t.Fatal("cold runtime issued authority while checking an old approval")
					}
					if state == "missing" {
						result, err := service.Review(t.Context(), GitAdvancedReviewRequest{
							ProtocolVersion: GitAdvancedAPIProtocolVersion, RunID: f.run.ID,
							Scope: f.scope(), OperationKey: "missing-runtime-review", RequestedBy: "operator",
							Spec: review.Preview.Spec})
						if err == nil || result.Operation != nil || result.Approval != nil {
							t.Fatalf("missing runtime created an executable approval: %v %#v", err, result)
						}
						projection, err := service.Projection(t.Context(), f.run.ID, 20)
						if err != nil || projection.Authority.Executable || len(projection.Operations) != 1 {
							t.Fatalf("missing-runtime observation must remain readable without new authority: %v %#v", err, projection)
						}
					}
				})
			}
			stored, found, err := f.state.GetGitAdvancedOperation(t.Context(), review.Operation.ID)
			if err != nil || !found || stored.Status != gitadvanced.OperationProposed {
				t.Fatalf("cold rejection began the mutation: %v %#v", err, stored)
			}
			if got := runFixtureGit(t, "-C", f.root, "diff", "--cached", "--name-only"); got != "" {
				t.Fatalf("cold runtime mutated index: %s", got)
			}
		})
	}
}

func TestGitAdvancedDispatchGuardCannotResumeAfterInputRejection(t *testing.T) {
	f := newGitAdvancedApplicationFixture(t)
	review := reviewApprovalModeGitHunk(t, f)
	request := approveModeGitHunk(t, f, review)
	guard, err := f.service.operationDispatchGuard(t.Context(), request, *review.Operation, review.Preview)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := repository.AdvancedOperation(review.Preview)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard(t.Context(), strings.Repeat("0", 64)); err == nil {
		t.Fatal("changed input received execution authority")
	}
	if err := guard(t.Context(), fingerprint); err == nil {
		t.Fatal("a rejected operation guard was revived with its original input")
	}
	if got := runFixtureGit(t, "-C", f.root, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("rejected operation changed the index: %s", got)
	}
}
