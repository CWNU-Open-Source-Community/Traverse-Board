package outputsafe

import (
	"strings"
	"testing"
)

func TestStreamCarriesEscapeAndUTF8StateAcrossChunks(t *testing.T) {
	stream := &Stream{}
	parts := [][]byte{
		[]byte("safe\x1b[3"),
		append([]byte("1mred\x1b]0;title"), '\x1b'),
		append([]byte("\\"), []byte("你")[:2]...),
		append([]byte{[]byte("你")[2]}, []byte("好\n")...),
	}
	var result strings.Builder
	for _, part := range parts {
		result.WriteString(stream.Feed(part))
	}
	result.WriteString(stream.Flush())
	if got := result.String(); got != "safered你好\n" {
		t.Fatalf("sanitized stream=%q", got)
	}
}

func TestSanitizeRemovesC1UnicodeControlsAndRedactsSecrets(t *testing.T) {
	got := Sanitize([]byte("before\u009b31mafter\u009dtitle\u009c token=secret-value-1234567890"))
	if strings.Contains(got, "31m") || strings.Contains(got, "title") ||
		strings.Contains(got, "secret-value") {
		t.Fatalf("unsafe output escaped sanitizer: %q", got)
	}
}

func TestRedactingStreamCarriesSecretAndPrivateKeyStateAcrossChunks(t *testing.T) {
	stream := &RedactingStream{}
	parts := []string{
		"visible\n-----BE", "GIN TEST PRIVATE KEY-----\n",
		"private-material\n-----END TEST PRI", "VATE KEY-----\n",
		"token=secret-value-", "1234567890\nafter\n",
	}
	var result strings.Builder
	for _, part := range parts {
		result.WriteString(stream.Feed([]byte(part)))
	}
	result.WriteString(stream.Flush())
	got := result.String()
	if strings.Contains(got, "private-material") || strings.Contains(got, "secret-value") ||
		!strings.Contains(got, "[REDACTED:private-key]") ||
		!strings.Contains(got, "[REDACTED:secret]") ||
		!strings.Contains(got, "visible") || !strings.Contains(got, "after") {
		t.Fatalf("streaming redaction failed: %q", got)
	}
}

func TestRedactingStreamReplacesOversizedUnterminatedLine(t *testing.T) {
	stream := &RedactingStream{}
	value := strings.Repeat("x", maxRedactingStreamLineBytes+128)
	got := stream.Feed([]byte(value)) + stream.Feed([]byte("\nvisible\n")) + stream.Flush()
	if strings.Contains(got, strings.Repeat("x", 128)) ||
		got != "[REDACTED:oversized-line]\nvisible\n" {
		t.Fatalf("oversized line was not replaced: %q", got)
	}
}

func TestRedactingStreamRemovesPresentationControlWhitespace(t *testing.T) {
	stream := &RedactingStream{}
	got := stream.Feed([]byte("first\rsecond\tcolumn\n")) + stream.Flush()
	if got != "firstsecond    column\n" {
		t.Fatalf("presentation controls were retained: %q", got)
	}
}
