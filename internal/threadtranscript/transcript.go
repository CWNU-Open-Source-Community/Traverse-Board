package threadtranscript

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runactivity"
)

const (
	ProtocolVersion  = "thread_transcript.v1"
	MaxSourceRecords = 101
	maxIdentityRunes = 256
)

type ActivityType string

const (
	TypeMessage    ActivityType = "message"
	TypeSearch     ActivityType = "search"
	TypeRead       ActivityType = "read"
	TypeEdit       ActivityType = "edit"
	TypeExecute    ActivityType = "execute"
	TypeVerify     ActivityType = "verify"
	TypeApproval   ActivityType = "approval"
	TypeCheckpoint ActivityType = "checkpoint"
	TypeDelivery   ActivityType = "delivery"
)

type Stage string

const (
	StageStarted        Stage = "started"
	StageArgumentsReady Stage = "arguments_ready"
	StageRunning        Stage = "running"
	StageResult         Stage = "result"
	StageBlocked        Stage = "blocked"
)

// Source is the immutable ordering record loaded by the store. Sequence zero
// is reserved for the Run boundary; positive values are durable Run events.
type Source struct {
	RunID                string
	SessionID            string
	Ordinal              int64
	PredecessorRunID     string
	PredecessorRunStatus string
	RunStatus            string
	OperatorContent      string
	OperatorStatus       string
	Sequence             int64
	CreatedAt            time.Time
	Event                *events.Event
}

type Item struct {
	Version               string
	ID                    string
	CanonicalID           string
	RunID                 string
	RunOrdinal            int64
	Sequence              int64
	Position              int
	Type                  ActivityType
	Stage                 Stage
	Kind                  runactivity.Kind
	Source                runactivity.Source
	Title                 string
	Detail                string
	Status                string
	Verifiable            bool
	InstructionAuthorized bool
	AttemptID             string
	ModelAttempt          int
	ToolRound             int
	ToolName              string
	StreamResponseID      string
	StreamItemID          string
	StreamCallID          string
	DurableCallID         string
	SourceRef             string
	BoundaryReason        string
	Provisional           bool
	Durable               bool
	CreatedAt             time.Time
}

func Build(threadID string, source []Source) ([]Item, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || utf8.RuneCountInString(threadID) > maxIdentityRunes {
		return nil, errors.New("thread transcript requires a bounded Thread id")
	}
	if len(source) > MaxSourceRecords {
		return nil, fmt.Errorf("thread transcript accepts at most %d source records", MaxSourceRecords)
	}
	ordered := append([]Source(nil), source...)
	slices.SortFunc(ordered, func(left, right Source) int {
		if order := cmp.Compare(left.Ordinal, right.Ordinal); order != 0 {
			return order
		}
		return cmp.Compare(left.Sequence, right.Sequence)
	})
	items := make([]Item, 0, len(ordered))
	var previousOrdinal, previousSequence int64
	for index, record := range ordered {
		if err := validateSource(record); err != nil {
			return nil, err
		}
		if index > 0 && (record.Ordinal < previousOrdinal ||
			(record.Ordinal == previousOrdinal && record.Sequence <= previousSequence)) {
			return nil, errors.New("thread transcript source ordering is inconsistent")
		}
		previousOrdinal, previousSequence = record.Ordinal, record.Sequence
		if record.Sequence == 0 {
			items = append(items, projectRunBoundary(record))
			continue
		}
		if record.Event.Type == events.OperatorSteeringQueuedEvent &&
			record.OperatorStatus == "pending" && strings.TrimSpace(record.OperatorContent) != "" {
			items = append(items, projectPendingOperatorMessage(record))
			continue
		}
		if record.Event.Type == events.SupervisorToolBatchEvent {
			items = append(items, projectToolBatch(record)...)
			continue
		}
		projection, err := runactivity.Build(record.RunID, []events.Event{*record.Event}, false)
		if err != nil {
			return nil, err
		}
		for _, projected := range projection.Items {
			items = append(items, projectActivity(record, projected))
		}
	}
	return items, nil
}

func projectPendingOperatorMessage(source Source) Item {
	detail := strings.TrimSpace(redact.String(source.OperatorContent))
	if utf8.RuneCountInString(detail) > runactivity.MaxDetailRunes {
		runes := []rune(detail)
		detail = string(runes[:runactivity.MaxDetailRunes-1]) + "…"
	}
	return Item{
		Version: ProtocolVersion, ID: source.Event.EventID,
		CanonicalID: source.Event.SubjectID, RunID: source.RunID,
		RunOrdinal: source.Ordinal, Sequence: source.Sequence, Type: TypeMessage,
		Stage: StageStarted, Kind: runactivity.KindOperatorInput,
		Source: runactivity.SourceOperator, Title: "用户消息已排队", Detail: detail,
		Status: "pending", Verifiable: true, InstructionAuthorized: true,
		SourceRef: source.Event.SubjectID, Durable: true, CreatedAt: source.CreatedAt,
	}
}

