package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/store"
)

var threadGitApprovalModes = []domain.RunExecutionPermissionMode{
	domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull,
}

func reviewNativeThreadGit(t *testing.T, svc *ThreadGitService, threadID, runID, key string, spec ThreadGitSpec) ThreadGitExecuteRequest {
	t.Helper()
	preview, err := svc.Preview(t.Context(), threadID, ThreadGitPreviewRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: spec})
	if err != nil || !preview.CanExecute {
		t.Fatalf("review %s: executable=%v reason=%s err=%v", spec.Operation, preview.CanExecute, preview.BlockedReason, err)
	}
	return ThreadGitExecuteRequest{Version: ThreadGitProtocolVersion, RunID: runID, Spec: spec,
		OperationKey: key, ExpectedPreviewFingerprint: preview.PreviewFingerprint, RequestedBy: "native-operator"}
}

func executeNativeThreadGit(t *testing.T, svc *ThreadGitService, state *store.SQLiteStore, threadID, runID string, spec ThreadGitSpec) ThreadGitResult {
	t.Helper()
	request := reviewNativeThreadGit(t, svc, threadID, runID, "native-"+spec.Operation, spec)
	result, err := svc.Execute(t.Context(), threadID, request)
	if err != nil || result.State != "completed" || !result.ReceiptSaved {
		t.Fatalf("execute %s: result=%+v err=%v", spec.Operation, result, err)
	}
	proof, err := state.GetApprovalByProposal(t.Context(), result.OperationID)
	if err != nil || proof.Status != approval.StatusApproved || proof.RunID != runID || proof.WorkspaceID != result.WorkspaceID ||
		proof.ToolName != "thread.git" || proof.ActionClass != "git_write" || proof.Mode != "per_call" || proof.GrantID != "" {
		t.Fatalf("missing exact native consent for %s: %+v err=%v", spec.Operation, proof, err)
	}
	return result
}

func TestThreadGitApprovalModesExecuteExactNativeOperations(t *testing.T) {
	for _, mode := range threadGitApprovalModes {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			svc, state, threadID, runID, root := threadGitApplicationFixture(t, mode)
			if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("approved bytes\n"), 0600); err != nil {
				t.Fatal(err)
			}
			selected := []string{"selected.txt"}
			executeNativeThreadGit(t, svc, state, threadID, runID, ThreadGitSpec{Operation: "stage", Paths: selected})
			if got := runFixtureGit(t, "-C", root, "show", ":selected.txt"); got != "approved bytes" {
				t.Fatalf("stage content=%q", got)
			}
			executeNativeThreadGit(t, svc, state, threadID, runID, ThreadGitSpec{Operation: "unstage", Paths: selected})
			if got := runFixtureGit(t, "-C", root, "diff", "--cached", "--name-only"); got != "" {
				t.Fatalf("unstage left entries: %q", got)
			}
			commit := executeNativeThreadGit(t, svc, state, threadID, runID, ThreadGitSpec{Operation: "commit", Paths: selected, Message: "exact native commit"})
			if got := runFixtureGit(t, "-C", root, "show", "HEAD:selected.txt"); got != "approved bytes" {
				t.Fatalf("commit content=%q", got)
			}
			executeNativeThreadGit(t, svc, state, threadID, runID, ThreadGitSpec{Operation: "create_branch", Branch: "confirmed"})
			executeNativeThreadGit(t, svc, state, threadID, runID, ThreadGitSpec{Operation: "switch_branch", Branch: "confirmed"})
			if got := runFixtureGit(t, "-C", root, "symbolic-ref", "--short", "HEAD"); got != "confirmed" {
				t.Fatalf("switch branch=%q", got)
			}
			bare := filepath.Join(t.TempDir(), "remote.git")
			runFixtureGit(t, "init", "--bare", "-q", bare)
			push := executeNativeThreadGit(t, svc, state, threadID, runID, ThreadGitSpec{Operation: "push_branch", Branch: "confirmed", RemoteURL: "file:///" + filepath.ToSlash(bare)})
			if got := runFixtureGit(t, "--git-dir", bare, "rev-parse", "refs/heads/confirmed"); got != commit.CommitOID || push.RemoteOID != commit.CommitOID {
				t.Fatalf("remote did not receive exact commit: %s %+v", got, push)
			}
		})
	}
}

