package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/toolbudget"
)

// Change real durable state after the application revalidation and immediately
// before the store transaction. A passing application helper cannot satisfy
// these tests: each transaction must read current usage and the exact live lease.
type sandboxCandidateTransactionBoundary struct {
	*SQLiteStore
	stage  string
	before func()
	calls  int
}

func (s *sandboxCandidateTransactionBoundary) intercept(stage string) {
	if s.stage == stage {
		s.calls++
		s.before()
	}
}

func (s *sandboxCandidateTransactionBoundary) CreateSandboxDisabledExecution(ctx context.Context,
	execution sandbox.DisabledExecution, inputs []sandbox.InputArtifactBinding,
	operation sandbox.ExecutionOperation, ownerID string, ttl time.Duration,
) (sandbox.Lifecycle, bool, error) {
	s.intercept("lifecycle")
	return s.SQLiteStore.CreateSandboxDisabledExecution(ctx, execution, inputs, operation, ownerID, ttl)
}

func (s *sandboxCandidateTransactionBoundary) CreateSandboxDisabledPreflight(ctx context.Context,
	preflight sandbox.DisabledPreflight, operation sandbox.PreflightOperation,
) (sandbox.DisabledPreflight, bool, error) {
	s.intercept("preflight")
	return s.SQLiteStore.CreateSandboxDisabledPreflight(ctx, preflight, operation)
}

func (s *sandboxCandidateTransactionBoundary) CreateSandboxBackendEvidence(ctx context.Context,
	evidence sandbox.BackendEvidence, operation sandbox.BackendEvidenceOperation,
) (sandbox.BackendEvidence, bool, error) {
	s.intercept("evidence")
	return s.SQLiteStore.CreateSandboxBackendEvidence(ctx, evidence, operation)
}

