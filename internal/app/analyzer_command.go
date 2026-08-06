package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cyberagent-workbench/internal/application"
)

func (a *App) analyzerCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("analyzer subcommand is required")
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	switch args[0] {
	case "execute":
		return a.analyzerExecute(ctx, args[1:])
	default:
		return fmt.Errorf("unknown analyzer subcommand %q", args[0])
	}
}

func (a *App) analyzerExecute(ctx context.Context, args []string) error {
	fs := newFlagSet("analyzer execute", a.errOut)
	runID := fs.String("run", "", "Run id")
	textInput := fs.String("text", "", "inline text input")
	fileInput := fs.String("file", "", "workspace-relative input file")
	mediaType := fs.String("media-type", "text/plain", "input media type")
	confirm := fs.String("confirm", "", "explicit execution confirmation")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"run": true, "text": true, "file": true, "media-type": true,
		"confirm": true, "json": false,
	})); err != nil {
		return err
	}
	if fs.NArg() != 0 || *runID == "" || (*textInput == "") == (*fileInput == "") {
		return errors.New("usage: cyberagent analyzer execute --run <id> (--text <value> | --file <workspace-relative-path>) --confirm RUN-EMBEDDED-ANALYZER [--media-type <type>] [--json]")
	}
	result, err := application.NewEmbeddedAnalyzerExecutionService(a.store).Execute(ctx,
		application.EmbeddedAnalyzerExecutionRequest{
			ProtocolVersion: application.EmbeddedAnalyzerExecutionProtocolVersion,
			RunID:           *runID, Text: *textInput, File: *fileInput, MediaType: *mediaType,
			RequestedBy: "cli", Confirmation: *confirm,
		})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	fmt.Fprintf(a.out, "analyzer execution %s completed\n", result.Record.ID)
	fmt.Fprintf(a.out, "run: %s\nartifact: %s\nsha256: %s\ninput_bytes: %d\nlines: %d\n",
		result.Record.RunID, result.Artifact.ID, result.Result.Summary.SHA256,
		result.Result.Summary.InputBytes, result.Result.Summary.LineCount)
	fmt.Fprintf(a.out, "filesystem: false\nnetwork: false\nsubprocess: false\nreplayed: %t\n",
		result.Replayed)
	return nil
}
