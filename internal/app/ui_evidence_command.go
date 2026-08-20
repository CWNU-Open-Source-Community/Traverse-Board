package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/uievidence"
)

const uiEvidenceUsage = "usage: cyberagent ui-evidence " +
	"list --run <run-id> [--status <status>] [--limit <n>] | " +
	"show <attempt-id> | artifact <attempt-id> <artifact-id> --output <path>"

func (a *App) uiEvidenceCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(uiEvidenceUsage)
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return a.uiEvidenceList(ctx, args[1:])
	case "show":
		return a.uiEvidenceShow(ctx, args[1:])
	case "artifact":
		return a.uiEvidenceArtifact(ctx, args[1:])
	default:
		return errors.New(uiEvidenceUsage)
	}
}

func (a *App) uiEvidenceList(ctx context.Context, args []string) error {
	fs := newFlagSet("ui-evidence list", a.errOut)
	runID := fs.String("run", "", "Run identity")
	status := fs.String("status", "", "optional exact UI evidence status")
	limit := fs.Int("limit", 100, "maximum attempts")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"run": true, "status": true, "limit": true,
	})); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" {
		return errors.New("usage: cyberagent ui-evidence list --run <run-id> [--status <status>] [--limit <n>]")
	}
	filter := uievidence.ListFilter{RunID: strings.TrimSpace(*runID),
		Status: uievidence.Status(strings.TrimSpace(*status)), Limit: *limit}
	if err := filter.Validate(); err != nil {
		return fmt.Errorf("invalid UI evidence list filter: %w", err)
	}
	values, err := a.store.ListUIEvidenceAttempts(ctx, filter)
	if err != nil {
		return err
	}
	if values == nil {
		values = []uievidence.Attempt{}
	}
	return writeUIEvidenceJSON(a.out, values)
}

func (a *App) uiEvidenceShow(ctx context.Context, args []string) error {
	fs := newFlagSet("ui-evidence show", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent ui-evidence show <attempt-id>")
	}
	attemptID := fs.Arg(0)
	attempt, err := a.store.GetUIEvidenceAttempt(ctx, attemptID)
	if err != nil {
		return err
	}
	steps, err := a.store.ListUIEvidenceSteps(ctx, attemptID)
	if err != nil {
		return err
	}
	artifacts, err := a.store.ListUIEvidenceArtifacts(ctx, attemptID)
	if err != nil {
		return err
	}
	if steps == nil {
		steps = []uievidence.StepReceipt{}
	}
	if artifacts == nil {
		artifacts = []uievidence.ArtifactMetadata{}
	}
	return writeUIEvidenceJSON(a.out, application.UIEvidenceBundle{
		Attempt: attempt, Steps: steps, Artifacts: artifacts})
}

func (a *App) uiEvidenceArtifact(ctx context.Context, args []string) error {
	fs := newFlagSet("ui-evidence artifact", a.errOut)
	output := fs.String("output", "", "new output file for the verified artifact bytes")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"output": true})); err != nil {
		return err
	}
	if fs.NArg() != 2 || strings.TrimSpace(*output) == "" {
		return errors.New("usage: cyberagent ui-evidence artifact <attempt-id> <artifact-id> --output <path>")
	}
	artifact, err := a.store.GetUIEvidenceArtifact(ctx, fs.Arg(0), fs.Arg(1))
	if err != nil {
		return err
	}
	if err := artifact.Validate(); err != nil {
		return fmt.Errorf("UI evidence artifact failed integrity verification: %w", err)
	}
	outputPath, err := filepath.Abs(strings.TrimSpace(*output))
	if err != nil {
		return err
	}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create UI evidence artifact output: %w", err)
	}
	writeErr := error(nil)
	if _, err := file.Write(artifact.Content); err != nil {
		writeErr = err
	} else if err := file.Sync(); err != nil {
		writeErr = err
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("write UI evidence artifact output: %w",
			errors.Join(writeErr, closeErr))
	}
	fmt.Fprintf(a.out, "artifact: %s\noutput: %s\nsha256: %s\nbytes: %d\nuntrusted: true\n",
		artifact.Metadata.ID, outputPath, artifact.Metadata.SHA256,
		artifact.Metadata.Bytes)
	return nil
}

func writeUIEvidenceJSON(destination interface {
	Write([]byte) (int, error)
}, value any) error {
	encoder := json.NewEncoder(destination)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
