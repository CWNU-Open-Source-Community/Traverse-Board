package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
)

func (a *App) pluginCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent plugin stage|import-url|import-git|list|show|review|rollback|trust-publisher|revoke-publisher|stage-mcp")
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	service, err := plugins.NewService(a.store)
	if err != nil {
		return err
	}
	switch args[0] {
	case "stage":
		return a.pluginStageCommand(ctx, service, args[1:])
	case "import-url":
		return a.pluginImportURLCommand(ctx, service, args[1:])
	case "import-git":
		return a.pluginImportGitCommand(ctx, service, args[1:])
	case "list":
		fs := newFlagSet("plugin list", a.errOut)
		pluginID := fs.String("plugin", "", "optional plugin identity")
		limit := fs.Int("limit", 100, "maximum installation records")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("usage: cyberagent plugin list [--plugin <id>] [--limit <n>]")
		}
		values, err := a.store.ListPluginInstallations(ctx, *pluginID, *limit)
		if err != nil {
			return err
		}
		return writeExtensionJSON(a.out, values)
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cyberagent plugin show <installation-id>")
		}
		value, err := a.store.GetPluginInstallation(ctx, args[1])
		if err != nil {
			return err
		}
		return writeExtensionJSON(a.out, value)
	case "review":
		return a.pluginReviewCommand(ctx, service, args[1:])
	case "rollback":
		return a.pluginRollbackCommand(ctx, service, args[1:])
	case "trust-publisher":
		fs := newFlagSet("plugin trust-publisher", a.errOut)
		actor := fs.String("by", "", "review actor")
		if err := fs.Parse(reorderFlags(args[1:], map[string]bool{"by": true})); err != nil {
			return err
		}
		if fs.NArg() != 1 || *actor == "" {
			return errors.New("usage: cyberagent plugin trust-publisher <installation-id> --by <actor>")
		}
		value, err := service.TrustPublisher(ctx, fs.Arg(0), *actor)
		if err != nil {
			return err
		}
		return writeExtensionJSON(a.out, value)
	case "revoke-publisher":
		fs := newFlagSet("plugin revoke-publisher", a.errOut)
		generation := fs.Int64("generation", 0, "expected publisher trust generation")
		actor := fs.String("by", "", "review actor")
		if err := fs.Parse(reorderFlags(args[1:], map[string]bool{"generation": true, "by": true})); err != nil {
			return err
		}
		if fs.NArg() != 1 || *generation < 1 || *actor == "" {
			return errors.New("usage: cyberagent plugin revoke-publisher <fingerprint> --generation <n> --by <actor>")
		}
		value, err := service.RevokePublisher(ctx, fs.Arg(0), *generation, *actor)
		if err != nil {
			return err
		}
		return writeExtensionJSON(a.out, value)
	case "stage-mcp":
		return a.pluginStageMCPCommand(ctx, service, args[1:])
	default:
		return fmt.Errorf("unknown plugin subcommand %q", args[0])
	}
}

