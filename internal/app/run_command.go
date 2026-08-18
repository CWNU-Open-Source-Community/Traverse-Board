package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/coordinator"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/pricing"
	"cyberagent-workbench/internal/projectconfig"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/workspace"
)

type controlledCommandExecutor interface {
	Available() bool
	Execute(context.Context,
		runner.ControlledExecutionRequest) (runner.ControlledExecutionResult, error)
}

type hostCommandExecutor interface {
	Available() bool
	Execute(context.Context,
		runner.HostExecutionRequest) (runner.HostExecutionResult, error)
}

func (a *App) runCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("run subcommand is required")
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	service := application.NewRunService(a.store)
	switch args[0] {
	case "create":
		return a.runCreate(ctx, service, args[1:])
	case "adapt-task":
		return a.runAdaptTask(ctx, args[1:])
	case "list":
		return a.runList(ctx, service, args[1:])
	case "show":
		return a.runShow(ctx, service, args[1:])
	case "mode":
		return a.runMode(ctx, service, args[1:])
	case "phase":
		return a.runPhase(ctx, service, args[1:])
	case "execution-profile":
		return a.runExecutionProfile(ctx, args[1:])
	case "execution-interaction":
		return a.runExecutionInteraction(ctx, args[1:])
	case "execution-permission":
		return a.runExecutionPermission(ctx, args[1:])
	case "browser-cdp-permission":
		return a.runBrowserCDPPermission(ctx, args[1:])
	case "command-plan":
		return a.runCommandPlan(ctx, args[1:])
	case "command-execute":
		return a.runCommandExecute(ctx, args[1:])
	case "host-execute":
		return a.runHostExecute(ctx, args[1:])
	case "command-proposal":
		return a.runCommandProposal(ctx, args[1:])
	case "events":
		return a.runEvents(ctx, service, args[1:])
	case "step":
		return a.runSupervisorStep(ctx, args[1:])
	case "execute":
		return a.runSupervisorExecute(ctx, args[1:])
	case "checkpoint":
		return a.runSupervisorCheckpoint(ctx, args[1:])
	case "graph":
		return a.runAgentGraph(ctx, args[1:])
	case "delegations":
		return a.runDelegations(ctx, args[1:])
	case "delegation":
		return a.runDelegation(ctx, args[1:])
	case "plans":
		return a.runPlanDeliveryProposals(ctx, args[1:])
	case "plan":
		return a.runPlanDelivery(ctx, args[1:])
	case "delivery":
		return a.runDeliveryCheckpoint(ctx, args[1:])
	case "steer":
		return a.runOperatorSteering(ctx, args[1:])
	case "fanouts":
		return a.runFanouts(ctx, args[1:])
	case "fanout":
		return a.runFanout(ctx, args[1:])
	case "sandbox":
		return a.runSandboxManifest(ctx, args[1:])
	case "wake":
		return a.runWake(ctx, args[1:])
	case "lease":
		return a.runExecutionLease(ctx, service, args[1:])
	case "usage":
		return a.runUsage(ctx, service, args[1:])
	case "finish":
		return a.runSupervisorFinalize(ctx, application.LifecycleOutcomeCompleted, args[1:])
	case "fail":
		return a.runSupervisorFinalize(ctx, application.LifecycleOutcomeFailed, args[1:])
	case "start":
		return a.runTransition(ctx, service, "start", args[1:])
	case "pause":
		return a.runTransition(ctx, service, "pause", args[1:])
	case "resume":
		return a.runTransition(ctx, service, "resume", args[1:])
	case "cancel":
		return a.runTransition(ctx, service, "cancel", args[1:])
	default:
		return fmt.Errorf("unknown run subcommand %q", args[0])
	}
}

func (a *App) runCommandProposal(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(
			"usage: cyberagent run command-proposal list|show|review")
	}
	switch args[0] {
	case "list":
		fs := newFlagSet("run command-proposal list", a.errOut)
		limit := fs.Int("limit", 50, "maximum proposals")
		if err := fs.Parse(reorderFlags(args[1:],
			map[string]bool{"limit": true})); err != nil {
			return err
		}
		if fs.NArg() != 1 || *limit <= 0 || *limit > 100 {
			return errors.New(
				"usage: cyberagent run command-proposal list <run-id> [--limit <1..100>]")
		}
		service := application.NewControlledCommandProposalReviewService(
			a.store, nil, domain.ExecutionPermissionRuntimeCapabilities{})
		views, err := service.List(ctx, fs.Arg(0), *limit)
		if err != nil {
			return err
		}
		if len(views) == 0 {
			fmt.Fprintln(a.out, "no controlled command proposals")
			return nil
		}
		for _, view := range views {
			review := "pending"
			result := "none"
			if view.Review != nil {
				review = string(view.Review.Decision)
			}
			if view.Result != nil {
				result = string(view.Result.Status)
			}
			fmt.Fprintf(a.out,
				"%s\tkind=%s\treview=%s\tresult=%s\tpurpose=%s\tcreated_at=%s\n",
				view.Proposal.ID, view.Proposal.Kind, review, result,
				view.Proposal.Purpose,
				view.Proposal.CreatedAt.Format(time.RFC3339Nano))
		}
		return nil
	case "show":
		fs := newFlagSet("run command-proposal show", a.errOut)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New(
				"usage: cyberagent run command-proposal show <proposal-id>")
		}
		service := application.NewControlledCommandProposalReviewService(
			a.store, nil, domain.ExecutionPermissionRuntimeCapabilities{})
		view, err := service.Get(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		return writeControlledCommandProposalView(a.out, view)
	case "review":
		fs := newFlagSet("run command-proposal review", a.errOut)
		operationKey := fs.String("operation-key", "",
			"stable operator-owned review operation key")
		operator := fs.String("operator", "cli_operator",
			"operator identity")
		reason := fs.String("reason", "", "review reason")
		confirm := fs.Bool("confirm-execution", false,
			"confirm execution of the exact approved fixed action")
		enablePermissionControl := fs.Bool("enable-permission-control", false,
			"enable user-approval permission evaluation for this process")
		enableFullAccess := fs.Bool("enable-danger-full-access", false,
			"enable danger-full-access evaluation for this process")
		enableDebugAccess := fs.Bool("enable-debug-maximum-access", false,
			"enable maximum debug evaluation for this process")
		if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
			"operation-key": true, "operator": true, "reason": true,
			"confirm-execution":           false,
			"enable-permission-control":   false,
			"enable-danger-full-access":   false,
			"enable-debug-maximum-access": false,
		})); err != nil {
			return err
		}
		if fs.NArg() != 2 {
			return errors.New(
				"usage: cyberagent run command-proposal review <proposal-id> approve|deny --operation-key <key> [--confirm-execution] [--operator <id>] [--reason <text>]")
		}
		capabilities := domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled:   *enablePermissionControl,
			DangerFullAccessEnabled:   *enableFullAccess,
			DebugMaximumAccessEnabled: *enableDebugAccess,
		}
		if err := capabilities.Validate(); err != nil {
			return apperror.Wrap(apperror.CodeInvalidArgument,
				err.Error(), err)
		}
		executor, err := a.controlledCommandExecutor()
		if err != nil {
			return err
		}
		service := application.NewControlledCommandProposalReviewService(
			a.store, executor, capabilities)
		result, err := service.Review(ctx,
			application.ReviewControlledCommandProposalRequest{
				ProposalID: fs.Arg(0), Decision: fs.Arg(1),
				OperationKey: *operationKey, ReviewedBy: *operator,
				Reason: *reason, ConfirmExecution: *confirm,
			})
		if err != nil {
			return err
		}
		if err := writeControlledCommandProposalView(
			a.out, result.View); err != nil {
			return err
		}
		fmt.Fprintf(a.out,
			"review_replayed: %t\nexecution_replayed: %t\n",
			result.ReviewReplayed, result.ExecutionReplayed)
		if result.EvidenceContent != "" {
			fmt.Fprintln(a.out, "untrusted_evidence_begin")
			fmt.Fprintln(a.out, result.EvidenceContent)
			fmt.Fprintln(a.out, "untrusted_evidence_end")
		}
		return nil
	default:
		return fmt.Errorf("unknown run command-proposal subcommand %q",
			args[0])
	}
}

func writeControlledCommandProposalView(
	out interface{ Write([]byte) (int, error) },
	view application.ControlledCommandProposalView,
) error {
	proposal := view.Proposal
	if _, err := fmt.Fprintf(out,
		"proposal: %s\nprotocol: %s\npolicy: %s\nrun: %s\nmission: %s\nsession: %s\nworkspace: %s\nkind: %s\nrelative_path: %s\ntimeout_millis: %d\npurpose: %s\npermission_mode: %s\npermission_revision: %d\noperator_review_required: true\ninstruction_authorized: false\nexecution_authorized: false\ncapability_grant: false\nfingerprint: %s\n",
		proposal.ID, proposal.ProtocolVersion, proposal.PolicyVersion,
		proposal.RunID, proposal.MissionID, proposal.SessionID,
		proposal.WorkspaceID, proposal.Kind, proposal.RelativePath,
		proposal.TimeoutMilliseconds, proposal.Purpose,
		proposal.PermissionMode, proposal.PermissionRevision,
		proposal.Fingerprint); err != nil {
		return err
	}
	if view.Review == nil {
		_, err := fmt.Fprintln(out, "review: pending")
		return err
	}
	if _, err := fmt.Fprintf(out,
		"review: %s\nreview_id: %s\nreviewed_by: %s\nreview_reason: %s\nsingle_use_execution_authorized: %t\n",
		view.Review.Decision, view.Review.ID, view.Review.ReviewedBy,
		view.Review.Reason,
		view.Review.SingleUseExecutionAuthorized); err != nil {
		return err
	}
	if view.Result == nil {
		_, err := fmt.Fprintln(out, "result: none")
		return err
	}
	if _, err := fmt.Fprintf(out,
		"result: %s\nresult_id: %s\nsource_kind: %s\nsource_ref: %s\nresult_instruction_authorized: false\nraw_output_persisted: false\nautomatic_retry_allowed: false\n",
		view.Result.Status, view.Result.ID, view.Result.SourceKind,
		view.Result.SourceRef); err != nil {
		return err
	}
	if view.Receipt != nil {
		return writeControlledExecutionReceipt(
			out, *view.Receipt, false, false)
	}
	return nil
}

func (a *App) runOperatorSteering(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent run steer enqueue|cancel|drain|list|show")
	}
	service := application.NewOperatorSteeringService(a.store)
	switch args[0] {
	case "enqueue":
		fs := newFlagSet("run steer enqueue", a.errOut)
		operationKey := fs.String("operation-key", "", "stable operator steering operation key")
		operator := fs.String("operator", "cli_operator", "operator identity")
		if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
			"operation-key": true, "operator": true,
		})); err != nil {
			return err
		}
		if fs.NArg() < 2 || strings.TrimSpace(*operationKey) == "" {
			return errors.New(`usage: cyberagent run steer enqueue <run-id> "message" --operation-key <key> [--operator <id>]`)
		}
		result, err := service.Enqueue(ctx, application.QueueOperatorSteeringRequest{
			RunID: fs.Arg(0), Content: strings.Join(fs.Args()[1:], " "),
			OperationKey: *operationKey, RequestedBy: *operator,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "steering: %s\nrun: %s\nsequence: %d\nstatus: %s\nrequested_by: %s\nreplayed: %t\nnext: cyberagent run execute %s --max-steps 1\n",
			result.Message.ID, result.Message.RunID, result.Message.Sequence,
			result.Message.Status, result.Message.RequestedBy, result.Replayed,
			result.Message.RunID)
		return nil
	case "cancel":
		fs := newFlagSet("run steer cancel", a.errOut)
		operationKey := fs.String("operation-key", "", "stable cancellation operation key")
		operator := fs.String("operator", "cli_operator", "operator identity")
		reason := fs.String("reason", "", "operator cancellation reason")
		if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
			"operation-key": true, "operator": true, "reason": true,
		})); err != nil {
			return err
		}
		if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" ||
			strings.TrimSpace(*reason) == "" {
			return errors.New("usage: cyberagent run steer cancel <steering-id> --operation-key <key> --reason <text> [--operator <id>]")
		}
		result, err := service.Cancel(ctx, application.CancelQueuedOperatorSteeringRequest{
			MessageID: fs.Arg(0), OperationKey: *operationKey,
			RequestedBy: *operator, Reason: *reason,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "cancellation: %s\nsteering: %s\nrun: %s\nsequence: %d\nstatus: %s\nkind: %s\nrequested_by: %s\nreplayed: %t\n",
			result.Cancellation.ID, result.Message.ID, result.Message.RunID,
			result.Message.Sequence, result.Message.Status, result.Cancellation.Kind,
			result.Cancellation.RequestedBy, result.Replayed)
		return nil
	case "drain":
		fs := newFlagSet("run steer drain", a.errOut)
		maxSteps := fs.Int("max-steps", 1, "maximum queued turns to drain")
		if err := fs.Parse(reorderFlags(args[1:], map[string]bool{"max-steps": true})); err != nil {
			return err
		}
		if fs.NArg() != 1 || *maxSteps <= 0 ||
			*maxSteps > domain.MaxPendingOperatorSteering {
			return fmt.Errorf("usage: cyberagent run steer drain <run-id> [--max-steps <1..%d>]",
				domain.MaxPendingOperatorSteering)
		}
		result, err := application.NewOperatorSteeringDrainService(a.store, a.router,
			a.checker).WithActiveCalls(a.calls).Drain(ctx,
			application.DrainOperatorSteeringRequest{RunID: fs.Arg(0), MaxSteps: *maxSteps})
		fmt.Fprintf(a.out, "run: %s\nwoke: %t\nbefore_pending: %d\nbefore_prepared: %d\n",
			result.RunID, result.Woke, result.Before.Pending, result.Before.Prepared)
		for _, step := range result.Execution.Steps {
			fmt.Fprintf(a.out, "turn: %d\taction=%s\tstatus=%s\ttokens=%d\n",
				step.Turn, step.Action.Kind, step.RunStatus, step.Usage.TotalTokens)
		}
		fmt.Fprintf(a.out, "after_pending: %d\nafter_prepared: %d\ncommitted: %d\ncancelled: %d\nstop: %s\n",
			result.After.Pending, result.After.Prepared, result.After.Committed,
			result.After.Cancelled, result.Execution.StopReason)
		return err
	case "list":
		fs := newFlagSet("run steer list", a.errOut)
		limit := fs.Int("limit", 100, "maximum steering messages")
		if err := fs.Parse(reorderFlags(args[1:], map[string]bool{"limit": true})); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: cyberagent run steer list <run-id> [--limit <n>]")
		}
		values, summary, err := service.List(ctx, fs.Arg(0), *limit)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "pending: %d\nprepared: %d\ncommitted: %d\ncancelled: %d\n",
			summary.Pending, summary.Prepared, summary.Committed, summary.Cancelled)
		if len(values) == 0 {
			fmt.Fprintln(a.out, "no operator steering messages")
			return nil
		}
		for _, value := range values {
			fmt.Fprintf(a.out, "%s\tsequence=%d\tstatus=%s\trequested_by=%s\tcreated_at=%s\n",
				value.ID, value.Sequence, value.Status, value.RequestedBy,
				value.CreatedAt.Format(time.RFC3339Nano))
		}
		return nil
	case "show":
		fs := newFlagSet("run steer show", a.errOut)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: cyberagent run steer show <steering-id>")
		}
		value, err := service.Get(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "steering: %s\nrun: %s\nsession: %s\nsequence: %d\nstatus: %s\nrequested_by: %s\ncontent_sha256: %s\ncontent:\n%s\n",
			value.ID, value.RunID, value.SessionID, value.Sequence, value.Status,
			value.RequestedBy, value.ContentSHA256, value.Content)
		return nil
	default:
		return fmt.Errorf("unknown run steer subcommand %q", args[0])
	}
}