func TestSandboxCandidateTransactionsRecheckCurrentUsageAndLease(t *testing.T) {
	for _, stage := range []string{"lifecycle", "preflight", "evidence"} {
		t.Run(stage, func(t *testing.T) {
			for _, change := range []string{"progress", "exhausted", "rollback", "quiescent", "revoked", "replaced", "cancelled"} {
				t.Run(change, func(t *testing.T) {
					ctx := t.Context()
					st, run, _ := openSandboxManifestStore(t, ctx)
					wrapped := &sandboxCandidateTransactionBoundary{SQLiteStore: st}
					service := application.NewSandboxManifestService(wrapped, policy.NewDefaultChecker())
					manifest := sandboxStoreTestManifest()
					manifest.Backend = sandbox.BackendDocker
					charge := func() {
						t.Helper()
						if _, err := st.ChargeToolCall(ctx, toolbudget.ChargeRequest{
							RunID: run.ID, SessionID: run.SessionID, WorkspaceID: "ws-sandbox-store",
							ToolName: "command_runtime", ActionClass: "process", RequestedBy: "boundary_operator",
						}); err != nil {
							t.Fatal(err)
						}
					}
					charge()
					prepared, err := service.Prepare(ctx, application.PrepareSandboxManifestRequest{
						RunID: run.ID, Manifest: manifest, OperationKey: "boundary-prepare", RequestedBy: "boundary_operator"})
					if err != nil {
						t.Fatal(err)
					}
					review, err := service.RequestApproval(ctx, prepared.Preparation.ID, "boundary_operator")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := service.ReviewApproval(ctx, prepared.Preparation.ID, approval.ActionApprove,
						"boundary-approve", "boundary_operator", ""); err != nil {
						t.Fatal(err)
					}
					validated, err := service.ValidateExecutionCandidate(ctx, application.ValidateSandboxExecutionCandidateRequest{
						PreparationID: prepared.Preparation.ID, Manifest: manifest, ApprovalID: review.ID,
						OperationKey: "boundary-candidate", RequestedBy: "boundary_operator"})
					if err != nil {
						t.Fatal(err)
					}
					candidate := validated.Candidate
					var lease domain.RunExecutionLease
					if change != "quiescent" {
						acquired, err := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
							RunID: run.ID, OwnerID: "boundary-native-owner", TTL: time.Minute})
						if err != nil {
							t.Fatal(err)
						}
						lease = acquired.Lease
						// Construct a separate native candidate through the real store API;
						// leave the original quiescent candidate and its snapshot immutable.
						candidate.ID, candidate.ValidatedAt = idgen.New("boundary-native-candidate"), time.Now().UTC()
						candidate.LeaseQuiescent = false
						candidate.RunLeaseID, candidate.RunLeaseGeneration, candidate.RunLeaseOwnerID = lease.LeaseID, lease.Generation, lease.OwnerID
						operation := sandbox.CandidateOperation{KeyDigest: runmutation.Fingerprint("boundary-native", candidate.ID),
							RequestFingerprint: sandbox.CandidateOperationRequestFingerprint(candidate), CandidateID: candidate.ID,
							PreparationID: candidate.PreparationID, RunID: run.ID, RequestedBy: candidate.RequestedBy, CreatedAt: candidate.ValidatedAt}
						stored, _, err := st.CreateSandboxExecutionCandidate(ctx, candidate, operation)
						if err != nil {
							t.Fatal(err)
						}
						candidate = stored.Candidate
					}
					if candidate.ToolCallsUsed != 1 {
						t.Fatalf("wrong initial candidate usage: %+v", candidate)
					}
					expectedUsage := int64(2)
					wrapped.stage = stage
					wrapped.before = func() {
						switch change {
						case "progress", "quiescent":
							charge()
						case "exhausted":
							for i := int64(1); i < run.Budget.MaxToolCalls; i++ {
								charge()
							}
							expectedUsage = run.Budget.MaxToolCalls
						case "rollback":
							// Corrupt only this disposable SQLite projection to prove the
							// immutable lower-bound snapshot catches a regressed counter.
							if _, err := st.db.ExecContext(ctx, `UPDATE run_tool_usage SET consumed=0 WHERE run_id=?`, run.ID); err != nil {
								t.Fatal(err)
							}
							expectedUsage = 0
						case "revoked", "replaced":
							charge()
							if _, _, err := st.ReleaseRunExecutionLease(ctx, lease); err != nil {
								t.Fatal(err)
							}
							if change == "replaced" {
								if _, err := st.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
									RunID: run.ID, OwnerID: "replacement-owner", TTL: time.Minute}); err != nil {
									t.Fatal(err)
								}
							}
						case "cancelled":
							charge()
							if _, err := application.NewRunService(st).Cancel(ctx, run.ID); err != nil {
								t.Fatal(err)
							}
						}
					}
					lifecycle, err := service.BeginDisabledExecution(ctx, application.BeginSandboxExecutionRequest{
						CandidateID: candidate.ID, Manifest: manifest, OperationKey: "boundary-begin-operation", RequestedBy: "boundary_operator"})
					if stage != "lifecycle" {
						if err != nil {
							t.Fatal(err)
						}
						var preflight sandbox.DisabledPreflight
						preflight, err = service.PrepareDisabledPreflight(ctx, application.PrepareSandboxPreflightRequest{
							ExecutionID: lifecycle.Execution.ID, Manifest: manifest, OperationKey: "boundary-preflight-operation", RequestedBy: "boundary_operator"})
						if stage == "evidence" {
							if err != nil {
								t.Fatal(err)
							}
							_, err = service.RecordSimulatedBackendEvidence(ctx, application.RecordSandboxBackendEvidenceRequest{
								PreflightID: preflight.ID, Manifest: manifest, ImageDigest: "sha256:" + strings.Repeat("c", 64),
								OperationKey: "boundary-evidence-operation", RequestedBy: "boundary_operator"})
						}
					}
					if wrapped.calls != 1 {
						t.Fatalf("did not reach exactly one real %s store transaction: calls=%d err=%v", stage, wrapped.calls, err)
					}
					if change == "progress" {
						if err != nil {
							t.Fatalf("transaction rejected still-budgeted live progress: %v", err)
						}
					} else if err == nil {
						t.Fatal("transaction accepted invalid current usage or authority")
					} else if change == "exhausted" && apperror.CodeOf(err) != apperror.CodeResourceExhausted {
						t.Fatalf("transaction did not enforce current budget: %v", err)
					} else if (change == "revoked" || change == "replaced") && !strings.Contains(err.Error(), "lease binding is stale") {
						t.Fatalf("transaction did not reach exact live lease revalidation: %v", err)
					}
					stored, readErr := st.GetSandboxExecutionCandidate(ctx, candidate.ID)
					usage, usageErr := st.GetToolCallUsage(ctx, run.ID)
					if readErr != nil || usageErr != nil || stored.Candidate.ToolCallsUsed != 1 || usage.Consumed != expectedUsage {
						t.Fatalf("transaction rewrote snapshot or accounting: candidate=%+v usage=%+v errors=%v,%v", stored, usage, readErr, usageErr)
					}
					table := map[string]string{"lifecycle": "sandbox_disabled_executions", "preflight": "sandbox_disabled_preflights", "evidence": "sandbox_backend_evidence"}[stage]
					var count int
					if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE run_id=?", run.ID).Scan(&count); err != nil {
						t.Fatal(err)
					}
					wantCount := 0
					if change == "progress" {
						wantCount = 1
					}
					if count != wantCount {
						t.Fatalf("transaction commit count=%d, want=%d", count, wantCount)
					}
				})
			}
		})
	}
}

