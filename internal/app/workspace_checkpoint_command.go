package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

const workspaceCheckpointUsage = "usage: cyberagent workspace checkpoint " +
	"timeline|capture|preview|rewind|undo|redo|fork --run <run-id> [options]"

type workspaceCheckpointAuthorityFlags struct {
	operator          *string
	confirm           *bool
	permissionControl *bool
	dangerFullAccess  *bool
	debugMaximum      *bool
}

func addWorkspaceCheckpointAuthorityFlags(fs *flag.FlagSet) workspaceCheckpointAuthorityFlags {
	return workspaceCheckpointAuthorityFlags{
		operator: fs.String("operator", "cli_operator", "operator identity"),
		confirm: fs.Bool("confirm", false,
			"confirm the exact Workspace restore or Fork intent"),
		permissionControl: fs.Bool("enable-permission-control", false,
			"enable operator-selected Run permission for this process"),
		dangerFullAccess: fs.Bool("enable-danger-full-access", false,
			"enable danger-full-access for this process"),
		debugMaximum: fs.Bool("enable-debug-maximum-access", false,
			"enable maximum Debug permission for this process"),
	}
}

func (f workspaceCheckpointAuthorityFlags) capabilities() (
	domain.ExecutionPermissionRuntimeCapabilities, error,
) {
	value := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:   *f.permissionControl,
		DangerFullAccessEnabled:   *f.dangerFullAccess,
		DebugMaximumAccessEnabled: *f.debugMaximum,
	}
	if err := value.Validate(); err != nil {
		return domain.ExecutionPermissionRuntimeCapabilities{},
			apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	return value, nil
}

func (a *App) workspaceCheckpointCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(workspaceCheckpointUsage)
	}
	switch args[0] {
	case "timeline":
		return a.workspaceCheckpointTimeline(ctx, args[1:])
	case "capture":
		return a.workspaceCheckpointCapture(ctx, args[1:])
	case "preview":
		return a.workspaceCheckpointPreview(ctx, args[1:])
	case "rewind":
		return a.workspaceCheckpointRewind(ctx, args[1:])
	case "undo", "redo":
		return a.workspaceCheckpointCursorAction(ctx, args[0], args[1:])
	case "fork":
		return a.workspaceCheckpointFork(ctx, args[1:])
	default:
		return errors.New(workspaceCheckpointUsage)
	}
}

func (a *App) newWorkspaceCheckpointService(
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) (*application.WorkspaceCheckpointService, error) {
	if err := a.ensureStore(); err != nil {
		return nil, err
	}
	service, err := application.NewWorkspaceCheckpointService(a.store, capabilities)
	if err != nil {
		return nil, err
	}
	return service.WithLifecycleHooks(a.newLifecycleHookEngine()), nil
}

