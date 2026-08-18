package toolgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/tools"
)

const (
	DebugTerminalProtocolVersion = "debug_terminal.v1"
	DebugTerminalActionWrite     = "write"
	DebugTerminalActionRead      = "read"
	MaxDebugTerminalWaitMillis   = 5_000
	MaxDebugTerminalOutputBytes  = 64 * 1024
)

type DebugTerminalInput struct {
	Version          string `json:"version"`
	Action           string `json:"action"`
	Command          string `json:"command,omitempty"`
	Cursor           uint64 `json:"cursor,omitempty"`
	MaxBytes         int    `json:"max_bytes"`
	WaitMilliseconds int    `json:"wait_milliseconds"`
}

func (i DebugTerminalInput) Validate() error {
	if i.Version != DebugTerminalProtocolVersion ||
		(i.Action != DebugTerminalActionWrite &&
			i.Action != DebugTerminalActionRead) ||
		i.MaxBytes < 1 || i.MaxBytes > MaxDebugTerminalOutputBytes ||
		i.WaitMilliseconds < 0 ||
		i.WaitMilliseconds > MaxDebugTerminalWaitMillis {
		return errors.New("debug terminal payload is invalid")
	}
	if i.Action == DebugTerminalActionRead {
		if i.Command != "" {
			return errors.New("debug terminal read cannot include a command")
		}
		return nil
	}
	if i.Cursor != 0 || i.Command == "" ||
		strings.TrimSpace(i.Command) == "" || !utf8.ValidString(i.Command) ||
		strings.ContainsRune(i.Command, 0) ||
		strings.ContainsAny(i.Command, "\r\n") ||
		len([]byte(i.Command)) > MaxCommandBytes ||
		redact.String(i.Command) != i.Command {
		return errors.New("debug terminal command is invalid or contains secret-like material")
	}
	for _, value := range i.Command {
		if unicode.IsControl(value) && value != '\t' {
			return errors.New("debug terminal command contains an unsupported control character")
		}
	}
	return nil
}

type DebugTerminalContext struct {
	InvocationID    string
	OperationKey    string
	RunID           string
	RootAgentID     string
	SessionID       string
	WorkspaceID     string
	LeaseID         string
	LeaseGeneration int64
	RequestedBy     string
	PolicyDecision  Decision
}

func (c DebugTerminalContext) Validate() error {
	for label, value := range map[string]string{
		"invocation id": c.InvocationID, "operation key": c.OperationKey,
		"Run id": c.RunID, "root Agent id": c.RootAgentID,
		"Session id": c.SessionID, "Workspace id": c.WorkspaceID,
		"lease id": c.LeaseID, "requester": c.RequestedBy,
	} {
		if value == "" || strings.TrimSpace(value) != value ||
			!utf8.ValidString(value) || len([]rune(value)) > MaxToolIdentityRunes {
			return fmt.Errorf("debug terminal %s is invalid", label)
		}
	}
	if !domain.ValidAgentID(c.RootAgentID) || c.LeaseGeneration <= 0 ||
		c.RequestedBy != "run_supervisor" {
		return errors.New("debug terminal requires a fenced root Supervisor scope")
	}
	if err := c.PolicyDecision.Validate(); err != nil {
		return err
	}
	if !c.PolicyDecision.Allowed ||
		c.PolicyDecision.Approval != ApprovalAutomatic {
		return errors.New("debug terminal requires an allowed short-lived lease decision")
	}
	return nil
}

type DebugTerminalExecutionResult struct {
	BindingID         string
	TerminalSessionID string
	Backend           string
	BaseCursor        uint64
	NextCursor        uint64
	Output            []byte
	Dropped           bool
	State             string
	InputSubmitted    bool
	BytesWritten      int
	Replayed          bool
}

func (r DebugTerminalExecutionResult) Validate() error {
	if !domain.ValidAgentID(r.BindingID) ||
		!domain.ValidAgentID(r.TerminalSessionID) ||
		strings.TrimSpace(r.Backend) == "" ||
		strings.TrimSpace(r.Backend) != r.Backend ||
		len([]rune(r.Backend)) > MaxExecutionBackendRunes ||
		r.NextCursor < r.BaseCursor || len(r.Output) > MaxDebugTerminalOutputBytes ||
		strings.TrimSpace(r.State) == "" || len([]rune(r.State)) > 64 ||
		r.BytesWritten < 0 {
		return errors.New("debug terminal execution result is invalid")
	}
	if !r.InputSubmitted && (r.BytesWritten != 0 || r.Replayed) {
		return errors.New("debug terminal read result cannot report an input write")
	}
	return nil
}

