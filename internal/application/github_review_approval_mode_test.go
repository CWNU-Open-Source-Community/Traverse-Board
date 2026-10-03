package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolcontract"
)

type githubReviewAfterStartStore struct {
	*store.SQLiteStore
	afterStart func()
	proof      func(approval.Record) approval.Record
}

func (s *githubReviewAfterStartStore) StartGitHubReviewWrite(ctx context.Context, id, approvalID, fingerprint string,
	at time.Time,
) (githubreview.WriteRecord, bool, error) {
	record, replayed, err := s.SQLiteStore.StartGitHubReviewWrite(ctx, id, approvalID, fingerprint, at)
	if err == nil && !replayed && s.afterStart != nil {
		s.afterStart()
	}
	return record, replayed, err
}

func (s *githubReviewAfterStartStore) GetApproval(ctx context.Context, id string) (approval.Record, error) {
	proof, err := s.SQLiteStore.GetApproval(ctx, id)
	if err == nil && s.proof != nil {
		proof = s.proof(proof)
	}
	return proof, err
}

func reviewModeGitHubWrite(t *testing.T, f githubReviewApplicationFixture) GitHubReviewWriteReviewResult {
	t.Helper()
	review, err := f.service.ReviewWrite(t.Context(), f.request)
	if err != nil || review.Approval.Status != approval.StatusPending ||
		review.Operation.ApprovalFingerprint == review.Preview.ApprovalFingerprint {
		t.Fatalf("review did not bind pending native authority: %v %#v", err, review)
	}
	return review
}

func approveModeGitHubWrite(t *testing.T, f githubReviewApplicationFixture, review GitHubReviewWriteReviewResult) GitHubReviewWriteExecuteRequest {
	t.Helper()
	decided, err := f.native.state.DecideApproval(t.Context(), approval.DecisionRequest{
		ProposalID: review.Operation.ID, IdempotencyKey: "approve-" + review.Operation.ID,
		Action: approval.ActionApprove, ReviewedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	return GitHubReviewWriteExecuteRequest{ProtocolVersion: GitHubReviewAPIProtocolVersion,
		RunID: f.native.run.ID, OperationID: review.Operation.ID, ApprovalID: decided.Approval.ID, RequestedBy: "operator"}
}

func TestGitHubReviewApprovalModesRequireExactConsentAndTerminalReplay(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGitHubReviewApplicationFixture(t, mode)
			review := reviewModeGitHubWrite(t, f)
			request := GitHubReviewWriteExecuteRequest{ProtocolVersion: GitHubReviewAPIProtocolVersion,
				RunID: f.native.run.ID, OperationID: review.Operation.ID, ApprovalID: review.Approval.ID, RequestedBy: "operator"}
			if _, err := f.service.ExecuteWrite(t.Context(), request); err == nil || f.remote.executeCalls != 0 {
				t.Fatalf("pending approval executed: %v calls=%d", err, f.remote.executeCalls)
			}
			request = approveModeGitHubWrite(t, f, review)
			result, err := f.service.ExecuteWrite(t.Context(), request)
			if err != nil || result.Operation.Status != githubreview.OperationSucceeded || f.remote.executeCalls != 1 {
				t.Fatalf("exact approved write failed: %v %#v calls=%d", err, result, f.remote.executeCalls)
			}
			f.native.capabilities.RuntimeAuthority.RevokeRun(f.native.run.ID)
			replay, err := f.service.ExecuteWrite(t.Context(), request)
			if err != nil || !replay.Replayed || replay.Receipt.ID != result.Receipt.ID || f.remote.executeCalls != 1 {
				t.Fatalf("terminal receipt was not a pure replay: %v %#v", err, replay)
			}
			if _, found := f.native.capabilities.RuntimeAuthority.RunAuthorizationFence(f.native.run.ID); found {
				t.Fatal("terminal replay renewed revoked authority")
			}
		})
	}
}

