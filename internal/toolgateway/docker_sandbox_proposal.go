package toolgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/tools"
)

const DockerSandboxRunProposalVersion = "sandbox_docker_run_proposal.v1"

// DockerSandboxProposalSpec is deliberately narrower than a Docker create
// request. The model may identify an already-compiled plan and provide the
// established Sandbox Manifest protocol; it cannot supply an image, daemon
// endpoint, Docker flags, host bind, environment, proxy, or start authority.
type DockerSandboxProposalSpec struct {
	Version  string
	PlanID   string
	Manifest sandbox.Manifest
}

func (s DockerSandboxProposalSpec) Validate() error {
	if s.Version != DockerSandboxRunProposalVersion ||
		!domain.ValidAgentID(s.PlanID) || strings.ContainsRune(s.PlanID, 0) {
		return errors.New("Docker Sandbox proposal identity is invalid")
	}
	manifest, err := sandbox.NormalizeManifest(s.Manifest)
	if err != nil || manifest.Backend != sandbox.BackendDocker ||
		manifest.Network.Mode != "disabled" ||
		len(manifest.Network.AllowedTargets) != 0 ||
		len(manifest.Environment) != 0 {
		return errors.New("Docker Sandbox proposal must use the environment-free Docker Manifest with network disabled")
	}
	return nil
}