type DebugTerminalExecutor interface {
	ExecuteDebugTerminal(context.Context, DebugTerminalContext,
		DebugTerminalInput) (DebugTerminalExecutionResult, error)
}

var debugTerminalDefinition = ToolDefinition{
	Name: DebugTerminalTool, Class: ClassShell, Approval: ApprovalAutomatic,
	Description: "Write one policy-checked command to, or read one bounded cursor page from, the current user-owned Debug terminal. Available only in Code/Deliver/Debug after the operator grants a short-lived Agent-input lease. A write submits the command to a persistent shell; the canonical command and sanitized bounded result become durable Supervisor evidence, output may be incomplete, and background processes may continue after this call. Never submit secrets.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","action","max_bytes","wait_milliseconds"],"properties":{"version":{"const":"debug_terminal.v1"},"action":{"enum":["write","read"]},"command":{"type":"string","minLength":1,"maxLength":16384},"cursor":{"type":"integer","minimum":0},"max_bytes":{"type":"integer","minimum":1,"maximum":65536},"wait_milliseconds":{"type":"integer","minimum":0,"maximum":5000}},"oneOf":[{"properties":{"action":{"const":"write"}},"required":["command"],"not":{"required":["cursor"]}},{"properties":{"action":{"const":"read"}},"not":{"required":["command"]}}]}`),
}

func normalizeDebugTerminalPayload(payload json.RawMessage) (
	DebugTerminalInput, json.RawMessage, error,
) {
	input, err := decodeStructuredPayload[DebugTerminalInput](payload)
	if err != nil {
		return DebugTerminalInput{}, nil, err
	}
	fields, err := structuredPayloadFields(payload)
	if err != nil {
		return DebugTerminalInput{}, nil, err
	}
	input.Version = strings.TrimSpace(input.Version)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	maxBytesPresent, maxBytesNonNull := structuredPayloadField(fields, "max_bytes")
	waitPresent, waitNonNull := structuredPayloadField(fields, "wait_milliseconds")
	commandPresent, commandNonNull := structuredPayloadField(fields, "command")
	cursorPresent, cursorNonNull := structuredPayloadField(fields, "cursor")
	if !maxBytesPresent || !maxBytesNonNull || !waitPresent || !waitNonNull {
		return DebugTerminalInput{}, nil,
			errors.New("debug terminal bounded read fields are required")
	}
	switch input.Action {
	case DebugTerminalActionWrite:
		if !commandPresent || !commandNonNull || cursorPresent {
			return DebugTerminalInput{}, nil,
				errors.New("debug terminal write fields are invalid")
		}
	case DebugTerminalActionRead:
		if commandPresent || cursorPresent && !cursorNonNull {
			return DebugTerminalInput{}, nil,
				errors.New("debug terminal read fields are invalid")
		}
	}
	if err := input.Validate(); err != nil {
		return DebugTerminalInput{}, nil, err
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return DebugTerminalInput{}, nil, err
	}
	return input, canonical, nil
}

func (g *Gateway) WithDebugTerminalExecutor(executor DebugTerminalExecutor) *Gateway {
	if g != nil {
		g.debugTerminal = executor
	}
	return g
}

func (g *Gateway) invokeDebugTerminal(ctx context.Context, call ToolCall) (
	Outcome, error,
) {
	input, canonical, err := normalizeDebugTerminalPayload(call.Payload)
	if err != nil {
		return Outcome{}, err
	}
	call.Payload = canonical
	policyDecision := policy.Decision{
		Allowed: true, Reason: "bounded Debug terminal output read allowed",
		Risk: "low",
	}
	if input.Action == DebugTerminalActionWrite {
		policyDecision = g.checker.CheckToolCall(tools.Call{
			Name: string(ShellTool), Args: map[string]string{"command": input.Command},
		})
	}
	if !policyDecision.Allowed || policyDecision.NeedsApproval {
		if policyDecision.NeedsApproval {
			policyDecision.Allowed = false
			policyDecision.Risk = defaultRisk(policyDecision.Risk, "medium")
			policyDecision.Reason = "Debug terminal command requires a separate per-command approval: " +
				policyDecision.Reason
		}
		if err := g.recordDebugTerminalPolicyDecision(ctx, call,
			policyDecision); err != nil {
			return Outcome{}, err
		}
		return deniedOutcome(call, policyDecision)
	}
	decision, err := gatewayDecision(policyDecision, ApprovalAutomatic, "high")
	if err != nil {
		return Outcome{}, err
	}
	scope := DebugTerminalContext{
		InvocationID: call.InvocationID, OperationKey: call.OperationKey,
		RunID: call.RunID, RootAgentID: call.AgentID,
		SessionID: call.SessionID, WorkspaceID: call.WorkspaceID,
		LeaseID: call.LeaseID, LeaseGeneration: call.LeaseGeneration,
		RequestedBy: call.RequestedBy, PolicyDecision: decision,
	}
	if err := scope.Validate(); err != nil {
		return Outcome{}, err
	}
	started := time.Now().UTC()
	result, err := g.debugTerminal.ExecuteDebugTerminal(ctx, scope, input)
	if err != nil {
		return Outcome{}, err
	}
	if err := result.Validate(); err != nil {
		return Outcome{}, err
	}
	completed := time.Now().UTC()
	stdout := sanitizeDebugTerminalOutput(result.Output)
	stdout, truncated := boundResultText(stdout, MaxResultStdoutBytes)
	metadata := map[string]string{
		"binding_id":                 result.BindingID,
		"terminal_session_id":        result.TerminalSessionID,
		"backend":                    result.Backend,
		"base_cursor":                strconv.FormatUint(result.BaseCursor, 10),
		"next_cursor":                strconv.FormatUint(result.NextCursor, 10),
		"dropped":                    strconv.FormatBool(result.Dropped),
		"terminal_state":             result.State,
		"input_submitted":            strconv.FormatBool(result.InputSubmitted),
		"bytes_written":              strconv.Itoa(result.BytesWritten),
		"replayed":                   strconv.FormatBool(result.Replayed),
		"output_complete":            "false",
		"lease_token_exposed":        "false",
		"raw_output_persisted":       "false",
		"command_persisted":          "true",
		"sanitized_output_persisted": "true",
		"output_source":              "untrusted_debug_terminal",
		"instruction_authorized":     "false",
	}
	outcome := Outcome{
		Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{Backend: "debug_terminal",
			Status: StatusCompleted, StartedAt: started, CompletedAt: &completed},
		Result: &Result{Status: StatusCompleted, Stdout: stdout, ExitCode: 0,
			MIME: "text/plain; charset=utf-8", Truncated: truncated,
			Metadata: metadata, CompletedAt: completed},
	}
	return validateOutcome(outcome, nil)
}

func (g *Gateway) recordDebugTerminalPolicyDecision(ctx context.Context,
	call ToolCall, decision policy.Decision,
) error {
	if g == nil || g.policyRecorder == nil {
		return errors.New("debug terminal policy decision recorder is required")
	}
	return g.policyRecorder.RecordPolicyDecision(ctx, policy.DecisionRecord{
		SessionID: call.SessionID, SubjectID: call.InvocationID,
		Context: "tool_run.debug_terminal", Decision: decision,
	})
}

func sanitizeDebugTerminalOutput(data []byte) string {
	// Remove terminal control sequences before the page becomes model context.
	// The renderer still receives the untouched PTY bytes through its own ring.
	input := strings.ToValidUTF8(string(data), "?")
	// Terminals may encode C1 controls either as the common ESC-prefixed form
	// or as their Unicode control code points. Normalize the latter so their
	// parameter/string payload is removed with the same parser below instead of
	// leaving misleading fragments such as "31m" in model-visible text.
	input = strings.NewReplacer(
		"\u0090", "\x1bP", // DCS
		"\u0098", "\x1bX", // SOS
		"\u009b", "\x1b[", // CSI
		"\u009c", "\x1b\\", // ST
		"\u009d", "\x1b]", // OSC
		"\u009e", "\x1b^", // PM
		"\u009f", "\x1b_", // APC
	).Replace(input)
	var output strings.Builder
	output.Grow(len(input))
	for index := 0; index < len(input); {
		if input[index] != 0x1b {
			value := input[index]
			if value == '\n' || value == '\r' || value == '\t' || value >= 0x20 {
				output.WriteByte(value)
			}
			index++
			continue
		}
		index++
		if index >= len(input) {
			break
		}
		switch input[index] {
		case '[':
			index++
			for index < len(input) {
				value := input[index]
				index++
				if value >= 0x40 && value <= 0x7e {
					break
				}
			}
		case ']', 'P', 'X', '^', '_':
			index++
			for index < len(input) {
				if input[index] == 0x07 {
					index++
					break
				}
				if input[index] == 0x1b && index+1 < len(input) &&
					input[index+1] == '\\' {
					index += 2
					break
				}
				index++
			}
		default:
			index++
		}
	}
	var safe strings.Builder
	safe.Grow(output.Len())
	for _, value := range output.String() {
		if value == '\n' || value == '\r' || value == '\t' {
			safe.WriteRune(value)
			continue
		}
		if unicode.IsControl(value) || unicode.In(value, unicode.Cf) {
			continue
		}
		safe.WriteRune(value)
	}
	return redact.String(safe.String())
}
