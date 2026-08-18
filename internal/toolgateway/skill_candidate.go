package toolgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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

const SkillCandidateProposalVersion = "skill_candidate_proposal.v1"

type SkillCandidateSpec struct {
	Version          string                    `json:"version"`
	Name             string                    `json:"name"`
	SkillVersion     string                    `json:"skill_version"`
	Description      string                    `json:"description"`
	Profiles         []domain.Profile          `json:"profiles"`
	Surfaces         []domain.ExecutionSurface `json:"surfaces"`
	Phases           []domain.ExecutionPhase   `json:"phases"`
	Roles            []domain.AgentRole        `json:"roles"`
	UserInvocable    bool                      `json:"user_invocable"`
	ModelInvocable   bool                      `json:"model_invocable"`
	ExplicitOnly     bool                      `json:"explicit_only"`
	ToolDependencies []ToolName                `json:"tool_dependencies"`
	Content          string                    `json:"content"`
}

type SkillCandidateContext struct {
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

func (c SkillCandidateContext) Validate() error {
	for label, value := range map[string]string{
		"invocation id": c.InvocationID, "operation key": c.OperationKey,
		"Run id": c.RunID, "root Agent id": c.RootAgentID, "Session id": c.SessionID,
		"Workspace id": c.WorkspaceID, "lease id": c.LeaseID, "requester": c.RequestedBy,
	} {
		if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) ||
			len([]rune(value)) > MaxToolIdentityRunes {
			return fmt.Errorf("Skill candidate %s is invalid", label)
		}
	}
	if c.RequestedBy != "run_supervisor" || c.LeaseGeneration <= 0 ||
		!domain.ValidAgentID(c.RootAgentID) {
		return errors.New("Skill candidate proposal requires a fenced root Supervisor")
	}
	if err := c.PolicyDecision.Validate(); err != nil || !c.PolicyDecision.Allowed ||
		c.PolicyDecision.Approval != ApprovalAutomatic {
		return errors.New("Skill candidate proposal requires an automatic allowed decision")
	}
	return nil
}

type SkillCandidateResult struct {
	CandidateID          string
	CandidateFingerprint string
	Name                 string
	Version              string
	Status               string
	PackageFingerprint   string
	ContentSHA256        string
	ContentBytes         int
	Replayed             bool
}

func (r SkillCandidateResult) Validate() error {
	if r.CandidateID == "" || r.CandidateFingerprint == "" || r.Name == "" ||
		r.Version == "" || r.Status != "proposed" || r.PackageFingerprint == "" ||
		r.ContentSHA256 == "" || r.ContentBytes <= 0 {
		return errors.New("Skill candidate result is invalid")
	}
	return nil
}

type SkillCandidateExecutor interface {
	ProposeSkillCandidate(context.Context, SkillCandidateContext, SkillCandidateSpec) (
		SkillCandidateResult, error)
}