func TestSandboxCandidateRunLeaseRequiresExactActiveBinding(t *testing.T) {
	now := time.Now().UTC()
	lease := domain.RunExecutionLease{RunID: "run-lease-bound", LeaseID: "lease-exact",
		OwnerID: "command-runtime-owner", Generation: 7,
		Status: domain.RunExecutionLeaseActive, AcquiredAt: now.Add(-time.Minute),
		RenewedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
	candidate := sandbox.ExecutionCandidate{RunID: lease.RunID, LeaseQuiescent: false,
		RunLeaseID: lease.LeaseID, RunLeaseGeneration: lease.Generation,
		RunLeaseOwnerID: lease.OwnerID}
	if err := validateSandboxCandidateRunLease(candidate, lease, true, now,
		"expected a quiescent Run"); err != nil {
		t.Fatalf("exact active lease was rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.RunExecutionLease){
		"lease id":   func(value *domain.RunExecutionLease) { value.LeaseID = "lease-drifted" },
		"generation": func(value *domain.RunExecutionLease) { value.Generation++ },
		"owner":      func(value *domain.RunExecutionLease) { value.OwnerID = "different-owner" },
		"run":        func(value *domain.RunExecutionLease) { value.RunID = "different-run" },
		"expiry":     func(value *domain.RunExecutionLease) { value.ExpiresAt = now },
	} {
		t.Run(name, func(t *testing.T) {
			drifted := lease
			mutate(&drifted)
			if err := validateSandboxCandidateRunLease(candidate, drifted, true, now,
				"expected a quiescent Run"); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("drift error=%v, want conflict", err)
			}
		})
	}
	if err := validateSandboxCandidateRunLease(candidate, domain.RunExecutionLease{},
		false, now, "expected a quiescent Run"); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("missing lease error=%v, want conflict", err)
	}
	quiescent := candidate
	quiescent.LeaseQuiescent = true
	quiescent.RunLeaseID, quiescent.RunLeaseOwnerID = "", ""
	quiescent.RunLeaseGeneration = 0
	if err := validateSandboxCandidateRunLease(quiescent, lease, true, now,
		"expected a quiescent Run"); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("quiescent candidate active-lease error=%v", err)
	}
}

