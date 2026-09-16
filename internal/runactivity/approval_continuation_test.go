package runactivity

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/events"
)

func TestBuildSeparatesSavedReviewFromFailedContinuation(t *testing.T) {
	for _, test := range []struct {
		name, source, payload, status string
		visible                       bool
	}{
		{"failed", "run_execution_handoff", `{"requested_by":"approval_continuation","status":"failed","error_code":"failed_precondition","reason":"private diagnostic"}`, "failed", true},
		{"empty reply", "run_execution_handoff", `{"requested_by":"approval_continuation","status":"failed","error_code":"failed_precondition","failure_stage":"empty_model_response"}`, "failed", true},
		{"invalid tool", "run_execution_handoff", `{"requested_by":"approval_continuation","status":"failed","error_code":"failed_precondition","failure_stage":"tool_request_rejected"}`, "failed", true},
		{"invalid reply", "run_execution_handoff", `{"requested_by":"approval_continuation","status":"failed","error_code":"failed_precondition","failure_stage":"invalid_model_response"}`, "failed", true},
		{"future stage", "run_execution_handoff", `{"requested_by":"approval_continuation","status":"failed","error_code":"failed_precondition","failure_stage":"future_stage"}`, "failed", true},
		{"cancelled", "run_execution_handoff", `{"requested_by":"approval_continuation","status":"failed","error_code":"cancelled"}`, "cancelled", true},
		{"success", "run_execution_handoff", `{"requested_by":"approval_continuation","status":"completed"}`, "", false},
		{"old handoff", "run_execution_handoff", `{"status":"failed"}`, "", false},
		{"other handoff", "run_execution_handoff", `{"requested_by":"operator","status":"failed"}`, "", false},
		{"wrong source", "model_gateway", `{"requested_by":"approval_continuation","status":"failed"}`, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := event(1, events.RunExecutionHandoffCompletedEvent, test.payload, time.Now().UTC())
			value.Source = test.source
			projection, err := Build("run-1", []events.Event{value}, false)
			if err != nil {
				t.Fatal(err)
			}
			if !test.visible {
				if len(projection.Items) != 0 {
					t.Fatalf("unexpected notice: %+v", projection.Items)
				}
				return
			}
			if len(projection.Items) != 1 {
				t.Fatalf("missing failure notice: %+v", projection.Items)
			}
			item := projection.Items[0]
			if test.name == "empty reply" && !strings.Contains(item.Detail, "模型没有返回有效答复") ||
				test.name == "invalid tool" && !strings.Contains(item.Detail, "该批请求尚未执行") ||
				test.name == "invalid reply" && !strings.Contains(item.Detail, "模型答复未通过校验") ||
				test.name == "future stage" && strings.Contains(item.Detail, "future_stage") {
				t.Fatalf("incorrect model cause: %+v", item)
			}
			if item.Status != test.status || !strings.HasPrefix(item.Detail, "审批已保存，后续执行") ||
				strings.Contains(item.Detail, "private diagnostic") || strings.Contains(item.Detail, "发送新消息") ||
				item.Kind != KindHarnessStatus || item.Source != SourceHarness || !item.Verifiable {
				t.Fatalf("continuation misrepresented review or recovery: %+v", item)
			}
		})
	}
}
