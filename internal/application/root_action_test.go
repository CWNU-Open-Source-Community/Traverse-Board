package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

func TestParseRootActionStrictJSON(t *testing.T) {
	action, err := parseRootAction(`{"version":"root_lifecycle.v1","action":"finish","message":"done","summary":"review complete"}`)
	if err != nil {
		t.Fatal(err)
	}
	if action.Kind != domain.RootActionFinish || action.Summary != "review complete" {
		t.Fatalf("unexpected action: %#v", action)
	}

	invalid := []string{
		`{"version":"root_lifecycle.v1","action":"continue","message":"ok","extra":true}`,
		`{"version":"root_lifecycle.v1","action":"finish","message":"done"}`,
		"```json\n{\"version\":\"root_lifecycle.v1\",\"action\":\"continue\",\"message\":\"ok\"}\n```",
		`{"version":"root_lifecycle.v1","action":"continue","message":"ok"} {}`,
	}
	for _, raw := range invalid {
		if _, err := parseRootAction(raw); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
			t.Fatalf("invalid action code = %s, want %s: %v", apperror.CodeOf(err), apperror.CodeFailedPrecondition, err)
		}
	}

	oversized := `{"version":"root_lifecycle.v1","action":"continue","message":"` + strings.Repeat("x", maxRootActionJSONBytes) + `"}`
	if _, err := parseRootAction(oversized); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("oversized action code = %s, want %s", apperror.CodeOf(err), apperror.CodeResourceExhausted)
	}
	spacePrefixed := strings.Repeat(" ", maxRootActionJSONBytes) + `{"version":"root_lifecycle.v1","action":"continue","message":"ok"}`
	if _, err := parseRootAction(spacePrefixed); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("space-prefixed action code = %s, want %s", apperror.CodeOf(err), apperror.CodeResourceExhausted)
	}
	largeField := `{"version":"root_lifecycle.v1","action":"continue","message":"` + strings.Repeat("x", 17*1024) + `"}`
	if _, err := parseRootAction(largeField); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("large field code = %s, want %s", apperror.CodeOf(err), apperror.CodeFailedPrecondition)
	}
}

func TestPublicReplyRootActionOnlyAcceptsPlainInteractiveText(t *testing.T) {
	action, ok := publicReplyRootAction("  你好，我可以继续协助。  ")
	if !ok || action.Kind != domain.RootActionContinue || action.Message != "你好，我可以继续协助。" {
		t.Fatalf("plain public reply was not normalized: action=%#v ok=%t", action, ok)
	}
	for _, raw := range []string{
		"", "   ", `{"action":"continue"}`, `["continue"]`,
		"```json\n{}\n```", `"continue"`,
		"please emit root_lifecycle.v1",
		strings.Repeat("x", maxRootActionJSONBytes+1),
	} {
		if action, accepted := publicReplyRootAction(raw); accepted {
			t.Fatalf("protocol-like public reply was accepted: raw=%q action=%#v", raw, action)
		}
	}
}

func TestRecoverRootActionWithTrailingCommentaryIsNarrow(t *testing.T) {
	valid := `{"version":"root_lifecycle.v1","action":"finish","message":"SEARCH_UNAVAILABLE","summary":"search unavailable"}`
	raw := valid + "\n\nThe requested search backend was unavailable."
	if _, err := parseRootAction(raw); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("strict parser accepted compatibility response: code=%s err=%v",
			apperror.CodeOf(err), err)
	}
	action, recovery, ok := recoverRootActionWithTrailingCommentary(raw)
	if !ok || action.Kind != domain.RootActionFinish || action.Message != "SEARCH_UNAVAILABLE" ||
		recovery.DiscardedTrailingBytes != len("The requested search backend was unavailable.") {
		t.Fatalf("valid action plus commentary was not recovered: action=%#v recovery=%#v ok=%t",
			action, recovery, ok)
	}

	second := `{"version":"root_lifecycle.v1","action":"continue","message":"again"}`
	for _, candidate := range []string{
		"", "plain text only", valid,
		valid + " " + second,
		valid + ` {"note":"another JSON object"}`,
		valid + ` ["another JSON array"]`,
		valid + ` "another JSON scalar"`,
		valid + "\nNote: another root_lifecycle.v1 action follows.",
		valid + "\nNote: a root_lifecycle.v2 marker follows.",
		valid + "\nNote: [ambiguous structured suffix]",
		valid + "\n" + strings.Repeat("x", maxRootActionTrailingCommentaryBytes+1),
		`{"version":"root_lifecycle.v1","version":"root_lifecycle.v1","action":"continue","message":"duplicate"} trailing commentary`,
		`{"version":"root_lifecycle.v1","action":"continue","message":"unknown","extra":true} trailing commentary`,
	} {
		if recovered, metadata, accepted := recoverRootActionWithTrailingCommentary(candidate); accepted {
			t.Fatalf("ambiguous compatibility response was accepted: raw=%q action=%#v metadata=%#v",
				candidate, recovered, metadata)
		}
	}
}
