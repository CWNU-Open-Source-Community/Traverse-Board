package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/codeintel"
	"cyberagent-workbench/internal/workspace"
)

func (a *App) codeIntelCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("code-intel subcommand is required")
	}
	switch args[0] {
	case "status":
		return a.codeIntelStatus(ctx, args[1:], false)
	case "qualify":
		return a.codeIntelStatus(ctx, args[1:], true)
	default:
		return fmt.Errorf("unknown code-intel subcommand %q", args[0])
	}
}

func (a *App) codeIntelStatus(ctx context.Context, args []string, qualify bool) error {
	command := "code-intel status"
	if qualify {
		command = "code-intel qualify"
	}
	fs := newFlagSet(command, a.errOut)
	configPath := fs.String("config", "", "absolute operator-reviewed code-intel config")
	workspaceName := fs.String("workspace", "", "registered workspace name")
	jsonOutput := fs.Bool("json", false, "print JSON")
	start := fs.Bool("start", qualify, "initialize reviewed servers to negotiate capabilities")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"config": true, "workspace": true,
		"json": false, "start": false})); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cyberagent code-intel status|qualify [--config <absolute-path>] [--workspace <name>] [--start] [--json]")
	}
	if strings.TrimSpace(*configPath) != "" {
		if a.codeIntelConfigLoaded || a.codeIntel != nil {
			return errors.New("code-intel config must be selected before the runtime is initialized")
		}
		absolute, err := filepath.Abs(strings.TrimSpace(*configPath))
		if err != nil {
			return err
		}
		a.codeIntelConfigPath = filepath.Clean(absolute)
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	if a.codeIntel == nil {
		return errors.New("no explicit code-intel config is selected; use --config or " +
			codeIntelConfigEnvironment)
	}
	type output struct {
		ProtocolVersion string                         `json:"protocol_version"`
		ConfigSHA256    string                         `json:"config_sha256"`
		Qualifications  []codeintel.Qualification      `json:"qualifications"`
		Servers         []codeintel.CapabilitySnapshot `json:"servers"`
	}
	result := output{ProtocolVersion: codeintel.ProtocolVersion,
		ConfigSHA256: a.codeIntelConfigDigest, Qualifications: []codeintel.Qualification{}}
	if strings.TrimSpace(*workspaceName) != "" {
		record, err := a.store.GetWorkspaceByName(ctx, workspace.Slug(*workspaceName))
		if err != nil {
			return err
		}
		result.Qualifications = a.codeIntel.Qualify(ctx, record.ID, record.RootPath)
		if *start {
			result.Servers = a.codeIntel.Capabilities(ctx, record.ID, record.RootPath)
		} else {
			result.Servers = a.codeIntel.Inventory()
		}
	} else {
		if qualify || *start {
			return errors.New("code-intel qualification requires --workspace")
		}
		result.Servers = a.codeIntel.Inventory()
	}
	if *jsonOutput {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(a.out, string(encoded))
		return err
	}
	fmt.Fprintf(a.out, "protocol: %s\nconfig_sha256: %s\nservers: %d\n",
		result.ProtocolVersion, result.ConfigSHA256, len(result.Servers))
	for _, server := range result.Servers {
		fmt.Fprintf(a.out, "%s\tworkspace=%s\tlanguages=%s\thealth=%s\tgeneration=%s\tcapability_fingerprint=%s\ttools=%s\terror=%s\n",
			server.ServerID, server.WorkspaceID, strings.Join(server.Languages, ","),
			server.Health, server.Generation, server.CapabilityFingerprint,
			strings.Join(server.ModelVisibleTools, ","), server.LastError)
	}
	for _, item := range result.Qualifications {
		fmt.Fprintf(a.out, "qualification\tserver=%s\teligible=%t\thash_match=%t\treviewed=%t\tminimal_env=%t\tnetwork_grant=%t\treason=%s\n",
			item.ServerID, item.Eligible, item.ExecutableHashMatched, item.Reviewed,
			item.MinimalEnvironment, item.NetworkAccessGranted, item.Reason)
	}
	return nil
}