type DockerSandboxProposalContext struct {
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

func (c DockerSandboxProposalContext) Validate() error {
	for label, value := range map[string]string{
		"invocation id": c.InvocationID, "operation key": c.OperationKey,
		"Run id": c.RunID, "root Agent id": c.RootAgentID,
		"Session id": c.SessionID, "Workspace id": c.WorkspaceID,
		"lease id": c.LeaseID, "requester": c.RequestedBy,
	} {
		if value == "" || !utf8.ValidString(value) ||
			strings.TrimSpace(value) != value || strings.ContainsRune(value, 0) ||
			utf8.RuneCountInString(value) > MaxToolIdentityRunes {
			return fmt.Errorf("Docker Sandbox proposal %s is invalid", label)
		}
	}
	if c.RequestedBy != "run_supervisor" || c.LeaseGeneration <= 0 ||
		!domain.ValidAgentID(c.RootAgentID) {
		return errors.New("Docker Sandbox proposal requires a fenced root Supervisor scope")
	}
	if err := c.PolicyDecision.Validate(); err != nil {
		return err
	}
	if !c.PolicyDecision.Allowed || c.PolicyDecision.Approval != ApprovalAutomatic {
		return errors.New("Docker Sandbox proposal requires an automatic allowed decision")
	}
	return nil
}

// DockerSandboxProposalResult describes admission only. It intentionally has
// no execution flag that an executor can set to true and carries no Manifest,
// command, path, Docker configuration, or authority fingerprint.
type DockerSandboxProposalResult struct {
	AdmissionID     string
	Allowed         bool
	ReasonCode      string
	RemediationCode string
	Replayed        bool
}

func (r DockerSandboxProposalResult) Validate() error {
	if r.Allowed != (r.AdmissionID != "") ||
		(r.AdmissionID != "" && (!domain.ValidAgentID(r.AdmissionID) ||
			strings.IndexFunc(r.AdmissionID, unicode.IsControl) >= 0)) {
		return errors.New("Docker Sandbox proposal admission identity is invalid")
	}
	for label, value := range map[string]string{
		"reason code": r.ReasonCode, "remediation code": r.RemediationCode,
	} {
		if !validDockerSandboxProposalCode(value) {
			return fmt.Errorf("Docker Sandbox proposal %s is invalid", label)
		}
	}
	return nil
}

func validDockerSandboxProposalCode(value string) bool {
	if value == "" || len(value) > MaxToolIdentityRunes {
		return false
	}
	for _, current := range value {
		if (current < 'a' || current > 'z') &&
			(current < '0' || current > '9') && current != '_' {
			return false
		}
	}
	return true
}

type DockerSandboxProposalExecutor interface {
	ProposeDockerSandbox(context.Context, DockerSandboxProposalContext,
		DockerSandboxProposalSpec) (DockerSandboxProposalResult, error)
}

var dockerSandboxProposalDefinition = ToolDefinition{
	Name: DockerSandboxRunProposeTool, Class: ClassAgentProposal,
	Approval:    ApprovalAutomatic,
	Description: "Propose one already-compiled Docker Sandbox plan for Go-owned product admission; this tool never starts a container.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","plan_id","manifest"],"properties":{"version":{"const":"sandbox_docker_run_proposal.v1"},"plan_id":{"type":"string","minLength":1,"maxLength":256},"manifest":{"type":"object","additionalProperties":false,"required":["protocol_version","backend","command","mounts","network","resources","output","timeout_seconds","cancellation"],"properties":{"protocol_version":{"const":"sandbox_manifest.v1"},"backend":{"const":"docker"},"command":{"type":"object","additionalProperties":false,"required":["executable","working_directory"],"properties":{"executable":{"type":"string","minLength":1,"maxLength":1024},"arguments":{"type":"array","maxItems":128,"items":{"type":"string","maxLength":4096}},"working_directory":{"type":"string","minLength":2,"maxLength":1024}}},"mounts":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["source","target","access"],"properties":{"source":{"type":"string","minLength":1,"maxLength":1024},"target":{"type":"string","minLength":2,"maxLength":1024},"access":{"enum":["read_only","read_write"]}}}},"network":{"type":"object","additionalProperties":false,"required":["mode"],"properties":{"mode":{"const":"disabled"},"allowed_targets":{"type":"array","maxItems":0}}},"resources":{"type":"object","additionalProperties":false,"required":["cpu_quota_millis","memory_bytes","pids","max_output_bytes"],"properties":{"cpu_quota_millis":{"type":"integer","minimum":1,"maximum":8000},"memory_bytes":{"type":"integer","minimum":16777216,"maximum":8589934592},"pids":{"type":"integer","minimum":1,"maximum":512},"max_output_bytes":{"type":"integer","minimum":1,"maximum":16777216}}},"input_artifact_ids":{"type":"array","maxItems":16,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":256}},"output":{"type":"object","additionalProperties":false,"required":["capture_stdout","capture_stderr"],"properties":{"capture_stdout":{"type":"boolean"},"capture_stderr":{"type":"boolean"},"paths":{"type":"array","maxItems":16,"uniqueItems":true,"items":{"type":"string","minLength":2,"maxLength":1024}}}},"timeout_seconds":{"type":"integer","minimum":1,"maximum":3600},"cancellation":{"type":"object","additionalProperties":false,"required":["grace_period_millis"],"properties":{"grace_period_millis":{"type":"integer","minimum":0,"maximum":30000}}}}}}}`),
}

type dockerSandboxProposalWire struct {
	Version  string          `json:"version"`
	PlanID   string          `json:"plan_id"`
	Manifest json.RawMessage `json:"manifest"`
}

func normalizeDockerSandboxProposalPayload(payload json.RawMessage) (
	DockerSandboxProposalSpec, json.RawMessage, error,
) {
	wire, err := decodeDockerSandboxProposalWire(payload)
	if err != nil {
		return DockerSandboxProposalSpec{}, nil, err
	}
	var manifestShape map[string]json.RawMessage
	if err := json.Unmarshal(wire.Manifest, &manifestShape); err != nil {
		return DockerSandboxProposalSpec{}, nil,
			fmt.Errorf("invalid Docker Sandbox Manifest object: %w", err)
	}
	if _, present := manifestShape["environment"]; present {
		return DockerSandboxProposalSpec{}, nil, errors.New(
			"model Docker Sandbox proposals cannot supply environment bindings")
	}
	manifest, err := sandbox.DecodeManifest(wire.Manifest)
	if err != nil {
		return DockerSandboxProposalSpec{}, nil,
			fmt.Errorf("invalid Docker Sandbox Manifest: %w", err)
	}
	spec := DockerSandboxProposalSpec{
		Version: strings.TrimSpace(wire.Version),
		PlanID:  strings.TrimSpace(wire.PlanID), Manifest: manifest,
	}
	if err := spec.Validate(); err != nil {
		return DockerSandboxProposalSpec{}, nil, err
	}
	manifestJSON, err := manifest.CanonicalJSON()
	if err != nil {
		return DockerSandboxProposalSpec{}, nil, err
	}
	canonical, err := json.Marshal(struct {
		Version  string          `json:"version"`
		PlanID   string          `json:"plan_id"`
		Manifest json.RawMessage `json:"manifest"`
	}{Version: spec.Version, PlanID: spec.PlanID, Manifest: manifestJSON})
	return spec, canonical, err
}

func decodeDockerSandboxProposalWire(payload json.RawMessage) (
	dockerSandboxProposalWire, error,
) {
	if len(payload) == 0 || len(payload) > MaxStructuredMemoryPayloadBytes ||
		!utf8.Valid(payload) {
		return dockerSandboxProposalWire{}, errors.New(
			"Docker Sandbox proposal must be bounded UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return dockerSandboxProposalWire{}, errors.New(
			"Docker Sandbox proposal must be a JSON object")
	}
	var wire dockerSandboxProposalWire
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		fieldToken, err := decoder.Token()
		if err != nil {
			return dockerSandboxProposalWire{}, errors.New("invalid Docker Sandbox proposal field")
		}
		field, ok := fieldToken.(string)
		if !ok {
			return dockerSandboxProposalWire{}, errors.New("invalid Docker Sandbox proposal field")
		}
		if _, duplicate := seen[field]; duplicate {
			return dockerSandboxProposalWire{}, fmt.Errorf(
				"Docker Sandbox proposal contains duplicate field %q", field)
		}
		seen[field] = struct{}{}
		switch field {
		case "version":
			err = decoder.Decode(&wire.Version)
		case "plan_id":
			err = decoder.Decode(&wire.PlanID)
		case "manifest":
			err = decoder.Decode(&wire.Manifest)
		default:
			return dockerSandboxProposalWire{}, fmt.Errorf(
				"Docker Sandbox proposal contains unknown field %q", field)
		}
		if err != nil {
			return dockerSandboxProposalWire{}, fmt.Errorf(
				"decode Docker Sandbox proposal field %q: %w", field, err)
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return dockerSandboxProposalWire{}, errors.New("Docker Sandbox proposal object is not closed")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return dockerSandboxProposalWire{}, errors.New("Docker Sandbox proposal contains trailing JSON")
	}
	if _, ok := seen["version"]; !ok {
		return dockerSandboxProposalWire{}, errors.New("Docker Sandbox proposal requires version")
	}
	if _, ok := seen["plan_id"]; !ok {
		return dockerSandboxProposalWire{}, errors.New("Docker Sandbox proposal requires plan_id")
	}
	if _, ok := seen["manifest"]; !ok {
		return dockerSandboxProposalWire{}, errors.New("Docker Sandbox proposal requires Manifest")
	}
	return wire, nil
}

func (g *Gateway) WithDockerSandboxProposalExecutor(
	executor DockerSandboxProposalExecutor,
) *Gateway {
	if g != nil {
		g.dockerSandboxProposals = executor
	}
	return g
}

func (g *Gateway) invokeDockerSandboxProposal(ctx context.Context,
	call ToolCall,
) (Outcome, error) {
	spec, canonical, err := normalizeDockerSandboxProposalPayload(call.Payload)
	if err != nil {
		return Outcome{}, err
	}
	call.Payload = canonical
	policyDecision := g.checker.CheckToolCall(tools.Call{
		Name: string(call.Name), Args: map[string]string{"payload": string(canonical)},
	})
	if !policyDecision.Allowed {
		if err := g.recordDockerSandboxProposalPolicyDecision(ctx, call,
			policyDecision); err != nil {
			return Outcome{}, err
		}
		return deniedOutcome(call, policyDecision)
	}
	decision, err := gatewayDecision(policyDecision, ApprovalAutomatic, "medium")
	if err != nil {
		return Outcome{}, err
	}
	scope := DockerSandboxProposalContext{
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
	result, err := g.dockerSandboxProposals.ProposeDockerSandbox(ctx, scope, spec)
	if err != nil {
		return Outcome{}, err
	}
	if err := result.Validate(); err != nil {
		return Outcome{}, err
	}
	completed := time.Now().UTC()
	metadata := map[string]string{
		"allowed":              strconv.FormatBool(result.Allowed),
		"reason":               result.ReasonCode,
		"remediation":          result.RemediationCode,
		"replayed":             strconv.FormatBool(result.Replayed),
		"execution_authorized": "false",
	}
	if result.AdmissionID != "" {
		metadata["admission_id"] = result.AdmissionID
	}
	outcome := Outcome{
		Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{Backend: "docker_sandbox_admission",
			Status: StatusCompleted, StartedAt: started, CompletedAt: &completed},
		Result: &Result{Status: StatusCompleted, ExitCode: 0,
			MIME: "application/json", Metadata: metadata, CompletedAt: completed},
	}
	return validateOutcome(outcome, nil)
}

func (g *Gateway) recordDockerSandboxProposalPolicyDecision(ctx context.Context,
	call ToolCall, decision policy.Decision,
) error {
	if g == nil || g.policyRecorder == nil {
		return errors.New("Docker Sandbox proposal policy decision recorder is required")
	}
	return g.policyRecorder.RecordPolicyDecision(ctx, policy.DecisionRecord{
		SessionID: call.SessionID, SubjectID: call.InvocationID,
		Context: "tool_run." + string(call.Name), Decision: decision,
	})
}
