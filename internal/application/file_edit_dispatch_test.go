package application_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/tools"
)

type fileDispatchStore struct {
	*store.SQLiteStore
	beforeMutation func() error
}

func (s *fileDispatchStore) SaveFileEdit(ctx context.Context, edit fileedit.Edit) (fileedit.Edit, error) {
	saved, err := s.SQLiteStore.SaveFileEdit(ctx, edit)
	if err == nil && edit.Status == fileedit.StatusApproved && s.beforeMutation != nil {
		hook := s.beforeMutation
		s.beforeMutation = nil
		err = hook()
	}
	return saved, err
}

type fileDispatchPolicy struct {
	root                           string
	denied, denyStaged, sawStaging bool
}

func (c *fileDispatchPolicy) CheckText(string, string) policy.Decision {
	return policy.Decision{Allowed: true}
}

func (c *fileDispatchPolicy) CheckToolCall(tools.Call) policy.Decision {
	if c.denyStaged {
		paths, _ := filepath.Glob(filepath.Join(c.root, ".cyberagent-edit-*"))
		if len(paths) > 0 {
			c.sawStaging = true
			c.denied = true
		}
	}
	return policy.Decision{Allowed: !c.denied, Reason: "fixture live policy"}
}

func TestFileEditCommonAuthorityStopsRealMutationsAtDispatch(t *testing.T) {
	for _, test := range []struct{ name, operation, boundary string }{
		{"create revoked before directory or staging write", fileedit.OperationCreate, "revoke"},
		{"replace revoked after staging before publication", fileedit.OperationReplace, "staged"},
		{"delete revoked before removal", fileedit.OperationDelete, "revoke"},
		{"delete cancelled before removal", fileedit.OperationDelete, "cancel"},
		{"delete paused before removal", fileedit.OperationDelete, "pause"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			home := t.TempDir()
			root := filepath.Join(home, "workspace")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			state, err := store.Open(filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			workspace := store.WorkspaceRecord{ID: "workspace-dispatch", Name: "dispatch", RootPath: root}
			if err := state.SaveWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			runs := application.NewRunService(state)
			_, created, err := runs.Create(ctx, application.CreateRunRequest{Goal: "verify file dispatch", Profile: "code",
				WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 4}})
			if err != nil {
				t.Fatal(err)
			}
			run, err := runs.Start(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			path := "target.txt"
			expected := "missing"
			if test.operation == fileedit.OperationCreate {
				path = "new-parent/target.txt"
			} else {
				if err := os.WriteFile(filepath.Join(root, path), []byte("original\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				expected, err = fileedit.CurrentHash(root, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			proposal := fileedit.Proposal{SessionID: run.SessionID, WorkspaceID: workspace.ID,
				WorkspaceRoot: root, Path: path, Operation: test.operation, ExpectedOriginalHash: expected}
			if test.operation != fileedit.OperationDelete {
				proposal.ProposedText = "proposed\n"
			}
			edit, err := fileedit.NewManager(state).Propose(ctx, proposal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := application.NewFileEditReviewService(state).Review(ctx, application.ReviewFileEditRequest{
				Version: application.FileEditReviewProtocolVersion, RunID: run.ID, EditID: edit.ID,
				Action: application.FileEditApproveIntent}); err != nil {
				t.Fatal(err)
			}
			checker := &fileDispatchPolicy{root: root, denyStaged: test.boundary == "staged"}
			wrapped := &fileDispatchStore{SQLiteStore: state}
			wrapped.beforeMutation = func() error {
				switch test.boundary {
				case "revoke":
					checker.denied = true
				case "cancel":
					cancel()
				case "pause":
					_, err := runs.Pause(context.Background(), run.ID)
					return err
				}
				return nil
			}
			_, err = application.NewFileEditApplyService(wrapped, checker).Apply(ctx, application.ApplyFileEditRequest{
				Version: fileedit.FileEditApplyProtocolVersion, RunID: run.ID, EditID: edit.ID,
				OperationKey: "file-dispatch-boundary-test-01", AppliedBy: "test_operator"})
			if err == nil {
				t.Fatal("revoked or cancelled operation succeeded")
			}
			if test.boundary == "staged" && !checker.sawStaging {
				t.Fatalf("did not reach publication boundary: %v", err)
			}
			if wrapped.beforeMutation != nil {
				t.Fatalf("did not reach manager mutation boundary: %v", err)
			}
			if test.operation == fileedit.OperationCreate {
				if _, err := os.Stat(filepath.Join(root, "new-parent")); !os.IsNotExist(err) {
					t.Fatalf("denied create changed directories: %v", err)
				}
			} else {
				data, readErr := os.ReadFile(filepath.Join(root, path))
				if readErr != nil || string(data) != "original\n" {
					t.Fatalf("denied operation changed target=%q err=%v", data, readErr)
				}
			}
			staging, err := filepath.Glob(filepath.Join(root, ".cyberagent-edit-*"))
			if err != nil || len(staging) != 0 {
				t.Fatalf("staging files=%v err=%v", staging, err)
			}
		})
	}
}