func TestSandboxCandidateCurrentBudgetRetainsSnapshotAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		quiescent             bool
		tokens, millis, calls int64
		want                  string
	}{
		{name: "unchanged", tokens: 10, millis: 1000, calls: 1},
		{name: "background progress", tokens: 11, millis: 1001, calls: 2},
		{name: "last remaining capacity", tokens: 99, millis: 9999, calls: 3},
		{name: "quiescent unchanged", quiescent: true, tokens: 10, millis: 1000, calls: 1},
		{name: "quiescent token drift", quiescent: true, tokens: 11, millis: 1000, calls: 1, want: "conflict"},
		{name: "quiescent time drift", quiescent: true, tokens: 10, millis: 1001, calls: 1, want: "conflict"},
		{name: "quiescent tool drift", quiescent: true, tokens: 10, millis: 1000, calls: 2, want: "conflict"},
		{name: "token rollback", tokens: 9, millis: 1001, calls: 2, want: "conflict"},
		{name: "time rollback", tokens: 11, millis: 999, calls: 2, want: "conflict"},
		{name: "tool rollback", tokens: 11, millis: 1001, calls: 0, want: "conflict"},
		{name: "token limit", tokens: 100, millis: 1001, calls: 2, want: "exhausted"},
		{name: "time limit", tokens: 11, millis: 10000, calls: 2, want: "exhausted"},
		{name: "tool limit", tokens: 11, millis: 1001, calls: 4, want: "exhausted"},
		{name: "over all limits", tokens: 101, millis: 10001, calls: 5, want: "exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := sandbox.ExecutionCandidate{TokensUsed: 10, ExecutionMillisUsed: 1000,
				ToolCallsUsed: 1, LeaseQuiescent: tc.quiescent}
			err := requireSandboxCandidateStoreCurrentBudget(candidate,
				domain.Budget{MaxTokens: 100, TimeoutSeconds: 10, MaxToolCalls: 4},
				domain.RunAgentUsage{TotalTokens: tc.tokens, TotalExecutionMillis: tc.millis}, tc.calls)
			switch tc.want {
			case "":
				if err != nil {
					t.Fatal(err)
				}
			case "conflict":
				if apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("counter snapshot drift was accepted: %v", err)
				}
			case "exhausted":
				if apperror.CodeOf(err) != apperror.CodeResourceExhausted {
					t.Fatalf("current budget exhaustion was accepted: %v", err)
				}
			}
		})
	}
}