func (a *App) pluginStageCommand(ctx context.Context, service *plugins.Service,
	args []string,
) error {
	fs := newFlagSet("plugin stage", a.errOut)
	file := fs.String("file", "", "local plugin.v1 ZIP path")
	supersedes := fs.String("supersedes", "", "prior installation identity")
	actor := fs.String("by", "", "staging actor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *file == "" || *actor == "" {
		return errors.New("usage: cyberagent plugin stage --file <plugin.zip> [--supersedes <installation-id>] --by <actor>")
	}
	absolute, err := filepath.Abs(strings.TrimSpace(*file))
	if err != nil {
		return err
	}
	opened, err := os.Open(absolute)
	if err != nil {
		return err
	}
	defer opened.Close()
	raw, err := io.ReadAll(io.LimitReader(opened, plugins.MaxArchiveBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > plugins.MaxArchiveBytes {
		return errors.New("plugin archive exceeds its size limit")
	}
	digest := sha256.Sum256(raw)
	installation, replayed, err := service.Stage(ctx, raw, plugins.InstallSource{
		Kind: "local_file", URI: filepath.Clean(absolute), SHA256: hex.EncodeToString(digest[:])},
		*supersedes, *actor)
	if err != nil {
		return err
	}
	return a.writeStagedPlugin(installation, replayed)
}

func (a *App) pluginImportURLCommand(ctx context.Context, service *plugins.Service,
	args []string,
) error {
	fs := newFlagSet("plugin import-url", a.errOut)
	rawURL := fs.String("url", "", "fixed HTTPS plugin ZIP URL")
	sha := fs.String("sha256", "", "expected archive SHA-256")
	supersedes := fs.String("supersedes", "", "prior installation identity")
	actor := fs.String("by", "", "staging actor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *rawURL == "" || *sha == "" || *actor == "" {
		return errors.New("usage: cyberagent plugin import-url --url <https-url> --sha256 <hex> [--supersedes <installation-id>] --by <actor>")
	}
	raw, err := plugins.FetchPinnedHTTPS(ctx, *rawURL, *sha, nil)
	if err != nil {
		return err
	}
	installation, replayed, err := service.Stage(ctx, raw, plugins.InstallSource{
		Kind: "https", URI: strings.TrimSpace(*rawURL), SHA256: strings.TrimSpace(*sha)},
		*supersedes, *actor)
	if err != nil {
		return err
	}
	return a.writeStagedPlugin(installation, replayed)
}

func (a *App) pluginImportGitCommand(ctx context.Context, service *plugins.Service,
	args []string,
) error {
	fs := newFlagSet("plugin import-git", a.errOut)
	repository := fs.String("repo", "", "fixed credential-free HTTPS Git repository")
	commit := fs.String("commit", "", "exact 40/64-character commit")
	archivePath := fs.String("archive", "plugin.zip", "repository-relative plugin ZIP blob")
	supersedes := fs.String("supersedes", "", "prior installation identity")
	actor := fs.String("by", "", "staging actor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *repository == "" || *commit == "" || *actor == "" {
		return errors.New("usage: cyberagent plugin import-git --repo <https-url> --commit <sha> [--archive <path>] [--supersedes <installation-id>] --by <actor>")
	}
	raw, err := plugins.FetchPinnedGitArchive(ctx, *repository, *commit, *archivePath, "")
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	installation, replayed, err := service.Stage(ctx, raw, plugins.InstallSource{
		Kind: "git", URI: strings.TrimSpace(*repository), Commit: strings.TrimSpace(*commit),
		SHA256: hex.EncodeToString(digest[:])}, *supersedes, *actor)
	if err != nil {
		return err
	}
	return a.writeStagedPlugin(installation, replayed)
}

func (a *App) writeStagedPlugin(installation plugins.Installation, replayed bool) error {
	return writeExtensionJSON(a.out, struct {
		Replayed     bool                 `json:"replayed"`
		Installation plugins.Installation `json:"installation"`
	}{Replayed: replayed, Installation: installation})
}

func (a *App) pluginReviewCommand(ctx context.Context, service *plugins.Service,
	args []string,
) error {
	fs := newFlagSet("plugin review", a.errOut)
	action := fs.String("action", "", "approve|enable|disable|quarantine|revoke")
	fingerprint := fs.String("fingerprint", "", "exact package fingerprint")
	generation := fs.Int64("generation", 0, "expected installation generation")
	capabilities := fs.String("capabilities", "", "comma-separated capabilities to enable")
	confirmUntrusted := fs.Bool("confirm-untrusted", false, "explicitly accept unsigned/untrusted package risk")
	actor := fs.String("by", "", "review actor")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"action": true,
		"fingerprint": true, "generation": true, "capabilities": true,
		"confirm-untrusted": false, "by": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 || *action == "" || *fingerprint == "" || *generation < 1 || *actor == "" {
		return errors.New("usage: cyberagent plugin review <installation-id> --action <action> --fingerprint <sha256> --generation <n> [--capabilities <list>] [--confirm-untrusted] --by <actor>")
	}
	value, err := service.Review(ctx, fs.Arg(0), plugins.ReviewRequest{
		Action: plugins.ReviewAction(*action), ExpectedPackageFingerprint: *fingerprint,
		ExpectedGeneration: *generation, Capabilities: parsePluginCapabilities(*capabilities),
		ConfirmUntrusted: *confirmUntrusted, ReviewedBy: *actor})
	if err != nil {
		return err
	}
	return writeExtensionJSON(a.out, value)
}

func (a *App) pluginRollbackCommand(ctx context.Context, service *plugins.Service,
	args []string,
) error {
	fs := newFlagSet("plugin rollback", a.errOut)
	targetID := fs.String("target", "", "prior installation identity")
	currentFingerprint := fs.String("current-fingerprint", "", "exact current package fingerprint")
	currentGeneration := fs.Int64("current-generation", 0, "expected current generation")
	targetFingerprint := fs.String("target-fingerprint", "", "exact target package fingerprint")
	targetGeneration := fs.Int64("target-generation", 0, "expected target generation")
	capabilities := fs.String("capabilities", "", "comma-separated target capabilities")
	confirmUntrusted := fs.Bool("confirm-untrusted", false, "explicitly accept unsigned/untrusted package risk")
	actor := fs.String("by", "", "review actor")
	known := map[string]bool{"target": true, "current-fingerprint": true,
		"current-generation": true, "target-fingerprint": true, "target-generation": true,
		"capabilities": true, "confirm-untrusted": false, "by": true}
	if err := fs.Parse(reorderFlags(args, known)); err != nil {
		return err
	}
	if fs.NArg() != 1 || *targetID == "" || *currentFingerprint == "" ||
		*targetFingerprint == "" || *currentGeneration < 1 || *targetGeneration < 1 || *actor == "" {
		return errors.New("usage: cyberagent plugin rollback <current-id> --target <target-id> --current-fingerprint <sha256> --current-generation <n> --target-fingerprint <sha256> --target-generation <n> --capabilities <list> [--confirm-untrusted] --by <actor>")
	}
	current, target, err := service.Rollback(ctx, fs.Arg(0), *targetID,
		plugins.RollbackRequest{ExpectedCurrentFingerprint: *currentFingerprint,
			ExpectedCurrentGeneration: *currentGeneration,
			ExpectedTargetFingerprint: *targetFingerprint,
			ExpectedTargetGeneration:  *targetGeneration,
			Capabilities:              parsePluginCapabilities(*capabilities),
			ConfirmUntrusted:          *confirmUntrusted, ReviewedBy: *actor})
	if err != nil {
		return err
	}
	return writeExtensionJSON(a.out, map[string]plugins.Installation{
		"rolled_back": current, "enabled": target})
}

func (a *App) pluginStageMCPCommand(ctx context.Context, service *plugins.Service,
	args []string,
) error {
	fs := newFlagSet("plugin stage-mcp", a.errOut)
	scope := fs.String("scope", "workspace", "run|workspace")
	runID := fs.String("run", "", "Run identity for run scope")
	workspaceID := fs.String("workspace", "", "exact Workspace identity")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"scope": true,
		"run": true, "workspace": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 || *workspaceID == "" {
		return errors.New("usage: cyberagent plugin stage-mcp <installation-id> --scope <run|workspace> [--run <id>] --workspace <id>")
	}
	manager := a.newMCPClientManager()
	if manager == nil {
		return errors.New("MCP client runtime is unavailable")
	}
	if _, err := manager.ReconcileStartup(ctx); err != nil {
		return err
	}
	values, err := service.StageMCPServers(ctx, fs.Arg(0), mcp.ScopeKind(*scope),
		*runID, *workspaceID, manager)
	if err != nil {
		return err
	}
	return writeExtensionJSON(a.out, values)
}

func parsePluginCapabilities(value string) []plugins.Capability {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]plugins.Capability, 0, len(parts))
	for _, part := range parts {
		result = append(result, plugins.Capability(strings.TrimSpace(part)))
	}
	return result
}
