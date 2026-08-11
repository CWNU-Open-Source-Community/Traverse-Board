package runactivity

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/events"
)

func TestBuildSeparatesPublicModelUpdatesFromHarnessEvents(t *testing.T) {
	now := time.Now().UTC()
	source := []events.Event{
		event(1, events.ModelStartedEvent,
			`{"provider":"anthropic","model":"test-model","thinking":"private"}`, now),
		event(2, events.SessionMessageEvent,
			`{"role":"assistant","content":"I inspected the files.","source_kind":"model_response","instruction_authorized":false}`, now),
		event(3, events.SupervisorToolBatchEvent,
			`{"tools":["list_workspace","read_file"],"arguments":{"secret":"do-not-show"}}`, now),
		event(4, events.SupervisorToolResultEvent,
			`{"tool":"read_file","status":"completed source-model","result":"private tool output"}`, now),
		event(5, events.ModelDeltaEvent,
			`{"thinking":"hidden chain of thought","text":"raw delta"}`, now),
	}

	got, err := Build("run-1", source, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != ProtocolVersion || got.PrivateReasoningIncluded ||
		got.ThroughSequence != 5 || got.Truncated || len(got.Items) != 4 {
		t.Fatalf("unexpected projection: %#v", got)
	}
	if got.Items[0].Source != SourceHarness || !got.Items[0].Verifiable ||
		got.Items[1].Source != SourceModel || got.Items[1].Verifiable ||
		got.Items[1].Kind != KindModelUpdate {
		t.Fatalf("activity provenance was not separated: %#v", got.Items)
	}
	encoded := strings.Builder{}
	for _, item := range got.Items {
		encoded.WriteString(item.Title)
		encoded.WriteString(item.Detail)
	}
	for _, forbidden := range []string{"private", "do-not-show", "chain of thought", "raw delta"} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("private event data leaked into activity: %q", encoded.String())
		}
	}
	if got.Items[2].Detail != "浏览工作区、读取文件" ||
		got.Items[3].Detail != "读取文件" || got.Items[3].Status != "" {
		t.Fatalf("tool activity was not bounded to names: %#v", got.Items)
	}
}

func TestBuildRedactsAndBoundsPublicMessages(t *testing.T) {
	secret := "sk-" + strings.Repeat("q", 30)
	content := strings.Repeat("界", MaxDetailRunes+50) + " " + secret
	source := []events.Event{event(1, events.SessionMessageEvent,
		`{"role":"user","content":`+quoted(content)+
			`,"source_kind":"operator_message","instruction_authorized":true}`, time.Now().UTC())}

	got, err := Build("run-1", source, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Source != SourceOperator ||
		!got.Items[0].InstructionAuthorized || !got.Truncated {
		t.Fatalf("unexpected operator activity: %#v", got)
	}
	if strings.Contains(got.Items[0].Detail, secret) ||
		len([]rune(got.Items[0].Detail)) > MaxDetailRunes {
		t.Fatalf("public message was not redacted and bounded: %q", got.Items[0].Detail)
	}
}

func TestBuildProjectsPublicCommentaryWithoutTrustingIt(t *testing.T) {
	secret := "sk-" + strings.Repeat("c", 30)
	source := []events.Event{event(1, events.ModelPublicCommentaryEvent,
		`{"version":"model_public_commentary.v1","run_id":"run-1","attempt_id":"attempt-1",`+
			`"model_attempt":2,"tool_round":1,"phase":"before_tools","text":"准备检查构建 `+secret+`"}`,
		time.Now().UTC())}

	got, err := Build("run-1", source, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Source != SourceModel ||
		got.Items[0].Kind != KindModelUpdate || got.Items[0].Verifiable ||
		got.Items[0].AttemptID != "attempt-1" || got.Items[0].ModelAttempt != 2 ||
		got.Items[0].ToolRound != 1 || strings.Contains(got.Items[0].Detail, secret) {
		t.Fatalf("public commentary projection widened trust or leaked data: %#v", got.Items)
	}
}

func TestBuildRejectsCrossRunAndDuplicateEvents(t *testing.T) {
	now := time.Now().UTC()
	crossRun := event(1, events.ModelStartedEvent, `{}`, now)
	crossRun.RunID = "run-2"
	if _, err := Build("run-1", []events.Event{crossRun}, false); err == nil {
		t.Fatal("cross-Run activity source was accepted")
	}
	duplicate := event(1, events.ModelStartedEvent, `{}`, now)
	if _, err := Build("run-1", []events.Event{duplicate, duplicate}, false); err == nil {
		t.Fatal("duplicate event sequence was accepted")
	}
}

func event(sequence int64, eventType string, payload string, createdAt time.Time) events.Event {
	return events.Event{
		EventID: "evt-" + string(rune('a'+sequence)), Version: events.EnvelopeVersion,
		RunID: "run-1", MissionID: "mission-1", Sequence: sequence, Type: eventType,
		Source: "test", PayloadJSON: payload, CreatedAt: createdAt,
	}
}

func quoted(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, current := range value {
		switch current {
		case '\\', '"':
			builder.WriteByte('\\')
			builder.WriteRune(current)
		case '\n':
			builder.WriteString(`\n`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			builder.WriteRune(current)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