func (a *App) runDeliveryCheckpoint(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent run delivery checkpoint|list|show")
	}
	switch args[0] {
	case "checkpoint":
		return a.runDeliveryCheckpointRecord(ctx, args[1:])
	case "list":
		return a.runDeliveryCheckpointList(ctx, args[1:])
	case "show":
		return a.runDeliveryCheckpointShow(ctx, args[1:])
	default:
		return fmt.Errorf("unknown run delivery subcommand %q", args[0])
	}
}

func (a *App) runDeliveryCheckpointRecord(ctx context.Context, args []string) error {
	fs := newFlagSet("run delivery checkpoint", a.errOut)
	operationKey := fs.String("operation-key", "", "stable Delivery checkpoint operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	focused := fs.String("focused", "", "focused verification evidence")
	diffAudit := fs.String("diff-audit", "", "diff audit evidence")
	securityAudit := fs.String("security-audit", "", "security audit evidence")
	functional := fs.String("functional", "", "final-boundary functional verification evidence")
	robustness := fs.String("robustness", "", "final-boundary robustness audit evidence")
	handoff := fs.String("handoff", "", "compact durable handoff summary")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "operator": true, "focused": true,
		"diff-audit": true, "security-audit": true, "functional": true,
		"robustness": true, "handoff": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run delivery checkpoint <work-id> --operation-key <key> --focused <evidence> --diff-audit <evidence> --security-audit <evidence> --handoff <summary> [--functional <evidence> --robustness <evidence>] [--operator <id>]")
	}
	result, err := application.NewDeliveryCheckpointService(a.store).Record(ctx,
		application.RecordDeliveryCheckpointRequest{
			WorkItemID: fs.Arg(0), OperationKey: *operationKey,
			RequestedBy: *operator, FocusedVerification: *focused,
			DiffAudit: *diffAudit, SecurityAudit: *securityAudit,
			FunctionalVerification: *functional, RobustnessAudit: *robustness,
			HandoffSummary: *handoff,
		})
	if err != nil {
		return err
	}
	printDeliveryCheckpoint(a, result.Checkpoint, false)
	fmt.Fprintf(a.out, "handoff_note: %s\nreplayed: %t\ncompletion_gate_ready: true\nnext: cyberagent todo complete %s --version %d\n",
		result.Note.ID, result.Replayed, result.Checkpoint.WorkItemID,
		result.Checkpoint.WorkItemVersion)
	return nil
}

func (a *App) runDeliveryCheckpointList(ctx context.Context, args []string) error {
	fs := newFlagSet("run delivery list", a.errOut)
	limit := fs.Int("limit", 100, "maximum Delivery checkpoints")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"limit": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run delivery list <run-id> [--limit <n>]")
	}
	values, err := application.NewDeliveryCheckpointService(a.store).List(ctx,
		fs.Arg(0), *limit)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		fmt.Fprintln(a.out, "no Delivery checkpoints")
		return nil
	}
	for _, value := range values {
		fmt.Fprintf(a.out, "%s\twork_item=%s\tslice=%d/%d\tmode_revision=%d\twork_item_version=%d\tfull_gate=%t\thandoff_note=%s\tcreated_at=%s\n",
			value.ID, value.WorkItemID, value.ModuleOrdinal, value.ModuleCount,
			value.ModeRevision, value.WorkItemVersion, value.FullGateRequired,
			value.HandoffNoteID, value.CreatedAt.Format(time.RFC3339Nano))
	}
	return nil
}

func (a *App) runDeliveryCheckpointShow(ctx context.Context, args []string) error {
	fs := newFlagSet("run delivery show", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run delivery show <checkpoint-id>")
	}
	value, err := application.NewDeliveryCheckpointService(a.store).Get(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	printDeliveryCheckpoint(a, value, true)
	return nil
}

func printDeliveryCheckpoint(a *App, value domain.DeliveryCheckpoint,
	includeEvidence bool,
) {
	fmt.Fprintf(a.out, "checkpoint: %s\nrun: %s\nselection: %s\nproposal: %s\nwork_item: %s\ndirection: %d\nslice: %d/%d\nmode_snapshot: %s\nmode_revision: %d\nwork_item_version: %d\nacceptance_fingerprint: %s\nsource_fingerprint: %s\nfull_gate_required: %t\nhandoff_note: %s\nhandoff_digest: %s\nrequested_by: %s\nversion: %d\ncreated_at: %s\n",
		value.ID, value.RunID, value.SelectionID, value.ProposalID,
		value.WorkItemID, value.DirectionOrdinal, value.ModuleOrdinal,
		value.ModuleCount, value.ModeSnapshotID, value.ModeRevision,
		value.WorkItemVersion, value.AcceptanceFingerprint,
		value.SourceFingerprint, value.FullGateRequired, value.HandoffNoteID,
		value.HandoffDigest, value.RequestedBy, value.Version,
		value.CreatedAt.Format(time.RFC3339Nano))
	if includeEvidence {
		fmt.Fprintf(a.out, "focused_verification: %s\ndiff_audit: %s\nsecurity_audit: %s\n",
			planDeliveryCLIText(value.FocusedVerification),
			planDeliveryCLIText(value.DiffAudit),
			planDeliveryCLIText(value.SecurityAudit))
		if value.FullGateRequired {
			fmt.Fprintf(a.out, "functional_verification: %s\nrobustness_audit: %s\n",
				planDeliveryCLIText(value.FunctionalVerification),
				planDeliveryCLIText(value.RobustnessAudit))
		}
	}
}

func (a *App) runPlanDelivery(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent run plan show|choose|selection")
	}
	switch args[0] {
	case "show":
		return a.runPlanDeliveryShow(ctx, args[1:])
	case "choose":
		return a.runPlanDeliveryChoose(ctx, args[1:])
	case "selection":
		return a.runPlanDeliverySelection(ctx, args[1:])
	default:
		return fmt.Errorf("unknown run plan subcommand %q", args[0])
	}
}

func (a *App) runPlanDeliveryProposals(ctx context.Context, args []string) error {
	fs := newFlagSet("run plans", a.errOut)
	limit := fs.Int("limit", 20, "maximum Plan/Delivery proposals")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"limit": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run plans <run-id> [--limit <n>]")
	}
	proposals, err := application.NewPlanDeliveryService(a.store).
		ListProposals(ctx, fs.Arg(0), *limit)
	if err != nil {
		return err
	}
	if len(proposals) == 0 {
		fmt.Fprintln(a.out, "no Plan/Delivery proposals")
		return nil
	}
	selection, selected, err := application.NewPlanDeliveryService(a.store).
		SelectionForRun(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	for _, proposal := range proposals {
		selectedDirection := 0
		if selected && selection.ProposalID == proposal.ID {
			selectedDirection = selection.DirectionOrdinal
		}
		fmt.Fprintf(a.out, "%s\tstatus=%s\tdirections=%d\tmode_revision=%d\tselected_direction=%d\tfingerprint=%s\tcreated_at=%s\n",
			proposal.ID, proposal.Status, len(proposal.Spec.Directions),
			proposal.ModeRevision, selectedDirection, proposal.Fingerprint,
			proposal.CreatedAt.Format(time.RFC3339Nano))
	}
	return nil
}

func (a *App) runPlanDeliveryShow(ctx context.Context, args []string) error {
	fs := newFlagSet("run plan show", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run plan show <proposal-id>")
	}
	proposal, err := application.NewPlanDeliveryService(a.store).
		GetProposal(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	printPlanDeliveryProposal(a, proposal)
	return nil
}

func (a *App) runPlanDeliveryChoose(ctx context.Context, args []string) error {
	fs := newFlagSet("run plan choose", a.errOut)
	operationKey := fs.String("operation-key", "", "stable direction choice operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "operator": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run plan choose <proposal-id> 1|2|3 --operation-key <key> [--operator <id>]")
	}
	direction, err := strconv.Atoi(fs.Arg(1))
	if err != nil {
		return errors.New("Plan/Delivery direction must be 1, 2, or 3")
	}
	result, err := application.NewPlanDeliveryService(a.store).Select(ctx,
		application.SelectPlanDeliveryDirectionRequest{
			ProposalID: fs.Arg(0), Direction: direction,
			OperationKey: *operationKey, RequestedBy: *operator,
		})
	if err != nil {
		return err
	}
	printPlanDeliverySelection(a, result.Selection)
	for _, item := range result.WorkItems {
		fmt.Fprintf(a.out, "work_item[%d]: %s title=%s dependencies=%s\n",
			indexPlanDeliveryWorkItem(result.Selection, item.ID), item.ID,
			item.Title, strings.Join(item.Dependencies, ","))
	}
	fmt.Fprintf(a.out, "handoff_note: %s\nreplayed: %t\nphase_changed: false\ncapability_grant: false\n",
		result.Note.ID, result.Replayed)
	fmt.Fprintf(a.out, "next: cyberagent run phase %s deliver --operation-key <key> --operator %s --reason \"accepted direction %d\"\n",
		result.Selection.RunID, result.Selection.RequestedBy,
		result.Selection.DirectionOrdinal)
	return nil
}

func (a *App) runPlanDeliverySelection(ctx context.Context, args []string) error {
	fs := newFlagSet("run plan selection", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run plan selection <run-id>")
	}
	selection, found, err := application.NewPlanDeliveryService(a.store).
		SelectionForRun(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintln(a.out, "no Plan/Delivery direction selected")
		return nil
	}
	printPlanDeliverySelection(a, selection)
	return nil
}

func printPlanDeliveryProposal(a *App, proposal domain.PlanDeliveryProposal) {
	fmt.Fprintf(a.out, "proposal: %s\nrun: %s\nprotocol: %s\nstatus: %s\nmode_revision: %d\ndirection_count: %d\nfingerprint: %s\noperator_choice_required: true\nselection_authorized: false\nphase_change_authorized: false\nexecution_authorized: false\n",
		proposal.ID, proposal.RunID, proposal.Spec.Version, proposal.Status,
		proposal.ModeRevision, len(proposal.Spec.Directions), proposal.Fingerprint)
	for _, direction := range proposal.Spec.Directions {
		fmt.Fprintf(a.out, "\ndirection %d: %s\nsummary: %s\ntradeoffs: %s\n",
			direction.Ordinal, planDeliveryCLIText(direction.Title),
			planDeliveryCLIText(direction.Summary),
			planDeliveryCLIText(strings.Join(direction.Tradeoffs, " | ")))
		for _, module := range direction.Modules {
			dependencies := make([]string, len(module.Dependencies))
			for index, dependency := range module.Dependencies {
				dependencies[index] = strconv.Itoa(dependency)
			}
			fmt.Fprintf(a.out, "  slice %d: %s\n    objective: %s\n    acceptance: %s\n    depends_on: %s\n",
				module.Ordinal, planDeliveryCLIText(module.Title),
				planDeliveryCLIText(module.Objective),
				planDeliveryCLIText(strings.Join(module.AcceptanceCriteria, " | ")),
				strings.Join(dependencies, ","))
		}
	}
}

func planDeliveryCLIText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func printPlanDeliverySelection(a *App, selection domain.PlanDeliverySelection) {
	fmt.Fprintf(a.out, "selection: %s\nproposal: %s\nrun: %s\ndirection: %d\nmodule_count: %d\nnote: %s\nrequested_by: %s\ncreated_at: %s\n",
		selection.ID, selection.ProposalID, selection.RunID,
		selection.DirectionOrdinal, len(selection.Items), selection.NoteID,
		selection.RequestedBy, selection.CreatedAt.Format(time.RFC3339Nano))
	for _, item := range selection.Items {
		fmt.Fprintf(a.out, "selected_slice[%d]: module=%d work_item=%s\n",
			item.Ordinal, item.ModuleOrdinal, item.WorkItemID)
	}
}

func indexPlanDeliveryWorkItem(selection domain.PlanDeliverySelection,
	workItemID string,
) int {
	for _, item := range selection.Items {
		if item.WorkItemID == workItemID {
			return item.Ordinal
		}
	}
	return 0
}

func (a *App) runFanouts(ctx context.Context, args []string) error {
	fs := newFlagSet("run fanouts", a.errOut)
	limit := fs.Int("limit", 20, "maximum read-only fan-out plans")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"limit": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run fanouts <run-id> [--limit <n>]")
	}
	plans, err := a.store.ListReadOnlyFanoutPlans(ctx, fs.Arg(0), *limit)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		fmt.Fprintln(a.out, "no read-only fan-out plans")
		return nil
	}
	for _, plan := range plans {
		fmt.Fprintf(a.out, "%s\tstatus=%s\ttier=%s\tparallelism=%d\tfiles=%d\tshards=%d\texecution_authorized=false\tcreated_at=%s\n",
			plan.ID, plan.Status, plan.RequestedTier, plan.EffectiveParallelism,
			plan.FileCount, plan.ShardCount, plan.CreatedAt.Format(time.RFC3339Nano))
	}
	return nil
}

func (a *App) runFanout(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent run fanout plan|execute|show|execution|report")
	}
	switch args[0] {
	case "plan":
		return a.runFanoutPlan(ctx, args[1:])
	case "execute":
		return a.runFanoutExecute(ctx, args[1:])
	case "show":
		return a.runFanoutShow(ctx, args[1:])
	case "execution":
		return a.runFanoutExecutionShow(ctx, args[1:])
	case "report":
		return a.runFanoutReport(ctx, args[1:])
	default:
		return a.runFanoutShow(ctx, args)
	}
}