func TestGitHubReviewApprovalModesKeepDeniedConsent(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGitHubReviewApplicationFixture(t, mode)
			review := reviewModeGitHubWrite(t, f)
			denied, err := f.native.state.DecideApproval(t.Context(), approval.DecisionRequest{
				ProposalID: review.Operation.ID, IdempotencyKey: "deny-write", Action: approval.ActionDeny, ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			request := GitHubReviewWriteExecuteRequest{ProtocolVersion: GitHubReviewAPIProtocolVersion, RunID: f.native.run.ID,
				OperationID: review.Operation.ID, ApprovalID: denied.Approval.ID, RequestedBy: "operator"}
			if _, err := f.service.ExecuteWrite(t.Context(), request); err == nil || f.remote.executeCalls != 0 {
				t.Fatalf("denied consent executed: %v", err)
			}
			if _, err := f.service.ReviewWrite(t.Context(), f.request); err == nil {
				t.Fatal("re-review revived denied consent")
			}
			stored, _, err := f.native.state.GetGitHubReviewWrite(t.Context(), review.Operation.ID)
			if err != nil || stored.Status != githubreview.OperationProposed {
				t.Fatalf("denied write started: %v %#v", err, stored)
			}
		})
	}
}

func TestGitHubReviewApprovalModesRecheckAfterDurableStart(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		for _, change := range []string{"revoke", "network", "connection-generation", "approval", "cancel"} {
			t.Run(string(mode)+"/"+change, func(t *testing.T) {
				f := newGitHubReviewApplicationFixture(t, mode)
				review := reviewModeGitHubWrite(t, f)
				request := approveModeGitHubWrite(t, f, review)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				wrapped := &githubReviewAfterStartStore{SQLiteStore: f.native.state}
				wrapped.afterStart = func() {
					switch change {
					case "revoke":
						f.native.capabilities.RuntimeAuthority.RevokeRun(f.native.run.ID)
					case "approval":
						wrapped.proof = func(a approval.Record) approval.Record { a.Status = approval.StatusDenied; return a }
					case "cancel":
						cancel()
					default:
						connection := f.configured.Connection
						configuration := GitHubReviewConfigureRequest{ProtocolVersion: GitHubReviewAPIProtocolVersion,
							Repository: connection.Repository, Credential: connection.Credential, AllowedLogHosts: []string{},
							WriteEnabled: false, Enabled: true, ExpectedGeneration: connection.Generation, RequestedBy: "operator"}
						disabled, err := f.service.Configure(t.Context(), configuration)
						if err != nil {
							t.Fatal(err)
						}
						if change == "connection-generation" {
							configuration.WriteEnabled = true
							configuration.ExpectedGeneration = disabled.Connection.Generation
							if _, err := f.service.Configure(t.Context(), configuration); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				f.service.store = wrapped
				result, err := f.service.ExecuteWrite(ctx, request)
				if err == nil || result.Operation.Status != githubreview.OperationFailed ||
					result.Receipt.Status != githubreview.ReceiptFailed || f.remote.executeCalls != 0 {
					t.Fatalf("late authority change reached mutation: %v %#v calls=%d", err, result, f.remote.executeCalls)
				}
				if change == "revoke" {
					if _, found := f.native.capabilities.RuntimeAuthority.RunAuthorizationFence(f.native.run.ID); found {
						t.Fatal("dispatch recreated a revoked fence")
					}
				}
				replay, err := f.service.ExecuteWrite(t.Context(), request)
				if err != nil || !replay.Replayed || f.remote.executeCalls != 0 {
					t.Fatalf("locally rejected attempt was dispatched again: %v %#v", err, replay)
				}
			})
		}
	}
}

func TestGitHubReviewApprovalModesRejectColdUnstartedApproval(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGitHubReviewApplicationFixture(t, mode)
			review := reviewModeGitHubWrite(t, f)
			request := approveModeGitHubWrite(t, f, review)
			for _, missing := range []bool{false, true} {
				cold := f.native.capabilities
				cold.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
				if missing {
					cold.RuntimeAuthority = nil
					cold.FullAccessRequiresRuntimeGrant = false
				}
				service, err := NewGitHubReviewService(f.native.state, f.credentials, f.native.executor, cold)
				if err != nil {
					t.Fatal(err)
				}
				service.clientFactory = f.service.clientFactory
				if _, err := service.ExecuteWrite(t.Context(), request); err == nil || f.remote.executeCalls != 0 {
					t.Fatalf("cold runtime executed an old approval: %v missing=%t", err, missing)
				}
				if _, found := cold.RuntimeAuthority.RunAuthorizationFence(f.native.run.ID); found {
					t.Fatal("cold execution issued a new fence")
				}
			}
			stored, _, err := f.native.state.GetGitHubReviewWrite(t.Context(), review.Operation.ID)
			if err != nil || stored.Status != githubreview.OperationProposed {
				t.Fatalf("cold rejection started the write: %v %#v", err, stored)
			}
		})
	}
}

