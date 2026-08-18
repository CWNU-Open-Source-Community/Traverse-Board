package toolgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/tools"
)

type HostCommandProposalSpec struct {
	Version             string   `json:"version"`
	Transport           string   `json:"transport,omitempty"`
	ExecutablePath      string   `json:"executable_path,omitempty"`
	Argv                []string `json:"argv,omitempty"`
	Shell               string   `json:"shell,omitempty"`
	Command             string   `json:"command,omitempty"`
	WorkingDirectory    string   `json:"working_directory"`
	TimeoutMilliseconds int64    `json:"timeout_milliseconds"`
	Purpose             string   `json:"purpose"`
}

const (
	HostCommandTransportProcess = "process"
	HostCommandTransportShell   = "shell"
	HostCommandShellPowerShell  = "powershell"
	HostCommandShellBash        = "bash"
)

func normalizeHostCommandProposalPayload(payload json.RawMessage) (
	HostCommandProposalSpec, json.RawMessage, error,
) {
	spec, err := decodeStructuredPayload[HostCommandProposalSpec](payload)
	if err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	fields, err := structuredPayloadFields(payload)
	if err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	spec.Version = strings.TrimSpace(spec.Version)
	spec.Transport = strings.ToLower(strings.TrimSpace(spec.Transport))
	if spec.Transport == "" {
		// Keep durable pre-shell payloads replayable. New callers are directed
		// to send the transport explicitly by the Tool schema.
		if transportPresent, _ := structuredPayloadField(fields, "transport"); transportPresent {
			return HostCommandProposalSpec{}, nil,
				errors.New("host command proposal transport is invalid")
		}
		spec.Transport = HostCommandTransportProcess
	}
	spec.ExecutablePath = strings.TrimSpace(spec.ExecutablePath)
	spec.Shell = strings.ToLower(strings.TrimSpace(spec.Shell))
	spec.Command = strings.TrimSpace(spec.Command)
	spec.WorkingDirectory = strings.TrimSpace(spec.WorkingDirectory)
	spec.Purpose = strings.TrimSpace(redact.String(spec.Purpose))
	if spec.Argv != nil {
		spec.Argv = append([]string{}, spec.Argv...)
	}
	executablePresent, executableNonNull := structuredPayloadField(fields, "executable_path")
	argvPresent, argvNonNull := structuredPayloadField(fields, "argv")
	shellPresent, shellNonNull := structuredPayloadField(fields, "shell")
	commandPresent, commandNonNull := structuredPayloadField(fields, "command")
	switch spec.Transport {
	case HostCommandTransportProcess:
		if !executablePresent || !executableNonNull || !argvPresent || !argvNonNull ||
			spec.Argv == nil || shellPresent || commandPresent {
			return HostCommandProposalSpec{}, nil,
				errors.New("process host command proposal fields are invalid")
		}
	case HostCommandTransportShell:
		if executablePresent || argvPresent || !shellPresent || !shellNonNull ||
			!commandPresent || !commandNonNull {
			return HostCommandProposalSpec{}, nil,
				errors.New("shell host command proposal fields are invalid")
		}
	}
	if err := spec.Validate(); err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	canonical, err := marshalHostCommandProposalSpec(spec)
	if err != nil {
		return HostCommandProposalSpec{}, nil, err
	}
	return spec, canonical, nil
}

func marshalHostCommandProposalSpec(spec HostCommandProposalSpec) (
	json.RawMessage, error,
) {
	if spec.Transport == HostCommandTransportProcess {
		return json.Marshal(struct {
			Version             string   `json:"version"`
			Transport           string   `json:"transport"`
			ExecutablePath      string   `json:"executable_path"`
			Argv                []string `json:"argv"`
			WorkingDirectory    string   `json:"working_directory"`
			TimeoutMilliseconds int64    `json:"timeout_milliseconds"`
			Purpose             string   `json:"purpose"`
		}{spec.Version, spec.Transport, spec.ExecutablePath, spec.Argv,
			spec.WorkingDirectory, spec.TimeoutMilliseconds, spec.Purpose})
	}
	return json.Marshal(struct {
		Version             string `json:"version"`
		Transport           string `json:"transport"`
		Shell               string `json:"shell"`
		Command             string `json:"command"`
		WorkingDirectory    string `json:"working_directory"`
		TimeoutMilliseconds int64  `json:"timeout_milliseconds"`
		Purpose             string `json:"purpose"`
	}{spec.Version, spec.Transport, spec.Shell, spec.Command,
		spec.WorkingDirectory, spec.TimeoutMilliseconds, spec.Purpose})
}