func (a *App) runFanoutReport(ctx context.Context, args []string) error {
	fs := newFlagSet("run fanout report", a.errOut)
	format := fs.String("format", "markdown", "report format: markdown, json, or sarif")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"format": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run fanout report <execution-id> [--format markdown|json|sarif]")
	}
	value, _, err := application.NewFindingReportService(a.store).
		GenerateReadOnlyFanout(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	return a.renderFindingReport(value, *format)
}

func (a *App) runFanoutExecute(ctx context.Context, args []string) error {
	fs := newFlagSet("run fanout execute", a.errOut)
	operationKey := fs.String("operation-key", "", "stable execution operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	maxOutputTokens := fs.Int("max-output-tokens", 1024,
		"maximum output tokens reserved for each shard")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "operator": true, "max-output-tokens": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run fanout execute <plan-id> --operation-key <key> [--operator <id>] [--max-output-tokens <128..4096>]")
	}
	result, err := application.NewReadOnlyFanoutExecutionService(a.store, a.router,
		a.checker).WithMonetaryBudget(application.NewMonetaryBudgetService(a.store)).
		Execute(ctx, application.ExecuteReadOnlyFanoutRequest{
			PlanID: fs.Arg(0), OperationKey: *operationKey, RequestedBy: *operator,
			MaxOutputTokensPerShard: *maxOutputTokens,
		})
	if result.Execution.ID != "" {
		a.printReadOnlyFanoutExecution(result.Execution, result.Replayed,
			result.Recovered, &result.UsageAfter)
	}
	return err
}

func (a *App) runFanoutExecutionShow(ctx context.Context, args []string) error {
	fs := newFlagSet("run fanout execution", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run fanout execution <execution-id>")
	}
	execution, err := a.store.GetReadOnlyFanoutExecution(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	a.printReadOnlyFanoutExecution(execution, false, false, nil)
	return nil
}

func (a *App) printReadOnlyFanoutExecution(execution domain.ReadOnlyFanoutExecution,
	replayed bool, recovered bool, usage *domain.RunAgentUsage,
) {
	fmt.Fprintf(a.out, "fanout_execution: %s\nplan: %s\nrun: %s\nstatus: %s\nparallelism: %d\nmax_output_tokens_per_shard: %d\nsnapshot_digest: %s\ncapability: workspace_readonly\nshell: false\nfile_write: false\nprocess: false\nnetwork: false\nexternal_tools: false\nchild_spawn: false\nreplayed: %t\nrecovered: %t\n",
		execution.ID, execution.PlanID, execution.RunID, execution.Status,
		execution.Parallelism, execution.MaxOutputTokensPerShard,
		execution.SnapshotDigest, replayed, recovered)
	if execution.StopCode != "" {
		fmt.Fprintf(a.out, "stop_code: %s\n", execution.StopCode)
	}
	for _, shard := range execution.Shards {
		fmt.Fprintf(a.out, "shard_%d: status=%s attempts=%d provider=%s model=%s tokens=%d elapsed_millis=%d findings=%d",
			shard.Ordinal, shard.Status, shard.AttemptCount, shard.Provider, shard.Model,
			shard.TotalTokens, shard.ElapsedMillis, shard.FindingCount)
		if shard.ErrorCode != "" {
			fmt.Fprintf(a.out, " error_code=%s", shard.ErrorCode)
		}
		fmt.Fprintln(a.out)
		if shard.ReportJSON != "" {
			var report domain.ReadOnlyFanoutReport
			if json.Unmarshal([]byte(shard.ReportJSON), &report) == nil {
				fmt.Fprintf(a.out, "  summary: %s\n", report.Summary)
				for _, finding := range report.Findings {
					fmt.Fprintf(a.out, "  finding: severity=%s path=%s line=%d-%d title=%s\n",
						finding.Severity, finding.Path, finding.LineStart,
						finding.LineEnd, finding.Title)
				}
			}
		}
	}
	if usage != nil && usage.RunID != "" {
		fmt.Fprintf(a.out, "run_total_tokens: %d\nrun_readonly_fanout_tokens: %d\nrun_total_execution_millis: %d\nrun_readonly_fanout_millis: %d\n",
			usage.TotalTokens, usage.ReadOnlyFanoutTokens,
			usage.TotalExecutionMillis, usage.ReadOnlyFanoutMillis)
	}
}

func (a *App) runFanoutPlan(ctx context.Context, args []string) error {
	fs := newFlagSet("run fanout plan", a.errOut)
	tier := fs.String("tier", "auto", "parallelism cap: auto, 1, 2, 4, or 6")
	scopePath := fs.String("path", ".", "workspace-relative directory scope")
	operationKey := fs.String("operation-key", "", "stable planning operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"tier": true, "path": true, "operation-key": true, "operator": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run fanout plan <run-id> <goal> --operation-key <key> [--tier auto|1|2|4|6] [--path <dir>] [--operator <id>]")
	}
	result, err := application.NewReadOnlyFanoutPlanService(a.store, a.checker).Create(ctx,
		application.CreateReadOnlyFanoutPlanRequest{
			RunID: fs.Arg(0), Goal: fs.Arg(1), ScopePath: *scopePath, Tier: *tier,
			OperationKey: *operationKey, RequestedBy: *operator,
		})
	if err != nil {
		return err
	}
	plan := result.Plan
	fmt.Fprintf(a.out, "fanout_plan: %s\nrun: %s\nstatus: %s\nprotocol: %s\nrequested_tier: %s\neffective_parallelism: %d\nfiles: %d\nexcluded: %d\nshards: %d\nsnapshot_digest: %s\ncapability: workspace_readonly\nshell: false\nfile_write: false\nnetwork: false\nchild_spawn: false\nexecution_authorized: false\nreplayed: %t\n",
		plan.ID, plan.RunID, plan.Status, plan.ProtocolVersion, plan.RequestedTier,
		plan.EffectiveParallelism, plan.FileCount, plan.ExcludedCount, plan.ShardCount,
		plan.SnapshotDigest, result.Replayed)
	return nil
}

func (a *App) runFanoutShow(ctx context.Context, args []string) error {
	fs := newFlagSet("run fanout show", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run fanout show <plan-id>")
	}
	plan, err := a.store.GetReadOnlyFanoutPlan(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "fanout_plan: %s\nrun: %s\nworkspace: %s\nstatus: %s\nprotocol: %s\ngoal: %s\nscope: %s\nrequested_tier: %s\neffective_parallelism: %d\nfiles: %d\ntotal_bytes: %d\nexcluded: %d\nshards: %d\nsnapshot_digest: %s\ncapability_fingerprint: %s\ncapability: workspace_readonly\nexecution_authorized: false\ncreated_at: %s\n",
		plan.ID, plan.RunID, plan.WorkspaceID, plan.Status, plan.ProtocolVersion,
		plan.Goal, plan.ScopePath, plan.RequestedTier, plan.EffectiveParallelism,
		plan.FileCount, plan.TotalBytes, plan.ExcludedCount, plan.ShardCount,
		plan.SnapshotDigest, plan.CapabilityFingerprint,
		plan.CreatedAt.Format(time.RFC3339Nano))
	for _, shard := range plan.Shards {
		fmt.Fprintf(a.out, "shard_%d: status=%s files=%d bytes=%d digest=%s\n",
			shard.Ordinal, shard.Status, shard.FileCount, shard.TotalBytes,
			shard.InputDigest)
		for _, file := range plan.Files {
			if file.ShardOrdinal == shard.Ordinal {
				fmt.Fprintf(a.out, "  %d. %s bytes=%d sha256=%s\n", file.Ordinal,
					file.RelativePath, file.SizeBytes, file.ContentSHA256)
			}
		}
	}
	return nil
}

func (a *App) runDelegations(ctx context.Context, args []string) error {
	fs := newFlagSet("run delegations", a.errOut)
	limit := fs.Int("limit", 20, "maximum proposals")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"limit": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run delegations <run-id> [--limit <n>]")
	}
	proposals, err := a.store.ListSpecialistDelegationProposals(ctx, fs.Arg(0), *limit)
	if err != nil {
		return err
	}
	if len(proposals) == 0 {
		fmt.Fprintln(a.out, "no Specialist delegation proposals")
		return nil
	}
	for _, proposal := range proposals {
		reviewStatus := "pending"
		if review, found, err := a.store.GetSpecialistDelegationReviewByProposal(ctx,
			proposal.ID); err != nil {
			return err
		} else if found {
			reviewStatus = string(review.Decision)
		}
		applicationStatus := "none"
		if applied, found, err := a.store.GetSpecialistDelegationApplicationByProposal(ctx,
			proposal.ID); err != nil {
			return err
		} else if found {
			applicationStatus = string(applied.Status)
		}
		fmt.Fprintf(a.out, "%s\tstatus=%s\treview=%s\tapplication=%s\tassignments=%d\troot=%s\tcreated_at=%s\n",
			proposal.ID, proposal.Status, reviewStatus, applicationStatus,
			len(proposal.Spec.Assignments), proposal.RootAgentID,
			proposal.CreatedAt.Format(time.RFC3339Nano))
	}
	return nil
}

func (a *App) runDelegation(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent run delegation [show] <proposal-id> | approve|reject|apply|schedule|continue <proposal-id> --operation-key <key>")
	}
	switch args[0] {
	case "approve", "reject":
		return a.runDelegationReview(ctx, args[0], args[1:])
	case "apply":
		return a.runDelegationApply(ctx, args[1:])
	case "schedule", "continue":
		return a.runDelegationSchedule(ctx, args[0], args[1:])
	case "show":
		return a.runDelegationShow(ctx, args[1:])
	default:
		return a.runDelegationShow(ctx, args)
	}
}

func (a *App) runDelegationShow(ctx context.Context, args []string) error {
	fs := newFlagSet("run delegation", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run delegation <proposal-id>")
	}
	proposal, err := a.store.GetSpecialistDelegationProposal(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "proposal: %s\nrun: %s\nroot_agent: %s\nstatus: %s\nprotocol: %s\nassignments: %d\nproposal_admission_authorized: false\noperator_review_required: true\ncreated_at: %s\n",
		proposal.ID, proposal.RunID, proposal.RootAgentID, proposal.Status,
		proposal.Spec.Version, len(proposal.Spec.Assignments),
		proposal.CreatedAt.Format(time.RFC3339Nano))
	for _, assignment := range proposal.Spec.Assignments {
		fmt.Fprintf(a.out, "%d. %s\n   goal: %s\n   skills: %s\n   budget: turns=%d tokens=%d\n",
			assignment.Ordinal, assignment.Title, assignment.Goal,
			strings.Join(assignment.Skills, ","), assignment.TurnLimit,
			assignment.TokenLimit)
	}
	if review, found, err := a.store.GetSpecialistDelegationReviewByProposal(ctx,
		proposal.ID); err != nil {
		return err
	} else if !found {
		fmt.Fprintln(a.out, "review: pending")
	} else {
		fmt.Fprintf(a.out, "review: %s\nreview_id: %s\nreviewed_by: %s\nreview_reason: %s\nreviewed_at: %s\napplication_required: true\n",
			review.Decision, review.ID, review.ReviewedBy, review.Reason,
			review.CreatedAt.Format(time.RFC3339Nano))
	}
	if applied, found, err := a.store.GetSpecialistDelegationApplicationByProposal(ctx,
		proposal.ID); err != nil {
		return err
	} else if !found {
		fmt.Fprintln(a.out, "application: none")
	} else {
		fmt.Fprintf(a.out, "application: %s\napplication_id: %s\napplication_version: %d\napplication_stop_code: %s\n",
			applied.Status, applied.ID, applied.Version, applied.StopCode)
		for _, assignment := range applied.Assignments {
			fmt.Fprintf(a.out, "application_assignment_%d: status=%s agent=%s message=%s\n",
				assignment.Ordinal, assignment.Status, assignment.AgentID, assignment.MessageID)
		}
		request, requested, err := a.store.
			GetLatestSpecialistOperatorScheduleRequestByApplication(ctx, applied.ID)
		if err != nil {
			return err
		}
		if !requested {
			fmt.Fprintln(a.out, "scheduling_requested: false\nscheduling_started: false")
			return nil
		}
		fmt.Fprintf(a.out, "scheduling_requested: true\nschedule_request_id: %s\nschedule_requested_by: %s\nschedule_agents: %s\nschedule_max_rounds: %d\n",
			request.ID, request.RequestedBy, strings.Join(request.AgentIDs, ","),
			request.MaxRounds)
		schedule, attempt, started, err := a.store.
			GetLatestSpecialistOperatorScheduleAttempt(ctx, request.ID)
		if err != nil {
			return err
		}
		if !started {
			fmt.Fprintln(a.out, "scheduling_started: false\nschedule_status: pending")
			return nil
		}
		fmt.Fprintf(a.out, "scheduling_started: true\nschedule_id: %s\nschedule_status: %s\nschedule_attempt_ordinal: %d\nschedule_rounds_completed: %d\nschedule_turns_started: %d\n",
			schedule.ID, schedule.Status, attempt.Ordinal, schedule.RoundsCompleted,
			schedule.TurnsStarted)
	}
	return nil
}

func (a *App) runDelegationReview(ctx context.Context, action string, args []string) error {
	fs := newFlagSet("run delegation "+action, a.errOut)
	operationKey := fs.String("operation-key", "", "stable review operation key")
	reviewer := fs.String("reviewer", "cli_operator", "reviewer identity")
	reason := fs.String("reason", "", "redacted review reason")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "reviewer": true, "reason": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" {
		return fmt.Errorf("usage: cyberagent run delegation %s <proposal-id> --operation-key <key> [--reviewer <id>] [--reason <text>]", action)
	}
	decision := domain.SpecialistDelegationApproved
	if action == "reject" {
		decision = domain.SpecialistDelegationRejected
	}
	result, err := application.NewSpecialistDelegationReviewService(a.store).Review(ctx,
		application.ReviewSpecialistDelegationRequest{
			ProposalID: fs.Arg(0), OperationKey: *operationKey, Decision: decision,
			Reason: *reason, ReviewedBy: *reviewer,
		})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "review: %s\nproposal: %s\ndecision: %s\nreviewed_by: %s\nadmission_authorized: false\napplication_required: true\nreplayed: %t\n",
		result.Review.ID, result.Review.ProposalID, result.Review.Decision,
		result.Review.ReviewedBy, result.Replayed)
	return nil
}

