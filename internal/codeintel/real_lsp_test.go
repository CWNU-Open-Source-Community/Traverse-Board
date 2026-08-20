package codeintel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	goplsSmokeBinaryEnvironment = "CYBERAGENT_GOPLS_SMOKE_BINARY"
	tsSmokeNodeEnvironment      = "CYBERAGENT_TYPESCRIPT_LSP_SMOKE_NODE"
	tsSmokeCLIEnvironment       = "CYBERAGENT_TYPESCRIPT_LSP_SMOKE_CLI"
)

type realToolPosition struct {
	Line      int
	Character int
}

func TestRealGoplsReadOnlySemanticToolSmoke(t *testing.T) {
	executable := strings.TrimSpace(os.Getenv(goplsSmokeBinaryEnvironment))
	if executable == "" {
		t.Skip(goplsSmokeBinaryEnvironment + " is not set")
	}
	root := t.TempDir()
	writeSmokeFile(t, root, "go.mod", "module example.invalid/codeintel\n\ngo 1.25\n")
	writeSmokeFile(t, root, "sample.go", `package sample

type Runner interface {
	Run(value string) string
}

type Worker struct{}

func (Worker) Run(value string) string {
	return Helper(value)
}

func Helper(value string) string {
	return value
}

func Call() string {
	return Worker{}.Run("x")
}
`)
	positions := map[string]realToolPosition{
		ToolDefinition:     {Line: 9, Character: 12},
		ToolReferences:     {Line: 12, Character: 6},
		ToolImplementation: {Line: 3, Character: 2},
		ToolHover:          {Line: 9, Character: 12},
		ToolSignatureHelp:  {Line: 9, Character: 19},
		ToolCallHierarchy:  {Line: 12, Character: 6},
		ToolTypeHierarchy:  {Line: 2, Character: 6},
	}
	runRealLSPToolSmoke(t, root, executable, []string{"serve"},
		"gopls-smoke", "gopls", Language{ID: "go", Extensions: []string{".go"}},
		"sample.go", "Helper", nil, allSemanticTools(), positions)
}

