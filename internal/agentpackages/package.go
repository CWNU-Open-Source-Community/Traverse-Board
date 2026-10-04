package agentpackages

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/toolcontract"
)

// Package owns a read-only directory handle and parsed metadata. All shared
// values use the approved toolcontract; private descriptions are not persisted.
// C supplies an acquired immutable installation snapshot, package identity and
// provenance, and retains ownership of trust, authority and resource inventory.
type Package struct {
	root                  *directorySource
	source                toolcontract.SourceRef
	format, name, version string
	manifest              toolcontract.ContentRef
	skills                map[toolcontract.ComponentRef]skillDescription
	serverKeys            map[toolcontract.ComponentRef]string
	serverContent         map[toolcontract.ComponentRef]toolcontract.ContentRef
	launches              []toolcontract.LaunchDeclaration
	diagnostics           []toolcontract.Diagnostic
}

func OpenDirectory(ctx context.Context, directory, packageID string, source toolcontract.SourceRef) (*Package, error) {
	if source.Validate() != nil || (toolcontract.ComponentRef{PackageID: packageID, ComponentID: "package"}).Validate() != nil {
		return nil, errors.New("package_identity_invalid")
	}
	root, err := openDirectory(directory)
	if err != nil {
		return nil, err
	}
	pkg, err := describeDirectory(ctx, root, filepath.Base(filepath.Clean(directory)), packageID, source)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	pkg.root = root
	return pkg, nil
}

func describeDirectory(ctx context.Context, root *directorySource, directoryName, packageID string, source toolcontract.SourceRef) (*Package, error) {
	format, err := detectFormat(ctx, root)
	if err != nil {
		return nil, err
	}
	if format == FormatLegacySkill || format == FormatLegacyPlugin {
		return nil, ErrLegacyFormat
	}
	result := &Package{source: source, format: format, skills: map[toolcontract.ComponentRef]skillDescription{}, serverKeys: map[toolcontract.ComponentRef]string{}, serverContent: map[toolcontract.ComponentRef]toolcontract.ContentRef{}}
	if format == FormatAgentSkill {
		skill, err := inspectNamedSkill(ctx, root, ".", directoryName)
		if err != nil {
			return nil, err
		}
		ref := componentRef(packageID, "skill", ".")
		result.skills[ref] = skill
		result.name = skill.name
		result.manifest = publicContent(ref, skill.instructions)
		return result, nil
	}
	plugin, err := inspectPlugin(ctx, root)
	if err != nil {
		return nil, err
	}
	result.name, result.version = plugin.name, plugin.version
	result.manifest = publicContent(componentRef(packageID, "manifest", ""), plugin.manifest)
	for _, skill := range plugin.skills {
		result.skills[componentRef(packageID, "skill", skill.root)] = skill
	}
	for _, diagnostic := range plugin.diagnostics {
		result.diagnostics = append(result.diagnostics, publicDiagnostic(packageID, diagnostic))
	}
	for _, server := range plugin.servers {
		ref := componentRef(packageID, "server", server.key)
		result.serverKeys[ref] = server.key
		result.serverContent[ref] = publicContent(ref, plugin.mcp)
		launch, err := bindLaunch(server, ref)
		if err != nil {
			result.diagnostics = append(result.diagnostics, toolcontract.Diagnostic{Component: ref, Code: err.Error(), Severity: "warning", Message: "MCP component is retained in the source but cannot be prepared by this host."})
			continue
		}
		result.launches = append(result.launches, launch)
	}
	return result, nil
}

// Component keys are bounded even for arbitrary MCP map keys. They distinguish
// component kinds, remain stable across revisions, and never contain config.
func componentRef(packageID, kind, key string) toolcontract.ComponentRef {
	return toolcontract.ComponentRef{PackageID: packageID, ComponentID: kind + ":" + digestBytes([]byte(key))}
}

func publicContent(ref toolcontract.ComponentRef, content contentReference) toolcontract.ContentRef {
	return toolcontract.ContentRef{Component: ref, Path: content.path, SHA256: content.sha256}
}

