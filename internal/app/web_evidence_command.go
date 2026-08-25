package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/webevidence"
)

const webEvidenceUsage = "usage: cyberagent web-evidence list --run <run-id> [--limit <n>]"

func (a *App) webEvidenceCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New(webEvidenceUsage)
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	fs := newFlagSet("web-evidence list", a.errOut)
	runID := fs.String("run", "", "Run identity")
	limit := fs.Int("limit", 100, "maximum entries loaded from each evidence ledger")
	if err := fs.Parse(reorderFlags(args[1:], map[string]bool{
		"run": true, "limit": true,
	})); err != nil {
		return err
	}
	resolvedRunID := strings.TrimSpace(*runID)
	if fs.NArg() != 0 || resolvedRunID == "" || *limit < 1 ||
		*limit > webevidence.MaxInventoryItems {
		return errors.New(webEvidenceUsage)
	}
	if _, err := a.store.GetRun(ctx, resolvedRunID); err != nil {
		return err
	}
	inventory, err := webevidence.LoadInventory(ctx, a.store, resolvedRunID,
		*limit, time.Now().UTC())
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(a.out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(inventory)
}
