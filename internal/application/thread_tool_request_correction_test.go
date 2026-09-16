package application_test

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
)

func TestThreadCorrectsRejectedToolBatchOnceAndContinuesActualResults(t *testing.T) {
	bad := toolResponse("bad", "note_create", `{"title":123,"content":"rejected-private-body"}`)
	// One invalid member rejects the entire batch, including its valid member.
	bad.ToolCalls = append(toolResponse("not-executed", "note_create", `{"title":"must not be saved","content":"rejected sibling"}`).ToolCalls, bad.ToolCalls...)
	provider := &scriptedToolProvider{responses: []*llm.ChatResponse{
		bad,
		toolResponse("corrected", "note_create", `{"title":"Corrected once","content":"verified first effect"}`),
		toolResponse("next", "note_create", `{"title":"Next once","content":"verified second effect"}`),
		textResponse(`{"version":"root_lifecycle.v1","action":"finish","message":"已保存两条笔记；尚未修改代码或运行测试。"}`),
	}}
	st, turns, request := threadControlFixture(t, provider)
	result, err := turns.Execute(t.Context(), request)
	if err != nil || result.Execution == nil || len(provider.Requests()) != 4 {
		t.Fatalf("corrected Thread did not complete its original input: %#v %v", result, err)
	}
	notes, err := st.ListNotes(t.Context(), domain.NoteFilter{RunID: result.Submission.Run.ID})
	if err != nil || len(notes) != 2 {
		t.Fatalf("rejected member executed or corrected effects lost: %#v %v", notes, err)
	}
	for _, note := range notes {
		if note.Title == "must not be saved" {
			t.Fatal("valid sibling of a rejected batch was executed")
		}
	}
	requests := provider.Requests()
	for index := 1; index < len(requests); index++ {
		if requests[index].Metadata["protocol_repair"] != "1" || !hasToolSpec(requests[index], "note_create") {
			t.Fatal("tool correction lost the offered tools or durable single repair")
		}
		for _, message := range requests[index].Messages {
			if strings.Contains(message.Content, "rejected-private-body") {
				t.Fatal("rejected arguments were echoed as diagnostic input")
			}
		}
	}
	for _, note := range notes {
		index := 2
		if note.Title == "Next once" {
			index = 3
		}
		if !hasToolResult(requests[index], note.ID) {
			t.Fatal("corrected tool result identity did not reach the continuation")
		}
	}
	list, err := st.ListRunEvents(t.Context(), result.Submission.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]int{
		events.ModelFailedEvent: 1, events.ModelCompletedEvent: 3,
		events.ProtocolRepairRequestedEvent: 1, events.ProtocolRepairStartedEvent: 1,
		events.ProtocolRepairCompletedEvent: 1, events.SupervisorToolBatchEvent: 2,
		events.NoteCreatedEvent: 2, events.SupervisorRunCompletedEvent: 0,
	} {
		if got := countEventType(list, kind); got != want {
			t.Fatalf("%s=%d want=%d", kind, got, want)
		}
	}
	cp, found, err := st.GetSupervisorCheckpoint(t.Context(), result.Submission.Run.ID)
	if err != nil || !found || cp.TotalTokens != 16 || cp.RepairPhase != domain.ProtocolRepairNone {
		t.Fatalf("usage or correction closure lost: %#v %v", cp, err)
	}
	if _, err := turns.Execute(t.Context(), request); err != nil || len(provider.Requests()) != 4 {
		t.Fatal("original input confirmation replayed a corrected effect")
	}
}