func (a *App) runDelegationApply(ctx context.Context, args []string) error {
	fs := newFlagSet("run delegation apply", a.errOut)
	operationKey := fs.String("operation-key", "", "stable application operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "operator": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run delegation apply <proposal-id> --operation-key <key> [--operator <id>]")
	}
	service, err := application.NewDefaultSpecialistDelegationApplicationService(a.store, a.checker)
	if err != nil {
		return err
	}
	result, err := service.Apply(ctx, application.ApplySpecialistDelegationRequest{
		ProposalID: fs.Arg(0), OperationKey: *operationKey, RequestedBy: *operator,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "application: %s\nproposal: %s\nstatus: %s\nassignments: %d\nadmission_authorized: true\nscheduling_started: false\nreplayed: %t\nrecovered: %t\n",
		result.Application.ID, result.Application.ProposalID, result.Application.Status,
		result.Application.AssignmentCount, result.Replayed, result.Recovered)
	for _, assignment := range result.Application.Assignments {
		fmt.Fprintf(a.out, "%d. status=%s agent=%s message=%s\n", assignment.Ordinal,
			assignment.Status, assignment.AgentID, assignment.MessageID)
	}
	return nil
}

func (a *App) runDelegationSchedule(ctx context.Context, action string, args []string) error {
	fs := newFlagSet("run delegation "+action, a.errOut)
	operationKey := fs.String("operation-key", "", "stable schedule operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	maxRounds := fs.Int("max-rounds", 1, "bounded schedule rounds")
	var agentIDs stringListFlag
	fs.Var(&agentIDs, "agent", "instructed child Agent id (repeatable)")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "operator": true, "max-rounds": true, "agent": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" {
		return fmt.Errorf("usage: cyberagent run delegation %s <proposal-id> --operation-key <key> [--operator <id>] [--max-rounds <n>] [--agent <id>]", action)
	}
	result, err := application.NewSpecialistOperatorScheduleService(
		a.store, a.router, a.checker).Execute(ctx,
		application.ExecuteSpecialistOperatorScheduleRequest{
			ProposalID: fs.Arg(0), AgentIDs: agentIDs.values, MaxRounds: *maxRounds,
			OperationKey: *operationKey, RequestedBy: *operator,
		})
	if result.Request.ID != "" {
		printSpecialistOperatorScheduleResult(a.out, result)
	}
	return err
}

func printSpecialistOperatorScheduleResult(out interface {
	Write([]byte) (int, error)
}, result application.ExecuteSpecialistOperatorScheduleResult) {
	status := "pending"
	if result.Schedule.ID != "" {
		status = string(result.Schedule.Status)
	}
	fmt.Fprintf(out, "schedule_request: %s\nproposal: %s\nrequested_by: %s\nagents: %s\nmax_rounds: %d\noperator_controlled: true\nschedule: %s\nstatus: %s\nattempt_ordinal: %d\nrounds_completed: %d\nturns_started: %d\nreplayed: %t\nrecovered: %t\n",
		result.Request.ID, result.Request.ProposalID, result.Request.RequestedBy,
		strings.Join(result.Request.AgentIDs, ","), result.Request.MaxRounds,
		result.Schedule.ID, status, result.Attempt.Ordinal,
		result.Schedule.RoundsCompleted, result.Schedule.TurnsStarted,
		result.Replayed, result.Recovered)
}

func (a *App) runExecutionLease(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run lease", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run lease <run-id>")
	}
	_, run, err := service.Get(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	lease, found, err := a.store.GetRunExecutionLease(ctx, run.ID)
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(a.out, "run %s has no execution lease\n", run.ID)
		return nil
	}
	now := time.Now().UTC()
	fmt.Fprintf(a.out, "run: %s\nowner: %s\ngeneration: %d\nstatus: %s\nactive: %t\nacquired_at: %s\nrenewed_at: %s\nexpires_at: %s\n",
		lease.RunID, lease.OwnerID, lease.Generation, lease.Status, lease.ActiveAt(now),
		lease.AcquiredAt.Format(time.RFC3339Nano), lease.RenewedAt.Format(time.RFC3339Nano),
		lease.ExpiresAt.Format(time.RFC3339Nano))
	if lease.ReleasedAt != nil {
		fmt.Fprintf(a.out, "released_at: %s\n", lease.ReleasedAt.Format(time.RFC3339Nano))
	}
	return nil
}

func (a *App) runUsage(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run usage", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run usage <run-id>")
	}
	_, run, err := service.Get(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	toolUsage, err := a.store.GetToolCallUsage(ctx, run.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run: %s\nstatus: %s\ntool_calls: %d\ntool_call_limit: %d\ntool_calls_remaining: %d\n",
		run.ID, run.Status, toolUsage.Consumed, toolUsage.Limit, toolUsage.Remaining)
	if toolUsage.ExhaustedAt != nil {
		fmt.Fprintf(a.out, "tool_budget_exhausted_at: %s\n", toolUsage.ExhaustedAt.Format(time.RFC3339))
	}
	agentUsage, err := a.store.GetRunAgentUsage(ctx, run.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "agent_root_tokens: %d\nagent_specialist_tokens: %d\nagent_readonly_fanout_tokens: %d\nagent_total_tokens: %d\nagent_root_execution_millis: %d\nagent_specialist_execution_millis: %d\nagent_readonly_fanout_millis: %d\nagent_total_execution_millis: %d\n",
		agentUsage.RootTokens, agentUsage.SpecialistTokens,
		agentUsage.ReadOnlyFanoutTokens, agentUsage.TotalTokens,
		agentUsage.RootExecutionMillis, agentUsage.SpecialistExecutionMillis,
		agentUsage.ReadOnlyFanoutMillis, agentUsage.TotalExecutionMillis)
	if checkpoint, ok, err := a.newRunSupervisor().Checkpoint(ctx, run.ID); err != nil {
		return err
	} else if ok {
		fmt.Fprintf(a.out, "turns_completed: %d\ninput_tokens: %d\noutput_tokens: %d\ntotal_tokens: %d\nexecution_millis: %d\n",
			checkpoint.NextTurn-1, checkpoint.InputTokens, checkpoint.OutputTokens,
			checkpoint.TotalTokens, checkpoint.ExecutionMillis)
	}
	monetary, err := a.store.GetMonetaryUsage(ctx, run.ID)
	if err != nil {
		return err
	}
	if monetary.Tracked {
		fmt.Fprintf(a.out, "monetary_cap_usd: %s\nmonetary_reserved_usd: %s\nmonetary_settled_usd: %s\nmonetary_released_usd: %s\nmonetary_remaining_usd: %s\nmonetary_estimate_source: %s\n",
			pricing.MicrosToUSD(monetary.CapMicros),
			pricing.MicrosToUSD(monetary.ReservedMicros),
			pricing.MicrosToUSD(monetary.SettledMicros),
			pricing.MicrosToUSD(monetary.ReleasedMicros),
			pricing.MicrosToUSD(monetary.RemainingMicros),
			monetary.EstimateSource)
		if monetary.ExhaustedAt != nil {
			fmt.Fprintf(a.out, "monetary_budget_exhausted_at: %s\n",
				monetary.ExhaustedAt.Format(time.RFC3339))
		}
	} else {
		fmt.Fprintln(a.out, "monetary_tracked: false")
	}
	return nil
}

func (a *App) runSupervisorStep(ctx context.Context, args []string) (resultErr error) {
	fs := newFlagSet("run step", a.errOut)
	enablePermissionControl := fs.Bool("enable-permission-control", false,
		"enable execution permission evaluation for this process")
	enableFullAccess := fs.Bool("enable-danger-full-access", false,
		"enable the ordinary full-access command runtime for this process")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"enable-permission-control": false, "enable-danger-full-access": false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run step <run-id> [--enable-permission-control --enable-danger-full-access]")
	}
	supervisor := a.newRunSupervisor()
	manager, commandRuntime, err := a.newCLICommandRuntime(ctx,
		*enablePermissionControl, *enableFullAccess)
	if err != nil {
		return err
	}
	if commandRuntime != nil {
		supervisor.WithCommandRuntime(commandRuntime)
	}
	stopReconciler := a.startCLICommandRuntimeReconciler(ctx, commandRuntime)
	defer func() {
		resultErr = errors.Join(resultErr, stopReconciler(),
			shutdownCLICommandRuntime(manager))
	}()
	result, err := supervisor.Step(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run %s turn %d completed\nagent: %s\nattempt: %s\nrecovered: %t\nmodel_attempts: %d\nprotocol_repairs: %d\ntool_rounds: %d\ntool_calls: %d\nstream_events: %d\nstream_bytes: %d\nmodel_outcome: %s\naction: %s\nrun_status: %s\nprovider: %s\nmodel: %s\nusage: input=%d output=%d total=%d\ncumulative_tokens: %d\nexecution_millis: %d\nnext_turn: %d\nresponse: %s\n",
		result.Handle.RunID, result.Turn, result.AgentID, result.AttemptID, result.Recovered, result.ModelAttempts,
		result.ProtocolRepairs, result.ToolRounds, result.ToolCalls, result.StreamEvents, result.StreamBytes,
		result.ModelOutcome, result.Action.Kind, result.RunStatus,
		result.Provider, result.Model,
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.TotalTokens,
		result.Checkpoint.TotalTokens, result.Checkpoint.ExecutionMillis, result.Checkpoint.NextTurn, result.Text)
	return nil
}

func (a *App) runAgentGraph(ctx context.Context, args []string) error {
	fs := newFlagSet("run graph", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run graph <run-id>")
	}
	service := coordinator.New(a.store)
	if _, _, err := service.RegisterRoot(ctx, fs.Arg(0)); err != nil {
		return err
	}
	graph, err := service.Restore(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run: %s\nroot_agent: %s\nnodes: %d\npending_messages: %d\nsnapshot_version: %d\nsnapshot_protocol: %s\n",
		graph.RunID, graph.RootAgentID, len(graph.Nodes), len(graph.PendingMessages),
		graph.LatestSnapshot.Version, graph.LatestSnapshot.ProtocolVersion)
	for _, node := range graph.Nodes {
		fmt.Fprintf(a.out, "%s\trole=%s\tstatus=%s\tprofile=%s\tdepth=%d\tchildren=%d\tturns=%d/%d\ttokens=%d/%d\tversion=%d\n",
			node.ID, node.Role, node.Status, node.Profile, node.Depth, node.ChildLimit,
			node.TurnsUsed, node.TurnLimit, node.TokensUsed, node.TokenLimit, node.Version)
	}
	return nil
}

func (a *App) runSupervisorExecute(ctx context.Context, args []string) (resultErr error) {
	fs := newFlagSet("run execute", a.errOut)
	maxSteps := fs.Int("max-steps", 1, "maximum supervised turns in this invocation")
	finish := fs.Bool("finish", false, "finalize the run as completed after the step limit")
	summary := fs.String("summary", "", "completion summary used with --finish")
	enablePermissionControl := fs.Bool("enable-permission-control", false,
		"enable execution permission evaluation for this process")
	enableFullAccess := fs.Bool("enable-danger-full-access", false,
		"enable the ordinary full-access command runtime for this process")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"max-steps": true, "finish": false, "summary": true,
		"enable-permission-control": false, "enable-danger-full-access": false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 || *maxSteps <= 0 {
		return errors.New("usage: cyberagent run execute <run-id> [--max-steps <n>] [--finish] [--summary <text>] [--enable-permission-control --enable-danger-full-access]")
	}
	supervisor := a.newRunSupervisor()
	manager, commandRuntime, err := a.newCLICommandRuntime(ctx,
		*enablePermissionControl, *enableFullAccess)
	if err != nil {
		return err
	}
	if commandRuntime != nil {
		supervisor.WithCommandRuntime(commandRuntime)
	}
	stopReconciler := a.startCLICommandRuntimeReconciler(ctx, commandRuntime)
	defer func() {
		resultErr = errors.Join(resultErr, stopReconciler(),
			shutdownCLICommandRuntime(manager))
	}()
	result, err := supervisor.Execute(ctx, fs.Arg(0), *maxSteps)
	for _, step := range result.Steps {
		fmt.Fprintf(a.out, "turn %d\t%s\t%s/%s\tattempts=%d\trepairs=%d\ttool_rounds=%d\ttool_calls=%d\tstream_events=%d\ttokens=%d\tnext=%d\n",
			step.Turn, step.Action.Kind, step.Provider, step.Model, step.ModelAttempts, step.ProtocolRepairs,
			step.ToolRounds, step.ToolCalls, step.StreamEvents, step.Usage.TotalTokens, step.Checkpoint.NextTurn)
	}
	if err != nil {
		fmt.Fprintf(a.out, "execution stopped: %s\n", result.StopReason)
		return err
	}
	if *finish {
		if result.RunStatus == domain.RunPaused || result.RunStatus == domain.RunWaitingApproval {
			return apperror.New(apperror.CodeFailedPrecondition, "cannot finalize a waiting run with --finish; resume it or use run fail")
		}
		completionSummary := strings.TrimSpace(*summary)
		if completionSummary == "" {
			completionSummary = fmt.Sprintf("operator finalized after %d supervised turn(s)", len(result.Steps))
		}
		finalized, err := supervisor.Finalize(ctx, fs.Arg(0), application.LifecycleOutcomeCompleted, completionSummary)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "run %s finalized: %s\n", finalized.Run.ID, finalized.Run.Status)
		return nil
	}
	fmt.Fprintf(a.out, "execution stopped: %s\nrun_status: %s\n", result.StopReason, result.RunStatus)
	return nil
}

func (a *App) newCLICommandRuntime(ctx context.Context,
	enablePermissionControl bool, enableFullAccess bool,
) (*runner.CommandRuntimeManager, *application.CommandRuntimeService, error) {
	if !enablePermissionControl && !enableFullAccess {
		return nil, nil, nil
	}
	if !enablePermissionControl || !enableFullAccess {
		return nil, nil, apperror.New(apperror.CodeInvalidArgument,
			"command runtime requires both --enable-permission-control and --enable-danger-full-access")
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true}
	if err := capabilities.Validate(); err != nil {
		return nil, nil, apperror.Wrap(apperror.CodeInvalidArgument,
			"command runtime startup capability is invalid", err)
	}
	manager, err := runner.NewPlatformCommandRuntimeManager(a.store,
		idgen.New("command-runtime-cli-owner"))
	if err != nil {
		return nil, nil, commandRuntimeCLIError(err)
	}
	if _, err := manager.ReconcileStartup(ctx); err != nil {
		_ = shutdownCLICommandRuntime(manager)
		return nil, nil, apperror.Wrap(apperror.CodeUnavailable,
			"command runtime startup reconciliation failed", err)
	}
	service, err := application.NewCommandRuntimeService(a.store, manager, capabilities)
	if err != nil {
		_ = shutdownCLICommandRuntime(manager)
		return nil, nil, err
	}
	return manager, service, nil
}

