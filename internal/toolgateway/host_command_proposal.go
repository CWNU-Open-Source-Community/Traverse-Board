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
	RiskKinds           []string `json:"risk_kinds,omitempty"`
	NetworkTargets      []string `json:"network_targets,omitempty"`
	NetworkPurpose      string   `json:"network_purpose,omitempty"`
	CredentialKinds     []string `json:"credential_kinds,omitempty"`
	HostPaths           []string `json:"host_paths,omitempty"`
	PolicyCode          string   `json:"policy_code,omitempty"`
	PolicyReason        string   `json:"policy_reason,omitempty"`
	RequestedTool       string   `json:"requested_tool,omitempty"`
	OtherRiskReason     string   `json:"other_risk_reason,omitempty"`
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
	if spec.Version == runner.RiskEscalationProtocolVersion {
		scope, scopeErr := spec.riskEscalationScope()
		if scopeErr != nil {
			return HostCommandProposalSpec{}, nil, scopeErr
		}
		spec.RiskKinds = make([]string, len(scope.Kinds))
		for index, kind := range scope.Kinds {
			spec.RiskKinds[index] = string(kind)
		}
		spec.NetworkTargets = append([]string(nil), scope.NetworkTargets...)
		spec.NetworkPurpose = scope.NetworkPurpose
		spec.CredentialKinds = append([]string(nil), scope.CredentialKinds...)
		spec.HostPaths = append([]string(nil), scope.HostPaths...)
		spec.PolicyCode = scope.PolicyCode
		spec.PolicyReason = scope.PolicyReason
		spec.RequestedTool = scope.RequestedTool
		spec.OtherRiskReason = scope.OtherReason
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

func NormalizeHostCommandProposalPayload(payload json.RawMessage) (
	HostCommandProposalSpec, json.RawMessage, error,
) {
	return normalizeHostCommandProposalPayload(payload)
}

func marshalHostCommandProposalSpec(spec HostCommandProposalSpec) (
	json.RawMessage, error,
) {
	if spec.Version == runner.RiskEscalationProtocolVersion {
		type canonicalRiskEscalation struct {
			Version             string   `json:"version"`
			Transport           string   `json:"transport"`
			ExecutablePath      string   `json:"executable_path,omitempty"`
			Argv                []string `json:"argv,omitempty"`
			Shell               string   `json:"shell,omitempty"`
			Command             string   `json:"command,omitempty"`
			WorkingDirectory    string   `json:"working_directory"`
			TimeoutMilliseconds int64    `json:"timeout_milliseconds"`
			Purpose             string   `json:"purpose"`
			RiskKinds           []string `json:"risk_kinds"`
			NetworkTargets      []string `json:"network_targets,omitempty"`
			NetworkPurpose      string   `json:"network_purpose,omitempty"`
			CredentialKinds     []string `json:"credential_kinds,omitempty"`
			HostPaths           []string `json:"host_paths,omitempty"`
			PolicyCode          string   `json:"policy_code,omitempty"`
			PolicyReason        string   `json:"policy_reason,omitempty"`
			RequestedTool       string   `json:"requested_tool,omitempty"`
			OtherRiskReason     string   `json:"other_risk_reason,omitempty"`
		}
		return json.Marshal(canonicalRiskEscalation{
			Version: spec.Version, Transport: spec.Transport,
			ExecutablePath: spec.ExecutablePath, Argv: spec.Argv,
			Shell: spec.Shell, Command: spec.Command,
			WorkingDirectory:    spec.WorkingDirectory,
			TimeoutMilliseconds: spec.TimeoutMilliseconds, Purpose: spec.Purpose,
			RiskKinds: spec.RiskKinds, NetworkTargets: spec.NetworkTargets,
			NetworkPurpose: spec.NetworkPurpose, CredentialKinds: spec.CredentialKinds,
			HostPaths: spec.HostPaths, PolicyCode: spec.PolicyCode,
			PolicyReason: spec.PolicyReason, RequestedTool: spec.RequestedTool,
			OtherRiskReason: spec.OtherRiskReason,
		})
	}
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
	if (s.Version != runner.HostCommandProposalProtocolVersion &&
		s.Version != runner.RiskEscalationProtocolVersion) ||
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
	if s.Version == runner.HostCommandProposalProtocolVersion {
		if len(s.RiskKinds) != 0 || len(s.NetworkTargets) != 0 ||
			s.NetworkPurpose != "" || len(s.CredentialKinds) != 0 ||
			len(s.HostPaths) != 0 || s.PolicyCode != "" || s.PolicyReason != "" ||
			s.RequestedTool != "" || s.OtherRiskReason != "" {
			return errors.New("approval-mode host proposal cannot carry Workspace Access escalation fields")
		}
	} else if _, err := s.riskEscalationScope(); err != nil {
		return err
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

func (s HostCommandProposalSpec) riskEscalationScope() (
	runner.RiskEscalationScope, error,
) {
	kinds := make([]runner.RiskEscalationKind, len(s.RiskKinds))
	for index, value := range s.RiskKinds {
		kinds[index] = runner.RiskEscalationKind(strings.ToLower(strings.TrimSpace(value)))
	}
	scope, err := runner.NewRiskEscalationScope(runner.RiskEscalationScopeRequest{
		Kinds: kinds, NetworkTargets: s.NetworkTargets,
		NetworkPurpose: s.NetworkPurpose, CredentialKinds: s.CredentialKinds,
		HostPaths: s.HostPaths, PolicyCode: s.PolicyCode,
		PolicyReason: s.PolicyReason, RequestedTool: s.RequestedTool,
		OtherReason: s.OtherRiskReason,
	})
	if err != nil {
		return runner.RiskEscalationScope{}, errors.New("risk escalation scope is invalid or contains secret-like material")
	}
	return scope, nil
}

func (s HostCommandProposalSpec) RiskEscalationScope() (
	runner.RiskEscalationScope, error,
) {
	if s.Version != runner.RiskEscalationProtocolVersion {
		return runner.RiskEscalationScope{}, errors.New("host command payload is not a risk escalation")
	}
	return s.riskEscalationScope()
}

type HostCommandProposalContext struct {
	InvocationID         string
	OperationKey         string
	RunID                string
	RootAgentID          string
	SessionID            string
	WorkspaceID          string
	LeaseID              string
	LeaseGeneration      int64
	RequestedBy          string
	PolicyDecision       Decision
	SupervisorTurn       int
	SupervisorToolCallID string
	RootFingerprint      string
	CapabilityGeneration string
	ModeRevision         int64
	PermissionRevision   int64
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

func (c HostCommandProposalContext) ValidateRiskEscalation() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.SupervisorTurn <= 0 || strings.TrimSpace(c.SupervisorToolCallID) == "" ||
		len(strings.TrimSpace(c.RootFingerprint)) != 64 ||
		len(strings.TrimSpace(c.CapabilityGeneration)) != 64 ||
		c.ModeRevision <= 0 || c.PermissionRevision <= 0 {
		return errors.New("risk escalation requires an exact durable Supervisor call and Workspace generation")
	}
	return nil
}

type HostCommandProposalState string

const (
	HostCommandProposalRecorded  HostCommandProposalState = "recorded"
	HostCommandProposalWaiting   HostCommandProposalState = "waiting_approval"
	HostCommandProposalDenied    HostCommandProposalState = "denied"
	HostCommandProposalCompleted HostCommandProposalState = "completed"
	HostCommandProposalFailed    HostCommandProposalState = "failed"
)

type HostCommandProposalResult struct {
	ProposalID      string
	SpecFingerprint string
	Replayed        bool
	State           HostCommandProposalState
	ApprovalID      string
	GrantID         string
	Evidence        string
	ErrorCode       string
	Message         string
	Uncertain       bool
}

func (r HostCommandProposalResult) Validate() error {
	if strings.TrimSpace(r.ProposalID) == "" ||
		strings.TrimSpace(r.ProposalID) != r.ProposalID ||
		len([]rune(r.ProposalID)) > MaxToolIdentityRunes ||
		len(r.SpecFingerprint) != 64 {
		return errors.New("host command proposal result is invalid")
	}
	if r.State == "" {
		r.State = HostCommandProposalRecorded
	}
	switch r.State {
	case HostCommandProposalRecorded:
		if r.ApprovalID != "" || r.GrantID != "" || r.Evidence != "" ||
			r.ErrorCode != "" || r.Message != "" || r.Uncertain {
			return errors.New("legacy host command proposal result contains escalation state")
		}
	case HostCommandProposalWaiting:
		if strings.TrimSpace(r.ApprovalID) == "" || r.Evidence != "" ||
			r.ErrorCode != "" || r.Uncertain {
			return errors.New("waiting risk escalation result is invalid")
		}
	case HostCommandProposalDenied:
		if strings.TrimSpace(r.ApprovalID) == "" || strings.TrimSpace(r.Message) == "" ||
			r.Evidence != "" || r.Uncertain {
			return errors.New("denied risk escalation result is invalid")
		}
	case HostCommandProposalCompleted:
		if strings.TrimSpace(r.ApprovalID) == "" || r.Uncertain {
			return errors.New("completed risk escalation result is invalid")
		}
	case HostCommandProposalFailed:
		if strings.TrimSpace(r.ApprovalID) == "" || strings.TrimSpace(r.ErrorCode) == "" ||
			strings.TrimSpace(r.Message) == "" || (r.Uncertain && r.Evidence != "") {
			return errors.New("failed risk escalation result is invalid")
		}
	default:
		return errors.New("host command proposal result state is invalid")
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
	Description: "Record one exact, non-persistent host process for independent operator review. In Workspace Access use version=risk_escalation.v1 and declare each network target/purpose, credential kind, host path, policy denial, non-whitelisted tool, or other high-risk reason. The model cannot select a reviewer, confirmation, TTL, use count, grant, credential value, or capability bearer, and it cannot execute or persist the process.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","transport","working_directory","timeout_milliseconds","purpose"],"properties":{"version":{"enum":["host_command_proposal.v1","risk_escalation.v1"]},"transport":{"enum":["process","shell"]},"executable_path":{"type":"string","minLength":1,"maxLength":4096},"argv":{"type":"array","maxItems":64,"items":{"type":"string","maxLength":16384}},"shell":{"enum":["powershell","bash"]},"command":{"type":"string","minLength":1,"maxLength":16384},"working_directory":{"type":"string","minLength":1,"maxLength":4096},"timeout_milliseconds":{"type":"integer","minimum":1,"maximum":600000},"purpose":{"type":"string","minLength":1,"maxLength":1200},"risk_kinds":{"type":"array","minItems":1,"maxItems":6,"uniqueItems":true,"items":{"enum":["network","credential","host_path","policy_denial","non_whitelisted_tool","other_high_risk"]}},"network_targets":{"type":"array","minItems":1,"maxItems":16,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":512}},"network_purpose":{"type":"string","minLength":1,"maxLength":1200},"credential_kinds":{"type":"array","minItems":1,"maxItems":16,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":512}},"host_paths":{"type":"array","minItems":1,"maxItems":16,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":512}},"policy_code":{"type":"string","minLength":1,"maxLength":512},"policy_reason":{"type":"string","minLength":1,"maxLength":1200},"requested_tool":{"type":"string","minLength":1,"maxLength":512},"other_risk_reason":{"type":"string","minLength":1,"maxLength":1200}},"allOf":[{"oneOf":[{"properties":{"transport":{"const":"process"}},"required":["executable_path","argv"],"allOf":[{"not":{"required":["shell"]}},{"not":{"required":["command"]}}]},{"properties":{"transport":{"const":"shell"}},"required":["shell","command"],"allOf":[{"not":{"required":["executable_path"]}},{"not":{"required":["argv"]}}]}]},{"oneOf":[{"properties":{"version":{"const":"host_command_proposal.v1"}},"allOf":[{"not":{"required":["risk_kinds"]}},{"not":{"required":["network_targets"]}},{"not":{"required":["credential_kinds"]}},{"not":{"required":["host_paths"]}}]},{"properties":{"version":{"const":"risk_escalation.v1"}},"required":["risk_kinds"]}]}]}`),
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
		SupervisorTurn:       call.SupervisorTurn,
		SupervisorToolCallID: call.SupervisorToolCallID,
		RootFingerprint:      call.RootFingerprint,
		CapabilityGeneration: call.CapabilityGeneration,
		ModeRevision:         call.ModeRevision,
		PermissionRevision:   call.PermissionRevision,
	}
	if spec.Version == runner.RiskEscalationProtocolVersion {
		err = scope.ValidateRiskEscalation()
	} else {
		err = scope.Validate()
	}
	if err != nil {
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
	state := result.State
	if state == "" {
		state = HostCommandProposalRecorded
	}
	result.State = state
	resultStatus := StatusCompleted
	if state == HostCommandProposalWaiting {
		resultStatus = StatusProposed
	} else if state == HostCommandProposalDenied {
		resultStatus = StatusDenied
	} else if state == HostCommandProposalFailed {
		resultStatus = StatusFailed
	}
	allowed := decision.Allowed && state != HostCommandProposalDenied
	if !allowed {
		decision.Allowed = false
		decision.Approval = ApprovalNever
		decision.Reason = result.Message
	}
	metadata := map[string]string{
		"proposal_id":              result.ProposalID,
		"spec_fingerprint":         result.SpecFingerprint,
		"operator_review_required": strconv.FormatBool(state == HostCommandProposalWaiting),
		"execution_authorized":     "false",
		"capability_grant":         strconv.FormatBool(result.GrantID != ""),
		"replayed":                 strconv.FormatBool(result.Replayed),
		"proposal_state":           string(state),
	}
	if result.ApprovalID != "" {
		metadata["approval_id"] = result.ApprovalID
	}
	if result.GrantID != "" {
		metadata["grant_id"] = result.GrantID
	}
	if result.Uncertain {
		metadata["automatic_retry_allowed"] = "false"
		metadata["execution_result_uncertain"] = "true"
	}
	outcome := Outcome{Call: safeToolCall(call), Decision: decision}
	if state == HostCommandProposalWaiting {
		outcome.Proposal = &Proposal{
			ID: result.ProposalID, Tool: call.Name, Class: ClassAgentProposal,
			Status: StatusProposed, Preview: "Exact high-risk host action awaiting operator approval",
			CreatedAt: started, UpdatedAt: completed,
		}
	} else if state == HostCommandProposalDenied {
		outcome.Result = &Result{Status: StatusDenied, ExitCode: 0,
			MIME: "application/json", CompletedAt: completed,
			Stderr: result.Message, Metadata: metadata}
	} else {
		outcome.Execution = &Execution{Backend: "agent_proposal", Status: resultStatus,
			StartedAt: started, CompletedAt: &completed}
		outcome.Result = &Result{Status: resultStatus, ExitCode: 0,
			MIME: "application/json", CompletedAt: completed,
			Stdout: result.Evidence, Stderr: result.Message, Metadata: metadata}
	}
	return validateOutcome(outcome, nil)
}
