package toolgateway

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/outputsafe"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/tools"
)

const (
	CommandRuntimeToolProtocolVersion = "command-runtime.v2"
	CommandRuntimeActionRun           = "run"
	CommandRuntimeActionStart         = "start"
	CommandRuntimeActionList          = "list"
	CommandRuntimeActionRead          = "read"
	CommandRuntimeActionWait          = "wait"
	CommandRuntimeActionWriteStdin    = "write_stdin"
	CommandRuntimeActionCancel        = "cancel"
	CommandRuntimeActionKill          = "kill"
	CommandRuntimeFailFast            = "fail_fast"
	CommandRuntimeContinue            = "continue"
	MaxCommandRuntimeBatch            = 4
	MaxCommandRuntimeResultJobs       = 32
	MaxCommandRuntimeForegroundMillis = 25_000
	MaxCommandRuntimePageBytes        = 32 * 1024
)

type CommandRuntimeInput struct {
	Version          string                      `json:"version"`
	Action           string                      `json:"action"`
	Commands         []runner.CommandRuntimeSpec `json:"commands,omitempty"`
	FailurePolicy    string                      `json:"failure_policy,omitempty"`
	JobID            string                      `json:"job_id,omitempty"`
	Cursor           *uint64                     `json:"cursor,omitempty"`
	MaxBytes         *int                        `json:"max_bytes,omitempty"`
	WaitMilliseconds *int                        `json:"wait_milliseconds,omitempty"`
	Stdin            *string                     `json:"stdin,omitempty"`
	CloseStdin       *bool                       `json:"close_stdin,omitempty"`
}

func (i CommandRuntimeInput) Validate() error {
	if i.Version != CommandRuntimeToolProtocolVersion ||
		!validCommandRuntimeAction(i.Action) {
		return errors.New("command runtime payload version or action is invalid")
	}
	if i.JobID != "" && (!domain.ValidAgentID(i.JobID) ||
		strings.TrimSpace(i.JobID) != i.JobID) {
		return errors.New("command runtime job id is invalid")
	}
	if i.Stdin != nil && (len([]byte(*i.Stdin)) > runner.MaxCommandRuntimeStdinBytes ||
		outputsafe.Sanitize([]byte(*i.Stdin)) != *i.Stdin) {
		return errors.New("command runtime stdin is invalid or secret-like")
	}
	if i.MaxBytes != nil && (*i.MaxBytes < runner.MinCommandRuntimeOutputRead ||
		*i.MaxBytes > MaxCommandRuntimePageBytes) {
		return errors.New("command runtime output page is invalid")
	}
	if i.WaitMilliseconds != nil && (*i.WaitMilliseconds < 0 ||
		*i.WaitMilliseconds > int(runner.MaxCommandRuntimeWait.Milliseconds())) {
		return errors.New("command runtime wait is invalid")
	}
	for _, command := range i.Commands {
		if command.Version != runner.CommandRuntimeProtocolVersion ||
			!command.Profile.Valid() || command.Network != runner.CommandRuntimeNetworkDisabled ||
			command.Credentials != runner.CommandRuntimeCredentialsNone ||
			command.TimeoutMilliseconds < 1 ||
			command.TimeoutMilliseconds > runner.MaxCommandRuntimeTimeout.Milliseconds() {
			return errors.New("command runtime command contract is invalid")
		}
	}
	switch i.Action {
	case CommandRuntimeActionRun:
		if len(i.Commands) < 1 || len(i.Commands) > MaxCommandRuntimeBatch ||
			(i.FailurePolicy != CommandRuntimeFailFast &&
				i.FailurePolicy != CommandRuntimeContinue) ||
			i.MaxBytes == nil || i.JobID != "" || i.Cursor != nil ||
			i.WaitMilliseconds != nil || i.Stdin != nil || i.CloseStdin != nil {
			return errors.New("command runtime foreground batch fields are invalid")
		}
		total := int64(0)
		for _, command := range i.Commands {
			total += command.TimeoutMilliseconds
		}
		if total > MaxCommandRuntimeForegroundMillis {
			return errors.New("command runtime foreground timeout budget exceeds 25 seconds")
		}
	case CommandRuntimeActionStart:
		if len(i.Commands) != 1 || i.FailurePolicy != "" || i.JobID != "" ||
			i.Cursor != nil || i.MaxBytes != nil || i.WaitMilliseconds != nil ||
			i.Stdin != nil || i.CloseStdin != nil {
			return errors.New("command runtime background start fields are invalid")
		}
	case CommandRuntimeActionList:
		if len(i.Commands) != 0 || i.FailurePolicy != "" || i.JobID != "" ||
			i.Cursor != nil || i.MaxBytes != nil || i.WaitMilliseconds != nil ||
			i.Stdin != nil || i.CloseStdin != nil {
			return errors.New("command runtime list fields are invalid")
		}
	case CommandRuntimeActionRead, CommandRuntimeActionWait:
		if len(i.Commands) != 0 || i.FailurePolicy != "" || i.JobID == "" ||
			i.Cursor == nil || i.MaxBytes == nil || i.WaitMilliseconds == nil ||
			i.Stdin != nil || i.CloseStdin != nil ||
			(i.Action == CommandRuntimeActionRead && *i.WaitMilliseconds != 0) ||
			(i.Action == CommandRuntimeActionWait && *i.WaitMilliseconds == 0) {
			return errors.New("command runtime output read fields are invalid")
		}
	case CommandRuntimeActionWriteStdin:
		if len(i.Commands) != 0 || i.FailurePolicy != "" || i.JobID == "" ||
			i.Cursor != nil || i.MaxBytes != nil || i.WaitMilliseconds != nil ||
			i.Stdin == nil || i.CloseStdin == nil ||
			(*i.Stdin == "" && !*i.CloseStdin) {
			return errors.New("command runtime stdin fields are invalid")
		}
	case CommandRuntimeActionCancel:
		if len(i.Commands) != 0 || i.FailurePolicy != "" || i.JobID == "" ||
			i.Cursor != nil || i.MaxBytes != nil || i.WaitMilliseconds == nil ||
			i.Stdin != nil || i.CloseStdin != nil {
			return errors.New("command runtime cancel fields are invalid")
		}
	case CommandRuntimeActionKill:
		if len(i.Commands) != 0 || i.FailurePolicy != "" || i.JobID == "" ||
			i.Cursor != nil || i.MaxBytes != nil || i.WaitMilliseconds != nil ||
			i.Stdin != nil || i.CloseStdin != nil {
			return errors.New("command runtime kill fields are invalid")
		}
	}
	return nil
}