func publicDiagnostic(packageID string, value componentDiagnostic) toolcontract.Diagnostic {
	key := value.component
	if value.boundary == "skill" {
		key = strings.TrimSuffix(value.path, "/SKILL.md")
	} else if value.boundary == "manifest" {
		key = ""
	}
	location := value.path
	if len(location) > 512 {
		location = "component source"
	}
	return toolcontract.Diagnostic{Component: componentRef(packageID, value.boundary, key), Code: value.code,
		Severity: "warning", Message: "Package component at " + strconv.Quote(location) + " was skipped or an unsupported field was ignored; valid siblings remain available."}
}

func (*Package) String() string     { return "AgentPackage[redacted]" }
func (p *Package) GoString() string { return p.String() }

func (p *Package) Close() error                           { return p.root.Close() }
func (p *Package) Source() toolcontract.SourceRef         { return p.source }
func (p *Package) Format() string                         { return p.format }
func (p *Package) Name() string                           { return p.name }
func (p *Package) Version() string                        { return p.version }
func (p *Package) Manifest() toolcontract.ContentRef      { return p.manifest }
func (p *Package) Diagnostics() []toolcontract.Diagnostic { return slices.Clone(p.diagnostics) }

// ServerSource preserves the native map key and source document reference,
// including for valid transports this host cannot launch. It exposes no config.
func (p *Package) ServerSource(ref toolcontract.ComponentRef) (key string, content toolcontract.ContentRef, found bool) {
	key, found = p.serverKeys[ref]
	return key, p.serverContent[ref], found
}

func (p *Package) Skills() []toolcontract.ComponentRef {
	refs := slices.Collect(maps.Keys(p.skills))
	slices.SortFunc(refs, func(a, b toolcontract.ComponentRef) int { return strings.Compare(a.ComponentID, b.ComponentID) })
	return refs
}

// SkillSummary exposes only name/description. Frontmatter preserves all source
// metadata separately. Listing never injects instruction bodies or resources.
func (p *Package) SkillSummary(ref toolcontract.ComponentRef) (name, description string, err error) {
	skill, found := p.skills[ref]
	if !found {
		return "", "", errors.New("skill_component_unknown")
	}
	return skill.name, skill.description, nil
}

func (p *Package) Frontmatter(ref toolcontract.ComponentRef) ([]byte, error) {
	skill, found := p.skills[ref]
	if !found {
		return nil, errors.New("skill_component_unknown")
	}
	return bytes.Clone(skill.frontmatter), nil
}

func (p *Package) Instructions(ref toolcontract.ComponentRef) (toolcontract.ContentRef, error) {
	skill, found := p.skills[ref]
	if !found {
		return toolcontract.ContentRef{}, errors.New("skill_component_unknown")
	}
	return publicContent(ref, skill.instructions), nil
}

// Resource receives the digest from C's trusted snapshot inventory. It does not
// hash or open a file at discovery time and grants no filesystem authority.
func (p *Package) Resource(ref toolcontract.ComponentRef, relative, digest string) (toolcontract.ContentRef, error) {
	skill, found := p.skills[ref]
	if !found || !relativeFile(relative) || digest == "" {
		return toolcontract.ContentRef{}, errors.New("resource_reference_invalid")
	}
	content := toolcontract.ContentRef{Component: ref, Path: path.Join(skill.root, relative), SHA256: digest}
	if content.Validate() != nil {
		return toolcontract.ContentRef{}, errors.New("resource_reference_invalid")
	}
	return content, nil
}

func (p *Package) Read(ctx context.Context, ref toolcontract.ContentRef, limit int) ([]byte, error) {
	if ref.Validate() != nil || ref.SHA256 == "" {
		return nil, errors.New("content_reference_invalid")
	}
	skill, found := p.skills[ref.Component]
	if !found || (skill.root != "." && !strings.HasPrefix(ref.Path, skill.root+"/")) {
		return nil, errors.New("content_component_mismatch")
	}
	if ref.Path == skill.instructions.path && ref.SHA256 != skill.instructions.sha256 {
		return nil, errors.New("content_revision_changed")
	}
	return readContent(ctx, p.root, contentReference{path: ref.Path, sha256: ref.SHA256, bytes: -1}, limit)
}

// Launches returns detached, unexpanded declarations. B alone applies trusted
// roots/environment and freezes runtime configuration. Never log subfields.
func (p *Package) Launches() []toolcontract.LaunchDeclaration {
	result := make([]toolcontract.LaunchDeclaration, len(p.launches))
	for index, launch := range p.launches {
		result[index] = cloneLaunch(launch)
	}
	return result
}