type threadGitBoundaryStore struct {
	ThreadGitStore
	afterClaim     func()
	rejectApproval bool
	recordPrepared func(context.Context, string, string, string, domain.RunExecutionLease) error
}

func (s *threadGitBoundaryStore) StartGitMutationOperation(ctx context.Context, id, fingerprint string, at time.Time) (gitmutation.Record, bool, error) {
	row, claimed, err := s.ThreadGitStore.StartGitMutationOperation(ctx, id, fingerprint, at)
	if err == nil && claimed && s.afterClaim != nil {
		s.afterClaim()
	}
	return row, claimed, err
}

func (s *threadGitBoundaryStore) StartRemoteOperation(ctx context.Context, id, fingerprint string, at time.Time) (gitmutation.RemoteRecord, bool, error) {
	row, claimed, err := s.ThreadGitStore.StartRemoteOperation(ctx, id, fingerprint, at)
	if err == nil && claimed && s.afterClaim != nil {
		s.afterClaim()
	}
	return row, claimed, err
}

func (s *threadGitBoundaryStore) EnsureApproval(ctx context.Context, proposal approval.Proposal) (approval.Record, error) {
	if s.rejectApproval {
		return approval.Record{}, errors.New("fixture approval persistence failure")
	}
	return s.ThreadGitStore.EnsureApproval(ctx, proposal)
}

func (s *threadGitBoundaryStore) RecordThreadGitPreparation(ctx context.Context, id, fingerprint, raw string, lease domain.RunExecutionLease) error {
	if s.recordPrepared != nil {
		return s.recordPrepared(ctx, id, fingerprint, raw, lease)
	}
	return s.ThreadGitStore.RecordThreadGitPreparation(ctx, id, fingerprint, raw, lease)
}

type threadGitDiskSnapshot struct {
	head, index string
	objects     map[string]string
}