func validateSource(source Source) error {
	if strings.TrimSpace(source.RunID) == "" || source.Ordinal <= 0 || source.Sequence < 0 ||
		source.CreatedAt.IsZero() {
		return errors.New("thread transcript source identity is invalid")
	}
	if source.Sequence == 0 {
		if source.Event != nil {
			return errors.New("thread transcript Run boundary contains an event")
		}
		return nil
	}
	if source.Event == nil || source.Event.RunID != source.RunID ||
		source.Event.Sequence != source.Sequence || source.Event.CreatedAt.IsZero() {
		return errors.New("thread transcript event source is inconsistent")
	}
	return nil
}

func projectRunBoundary(source Source) Item {
	detail := "初始 Run"
	reason := "initial"
	if source.PredecessorRunID != "" {
		detail = "从 Run " + source.PredecessorRunID + " 继续"
		reason = "predecessor_terminal"
		if source.PredecessorRunStatus != "" {
			detail += "；前一 Run 状态：" + source.PredecessorRunStatus
			reason += "_" + source.PredecessorRunStatus
		}
	}
	return Item{
		Version: ProtocolVersion, ID: "run-boundary:" + source.RunID,
		CanonicalID: "run:" + source.RunID, RunID: source.RunID,
		RunOrdinal: source.Ordinal, Type: TypeCheckpoint, Stage: stageForStatus(source.RunStatus),
		Kind: runactivity.KindHarnessStatus, Source: runactivity.SourceHarness,
		Title: "Run " + strconv.FormatInt(source.Ordinal, 10), Detail: detail,
		Status: source.RunStatus, BoundaryReason: reason, Verifiable: true,
		Durable: true, CreatedAt: source.CreatedAt,
	}
}

type toolBatchPayload struct {
	Tools            []string `json:"tools"`
	StreamResponseID string   `json:"stream_response_id"`
	StreamItemIDs    []string `json:"stream_item_ids"`
	StreamCallIDs    []string `json:"stream_call_ids"`
}

func projectToolBatch(source Source) []Item {
	var payload toolBatchPayload
	if json.Unmarshal([]byte(source.Event.PayloadJSON), &payload) != nil {
		return nil
	}
	count := len(payload.Tools)
	if len(payload.StreamItemIDs) < count {
		count = len(payload.StreamItemIDs)
	}
	if len(payload.StreamCallIDs) < count {
		count = len(payload.StreamCallIDs)
	}
	if count == 0 {
		return nil
	}
	items := make([]Item, 0, count)
	for index := 0; index < count; index++ {
		toolName := safeIdentity(payload.Tools[index])
		streamItemID := safeIdentity(payload.StreamItemIDs[index])
		streamCallID := safeIdentity(payload.StreamCallIDs[index])
		if toolName == "" || streamItemID == "" || streamCallID == "" {
			continue
		}
		items = append(items, Item{
			Version: ProtocolVersion, ID: source.Event.EventID + ":" + strconv.Itoa(index+1),
			CanonicalID: streamItemID, RunID: source.RunID, RunOrdinal: source.Ordinal,
			Sequence: source.Sequence, Position: index + 1, Type: classifyTool(toolName),
			Stage: StageArgumentsReady, Kind: runactivity.KindToolCall,
			Source: runactivity.SourceHarness, Title: "工具参数已就绪", Status: "pending",
			Verifiable: true, ToolName: toolName,
			StreamResponseID: safeIdentity(payload.StreamResponseID), StreamItemID: streamItemID,
			StreamCallID: streamCallID, Durable: true, CreatedAt: source.CreatedAt,
		})
	}
	return items
}

func projectActivity(source Source, projected runactivity.Item) Item {
	item := Item{
		Version: ProtocolVersion, ID: projected.ID, RunID: source.RunID,
		RunOrdinal: source.Ordinal, Sequence: source.Sequence,
		Type:  classifyActivity(source.Event.Type, projected.Kind, ""),
		Stage: stageForEvent(source.Event.Type, projected.Status), Kind: projected.Kind,
		Source: projected.Source, Title: projected.Title, Detail: projected.Detail,
		Status: projected.Status, Verifiable: projected.Verifiable,
		InstructionAuthorized: projected.InstructionAuthorized,
		AttemptID:             projected.AttemptID, ModelAttempt: projected.ModelAttempt,
		ToolRound: projected.ToolRound, Durable: true, CreatedAt: projected.CreatedAt,
	}
	if source.Event.Type == events.SessionMessageEvent {
		var payload struct {
			SourceRef string `json:"source_ref"`
		}
		_ = json.Unmarshal([]byte(source.Event.PayloadJSON), &payload)
		item.SourceRef = safeIdentity(payload.SourceRef)
	}
	if projected.Kind == runactivity.KindToolCall {
		var payload struct {
			Tool             string `json:"tool"`
			ToolName         string `json:"tool_name"`
			StreamResponseID string `json:"stream_response_id"`
			StreamItemID     string `json:"stream_item_id"`
			StreamCallID     string `json:"stream_call_id"`
			DurableCallID    string `json:"durable_call_id"`
		}
		_ = json.Unmarshal([]byte(source.Event.PayloadJSON), &payload)
		item.ToolName = safeIdentity(payload.Tool)
		if item.ToolName == "" {
			item.ToolName = safeIdentity(payload.ToolName)
		}
		item.StreamResponseID = safeIdentity(payload.StreamResponseID)
		item.StreamItemID = safeIdentity(payload.StreamItemID)
		item.StreamCallID = safeIdentity(payload.StreamCallID)
		item.DurableCallID = safeIdentity(payload.DurableCallID)
		if item.StreamItemID != "" {
			item.CanonicalID = item.StreamItemID
		}
		item.Type = classifyActivity(source.Event.Type, projected.Kind, item.ToolName)
	}
	if item.CanonicalID == "" {
		item.CanonicalID = item.ID
	}
	return item
}