func shutdownCLICommandRuntime(manager *runner.CommandRuntimeManager) error {
	if manager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	return manager.Shutdown(ctx)
}

func (a *App) startCLICommandRuntimeReconciler(ctx context.Context,
	service *application.CommandRuntimeService,
) func() error {
	if service == nil {
		return func() error { return nil }
	}
	reconcileCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		err := service.RunReconciler(reconcileCtx, 500*time.Millisecond)
		if err != nil &&
			reconcileCtx.Err() == nil {
			fmt.Fprintln(a.errOut, "command-runtime-reconciler:", err)
		}
		done <- err
	}()
	return func() error {
		cancel()
		return <-done
	}
}

func commandRuntimeCLIError(err error) error {
	if errors.Is(err, runner.ErrCommandRuntimeUnavailable) {
		return apperror.Wrap(apperror.CodeUnavailable,
			"command runtime is unavailable on this host", err)
	}
	return apperror.Normalize(err)
}

func (a *App) runSupervisorFinalize(ctx context.Context, outcome application.LifecycleOutcome, args []string) error {
	name := "finish"
	flagName := "summary"
	if outcome == application.LifecycleOutcomeFailed {
		name = "fail"
		flagName = "reason"
	}
	fs := newFlagSet("run "+name, a.errOut)
	text := fs.String(flagName, "", flagName+" text")
	if err := fs.Parse(reorderFlags(args, map[string]bool{flagName: true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cyberagent run %s <run-id> [--%s <text>]", name, flagName)
	}
	result, err := a.newRunSupervisor().Finalize(ctx, fs.Arg(0), outcome, *text)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run %s finalized: %s\nphase: %s\nturns_completed: %d\ntotal_tokens: %d\nexecution_millis: %d\n",
		result.Run.ID, result.Run.Status, result.Checkpoint.Phase, result.Checkpoint.NextTurn-1,
		result.Checkpoint.TotalTokens, result.Checkpoint.ExecutionMillis)
	return nil
}

func (a *App) runSupervisorCheckpoint(ctx context.Context, args []string) error {
	fs := newFlagSet("run checkpoint", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run checkpoint <run-id>")
	}
	checkpoint, ok, err := a.newRunSupervisor().Checkpoint(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(a.out, "run %s has no supervisor checkpoint\n", fs.Arg(0))
		return nil
	}
	toolUsage, err := a.store.GetToolCallUsage(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run: %s\nphase: %s\nnext_turn: %d\nattempt: %s\nrepair_phase: %s\nrepair_reason: %s\nlast_error: %s\ninput_tokens: %d\noutput_tokens: %d\ntotal_tokens: %d\ntool_calls: %d\ntool_call_limit: %d\ntool_calls_remaining: %d\nexecution_millis: %d\nupdated_at: %s\n",
		checkpoint.RunID, checkpoint.Phase, checkpoint.NextTurn, checkpoint.AttemptID,
		checkpoint.RepairPhase, checkpoint.RepairReason, checkpoint.LastError, checkpoint.InputTokens, checkpoint.OutputTokens,
		checkpoint.TotalTokens, toolUsage.Consumed, toolUsage.Limit, toolUsage.Remaining,
		checkpoint.ExecutionMillis, checkpoint.UpdatedAt.Format(time.RFC3339))
	return nil
}

func (a *App) runAdaptTask(ctx context.Context, args []string) error {
	fs := newFlagSet("run adapt-task", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run adapt-task <task-id>")
	}
	result, err := application.NewTaskAdapter(a.store).Adapt(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	action := "reused"
	if result.Created {
		action = "adapted"
	}
	fmt.Fprintf(a.out, "task %s %s\nmission: %s\nrun: %s\nsession: %s\nstatus: %s\nprofile: %s\n",
		result.Source.ID, action, result.Mission.ID, result.Run.ID, result.Run.SessionID, result.Run.Status, result.Mission.Profile)
	return nil
}

func (a *App) runCreate(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run create", a.errOut)
	workspaceName := fs.String("workspace", "", "workspace name")
	profile := fs.String("profile", string(domain.ProfileCode), "mission profile")
	surface := fs.String("surface", string(domain.ExecutionSurfaceCode), "execution surface: code or cyber")
	phase := fs.String("phase", string(domain.ExecutionPhaseDeliver), "execution phase: plan or deliver")
	route := fs.String("route", "", "model route")
	sessionID := fs.String("session", "", "existing session id")
	interactive := fs.Bool("interactive", false, "mark run as interactive")
	maxTurns := fs.Int("max-turns", domain.DefaultBudget().MaxTurns, "maximum agent turns")
	maxTokens := fs.Int64("max-tokens", 0, "maximum model tokens; zero means unset")
	maxToolCalls := fs.Int64("max-tool-calls", domain.DefaultBudget().MaxToolCalls, "maximum tool calls; zero means unlimited")
	maxCostUSD := fs.Float64("max-cost-usd", 0, "maximum model cost in USD; zero means unset")
	timeout := fs.Duration("timeout", 0, "run timeout; zero means unset")
	ignoreProjectConfig := fs.Bool("ignore-project-config", false,
		"skip the .prayu/config.yaml narrowing snapshot for this Run")
	instructionTarget := fs.String("instruction-target", ".",
		"workspace-relative file or directory used for hierarchical project instructions")
	ignoreProjectInstructions := fs.Bool("ignore-project-instructions", false,
		"skip hierarchical project-instruction discovery for this Run")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"workspace":                   true,
		"profile":                     true,
		"surface":                     true,
		"phase":                       true,
		"route":                       true,
		"session":                     true,
		"interactive":                 false,
		"max-turns":                   true,
		"max-tokens":                  true,
		"max-tool-calls":              true,
		"max-cost-usd":                true,
		"timeout":                     true,
		"ignore-project-config":       false,
		"instruction-target":          true,
		"ignore-project-instructions": false,
	})); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New(`usage: cyberagent run create "goal" [--workspace <name>] [--profile code|review|learn|script] [--surface code|cyber] [--phase plan|deliver]`)
	}
	workspaceID := ""
	var workspaceRecord *store.WorkspaceRecord
	if strings.TrimSpace(*workspaceName) != "" {
		rec, err := a.store.GetWorkspaceByName(ctx, workspace.Slug(*workspaceName))
		if err != nil {
			return err
		}
		workspaceRecord = &rec
		workspaceID = rec.ID
	}
	if strings.TrimSpace(*sessionID) != "" {
		sess, err := a.store.GetSession(ctx, strings.TrimSpace(*sessionID))
		if err != nil {
			return err
		}
		if workspaceID != "" && sess.WorkspaceID != "" && workspaceID != sess.WorkspaceID {
			return errors.New("session and requested workspace do not match")
		}
		if workspaceID == "" {
			workspaceID = sess.WorkspaceID
		}
		if workspaceRecord == nil && workspaceID != "" {
			rec, err := a.store.GetWorkspaceByID(ctx, workspaceID)
			if err != nil {
				return err
			}
			workspaceRecord = &rec
		}
	}
	var projectConfig *projectconfig.Effective
	var projectInstructions *projectconfig.InstructionSnapshot
	if workspaceRecord != nil && !*ignoreProjectConfig {
		config, found, err := projectconfig.LoadWorkspace(ctx, workspaceRecord.RootPath)
		if err != nil {
			return fmt.Errorf("project config fail-closed: %w", err)
		}
		if found {
			effective, rejections, err := config.Narrow(projectconfig.Ceiling{
				AllowedProfiles:    []string{*profile},
				MaxTurns:           *maxTurns,
				MaxToolCalls:       int(*maxToolCalls),
				RegisteredCommands: toolgateway.TypedActionIDs(),
			})
			if err != nil {
				return fmt.Errorf("project config fail-closed: %w", err)
			}
			if len(rejections) > 0 {
				return fmt.Errorf("project config rejection: field=%s reason=%s",
					rejections[0].Field, rejections[0].Reason)
			}
			projectConfig = &effective
		}
	}
	if workspaceRecord != nil && !*ignoreProjectInstructions {
		discovered, err := projectconfig.DiscoverInstructions(ctx, workspaceRecord.RootPath,
			*instructionTarget)
		if err != nil {
			return fmt.Errorf("project instruction discovery fail-closed: %w", err)
		}
		projectInstructions = &discovered
	}
	mission, run, err := service.Create(ctx, application.CreateRunRequest{
		Goal:        strings.Join(fs.Args(), " "),
		Profile:     *profile,
		Surface:     *surface,
		Phase:       *phase,
		WorkspaceID: workspaceID,
		SessionID:   *sessionID,
		ModelRoute:  *route,
		Interactive: *interactive,
		Budget: domain.Budget{
			MaxTurns:       *maxTurns,
			MaxTokens:      *maxTokens,
			MaxToolCalls:   *maxToolCalls,
			MaxCostUSD:     *maxCostUSD,
			TimeoutSeconds: int64(timeout.Seconds()),
		},
		ProjectConfig:       projectConfig,
		ProjectInstructions: projectInstructions,
		RequestedBy:         "cli_operator",
	})
	if err != nil {
		return err
	}
	mode, err := service.Mode(ctx, run.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run %s created\nmission: %s\nsession: %s\nstatus: %s\nprofile: %s\nsurface: %s\nphase: %s\nmode_revision: %d\nworkspace: %s\nroute: %s\nproject_instructions: %s\n",
		run.ID, mission.ID, run.SessionID, run.Status, mission.Profile, mode.Surface,
		mode.Phase, mode.Revision, mission.WorkspaceID, run.Config.ModelRoute,
		run.Config.ProjectInstructionsFingerprint)
	return nil
}

func (a *App) runList(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run list", a.errOut)
	statusValue := fs.String("status", "", "run status")
	missionID := fs.String("mission", "", "mission id")
	limit := fs.Int("limit", 100, "maximum rows")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"status": true, "mission": true, "limit": true})); err != nil {
		return err
	}
	status := domain.RunStatus(strings.TrimSpace(*statusValue))
	if status != "" && !domain.ValidRunStatus(status) {
		return fmt.Errorf("invalid run status %q", status)
	}
	if *limit <= 0 || *limit > 1000 {
		return errors.New("run list limit must be between 1 and 1000")
	}
	runs, err := service.List(ctx, domain.RunFilter{MissionID: *missionID, Status: status, Limit: *limit})
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Fprintln(a.out, "no runs")
		return nil
	}
	for _, run := range runs {
		mode, err := service.Mode(ctx, run.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s\t%s\t%s/%s\t%s\t%s\t%s\n", run.ID, run.Status,
			mode.Surface, mode.Phase, run.MissionID, run.Config.ModelRoute,
			run.UpdatedAt.Format(time.RFC3339))
	}
	return nil
}

func (a *App) runShow(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run show", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run show <run-id>")
	}
	mission, run, err := service.Get(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	mode, err := service.Mode(ctx, run.ID)
	if err != nil {
		return err
	}
	permission, err := a.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return err
	}
	rootAgent, found, err := a.store.GetRootAgent(ctx, run.ID)
	if err != nil {
		return err
	}
	unavailableReason := ""
	if !found {
		unavailableReason = "Run root Agent is unavailable"
		rootAgent.Role = domain.AgentRoleRoot
		rootAgent.Profile = mode.Profile
	}
	rootFingerprint := ""
	if mission.WorkspaceID == "" {
		unavailableReason = "Run has no registered Workspace"
	} else {
		registered, lookupErr := a.store.GetWorkspaceInfo(ctx, mission.WorkspaceID)
		if lookupErr != nil {
			return lookupErr
		}
		rootFingerprint, err = workspace.AgentCodeRootFingerprint(registered.RootPath)
		if err != nil {
			unavailableReason = "registered Workspace root is unavailable"
		}
	}
	agentCodeTools := toolgateway.AgentCodeCapabilities(toolgateway.AgentCodeCapabilityContext{
		RunID: run.ID, MissionID: mission.ID, RootAgentID: rootAgent.ID,
		WorkspaceID: mission.WorkspaceID, RootFingerprint: rootFingerprint,
		Surface: mode.Surface, Phase: mode.Phase, Role: rootAgent.Role,
		Profile: rootAgent.Profile, PermissionMode: permission.Mode,
		ModeRevision: mode.Revision, PermissionRevision: permission.Revision,
		UnavailableReason: unavailableReason})
	scope, _ := json.Marshal(mission.Scope)
	budget, _ := json.Marshal(run.Budget)
	fmt.Fprintf(a.out, "id: %s\nmission: %s\nstatus: %s\ngoal: %s\nprofile: %s\nsurface: %s\nphase: %s\nmode_revision: %d\nmode_policy: %s\nworkspace: %s\nsession: %s\nroute: %s\ninteractive: %t\nscope: %s\nbudget: %s\ncreated_at: %s\nupdated_at: %s\n",
		run.ID, mission.ID, run.Status, mission.Goal, mission.Profile, mode.Surface,
		mode.Phase, mode.Revision, mode.PolicyVersion, mission.WorkspaceID, run.SessionID,
		run.Config.ModelRoute, run.Config.Interactive, scope, budget, run.CreatedAt.Format(time.RFC3339), run.UpdatedAt.Format(time.RFC3339))
	if run.StartedAt != nil {
		fmt.Fprintf(a.out, "started_at: %s\n", run.StartedAt.Format(time.RFC3339))
	}
	if run.FinishedAt != nil {
		fmt.Fprintf(a.out, "finished_at: %s\n", run.FinishedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(a.out, "agent_code_tools_protocol: %s\nagent_code_tools_generation: %s\n",
		agentCodeTools.ProtocolVersion, agentCodeTools.Generation)
	for _, capability := range agentCodeTools.Tools {
		fmt.Fprintf(a.out, "agent_code_tool: %s\tavailable=%t\tclass=%s\tapproval=%s\tsource=%s",
			capability.Name, capability.Available, capability.Class, capability.Approval,
			capability.Source)
		if capability.Refusal != "" {
			fmt.Fprintf(a.out, "\treason=%s", capability.Refusal)
		}
		fmt.Fprintln(a.out)
	}
	return nil
}

func (a *App) runMode(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run mode", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run mode <run-id>")
	}
	mode, err := service.Mode(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run: %s\nmission: %s\nprotocol: %s\nrevision: %d\nsurface: %s\nphase: %s\nprofile: %s\npolicy: %s\nnetwork_mode: %s\nallowed_target_count: %d\nrequested_by: %s\nreason: %s\ncreated_at: %s\ncapability_grant: false\n",
		mode.RunID, mode.MissionID, mode.ProtocolVersion, mode.Revision, mode.Surface,
		mode.Phase, mode.Profile, mode.PolicyVersion, mode.Scope.NetworkMode,
		len(mode.Scope.AllowedTargets), mode.RequestedBy, mode.Reason,
		mode.CreatedAt.Format(time.RFC3339Nano))
	return nil
}

