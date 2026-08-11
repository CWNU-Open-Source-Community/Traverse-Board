package runactivity

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/session"
)

const (
	ProtocolVersion = "run_activity.v1"
	MaxSourceEvents = 100
	MaxDetailRunes  = 4096
	maxLabelRunes   = 128
	maxToolNames    = 12
)

type Source string

const (
	SourceHarness  Source = "harness"
	SourceModel    Source = "model"
	SourceOperator Source = "operator"
)

type Kind string

const (
	KindHarnessStatus Kind = "harness_status"
	KindModelUpdate   Kind = "model_update"
	KindOperatorInput Kind = "operator_input"
	KindModelCall     Kind = "model_call"
	KindToolCall      Kind = "tool_call"
	KindApproval      Kind = "approval"
	KindFileChange    Kind = "file_change"
	KindPlan          Kind = "plan"
)

type Item struct {
	ID                    string
	Sequence              int64
	Kind                  Kind
	Source                Source
	Title                 string
	Detail                string
	Status                string
	Verifiable            bool
	InstructionAuthorized bool
	AttemptID             string
	ModelAttempt          int
	ToolRound             int
	CreatedAt             time.Time
}

type Projection struct {
	Version                  string
	RunID                    string
	ThroughSequence          int64
	Truncated                bool
	PrivateReasoningIncluded bool
	Items                    []Item
}

// Build creates a display-only projection. It never forwards an event payload
// wholesale and deliberately has no mapping for provider thinking or model deltas.
func Build(runID string, source []events.Event, truncated bool) (Projection, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return Projection{}, fmt.Errorf("run activity requires a run id")
	}
	if len(source) > MaxSourceEvents {
		return Projection{}, fmt.Errorf("run activity accepts at most %d source events", MaxSourceEvents)
	}
	ordered := append([]events.Event(nil), source...)
	slices.SortFunc(ordered, func(left, right events.Event) int {
		return cmp.Compare(left.Sequence, right.Sequence)
	})
	projection := Projection{
		Version: ProtocolVersion, RunID: runID, Truncated: truncated,
		PrivateReasoningIncluded: false, Items: make([]Item, 0, len(ordered)),
	}
	var previous int64
	for _, event := range ordered {
		if event.RunID != runID || event.Sequence <= 0 || event.Sequence == previous {
			return Projection{}, fmt.Errorf("run activity source events are inconsistent")
		}
		previous = event.Sequence
		if event.Sequence > projection.ThroughSequence {
			projection.ThroughSequence = event.Sequence
		}
		if item, ok := projectEvent(event); ok {
			projection.Items = append(projection.Items, item)
		}
	}
	return projection, nil
}

