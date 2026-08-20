package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/application"
)

func (a *App) debugCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "query" {
		return errors.New("usage: cyberagent debug query --run <run-id> [bounded filters] [--json]")
	}
	flags := newFlagSet("debug query", a.errOut)
	runID := flags.String("run", "", "Run identity")
	after := flags.Int64("after", 0, "exclusive event-sequence cursor")
	limit := flags.Int("limit", 100, "maximum returned items")
	fromText := flags.String("from", "", "RFC3339 lower time bound")
	toText := flags.String("to", "", "RFC3339 upper time bound")
	correlationKind := flags.String("correlation-kind", "", "run, attempt, tool, process, or request")
	correlationID := flags.String("correlation-id", "", "exact metadata subject identity")
	typePrefix := flags.String("type-prefix", "", "event type prefix")
	sourcePrefix := flags.String("source-prefix", "", "event source prefix")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(reorderFlags(args[1:], map[string]bool{
		"run": true, "after": true, "limit": true, "from": true, "to": true,
		"correlation-kind": true, "correlation-id": true, "type-prefix": true,
		"source-prefix": true, "json": false,
	})); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*runID) == "" {
		return errors.New("usage: cyberagent debug query --run <run-id> [--after <sequence>] [--limit <1..100>] [--from <RFC3339>] [--to <RFC3339>] [--correlation-kind <kind> --correlation-id <id>] [--json]")
	}
	from, err := parseOptionalDiagnosticTime(*fromText)
	if err != nil {
		return err
	}
	to, err := parseOptionalDiagnosticTime(*toText)
	if err != nil {
		return err
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	result, err := application.NewDiagnosticsService(a.store, a.models).Debug(ctx,
		application.DebugQueryRequest{Version: application.DebugQueryProtocolVersion,
			RunID: strings.TrimSpace(*runID), AfterSequence: *after, Limit: *limit,
			From: from, To: to, CorrelationKind: *correlationKind,
			CorrelationID: *correlationID, TypePrefix: *typePrefix,
			SourcePrefix: *sourcePrefix})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	fmt.Fprintf(a.out, "protocol: %s\nrun: %s\nitems: %d\nscanned: %d\nnext_after: %d\nhas_more: %t\n",
		result.ProtocolVersion, result.RunID, len(result.Items), result.Scanned,
		result.NextAfterSequence, result.HasMore)
	for _, item := range result.Items {
		fmt.Fprintf(a.out, "- #%d %s %s %s subject=%s evidence=%s payload=%s\n",
			item.Sequence, item.ObservedAt.Format(time.RFC3339Nano), item.Category,
			item.Type, item.SubjectID, item.Evidence, item.PayloadState)
	}
	return nil
}

func parseOptionalDiagnosticTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("diagnostic time must use RFC3339: %w", err)
	}
	return parsed.UTC(), nil
}
