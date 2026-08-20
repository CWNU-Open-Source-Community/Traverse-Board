package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestValidateEvidenceInvalidatesBranchAndDirtyStateChanges(t *testing.T) {
	root := testWorkspace(t)
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("initial", &git.CommitOptions{Author: &object.Signature{
		Name: "code-intel-test", Email: "code-intel@example.invalid",
		When: time.Unix(1, 0).UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	manager := testManager(t, root, "normal", 2*time.Second)
	snapshot := manager.Capabilities(context.Background(), helperWorkspaceID, root)[0]
	result, err := manager.Execute(context.Background(), semanticRequest(root, snapshot,
		ToolHover, "main.go", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if validation := manager.ValidateEvidence(context.Background(), root,
		result.Provenance); validation.State != EvidenceCurrent {
		t.Fatalf("fresh Git-bound evidence was not current: %#v", validation)
	}

	head, err := repository.Head()
	if err != nil {
		t.Fatal(err)
	}
	feature := plumbing.NewBranchReferenceName("code-intel-feature")
	if err := repository.Storer.SetReference(plumbing.NewHashReference(feature,
		head.Hash())); err != nil {
		t.Fatal(err)
	}
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: feature}); err != nil {
		t.Fatal(err)
	}
	if validation := manager.ValidateEvidence(context.Background(), root,
		result.Provenance); validation.State != EvidenceStale {
		t.Fatalf("branch change did not stale semantic evidence: %#v", validation)
	}
	branchSnapshot := manager.Capabilities(context.Background(), helperWorkspaceID, root)[0]
	if branchSnapshot.Generation == snapshot.Generation {
		t.Fatal("branch change reused the old language-server generation")
	}

	branchResult, err := manager.Execute(context.Background(), semanticRequest(root, branchSnapshot,
		ToolHover, "main.go", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\nfunc Main() { Helper() }\nfunc Helper() { println(\"dirty\") }\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	if validation := manager.ValidateEvidence(context.Background(), root,
		branchResult.Provenance); validation.State != EvidenceStale {
		t.Fatalf("dirty state change did not stale semantic evidence: %#v", validation)
	}
	if _, err := manager.Execute(context.Background(), semanticRequest(root, branchSnapshot,
		ToolWorkspaceSymbols, "", "Main", "")); err == nil ||
		apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatalf("request pinned to the pre-edit Server generation was accepted: %v", err)
	}
	dirtySnapshot := manager.Capabilities(context.Background(), helperWorkspaceID, root)[0]
	if dirtySnapshot.Generation == branchSnapshot.Generation {
		t.Fatal("dirty-state change reused the old language-server generation")
	}

	dirtyResult, err := manager.Execute(context.Background(), semanticRequest(root, dirtySnapshot,
		ToolWorkspaceSymbols, "", "Main", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\nfunc Main() { Helper() }\nfunc Helper() { println(\"dirty-again\") }\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	if validation := manager.ValidateEvidence(context.Background(), root,
		dirtyResult.Provenance); validation.State != EvidenceStale {
		t.Fatalf("content change within an already-dirty status did not stale workspace evidence: %#v",
			validation)
	}
}
