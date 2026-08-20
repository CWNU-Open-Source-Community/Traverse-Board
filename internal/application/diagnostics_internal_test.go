package application

import (
	"strings"
	"testing"
)

func TestBoundedDiagnosticMetadataRedactsAndRejectsUnsafeFields(t *testing.T) {
	if got := boundedDiagnosticMetadata("tool.completed", 128, false); got != "tool.completed" {
		t.Fatalf("ordinary metadata changed: %q", got)
	}
	if got := boundedDiagnosticMetadata("token=secret-value-1234567890", 128, false); strings.Contains(got, "secret-value") || !strings.Contains(got, "REDACTED") {
		t.Fatalf("secret-shaped metadata was not redacted: %q", got)
	}
	for name, value := range map[string]string{
		"control": "source\nforged", "oversized": strings.Repeat("x", 129),
		"invalid_utf8": string([]byte{0xff}),
	} {
		if got := boundedDiagnosticMetadata(value, 128, false); got != "withheld" {
			t.Fatalf("%s metadata=%q want withheld", name, got)
		}
	}
	if got := boundedDiagnosticMetadata("", 256, true); got != "" {
		t.Fatalf("optional empty subject changed: %q", got)
	}
}
