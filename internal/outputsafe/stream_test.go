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