var skillCandidateProposalDefinition = ToolDefinition{
	Name: SkillCandidateProposeTool, Class: ClassAgentProposal, Approval: ApprovalAutomatic,
	Description: "Persist one validated, untrusted Code Skill candidate for exact-fingerprint human review; never installs or selects it.",
	InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["version","name","skill_version","description","profiles","surfaces","phases","roles","user_invocable","model_invocable","explicit_only","tool_dependencies","content"],"properties":{"version":{"const":"skill_candidate_proposal.v1"},"name":{"type":"string","pattern":"^[a-z][a-z0-9-]{0,63}$"},"skill_version":{"type":"string","pattern":"^[0-9]+\\.[0-9]+\\.[0-9]+$"},"description":{"type":"string","minLength":1,"maxLength":512},"profiles":{"type":"array","minItems":1,"maxItems":4,"uniqueItems":true,"items":{"enum":["code","learn","review","script"]}},"surfaces":{"const":["code"]},"phases":{"type":"array","minItems":1,"maxItems":2,"uniqueItems":true,"items":{"enum":["plan","deliver"]}},"roles":{"type":"array","minItems":1,"maxItems":2,"uniqueItems":true,"items":{"enum":["root","specialist"]}},"user_invocable":{"type":"boolean"},"model_invocable":{"type":"boolean"},"explicit_only":{"type":"boolean"},"tool_dependencies":{"type":"array","maxItems":8,"uniqueItems":true,"items":{"enum":["list_workspace","read_file","replace_file","script_process"]}},"content":{"type":"string","minLength":1,"maxLength":4096}}}`),
}

func normalizeSkillCandidatePayload(payload json.RawMessage) (SkillCandidateSpec,
	json.RawMessage, error,
) {
	if len(payload) == 0 || len(payload) > MaxArgumentValueBytes || !utf8.Valid(payload) {
		return SkillCandidateSpec{}, nil, errors.New("Skill candidate payload is invalid")
	}
	if err := rejectDuplicateSkillCandidateFields(payload); err != nil {
		return SkillCandidateSpec{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var spec SkillCandidateSpec
	if err := decoder.Decode(&spec); err != nil {
		return SkillCandidateSpec{}, nil, fmt.Errorf("decode Skill candidate payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SkillCandidateSpec{}, nil, errors.New("Skill candidate payload has trailing JSON data")
	}
	if err := validateSkillCandidateSpec(spec); err != nil {
		return SkillCandidateSpec{}, nil, err
	}
	canonical, err := json.Marshal(spec)
	if err != nil {
		return SkillCandidateSpec{}, nil, err
	}
	return spec, canonical, nil
}

func rejectDuplicateSkillCandidateFields(payload json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("Skill candidate payload must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		field, ok := token.(string)
		if !ok {
			return errors.New("Skill candidate payload field name is invalid")
		}
		if _, exists := seen[field]; exists {
			return fmt.Errorf("Skill candidate payload contains duplicate field %q", field)
		}
		seen[field] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return errors.New("Skill candidate payload JSON object is not closed")
	}
	return nil
}

func validateSkillCandidateSpec(spec SkillCandidateSpec) error {
	if spec.Version != SkillCandidateProposalVersion || !candidateSkillName(spec.Name) ||
		!candidateSkillVersion(spec.SkillVersion) {
		return errors.New("Skill candidate identity is invalid")
	}
	if strings.TrimSpace(spec.Description) != spec.Description || spec.Description == "" ||
		utf8.RuneCountInString(spec.Description) > 512 ||
		redact.String(spec.Description) != spec.Description {
		return errors.New("Skill candidate description is invalid or contains sensitive data")
	}
	if len(spec.Profiles) == 0 || len(spec.Profiles) > 4 ||
		!orderedCandidateProfiles(spec.Profiles) {
		return errors.New("Skill candidate profiles must be valid, unique, and sorted")
	}
	if !slices.Equal(spec.Surfaces, []domain.ExecutionSurface{domain.ExecutionSurfaceCode}) ||
		!orderedCandidatePhases(spec.Phases) || !orderedCandidateRoles(spec.Roles) {
		return errors.New("Skill candidate mode metadata is invalid or not canonically ordered")
	}
	if (!spec.UserInvocable && !spec.ModelInvocable) ||
		(spec.ExplicitOnly && (!spec.UserInvocable || spec.ModelInvocable)) {
		return errors.New("Skill candidate invocation policy is invalid")
	}
	if len(spec.ToolDependencies) > 8 {
		return errors.New("Skill candidate has too many tool dependencies")
	}
	previous := ""
	for _, dependency := range spec.ToolDependencies {
		if !candidateSkillDependency(dependency, spec.Profiles) ||
			(previous != "" && previous >= string(dependency)) {
			return errors.New("Skill candidate tool dependencies are invalid or not sorted")
		}
		previous = string(dependency)
	}
	if len([]byte(spec.Content)) == 0 || len([]byte(spec.Content)) > 4096 ||
		!utf8.ValidString(spec.Content) || redact.String(spec.Content) != spec.Content {
		return errors.New("Skill candidate content is invalid or contains sensitive data")
	}
	for _, current := range spec.Description + spec.Content {
		if current == 0 || (unicode.IsControl(current) && current != '\n' &&
			current != '\r' && current != '\t') {
			return errors.New("Skill candidate text contains a forbidden control character")
		}
	}
	return nil
}

func candidateSkillName(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' ||
		strings.HasSuffix(value, "-") {
		return false
	}
	for _, current := range []byte(value) {
		if (current >= 'a' && current <= 'z') || (current >= '0' && current <= '9') ||
			current == '-' {
			continue
		}
		return false
	}
	return true
}

func candidateSkillVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 9 || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, current := range []byte(part) {
			if current < '0' || current > '9' {
				return false
			}
		}
	}
	return true
}

func orderedCandidateProfiles(values []domain.Profile) bool {
	previous := ""
	for _, value := range values {
		parsed, err := domain.ParseProfile(string(value))
		if err != nil || parsed != value || (previous != "" && previous >= string(value)) {
			return false
		}
		previous = string(value)
	}
	return true
}

func orderedCandidatePhases(values []domain.ExecutionPhase) bool {
	if len(values) == 0 || len(values) > 2 {
		return false
	}
	order := []domain.ExecutionPhase{domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver}
	previous := -1
	for _, value := range values {
		index := slices.Index(order, value)
		if index < 0 || index <= previous {
			return false
		}
		previous = index
	}
	return true
}

func orderedCandidateRoles(values []domain.AgentRole) bool {
	if len(values) == 0 || len(values) > 2 {
		return false
	}
	order := []domain.AgentRole{domain.AgentRoleRoot, domain.AgentRoleSpecialist}
	previous := -1
	for _, value := range values {
		index := slices.Index(order, value)
		if index < 0 || index <= previous {
			return false
		}
		previous = index
	}
	return true
}

func candidateSkillDependency(dependency ToolName, profiles []domain.Profile) bool {
	if dependency != ListWorkspaceTool && dependency != ReadFileTool &&
		dependency != ReplaceFileTool && dependency != ScriptProcessTool {
		return false
	}
	for _, profile := range profiles {
		readOnly := dependency == ListWorkspaceTool || dependency == ReadFileTool
		if (profile == domain.ProfileCode && !readOnly && dependency != ReplaceFileTool) ||
			((profile == domain.ProfileReview || profile == domain.ProfileLearn) && !readOnly) ||
			(profile == domain.ProfileScript && !readOnly && dependency != ScriptProcessTool) {
			return false
		}
	}
	return true
}

func (g *Gateway) WithSkillCandidateExecutor(executor SkillCandidateExecutor) *Gateway {
	if g != nil {
		g.skillCandidates = executor
	}
	return g
}

func (g *Gateway) invokeSkillCandidate(ctx context.Context, call ToolCall) (Outcome, error) {
	spec, canonical, err := normalizeSkillCandidatePayload(call.Payload)
	if err != nil {
		return Outcome{}, err
	}
	call.Payload = canonical
	policyDecision := g.checker.CheckToolCall(tools.Call{
		Name: string(call.Name), Args: map[string]string{"payload": string(canonical)},
	})
	if !policyDecision.Allowed {
		if err := g.recordSkillCandidatePolicyDecision(ctx, call, policyDecision); err != nil {
			return Outcome{}, err
		}
		return deniedOutcome(call, policyDecision)
	}
	decision, err := gatewayDecision(policyDecision, ApprovalAutomatic, "low")
	if err != nil {
		return Outcome{}, err
	}
	scope := SkillCandidateContext{
		InvocationID: call.InvocationID, OperationKey: call.OperationKey, RunID: call.RunID,
		RootAgentID: call.AgentID, SessionID: call.SessionID, WorkspaceID: call.WorkspaceID,
		LeaseID: call.LeaseID, LeaseGeneration: call.LeaseGeneration,
		RequestedBy: call.RequestedBy, PolicyDecision: decision,
	}
	if err := scope.Validate(); err != nil {
		return Outcome{}, err
	}
	started := time.Now().UTC()
	result, err := g.skillCandidates.ProposeSkillCandidate(ctx, scope, spec)
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
		Result: &Result{Status: StatusCompleted, ExitCode: 0, MIME: "application/json",
			CompletedAt: completed, Metadata: map[string]string{
				"candidate_id":          result.CandidateID,
				"candidate_fingerprint": result.CandidateFingerprint,
				"name":                  result.Name, "version": result.Version, "status": result.Status,
				"package_fingerprint":   result.PackageFingerprint,
				"content_sha256":        result.ContentSHA256,
				"content_bytes":         strconv.Itoa(result.ContentBytes),
				"human_review_required": "true", "installation_authorized": "false",
				"selection_authorized": "false", "replayed": strconv.FormatBool(result.Replayed),
			}},
	}
	return validateOutcome(outcome, nil)
}

func (g *Gateway) recordSkillCandidatePolicyDecision(ctx context.Context, call ToolCall,
	decision policy.Decision,
) error {
	if g == nil || g.policyRecorder == nil {
		return errors.New("Skill candidate policy decision recorder is required")
	}
	return g.policyRecorder.RecordPolicyDecision(ctx, policy.DecisionRecord{
		SessionID: call.SessionID, SubjectID: call.InvocationID,
		Context: "tool_run." + string(call.Name), Decision: decision,
	})
}
