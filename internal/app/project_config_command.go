package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/projectconfig"
	"cyberagent-workbench/internal/toolgateway"
)

// projectConfigCommand inspects the narrowing-only .prayu/config.yaml. It
// never writes, executes, or grants anything.
func (a *App) projectConfigCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent project-config show|validate <path> [--profile <p> --max-turns <n> --max-tool-calls <n>]")
	}
	switch args[0] {
	case "validate":
		if len(args) != 2 {
			return errors.New("usage: cyberagent project-config validate <config-path>")
		}
		config, err := projectconfig.Load(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "valid: true\nprotocol: %s\nread_only: %t\nallowed_profiles: %s\n",
			config.Protocol, config.ReadOnly != nil && *config.ReadOnly, strings.Join(config.AllowedProfiles, ","))
		return nil
	case "show":
		flags := newFlagSet("project-config show", a.errOut)
		profileValue := flags.String("profile", "code", "requested profile for the narrowing preview")

		maxTurns := flags.Int("max-turns", 100, "requested turn ceiling")

		maxToolCalls := flags.Int("max-tool-calls", 100, "requested tool-call ceiling")

		if err := flags.Parse(reorderFlags(args[1:], map[string]bool{

			"profile": true, "max-turns": true, "max-tool-calls": true,
		})); err != nil {

			return err

		}

		if flags.NArg() != 1 {

			return errors.New("usage: cyberagent project-config show <config-path> [--profile <p> --max-turns <n> --max-tool-calls <n>]")
		}

		config, err := projectconfig.Load(ctx, flags.Arg(0))

		if err != nil {

			return err

		}

		effective, rejections, err := config.Narrow(projectconfig.Ceiling{

			AllowedProfiles: []string{*profileValue},

			MaxTurns: *maxTurns,

			MaxToolCalls: *maxToolCalls,

			RegisteredCommands: toolgateway.TypedActionIDs(),
		})

		if err != nil {

			return err

		}

		for _, rejection := range rejections {

			fmt.Fprintf(a.out, "rejected: %s\t%s\n", rejection.Field, rejection.Reason)

		}

		fmt.Fprintf(a.out, "protocol: %s\nread_only: %t\nallowed_profiles: %s\nmax_turns: %d\nmax_tool_calls: %d\nexclude_paths: %s\nskill_suggestions: %s\nfingerprint: %s\n",

			effective.Protocol, effective.ReadOnly, strings.Join(effective.AllowedProfiles, ","),

			effective.MaxTurns, effective.MaxToolCalls, strings.Join(effective.ExcludePaths, ","),

			strings.Join(effective.SkillSuggestions, ","), effective.Fingerprint())

		return nil

	default:

		return fmt.Errorf("unknown project-config subcommand %q", args[0])

	}

}
