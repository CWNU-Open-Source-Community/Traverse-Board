package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

// onceCommandCommand is the operator path for one-shot workspace commands.
// The four-tier gate decides: conservative denies, approval requires
// --approved, full access runs with audit, debug never opens a shell.
func (a *App) onceCommandCommand(ctx context.Context, args []string) error {
	if len(args) > 0 && (args[0] == "proposals" || args[0] == "review" || (args[0] == "run" && len(args) > 1 && strings.Contains(strings.Join(args, " "), "--proposal"))) {
		return a.onceCommandProposalCommands(ctx, args)
	}
	if len(args) == 0 || args[0] != "run" {
		return errors.New("usage: cyberagent once-command run --run <run-id> --executable <abs-path> [--cwd <relative>] [--env KEY=VALUE]... [--timeout 30s] [--purpose <text>] [--approved] [--enable-danger-full-access] -- <argv...>")
	}
	flags := newFlagSet("once-command run", a.errOut)
	runID := flags.String("run", "", "exact Run identity")

	executablePath := flags.String("executable", "", "absolute path to a native binary outside the Workspace")

	cwd := flags.String("cwd", "", "Workspace-relative working directory")

	timeout := flags.Duration("timeout", 30*time.Second, "per-command timeout")

	purpose := flags.String("purpose", "", "bounded operator purpose")

	approved := flags.Bool("approved", false, "operator approved this exact command in approval mode")

	enableFull := flags.Bool("enable-danger-full-access", false, "confirm the process runs with the danger-full-access gate")

	var environment multiStringFlag

	flags.Var(&environment, "env", "allowlisted KEY=VALUE entry (repeatable)")

	if err := flags.Parse(reorderFlags(args[1:], map[string]bool{

		"run": true, "executable": true, "cwd": true, "env": true,

		"timeout": true, "purpose": true, "approved": false,

		"enable-danger-full-access": false,
	})); err != nil {

		return err

	}

	if strings.TrimSpace(*runID) == "" || strings.TrimSpace(*executablePath) == "" || flags.NArg() == 0 {

		return errors.New("usage: cyberagent once-command run --run <run-id> --executable <abs-path> -- <argv...>")

	}

	if err := a.ensureStore(); err != nil {

		return err

	}

	executor, err := runner.NewPlatformOnceExecutor()

	if err != nil {

		return err

	}

	capabilities := domain.ExecutionPermissionRuntimeCapabilities{

		OperatorApprovalEnabled: true,

		DangerFullAccessEnabled: *enableFull,

		DebugMaximumAccessEnabled: false,
	}

	if err := capabilities.Validate(); err != nil {

		return err

	}

	service := application.NewOnceCommandService(a.store, executor, capabilities)

	result, err := service.Execute(ctx, application.OnceCommandRunRequest{

		RunID: *runID, ExecutablePath: *executablePath, Argv: flags.Args(),

		WorkingDirectory: *cwd, Environment: environment.values,

		TimeoutMilliseconds: timeout.Milliseconds(), Purpose: *purpose,

		RequestedBy: "cli_operator", OperatorApproved: *approved,
	})

	if err != nil {

		return err

	}

	fmt.Fprintf(a.out, "request_fingerprint: %s\nspec_fingerprint: %s\npermission_mode: %s\ndecision: %s\nexit_code: %d\nstdout_observed_bytes: %d\nstderr_observed_bytes: %d\noutput_truncated: %t\ntimed_out: %t\ncancelled: %t\ntree_reaped: %t\nduration_ms: %d\n",

		result.RequestFingerprint, result.SpecFingerprint, result.PermissionMode,

		result.DecisionReason, result.Result.ExitCode,

		result.Result.Stdout.ObservedBytes, result.Result.Stderr.ObservedBytes,

		result.Result.Stdout.Truncated || result.Result.Stderr.Truncated,

		result.Result.TimedOut, result.Result.Cancelled, result.Result.TreeReaped,

		result.Result.CompletedAt.Sub(result.Result.StartedAt).Milliseconds())

	if result.Result.Stdout.CapturedPrefix != "" {

		fmt.Fprintf(a.out, "stdout_prefix:\n%s\n", result.Result.Stdout.CapturedPrefix)

	}

	if result.Result.Stderr.CapturedPrefix != "" {

		fmt.Fprintf(a.out, "stderr_prefix:\n%s\n", result.Result.Stderr.CapturedPrefix)

	}

	return nil

}