type CommandRuntimeContext struct {
	InvocationID         string
	OperationKey         string
	RunID                string
	RootAgentID          string
	SessionID            string
	WorkspaceID          string
	CapabilityGeneration string
	LeaseID              string
	LeaseGeneration      int64
	RequestedBy          string
	PolicyDecision       Decision
	Adapter              commandruntimeadapter.Identity
}

func (c CommandRuntimeContext) Validate() error {
	for _, value := range []string{c.InvocationID, c.OperationKey, c.RunID,
		c.RootAgentID, c.SessionID, c.WorkspaceID, c.LeaseID, c.RequestedBy} {
		if value == "" || strings.TrimSpace(value) != value ||
			!utf8.ValidString(value) || len([]rune(value)) > MaxToolIdentityRunes {
			return errors.New("command runtime requires normalized bounded identities")
		}
	}
	if !domain.ValidAgentID(c.RootAgentID) ||
		!validAgentCodeDigest(c.CapabilityGeneration, false) || c.LeaseGeneration <= 0 ||
		!c.Adapter.Executable() ||
		c.RequestedBy != "run_supervisor" || c.PolicyDecision.Validate() != nil ||
		!c.PolicyDecision.Allowed || c.PolicyDecision.Approval != ApprovalAutomatic {
		return errors.New("command runtime requires an automatically authorized fenced root scope")
	}
	return nil
}

type CommandRuntimeArtifactOutput struct {
	JobID  string
	Stdout string
	Stderr string
}

type CommandRuntimeExecutionResult struct {
	Backend           string
	Adapter           commandruntimeadapter.Identity
	Action            string
	Jobs              []runner.CommandRuntimeJobSnapshot
	Pages             []runner.CommandRuntimeOutputPage
	Artifacts         []CommandRuntimeArtifactOutput
	Replayed          bool
	IncompleteReasons []string
}