func TestGitHubReviewRejectsForeignApprovalBindingsAndGuardRevival(t *testing.T) {
	f := newGitHubReviewApplicationFixture(t, domain.RunExecutionPermissionFull)
	review := reviewModeGitHubWrite(t, f)
	request := approveModeGitHubWrite(t, f, review)
	wrapped := &githubReviewAfterStartStore{SQLiteStore: f.native.state}
	f.service.store = wrapped
	for _, field := range []string{"proposal", "run", "session", "workspace", "tool", "action", "mode", "grant", "fingerprint"} {
		t.Run(field, func(t *testing.T) {
			wrapped.proof = func(a approval.Record) approval.Record {
				switch field {
				case "proposal":
					a.ProposalID = "another-proposal"
				case "run":
					a.RunID = "another-run"
				case "session":
					a.SessionID = "another-session"
				case "workspace":
					a.WorkspaceID = "another-workspace"
				case "tool":
					a.ToolName = "another-tool"
				case "action":
					a.ActionClass = "another-action"
				case "mode":
					a.Mode = "grant"
				case "grant":
					a.GrantID = "another-grant"
				case "fingerprint":
					a.RequestFingerprint = strings.Repeat("0", 64)
				}
				return a
			}
			if _, err := f.service.ExecuteWrite(t.Context(), request); err == nil || f.remote.executeCalls != 0 {
				t.Fatalf("foreign %s proof executed: %v", field, err)
			}
		})
	}
	wrapped.proof = nil
	guard, err := f.service.writeDispatchGuard(t.Context(), review.Operation, request.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := githubreview.ReviewWriteOperation(review.Operation.Spec, review.Preview)
	fingerprint, _ := toolcontract.FingerprintOperation(op)
	if err := guard(t.Context(), strings.Repeat("0", 64)); err == nil {
		t.Fatal("changed inputs received dispatch authority")
	}
	if err := guard(t.Context(), fingerprint); err == nil {
		t.Fatal("a denied guard was revived")
	}
}

func TestGitHubReviewUnknownOutcomeRecoversWithoutRenewedWriteAuthority(t *testing.T) {
	f := newGitHubReviewApplicationFixture(t, domain.RunExecutionPermissionAsk)
	review := reviewModeGitHubWrite(t, f)
	request := approveModeGitHubWrite(t, f, review)
	f.remote.executeError = &githubreview.Error{Code: githubreview.FailureOffline, Message: "fixture lost response"}
	result, err := f.service.ExecuteWrite(t.Context(), request)
	if err == nil || result.Operation.Status != githubreview.OperationRunning || f.remote.executeCalls != 1 {
		t.Fatalf("uncertain response was finalized: %v %#v", err, result)
	}
	f.native.capabilities.RuntimeAuthority.RevokeRun(f.native.run.ID)
	if _, err := f.service.ExecuteWrite(t.Context(), request); err == nil || f.remote.executeCalls != 1 {
		t.Fatalf("uncertain operation was sent again: %v", err)
	}
	recovered, err := f.service.ReconcileStartup(t.Context(), 10)
	if err != nil || recovered.Recovered != 1 || f.remote.recoverCalls != 1 || f.remote.executeCalls != 1 {
		t.Fatalf("read-only recovery failed: %v %#v", err, recovered)
	}
	if _, found := f.native.capabilities.RuntimeAuthority.RunAuthorizationFence(f.native.run.ID); found {
		t.Fatal("recovery renewed write authority")
	}
}

func TestGitHubReviewRetainedModesReadAndReplayWithoutNewAuthority(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionConservative,
		domain.RunExecutionPermissionWorkspaceAccess, domain.RunExecutionPermissionApproval,
		domain.RunExecutionPermissionFullAccess, domain.RunExecutionPermissionDebug} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGitHubReviewApplicationFixture(t, domain.RunExecutionPermissionAsk)
			review := reviewModeGitHubWrite(t, f)
			request := approveModeGitHubWrite(t, f, review)
			result, err := f.service.ExecuteWrite(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			seedRetainedNativePermission(t, f.native.database, f.native.state, f.native.run.ID, mode)
			f.native.capabilities.RuntimeAuthority.RevokeRun(f.native.run.ID)
			projection, err := f.service.Projection(t.Context(), f.native.run.ID, f.configured.Connection.ID, f.request.Spec.Identity.Number, 20)
			if err != nil || len(projection.Writes) != 1 {
				t.Fatalf("retained history unreadable: %v %#v", err, projection)
			}
			f.request.OperationKey = "retired-new-write"
			if _, err := f.service.ReviewWrite(t.Context(), f.request); err == nil {
				t.Fatal("retired mode created new write authority")
			}
			replay, err := f.service.ExecuteWrite(t.Context(), request)
			if err != nil || !replay.Replayed || replay.Receipt.ID != result.Receipt.ID || f.remote.executeCalls != 1 {
				t.Fatalf("retained receipt was dispatched again: %v %#v", err, replay)
			}
			if _, found := f.native.capabilities.RuntimeAuthority.RunAuthorizationFence(f.native.run.ID); found {
				t.Fatal("legacy observation renewed authority")
			}
		})
	}
}