func captureThreadGitDisk(t *testing.T, root string) threadGitDiskSnapshot {
	t.Helper()
	result := threadGitDiskSnapshot{head: runFixtureGit(t, "-C", root, "rev-parse", "HEAD"), objects: map[string]string{}}
	index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	result.index = string(index)
	objectRoot := filepath.Join(root, ".git", "objects")
	err = filepath.WalkDir(objectRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		result.objects[path] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestThreadGitApprovalModesRevokeAfterClaimBeforeGitEffects(t *testing.T) {
	for _, mode := range threadGitApprovalModes {
		for _, operation := range []string{"commit", "push_branch"} {
			t.Run(string(mode)+"/"+operation, func(t *testing.T) {
				t.Parallel()
				svc, state, threadID, runID, root := threadGitApplicationFixture(t, mode)
				spec := ThreadGitSpec{Operation: "commit", Paths: []string{"selected.txt"}, Message: "must not prepare"}
				if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("unpublished\n"), 0600); err != nil {
					t.Fatal(err)
				}
				bare := ""
				if operation == "push_branch" {
					bare = filepath.Join(t.TempDir(), "remote.git")
					runFixtureGit(t, "init", "--bare", "-q", bare)
					spec = ThreadGitSpec{Operation: operation, Branch: "denied", RemoteURL: "file:///" + filepath.ToSlash(bare)}
				}
				request := reviewNativeThreadGit(t, svc, threadID, runID, "revoke-after-claim", spec)
				before := captureThreadGitDisk(t, root)
				svc.store = &threadGitBoundaryStore{ThreadGitStore: state, afterClaim: func() { svc.capabilities.RuntimeAuthority.RevokeRun(runID) }}
				result, err := svc.Execute(t.Context(), threadID, request)
				if err == nil && result.State != "unknown" {
					t.Fatalf("revoked operation completed: %+v", result)
				}
				key := threadGitKey(threadID, request.OperationKey)
				if operation == "commit" {
					row, found, err := state.GetGitMutationByKey(t.Context(), key)
					if err != nil || !found || row.StartedAt == nil || row.CompletedAt != nil {
						t.Fatalf("claim evidence: %+v found=%v err=%v", row, found, err)
					}
				} else {
					row, found, err := state.GetGitRemoteByKey(t.Context(), key)
					if err != nil || !found || row.StartedAt == nil || row.CompletedAt != nil {
						t.Fatalf("remote claim: %+v found=%v err=%v", row, found, err)
					}
					if got := runFixtureGit(t, "--git-dir", bare, "for-each-ref", "refs/heads"); got != "" {
						t.Fatalf("revoked push created a remote ref: %s", got)
					}
				}
				svc.store = state
				beforeEvents, _ := state.ListRunEvents(t.Context(), runID)
				for i := 0; i < 2; i++ {
					observed, err := svc.Execute(t.Context(), threadID, request)
					if err != nil || observed.State != "unknown" || !observed.Replayed {
						t.Fatalf("revoked replay: %+v err=%v", observed, err)
					}
				}
				afterEvents, _ := state.ListRunEvents(t.Context(), runID)
				if len(afterEvents) != len(beforeEvents) || !reflect.DeepEqual(before, captureThreadGitDisk(t, root)) {
					t.Fatal("revoked preparation or replay changed Git objects, index, HEAD or history")
				}
				if _, found := svc.capabilities.RuntimeAuthority.RunAuthorizationFence(runID); found {
					t.Fatal("revoked replay recreated authority")
				}
			})
		}
	}
}

func TestThreadGitPreparationFailureAndRevocationNeverPublish(t *testing.T) {
	for _, boundary := range []string{"approval", "preparation_receipt", "revoke_after_receipt", "cancel_after_receipt"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			svc, state, threadID, runID, root := threadGitApplicationFixture(t)
			if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("prepared but unpublished\n"), 0600); err != nil {
				t.Fatal(err)
			}
			request := reviewNativeThreadGit(t, svc, threadID, runID, "stop-at-boundary", ThreadGitSpec{Operation: "commit", Paths: []string{"selected.txt"}, Message: "no publication"})
			before := captureThreadGitDisk(t, root)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wrapped := &threadGitBoundaryStore{ThreadGitStore: state, rejectApproval: boundary == "approval"}
			wrapped.recordPrepared = func(ctx context.Context, id, fingerprint, raw string, lease domain.RunExecutionLease) error {
				if boundary == "preparation_receipt" {
					return errors.New("fixture loses prepared metadata before publication")
				}
				if err := state.RecordThreadGitPreparation(ctx, id, fingerprint, raw, lease); err != nil {
					return err
				}
				if boundary == "revoke_after_receipt" {
					svc.capabilities.RuntimeAuthority.RevokeRun(runID)
				} else {
					cancel()
				}
				return nil
			}
			svc.store = wrapped
			result, err := svc.Execute(ctx, threadID, request)
			if err == nil && result.State != "unknown" {
				t.Fatalf("blocked publication completed: %+v", result)
			}
			after := captureThreadGitDisk(t, root)
			if before.head != after.head || before.index != after.index {
				t.Fatal("failed approval/preparation changed branch or index")
			}
			if boundary == "approval" && !reflect.DeepEqual(before.objects, after.objects) {
				t.Fatal("Git objects were written before durable approval")
			}
			if _, err := os.Stat(filepath.Join(root, ".git", "index.lock")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed preparation retained index lock: %v", err)
			}
			svc.store = state
			beforeEvents, _ := state.ListRunEvents(t.Context(), runID)
			for i := 0; i < 2; i++ {
				observed, err := svc.Execute(t.Context(), threadID, request)
				if err != nil || observed.State != "unknown" || !observed.Replayed {
					t.Fatalf("interrupted replay: %+v err=%v", observed, err)
				}
			}
			afterEvents, _ := state.ListRunEvents(t.Context(), runID)
			if len(beforeEvents) != len(afterEvents) || !reflect.DeepEqual(after, captureThreadGitDisk(t, root)) {
				t.Fatal("interrupted recovery changed Git or durable history")
			}
		})
	}
}

