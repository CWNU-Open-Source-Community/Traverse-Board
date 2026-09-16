package runner

import (
	"fmt"
	"strings"
	"unicode"
)

// Keep initialization in the canonical argv so review/replay fingerprints cover
// it. Never rewrite a stored Job or inject it later in the process starter.
func commandRuntimePowerShellArguments(script string) ([]string, error) {
	if hostPowerShellStartsWithDeclaration(commandRuntimePowerShellOpening(script)) {
		return nil, fmt.Errorf("%w: inline PowerShell param/using declarations require a project .ps1 file; invoke it with & ./script.ps1", ErrCommandRuntimeBoundary)
	}
	// Reuse the Host UTF-8 policy, but not its single-line command validator:
	// command_runtime accepts multiline scripts. -Command joins these arguments
	// into one top-level body, preserving explicit exit, return and final failure.
	return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-OutputFormat", "Text", "-Command", hostPowerShellUTF8Bootstrap, script}, nil
}

// The existing Host declaration detector handles a single-line command. Skip
// leading line/block comments here as well, because runtime scripts are multiline.
// Do not inspect quoted values or declarations inside functions/script files.
func commandRuntimePowerShellOpening(script string) string {
	for {
		script = strings.TrimLeftFunc(script, unicode.IsSpace)
		switch {
		case strings.HasPrefix(script, "#"):
			end := strings.IndexAny(script, "\r\n")
			if end < 0 {
				return ""
			}
			script = script[end:]
		case strings.HasPrefix(script, "<#"):
			depth, index := 1, 2
			for depth > 0 && index+1 < len(script) {
				switch script[index : index+2] {
				case "<#":
					depth++
					index += 2
				case "#>":
					depth--
					index += 2
				default:
					index++
				}
			}
			if depth != 0 {
				return script // The shell will reject its incomplete block comment.
			}
			script = script[index:]
		default:
			return script
		}
	}
}