func TestSandboxExecutionCandidateConcurrentReplayAndImmutability(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "candidate.db")
	st1, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st1.Close() })
	root := t.TempDir()
	if err := st1.SaveWorkspace(ctx, WorkspaceRecord{
		ID: "ws-sandbox-candidate", Name: "sandbox-candidate", RootPath: root,
	}); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st1).Create(ctx, application.CreateRunRequest{
		Goal: "validate a disabled execution candidate", Profile: "code",
		WorkspaceID: "ws-sandbox-candidate",
		Budget:      domain.Budget{MaxTurns: 4, MaxToolCalls: 4, MaxTokens: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := application.NewSandboxManifestService(st1, policy.NewDefaultChecker()).Prepare(ctx,
		application.PrepareSandboxManifestRequest{
			RunID: run.ID, Manifest: sandboxStoreTestManifest(),
			OperationKey: "candidate-concurrent-prepare", RequestedBy: "candidate_store_test",
		})
	if err != nil {
		t.Fatal(err)
	}
	st2, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	services := []*application.SandboxManifestService{
		application.NewSandboxManifestService(st1, policy.NewDefaultChecker()),
		application.NewSandboxManifestService(st2, policy.NewDefaultChecker()),
	}
	request := application.ValidateSandboxExecutionCandidateRequest{
		PreparationID: prepared.Preparation.ID, Manifest: sandboxStoreTestManifest(),
		OperationKey: "candidate-concurrent-validate", RequestedBy: "candidate_store_test",
	}
	start := make(chan struct{})
	results := make([]sandbox.ValidatedExecutionCandidate, len(services))
	errorsFound := make([]error, len(services))
	var group sync.WaitGroup
	for index := range services {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			results[index], errorsFound[index] = services[index].ValidateExecutionCandidate(ctx, request)
		}(index)
	}
	close(start)
	group.Wait()
	if errorsFound[0] != nil || errorsFound[1] != nil ||
		results[0].Candidate.ID != results[1].Candidate.ID {
		t.Fatalf("concurrent candidate validation diverged: results=%#v errors=%v", results, errorsFound)
	}
	if results[0].Replayed == results[1].Replayed {
		t.Fatalf("expected one candidate create and one replay: %#v", results)
	}
	candidateID := results[0].Candidate.ID
	if _, err := st1.db.ExecContext(ctx, `UPDATE sandbox_execution_candidates
		SET execution_authorized = 1 WHERE id = ?`, candidateID); err == nil {
		t.Fatal("sandbox execution candidate was mutable or could authorize execution")
	}
	values, err := st1.ListSandboxExecutionCandidates(ctx, run.ID, 100)
	if err != nil || len(values) != 1 || values[0].Candidate.ExecutionAuthorized ||
		values[0].Candidate.BackendEnabled {
		t.Fatalf("stored candidate projection is invalid: %#v err=%v", values, err)
	}
	rows, err := st1.db.QueryContext(ctx, `PRAGMA table_info(sandbox_execution_candidates)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		switch name {
		case "manifest_json", "command", "arguments_json", "mount_sources_json",
			"workspace_root", "environment_json", "secret_references_json":
			t.Fatalf("schema v49 candidate persists raw intent in column %q", name)
		}
	}
	eventsFound, err := st1.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range eventsFound {
		if strings.Contains(event.PayloadJSON, root) || strings.Contains(event.PayloadJSON, `"executable"`) {
			t.Fatalf("candidate event leaked raw workspace or command data: %#v", event)
		}
	}
}

func TestSchemaV48UpgradeAddsSandboxExecutionCandidates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v48.db")
	st, run, _ := openSandboxManifestStoreAt(t, ctx, filepath.Join(t.TempDir(), "seed.db"), 177)
	prepared, err := application.NewSandboxManifestService(st, policy.NewDefaultChecker()).Prepare(ctx,
		application.PrepareSandboxManifestRequest{
			RunID: run.ID, Manifest: sandboxStoreTestManifest(),
			OperationKey: "candidate-v48-prepare", RequestedBy: "upgrade_test",
		})
	if err != nil {
		t.Fatal(err)
	}
	st = historicalTestDatabaseFromSeed(t, st, path, 48)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if version, err := st.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema v48 did not upgrade to v49: version=%d err=%v", version, err)
	}
	loaded, err := st.GetSandboxManifestIntent(ctx, prepared.Preparation.ID)
	if err != nil || loaded.Preparation.ID != prepared.Preparation.ID {
		t.Fatalf("schema v48 preparation was not preserved: %#v err=%v", loaded, err)
	}
	var table string
	if err := st.db.QueryRowContext(ctx, `SELECT name FROM sqlite_master
		WHERE type = 'table' AND name = 'sandbox_execution_candidates'`).Scan(&table); err != nil ||
		table != "sandbox_execution_candidates" {
		t.Fatalf("schema v49 candidate ledger is missing: %q err=%v", table, err)
	}
}

func TestSandboxApprovalRequestConcurrentReplayAcrossStores(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "approval-race.db")
	st1, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st1.Close() })
	root := t.TempDir()
	if err := st1.SaveWorkspace(ctx, WorkspaceRecord{
		ID: "ws-sandbox-approval-race", Name: "sandbox-approval-race", RootPath: root,
	}); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st1).Create(ctx, application.CreateRunRequest{
		Goal: "converge sandbox approval request", Profile: "code",
		WorkspaceID: "ws-sandbox-approval-race",
		Budget:      domain.Budget{MaxTurns: 4, MaxToolCalls: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := sandboxStoreTestManifest()
	manifest.Mounts[0].Access = sandbox.MountReadWrite
	prepared, err := application.NewSandboxManifestService(st1, policy.NewDefaultChecker()).Prepare(ctx,
		application.PrepareSandboxManifestRequest{
			RunID: run.ID, Manifest: manifest, OperationKey: "approval-race-prepare",
			RequestedBy: "approval_race_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	st2, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	services := []*application.SandboxManifestService{
		application.NewSandboxManifestService(st1, policy.NewDefaultChecker()),
		application.NewSandboxManifestService(st2, policy.NewDefaultChecker()),
	}
	start := make(chan struct{})
	ids := make([]string, len(services))
	errorsFound := make([]error, len(services))
	var group sync.WaitGroup
	for index := range services {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			record, requestErr := services[index].RequestApproval(ctx,
				prepared.Preparation.ID, "approval_race_operator")
			ids[index], errorsFound[index] = record.ID, requestErr
		}(index)
	}
	close(start)
	group.Wait()
	if errorsFound[0] != nil || errorsFound[1] != nil || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("concurrent sandbox approval requests diverged: ids=%v errors=%v", ids, errorsFound)
	}
}