func (a *App) runPhase(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run phase", a.errOut)
	operationKey := fs.String("operation-key", "", "stable phase transition operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	reason := fs.String("reason", "", "redacted transition reason")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "operator": true, "reason": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run phase <run-id> plan|deliver --operation-key <key> [--operator <id>] [--reason <text>]")
	}
	result, err := service.ChangePhase(ctx, application.ChangeRunPhaseRequest{
		RunID: fs.Arg(0), Phase: fs.Arg(1), OperationKey: *operationKey,
		RequestedBy: *operator, Reason: *reason,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run: %s\nsurface: %s\nphase: %s\nrevision: %d\npolicy: %s\nrequested_by: %s\nreplayed: %t\ncapability_grant: false\n",
		result.Mode.RunID, result.Mode.Surface, result.Mode.Phase, result.Mode.Revision,
		result.Mode.PolicyVersion, result.Mode.RequestedBy, result.Replayed)
	return nil
}

func (a *App) runExecutionProfile(ctx context.Context, args []string) error {
	service := application.NewRunExecutionProfileService(a.store)
	if len(args) == 1 {
		profile, err := service.Current(ctx, args[0])
		if err != nil {
			return err
		}
		writeRunExecutionProfile(a.out, profile, false)
		return nil
	}
	if len(args) == 0 || args[0] != "set" {
		return errors.New("usage: cyberagent run execution-profile <run-id> | cyberagent run execution-profile set <run-id> preview|docker|local --operation-key <key> [--operator <id>] [--reason <text>]")
	}
	fs := newFlagSet("run execution-profile set", a.errOut)
	operationKey := fs.String("operation-key", "", "stable execution-profile operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	reason := fs.String("reason", "", "redacted selection reason")
	if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
		"operation-key": true, "operator": true, "reason": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run execution-profile set <run-id> preview|docker|local --operation-key <key> [--operator <id>] [--reason <text>]")
	}
	result, err := service.Change(ctx, application.ChangeRunExecutionProfileRequest{
		RunID: fs.Arg(0), Profile: fs.Arg(1), OperationKey: *operationKey,
		RequestedBy: *operator, Reason: *reason,
	})
	if err != nil {
		return err
	}
	writeRunExecutionProfile(a.out, result.Profile, result.Replayed)
	return nil
}

func writeRunExecutionProfile(out interface{ Write([]byte) (int, error) },
	profile domain.RunExecutionProfileSnapshot, replayed bool,
) {
	fmt.Fprintf(out, "run: %s\nmission: %s\nprotocol: %s\nrevision: %d\nprofile: %s\nbackend: %s\napproval_policy: %s\nfilesystem_scope: %s\nnetwork_scope: %s\nrisk_tier: %s\nrequired_gate: %s\npolicy: %s\nrequested_by: %s\nreason: %s\ncreated_at: %s\nprocess_enabled: false\nexecution_authorized: false\ncapability_grant: false\nreplayed: %t\n",
		profile.RunID, profile.MissionID, profile.ProtocolVersion, profile.Revision,
		profile.Profile, profile.Backend, profile.ApprovalPolicy, profile.FilesystemScope,
		profile.NetworkScope, profile.RiskTier, profile.RequiredGate, profile.PolicyVersion,
		profile.RequestedBy, profile.Reason, profile.CreatedAt.Format(time.RFC3339Nano), replayed)
}

func (a *App) runExecutionInteraction(ctx context.Context, args []string) error {
	service := application.NewRunExecutionInteractionService(a.store)
	if len(args) == 1 {
		interaction, err := service.Current(ctx, args[0])
		if err != nil {
			return err
		}
		writeRunExecutionInteraction(a.out, interaction, false)
		return nil
	}
	if len(args) == 0 || args[0] != "set" {
		return errors.New("usage: cyberagent run execution-interaction <run-id> | cyberagent run execution-interaction set <run-id> preview|controlled|debug|cyber --operation-key <key> [--trust untrusted|trusted] [--confirm-workspace-trust] [--confirm-debug-boundary|--confirm-container-boundary] [--operator <id>] [--reason <text>]")
	}
	fs := newFlagSet("run execution-interaction set", a.errOut)
	operationKey := fs.String("operation-key", "",
		"stable execution-interaction operation key")
	trust := fs.String("trust", "", "Workspace trust level")
	operator := fs.String("operator", "cli_operator", "operator identity")
	reason := fs.String("reason", "", "redacted selection reason")
	confirmWorkspace := fs.Bool("confirm-workspace-trust", false,
		"confirm that the registered Workspace is trusted")
	confirmDebug := fs.Bool("confirm-debug-boundary", false,
		"confirm the user-terminal-first debug boundary")
	confirmContainer := fs.Bool("confirm-container-boundary", false,
		"confirm the isolated Cyber container boundary")
	if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
		"operation-key": true, "trust": true, "operator": true, "reason": true,
		"confirm-workspace-trust": false, "confirm-debug-boundary": false,
		"confirm-container-boundary": false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run execution-interaction set <run-id> preview|controlled|debug|cyber --operation-key <key> [--trust untrusted|trusted] [--confirm-workspace-trust] [--confirm-debug-boundary|--confirm-container-boundary] [--operator <id>] [--reason <text>]")
	}
	result, err := service.Change(ctx,
		application.ChangeRunExecutionInteractionRequest{
			RunID: fs.Arg(0), Mode: fs.Arg(1), Trust: *trust,
			OperationKey: *operationKey, RequestedBy: *operator, Reason: *reason,
			ConfirmWorkspaceTrust:    *confirmWorkspace,
			ConfirmDebugBoundary:     *confirmDebug,
			ConfirmContainerBoundary: *confirmContainer,
		})
	if err != nil {
		return err
	}
	writeRunExecutionInteraction(a.out, result.Interaction, result.Replayed)
	return nil
}

func writeRunExecutionInteraction(out interface{ Write([]byte) (int, error) },
	interaction domain.RunExecutionInteractionSnapshot, replayed bool,
) {
	fmt.Fprintf(out, "run: %s\nmission: %s\nprotocol: %s\nrevision: %d\nmode: %s\nsurface: %s\nexecution_profile: %s\nexecution_profile_revision: %d\nworkspace_trust: %s\ncommand_form: %s\npersistent_terminal: %t\nuser_input_available: %t\nagent_input_default: false\nnetwork_scope: %s\nrequired_gate: %s\npolicy: %s\noperator_confirmed: %t\nrequested_by: %s\nreason: %s\ncreated_at: %s\nprocess_enabled: false\nexecution_authorized: false\ncapability_grant: false\nreplayed: %t\n",
		interaction.RunID, interaction.MissionID, interaction.ProtocolVersion,
		interaction.Revision, interaction.Mode, interaction.Surface,
		interaction.ExecutionProfile, interaction.ExecutionProfileRevision,
		interaction.WorkspaceTrust, interaction.CommandForm,
		interaction.PersistentTerminal, interaction.UserInputAvailable,
		interaction.NetworkScope, interaction.RequiredGate,
		interaction.PolicyVersion, interaction.OperatorConfirmed,
		interaction.RequestedBy, interaction.Reason,
		interaction.CreatedAt.Format(time.RFC3339Nano), replayed)
}

func (a *App) runExecutionPermission(ctx context.Context, args []string) error {
	if len(args) == 1 {
		capabilities := domain.ExecutionPermissionRuntimeCapabilities{}
		service := application.NewRunExecutionPermissionService(a.store, capabilities)
		permission, err := service.Current(ctx, args[0])
		if err != nil {
			return err
		}
		writeRunExecutionPermission(a.out, permission, false,
			capabilities.Allows(permission.Mode))
		return nil
	}
	if len(args) == 0 || args[0] != "set" {
		return errors.New("usage: cyberagent run execution-permission <run-id> | cyberagent run execution-permission set <run-id> conservative|approval|full_access|debug --operation-key <key> [--confirm-user-approval|--confirm-danger-full-access|--confirm-debug-access] [--enable-permission-control] [--enable-danger-full-access] [--enable-debug-maximum-access] [--operator <id>] [--reason <text>]")
	}
	fs := newFlagSet("run execution-permission set", a.errOut)
	operationKey := fs.String("operation-key", "",
		"stable execution-permission operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	reason := fs.String("reason", "", "redacted selection reason")
	confirmApproval := fs.Bool("confirm-user-approval", false,
		"confirm exact per-command operator approval")
	confirmFull := fs.Bool("confirm-danger-full-access", false,
		"confirm unsandboxed one-shot host access")
	confirmDebug := fs.Bool("confirm-debug-access", false,
		"confirm persistent maximum-access debug capabilities")
	enableControl := fs.Bool("enable-permission-control", false,
		"enable permission elevation for this process")
	enableFull := fs.Bool("enable-danger-full-access", false,
		"enable danger-full-access for this process")
	enableDebug := fs.Bool("enable-debug-maximum-access", false,
		"enable maximum debug access for this process")
	if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
		"operation-key": true, "operator": true, "reason": true,
		"confirm-user-approval": false, "confirm-danger-full-access": false,
		"confirm-debug-access": false, "enable-permission-control": false,
		"enable-danger-full-access": false, "enable-debug-maximum-access": false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run execution-permission set <run-id> conservative|approval|full_access|debug --operation-key <key> [--confirm-user-approval|--confirm-danger-full-access|--confirm-debug-access] [--enable-permission-control] [--enable-danger-full-access] [--enable-debug-maximum-access] [--operator <id>] [--reason <text>]")
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:   *enableControl,
		DangerFullAccessEnabled:   *enableFull,
		DebugMaximumAccessEnabled: *enableDebug,
	}
	if err := capabilities.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			err.Error(), err)
	}
	service := application.NewRunExecutionPermissionService(a.store, capabilities)
	result, err := service.Change(ctx,
		application.ChangeRunExecutionPermissionRequest{
			RunID: fs.Arg(0), Mode: fs.Arg(1), OperationKey: *operationKey,
			RequestedBy: *operator, Reason: *reason,
			ConfirmUserApproval:     *confirmApproval,
			ConfirmDangerFullAccess: *confirmFull,
			ConfirmDebugAccess:      *confirmDebug,
		})
	if err != nil {
		return err
	}
	writeRunExecutionPermission(a.out, result.Permission, result.Replayed,
		capabilities.Allows(result.Permission.Mode))
	return nil
}

func writeRunExecutionPermission(out interface{ Write([]byte) (int, error) },
	permission domain.RunExecutionPermissionSnapshot, replayed bool,
	runtimeGateAvailable bool,
) {
	fmt.Fprintf(out, "run: %s\nmission: %s\nprotocol: %s\nrevision: %d\nmode: %s\napproval_policy: %s\ncommand_scope: %s\nfilesystem_scope: %s\nnetwork_scope: %s\npersistent_terminal: %t\nbackground_process: %t\nagent_terminal_input: %t\nrisk_tier: %s\nrequired_gate: %s\npolicy: %s\noperator_confirmed: %t\nrequested_by: %s\nreason: %s\ncreated_at: %s\nruntime_gate_available: %t\nprocess_enabled: false\nexecution_authorized: false\ncapability_grant: false\nreplayed: %t\n",
		permission.RunID, permission.MissionID, permission.ProtocolVersion,
		permission.Revision, permission.Mode, permission.ApprovalPolicy,
		permission.CommandScope, permission.FilesystemScope, permission.NetworkScope,
		permission.PersistentTerminal, permission.BackgroundProcess,
		permission.AgentTerminalInput, permission.RiskTier, permission.RequiredGate,
		permission.PolicyVersion, permission.OperatorConfirmed, permission.RequestedBy,
		permission.Reason, permission.CreatedAt.Format(time.RFC3339Nano),
		runtimeGateAvailable, replayed)
}

func (a *App) runBrowserCDPPermission(ctx context.Context, args []string) error {
	if len(args) == 1 {
		capabilities := domain.BrowserCDPPermissionRuntimeCapabilities{}
		service := application.NewRunBrowserCDPPermissionService(a.store, capabilities)
		permission, err := service.Current(ctx, args[0])
		if err != nil {
			return err
		}
		writeRunBrowserCDPPermission(a.out, permission, false,
			capabilities.Allows(permission.Mode))
		return nil
	}
	if len(args) == 0 || args[0] != "set" {
		return errors.New("usage: cyberagent run browser-cdp-permission <run-id> | cyberagent run browser-cdp-permission set <run-id> restricted|full_debug --operation-key <key> [--confirm-full-cdp-debug] [--enable-browser-cdp-control] [--enable-full-cdp-debug --enable-permission-control --enable-danger-full-access --enable-debug-maximum-access] [--operator <id>] [--reason <text>]")
	}
	fs := newFlagSet("run browser-cdp-permission set", a.errOut)
	operationKey := fs.String("operation-key", "", "stable browser-CDP operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	reason := fs.String("reason", "", "redacted selection reason")
	confirmFull := fs.Bool("confirm-full-cdp-debug", false,
		"confirm highly sensitive complete CDP debugging")
	enableControl := fs.Bool("enable-browser-cdp-control", false,
		"enable browser CDP permission control for this process")
	enableFull := fs.Bool("enable-full-cdp-debug", false,
		"enable complete CDP debug selection for this process")
	enablePermissionControl := fs.Bool("enable-permission-control", false,
		"enable execution permission control for this process")
	enableDangerFullAccess := fs.Bool("enable-danger-full-access", false,
		"enable danger-full-access for this process")
	enableDebugMaximumAccess := fs.Bool("enable-debug-maximum-access", false,
		"enable maximum Debug access for this process")
	if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
		"operation-key": true, "operator": true, "reason": true,
		"confirm-full-cdp-debug": false, "enable-browser-cdp-control": false,
		"enable-full-cdp-debug": false, "enable-permission-control": false,
		"enable-danger-full-access": false, "enable-debug-maximum-access": false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run browser-cdp-permission set <run-id> restricted|full_debug --operation-key <key> [--confirm-full-cdp-debug] [--enable-browser-cdp-control] [--enable-full-cdp-debug --enable-permission-control --enable-danger-full-access --enable-debug-maximum-access] [--operator <id>] [--reason <text>]")
	}
	executionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:   *enablePermissionControl,
		DangerFullAccessEnabled:   *enableDangerFullAccess,
		DebugMaximumAccessEnabled: *enableDebugMaximumAccess,
	}
	if err := executionCapabilities.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	if *enableFull && !executionCapabilities.DebugMaximumAccessEnabled {
		return apperror.New(apperror.CodeInvalidArgument,
			"full CDP debug requires --enable-permission-control, --enable-danger-full-access, and --enable-debug-maximum-access")
	}
	capabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: *enableControl, FullDebugEnabled: *enableFull,
	}
	if err := capabilities.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	service := application.NewRunBrowserCDPPermissionService(a.store, capabilities)
	result, err := service.Change(ctx, application.ChangeRunBrowserCDPPermissionRequest{
		RunID: fs.Arg(0), Mode: fs.Arg(1), OperationKey: *operationKey,
		RequestedBy: *operator, Reason: *reason,
		ConfirmFullCDPDebug: *confirmFull,
	})
	if err != nil {
		return err
	}
	writeRunBrowserCDPPermission(a.out, result.Permission, result.Replayed,
		capabilities.Allows(result.Permission.Mode))
	return nil
}

