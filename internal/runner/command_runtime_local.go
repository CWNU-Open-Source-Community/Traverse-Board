package runner

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

var ErrCommandRuntimeLocalPowerShell = errors.New("Windows Local Sandbox requires PowerShell 7 (pwsh.exe)")

// NormalizeLocalSandboxCommandRuntimeSpec makes the actual launch script part
// of the canonical argv before review/replay fingerprints or Jobs are created.
// LPAC cannot inspect host ancestor directories, so PowerShell's filesystem
// provider needs a process-local drive rooted at the already-owned Workspace.
func NormalizeLocalSandboxCommandRuntimeSpec(spec CommandRuntimeSpec, workspaceRoot string) (CommandRuntimeResolvedSpec, error) {
	resolved, err := NormalizeCommandRuntimeSpec(spec, workspaceRoot)
	if err != nil || resolved.Spec.Profile != CommandRuntimePowerShell {
		return resolved, err
	}
	if !strings.EqualFold(filepath.Base(resolved.ExecutablePath), "pwsh.exe") {
		return CommandRuntimeResolvedSpec{}, ErrCommandRuntimeLocalPowerShell
	}
	relative := resolved.Spec.WorkingDirectory
	depth := 0
	location := `TraverseWorkspace:\`
	if relative != "." {
		depth = len(strings.Split(relative, "/"))
		location += strings.ReplaceAll(relative, "/", `\`)
	}
	// GetDirectoryName is lexical: it does not inspect or grant access to host
	// ancestors. Starting from the pinned OS cwd preserves ../ access within
	// Workspace when the requested working_directory is a subdirectory.
	bootstrap := fmt.Sprintf(`try {
  [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
  $OutputEncoding = [Console]::OutputEncoding
  $__traverseWorkspaceRoot = [Environment]::CurrentDirectory
  for ($__traverseLevel = 0; $__traverseLevel -lt %d; $__traverseLevel++) {
    $__traverseWorkspaceRoot = [IO.Path]::GetDirectoryName($__traverseWorkspaceRoot)
  }
  $null = New-PSDrive -Name TraverseWorkspace -PSProvider FileSystem -Root $__traverseWorkspaceRoot -Scope Global -ErrorAction Stop
  Set-Location -LiteralPath '%s' -ErrorAction Stop
  if (([scriptblock]::Create('%s')).Ast.ParamBlock) {
    throw 'Inline param declarations require a project .ps1 file; invoke it with & ./script.ps1.'
  }
} catch {
  [Console]::Error.WriteLine('PowerShell workspace initialization failed: ' + $_.Exception.Message)
  exit 125
}
`, depth, strings.ReplaceAll(location, "'", "''"), strings.ReplaceAll(resolved.Spec.Script, "'", "''"))
	// Keep the original command body at top level. Invoking it as a nested
	// scriptblock resets $? and can falsely turn errors or native failures into
	// exit 0, including on early return. Declarations belong in project .ps1
	// files; param is explicitly rejected because after initialization it would
	// otherwise parse as an ordinary command and could continue after failing.
	command := bootstrap + resolved.Spec.Script
	// PowerShell's documented encoded form carries multiline Unicode without
	// weakening the sandbox manifest's control-character restrictions on argv.
	units := utf16.Encode([]rune(command))
	encoded := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	resolved.CanonicalArgv = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded)}
	return resolved, nil
}
