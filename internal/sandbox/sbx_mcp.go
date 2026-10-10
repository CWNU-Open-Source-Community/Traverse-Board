package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cyberagent-workbench/internal/sbxmcp"
)

// Prepare registers this application's zero-tool stdio entrypoint in the fixed
// product namespace. It runs only during an enabled native startup. Settings
// reads use Readiness and never mutate the daemon's MCP registrations.
func (b *SBXBackend) Prepare(ctx context.Context) error {
	if b == nil || ctx == nil {
		return ErrSBXBoundary
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.helperPrepared.Store(false)
	if !b.config.Enabled {
		return nil
	}
	if b.closed || b.lock == nil || !b.Available() || b.helperSHA == "" {
		return ErrSBXUnavailable
	}
	if err := b.acquireNamespace(); err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := b.daemon.Check(bounded); err != nil {
		return err
	}
	if err := b.checkMCPHelperFile(); err != nil {
		return err
	}
	if err := b.probeMCPHelper(bounded); err != nil {
		return err
	}
	// Read the complete owned record first. Existing exact registrations need
	// no CLI startup; malformed or inaccessible records must never be replaced.
	err := b.verifyMCPHelper(bounded)
	if err == nil {
		b.helperPrepared.Store(true)
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Only a verified absence permits creating this hash-derived name. Older
	// application versions retain their own registrations and VMs.
	added, err := b.call(bounded, []string{"mcp", "add", b.helperName,
		"--command", b.config.HelperExecutable, "--args=" + sbxmcp.Arg,
		"--dir", filepath.Dir(b.config.HelperExecutable)}, nil, 16*1024)
	if err != nil || added.ExitCode != 0 {
		return errors.Join(ErrSBXUnavailable, err)
	}
	if err := b.verifyMCPHelper(bounded); err != nil {
		return err
	}
	b.helperPrepared.Store(true)
	return nil
}

func (b *SBXBackend) checkMCPHelperFile() error {
	if b.helperSHA == "" || b.helperName == "" || b.config.HelperExecutable == "" {
		return ErrSBXUnavailable
	}
	digest, err := sbxFileDigest(b.config.HelperExecutable)
	if err != nil || digest != b.helperSHA {
		return ErrSBXBoundary
	}
	return nil
}

func (b *SBXBackend) verifyMCPHelper(ctx context.Context) error {
	if err := b.checkMCPHelperFile(); err != nil {
		return err
	}
	// v0.47 mcp inspect reads this same Store directly and returns only a
	// subset. Verify its complete owned registration without starting the CLI.
	return b.daemon.VerifyHelper(ctx, b.helperName, b.config.HelperExecutable, sbxmcp.Arg)
}

func (b *SBXBackend) probeMCPHelper(ctx context.Context) error {
	// Probe the packaged executable, not an application configuration claim.
	// A successful handshake also catches a binary that lacks the entrypoint.
	request := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"traverse-sbx-binding","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
{"jsonrpc":"2.0","id":3,"method":"resources/list","params":{}}
{"jsonrpc":"2.0","id":4,"method":"resources/templates/list","params":{}}
{"jsonrpc":"2.0","id":5,"method":"prompts/list","params":{}}
`
	result, err := b.transport.Run(ctx, SBXProcessRequest{Executable: b.config.HelperExecutable,
		Arguments: []string{sbxmcp.Arg}, Directory: filepath.Dir(b.config.HelperExecutable),
		Environment: sbxHostEnvironment(), Stdin: strings.NewReader(request), OutputLimit: 16 * 1024})
	if err != nil || result.ExitCode != 0 || len(result.Stderr) != 0 || len(result.Stdout) > 16*1024 {
		return ErrSBXBoundary
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
	for id := 1; id <= 5; id++ {
		var envelope struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		if decoder.Decode(&envelope) != nil || envelope.JSONRPC != "2.0" || envelope.ID != id || len(envelope.Error) != 0 ||
			sbxUniqueJSON(envelope.Result) != nil {
			return ErrSBXBoundary
		}
		if id == 1 {
			var initialization struct {
				ProtocolVersion string `json:"protocolVersion"`
				ServerInfo      struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			}
			if json.Unmarshal(envelope.Result, &initialization) != nil || initialization.ProtocolVersion != sbxmcp.ProtocolVersion ||
				initialization.ServerInfo.Name != sbxmcp.ServerName || initialization.ServerInfo.Version != sbxmcp.ServerVersion {
				return ErrSBXBoundary
			}
			continue
		}
		field := []string{"", "", "tools", "resources", "resourceTemplates", "prompts"}[id]
		var value map[string]json.RawMessage
		if json.Unmarshal(envelope.Result, &value) != nil || len(value) != 1 || string(value[field]) != "[]" {
			return ErrSBXBoundary
		}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrSBXBoundary
	}
	return b.checkMCPHelperFile()
}