func writeRunBrowserCDPPermission(out interface{ Write([]byte) (int, error) },
	permission domain.RunBrowserCDPPermissionSnapshot, replayed bool,
	runtimeGateAvailable bool,
) {
	fmt.Fprintf(out, "run: %s\nmission: %s\nprotocol: %s\nrevision: %d\nmode: %s\nnavigate_allowed: %t\ndom_snapshot_allowed: %t\nscreenshot_allowed: %t\nrequest_capture_allowed: %t\nrequest_mutation_allowed: %t\nrequest_replay_allowed: %t\ncookie_access_allowed: %t\narbitrary_method_allowed: %t\nrisk_tier: %s\nrequired_gate: %s\npolicy: %s\noperator_confirmed: %t\nrequested_by: %s\nreason: %s\ncreated_at: %s\nruntime_gate_available: %t\ntransport_enabled: false\nbrowser_start_authorized: false\nruntime_authorized: false\ncapability_grant: false\nreplayed: %t\n",
		permission.RunID, permission.MissionID, permission.ProtocolVersion,
		permission.Revision, permission.Mode, permission.NavigateAllowed,
		permission.DOMSnapshotAllowed, permission.ScreenshotAllowed,
		permission.RequestCaptureAllowed, permission.RequestMutationAllowed,
		permission.RequestReplayAllowed, permission.CookieAccessAllowed,
		permission.ArbitraryMethodAllowed, permission.RiskTier,
		permission.RequiredGate, permission.PolicyVersion,
		permission.OperatorConfirmed, permission.RequestedBy, permission.Reason,
		permission.CreatedAt.Format(time.RFC3339Nano), runtimeGateAvailable, replayed)
}

func (a *App) runCommandPlan(ctx context.Context, args []string) error {
	fs := newFlagSet("run command-plan", a.errOut)
	relativePath := fs.String("path", "",
		"Workspace-relative path for PowerShell list")
	timeout := fs.Duration("timeout", runner.DefaultControlledCommandTimeout,
		"one-shot command timeout")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"path": true, "timeout": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: cyberagent run command-plan <run-id> git-status|git-diff-check|go-version|powershell-workspace-list [--path <relative>] [--timeout <duration>]")
	}
	kind, err := runner.ParseControlledCommandKind(fs.Arg(1))
	if err != nil {
		return err
	}
	runRecord, err := a.store.GetRun(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	mission, err := a.store.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(mission.WorkspaceID) == "" {
		return apperror.New(apperror.CodeFailedPrecondition,
			"controlled command planning requires a registered Workspace")
	}
	workspaceRecord, err := a.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return err
	}
	interaction, err := a.store.GetRunExecutionInteraction(ctx, runRecord.ID)
	if err != nil {
		return err
	}
	profile, err := a.store.GetRunExecutionProfile(ctx, runRecord.ID)
	if err != nil {
		return err
	}
	mode, err := a.store.GetRunMode(ctx, runRecord.ID)
	if err != nil {
		return err
	}
	plan, err := runner.PlanControlledCommand(runner.ControlledCommandPlanRequest{
		ID:          idgen.New("controlled-command-plan"),
		WorkspaceID: mission.WorkspaceID, WorkspaceRoot: workspaceRecord.RootPath,
		Interaction: interaction, CurrentProfile: profile,
		CurrentSurface: mode.Surface, Kind: kind, RelativePath: *relativePath,
		Timeout: *timeout,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "plan: %s\nprotocol: %s\npolicy: %s\nrun: %s\nworkspace: %s\ninteraction_snapshot: %s\ninteraction_revision: %d\nexecution_profile_revision: %d\nkind: %s\nexecutable_id: %s\nargument_count: %d\nrelative_path: %s\ntimeout_millis: %d\nworking_directory_bound: true\nstdin_closed: true\nenvironment_inherited: false\nprofile_loading_enabled: false\npersistent_process: false\ncaller_shell_text_accepted: false\ngo_owned_powershell_script: %t\nnetwork_requested: false\nos_sandbox_required: true\nstart_blocked: true\nproduct_execution_enabled: false\nfingerprint: %s\n",
		plan.ID, plan.ProtocolVersion, plan.PolicyVersion, plan.RunID,
		plan.WorkspaceID, plan.InteractionSnapshotID, plan.InteractionRevision,
		plan.ExecutionProfileRevision, plan.Kind, plan.ExecutableID, len(plan.Argv),
		plan.RelativePath, plan.TimeoutMilliseconds, plan.GoOwnedPowerShellScript,
		plan.Fingerprint)
	return nil
}

func (a *App) runCommandExecute(ctx context.Context, args []string) error {
	fs := newFlagSet("run command-execute", a.errOut)
	relativePath := fs.String("path", "",
		"Workspace-relative path for PowerShell list")
	timeout := fs.Duration("timeout", runner.DefaultControlledCommandTimeout,
		"one-shot command timeout")
	operationKey := fs.String("operation-key", "",
		"stable operator-owned execution operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	confirm := fs.Bool("confirm-execution", false,
		"confirm one controlled OS-restricted process")
	enablePermissionControl := fs.Bool("enable-permission-control", false,
		"enable elevated permission evaluation for this process")
	enableFullAccess := fs.Bool("enable-danger-full-access", false,
		"enable danger-full-access evaluation for this process")
	enableDebugAccess := fs.Bool("enable-debug-maximum-access", false,
		"enable maximum debug evaluation for this process")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"path": true, "timeout": true, "operation-key": true,
		"operator": true, "confirm-execution": false,
		"enable-permission-control": false, "enable-danger-full-access": false,
		"enable-debug-maximum-access": false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 2 || !domain.ValidAgentID(strings.TrimSpace(*operationKey)) ||
		!domain.ValidAgentID(strings.TrimSpace(*operator)) || !*confirm {
		return apperror.New(apperror.CodeInvalidArgument,
			"usage: cyberagent run command-execute <run-id> git-status|git-diff-check|go-version|powershell-workspace-list --operation-key <key> --confirm-execution [--path <relative>] [--timeout <duration>] [--operator <id>]")
	}
	kind, err := runner.ParseControlledCommandKind(fs.Arg(1))
	if err != nil {
		return err
	}
	runtimeCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:   *enablePermissionControl,
		DangerFullAccessEnabled:   *enableFullAccess,
		DebugMaximumAccessEnabled: *enableDebugAccess,
	}
	if err := runtimeCapabilities.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			err.Error(), err)
	}
	runRecord, mission, workspaceRecord, interaction, profile, permission, mode, err :=
		a.loadControlledCommandBindings(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	permissionDecision, err := executionauth.EvaluateExecutionPermission(
		permission, runtimeCapabilities, executionauth.PermissionRequest{
			Kind:             executionauth.PermissionOperationFixedTemplate,
			OperatorApproved: true,
		})
	if err != nil {
		return err
	}
	if !permissionDecision.Allowed {
		return apperror.New(apperror.CodePolicyDenied, permissionDecision.Reason)
	}
	operationDigest := runmutation.Fingerprint(
		"controlled_command_execution_operation.v1", runRecord.ID,
		strings.TrimSpace(*operationKey))
	plan, err := runner.PlanControlledCommand(runner.ControlledCommandPlanRequest{
		ID:          "controlled-command-plan-" + operationDigest[:24],
		WorkspaceID: mission.WorkspaceID, WorkspaceRoot: workspaceRecord.RootPath,
		Interaction: interaction, CurrentProfile: profile,
		CurrentSurface: mode.Surface, Kind: kind, RelativePath: *relativePath,
		Timeout: *timeout,
	})
	if err != nil {
		return err
	}
	executor, err := a.controlledCommandExecutor()
	if err != nil {
		return err
	}
	if !executor.Available() {
		return runner.ErrControlledExecutionPlatform
	}
	intent, err := runner.NewControlledExecutionIntent(plan,
		strings.TrimSpace(*operator), time.Now().UTC())
	if err != nil {
		return err
	}
	replayed, err := a.store.PrepareControlledExecutionIntent(ctx, intent)
	if err != nil {
		return err
	}
	if replayed {
		receipt, found, err := a.store.GetControlledExecutionReceipt(ctx,
			intent.RequestID)
		if err != nil {
			return err
		}
		if !found {
			return apperror.New(apperror.CodeFailedPrecondition,
				"controlled command execution has a prepared intent without a receipt; automatic retry is disabled")
		}
		return writeControlledExecutionReceipt(a.out, receipt, true, false)
	}
	result, executeErr := executor.Execute(ctx, runner.ControlledExecutionRequest{
		Plan: plan, WorkspaceRoot: workspaceRecord.RootPath,
		Interaction: interaction, CurrentProfile: profile,
		CurrentSurface: mode.Surface, RequestedBy: strings.TrimSpace(*operator),
		OperatorConfirmed: true,
	})
	if validationErr := result.Validate(); validationErr != nil {
		if executeErr != nil {
			return errors.Join(executeErr, validationErr)
		}
		return validationErr
	}
	receipt, _, recordErr := a.store.RecordControlledExecutionResult(ctx, result)
	if recordErr != nil {
		return recordErr
	}
	if err := writeControlledExecutionReceipt(a.out, receipt, false, true); err != nil {
		return err
	}
	if err := writeTransientControlledOutput(a.out, "stdout", result.Stdout.Data); err != nil {
		return err
	}
	if err := writeTransientControlledOutput(a.out, "stderr", result.Stderr.Data); err != nil {
		return err
	}
	return executeErr
}

func (a *App) runHostExecute(ctx context.Context, args []string) error {
	fs := newFlagSet("run host-execute", a.errOut)
	executable := fs.String("executable", "",
		"absolute path to the exact host executable")
	var commandArgs repeatedString
	fs.Var(&commandArgs, "arg", "one exact argv value; repeat for multiple values")
	workingDirectory := fs.String("cwd", "",
		"absolute working directory; defaults to the Workspace root")
	timeout := fs.Duration("timeout", 2*time.Minute,
		"one-shot host command timeout")
	operationKey := fs.String("operation-key", "",
		"stable operator-owned execution operation key")
	operator := fs.String("operator", "cli_operator", "operator identity")
	purpose := fs.String("purpose", "operator-requested one-shot host command",
		"bounded non-secret execution purpose")
	confirmFullAccess := fs.Bool("confirm-danger-full-access", false,
		"confirm danger-full-access for this exact command")
	confirmHostExecution := fs.Bool(
		"confirm-non-sandboxed-host-execution", false,
		"confirm current-user execution without an OS filesystem or network sandbox")
	enablePermissionControl := fs.Bool("enable-permission-control", false,
		"enable elevated permission evaluation for this process")
	enableFullAccess := fs.Bool("enable-danger-full-access", false,
		"enable danger-full-access evaluation for this process")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"executable": true, "arg": true, "cwd": true, "timeout": true,
		"operation-key": true, "operator": true, "purpose": true,
		"confirm-danger-full-access":           false,
		"confirm-non-sandboxed-host-execution": false,
		"enable-permission-control":            false,
		"enable-danger-full-access":            false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 1 ||
		!domain.ValidAgentID(strings.TrimSpace(*operationKey)) ||
		!domain.ValidAgentID(strings.TrimSpace(*operator)) ||
		strings.TrimSpace(*executable) == "" ||
		!*confirmFullAccess || !*confirmHostExecution {
		return apperror.New(apperror.CodeInvalidArgument,
			"usage: cyberagent run host-execute <run-id> --executable <absolute-path> [--arg <value> ...] [--cwd <absolute-path>] --operation-key <key> --confirm-danger-full-access --confirm-non-sandboxed-host-execution --enable-permission-control --enable-danger-full-access [--timeout <duration>] [--operator <id>] [--purpose <text>]")
	}
	runtimeCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: *enablePermissionControl,
		DangerFullAccessEnabled: *enableFullAccess,
	}
	if err := runtimeCapabilities.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	runRecord, mission, workspaceRecord, interaction, profile, permission, mode, err :=
		a.loadControlledCommandBindings(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	permissionDecision, err := executionauth.EvaluateExecutionPermission(
		permission, runtimeCapabilities, executionauth.PermissionRequest{
			Kind:           executionauth.PermissionOperationStatelessCommand,
			HostFilesystem: true,
			Network:        true,
		})
	if err != nil {
		return err
	}
	if !permissionDecision.Allowed ||
		!permissionDecision.HostFilesystem || !permissionDecision.Network {
		return apperror.New(apperror.CodePolicyDenied,
			permissionDecision.Reason)
	}

	executablePath, executableDigest, err :=
		hashHostExecutable(strings.TrimSpace(*executable))
	if err != nil {
		return err
	}
	cwd := strings.TrimSpace(*workingDirectory)
	if cwd == "" {
		cwd = workspaceRecord.RootPath
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"host working directory is invalid", err)
	}
	cwd = filepath.Clean(cwd)
	environment := safeHostEnvironment()
	spec, err := runner.NewHostCommandSpec(runner.HostCommandSpecRequest{
		ExecutablePath: executablePath, ExecutableSHA256: executableDigest,
		Argv:             append([]string(nil), commandArgs...),
		WorkingDirectory: cwd, Environment: environment,
		NetworkIntent:       runner.HostNetworkIntentHost,
		TimeoutMilliseconds: timeout.Milliseconds(),
		Purpose:             strings.TrimSpace(*purpose),
	})
	if err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			"host command envelope is invalid", err)
	}
	policyText := strings.Join(append(
		[]string{spec.ExecutablePath}, spec.Argv...), " ")
	if decision := a.checker.CheckText("tool_run.shell", policyText); !decision.Allowed {
		return apperror.New(apperror.CodePolicyDenied, decision.Reason)
	}

	operationDigest := runmutation.Fingerprint(
		"host_command_execution_operation.v1", runRecord.ID,
		strings.TrimSpace(*operationKey))
	intent, err := runner.NewHostExecutionIntent(
		runner.HostExecutionIntentRequest{
			OperationKeyDigest: operationDigest,
			RunID:              runRecord.ID,
			MissionID:          mission.ID,
			SessionID:          runRecord.SessionID,
			WorkspaceID:        mission.WorkspaceID,
			Interaction:        interaction,
			Profile:            profile,
			Permission:         permission,
			Spec:               spec,
			RequestedBy:        strings.TrimSpace(*operator),
			CreatedAt:          time.Now().UTC(),
		})
	if err != nil {
		return err
	}
	fmt.Fprintln(a.errOut,
		"WARNING: NON-SANDBOXED HOST EXECUTION uses the current Windows user, host filesystem, and host network. The exact intent is durable and automatic retry is disabled.")
	replayed, err := a.store.PrepareHostExecutionIntent(ctx, intent)
	if err != nil {
		return err
	}
	if replayed {
		receipt, found, err := a.store.GetHostExecutionReceipt(
			ctx, intent.RequestID)
		if err != nil {
			return err
		}
		if !found {
			return apperror.New(apperror.CodeFailedPrecondition,
				"host command execution has a durable intent without a receipt; outcome is uncertain and automatic retry is disabled")
		}
		return writeHostExecutionReceipt(a.out, receipt, true, false)
	}
	executor, err := a.hostCommandExecutor()
	if err != nil {
		return err
	}
	if !executor.Available() {
		return runner.ErrHostCommandPlatform
	}
	result, executeErr := executor.Execute(ctx, runner.HostExecutionRequest{
		Intent: intent, Environment: environment,
		Interaction: interaction, CurrentProfile: profile,
		Permission: permission, Runtime: runtimeCapabilities,
		CurrentSurface:      mode.Surface,
		RequestedBy:         strings.TrimSpace(*operator),
		ExplicitlyConfirmed: true,
	})
	if validationErr := result.Validate(); validationErr != nil {
		if executeErr != nil {
			return errors.Join(executeErr, validationErr)
		}
		return validationErr
	}
	receipt, _, recordErr := a.store.RecordHostExecutionResult(ctx, result)
	if recordErr != nil {
		return recordErr
	}
	if err := writeHostExecutionReceipt(a.out, receipt, false, true); err != nil {
		return err
	}
	if err := writeTransientControlledOutput(
		a.out, "stdout", result.Stdout.Data); err != nil {
		return err
	}
	if err := writeTransientControlledOutput(
		a.out, "stderr", result.Stderr.Data); err != nil {
		return err
	}
	return executeErr
}

