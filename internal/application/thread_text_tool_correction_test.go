package application_test

import (
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
)

const rejectedTextToolEnvelope = `<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="note_create"><｜｜DSML｜｜ parameter name="title" string="true">FAKE NOTE</｜｜DSML｜｜ parameter></｜｜DSML｜｜ invoke></｜｜DSML｜｜ calls>`

func TestThreadTrailingToolTextRequiresOneNativeCorrection(t *testing.T) {
	pseudo := `{"version":"root_lifecycle.v1","action":"continue","message":"record the requested note"}` + "\n" + rejectedTextToolEnvelope
	for _, test := range []struct {
		name      string
		after     []*llm.ChatResponse
		notes     int
		wantError bool
	}{
		{"native tool then finish", []*llm.ChatResponse{
			toolResponse("real-note", "note_create", `{"title":"REAL NOTE","content":"actual native call"}`),
			textResponse(`{"version":"root_lifecycle.v1","action":"finish","message":"已保存真实笔记。"}`),
		}, 1, false},
		{"repeated markup exhausts", []*llm.ChatResponse{textResponse(pseudo)}, 0, true},
		{"tool-free continue cannot conceal no execution", []*llm.ChatResponse{textResponse(`{"version":"root_lifecycle.v1","action":"continue","message":"later"}`)}, 0, true},
		{"correction grants no unoffered tool", []*llm.ChatResponse{toolResponse("invalid", "unoffered_tool", `{}`)}, 0, true},
		{"explicit wait returns control", []*llm.ChatResponse{textResponse(`{"version":"root_lifecycle.v1","action":"wait","message":"需要笔记内容。","reason":"请提供内容"}`)}, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &scriptedToolProvider{responses: append([]*llm.ChatResponse{textResponse(pseudo)}, test.after...)}
			st, turns, request := threadControlFixture(t, provider)
			request.Content = "请保存笔记；缺内容请问我。"
			result, err := turns.Execute(t.Context(), request)
			requests := provider.Requests()
			if (err != nil) != test.wantError || len(requests) != 1+len(test.after) {
				t.Fatalf("calls=%d err=%v", len(requests), err)
			}
			if requests[1].Metadata["protocol_repair"] != "1" || !hasToolSpec(requests[1], "note_create") {
				t.Fatal("repair lost native channel")
			}
			foundGuidance := false
			for _, message := range requests[1].Messages {
				if strings.Contains(message.Content, "That text did not execute any tool") {
					foundGuidance = true
				}
			}
			if !foundGuidance {
				t.Fatal("repair omitted actual no-execution fact")
			}
			thread, err := st.GetThread(t.Context(), request.ThreadID)
			if err != nil {
				t.Fatal(err)
			}
			notes, err := st.ListNotes(t.Context(), domain.NoteFilter{RunID: thread.LastRunID})
			if err != nil || len(notes) != test.notes {
				t.Fatalf("effects=%d err=%v", len(notes), err)
			}
			for _, note := range notes {
				if note.Title != "REAL NOTE" {
					t.Fatal("textual pseudo call was executed")
				}
			}
			list, err := st.ListRunEvents(t.Context(), thread.LastRunID)
			if err != nil || countEventType(list, events.ProtocolRepairRequestedEvent) != 1 || countEventType(list, events.ProtocolRepairStartedEvent) != 1 || countEventType(list, events.SupervisorToolBatchEvent) != test.notes {
				t.Fatal("repair count or actual tool batch changed")
			}
			cp, _, err := st.GetSupervisorCheckpoint(t.Context(), thread.LastRunID)
			if err != nil || cp.TotalTokens != int64(4*len(requests)) {
				t.Fatalf("usage=%d err=%v", cp.TotalTokens, err)
			}
			if !test.wantError && result.Submission.Message.Status != domain.OperatorSteeringCommitted {
				t.Fatal("explicit reply did not settle original input")
			}
			_, _ = turns.Execute(t.Context(), request)
			if len(provider.Requests()) != len(requests) {
				t.Fatal("same key replay resent request")
			}
		})
	}
}

func TestThreadDiscussionOfToolMarkupRemainsOrdinaryReply(t *testing.T) {
	text, _ := json.Marshal(domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionContinue, Message: "格式说明及代码示例：" + rejectedTextToolEnvelope})
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{textResponse(string(text))}}
	st, turns, request := threadControlFixture(t, provider)
	request.Content = "请解释 DSML 格式并给代码示例，不要调用工具。"
	result, err := turns.Execute(t.Context(), request)
	if err != nil || len(provider.Requests()) != 1 {
		t.Fatalf("calls=%d err=%v", len(provider.Requests()), err)
	}
	list, err := st.ListRunEvents(t.Context(), result.Submission.Run.ID)
	if err != nil || countEventType(list, events.ProtocolRepairRequestedEvent) != 0 || countEventType(list, events.SupervisorToolBatchEvent) != 0 {
		t.Fatal("ordinary format explanation acquired repair or tools")
	}
}

func TestThreadContinueCorrectionUsesSameWhitespaceNormalizationAsStore(t *testing.T) {
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		toolResponse("actual", "note_create", `{"title":"One note","content":"actual effect"}`),
		textResponse(`{"version":" root_lifecycle.v1 ","action":"continue","message":"` + strings.Repeat(" ", 17*1024) + `next step ","summary":" ","reason":" "}`),
		textResponse(`{"version":"root_lifecycle.v1","action":"finish","message":"结束回复。"}`),
	}}
	st, turns, request := threadControlFixture(t, provider)
	result, err := turns.Execute(t.Context(), request)
	if err != nil || len(provider.Requests()) != 3 {
		t.Fatalf("calls=%d err=%v", len(provider.Requests()), err)
	}
	cp, _, err := st.GetSupervisorCheckpoint(t.Context(), result.Submission.Run.ID)
	if err != nil || cp.TotalTokens != 12 {
		t.Fatalf("normalized response lost actual usage: %+v %v", cp, err)
	}
}
