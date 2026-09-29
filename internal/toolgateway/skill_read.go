package toolgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/tools"
)

const SkillReadTool ToolName = "skill_read"

type SkillReadRequest struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
}

type BuiltinSkillDescriptor struct {
	SkillReadRequest
	Description  string `json:"description"`
	ContentBytes int    `json:"content_bytes"`
}

type SkillReadExecutor interface {
	ReadBuiltinSkill(context.Context, ToolCall) (json.RawMessage, error)
}

func SkillReadToolDefinition(catalog []BuiltinSkillDescriptor) ToolDefinition {
	description := "Read a relevant bundled workflow skill before using it. Choose by the task and description; do not load every skill. Copy the exact name, version and content_sha256 from the available catalog. Reading supplies guidance only: it neither changes operator selections nor grants tools or permissions. Successful reads are restored by the Harness as bounded guidance on subsequent requests, including after compaction."
	if len(catalog) > 0 {
		encoded, _ := json.Marshal(catalog)
		description += " Available skills for this mode: " + string(encoded)
	}
	return ToolDefinition{Name: SkillReadTool, Class: ClassRunMemory, Approval: ApprovalAutomatic,
		Description: description,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["name","version","content_sha256"],"properties":{"name":{"type":"string","minLength":1,"maxLength":64},"version":{"type":"string","minLength":1,"maxLength":32},"content_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"}}}`)}
}

func NormalizeSkillReadPayload(payload json.RawMessage) (SkillReadRequest, json.RawMessage, error) {
	var input SkillReadRequest
	if len(payload) == 0 || len(payload) > 1024 || !utf8.Valid(payload) {
		return input, nil, errors.New("skill_read requires a bounded JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return input, nil, errors.New("skill_read contains trailing data")
	}
	if input.Name == "" || len(input.Name) > 64 || strings.Trim(input.Name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" ||
		input.Version == "" || len(input.Version) > 32 || strings.Trim(input.Version, "0123456789.") != "" ||
		len(input.ContentSHA256) != 64 || strings.Trim(input.ContentSHA256, "0123456789abcdef") != "" {
		return input, nil, errors.New("skill_read requires an exact catalog name, version and digest")
	}
	canonical, err := json.Marshal(input)
	return input, canonical, err
}

func validateSkillReadCall(call ToolCall) error {
	if call.Name != SkillReadTool || len(call.Arguments) != 0 || call.RequestedBy != "run_supervisor" ||
		call.RunID == "" || call.SessionID == "" || call.AgentID == "" || call.AgentAttemptID == "" ||
		call.OperationKey == "" || call.LeaseID == "" || call.LeaseGeneration <= 0 {
		return apperror.New(apperror.CodeFailedPrecondition, "skill_read requires the current fenced root Supervisor")
	}
	_, _, err := NormalizeSkillReadPayload(call.Payload)
	return err
}

func (g *Gateway) WithSkillReadExecutor(executor SkillReadExecutor) *Gateway {
	if g != nil {
		g.skillRead = executor
	}
	return g
}

func (g *Gateway) invokeSkillRead(ctx context.Context, call ToolCall) (Outcome, error) {
	if g.skillRead == nil {
		return Outcome{}, errors.New("embedded skill reader is unavailable")
	}
	if err := validateSkillReadCall(call); err != nil {
		return Outcome{}, err
	}
	checked := g.checker.CheckToolCall(tools.Call{Name: string(call.Name)})
	if !checked.Allowed || checked.NeedsApproval {
		checked.Allowed = false
		return deniedOutcome(call, checked)
	}
	decision, err := gatewayDecision(checked, ApprovalAutomatic, "low")
	if err != nil {
		return Outcome{}, err
	}
	started := time.Now().UTC()
	content, err := g.skillRead.ReadBuiltinSkill(ctx, call)
	if err != nil {
		return Outcome{}, err
	}
	if !json.Valid(content) || len(content) > MaxResultStdoutBytes {
		return Outcome{}, errors.New("invalid embedded skill read result")
	}
	completed := time.Now().UTC()
	return validateOutcome(Outcome{Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{Backend: "embedded_skills", Status: StatusCompleted, StartedAt: started, CompletedAt: &completed},
		Result: &Result{Status: StatusCompleted, ExitCode: 0, MIME: "application/json", CompletedAt: completed,
			Stdout: string(content), Metadata: map[string]string{"skill_read": "true", "capability_grant": "false"}}}, nil)
}
