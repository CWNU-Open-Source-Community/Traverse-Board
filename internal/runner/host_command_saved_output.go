package runner

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
)

// MaxHostCommandSavedOutputBytes bounds the combined UTF-8 text of both streams.
// Receipt byte counts and digests describe the original capture, not this text.
const MaxHostCommandSavedOutputBytes = 16 * 1024

type HostCommandSavedStream struct {
	Text      string
	Truncated bool
	Redacted  bool
}

type HostCommandSavedOutput struct {
	Stdout HostCommandSavedStream
	Stderr HostCommandSavedStream
}

// NewHostCommandSavedOutput projects each actual execution stream before any
// evidence envelope is assembled. It never interprets output as delimiters.
func NewHostCommandSavedOutput(execution HostExecutionResult) HostCommandSavedOutput {
	remaining := MaxHostCommandSavedOutputBytes
	project := func(output ControlledOutput) HostCommandSavedStream {
		text := redact.String(SanitizeCommandEvidence(output.Data))
		truncated := output.Truncated || len(text) > remaining
		if len(text) > remaining {
			end := remaining
			for end > 0 && !utf8.ValidString(text[:end]) {
				end--
			}
			text = text[:end]
		}
		remaining -= len(text)
		return HostCommandSavedStream{Text: text, Truncated: truncated, Redacted: true}
	}
	return HostCommandSavedOutput{Stdout: project(execution.Stdout), Stderr: project(execution.Stderr)}
}

func (output HostCommandSavedOutput) Validate() error {
	if len(output.Stdout.Text)+len(output.Stderr.Text) > MaxHostCommandSavedOutputBytes {
		return ErrHostCommandBoundary
	}
	for _, stream := range []HostCommandSavedStream{output.Stdout, output.Stderr} {
		if !stream.Redacted || !utf8.ValidString(stream.Text) ||
			SanitizeCommandEvidence([]byte(stream.Text)) != stream.Text ||
			redact.String(stream.Text) != stream.Text {
			return ErrHostCommandBoundary
		}
	}
	return nil
}

func (output HostCommandSavedOutput) ValidateReceipt(receipt HostExecutionReceipt) error {
	if err := output.Validate(); err != nil {
		return err
	}
	if (receipt.StdoutTruncated && !output.Stdout.Truncated) ||
		(receipt.StderrTruncated && !output.Stderr.Truncated) ||
		(receipt.StdoutCapturedBytes == 0 && (output.Stdout.Text != "" || output.Stdout.Truncated)) ||
		(receipt.StderrCapturedBytes == 0 && (output.Stderr.Text != "" || output.Stderr.Truncated)) {
		return ErrHostCommandBoundary
	}
	return nil
}

// SanitizeCommandEvidence preserves the existing saved-evidence normalization.
// Invalid UTF-8 and control characters remain visibly lossy; no recovery is
// inferred from their replacement characters.
func SanitizeCommandEvidence(data []byte) string {
	value := strings.ToValidUTF8(string(data), "\uFFFD")
	var builder strings.Builder
	builder.Grow(len(value))
	for _, current := range value {
		switch current {
		case '\n', '\t':
			builder.WriteRune(current)
		case '\r':
			builder.WriteRune('\n')
		default:
			if unicode.IsControl(current) {
				builder.WriteRune('\uFFFD')
				continue
			}
			builder.WriteRune(current)
		}
	}
	return builder.String()
}