func (a *App) controlledCommandExecutor() (controlledCommandExecutor, error) {
	if a.controlledCommands != nil {
		return a.controlledCommands, nil
	}
	executor, err := runner.NewPlatformControlledExecutor()
	if err != nil {
		return nil, err
	}
	a.controlledCommands = executor
	return executor, nil
}

func (a *App) hostCommandExecutor() (hostCommandExecutor, error) {
	if a.hostCommands != nil {
		return a.hostCommands, nil
	}
	executor, err := runner.NewPlatformHostExecutor()
	if err != nil {
		return nil, err
	}
	a.hostCommands = executor
	return executor, nil
}

func hashHostExecutable(value string) (string, string, error) {
	absolutePath, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil {
		return "", "", apperror.Wrap(apperror.CodeInvalidArgument,
			"host executable path is invalid", err)
	}
	absolutePath = filepath.Clean(absolutePath)
	info, err := os.Lstat(absolutePath)
	if err != nil {
		return "", "", apperror.Wrap(apperror.CodeInvalidArgument,
			"host executable cannot be inspected", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 512 || info.Size() > 1<<30 {
		return "", "", apperror.New(apperror.CodeInvalidArgument,
			"host executable must be a regular non-link file between 512 bytes and 1 GiB")
	}
	file, err := os.Open(absolutePath)
	if err != nil {
		return "", "", apperror.Wrap(apperror.CodeInvalidArgument,
			"host executable cannot be opened", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", "", apperror.Wrap(apperror.CodeInvalidArgument,
			"host executable cannot be hashed", err)
	}
	return absolutePath, hex.EncodeToString(digest.Sum(nil)), nil
}

func safeHostEnvironment() []string {
	allowed := []string{
		"SystemRoot", "WINDIR", "SystemDrive", "ComSpec", "Path", "PATHEXT",
		"TEMP", "TMP", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
		"LOCALAPPDATA", "APPDATA", "ProgramData", "ProgramFiles",
		"ProgramW6432", "CommonProgramFiles", "CommonProgramW6432",
		"PSModulePath", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE",
		"PROCESSOR_IDENTIFIER", "PROCESSOR_LEVEL", "PROCESSOR_REVISION", "OS",
	}
	environment := make([]string, 0, len(allowed)+1)
	pathFound := false
	for _, key := range allowed {
		value, ok := os.LookupEnv(key)
		if !ok || value == "" {
			continue
		}
		entry := key + "=" + value
		if redact.String(entry) != entry {
			continue
		}
		if strings.EqualFold(key, "Path") {
			pathFound = true
		}
		environment = append(environment, entry)
	}
	if !pathFound {
		environment = append(environment, "Path=")
	}
	environment = append(environment, "NO_COLOR=1")
	sort.Slice(environment, func(left, right int) bool {
		return strings.ToLower(environment[left]) <
			strings.ToLower(environment[right])
	})
	return environment
}

func (a *App) loadControlledCommandBindings(ctx context.Context, runID string) (
	domain.Run, domain.Mission, store.WorkspaceRecord,
	domain.RunExecutionInteractionSnapshot,
	domain.RunExecutionProfileSnapshot, domain.RunExecutionPermissionSnapshot,
	domain.RunModeSnapshot, error,
) {
	runRecord, err := a.store.GetRun(ctx, strings.TrimSpace(runID))
	if err != nil {
		return domain.Run{}, domain.Mission{}, store.WorkspaceRecord{},
			domain.RunExecutionInteractionSnapshot{},
			domain.RunExecutionProfileSnapshot{},
			domain.RunExecutionPermissionSnapshot{}, domain.RunModeSnapshot{}, err
	}
	mission, err := a.store.GetMission(ctx, runRecord.MissionID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, store.WorkspaceRecord{},
			domain.RunExecutionInteractionSnapshot{},
			domain.RunExecutionProfileSnapshot{},
			domain.RunExecutionPermissionSnapshot{}, domain.RunModeSnapshot{}, err
	}
	if strings.TrimSpace(mission.WorkspaceID) == "" {
		return domain.Run{}, domain.Mission{}, store.WorkspaceRecord{},
			domain.RunExecutionInteractionSnapshot{},
			domain.RunExecutionProfileSnapshot{},
			domain.RunExecutionPermissionSnapshot{}, domain.RunModeSnapshot{},
			apperror.New(apperror.CodeFailedPrecondition,
				"controlled command execution requires a registered Workspace")
	}
	workspaceRecord, err := a.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, store.WorkspaceRecord{},
			domain.RunExecutionInteractionSnapshot{},
			domain.RunExecutionProfileSnapshot{},
			domain.RunExecutionPermissionSnapshot{}, domain.RunModeSnapshot{}, err
	}
	interaction, err := a.store.GetRunExecutionInteraction(ctx, runRecord.ID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, store.WorkspaceRecord{},
			domain.RunExecutionInteractionSnapshot{},
			domain.RunExecutionProfileSnapshot{},
			domain.RunExecutionPermissionSnapshot{}, domain.RunModeSnapshot{}, err
	}
	profile, err := a.store.GetRunExecutionProfile(ctx, runRecord.ID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, store.WorkspaceRecord{},
			domain.RunExecutionInteractionSnapshot{},
			domain.RunExecutionProfileSnapshot{},
			domain.RunExecutionPermissionSnapshot{}, domain.RunModeSnapshot{}, err
	}
	permission, err := a.store.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		return domain.Run{}, domain.Mission{}, store.WorkspaceRecord{},
			domain.RunExecutionInteractionSnapshot{},
			domain.RunExecutionProfileSnapshot{},
			domain.RunExecutionPermissionSnapshot{}, domain.RunModeSnapshot{}, err
	}
	mode, err := a.store.GetRunMode(ctx, runRecord.ID)
	return runRecord, mission, workspaceRecord, interaction, profile, permission, mode, err
}

func writeControlledExecutionReceipt(
	out interface{ Write([]byte) (int, error) },
	receipt store.ControlledExecutionReceipt, replayed bool,
	rawOutputAvailable bool,
) error {
	_, err := fmt.Fprintf(out,
		"request: %s\nprotocol: %s\npolicy: %s\nbackend: %s\nexit_code: %d\nstdout_observed_bytes: %d\nstdout_captured_bytes: %d\nstdout_prefix_sha256: %s\nstdout_truncated: %t\nstderr_observed_bytes: %d\nstderr_captured_bytes: %d\nstderr_prefix_sha256: %s\nstderr_truncated: %t\ntimed_out: %t\ncancelled: %t\noutput_limit_exceeded: %t\ntree_reaped: %t\nrestricted_token: %t\nlow_integrity_token: %t\njob_assigned_at_creation: %t\nkill_on_job_close: %t\nactive_process_limit: %d\nprocess_memory_limit: %d\nstdin_closed: %t\nenvironment_inherited: %t\nnetwork_requested: %t\npersistent_process: %t\nproduct_execution_enabled: %t\nraw_output_persisted: false\nraw_output_available: %t\nreplayed: %t\n",
		receipt.RequestID, receipt.ProtocolVersion, receipt.PolicyVersion,
		receipt.Backend, receipt.ExitCode, receipt.StdoutObservedBytes,
		receipt.StdoutCapturedBytes, receipt.StdoutPrefixSHA256,
		receipt.StdoutTruncated, receipt.StderrObservedBytes,
		receipt.StderrCapturedBytes, receipt.StderrPrefixSHA256,
		receipt.StderrTruncated, receipt.TimedOut, receipt.Cancelled,
		receipt.OutputLimitExceeded, receipt.TreeReaped,
		receipt.RestrictedToken, receipt.LowIntegrityToken,
		receipt.JobAssignedAtCreation, receipt.KillOnJobClose,
		receipt.ActiveProcessLimit, receipt.ProcessMemoryLimit,
		receipt.StdinClosed, receipt.EnvironmentInherited,
		receipt.NetworkRequested, receipt.PersistentProcess,
		receipt.ProductExecutionEnabled, rawOutputAvailable, replayed)
	return err
}

func writeHostExecutionReceipt(
	out interface{ Write([]byte) (int, error) },
	receipt runner.HostExecutionReceipt, replayed bool,
	rawOutputAvailable bool,
) error {
	_, err := fmt.Fprintf(out,
		"request: %s\nprotocol: %s\npolicy: %s\nbackend: %s\nexit_code: %d\nstdout_observed_bytes: %d\nstdout_captured_bytes: %d\nstdout_prefix_sha256: %s\nstdout_truncated: %t\nstderr_observed_bytes: %d\nstderr_captured_bytes: %d\nstderr_prefix_sha256: %s\nstderr_truncated: %t\ntimed_out: %t\ncancelled: %t\noutput_limit_exceeded: %t\ntree_reaped: %t\nnon_sandboxed: %t\nrestricted_token: %t\nlow_integrity_token: %t\njob_assigned_at_creation: %t\nkill_on_job_close: %t\nactive_process_limit: %d\njob_memory_limit: %d\nstdin_closed: %t\nenvironment_inherited: %t\nnetwork_requested: %t\npersistent_process: %t\nproduct_execution_enabled: %t\nautomatic_retry_allowed: false\nenvironment_values_persisted: false\nraw_output_persisted: false\nraw_output_available: %t\nreplayed: %t\n",
		receipt.RequestID, receipt.ProtocolVersion, receipt.PolicyVersion,
		receipt.Backend, receipt.ExitCode, receipt.StdoutObservedBytes,
		receipt.StdoutCapturedBytes, receipt.StdoutPrefixSHA256,
		receipt.StdoutTruncated, receipt.StderrObservedBytes,
		receipt.StderrCapturedBytes, receipt.StderrPrefixSHA256,
		receipt.StderrTruncated, receipt.TimedOut, receipt.Cancelled,
		receipt.OutputLimitExceeded, receipt.TreeReaped,
		receipt.NonSandboxed, receipt.RestrictedToken,
		receipt.LowIntegrityToken, receipt.JobAssignedAtCreation,
		receipt.KillOnJobClose, receipt.ActiveProcessLimit,
		receipt.JobMemoryLimit, receipt.StdinClosed,
		receipt.EnvironmentInherited, receipt.NetworkRequested,
		receipt.PersistentProcess, receipt.ProductExecutionEnabled,
		rawOutputAvailable, replayed)
	return err
}

func writeTransientControlledOutput(
	out interface{ Write([]byte) (int, error) }, stream string, data []byte,
) error {
	if len(data) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%s_begin\n", stream); err != nil {
		return err
	}
	if _, err := out.Write(data); err != nil {
		return err
	}
	if data[len(data)-1] != '\n' {
		if _, err := out.Write([]byte("\n")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "%s_end\n", stream)
	return err
}

func (a *App) runEvents(ctx context.Context, service *application.RunService, args []string) error {
	fs := newFlagSet("run events", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run events <run-id>")
	}
	items, err := service.Events(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(a.out, "no run events")
		return nil
	}
	for _, event := range items {
		fmt.Fprintf(a.out, "#%d\t%s\t%s\t%s\t%s\n", event.Sequence, event.CreatedAt.Format(time.RFC3339), event.Type, event.Source, event.PayloadJSON)
	}
	return nil
}

func (a *App) runTransition(ctx context.Context, service *application.RunService, action string, args []string) error {
	fs := newFlagSet("run "+action, a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cyberagent run %s <run-id>", action)
	}
	var run domain.Run
	var err error
	switch action {
	case "start":
		run, err = service.Start(ctx, fs.Arg(0))
	case "pause":
		run, err = service.Pause(ctx, fs.Arg(0))
	case "resume":
		run, err = service.Resume(ctx, fs.Arg(0))
	case "cancel":
		run, err = service.Cancel(ctx, fs.Arg(0))
	default:
		return fmt.Errorf("unknown run transition %q", action)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "run %s %s\n", run.ID, run.Status)
	if action == "start" {
		fmt.Fprintln(a.out, "note: lifecycle is running; use `cyberagent run step <run-id>` for one supervised turn")
	}
	return nil
}