func (r CommandRuntimeExecutionResult) Validate() error {
	if !r.Adapter.Executable() || r.Backend != r.Adapter.Backend {
		return errors.New("command runtime adapter receipt is invalid")
	}
	if strings.TrimSpace(r.Backend) == "" || strings.TrimSpace(r.Backend) != r.Backend ||
		len([]rune(r.Backend)) > MaxExecutionBackendRunes ||
		!validCommandRuntimeAction(r.Action) || len(r.Jobs) > MaxCommandRuntimeResultJobs ||
		len(r.Pages) > MaxCommandRuntimeBatch || len(r.Artifacts) > MaxCommandRuntimeBatch ||
		len(r.IncompleteReasons) > 16 {
		return errors.New("command runtime execution result is invalid")
	}
	for _, job := range r.Jobs {
		if !domain.ValidAgentID(job.ID) || !job.State.Valid() ||
			job.OutputBaseCursor > job.OutputCursor || job.Adapter.Validate() != nil {
			return errors.New("command runtime job result is invalid")
		}
	}
	for _, page := range r.Pages {
		if !domain.ValidAgentID(page.JobID) || page.BaseCursor > page.NextCursor ||
			page.NextCursor > page.EndCursor || !page.State.Valid() {
			return errors.New("command runtime output page is invalid")
		}
		for _, frame := range page.Frames {
			if frame.Cursor > frame.NextCursor || frame.NextCursor > page.EndCursor ||
				(frame.Stream != runner.CommandRuntimeStdout &&
					frame.Stream != runner.CommandRuntimeStderr) ||
				frame.Timestamp.IsZero() || !utf8.ValidString(frame.Text) {
				return errors.New("command runtime output frame is invalid")
			}
		}
	}
	for _, output := range r.Artifacts {
		if !domain.ValidAgentID(output.JobID) ||
			len([]byte(output.Stdout)) > artifact.MaxContentBytes ||
			len([]byte(output.Stderr)) > artifact.MaxContentBytes ||
			!utf8.ValidString(output.Stdout) || !utf8.ValidString(output.Stderr) {
			return errors.New("command runtime artifact output is invalid")
		}
	}
	for _, reason := range r.IncompleteReasons {
		if reason == "" || reason != strings.TrimSpace(reason) || !utf8.ValidString(reason) ||
			len([]rune(reason)) > 512 || strings.ContainsRune(reason, 0) {
			return errors.New("command runtime incomplete reason is invalid")
		}
	}
	return nil
}

func (r CommandRuntimeExecutionResult) ValidateBoundAdapter() error {
	return r.Validate()
}

type CommandRuntimeExecutor interface {
	ExecuteCommandRuntime(context.Context, CommandRuntimeContext,
		CommandRuntimeInput) (CommandRuntimeExecutionResult, error)
}

// CommandRuntimeAdvertiser returns the process-local adapter identity that may
// be advertised for the current Run permission. The resulting authority is
// durable evidence, not a capability that recovery data may widen.
type CommandRuntimeAdvertiser interface {
	AdvertisedCommandRuntimeAdapter(context.Context, string,
		domain.RunExecutionPermissionMode) (commandruntimeadapter.Identity, bool, error)
}