func TestRealTypeScriptLanguageServerReadOnlySemanticToolSmoke(t *testing.T) {
	executable := strings.TrimSpace(os.Getenv(tsSmokeNodeEnvironment))
	cli := strings.TrimSpace(os.Getenv(tsSmokeCLIEnvironment))
	if executable == "" || cli == "" {
		t.Skip(tsSmokeNodeEnvironment + " and " + tsSmokeCLIEnvironment + " are required")
	}
	if !filepath.IsAbs(cli) {
		t.Fatal("TypeScript language-server smoke CLI must be absolute")
	}
	tsserver := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(cli))),
		"typescript", "lib", "tsserver.js")
	if !filepath.IsAbs(tsserver) {
		t.Fatal("TypeScript tsserver smoke path must be absolute")
	}
	initializationOptions, err := json.Marshal(map[string]any{
		"tsserver": map[string]string{"path": tsserver},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeSmokeFile(t, root, "tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true},"include":["index.ts"]}`)
	writeSmokeFile(t, root, "index.ts", `interface Runner {
  run(value: string): string;
}
class Worker implements Runner {
  run(value: string): string {
    return helper(value);
  }
}
function helper(value: string): string {
  return value;
}
export function call(): string {
  return new Worker().run("x");
}
`)
	positions := map[string]realToolPosition{
		ToolDefinition:     {Line: 5, Character: 12},
		ToolReferences:     {Line: 8, Character: 10},
		ToolImplementation: {Line: 1, Character: 3},
		ToolHover:          {Line: 5, Character: 12},
		ToolSignatureHelp:  {Line: 5, Character: 18},
		ToolCallHierarchy:  {Line: 8, Character: 10},
		ToolTypeHierarchy:  {Line: 0, Character: 10},
	}
	runRealLSPToolSmoke(t, root, executable, []string{cli, "--stdio"},
		"typescript-smoke", "TypeScript Language Server",
		Language{ID: "typescript", Extensions: []string{".ts", ".tsx"}},
		"index.ts", "helper", initializationOptions,
		[]string{ToolWorkspaceSymbols, ToolDocumentSymbols, ToolDefinition, ToolReferences,
			ToolImplementation, ToolHover, ToolSignatureHelp, ToolDiagnostics,
			ToolCallHierarchy}, positions)
}

func runRealLSPToolSmoke(t *testing.T, root, executable string, arguments []string,
	serverID, serverName string, language Language, documentPath, symbolQuery string,
	initializationOptions json.RawMessage, requiredTools []string,
	positions map[string]realToolPosition,
) {
	t.Helper()
	executable, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	executable = filepath.Clean(executable)
	digest, available, err := executableDigest(executable)
	if err != nil || !available {
		t.Fatalf("hash real LSP executable: available=%t err=%v", available, err)
	}
	descriptor := ServerDescriptor{ProtocolVersion: ProtocolVersion, ID: serverID,
		Name: serverName, WorkspaceID: helperWorkspaceID, Languages: []Language{language},
		Executable: executable, Arguments: append([]string(nil), arguments...),
		ExecutableSHA256:      digest,
		InitializationOptions: append(json.RawMessage(nil), initializationOptions...),
		RequestTimeoutMillis:  (30 * time.Second).Milliseconds(),
		ReviewedBy:            "code-intel-real-smoke", ReviewedAt: time.Unix(1, 0).UTC(),
		Source: Source{Kind: "operator_config", Label: "real-lsp-smoke.json",
			SHA256: strings.Repeat("e", 64)}}
	manager, err := NewManager([]ServerDescriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestManager(manager) })

	snapshots := manager.Capabilities(context.Background(), helperWorkspaceID, root)
	if len(snapshots) != 1 || snapshots[0].Health != HealthHealthy {
		t.Fatalf("real LSP did not become healthy: %#v", snapshots)
	}
	snapshot := snapshots[0]
	t.Logf("real LSP %s %s negotiated tools: %v", snapshot.ServerName,
		snapshot.ServerVersion, snapshot.ModelVisibleTools)
	for _, required := range requiredTools {
		if !containsRealSmokeTool(snapshot.ModelVisibleTools, required) {
			t.Fatalf("real LSP did not negotiate required tool %s: %#v", required, snapshot)
		}
	}

	// Opening the document before workspace/symbol is important for servers
	// that create their project graph lazily (notably typescript-language-server).
	for _, tool := range allSemanticToolsDocumentFirst() {
		if !containsRealSmokeTool(snapshot.ModelVisibleTools, tool) {
			continue
		}
		request := Request{Tool: tool, WorkspaceID: helperWorkspaceID,
			WorkspaceRoot: root, ServerID: snapshot.ServerID,
			ServerGeneration:      snapshot.Generation,
			CapabilityFingerprint: snapshot.CapabilityFingerprint,
			Limit:                 100}
		switch tool {
		case ToolWorkspaceSymbols:
			request.Query = symbolQuery
		default:
			request.Path = documentPath
		}
		if position, found := positions[tool]; found {
			request.Position = Position{Line: position.Line, Character: position.Character}
		}
		switch tool {
		case ToolReferences:
			request.IncludeDeclaration = true
		case ToolCallHierarchy:
			request.Direction = "both"
		case ToolTypeHierarchy:
			request.Direction = "both"
		}
		result, err := manager.Execute(context.Background(), request)
		if err != nil {
			t.Fatalf("real LSP tool %s failed: %v", tool, err)
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("real LSP tool %s returned invalid evidence: %v", tool, err)
		}
		if result.State != EvidenceCurrent && result.State != EvidencePartial {
			t.Fatalf("real LSP tool %s returned unexpected state %q", tool, result.State)
		}
		if validation := manager.ValidateEvidence(context.Background(), root,
			result.Provenance); validation.State != EvidenceCurrent {
			t.Fatalf("real LSP tool %s evidence was not current: %#v", tool, validation)
		}
	}
}

func allSemanticTools() []string {
	return []string{ToolWorkspaceSymbols, ToolDocumentSymbols, ToolDefinition, ToolReferences,
		ToolImplementation, ToolHover, ToolSignatureHelp, ToolDiagnostics,
		ToolCallHierarchy, ToolTypeHierarchy}
}

func allSemanticToolsDocumentFirst() []string {
	return []string{ToolDocumentSymbols, ToolWorkspaceSymbols, ToolDefinition, ToolReferences,
		ToolImplementation, ToolHover, ToolSignatureHelp, ToolDiagnostics,
		ToolCallHierarchy, ToolTypeHierarchy}
}

func containsRealSmokeTool(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func writeSmokeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
