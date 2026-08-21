package githubreview

import (
	"strings"
	"testing"
)

func TestSanitizeRemoteTextMakesRemoteSyntaxInertAndBounded(t *testing.T) {
	token := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	value := "\x1b]0;title\x07::set-output name=x::owned\n" +
		"<script>alert(1)</script> ![x](https://evil.example/x) " +
		"[click](javascript:alert(1)) https://evil.example " + token + "\n" +
		strings.Repeat("界", 100)
	evidence := SanitizeRemoteText(value, 180)
	if !evidence.Untrusted || !evidence.Truncated || evidence.StoredBytes > 180 ||
		strings.Contains(evidence.Text, "\x1b") || strings.Contains(evidence.Text, "<script>") ||
		strings.Contains(evidence.Text, "javascript:") || strings.Contains(evidence.Text, "https://") ||
		strings.Contains(evidence.Text, token) || !strings.Contains(evidence.Text, "[REDACTED:github-token]") {
		t.Fatalf("remote evidence was not safely sanitized: %#v", evidence)
	}
	if evidence.SHA256 == "" || evidence.OriginalBytes != len([]byte(value)) {
		t.Fatalf("remote evidence provenance is incomplete: %#v", evidence)
	}
}

func TestSanitizeRemoteTextPreservesUTF8Boundary(t *testing.T) {
	evidence := SanitizeRemoteText(strings.Repeat("界", 10), 10)
	if !evidence.Truncated || !strings.HasPrefix(strings.Repeat("界", 10), evidence.Text) ||
		evidence.StoredBytes > 10 {
		t.Fatalf("UTF-8 truncation is invalid: %#v", evidence)
	}
}
