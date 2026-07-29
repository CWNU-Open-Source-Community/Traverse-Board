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

type ControlledCommandProposalSpec struct {
	Version      string                       `json:"version"`
	Kind         runner.ControlledCommandKind `json:"kind"`
	Purpose      string                       `json:"purpose"`
	RelativePath string                       `json:"relative_path"`
	TimeoutMS    int64                        `json:"timeout_millis"`
}

func normalizeControlledCommandProposalPayload(
	payload json.RawMessage,
) (ControlledCommandProposalSpec, json.RawMessage, error) {
	spec, err := decodeStructuredPayload[ControlledCommandProposalSpec](payload)
	if err != nil {
		return ControlledCommandProposalSpec{}, nil, err
	}
	spec.Version = strings.TrimSpace(spec.Version)
	spec.Purpose = strings.TrimSpace(redact.String(spec.Purpose))
	spec.RelativePath = strings.TrimSpace(spec.RelativePath)
	kind, err := runner.ParseControlledCommandKind(string(spec.Kind))
	if err != nil {
		return ControlledCommandProposalSpec{}, nil, err
	}
	spec.Kind = kind
	if err := spec.Validate(); err != nil {
		return ControlledCommandProposalSpec{}, nil, err
	}
	canonical, err := json.Marshal(spec)
	if err != nil {
		return ControlledCommandProposalSpec{}, nil, err
	}
	return spec, canonical, nil
}

func (s ControlledCommandProposalSpec) Validate() error {
	if s.Version != runner.ControlledCommandProposalProtocolVersion ||
		s.TimeoutMS < 1 ||
		s.TimeoutMS > runner.MaxControlledCommandTimeout.Milliseconds() ||
		!utf8.ValidString(s.Purpose) ||
		strings.TrimSpace(s.Purpose) != s.Purpose ||
		s.Purpose == "" || strings.ContainsRune(s.Purpose, 0) ||
		utf8.RuneCountInString(s.Purpose) >
			runner.MaxControlledCommandPurposeRunes {
		return errors.New("controlled command proposal payload is invalid")
	}
	if _, err := runner.ParseControlledCommandKind(string(s.Kind)); err != nil {
		return err
	}
	if s.Kind == runner.ControlledCommandPowerShellWorkspaceList {
		if s.RelativePath == "" {
			return errors.New("PowerShell Workspace list requires a relative path")
		}
	} else if s.RelativePath != "" {
		return errors.New("relative path is accepted only by PowerShell Workspace list")
	}
	if !utf8.ValidString(s.RelativePath) ||
		strings.ContainsRune(s.RelativePath, 0) ||
		utf8.RuneCountInString(s.RelativePath) >
			runner.MaxControlledRelativePathRunes {
		return errors.New("controlled command proposal relative path is invalid")
	}
	return nil
}

type ControlledCommandProposalContext struct {
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

func (c ControlledCommandProposalContext) Validate() error {
	for label, value := range map[string]string{
		"invocation id": c.InvocationID, "operation key": c.OperationKey,
		"run id": c.RunID, "root agent id": c.RootAgentID,
		"session id": c.SessionID, "workspace id": c.WorkspaceID,
		"lease id": c.LeaseID, "requester": c.RequestedBy,
	} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) != value ||
			len([]rune(value)) > MaxToolIdentityRunes {
			return fmt.Errorf("controlled command proposal %s is invalid", label)
		}
	}
	if c.InvocationID == "" || c.OperationKey == "" || c.RunID == "" ||
		c.RootAgentID == "" || c.SessionID == "" || c.WorkspaceID == "" ||
		c.LeaseID == "" || c.LeaseGeneration <= 0 ||
		c.RequestedBy != "run_supervisor" {
		return errors.New("controlled command proposal requires a fenced root Supervisor scope")
	}
	if err := c.PolicyDecision.Validate(); err != nil {
		return err
	}
	if !c.PolicyDecision.Allowed ||
		c.PolicyDecision.Approval != ApprovalAutomatic {
		return errors.New("controlled command proposal creation requires an automatic allowed decision")
	}
	return nil
}

type ControlledCommandProposalResult struct {
	ProposalID string
	Kind       runner.ControlledCommandKind
	Replayed   bool
}

func (r ControlledCommandProposalResult) Validate() error {
	if strings.TrimSpace(r.ProposalID) == "" ||
		strings.TrimSpace(r.ProposalID) != r.ProposalID ||
		len([]rune(r.ProposalID)) > MaxToolIdentityRunes {
		return errors.New("controlled command proposal result id is invalid")
	}
	_, err := runner.ParseControlledCommandKind(string(r.Kind))
	return err
}

type ControlledCommandProposalExecutor interface {
	ProposeControlledCommand(
		ctx context.Context,
		scope ControlledCommandProposalContext,
		spec ControlledCommandProposalSpec,
	) (ControlledCommandProposalResult, error)
}

var controlledCommandProposalDefinition = ToolDefinition{
	Name:  ControlledCommandProposeTool,
	Class: ClassAgentProposal, Approval: ApprovalAutomatic,
	Description: "Record a review-required request for one Go-owned fixed command template. This never executes a command and accepts no shell text, executable, argv, environment, network, or persistence setting.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","kind","purpose","relative_path","timeout_millis"],"properties":{"version":{"const":"controlled_command_proposal.v1"},"kind":{"enum":["git-status","git-diff-check","go-version","powershell-workspace-list"]},"purpose":{"type":"string","minLength":1,"maxLength":1200},"relative_path":{"type":"string","maxLength":512},"timeout_millis":{"type":"integer","minimum":1,"maximum":120000}}}`),
}

func (g *Gateway) WithControlledCommandProposalExecutor(
	executor ControlledCommandProposalExecutor,
) *Gateway {
	if g != nil {
		g.controlledCommandProposals = executor
	}
	return g
}

func (g *Gateway) invokeControlledCommandProposal(
	ctx context.Context,
	call ToolCall,
) (Outcome, error) {
	spec, canonical, err := normalizeControlledCommandProposalPayload(call.Payload)
	if err != nil {
		return Outcome{}, err
	}
	call.Payload = canonical
	policyDecision := g.checker.CheckToolCall(tools.Call{
		Name: string(call.Name), Args: map[string]string{
			"kind": string(spec.Kind), "purpose": spec.Purpose,
		},
	})
	if !policyDecision.Allowed {
		return deniedOutcome(call, policyDecision)
	}
	decision, err := gatewayDecision(policyDecision, ApprovalAutomatic, "low")
	if err != nil {
		return Outcome{}, err
	}
	scope := ControlledCommandProposalContext{
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
	result, err := g.controlledCommandProposals.ProposeControlledCommand(
		ctx, scope, spec)
	if err != nil {
		return Outcome{}, err
	}
	if err := result.Validate(); err != nil {
		return Outcome{}, err
	}
	completed := time.Now().UTC()
	outcome := Outcome{
		Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{
			Backend: "agent_proposal", Status: StatusCompleted,
			StartedAt: started, CompletedAt: &completed,
		},
		Result: &Result{
			Status: StatusCompleted, ExitCode: 0,
			MIME: "application/json", CompletedAt: completed,
			Metadata: map[string]string{
				"proposal_id":              result.ProposalID,
				"kind":                     string(result.Kind),
				"operator_review_required": "true",
				"execution_authorized":     "false",
				"capability_grant":         "false",
				"replayed":                 strconv.FormatBool(result.Replayed),
			},
		},
	}
	return validateOutcome(outcome, nil)
}
