package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/policy"
)

func TestRootMessagePreviewStreamsOnlyStableTopLevelMessage(t *testing.T) {
	prefix := `{"version":"root_lifecycle.v1","action":"continue","message":"`
	previewer := newRootMessagePreviewer(policy.NewDefaultChecker())
	text, complete, changed := previewer.Update(prefix + strings.Repeat("甲", 80))
	if !changed || complete || text != strings.Repeat("甲", 16) {
		t.Fatalf("unexpected stable preview text=%q complete=%t changed=%t", text, complete, changed)
	}
	text, complete, changed = previewer.Update(prefix + `你好\n\u4e16\ud83d\ude00"}`)
	if !changed || !complete || text != "你好\n世😀" {
		t.Fatalf("unexpected completed preview text=%q complete=%t changed=%t", text, complete, changed)
	}
}

func TestRootMessagePreviewFailsClosedForUntrustedShapesAndPolicy(t *testing.T) {
	tests := []string{
		`{"version":"root_lifecycle.v1","action":"continue","payload":{"message":"nested"}}`,
		`{"version":"root_lifecycle.v1","action":"continue","message":"first","message":"second"}`,
		`{"version":"root_lifecycle.v1","action":"continue","message":"run masscan now"}`,
		"```json\n{\"version\":\"root_lifecycle.v1\",\"action\":\"continue\",\"message\":\"fenced\"}",
	}
	for _, raw := range tests {
		previewer := newRootMessagePreviewer(policy.NewDefaultChecker())
		text, complete, _ := previewer.Update(raw)
		if text != "" || complete {
			t.Fatalf("unsafe shape exposed a preview for %q: text=%q complete=%t", raw, text, complete)
		}
	}
}

func TestRootMessagePreviewWithholdsPartialSecretsAndRedactsCompletedSecrets(t *testing.T) {
	prefix := `{"version":"root_lifecycle.v1","action":"continue","message":"`
	previewer := newRootMessagePreviewer(policy.NewDefaultChecker())
	raw := prefix + strings.Repeat("safe ", 30) + "sk-" + strings.Repeat("1", 30)
	text, complete, _ := previewer.Update(raw)
	if complete || strings.Contains(text, "sk-") || strings.Contains(text, "123456") || text == "" {
		t.Fatalf("partial secret was not withheld: %q", text)
	}
	text, complete, _ = previewer.Update(raw + `"}`)
	if !complete || strings.Contains(text, "123456") || !strings.Contains(text, "[REDACTED:api-key]") {
		t.Fatalf("completed secret was not redacted: %q complete=%t", text, complete)
	}
}

func TestParseRootActionRejectsDuplicateFields(t *testing.T) {
	_, err := parseRootAction(
		`{"version":"root_lifecycle.v1","action":"continue","message":"first","message":"second"}`)
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("duplicate root action field was not rejected: %v", err)
	}
}
