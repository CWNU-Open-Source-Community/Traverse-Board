package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/repository"
)

// gitRemoteCommand is the operator path for network-scoped remote Git and
// PR operations. The review (remote/branch/TTL/PR metadata) prints first;
// --confirm executes and prints the redacted receipt.
func (a *App) gitRemoteCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent git-remote fetch|pull|push-branch|create-pr|update-pr --run <run-id> --remote <https-url> --branch <name> [--base <name>] [--title <t>] [--body <b>] [--pr-number <n>] [--credential <name>] [--ttl 30s] --operation-key <key> [--confirm]")
	}
	flags := newFlagSet("git-remote", a.errOut)
	runID := flags.String("run", "", "exact Run identity")
	operationKey := flags.String("operation-key", "", "stable idempotency key")
	remoteURL := flags.String("remote", "", "HTTPS remote URL")
	branch := flags.String("branch", "", "target branch")
	base := flags.String("base", "", "PR base branch")
	title := flags.String("title", "", "PR title")
	body := flags.String("body", "", "PR body")
	prNumber := flags.Int64("pr-number", 0, "existing PR number for update")
	credentialName := flags.String("credential", "", "credential reference name (never the secret)")
	ttl := flags.Duration("ttl", 60*time.Second, "network TTL")
	confirm := flags.Bool("confirm", false, "confirm execution after reviewing")
	if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
		"run": true, "operation-key": true, "remote": true, "branch": true,
		"base": true, "title": true, "body": true, "pr-number": true,
		"credential": true, "ttl": true, "confirm": false,
	})); err != nil {
		return err
	}
	if strings.TrimSpace(*runID) == "" || strings.TrimSpace(*remoteURL) == "" ||
		strings.TrimSpace(*operationKey) == "" || strings.TrimSpace(*branch) == "" {
		return errors.New("usage: cyberagent git-remote <op> --run <run-id> --remote <https-url> --branch <name> --operation-key <key> [--confirm]")
	}
	spec := repository.RemoteSpec{
		ProtocolVersion: repository.RemoteProtocolVersion,
		Operation:       repository.RemoteOperation(args[0]), RemoteURL: *remoteURL,
		Branch: *branch, BaseBranch: *base, PRTitle: *title, PRBody: *body,
		PRNumber: *prNumber, CredentialName: *credentialName,
		NetworkTTLMillis: ttl.Milliseconds(),
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	executor, err := repository.NewRemoteExecutor(credential.NewSystemStore())
	if err != nil {
		return err
	}
	service := application.NewGitRemoteService(a.store, executor, repository.NewPRClient(),
		credential.NewSystemStore())
	fmt.Fprintf(a.out, "operation: %s\nremote: %s\nbranch: %s\nbase_branch: %s\npr_title: %s\ncredential_reference: %s\nnetwork_ttl: %s\n",
		spec.Operation, spec.RemoteURL, spec.Branch, spec.BaseBranch, spec.PRTitle,
		func() string {
			if spec.CredentialName == "" {
				return "(none)"
			}
			return spec.CredentialName
		}(), ttl)
	if !*confirm {
		fmt.Fprintln(a.out, "review_only: true (re-run with --confirm to execute)")
		return nil
	}
	result, err := service.Execute(ctx, application.GitRemoteRequest{
		RunID: *runID, OperationKey: *operationKey, Spec: spec, RequestedBy: "cli_operator",
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "operation_id: %s\nreplayed: %t\nremote_host: %s\npost_head: %s\ncommit_id: %s\npull_request_url: %s\npull_request_number: %d\n",
		result.Record.ID, result.Replayed, result.Record.RemoteHost, result.Record.PostHead,
		result.Record.CommitID, result.Record.PullRequestURL, result.Record.PullRequestNumber)
	return nil
}
