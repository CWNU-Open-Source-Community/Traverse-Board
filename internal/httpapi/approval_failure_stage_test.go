package httpapi

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestApprovalFailureRecoveryViewUsesOnlyRecordedStage(t *testing.T) {
	for _, tc := range []struct{ stage, code, contains string }{
		{domain.ThreadFailureEmptyModelResponse, "failed_precondition", "模型没有返回有效答复"},
		{domain.ThreadFailureToolRequestRejected, "failed_precondition", "该批请求尚未执行"},
		{domain.ThreadFailureInvalidModelResponse, "failed_precondition", "模型答复格式无效"},
		{"", "failed_precondition", "执行条件未满足"},
		{"future_stage", "failed_precondition", "执行条件未满足"},
		{domain.ThreadFailureEmptyModelResponse, "unavailable", "暂不可用"},
	} {
		view := threadRunRecoveryView(domain.ThreadRunRecovery{ErrorCode: tc.code, FailureStage: tc.stage,
			Detail: "untrusted text saying empty_model_response"})
		if !strings.Contains(view.Detail, tc.contains) || strings.Contains(view.Detail, "untrusted") {
			t.Fatalf("stage=%q code=%q detail=%q", tc.stage, tc.code, view.Detail)
		}
	}
}
