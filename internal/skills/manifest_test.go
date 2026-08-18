package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
)

func TestManifestValidationRejectsMalformedAndEscalatingMetadata(t *testing.T) {
	content := []byte("# Test\n\nBounded content.\n")
	valid := fixtureModeManifest(content)
	if err := valid.Validate(content); err != nil {
		t.Fatalf("valid manifest failed: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Manifest)
		content []byte
		want    string
	}{
		{name: "protocol", mutate: func(m *Manifest) { m.Protocol = "skill.v2" }, want: "unsupported skill protocol"},
		{name: "name", mutate: func(m *Manifest) { m.Name = "Code" }, want: "invalid skill name"},
		{name: "version", mutate: func(m *Manifest) { m.Version = "01.0.0" }, want: "invalid skill version"},
		{name: "description whitespace", mutate: func(m *Manifest) { m.Description += " " }, want: "description"},
		{name: "description control", mutate: func(m *Manifest) { m.Description = "bad\nline" }, want: "control"},
		{name: "missing profiles", mutate: func(m *Manifest) { m.Profiles = nil }, want: "profiles"},
		{name: "duplicate profiles", mutate: func(m *Manifest) { m.Profiles = []domain.Profile{domain.ProfileCode, domain.ProfileCode} }, want: "unique and sorted"},
		{name: "unsorted profiles", mutate: func(m *Manifest) { m.Profiles = []domain.Profile{domain.ProfileReview, domain.ProfileCode} }, want: "unique and sorted"},
		{name: "unknown profile", mutate: func(m *Manifest) { m.Profiles = []domain.Profile{"admin"} }, want: "invalid skill profile"},
		{name: "missing surfaces", mutate: func(m *Manifest) { m.Surfaces = nil }, want: "declared together"},
		{name: "unsorted surfaces", mutate: func(m *Manifest) {
			m.Surfaces = []domain.ExecutionSurface{domain.ExecutionSurfaceCyber, domain.ExecutionSurfaceCode}
		}, want: "canonically ordered"},
		{name: "unknown surface", mutate: func(m *Manifest) { m.Surfaces = []domain.ExecutionSurface{"admin"} }, want: "invalid skill surfaces"},
		{name: "unsorted phases", mutate: func(m *Manifest) {
			m.Phases = []domain.ExecutionPhase{domain.ExecutionPhaseDeliver, domain.ExecutionPhasePlan}
		}, want: "canonically ordered"},
		{name: "duplicate roles", mutate: func(m *Manifest) {
			m.Roles = []domain.AgentRole{domain.AgentRoleRoot, domain.AgentRoleRoot}
		}, want: "canonically ordered"},
		{name: "not invocable", mutate: func(m *Manifest) {
			m.UserInvocable, m.ModelInvocable = false, false
		}, want: "must be user_invocable"},
		{name: "explicit model invocation", mutate: func(m *Manifest) {
			m.ExplicitOnly, m.ModelInvocable = true, true
		}, want: "explicit_only"},
		{name: "unknown tool", mutate: func(m *Manifest) { m.ToolDependencies = []toolgateway.ToolName{"invented"} }, want: "unsupported skill tool dependency"},
		{name: "missing tool declaration", mutate: func(m *Manifest) { m.ToolDependencies = nil }, want: "tool_dependencies is required"},
		{name: "unsorted tools", mutate: func(m *Manifest) {
			m.ToolDependencies = []toolgateway.ToolName{toolgateway.ReadFileTool, toolgateway.ListWorkspaceTool}
		}, want: "unique and sorted"},
		{name: "profile escalation", mutate: func(m *Manifest) { m.ToolDependencies = []toolgateway.ToolName{toolgateway.ShellTool} }, want: "incompatible with profile"},
		{name: "path traversal", mutate: func(m *Manifest) { m.ContentPath = "../SKILL.md" }, want: "content_path"},
		{name: "windows path", mutate: func(m *Manifest) { m.ContentPath = `docs\SKILL.md` }, want: "content_path"},
		{name: "non markdown path", mutate: func(m *Manifest) { m.ContentPath = "skill.txt" }, want: "content_path"},
		{name: "byte count", mutate: func(m *Manifest) { m.ContentBytes++ }, want: "content_bytes"},
		{name: "token bound", mutate: func(m *Manifest) { m.ContentTokenUpperBound-- }, want: "content_token_upper_bound"},
		{name: "uppercase checksum", mutate: func(m *Manifest) { m.ContentSHA256 = strings.ToUpper(m.ContentSHA256) }, want: "lowercase hexadecimal"},
		{name: "checksum mismatch", mutate: func(m *Manifest) { m.ContentSHA256 = strings.Repeat("0", 64) }, want: "does not match"},
		{name: "invalid UTF-8", content: []byte{0xff}, want: "valid UTF-8"},
		{name: "content control", content: []byte("bad\x00content"), want: "control character"},
		{name: "content too large", content: []byte(strings.Repeat("x", MaxContentBytes+1)), want: "valid UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneManifest(valid)
			if test.mutate != nil {
				test.mutate(&manifest)
			}
			candidate := content
			if test.content != nil {
				candidate = test.content
			}
			err := manifest.Validate(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLegacyManifestKeepsConservativeInvocationAndModeCompatibility(t *testing.T) {
	content := []byte("# Legacy\n")
	manifest := fixtureManifest(content)
	if err := manifest.Validate(content); err != nil {
		t.Fatalf("legacy manifest failed validation: %v", err)
	}
	if !manifest.AllowsInvocation(InvocationSourceUser, true) ||
		manifest.AllowsInvocation(InvocationSourceModel, false) {
		t.Fatal("legacy manifest invocation policy drifted")
	}
	if !manifest.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleSpecialist,
	}) || manifest.SupportsContext(ExecutionContext{
		Surface: domain.ExecutionSurfaceCyber, Phase: domain.ExecutionPhaseDeliver,
		Profile: domain.ProfileCode, Role: domain.AgentRoleSpecialist,
	}) {
		t.Fatal("legacy Specialist compatibility drifted")
	}
}

func TestCoreVersionAndNameBounds(t *testing.T) {
	for _, value := range []string{"0.0.0", "1.2.3", "999999999.0.1"} {
		if !validCoreVersion(value) {
			t.Errorf("valid core version %q rejected", value)
		}
	}
	for _, value := range []string{"", "1", "1.2", "1.2.3.4", "v1.2.3", "1.2.-1", "1.2.3-beta", "0000000000.1.1"} {
		if validCoreVersion(value) {
			t.Errorf("invalid core version %q accepted", value)
		}
	}
	if validName("a-") || validName(strings.Repeat("a", MaxNameBytes+1)) || !validName("code-review2") {
		t.Fatal("skill name bounds are inconsistent")
	}
}

func fixtureManifest(content []byte) Manifest {
	digest := sha256.Sum256(content)
	return Manifest{
		Protocol:               ProtocolVersion,
		Name:                   "code",
		Version:                "1.0.0",
		Description:            "Test coding metadata.",
		Profiles:               []domain.Profile{domain.ProfileCode},
		ToolDependencies:       []toolgateway.ToolName{toolgateway.ListWorkspaceTool, toolgateway.ReadFileTool},
		ContentPath:            "SKILL.md",
		ContentSHA256:          hex.EncodeToString(digest[:]),
		ContentBytes:           len(content),
		ContentTokenUpperBound: ContentTokenUpperBound(content),
	}
}

func fixtureModeManifest(content []byte) Manifest {
	manifest := fixtureManifest(content)
	manifest.Surfaces = []domain.ExecutionSurface{
		domain.ExecutionSurfaceCode, domain.ExecutionSurfaceCyber,
	}
	manifest.Phases = []domain.ExecutionPhase{
		domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver,
	}
	manifest.Roles = []domain.AgentRole{domain.AgentRoleRoot, domain.AgentRoleSpecialist}
	manifest.UserInvocable = true
	manifest.ModelInvocable = true
	return manifest
}