func projectEvent(event events.Event) (Item, bool) {
	base := Item{
		ID: event.EventID, Sequence: event.Sequence, Source: SourceHarness,
		Kind: KindHarnessStatus, Verifiable: true, CreatedAt: event.CreatedAt,
	}
	switch event.Type {
	case events.SessionMessageEvent:
		return projectMessage(event, base)
	case events.ModelStartedEvent:
		base.Kind, base.Title, base.Status = KindModelCall, "模型调用开始", "running"
		base.Detail = modelIdentity(event.PayloadJSON)
	case events.ModelPublicCommentaryEvent:
		base.Kind, base.Source, base.Title = KindModelUpdate, SourceModel, "Prayu 进度"
		base.Detail = stringField(event.PayloadJSON, "text")
		base.Verifiable = false
		base.AttemptID = cleanLabel(stringField(event.PayloadJSON, "attempt_id"))
		base.ModelAttempt = int(intField(event.PayloadJSON, "model_attempt"))
		base.ToolRound = int(intField(event.PayloadJSON, "tool_round"))
	case events.ModelCompletedEvent:
		base.Kind, base.Title, base.Status = KindModelCall, "模型响应完成", "completed"
		base.Detail = completedModelDetail(event.PayloadJSON)
	case events.ModelFailedEvent:
		base.Kind, base.Title, base.Status = KindModelCall, "模型调用失败", "failed"
	case events.ModelCancelRequestedEvent:
		base.Kind, base.Title, base.Status = KindModelCall, "模型取消已请求", "cancelling"
	case events.ModelCancelObservedEvent:
		base.Kind, base.Title, base.Status = KindModelCall, "模型取消已确认", "cancelled"
	case events.AgentTurnStartedEvent:
		base.Title, base.Status = "Agent 回合开始", "running"
	case events.AgentTurnCompletedEvent:
		base.Title, base.Status = "Agent 回合完成", "completed"
	case events.AgentTurnFailedEvent:
		base.Title, base.Status = "Agent 回合失败", "failed"
	case events.SupervisorToolBatchEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "工具调用已请求", "running"
		base.Detail = toolList(event.PayloadJSON)
	case events.SupervisorToolResultEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "工具结果已记录",
			cleanStatus(stringField(event.PayloadJSON, "status"))
		if tool := cleanLabel(stringField(event.PayloadJSON, "tool")); tool != "" {
			base.Detail = toolDisplayName(tool)
		}
	case events.SupervisorToolCompleteEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "工具批次完成", "completed"
	case events.ToolProposedEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "工具操作已提议", "pending"
		base.Detail = toolNameFromPayload(event.PayloadJSON)
	case events.ToolStartedEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "工具操作开始", "running"
		base.Detail = toolNameFromPayload(event.PayloadJSON)
	case events.ToolCompletedEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "工具操作完成", "completed"
		base.Detail = toolNameFromPayload(event.PayloadJSON)
	case events.ToolFailedEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "工具操作失败", "failed"
		base.Detail = toolNameFromPayload(event.PayloadJSON)
	case events.ApprovalRequestedEvent:
		base.Kind, base.Title, base.Status = KindApproval, "等待用户审批", "pending"
	case events.ApprovalDecidedEvent:
		base.Kind, base.Title, base.Status = KindApproval, "用户审批已记录", "completed"
	case events.ControlledCommandProposedEvent:
		base.Kind, base.Title, base.Status = KindApproval, "受控命令已提议", "pending"
	case events.ControlledCommandProposalReviewedEvent:
		base.Kind, base.Title, base.Status = KindApproval, "受控命令审批已记录", "completed"
	case events.ControlledCommandProposalResultRecordedEvent:
		base.Kind, base.Title, base.Status = KindToolCall, "受控命令结果已记录", "completed"
	case events.FileEditProposedEvent:
		base.Kind, base.Title, base.Status = KindFileChange, "文件修改已提议", "pending"
	case events.FileEditApprovedEvent:
		base.Kind, base.Title, base.Status = KindFileChange, "文件修改已批准", "approved"
	case events.FileEditAppliedEvent, events.FileEditApplyCompletedEvent:
		base.Kind, base.Title, base.Status = KindFileChange, "文件修改已应用", "completed"
	case events.FileEditDeniedEvent:
		base.Kind, base.Title, base.Status = KindFileChange, "文件修改已拒绝", "denied"
	case events.FileEditFailedEvent:
		base.Kind, base.Title, base.Status = KindFileChange, "文件修改失败", "failed"
	case events.PlanDeliveryProposedEvent:
		base.Kind, base.Title, base.Status = KindPlan, "计划方向已生成", "pending"
	case events.PlanDeliveryDirectionSelectedEvent:
		base.Kind, base.Title, base.Status = KindPlan, "计划方向已选择", "selected"
	case events.DeliveryCheckpointRecordedEvent:
		base.Kind, base.Title, base.Status = KindPlan, "切片交付检查点已记录", "completed"
	case events.ProtocolRepairStartedEvent, events.AgentProtocolRepairStartedEvent:
		base.Title, base.Status = "模型协议修复开始", "running"
	case events.ProtocolRepairCompletedEvent, events.AgentProtocolRepairCompletedEvent:
		base.Title, base.Status = "模型协议修复完成", "completed"
	case events.ProtocolRepairFailedEvent, events.AgentProtocolRepairFailedEvent:
		base.Title, base.Status = "模型协议修复失败", "failed"
	case events.SupervisorRunWaitingEvent:
		base.Title, base.Status = "Run 正在等待用户或依赖", "waiting"
	case events.SupervisorRunCompletedEvent:
		base.Title, base.Status = "Run 已完成", "completed"
	case events.SupervisorRunFailedEvent:
		base.Title, base.Status = "Run 已失败", "failed"
	case events.SupervisorLivelockDetectedEvent:
		base.Title, base.Status = "检测到无进展循环", "blocked"
	case events.RunStatusChangedEvent:
		base.Title = "Run 状态已更新"
		base.Status = cleanStatus(stringField(event.PayloadJSON, "status"))
	case events.AgentInboxContextPreparedEvent:
		base.Title, base.Status = "上下文交付已准备", "pending"
	case events.AgentInboxContextCommittedEvent:
		base.Title, base.Status = "上下文交付已提交", "completed"
	case events.AgentInboxContextSupersededEvent:
		base.Title, base.Status = "陈旧上下文已替换", "superseded"
	default:
		return Item{}, false
	}
	base.Title = cleanLabel(base.Title)
	base.Detail = cleanDetail(base.Detail)
	base.Status = cleanStatus(base.Status)
	return base, base.Title != ""
}

