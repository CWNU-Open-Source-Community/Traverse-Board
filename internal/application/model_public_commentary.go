package application

import (
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/redact"
)

type publicCommentaryPreviewer struct {
	checker  policy.Checker
	lastText string
	complete bool
}

func newPublicCommentaryPreviewer(checker policy.Checker) *publicCommentaryPreviewer {
	return &publicCommentaryPreviewer{checker: checker}
}

func (p *publicCommentaryPreviewer) Update(raw string, complete bool) (string, bool, bool) {
	text, ok := safePublicCommentaryText(p.checker, raw, !complete)
	if !ok {
		text = ""
		complete = false
	}
	changed := text != p.lastText || complete != p.complete
	p.lastText = text
	p.complete = complete
	return text, complete, changed
}

func prepareModelPublicCommentary(checker policy.Checker, checkpoint domain.SupervisorCheckpoint,
	attempt llm.ModelAttempt, raw string,
) (domain.ModelPublicCommentary, bool) {
	text, ok := safePublicCommentaryText(checker, raw, false)
	if !ok {
		return domain.ModelPublicCommentary{}, false
	}
	value := domain.ModelPublicCommentary{
		Version: domain.ModelPublicCommentaryVersion, RunID: checkpoint.RunID,
		AttemptID: checkpoint.AttemptID, ModelAttempt: attempt.Number,
		ToolRound: attempt.ToolRound + 1, Phase: domain.PublicCommentaryBeforeTools,
		Text: text,
	}
	if value.Validate() != nil {
		return domain.ModelPublicCommentary{}, false
	}
	return value, true
}

func safePublicCommentaryText(checker policy.Checker, raw string, provisional bool) (string, bool) {
	if raw == "" || !utf8.ValidString(raw) {
		return "", false
	}
	value := strings.TrimSpace(raw)
	if value == "" || unsafePublicCommentaryShape(value) {
		return "", false
	}
	if provisional {
		value = truncateStreamingSecret(value)
		value = withholdPreviewTail(value, rootPreviewTailRunes)
		if value == "" {
			return "", false
		}
	}
	if checker != nil {
		decision := checker.CheckText("model_public_commentary", value)
		if !decision.Allowed {
			return "", false
		}
	}
	value = strings.TrimSpace(redact.String(value))
	if value == "" {
		return "", false
	}
	runes := []rune(value)
	if len(runes) > domain.MaxPublicCommentaryTextRunes {
		value = strings.TrimSpace(string(runes[:domain.MaxPublicCommentaryTextRunes-1])) + "…"
	}
	return value, value != ""
}

func unsafePublicCommentaryShape(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	switch trimmed[0] {
	case '{', '[':
		return true
	case '<':
		return true
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "```") || strings.HasPrefix(lower, "~~~") {
		return true
	}
	privateMarkers := []string{
		"<thinking", "</thinking", "<analysis", "</analysis",
		"\"thinking\":", "\"reasoning\":", "private reasoning:", "chain of thought:",
	}
	for _, marker := range privateMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