func (a *App) workspaceCheckpointTimeline(ctx context.Context, args []string) error {
	fs := newFlagSet("workspace checkpoint timeline", a.errOut)
	runID := fs.String("run", "", "Run identity")
	limit := fs.Int("limit", 100, "maximum checkpoints and transactions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" {
		return errors.New("usage: cyberagent workspace checkpoint timeline --run <run-id> [--limit <n>]")
	}
	service, err := a.newWorkspaceCheckpointService(
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		return err
	}
	value, err := service.Timeline(ctx, *runID, *limit)
	if err != nil {
		return err
	}
	return writeWorkspaceCheckpointJSON(a.out, value)
}

func (a *App) workspaceCheckpointCapture(ctx context.Context, args []string) error {
	fs := newFlagSet("workspace checkpoint capture", a.errOut)
	runID := fs.String("run", "", "Run identity")
	operationKey := fs.String("operation-key", "", "stable idempotency key")
	title := fs.String("title", "", "optional timeline title")
	operator := fs.String("operator", "cli_operator", "operator identity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" ||
		strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent workspace checkpoint capture --run <run-id> --operation-key <key> [--title <text>] [--operator <id>]")
	}
	service, err := a.newWorkspaceCheckpointService(
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		return err
	}
	checkpoint, replayed, err := service.Capture(ctx,
		application.WorkspaceCheckpointCaptureRequest{RunID: *runID,
			OperationKey: *operationKey, RequestedBy: *operator, Title: *title})
	if err != nil {
		return err
	}
	return writeWorkspaceCheckpointJSON(a.out, struct {
		Checkpoint workspacecheckpoint.Checkpoint `json:"checkpoint"`
		Replayed   bool                           `json:"replayed"`
	}{Checkpoint: checkpoint, Replayed: replayed})
}

func (a *App) workspaceCheckpointPreview(ctx context.Context, args []string) error {
	fs := newFlagSet("workspace checkpoint preview", a.errOut)
	runID := fs.String("run", "", "Run identity")
	target := fs.String("checkpoint", "", "target checkpoint identity")
	expected := fs.String("expected-current", "", "reviewed current checkpoint identity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" || strings.TrimSpace(*target) == "" {
		return errors.New("usage: cyberagent workspace checkpoint preview --run <run-id> --checkpoint <checkpoint-id> [--expected-current <checkpoint-id>]")
	}
	service, err := a.newWorkspaceCheckpointService(
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		return err
	}
	value, err := service.Restore(ctx, application.WorkspaceRestoreRequest{
		RunID: *runID, TargetCheckpointID: *target,
		ExpectedCurrentCheckpointID: *expected, RequestedBy: "cli_operator",
		Kind: workspacecheckpoint.TransactionRewind,
	})
	if err != nil {
		return err
	}
	return writeWorkspaceCheckpointJSON(a.out, value)
}

func (a *App) workspaceCheckpointRewind(ctx context.Context, args []string) error {
	fs := newFlagSet("workspace checkpoint rewind", a.errOut)
	runID := fs.String("run", "", "Run identity")
	target := fs.String("checkpoint", "", "target checkpoint identity")
	expected := fs.String("expected-current", "", "reviewed current checkpoint identity")
	operationKey := fs.String("operation-key", "", "stable idempotency key")
	authority := addWorkspaceCheckpointAuthorityFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" ||
		strings.TrimSpace(*target) == "" || strings.TrimSpace(*expected) == "" ||
		strings.TrimSpace(*operationKey) == "" || !*authority.confirm {
		return errors.New("usage: cyberagent workspace checkpoint rewind --run <run-id> --checkpoint <checkpoint-id> --expected-current <checkpoint-id> --operation-key <key> --confirm [runtime permission flags]")
	}
	capabilities, err := authority.capabilities()
	if err != nil {
		return err
	}
	service, err := a.newWorkspaceCheckpointService(capabilities)
	if err != nil {
		return err
	}
	value, err := service.Restore(ctx, application.WorkspaceRestoreRequest{
		RunID: *runID, TargetCheckpointID: *target,
		ExpectedCurrentCheckpointID: *expected, OperationKey: *operationKey,
		RequestedBy: *authority.operator, Kind: workspacecheckpoint.TransactionRewind,
		TriggerReceiptID: *target, Confirm: true,
	})
	if err != nil {
		return err
	}
	return writeWorkspaceCheckpointJSON(a.out, value)
}

func (a *App) workspaceCheckpointCursorAction(ctx context.Context, action string,
	args []string,
) error {
	fs := newFlagSet("workspace checkpoint "+action, a.errOut)
	runID := fs.String("run", "", "Run identity")
	expected := fs.String("expected-current", "", "reviewed current checkpoint identity")
	operationKey := fs.String("operation-key", "", "stable idempotency key")
	authority := addWorkspaceCheckpointAuthorityFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" ||
		strings.TrimSpace(*expected) == "" || strings.TrimSpace(*operationKey) == "" ||
		!*authority.confirm {
		return errors.New("usage: cyberagent workspace checkpoint " + action +
			" --run <run-id> --expected-current <checkpoint-id> --operation-key <key> --confirm [runtime permission flags]")
	}
	capabilities, err := authority.capabilities()
	if err != nil {
		return err
	}
	service, err := a.newWorkspaceCheckpointService(capabilities)
	if err != nil {
		return err
	}
	var value application.WorkspaceRestoreResult
	if action == "undo" {
		value, err = service.Undo(ctx, *runID, *expected, *operationKey,
			*authority.operator, true)
	} else {
		value, err = service.Redo(ctx, *runID, *expected, *operationKey,
			*authority.operator, true)
	}
	if err != nil {
		return err
	}
	return writeWorkspaceCheckpointJSON(a.out, value)
}

func (a *App) workspaceCheckpointFork(ctx context.Context, args []string) error {
	fs := newFlagSet("workspace checkpoint fork", a.errOut)
	runID := fs.String("run", "", "source Run identity")
	target := fs.String("checkpoint", "", "target checkpoint identity")
	expected := fs.String("expected-current", "", "reviewed current checkpoint identity")
	operationKey := fs.String("operation-key", "", "stable idempotency key")
	workspaceName := fs.String("workspace-name", "", "new Workspace name")
	workspaceRoot := fs.String("workspace-root", "", "new Git worktree path")
	branch := fs.String("branch", "", "new Git branch")
	goal := fs.String("goal", "", "optional new Run goal")
	authority := addWorkspaceCheckpointAuthorityFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" ||
		strings.TrimSpace(*target) == "" || strings.TrimSpace(*expected) == "" ||
		strings.TrimSpace(*operationKey) == "" || strings.TrimSpace(*workspaceName) == "" ||
		strings.TrimSpace(*workspaceRoot) == "" || strings.TrimSpace(*branch) == "" ||
		!*authority.confirm {
		return errors.New("usage: cyberagent workspace checkpoint fork --run <run-id> --checkpoint <checkpoint-id> --expected-current <checkpoint-id> --operation-key <key> --workspace-name <name> --workspace-root <path> --branch <branch> --confirm [--goal <text>] [runtime permission flags]")
	}
	capabilities, err := authority.capabilities()
	if err != nil {
		return err
	}
	service, err := a.newWorkspaceCheckpointService(capabilities)
	if err != nil {
		return err
	}
	value, err := service.Fork(ctx, application.WorkspaceForkRequest{
		RunID: *runID, TargetCheckpointID: *target,
		ExpectedCurrentCheckpointID: *expected, OperationKey: *operationKey,
		RequestedBy: *authority.operator, WorkspaceName: *workspaceName,
		WorkspaceRoot: *workspaceRoot, Branch: *branch, Goal: *goal,
		Confirm: true,
	})
	if err != nil {
		return err
	}
	return writeWorkspaceCheckpointJSON(a.out, value)
}

func writeWorkspaceCheckpointJSON(destination interface {
	Write([]byte) (int, error)
}, value any) error {
	encoder := json.NewEncoder(destination)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
