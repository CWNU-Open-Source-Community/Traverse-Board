package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func (a *App) runSchedule(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent run schedule create|list|show|pause|resume|cancel|tick")
	}
	service := application.NewScheduledJobService(a.store)
	switch args[0] {
	case "create":
		return a.runScheduleCreate(ctx, service, args[1:])
	case "list":
		flags := newFlagSet("run schedule list", a.errOut)
		limit := flags.Int("limit", 50, "maximum jobs")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{"limit": true})); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("usage: cyberagent run schedule list <run-id> [--limit <1..100>]")
		}
		values, err := service.List(ctx, flags.Arg(0), *limit)
		if err != nil {
			return err
		}
		return writeIndentedJSON(a.out, values)
	case "show":
		flags := newFlagSet("run schedule show", a.errOut)
		rounds := flags.Int("rounds", 20, "maximum recent rounds")
		notifications := flags.Int("notifications", 20, "maximum recent notifications")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
			"rounds": true, "notifications": true,
		})); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("usage: cyberagent run schedule show <job-id> [--rounds <1..100>] [--notifications <1..100>]")
		}
		snapshot, err := service.Get(ctx, flags.Arg(0), *rounds, *notifications)
		if err != nil {
			return err
		}
		return writeIndentedJSON(a.out, snapshot)
	case "pause", "resume", "cancel":
		return a.runScheduleTransition(ctx, service, args[0], args[1:])
	case "tick":
		flags := newFlagSet("run schedule tick", a.errOut)
		owner := flags.String("owner", "cli_scheduled_worker", "foreground worker identity")
		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{"owner": true})); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("usage: cyberagent run schedule tick [--owner <id>]")
		}
		handled, err := service.RunDue(ctx, *owner, time.Now().UTC())
		fmt.Fprintf(a.out, "protocol: %s\nhandled: %t\nconcurrency: 1\nmodel_executor_enabled: false\ntool_executor_enabled: false\n",
			domain.ScheduledJobProtocolVersion, handled)
		return err
	default:
		return fmt.Errorf("unknown run schedule subcommand %q", args[0])
	}
}

