package toolgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/tools"
)

// OneShotCommandProposalSpec is the structured agent request. Executable,
// argv, cwd, and environment are all literals; no shell string exists in the
// protocol. Full Workspace containment is enforced at propose time by the
// application executor with the real Workspace root.
type OneShotCommandProposalSpec struct {
	Version          string   `json:"version"`
	ExecutablePath   string   `json:"executable_path"`
	Argv             []string `json:"argv"`
	WorkingDirectory string   `json:"working_directory"`
	Environment      []string `json:"environment"`
	TimeoutMS        int64    `json:"timeout_millis"`
	Purpose          string   `json:"purpose"`
}

func normalizeOneShotCommandProposalPayload(payload json.RawMessage) (OneShotCommandProposalSpec, json.RawMessage, error) {
	spec, err := decodeStructuredPayload[OneShotCommandProposalSpec](payload)
	if err != nil {
		return OneShotCommandProposalSpec{}, nil, err
	}
	spec.Version = strings.TrimSpace(spec.Version)
	spec.ExecutablePath = strings.TrimSpace(spec.ExecutablePath)
	spec.WorkingDirectory = strings.TrimSpace(spec.WorkingDirectory)
	spec.Purpose = strings.TrimSpace(redact.String(spec.Purpose))
	if err := spec.Validate(); err != nil {
		return OneShotCommandProposalSpec{}, nil, err
	}
	canonical, err := json.Marshal(spec)
	if err != nil {
		return OneShotCommandProposalSpec{}, nil, err
	}
	return spec, canonical, nil
}

// Validate applies the field-level boundary. Workspace containment and
// executable existence checks happen in the application executor, which owns
// the trusted Workspace root.
func (s OneShotCommandProposalSpec) Validate() error {
	if s.Version != runner.OnceCommandProtocolVersion {
		return errors.New("one-shot command proposal version is invalid")
	}
	if !filepath.IsAbs(s.ExecutablePath) || strings.ContainsRune(s.ExecutablePath, 0) {
		return errors.New("one-shot command proposal requires an absolute executable path")
	}
	if len(s.Argv) == 0 || len(s.Argv) > runner.MaxOnceCommandArguments {
		return errors.New("one-shot command proposal argv bounds are invalid")
	}
	total := 0
	for _, argument := range s.Argv {
		if !utf8.ValidString(argument) || strings.ContainsRune(argument, 0) ||
			len(argument) > runner.MaxOnceCommandArgumentBytes {
			return errors.New("one-shot command proposal argv entry is invalid")
		}
		total += len(argument)
	}
	if total > runner.MaxOnceCommandArgumentsBytes {
		return errors.New("one-shot command proposal argv exceeds the byte bound")
	}
	if s.WorkingDirectory == "" || strings.ContainsRune(s.WorkingDirectory, 0) ||
		strings.Contains(s.WorkingDirectory, "..") {
		return errors.New("one-shot command proposal working directory is invalid")
	}
	if len(s.Environment) > runner.MaxOnceCommandEnvironment {
		return errors.New("one-shot command proposal environment exceeds the bound")
	}
	seen := map[string]bool{}
	for _, entry := range s.Environment {
		key, _, found := strings.Cut(entry, "=")
		if !found || !runner.OnceEnvironmentAllowlist[key] || seen[key] ||
			len(entry) > runner.MaxOnceCommandEnvironmentBytes || strings.ContainsRune(entry, 0) {
			return errors.New("one-shot command proposal environment entry is invalid")
		}
		seen[key] = true
	}
	if s.TimeoutMS < 1 || s.TimeoutMS > runner.MaxOnceCommandTimeout.Milliseconds() {
		return errors.New("one-shot command proposal timeout is invalid")
	}
	if s.Purpose == "" || utf8.RuneCountInString(s.Purpose) > runner.MaxOnceCommandPurposeRunes ||
		!utf8.ValidString(s.Purpose) {
		return errors.New("one-shot command proposal purpose is invalid")
	}
	return nil
}

// ToRunnerSpec converts the proposal payload into the runner spec used for
// full validation and execution.
func (s OneShotCommandProposalSpec) ToRunnerSpec() runner.OnceCommandSpec {
	return runner.OnceCommandSpec{
		ProtocolVersion:     runner.OnceCommandProtocolVersion,
		ExecutablePath:      s.ExecutablePath,
		Argv:                append([]string(nil), s.Argv...),
		WorkingDirectory:    s.WorkingDirectory,
		Environment:         append([]string(nil), s.Environment...),
		TimeoutMilliseconds: s.TimeoutMS,
		Purpose:             s.Purpose,
	}
}

