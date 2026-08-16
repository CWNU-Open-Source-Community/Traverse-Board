package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/repository"
)

// gitOpCommand is the operator path for typed local Git mutations. The
// review is printed before execution; every execution is bound to the exact
// reviewed repository state and to one Run.
func (a *App) gitOpCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent git-op stage|unstage|commit|create-branch|switch-branch --run <run-id> [--path p]... [--all] [--message m] [--branch b] --operation-key <key> [--confirm]")
	}
	flags := newFlagSet("git-op", a.errOut)
	runID := flags.String("run", "", "exact Run identity")
	operationKey := flags.String("operation-key", "", "stable idempotency key")
	message := flags.String("message", "", "commit message")
	branch := flags.String("branch", "", "target branch name")
	allChanges := flags.Bool("all", false, "apply to all changes")
	confirm := flags.Bool("confirm", false, "confirm execution after reviewing")
	var paths multiStringFlag
	flags.Var(&paths, "path", "Workspace-relative path (repeatable)")
	if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
		"run": true, "operation-key": true, "message": true, "branch": true,
		"all": false, "confirm": false, "path": true,
	})); err != nil {
		return err
	}
	if strings.TrimSpace(*runID) == "" || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent git-op <operation> --run <run-id> --operation-key <key> [--confirm]")
	}
	spec := repository.MutationSpec{
		ProtocolVersion: repository.MutationProtocolVersion,
		Operation:       repository.MutationOperation(args[0]),
		Paths:           paths.values, AllChanges: *allChanges,
		Message: *message, Branch: *branch,
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	executor, err := repository.NewMutationExecutor()
	if err != nil {
		return err
	}
	service := application.NewGitMutationService(a.store, executor)
	review, err := service.Review(ctx, *runID, spec)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run: %s\nworkspace: %s\noperation: %s\nhead: %s\nbranch: %s\ntarget_branch: %s\ncommit_message: %s\nfiles: %d\n",
		review.RunID, review.WorkspaceID, spec.Operation, review.Review.Binding.Head,
		review.Review.Binding.Branch, spec.Branch, spec.Message, len(review.Review.Changes))
	for _, change := range review.Review.Changes {
		fmt.Fprintf(a.out, "  %s\tstaging=%s\tworktree=%s\n", change.Path, change.Staging, change.Worktree)
	}
	if review.Review.DiffStat != "" {
		fmt.Fprintf(a.out, "diff_stat:\n%s", review.Review.DiffStat)
	}
	if !*confirm {
		fmt.Fprintln(a.out, "review_only: true (re-run with --confirm to execute)")
		return nil
	}
	result, err := service.Execute(ctx, application.GitMutationRequest{
		RunID: *runID, OperationKey: *operationKey, Spec: spec, RequestedBy: "cli_operator",
	}, review.Review.Binding)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "mutation_id: %s\nreplayed: %t\npre_head: %s\npost_head: %s\ncommit_id: %s\nbranch: %s\nconflicted: %t\nclean: %t\n",
		result.Record.ID, result.Replayed, result.Record.PreHead, result.Record.PostHead,
		result.Record.CommitID, result.Record.Branch, result.Record.Conflicted, result.Record.Clean)
	return nil
}