func TestThreadGitApprovalModesRejectColdAndRevokedReview(t *testing.T) {
	for _, mode := range threadGitApprovalModes {
		for _, cold := range []bool{false, true} {
			name := string(mode) + "/revoked"
			if cold {
				name = string(mode) + "/cold"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				svc, state, threadID, runID, root := threadGitApplicationFixture(t, mode)
				request := reviewNativeThreadGit(t, svc, threadID, runID, "cold-review", ThreadGitSpec{Operation: "create_branch", Branch: "blocked"})
				if cold {
					svc.capabilities.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
				} else {
					svc.capabilities.RuntimeAuthority.RevokeRun(runID)
				}
				if _, err := svc.Execute(t.Context(), threadID, request); err == nil {
					t.Fatal("old review dispatched after runtime authority disappeared")
				}
				if _, found, err := state.GetGitMutationByKey(t.Context(), threadGitKey(threadID, request.OperationKey)); err != nil || found {
					t.Fatalf("cold/revoked review created intent: %v %v", found, err)
				}
				if got := runFixtureGit(t, "-C", root, "for-each-ref", "refs/heads/blocked"); got != "" {
					t.Fatalf("branch created: %s", got)
				}
				if _, found := svc.capabilities.RuntimeAuthority.RunAuthorizationFence(runID); found {
					t.Fatal("execution reissued a fence")
				}
			})
		}
	}
}

func TestThreadGitPreparedReceiptRequiresExactClaimLeaseAndIsImmutable(t *testing.T) {
	svc, state, threadID, runID, root := threadGitApplicationFixture(t)
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("receipt-bound\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := reviewNativeThreadGit(t, svc, threadID, runID, "prepared-receipt", ThreadGitSpec{Operation: "commit", Paths: []string{"selected.txt"}, Message: "receipt validation"})
	var savedLease domain.RunExecutionLease
	var savedID, savedFingerprint, savedRaw string
	svc.store = &threadGitBoundaryStore{ThreadGitStore: state, recordPrepared: func(ctx context.Context, id, fingerprint, raw string, lease domain.RunExecutionLease) error {
		wrongLease := lease
		wrongLease.OwnerID += "-other"
		if err := state.RecordThreadGitPreparation(ctx, id, fingerprint, raw, wrongLease); err == nil {
			t.Fatal("receipt accepted another lease owner")
		}
		if err := state.RecordThreadGitPreparation(ctx, id, strings.Repeat("f", 64), raw, lease); err == nil {
			t.Fatal("receipt accepted another intent")
		}
		if err := state.RecordThreadGitPreparation(ctx, id, fingerprint, raw, lease); err != nil {
			return err
		}
		before, _ := state.ListRunEvents(ctx, runID)
		if err := state.RecordThreadGitPreparation(ctx, id, fingerprint, raw, lease); err != nil {
			return err
		}
		if err := state.RecordThreadGitPreparation(ctx, id, fingerprint, `{"commit_oid":"changed"}`, lease); err == nil {
			t.Fatal("immutable preparation changed")
		}
		after, _ := state.ListRunEvents(ctx, runID)
		if len(before) != len(after) {
			t.Fatal("duplicate receipt wrote another event")
		}
		savedLease, savedID, savedFingerprint, savedRaw = lease, id, fingerprint, raw
		return nil
	}}
	result, err := svc.Execute(t.Context(), threadID, request)
	if err != nil || result.State != "completed" || savedID == "" {
		t.Fatalf("receipt execute=%+v err=%v", result, err)
	}
	if err := state.RecordThreadGitPreparation(t.Context(), savedID, savedFingerprint, savedRaw, savedLease); err == nil {
		t.Fatal("completed operation accepted a preparation write")
	}
}
