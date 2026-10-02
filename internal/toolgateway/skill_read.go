package toolgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/tools"
)

const SkillReadTool ToolName = "skill_read"

type SkillReadRequest struct {
	Name                   string `json:"name,omitempty"`
	Version                string `json:"version,omitempty"`
	ContentSHA256          string `json:"content_sha256,omitempty"`
	InstallationID         string `json:"installation_id,omitempty"`
	PackageID              string `json:"package_id,omitempty"`
	ComponentID            string `json:"component_id,omitempty"`
	Revision               string `json:"revision,omitempty"`
	InstallationGeneration int64  `json:"installation_generation,omitempty"`
	Resource               string `json:"resource,omitempty"`
}

func (r SkillReadRequest) Portable() bool { return r.InstallationID != "" }

// CatalogRequest excludes the optional resource selector, not installation
// identity or generation. Resource names never supply digest authority.
func (r SkillReadRequest) CatalogRequest() SkillReadRequest { r.Resource = ""; return r }

type BuiltinSkillDescriptor struct {
	SkillReadRequest
	Description  string `json:"description"`
	ContentBytes int    `json:"content_bytes"`
}

type SkillReadExecutor interface {
	ReadBuiltinSkill(context.Context, ToolCall) (json.RawMessage, error)
}

func SkillReadToolDefinition(catalog []BuiltinSkillDescriptor) ToolDefinition {
	description := "Read a relevant workflow skill before using it. Choose by task and description; do not load every skill. Copy the exact read identity from the available catalog: bundled skills use name/version/content_sha256; installed skills use installation_id/package_id/component_id/revision/installation_generation. For an installed skill, omit resource to read its original SKILL.md, or set resource to an exact relative resource path mentioned there. Reading grants no tools or permissions. Bundled guidance is restored within its existing budget; installed activation references are restored so you can re-read exact source after compaction. Resource bodies are not automatically reinserted."
	if len(catalog) > 0 {
		encoded, _ := json.Marshal(catalog)
		description += " Available skills for this mode: " + string(encoded)
	}
	return ToolDefinition{Name: SkillReadTool, Class: ClassRunMemory, Approval: ApprovalAutomatic,
		Description: description,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string","maxLength":64},"version":{"type":"string","maxLength":32},"content_sha256":{"type":"string","pattern":"^[0-9a-f]{64}$"},"installation_id":{"type":"string","maxLength":256},"package_id":{"type":"string","maxLength":256},"component_id":{"type":"string","maxLength":256},"revision":{"type":"string","pattern":"^[0-9a-f]{64}$"},"installation_generation":{"type":"integer","minimum":1},"resource":{"type":"string","maxLength":4096}},"oneOf":[{"required":["name","version","content_sha256"]},{"required":["installation_id","package_id","component_id","revision","installation_generation"]}]}`)}
}

func NormalizeSkillReadPayload(payload json.RawMessage) (SkillReadRequest, json.RawMessage, error) {
	var input SkillReadRequest
	if len(payload) == 0 || len(payload) > 8192 || !utf8.Valid(payload) {
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
	if input.Portable() {
		component := toolcontract.ComponentRef{PackageID: input.PackageID, ComponentID: input.ComponentID}
		if component.Validate() != nil || !domain.ValidAgentID(input.InstallationID) || len(input.InstallationID) > 256 || strings.IndexFunc(input.InstallationID, unicode.IsControl) >= 0 ||
			input.Name != "" || input.Version != "" || input.ContentSHA256 != "" || input.InstallationGeneration < 1 ||
			len(input.Revision) != 64 || strings.Trim(input.Revision, "0123456789abcdef") != "" ||
			(input.Resource != "" && (len(input.Resource) > 4096 || !fs.ValidPath(input.Resource) || input.Resource == "." || strings.ContainsAny(input.Resource, "\\:\x00"))) {
			return input, nil, errors.New("skill_read requires an exact installed component and a contained resource path")
		}
		canonical, err := json.Marshal(input)
		return input, canonical, err
	}
	if input.PackageID != "" || input.ComponentID != "" || input.Revision != "" || input.InstallationGeneration != 0 || input.Resource != "" || len(payload) > 1024 {
		return input, nil, errors.New("skill_read identities cannot mix bundled and installed fields")
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
		return Outcome{}, errors.New("skill reader is unavailable")
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
		return Outcome{}, errors.New("invalid skill read result")
	}
	completed := time.Now().UTC()
	backend := "embedded_skills"
	if pin, _, _ := NormalizeSkillReadPayload(call.Payload); pin.Portable() {
		backend = "installed_skills"
	}
	return validateOutcome(Outcome{Call: safeToolCall(call), Decision: decision,
		Execution: &Execution{Backend: backend, Status: StatusCompleted, StartedAt: started, CompletedAt: &completed},
		Result: &Result{Status: StatusCompleted, ExitCode: 0, MIME: "application/json", CompletedAt: completed,
			Stdout: string(content), Metadata: map[string]string{"skill_read": "true", "capability_grant": "false"}}}, nil)
}