func (s HostCommandProposalSpec) Validate() error {
	if s.Version != runner.HostCommandProposalProtocolVersion ||
		s.WorkingDirectory == "" ||
		!utf8.ValidString(s.WorkingDirectory) ||
		strings.ContainsRune(s.WorkingDirectory, 0) ||
		len([]rune(s.WorkingDirectory)) > MaxWorkspaceRootPathRunes ||
		s.TimeoutMilliseconds < 1 ||
		s.TimeoutMilliseconds > runner.MaxHostCommandTimeout.Milliseconds() ||
		!utf8.ValidString(s.Purpose) || s.Purpose == "" ||
		strings.TrimSpace(s.Purpose) != s.Purpose ||
		strings.ContainsRune(s.Purpose, 0) ||
		utf8.RuneCountInString(s.Purpose) > runner.MaxHostCommandPurposeRunes {
		return errors.New("host command proposal payload is invalid")
	}
	switch s.Transport {
	case HostCommandTransportProcess:
		if s.ExecutablePath == "" || !utf8.ValidString(s.ExecutablePath) ||
			strings.ContainsRune(s.ExecutablePath, 0) ||
			len([]rune(s.ExecutablePath)) > MaxWorkspaceRootPathRunes ||
			s.Shell != "" || s.Command != "" {
			return errors.New("process host command proposal transport is invalid")
		}
	case HostCommandTransportShell:
		if s.ExecutablePath != "" || len(s.Argv) != 0 ||
			(s.Shell != HostCommandShellPowerShell && s.Shell != HostCommandShellBash) ||
			s.Command == "" || !utf8.ValidString(s.Command) ||
			strings.ContainsRune(s.Command, 0) || strings.ContainsAny(s.Command, "\r\n") ||
			len([]byte(s.Command)) > runner.MaxHostCommandArgumentBytes ||
			redact.String(s.Command) != s.Command {
			return errors.New("shell host command proposal is invalid or contains secret-like material")
		}
	default:
		return errors.New("host command proposal transport is invalid")
	}
	if len(s.Argv) > runner.MaxHostCommandArguments {
		return errors.New("host command proposal argv exceeds its item limit")
	}
	total := 0
	for _, argument := range s.Argv {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) ||
			len([]byte(argument)) > runner.MaxHostCommandArgumentBytes ||
			redact.String(argument) != argument {
			return errors.New("host command proposal argv is invalid or contains secret-like material")
		}
		total += len([]byte(argument))
		if total > runner.MaxHostCommandArgumentsBytes {
			return errors.New("host command proposal argv exceeds its total limit")
		}
	}
	return nil
}

type HostCommandProposalContext struct {
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

func (c HostCommandProposalContext) Validate() error {
	for label, value := range map[string]string{
		"invocation id": c.InvocationID, "operation key": c.OperationKey,
		"run id": c.RunID, "root agent id": c.RootAgentID,
		"session id": c.SessionID, "workspace id": c.WorkspaceID,
		"lease id": c.LeaseID, "requester": c.RequestedBy,
	} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) != value ||
			len([]rune(value)) > MaxToolIdentityRunes {
			return fmt.Errorf("host command proposal %s is invalid", label)
		}
	}
	if c.InvocationID == "" || c.OperationKey == "" || c.RunID == "" ||
		c.RootAgentID == "" || c.SessionID == "" || c.WorkspaceID == "" ||
		c.LeaseID == "" || c.LeaseGeneration <= 0 ||
		c.RequestedBy != "run_supervisor" {
		return errors.New("host command proposal requires a fenced root Supervisor scope")
	}
	if err := c.PolicyDecision.Validate(); err != nil {
		return err
	}
	if !c.PolicyDecision.Allowed || c.PolicyDecision.Approval != ApprovalAutomatic {
		return errors.New("host command proposal creation requires an automatic allowed decision")
	}
	return nil
}

type HostCommandProposalResult struct {
	ProposalID      string
	SpecFingerprint string
	Replayed        bool
}

