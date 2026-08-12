package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
)

func TestPrepareModelPublicCommentaryAcceptsOnlySafePublicProse(t *testing.T) {
	checker := policy.NewDefaultChecker()
	checkpoint := domain.SupervisorCheckpoint{RunID: "run-commentary", AttemptID: "attempt-commentary"}
	attempt := llm.ModelAttempt{Number: 2, ToolRound: 1}

	value, ok := prepareModelPublicCommentary(checker, checkpoint, attempt,
		"已完成差异检查。下一步运行 **API 校验**。")
	if !ok || value.Version != domain.ModelPublicCommentaryVersion ||
		value.RunID != checkpoint.RunID || value.AttemptID != checkpoint.AttemptID ||
		value.ModelAttempt != 2 || value.ToolRound != 2 ||
		value.Text != "已完成差异检查。下一步运行 **API 校验**。" {
		t.Fatalf("safe public commentary was not prepared: %#v ok=%v", value, ok)
	}

	rejected := []string{
		`{"version":"root_lifecycle.v1","action":"continue","message":"hidden"}`,
		"```json\n{\"thinking\":\"hidden\"}\n```",
		"<thinking>private chain of thought</thinking>",
		"I will run masscan against the public internet",
	}
	for _, input := range rejected {
		if got, accepted := prepareModelPublicCommentary(checker, checkpoint, attempt, input); accepted {
			t.Fatalf("unsafe commentary was accepted: input=%q got=%#v", input, got)
		}
	}
}

func TestSafePublicCommentaryRedactsSecretsAndBoundsText(t *testing.T) {
	checker := policy.NewDefaultChecker()
	secret := "Authorization: Bearer test-public-commentary-secret"
	text, ok := safePublicCommentaryText(checker, "准备调用工具。 "+secret, false)
	if !ok || strings.Contains(text, "test-public-commentary-secret") {
		t.Fatalf("commentary secret was not redacted: %q ok=%v", text, ok)
	}

	long := strings.Repeat("进", maxPublicCommentaryRunes+1)
	text, ok = safePublicCommentaryText(checker, long, false)
	if ok || text != "" {
		t.Fatalf("oversized commentary was not rejected: runes=%d ok=%v", len([]rune(text)), ok)
	}
	essay := "## 研究方向\n- 第一条\n- 第二条\n这是完整最终答复，不是工具前公开进度。"
	if text, ok = safePublicCommentaryText(checker, essay, false); ok || text != "" {
		t.Fatalf("block Markdown answer was accepted as commentary: %q", text)
	}
}

func TestPublicCommentaryPreviewKeepsSafeToolCallText(t *testing.T) {
	previewer := newPublicCommentaryPreviewer(policy.NewDefaultChecker())
	raw := strings.Repeat("已完成工作区检查，", 10) + "下一步调用测试工具。"
	partial, complete, changed := previewer.Update(raw, false)
	if !changed || complete || partial == "" || !strings.HasPrefix(raw, partial) {
		t.Fatalf("safe provisional commentary was not published: text=%q complete=%v changed=%v",
			partial, complete, changed)
	}
	final, complete, changed := previewer.Update(raw, true)
	if !changed || !complete || final != raw {
		t.Fatalf("safe final commentary was not published: text=%q complete=%v changed=%v",
			final, complete, changed)
	}
}