func (a *App) runScheduleCreate(ctx context.Context,
	service *application.ScheduledJobService, args []string,
) error {
	flags := newFlagSet("run schedule create", a.errOut)
	atText := flags.String("at", "", "first RFC3339 occurrence")
	every := flags.Duration("every", 0, "periodic elapsed interval; omit for once")
	timezone := flags.String("timezone", "UTC", "IANA display timezone")
	deadlineText := flags.String("deadline", "", "hard RFC3339 deadline")
	misfire := flags.String("misfire", string(domain.ScheduledJobMisfireRunOnce),
		"run_once or skip")
	maxRounds := flags.Int("max-rounds", 10, "hard round budget")
	maxModelCalls := flags.Int("max-model-calls", 0, "hard model-call budget; zero disables")
	maxElapsed := flags.Int64("max-elapsed-seconds", 3600, "hard elapsed budget")
	maxAttempts := flags.Int("max-attempts", 3, "attempts per occurrence")
	initialBackoff := flags.Int("initial-backoff-seconds", 5, "initial retry delay")
	maxBackoff := flags.Int("max-backoff-seconds", 60, "maximum retry delay")
	notify := flags.String("notify", string(domain.ScheduledJobNotifyFailure),
		"silent, on_change, on_failure, or all")
	mode := flags.String("mode", string(domain.ScheduledJobReadOnly),
		"read_only or approved_repair")
	confirmRepair := flags.Bool("confirm-repair", false,
		"bind exact current Code/Deliver permission snapshots")
	stopTerminal := flags.Bool("stop-on-target-terminal", true,
		"stop after the target Run reaches a terminal state")
	operationKey := flags.String("operation-key", "", "stable creation operation key")
	operator := flags.String("operator", "cli_operator", "requester identity")
	if err := flags.Parse(reorderFlags(args, map[string]bool{
		"at": true, "every": true, "timezone": true, "deadline": true,
		"misfire": true, "max-rounds": true, "max-model-calls": true,
		"max-elapsed-seconds": true, "max-attempts": true,
		"initial-backoff-seconds": true, "max-backoff-seconds": true,
		"notify": true, "mode": true, "confirm-repair": false,
		"stop-on-target-terminal": false, "operation-key": true, "operator": true,
	})); err != nil {
		return err
	}
	if flags.NArg() != 1 || strings.TrimSpace(*atText) == "" ||
		strings.TrimSpace(*deadlineText) == "" || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run schedule create <run-id> --at <RFC3339> --deadline <RFC3339> --operation-key <key> [--every <duration>] [bounded policy options]")
	}
	anchor, err := time.Parse(time.RFC3339, *atText)
	if err != nil {
		return fmt.Errorf("schedule --at must use RFC3339: %w", err)
	}
	deadline, err := time.Parse(time.RFC3339, *deadlineText)
	if err != nil {
		return fmt.Errorf("schedule --deadline must use RFC3339: %w", err)
	}
	kind := domain.ScheduledJobOnce
	intervalSeconds := int64(0)
	if *every != 0 {
		if *every < time.Second || *every%time.Second != 0 {
			return errors.New("schedule --every must be a positive whole-second duration")
		}
		kind = domain.ScheduledJobPeriodic
		intervalSeconds = int64(*every / time.Second)
	}
	runID := flags.Arg(0)
	result, err := service.Create(ctx, application.CreateScheduledJobRequest{
		Version: domain.ScheduledJobProtocolVersion, RunID: runID, TargetRunID: runID,
		Schedule: domain.ScheduledJobSchedule{Kind: kind,
			Timezone: strings.TrimSpace(*timezone), AnchorAt: anchor.UTC(),
			IntervalSeconds: intervalSeconds,
			MisfirePolicy:   domain.ScheduledJobMisfirePolicy(strings.TrimSpace(*misfire))},
		DeadlineAt: deadline.UTC(), StopOnTargetTerminal: *stopTerminal,
		MaxRounds: *maxRounds, MaxModelCalls: *maxModelCalls,
		MaxElapsedSeconds: *maxElapsed,
		Retry: domain.ScheduledJobRetryPolicy{MaxAttempts: *maxAttempts,
			InitialBackoffSeconds: *initialBackoff, MaxBackoffSeconds: *maxBackoff},
		Notification:  domain.ScheduledJobNotificationMode(strings.TrimSpace(*notify)),
		ExecutionMode: domain.ScheduledJobExecutionMode(strings.TrimSpace(*mode)),
		ConfirmRepair: *confirmRepair, OperationKey: *operationKey,
		RequestedBy: *operator,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.out, result)
}

func (a *App) runScheduleTransition(ctx context.Context,
	service *application.ScheduledJobService, actionText string, args []string,
) error {
	flags := newFlagSet("run schedule "+actionText, a.errOut)
	revision := flags.Int64("revision", 0, "expected job revision")
	operationKey := flags.String("operation-key", "", "stable transition operation key")
	operator := flags.String("operator", "cli_operator", "requester identity")
	if err := flags.Parse(reorderFlags(args, map[string]bool{
		"revision": true, "operation-key": true, "operator": true,
	})); err != nil {
		return err
	}
	if flags.NArg() != 2 || *revision < 1 || strings.TrimSpace(*operationKey) == "" {
		return fmt.Errorf("usage: cyberagent run schedule %s <run-id> <job-id> --revision <n> --operation-key <key>", actionText)
	}
	action := domain.ScheduledJobAction(actionText)
	result, err := service.Transition(ctx, application.TransitionScheduledJobRequest{
		Version: domain.ScheduledJobControlProtocolVersion,
		RunID:   flags.Arg(0), JobID: flags.Arg(1), Action: action,
		ExpectedRevision: *revision, OperationKey: *operationKey,
		RequestedBy: *operator,
	})
	if err != nil {
		return err
	}
	return writeIndentedJSON(a.out, result)
}

func writeIndentedJSON(out interface{ Write([]byte) (int, error) }, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
