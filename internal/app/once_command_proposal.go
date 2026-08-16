package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

// onceCommandProposalCommands are the operator review surface for the
// one_shot_command_propose tool.
func (a *App) onceCommandProposalCommands(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent once-command proposals <run-id> | review <proposal-id> approve|deny [--enable-danger-full-access] --reason <text> | run --proposal <proposal-id> [--env KEY=VALUE]... [--enable-danger-full-access]")
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	executor, err := runner.NewPlatformOnceExecutor()
	if err != nil {
		return err
	}
	switch args[0] {
	case "proposals":
		if len(args) != 2 {
			return errors.New("usage: cyberagent once-command proposals <run-id>")
		}
		service := application.NewOnceCommandProposalReviewService(a.store, executor,
			domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true})
		values, err := service.List(ctx, args[1], 50)
		if err != nil {
			return err
		}
		for _, proposal := range values {
			fmt.Fprintf(a.out, "%s\tstatus=%s\texecutable=%s\targv=%s\tpurpose=%s\n",
				proposal.ID, proposal.Status, proposal.ExecutablePath,
				strings.Join(proposal.Argv, " "), proposal.Purpose)
		}
		fmt.Fprintf(a.out, "proposal_count: %d\n", len(values))
		return nil
	case "review":
		flags := newFlagSet("once-command review", a.errOut)
		enableFull := flags.Bool("enable-danger-full-access", false,
			"confirm the process runs with the danger-full-access gate")
		reason := flags.String("reason", "", "review reason")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
			"enable-danger-full-access": false, "reason": true,
		})); err != nil {
			return err
		}
		if flags.NArg() != 2 || (flags.Arg(1) != "approve" && flags.Arg(1) != "deny") {
			return errors.New("usage: cyberagent once-command review <proposal-id> approve|deny [--enable-danger-full-access] --reason <text>")
		}
		capabilities := domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: *enableFull,
		}
		if err := capabilities.Validate(); err != nil {
			return err
		}
		service := application.NewOnceCommandProposalReviewService(a.store, executor, capabilities)
		updated, err := service.Review(ctx, flags.Arg(0), flags.Arg(1), "cli_operator", *reason)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "proposal: %s\nstatus: %s\nreviewer: %s\n",
			updated.ID, updated.Status, updated.Reviewer)
		return nil
	case "run":
		flags := newFlagSet("once-command run", a.errOut)
		proposalID := flags.String("proposal", "", "approved proposal to execute")
		enableFull := flags.Bool("enable-danger-full-access", false,
			"confirm the process runs with the danger-full-access gate")
		var environment multiStringFlag
		flags.Var(&environment, "env", "allowlisted KEY=VALUE entry (repeatable)")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{"proposal": true, "env": true, "enable-danger-full-access": false})); err != nil {
			return err
		}
		if strings.TrimSpace(*proposalID) == "" || flags.NArg() != 0 {
			return errors.New("usage: cyberagent once-command run --proposal <proposal-id> [--env KEY=VALUE]... [--enable-danger-full-access]")
		}
		capabilities := domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: *enableFull,
		}
		if err := capabilities.Validate(); err != nil {
			return err
		}
		service := application.NewOnceCommandProposalReviewService(a.store, executor, capabilities)
		result, err := service.Execute(ctx, *proposalID, "cli_operator", environment.values)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "proposal: %s\nstatus: executed\nexit_code: %d\ntimed_out: %t\ntree_reaped: %t\nrequest_fingerprint: %s\n",
			*proposalID, result.Result.ExitCode, result.Result.TimedOut,
			result.Result.TreeReaped, result.RequestFingerprint)
		return nil
	default:
		return fmt.Errorf("unknown once-command subcommand %q", args[0])
	}
}