type OneShotCommandProposalResult struct {
	ProposalID string
	Replayed   bool
}

func (r OneShotCommandProposalResult) Validate() error {
	if strings.TrimSpace(r.ProposalID) == "" || strings.TrimSpace(r.ProposalID) != r.ProposalID ||
		len([]rune(r.ProposalID)) > MaxToolIdentityRunes {
		return errors.New("one-shot command proposal result id is invalid")
	}
	return nil
}

type OneShotCommandProposalContext struct {
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

func (c OneShotCommandProposalContext) Validate() error {
	for label, value := range map[string]string{
		"invocation id": c.InvocationID, "operation key": c.OperationKey,
		"run id": c.RunID, "root agent id": c.RootAgentID,
		"session id": c.SessionID, "workspace id": c.WorkspaceID,
		"lease id": c.LeaseID, "requester": c.RequestedBy,
	} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) != value ||
			len([]rune(value)) > MaxToolIdentityRunes {
			return fmt.Errorf("one-shot command proposal %s is invalid", label)
		}
	}
	if c.InvocationID == "" || c.OperationKey == "" || c.RunID == "" ||
		c.RootAgentID == "" || c.SessionID == "" || c.WorkspaceID == "" ||
		c.LeaseID == "" || c.LeaseGeneration <= 0 || c.RequestedBy != "run_supervisor" {
		return errors.New("one-shot command proposal requires a fenced root Supervisor scope")
	}
	if err := c.PolicyDecision.Validate(); err != nil {
		return err
	}
	if !c.PolicyDecision.Allowed || c.PolicyDecision.Approval != ApprovalAutomatic {
		return errors.New("one-shot command proposal requires an automatic allowed decision")
	}
	return nil
}

type OneShotCommandProposalExecutor interface {
	ProposeOneShotCommand(ctx context.Context, scope OneShotCommandProposalContext,
		spec OneShotCommandProposalSpec) (OneShotCommandProposalResult, error)
}

var oneShotCommandProposalDefinition = ToolDefinition{
	Name: OneShotCommandProposeTool, Class: ClassAgentProposal, Approval: ApprovalAutomatic,
	Description: "Record a review-required request for one workspace-scoped one-shot command. This never executes anything and accepts no shell text; executable, argv, cwd, and environment are structured literals.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","executable_path","argv","working_directory","environment","timeout_millis","purpose"],"properties":{"version":{"const":"once_command.v1"},"executable_path":{"type":"string","minLength":1,"maxLength":1024},"argv":{"type":"array","items":{"type":"string","maxLength":16384},"minItems":1,"maxItems":64},"working_directory":{"type":"string","minLength":1,"maxLength":1024},"environment":{"type":"array","items":{"type":"string","maxLength":16384},"maxItems":16},"timeout_millis":{"type":"integer","minimum":1,"maximum":600000},"purpose":{"type":"string","minLength":1,"maxLength":1200}}}`),
}

func (g *Gateway) WithOneShotCommandProposalExecutor(executor OneShotCommandProposalExecutor) *Gateway {
	if g != nil {
		g.oneShotCommandProposals = executor
	}
	return g
}

func (g *Gateway) invokeOneShotCommandProposal(ctx context.Context, call ToolCall) (Outcome, error) {
	spec, canonical, err := normalizeOneShotCommandProposalPayload(call.Payload)
	if err != nil {
		return Outcome{}, err
	}
	call.Payload = canonical
	policyDecision := g.checker.CheckToolCall(tools.Call{
		Name: string(call.Name), Args: map[string]string{"purpose": spec.Purpose},
	})
	if !policyDecision.Allowed {
		return deniedOutcome(call, policyDecision)
	}
	decision, err := gatewayDecision(policyDecision, ApprovalAutomatic, "low")
	if err != nil {
		return Outcome{}, err
	}
	scope := OneShotCommandProposalContext{
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
	result, err := g.oneShotCommandProposals.ProposeOneShotCommand(ctx, scope, spec)
	if err != nil {
		return Outcome{}, err
	}
	if err := result.Validate(); err != nil {
		return Outcome{}, err
	}
	completed := time.Now().UTC()
	return Outcome{
		Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{Backend: "agent_proposal", Status: StatusCompleted,
			StartedAt: started, CompletedAt: &completed},
		Result: &Result{Status: StatusCompleted, ExitCode: 0, MIME: "application/json",
			Metadata: map[string]string{"proposal_id": result.ProposalID,
				"replayed": strconv.FormatBool(result.Replayed)}},
	}, nil
}

var _ = fmt.Sprintf