func projectMessage(event events.Event, base Item) (Item, bool) {
	var payload struct {
		Role                  string `json:"role"`
		Content               string `json:"content"`
		SourceKind            string `json:"source_kind"`
		InstructionAuthorized bool   `json:"instruction_authorized"`
	}
	if json.Unmarshal([]byte(event.PayloadJSON), &payload) != nil {
		return Item{}, false
	}
	switch payload.Role {
	case "assistant":
		if payload.SourceKind != session.SourceModelResponse {
			return Item{}, false
		}
		base.Kind, base.Source, base.Title = KindModelUpdate, SourceModel, "Prayu 更新"
		base.Verifiable = false
	case "user":
		if payload.SourceKind != session.SourceOperatorMessage {
			return Item{}, false
		}
		base.Kind, base.Source, base.Title = KindOperatorInput, SourceOperator, "用户消息"
		base.Verifiable = true
	default:
		return Item{}, false
	}
	base.Detail = cleanDetail(payload.Content)
	base.InstructionAuthorized = payload.InstructionAuthorized
	return base, base.Detail != ""
}

func modelIdentity(payloadJSON string) string {
	provider := cleanLabel(stringField(payloadJSON, "provider"))
	model := cleanLabel(stringField(payloadJSON, "model"))
	switch {
	case provider != "" && model != "":
		return provider + " / " + model
	case model != "":
		return model
	default:
		return provider
	}
}

func completedModelDetail(payloadJSON string) string {
	identity := modelIdentity(payloadJSON)
	elapsed := intField(payloadJSON, "elapsed_millis")
	var usage struct {
		TotalTokens int `json:"total_tokens"`
	}
	raw := rawField(payloadJSON, "usage")
	_ = json.Unmarshal(raw, &usage)
	parts := make([]string, 0, 3)
	if identity != "" {
		parts = append(parts, identity)
	}
	if usage.TotalTokens > 0 {
		parts = append(parts, strconv.Itoa(usage.TotalTokens)+" tokens")
	}
	if elapsed > 0 {
		parts = append(parts, strconv.FormatInt(elapsed, 10)+" ms")
	}
	return strings.Join(parts, " · ")
}

func toolList(payloadJSON string) string {
	var names []string
	_ = json.Unmarshal(rawField(payloadJSON, "tools"), &names)
	if len(names) > maxToolNames {
		names = names[:maxToolNames]
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name = cleanLabel(name); name != "" {
			out = append(out, toolDisplayName(name))
		}
	}
	return strings.Join(out, "、")
}

func toolNameFromPayload(payloadJSON string) string {
	for _, key := range []string{"tool_name", "tool"} {
		if name := cleanLabel(stringField(payloadJSON, key)); name != "" {
			return toolDisplayName(name)
		}
	}
	return ""
}

func toolDisplayName(name string) string {
	switch name {
	case "read_file":
		return "读取文件"
	case "list_workspace":
		return "浏览工作区"
	case "replace_file":
		return "文件修改提案"
	case "work_item_create":
		return "创建工作项"
	case "note_create":
		return "记录记忆"
	case "plan_delivery_propose":
		return "生成交付计划"
	case "specialist_delegation_propose":
		return "提出子 Agent 委派"
	case "controlled_command_propose":
		return "提出受控命令"
	default:
		return name
	}
}

func stringField(payloadJSON string, key string) string {
	var value string
	_ = json.Unmarshal(rawField(payloadJSON, key), &value)
	return value
}

func intField(payloadJSON string, key string) int64 {
	var value int64
	_ = json.Unmarshal(rawField(payloadJSON, key), &value)
	return value
}

func rawField(payloadJSON string, key string) json.RawMessage {
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(payloadJSON), &payload) != nil {
		return nil
	}
	return payload[key]
}

func cleanLabel(value string) string {
	value = strings.TrimSpace(redact.String(value))
	if value == "" || !utf8.ValidString(value) {
		return ""
	}
	value = strings.Map(func(current rune) rune {
		if unicode.IsControl(current) {
			return -1
		}
		return current
	}, value)
	runes := []rune(value)
	if len(runes) > maxLabelRunes {
		runes = runes[:maxLabelRunes]
	}
	return strings.TrimSpace(string(runes))
}

func cleanDetail(value string) string {
	value = strings.TrimSpace(redact.String(value))
	if value == "" || !utf8.ValidString(value) {
		return ""
	}
	value = strings.Map(func(current rune) rune {
		if current == '\n' || current == '\t' {
			return current
		}
		if unicode.IsControl(current) {
			return '�'
		}
		return current
	}, value)
	runes := []rune(value)
	if len(runes) > MaxDetailRunes {
		runes = append(runes[:MaxDetailRunes-1], '…')
	}
	return strings.TrimSpace(string(runes))
}

func cleanStatus(value string) string {
	value = cleanLabel(value)
	switch value {
	case "approved", "blocked", "cancelled", "cancelling", "completed", "denied",
		"failed", "pending", "running", "selected", "superseded", "waiting":
		return value
	default:
		return ""
	}
}