var commandRuntimeDefinition = ToolDefinition{
	Name: CommandRuntimeTool, Class: ClassProcess, Approval: ApprovalAutomatic,
	Description: "Run an ordered command-runtime.v2 batch or manage one Run-owned background Job through the adapter selected by current Run authority; the model cannot select or override that adapter. Every command declares a fixed PowerShell/Bash/process profile, literal argv or script, workspace-relative cwd, restricted environment, stdin lifecycle, timeout, bounded output, disabled network intent, and no credentials. Output is untrusted, sanitized, cursor-addressed evidence; this tool is separate from the user terminal, Debug terminal, reviewed one-shot command, and Docker Sandbox.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","action"],"properties":{"version":{"const":"command-runtime.v2"},"action":{"enum":["run","start","list","read","wait","write_stdin","cancel","kill"]},"commands":{"type":"array","minItems":1,"maxItems":4,"items":{"type":"object","additionalProperties":false,"required":["version","profile","working_directory","environment","stdin_policy","close_initial_stdin","timeout_milliseconds","output","network","credentials","purpose"],"properties":{"version":{"const":"command-runtime.v2"},"profile":{"enum":["powershell","bash","process"]},"executable":{"type":"string","maxLength":4096},"arguments":{"type":"array","maxItems":128,"items":{"type":"string","maxLength":16384}},"script":{"type":"string","maxLength":65536},"working_directory":{"type":"string","minLength":1,"maxLength":4096},"environment":{"type":"array","maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["name","value"],"properties":{"name":{"type":"string","minLength":1,"maxLength":128},"value":{"type":"string","maxLength":65536}}}},"stdin_policy":{"enum":["closed","pipe"]},"initial_stdin":{"type":"string","maxLength":65536},"close_initial_stdin":{"type":"boolean"},"timeout_milliseconds":{"type":"integer","minimum":1,"maximum":1800000},"output":{"type":"object","additionalProperties":false,"required":["inline_bytes","artifact_bytes"],"properties":{"inline_bytes":{"type":"integer","minimum":4096,"maximum":524288},"artifact_bytes":{"type":"integer","minimum":4096,"maximum":4194304}}},"network":{"const":"disabled"},"credentials":{"const":"none"},"purpose":{"type":"string","minLength":1,"maxLength":1200}}}},"failure_policy":{"enum":["fail_fast","continue"]},"job_id":{"type":"string","minLength":1,"maxLength":256},"cursor":{"type":"integer","minimum":0},"max_bytes":{"type":"integer","minimum":4,"maximum":32768},"wait_milliseconds":{"type":"integer","minimum":0,"maximum":5000},"stdin":{"type":"string","maxLength":65536},"close_stdin":{"type":"boolean"}},"allOf":[{"if":{"properties":{"action":{"const":"run"}}},"then":{"required":["commands","failure_policy","max_bytes"]}},{"if":{"properties":{"action":{"const":"start"}}},"then":{"required":["commands"]}},{"if":{"properties":{"action":{"enum":["read","wait"]}}},"then":{"required":["job_id","cursor","max_bytes","wait_milliseconds"]}},{"if":{"properties":{"action":{"const":"write_stdin"}}},"then":{"required":["job_id","stdin","close_stdin"]}},{"if":{"properties":{"action":{"const":"cancel"}}},"then":{"required":["job_id","wait_milliseconds"]}},{"if":{"properties":{"action":{"const":"kill"}}},"then":{"required":["job_id"]}}]}`),
}

func validCommandRuntimeAction(value string) bool {
	switch value {
	case CommandRuntimeActionRun, CommandRuntimeActionStart, CommandRuntimeActionList,
		CommandRuntimeActionRead, CommandRuntimeActionWait,
		CommandRuntimeActionWriteStdin, CommandRuntimeActionCancel,
		CommandRuntimeActionKill:
		return true
	default:
		return false
	}
}

func normalizeCommandRuntimePayload(payload json.RawMessage) (
	CommandRuntimeInput, json.RawMessage, error,
) {
	input, err := decodeStructuredPayload[CommandRuntimeInput](payload)
	if err != nil {
		return CommandRuntimeInput{}, nil, err
	}
	fields, err := structuredPayloadFields(payload)
	if err != nil {
		return CommandRuntimeInput{}, nil, err
	}
	input.Version = strings.TrimSpace(input.Version)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.FailurePolicy = strings.ToLower(strings.TrimSpace(input.FailurePolicy))
	input.JobID = strings.TrimSpace(input.JobID)
	for index := range input.Commands {
		normalized, normalizeErr := runner.NormalizeCommandRuntimeIntent(input.Commands[index])
		if normalizeErr != nil {
			return CommandRuntimeInput{}, nil, normalizeErr
		}
		input.Commands[index] = normalized
	}
	if err := validateCommandRuntimePayloadShape(fields, input); err != nil {
		return CommandRuntimeInput{}, nil, err
	}
	if err := input.Validate(); err != nil {
		return CommandRuntimeInput{}, nil, err
	}
	canonical, err := json.Marshal(input)
	return input, canonical, err
}

func validateCommandRuntimePayloadShape(fields map[string]json.RawMessage,
	input CommandRuntimeInput,
) error {
	require := func(names ...string) bool {
		for _, name := range names {
			present, nonNull := structuredPayloadField(fields, name)
			if !present || !nonNull {
				return false
			}
		}
		return true
	}
	allowed := map[string]struct{}{"version": {}, "action": {}}
	allow := func(names ...string) {
		for _, name := range names {
			allowed[name] = struct{}{}
		}
	}
	if !require("version", "action") {
		return errors.New("command runtime version and action fields are required")
	}
	switch input.Action {
	case CommandRuntimeActionRun:
		allow("commands", "failure_policy", "max_bytes")
		if !require("commands", "failure_policy", "max_bytes") {
			return errors.New("command runtime foreground fields are required")
		}
	case CommandRuntimeActionStart:
		allow("commands")
		if !require("commands") {
			return errors.New("command runtime start fields are required")
		}
	case CommandRuntimeActionList:
	case CommandRuntimeActionRead, CommandRuntimeActionWait:
		allow("job_id", "cursor", "max_bytes", "wait_milliseconds")
		if !require("job_id", "cursor", "max_bytes", "wait_milliseconds") {
			return errors.New("command runtime output fields are required")
		}
	case CommandRuntimeActionWriteStdin:
		allow("job_id", "stdin", "close_stdin")
		if !require("job_id", "stdin", "close_stdin") {
			return errors.New("command runtime stdin fields are required")
		}
	case CommandRuntimeActionCancel:
		allow("job_id", "wait_milliseconds")
		if !require("job_id", "wait_milliseconds") {
			return errors.New("command runtime cancel fields are required")
		}
	case CommandRuntimeActionKill:
		allow("job_id")
		if !require("job_id") {
			return errors.New("command runtime kill fields are required")
		}
	default:
		return errors.New("command runtime action is invalid")
	}
	for name := range fields {
		if _, found := allowed[name]; !found {
			return errors.New("command runtime action contains an inapplicable field")
		}
	}
	if input.Action != CommandRuntimeActionRun && input.Action != CommandRuntimeActionStart {
		return nil
	}
	var commands []map[string]json.RawMessage
	if err := json.Unmarshal(fields["commands"], &commands); err != nil || commands == nil ||
		len(commands) != len(input.Commands) {
		return errors.New("command runtime commands must be an explicit JSON array")
	}
	for index, command := range commands {
		if err := validateCommandRuntimeCommandShape(command, input.Commands[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateCommandRuntimeCommandShape(fields map[string]json.RawMessage,
	command runner.CommandRuntimeSpec,
) error {
	for _, name := range []string{"version", "profile", "working_directory", "environment",
		"stdin_policy", "close_initial_stdin", "timeout_milliseconds", "output",
		"network", "credentials", "purpose"} {
		present, nonNull := structuredPayloadField(fields, name)
		if !present || !nonNull {
			return errors.New("command runtime command omitted an explicit boundary field")
		}
	}
	if present, nonNull := structuredPayloadField(fields, "initial_stdin"); present && !nonNull {
		return errors.New("command runtime initial stdin cannot be null")
	}
	switch command.Profile {
	case runner.CommandRuntimePowerShell, runner.CommandRuntimeBash:
		scriptPresent, scriptNonNull := structuredPayloadField(fields, "script")
		executablePresent, _ := structuredPayloadField(fields, "executable")
		argumentsPresent, _ := structuredPayloadField(fields, "arguments")
		if !scriptPresent || !scriptNonNull || executablePresent || argumentsPresent {
			return errors.New("command runtime shell command shape is invalid")
		}
	case runner.CommandRuntimeProcess:
		executablePresent, executableNonNull := structuredPayloadField(fields, "executable")
		argumentsPresent, argumentsNonNull := structuredPayloadField(fields, "arguments")
		scriptPresent, _ := structuredPayloadField(fields, "script")
		if !executablePresent || !executableNonNull || !argumentsPresent ||
			!argumentsNonNull || scriptPresent {
			return errors.New("command runtime process command shape is invalid")
		}
	}
	var environment []runner.CommandRuntimeEnvironment
	if err := json.Unmarshal(fields["environment"], &environment); err != nil || environment == nil {
		return errors.New("command runtime environment must be an explicit JSON array")
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(fields["output"], &output); err != nil || len(output) != 2 {
		return errors.New("command runtime output policy is invalid")
	}
	for _, name := range []string{"inline_bytes", "artifact_bytes"} {
		present, nonNull := structuredPayloadField(output, name)
		if !present || !nonNull {
			return errors.New("command runtime output policy omitted a bound")
		}
	}
	return nil
}

func (g *Gateway) WithCommandRuntimeExecutor(executor CommandRuntimeExecutor) *Gateway {
	if g != nil {
		g.commandRuntime = executor
	}
	return g
}

func (g *Gateway) invokeCommandRuntime(ctx context.Context, call ToolCall) (
	Outcome, error,
) {
	input, canonical, err := normalizeCommandRuntimePayload(call.Payload)
	if err != nil {
		return Outcome{}, err
	}
	call.Payload = canonical
	policyDecision := policy.Decision{Allowed: true,
		Reason: "bounded command runtime lifecycle operation allowed", Risk: "low"}
	if input.Action == CommandRuntimeActionRun || input.Action == CommandRuntimeActionStart {
		allAllowed := true
		for index, command := range input.Commands {
			commandDecision := policy.Decision{}
			if networkReason := commandRuntimeNetworkViolation(command); networkReason != "" {
				commandDecision = policy.Decision{Allowed: false, Risk: "high",
					Reason: networkReason}
			} else {
				encoded, _ := json.Marshal(command)
				policyTool := ShellTool
				argumentName := "command"
				if command.Profile == runner.CommandRuntimeProcess {
					policyTool = ScriptProcessTool
					argumentName = "proposal"
				}
				commandDecision = g.checker.CheckToolCall(tools.Call{
					Name: string(policyTool), Args: map[string]string{argumentName: string(encoded)}})
				if commandDecision.NeedsApproval {
					commandDecision.Allowed = false
					commandDecision.Reason = "command runtime cannot bypass a required per-command review: " + commandDecision.Reason
				}
			}
			if err := g.recordCommandRuntimePolicyDecision(ctx, call, index,
				commandDecision); err != nil {
				return Outcome{}, err
			}
			if !commandDecision.Allowed && allAllowed {
				policyDecision = commandDecision
			}
			allAllowed = allAllowed && commandDecision.Allowed
		}
		if allAllowed {
			policyDecision = policy.Decision{Allowed: true, Risk: "high",
				Reason: "every command passed exact canonical policy review"}
		}
	}
	if !policyDecision.Allowed {
		return deniedOutcome(call, policyDecision)
	}
	decision, err := gatewayDecision(policyDecision, ApprovalAutomatic, "high")
	if err != nil {
		return Outcome{}, err
	}
	scope := CommandRuntimeContext{InvocationID: call.InvocationID,
		OperationKey: call.OperationKey, RunID: call.RunID, RootAgentID: call.AgentID,
		SessionID: call.SessionID, WorkspaceID: call.WorkspaceID,
		CapabilityGeneration: call.CapabilityGeneration,
		LeaseID:              call.LeaseID, LeaseGeneration: call.LeaseGeneration,
		RequestedBy: call.RequestedBy, PolicyDecision: decision,
		Adapter: call.CommandRuntimeAdapter}
	if err := scope.Validate(); err != nil {
		return Outcome{}, err
	}
	started := time.Now().UTC()
	result, err := g.commandRuntime.ExecuteCommandRuntime(ctx, scope, input)
	if err != nil {
		return Outcome{}, err
	}
	sanitizeCommandRuntimeExecutionResult(&result)
	if err := result.ValidateBoundAdapter(); err != nil ||
		!result.Adapter.SameBackend(scope.Adapter) {
		if err == nil {
			err = errors.New("command runtime adapter receipt does not match advertised authority")
		}
		return Outcome{}, err
	}
	completed := time.Now().UTC()
	projection := struct {
		Version           string                             `json:"version"`
		Action            string                             `json:"action"`
		Adapter           commandruntimeadapter.Identity     `json:"adapter"`
		Jobs              []runner.CommandRuntimeJobSnapshot `json:"jobs"`
		Pages             []runner.CommandRuntimeOutputPage  `json:"pages,omitempty"`
		IncompleteReasons []string                           `json:"incomplete_reasons,omitempty"`
	}{Version: runner.CommandRuntimeResultVersion, Action: result.Action,
		Adapter: result.Adapter, Jobs: result.Jobs, Pages: result.Pages,
		IncompleteReasons: result.IncompleteReasons}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return Outcome{}, err
	}
	stdout, truncated := boundResultText(outputsafe.Sanitize(encoded),
		MaxResultStdoutBytes)
	metadata := map[string]string{
		"backend": result.Backend, "action": result.Action,
		"adapter_kind":       string(result.Adapter.Kind),
		"backend_identity":   result.Adapter.BackendIdentity,
		"backend_generation": result.Adapter.Generation,
		"isolation_grade":    string(result.Adapter.IsolationGrade),
		"job_count":          strconv.Itoa(len(result.Jobs)),
		"replayed":           strconv.FormatBool(result.Replayed),
		"owner":              string(result.Adapter.Kind), "output_source": "untrusted_command_runtime",
		"network":     string(result.Adapter.NetworkPolicy),
		"credentials": string(result.Adapter.CredentialPolicy), "profile_files": "disabled",
		"user_terminal_shared": "false", "debug_terminal_shared": "false",
		"sandbox_shared": strconv.FormatBool(
			result.Adapter.Kind == commandruntimeadapter.KindSandboxedWorkspace),
		"instruction_authorized": "false",
	}
	if len(result.IncompleteReasons) > 0 {
		metadata["incomplete_reason_count"] = strconv.Itoa(len(result.IncompleteReasons))
	}
	for index, output := range result.Artifacts {
		captured, captureErr := g.captureTerminalArtifacts(ctx, call, output.JobID,
			output.Stdout, output.Stderr, "text/plain; charset=utf-8")
		if captureErr != nil {
			return Outcome{}, captureErr
		}
		prefix := "job_" + strconv.Itoa(index+1) + "_"
		metadata[prefix+"id"] = output.JobID
		for key, value := range captured {
			if strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_sha256") {
				metadata[prefix+key] = value
			}
		}
	}
	outcome := Outcome{Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{Backend: result.Backend, Status: StatusCompleted,
			StartedAt: started, CompletedAt: &completed},
		Result: &Result{Status: StatusCompleted, Stdout: stdout, ExitCode: 0,
			MIME: "application/json", Truncated: truncated,
			Metadata: metadata, CompletedAt: completed}}
	return validateOutcome(outcome, nil)
}

func sanitizeCommandRuntimeExecutionResult(result *CommandRuntimeExecutionResult) {
	if result == nil {
		return
	}
	for pageIndex := range result.Pages {
		for frameIndex := range result.Pages[pageIndex].Frames {
			frame := &result.Pages[pageIndex].Frames[frameIndex]
			frame.Text = outputsafe.Sanitize([]byte(frame.Text))
		}
	}
	for index := range result.Artifacts {
		result.Artifacts[index].Stdout = outputsafe.Sanitize(
			[]byte(result.Artifacts[index].Stdout))
		result.Artifacts[index].Stderr = outputsafe.Sanitize(
			[]byte(result.Artifacts[index].Stderr))
	}
}

func commandRuntimeNetworkViolation(spec runner.CommandRuntimeSpec) string {
	value := strings.ToLower(spec.Script + " " + spec.Executable + " " +
		strings.Join(spec.Arguments, " "))
	base := strings.ToLower(filepath.Base(spec.Executable))
	for _, executable := range []string{"curl", "curl.exe", "wget", "wget.exe",
		"ssh", "ssh.exe", "scp", "scp.exe", "sftp", "ftp", "nc", "netcat",
		"nmap", "telnet", "ping", "ping.exe"} {
		if base == executable {
			return "command runtime network declaration is disabled for this executable"
		}
	}
	for _, marker := range []string{"http://", "https://", "invoke-webrequest",
		"invoke-restmethod", "test-netconnection", "start-bitstransfer", "git clone",
		"git fetch", "git pull", "git push", "git ls-remote"} {
		if strings.Contains(value, marker) {
			return "command runtime detected network intent while network is declared disabled"
		}
	}
	return ""
}

func (g *Gateway) recordCommandRuntimePolicyDecision(ctx context.Context,
	call ToolCall, commandIndex int, decision policy.Decision,
) error {
	if g == nil || g.policyRecorder == nil {
		return errors.New("command runtime policy decision recorder is required")
	}
	return g.policyRecorder.RecordPolicyDecision(ctx, policy.DecisionRecord{
		SessionID: call.SessionID, SubjectID: call.InvocationID,
		Context:  "tool_run.command_runtime.command_" + strconv.Itoa(commandIndex+1),
		Decision: decision,
	})
}

func commandRuntimePointer[T any](value T) *T { return &value }