func classifyActivity(eventType string, kind runactivity.Kind, toolName string) ActivityType {
	switch eventType {
	case events.VerificationEvidenceRecordedEvent, events.VerificationPlanRecordedEvent,
		events.VerificationPlanEvidenceAssociatedEvent, events.VerificationSnapshotReceiptRecordedEvent,
		events.VerificationSnapshotReviewRecordedEvent, events.BrowserRuntimeReceiptRecordedEvent:
		return TypeVerify
	case events.DeliveryCheckpointRecordedEvent, events.ArtifactCreatedEvent:
		return TypeDelivery
	case events.WorkspaceCheckpointCreatedEvent, events.WorkspaceCheckpointTransactionPreparedEvent,
		events.WorkspaceCheckpointTransactionCompletedEvent, events.WorkspaceCheckpointTransactionFailedEvent:
		return TypeCheckpoint
	}
	switch kind {
	case runactivity.KindModelUpdate, runactivity.KindOperatorInput:
		return TypeMessage
	case runactivity.KindApproval:
		return TypeApproval
	case runactivity.KindFileChange:
		return TypeEdit
	case runactivity.KindToolCall:
		return classifyTool(toolName)
	case runactivity.KindBrowser:
		return TypeExecute
	case runactivity.KindPlan:
		return TypeCheckpoint
	default:
		return TypeCheckpoint
	}
}

func classifyTool(name string) ActivityType {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "list_workspace", "workspace_list", "workspace_glob", "workspace_grep",
		"workspace_search", "code_search", "search", "find_files",
		"github_review_evidence_list", "code_workspace_symbols", "code_document_symbols",
		"code_references", "code_implementation", "code_call_hierarchy", "code_type_hierarchy":
		return TypeSearch
	case "read_file", "workspace_read", "note_get", "artifact_get",
		"github_review_evidence_read", "code_definition", "code_hover", "code_signature_help":
		return TypeRead
	case "replace_file", "file_edit", "apply_patch", "workspace_restore",
		"workspace_change", "workspace_apply", "workspace_delete":
		return TypeEdit
	case "verification_record", "verification_plan", "ui_evidence", "run_tests", "code_diagnostics":
		return TypeVerify
	case "delivery_checkpoint", "artifact_create", "code_handoff", "plan_delivery_propose":
		return TypeDelivery
	case "work_item_create", "note_create", "specialist_delegation_propose",
		"child_task_propose", "controlled_command_propose", "host_command_propose",
		"one_shot_command_propose", "sandbox_docker_run_propose", "skill_candidate_propose":
		return TypeCheckpoint
	default:
		return TypeExecute
	}
}

func stageForEvent(eventType, status string) Stage {
	if stageForStatus(status) == StageBlocked {
		return StageBlocked
	}
	switch eventType {
	case events.SupervisorToolBatchEvent:
		return StageArgumentsReady
	case events.SupervisorToolExecutionStartedEvent, events.ToolStartedEvent:
		return StageRunning
	case events.ApprovalRequestedEvent, events.ControlledCommandProposedEvent,
		events.FileEditProposedEvent, events.ToolProposedEvent:
		return StageStarted
	case events.SupervisorToolExecutionCompletedEvent, events.SupervisorToolResultEvent,
		events.ToolCompletedEvent, events.SessionMessageEvent,
		events.DeliveryCheckpointRecordedEvent:
		return StageResult
	}
	return stageForStatus(status)
}

func stageForStatus(status string) Stage {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "blocked", "denied", "cancelled", "expired", "waiting_approval":
		return StageBlocked
	case "running", "preparing", "cancelling", "waiting":
		return StageRunning
	case "created", "paused", "pending", "proposed":
		return StageStarted
	default:
		return StageResult
	}
}

func safeIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxIdentityRunes {
		return ""
	}
	safe := redact.String(value)
	if safe != value {
		return "redacted"
	}
	return value
}

// Keep the item-stream dependency explicit: transcript identities and stages
// are the public projection of the provider-neutral protocol introduced by #152.
var _ = llm.ItemStreamProtocolVersion