func TestGitHubReviewContentOnlyLegacyApprovalCannotAuthorizeNewWrite(t *testing.T) {
	f := newGitHubReviewApplicationFixture(t, domain.RunExecutionPermissionAsk)
	review := reviewModeGitHubWrite(t, f)
	legacy := review.Operation
	legacy.ID = "retained-github-review-write"
	legacy.OperationKeySHA256 = githubreview.Fingerprint("legacy-key")
	legacy.ApprovalFingerprint = legacy.Preview.ApprovalFingerprint
	created, _, err := f.native.state.CreateGitHubReviewWrite(t.Context(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.native.state.EnsureApproval(t.Context(), approval.Proposal{
		IdempotencyKey: "legacy-approval", ProposalID: created.ID, SessionID: created.SessionID, WorkspaceID: created.WorkspaceID,
		ToolName: githubreview.ApprovalToolName, ActionClass: githubreview.ApprovalActionClass, Mode: "per_call",
		Status: approval.StatusPending, RequestFingerprint: legacy.ApprovalFingerprint,
		RequestedBy: "operator", CreatedAt: legacy.CreatedAt, UpdatedAt: legacy.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	request := approveModeGitHubWrite(t, f, GitHubReviewWriteReviewResult{Operation: created, Approval: proof})
	if _, err := f.service.ExecuteWrite(t.Context(), request); err == nil || f.remote.executeCalls != 0 {
		t.Fatalf("content-only legacy approval became authority: %v", err)
	}
	stored, found, err := f.native.state.GetGitHubReviewWrite(t.Context(), created.ID)
	if err != nil || !found || stored.Status != githubreview.OperationProposed {
		t.Fatalf("legacy operation is unreadable or started: %v %#v", err, stored)
	}
}