func (r HostCommandProposalResult) Validate() error {
	if strings.TrimSpace(r.ProposalID) == "" ||
		strings.TrimSpace(r.ProposalID) != r.ProposalID ||
		len([]rune(r.ProposalID)) > MaxToolIdentityRunes ||
		len(r.SpecFingerprint) != 64 {
		return errors.New("host command proposal result is invalid")
	}
	return nil
}

type HostCommandProposalExecutor interface {
	ProposeHostCommand(context.Context, HostCommandProposalContext,
		HostCommandProposalSpec) (HostCommandProposalResult, error)
}

var hostCommandProposalDefinition = ToolDefinition{
	Name: HostCommandProposeTool, Class: ClassAgentProposal,
	Approval:    ApprovalAutomatic,
	Description: "Record a review-required request for one exact, non-persistent host process. Use transport=process with an absolute executable and literal argv, or transport=shell with shell=powershell|bash and one bounded command line. The model cannot approve or execute it, provide environment values, persist a process, or request background execution.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","transport","working_directory","timeout_milliseconds","purpose"],"properties":{"version":{"const":"host_command_proposal.v1"},"transport":{"enum":["process","shell"]},"executable_path":{"type":"string","minLength":1,"maxLength":4096},"argv":{"type":"array","maxItems":64,"items":{"type":"string","maxLength":16384}},"shell":{"enum":["powershell","bash"]},"command":{"type":"string","minLength":1,"maxLength":16384},"working_directory":{"type":"string","minLength":1,"maxLength":4096},"timeout_milliseconds":{"type":"integer","minimum":1,"maximum":600000},"purpose":{"type":"string","minLength":1,"maxLength":1200}},"oneOf":[{"properties":{"transport":{"const":"process"}},"required":["executable_path","argv"],"allOf":[{"not":{"required":["shell"]}},{"not":{"required":["command"]}}]},{"properties":{"transport":{"const":"shell"}},"required":["shell","command"],"allOf":[{"not":{"required":["executable_path"]}},{"not":{"required":["argv"]}}]}]}`),
}

func (g *Gateway) WithHostCommandProposalExecutor(
	executor HostCommandProposalExecutor,
) *Gateway {
	if g != nil {
		g.hostCommandProposals = executor
	}
	return g
}

func (g *Gateway) invokeHostCommandProposal(ctx context.Context,
	call ToolCall,
) (Outcome, error) {
	spec, canonical, err := normalizeHostCommandProposalPayload(call.Payload)
	if err != nil {
		return Outcome{}, err
	}
	call.Payload = canonical
	policyPayload, err := json.Marshal(map[string]any{
		"transport": spec.Transport, "executable": spec.ExecutablePath,
		"arguments": spec.Argv, "shell": spec.Shell, "command": spec.Command,
	})
	if err != nil {
		return Outcome{}, err
	}
	policyDecision := g.checker.CheckToolCall(tools.Call{
		Name: string(call.Name), Args: map[string]string{
			"proposal": string(policyPayload), "purpose": spec.Purpose,
		},
	})
	if !policyDecision.Allowed {
		return deniedOutcome(call, policyDecision)
	}
	decision, err := gatewayDecision(policyDecision, ApprovalAutomatic, "high")
	if err != nil {
		return Outcome{}, err
	}
	scope := HostCommandProposalContext{
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
	result, err := g.hostCommandProposals.ProposeHostCommand(ctx, scope, spec)
	if err != nil {
		return Outcome{}, err
	}
	if err := result.Validate(); err != nil {
		return Outcome{}, err
	}
	completed := time.Now().UTC()
	outcome := Outcome{
		Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{Backend: "agent_proposal", Status: StatusCompleted,
			StartedAt: started, CompletedAt: &completed},
		Result: &Result{Status: StatusCompleted, ExitCode: 0,
			MIME: "application/json", CompletedAt: completed,
			Metadata: map[string]string{
				"proposal_id":              result.ProposalID,
				"spec_fingerprint":         result.SpecFingerprint,
				"operator_review_required": "true",
				"execution_authorized":     "false",
				"capability_grant":         "false",
				"replayed":                 strconv.FormatBool(result.Replayed),
			}},
	}
	return validateOutcome(outcome, nil)
}
