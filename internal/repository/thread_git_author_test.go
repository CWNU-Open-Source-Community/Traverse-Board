package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func isolateThreadGitIdentity(t *testing.T, root string, config string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	path := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	threadGitTestCommand(t, root, "config", "--local", "--unset-all", "user.name")
	threadGitTestCommand(t, root, "config", "--local", "--unset-all", "user.email")
	return path
}

func TestThreadGitAuthorGlobalOnlyProducesExactUnsignedCommit(t *testing.T) {
	root := threadGitFixture(t)
	isolateThreadGitIdentity(t, root, "[user]\nname = 全局作者\nemail = global@example.invalid\n[commit]\ngpgSign = true\n[gpg]\nprogram = nonexistent-signing-program\n")
	executor, _ := NewMutationExecutor()
	author, err := executor.ReadCommitAuthor(t.Context(), root)
	if err != nil || author.Name != "全局作者" || author.Email != "global@example.invalid" {
		t.Fatalf("author=%#v %v", author, err)
	}
	if err = os.WriteFile(filepath.Join(root, "chosen.txt"), []byte("global author content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := executor.ReviewSelected(t.Context(), root, []string{"chosen.txt"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := executor.PrepareSelectedCommit(t.Context(), root, review, "reviewed global identity", strings.Repeat("f", 64), author)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, err := executor.threadGit(t.Context(), root, "", nil, "cat-file", "commit", p.CommitOID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "\nauthor 全局作者 <global@example.invalid> ") || !strings.Contains(body, "\ncommitter 全局作者 <global@example.invalid> ") || strings.Contains(body, "\ngpgsig ") {
		t.Fatalf("unexpected signed/guessed identity: %s", body)
	}
}

func TestThreadGitAuthorIncludesLocalPrecedenceAndMissingIdentity(t *testing.T) {
	root := threadGitFixture(t)
	global := isolateThreadGitIdentity(t, root, "[user]\nname = Default Author\nemail = default@example.invalid\n")
	included := filepath.Join(filepath.Dir(global), "included.gitconfig")
	if err := os.WriteFile(included, []byte("[user]\nname = Conditional Author\nemail = conditional@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := "[user]\nname = Default Author\nemail = default@example.invalid\n[includeIf \"gitdir/i:" + filepath.ToSlash(filepath.Join(root, ".git")) + "\"]\npath = " + filepath.ToSlash(included) + "\n"
	if err := os.WriteFile(global, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	executor, _ := NewMutationExecutor()
	author, err := executor.ReadCommitAuthor(t.Context(), root)
	if err != nil || author.Name != "Conditional Author" || author.Email != "conditional@example.invalid" {
		t.Fatalf("includeIf not honored: %#v %v", author, err)
	}
	threadGitTestCommand(t, root, "config", "--local", "user.name", "Local Author")
	threadGitTestCommand(t, root, "config", "--local", "user.email", "local@example.invalid")
	author, err = executor.ReadCommitAuthor(t.Context(), root)
	if err != nil || author.Name != "Local Author" || author.Email != "local@example.invalid" {
		t.Fatalf("local precedence lost: %#v %v", author, err)
	}
	threadGitTestCommand(t, root, "config", "--local", "--unset-all", "user.name")
	threadGitTestCommand(t, root, "config", "--local", "--unset-all", "user.email")
	if err := os.WriteFile(global, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ReadCommitAuthor(t.Context(), root); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("missing identity guessed: %v", err)
	}
	localInclude := filepath.Join(filepath.Dir(global), "local-identity.gitconfig")
	if err := os.WriteFile(localInclude, []byte("[user]\nname = Local Included Author\nemail = included-local@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	threadGitTestCommand(t, root, "config", "--local", "include.path", "~/local-identity.gitconfig")
	author, err = executor.ReadCommitAuthor(t.Context(), root)
	if err != nil || author.Name != "Local Included Author" || author.Email != "included-local@example.invalid" {
		t.Fatalf("local tilde include lost: %#v %v", author, err)
	}
	if err = os.WriteFile(filepath.Join(root, "chosen.txt"), []byte("included author content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := executor.ReviewSelected(t.Context(), root, []string{"chosen.txt"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := executor.PrepareSelectedCommit(t.Context(), root, review, "local include identity", strings.Repeat("e", 64), author)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestThreadGitAuthorDriftRejectsPrepareAndPublication(t *testing.T) {
	root := threadGitFixture(t)
	global := isolateThreadGitIdentity(t, root, "[user]\nname = Original Author\nemail = original@example.invalid\n")
	executor, _ := NewMutationExecutor()
	author, err := executor.ReadCommitAuthor(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "chosen.txt"), []byte("pending exact content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := executor.ReviewSelected(t.Context(), root, []string{"chosen.txt"})
	if err != nil {
		t.Fatal(err)
	}
	head := threadGitTestCommand(t, root, "rev-parse", "HEAD")
	if err = os.WriteFile(global, []byte("[user]\nname = Changed Author\nemail = changed@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = executor.PrepareSelectedCommit(t.Context(), root, review, "reject changed identity", strings.Repeat("c", 64), author); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("prepare author drift accepted: %v", err)
	}
	current, err := executor.ReadCommitAuthor(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := executor.PrepareSelectedCommit(t.Context(), root, review, "pending publication", strings.Repeat("d", 64), current)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = os.WriteFile(global, []byte("[user]\nname = Another Author\nemail = another@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(t.Context()); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("publish author drift accepted: %v", err)
	}
	if got := threadGitTestCommand(t, root, "rev-parse", "HEAD"); got != head {
		t.Fatal("author drift changed current commit")
	}
}

func TestThreadGitIdentityIncludeCannotEnableExecutableDriver(t *testing.T) {
	root := threadGitFixture(t)
	global := isolateThreadGitIdentity(t, root, "[user]\nname = Global Author\nemail = global@example.invalid\n")
	included := filepath.Join(filepath.Dir(global), "local-driver.gitconfig")
	if err := os.WriteFile(included, []byte("[user]\nname = Local Included\nemail = local@example.invalid\n[filter \"unsafe\"]\nclean = forbidden-executable-fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	threadGitTestCommand(t, root, "config", "--local", "include.path", "~/local-driver.gitconfig")
	executor, _ := NewMutationExecutor()
	if author, err := executor.ReadCommitAuthor(t.Context(), root); err != nil || author.Name != "Local Included" {
		t.Fatalf("inert identity could not be read: %#v %v", author, err)
	}
	if _, err := executor.AdvancedBinding(t.Context(), root); err == nil || !strings.Contains(err.Error(), "executable Git drivers") {
		t.Fatalf("included driver was not rejected: %v", err)
	}
}
