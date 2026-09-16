package application

import (
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
)

func TestThreadActivitySavedSearchFailureExplainsProviderResponse(t *testing.T) {
	payload := []byte(`{"version":"web_search.v1","query":"Riemann Hypothesis latest progress 2026","limit":3}`)
	call := threadActivityFactsCall(toolgateway.WebSearchTool, payload, nil, "")
	call.Status, call.ErrorCode = domain.SupervisorToolFailed, "UNAVAILABLE"
	for _, tc := range []struct{ reason, want string }{
		{"search_not_performed", "搜索服务未返回已完成的联网搜索，未取得有效检索结果"},
		{"response_incomplete", "搜索服务未完成本次响应，未取得完整搜索结果"},
		{"response_invalid", "搜索服务返回了无法使用的响应，软件未取得有效搜索结果"},
		{"transport_unavailable", "未能完成与搜索服务的连接或响应读取，请检查网络及服务状态"},
		{"provider unreachable (network/proxy)", "未能连接到搜索服务，请检查网络与代理设置"},
		{"no usable results", "搜索服务已连接但未返回可用结果"},
		{"provider_rejected", "搜索服务拒绝了请求，请检查模型配置、凭据和服务状态"},
		{"tool_unsupported", "当前模型接口不支持所请求的搜索工具"},
		{"credential_unavailable", "无法读取搜索服务凭据，请检查模型设置"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			raw, _ := json.Marshal(supervisorToolResultEnvelope{Version: supervisorToolResultVersion,
				Tool: string(toolgateway.WebSearchTool), Status: "failed", Code: "UNAVAILABLE",
				Message: "web search provider request failed (" + tc.reason + "); no fallback provider was attempted"})
			call.ResultJSON = string(raw)
			detail, found, err := ProjectThreadActivityTypedDetail(call)
			if err != nil || !found || detail.WebSearch == nil {
				t.Fatalf("found=%t detail=%#v err=%v", found, detail, err)
			}
			search := detail.WebSearch
			if search.Boundary.FailureReason != tc.want || search.Boundary.ErrorCode != "UNAVAILABLE" ||
				search.SourceCount != 0 || search.Citeable || len(search.Sources) != 0 {
				t.Fatalf("wrong failed search projection: %#v", search)
			}
		})
	}
}

func TestThreadActivitySearchFailureDoesNotExposeArbitraryErrorText(t *testing.T) {
	for _, message := range []string{
		"private provider body secret-canary",
		"web search provider request failed (secret-canary); no fallback provider was attempted",
		"web search provider request failed (response_invalid); no fallback provider was attempted; secret-canary",
	} {
		call := threadActivityFactsCall(toolgateway.WebSearchTool,
			[]byte(`{"version":"web_search.v1","query":"public query","limit":1}`), nil, "")
		call.Status, call.ErrorCode = domain.SupervisorToolFailed, "UNAVAILABLE"
		raw, _ := json.Marshal(supervisorToolResultEnvelope{Version: supervisorToolResultVersion,
			Tool: string(toolgateway.WebSearchTool), Status: "failed", Code: "UNAVAILABLE", Message: message})
		call.ResultJSON = string(raw)
		detail, found, err := ProjectThreadActivityTypedDetail(call)
		if err != nil || !found || detail.WebSearch == nil ||
			detail.WebSearch.Boundary.FailureReason != "搜索服务未能返回可用结果" {
			t.Fatalf("unexpected fallback: %#v %v", detail, err)
		}
		encoded, _ := json.Marshal(detail)
		if strings.Contains(string(encoded), "secret-canary") || strings.Contains(string(encoded), message) {
			t.Fatal("raw search failure escaped the typed boundary")
		}
		call.Status, call.ErrorCode = domain.SupervisorToolDenied, "POLICY_DENIED"
		detail, _, err = ProjectThreadActivityTypedDetail(call)
		if err != nil || detail.WebSearch.Boundary.FailureReason != "当前执行边界未授权此操作" {
			t.Fatalf("search diagnosis overrode actual permission denial: %#v %v", detail, err)
		}
	}
}
