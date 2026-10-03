package plugins

import (
	"errors"
	"reflect"
	"slices"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
)

// SkillSelectionSource projects a real legacy-format Plugin component into the
// existing Run selection resolver, without an old installation or object receipt.
func SkillSelectionSource(value Installation) (skills.ExternalSelectionSource, error) {
	if value.Validate() != nil || value.Snapshot == nil || value.Snapshot.Legacy == nil ||
		value.State != StateEnabled || !slices.Contains(value.EnabledCapabilities, CapabilitySkills) || len(value.Snapshot.Skills) != 1 {
		return skills.ExternalSelectionSource{}, errors.New("Plugin Skill is not enabled for explicit selection")
	}
	m := value.Snapshot.Legacy.Manifest
	component := value.Snapshot.Skills[0].Instructions
	if component.SHA256 != m.ContentSHA256 {
		return skills.ExternalSelectionSource{}, errors.New("Plugin Skill content identity changed")
	}
	return skills.ExternalSelectionSource{Manifest: m, Item: skills.ExternalSelectionItem{
		Plugin: &skills.PluginSkillBinding{PackageID: component.Component.PackageID,
			ComponentID: component.Component.ComponentID, Revision: value.Revision(), Generation: value.Generation},
		InstallationID: value.ID, InstallationFingerprint: InstallationFingerprint(value),
		Name: m.Name, Version: m.Version, Surface: domain.ExecutionSurface(value.Source.Surface),
		ContentSHA256: m.ContentSHA256, ContentBytes: m.ContentBytes, TokenUpperBound: m.ContentTokenUpperBound,
		ArchiveSHA256: value.ArchiveSHA256, ArchiveBytes: value.ArchiveBytes, PackageFingerprint: value.PackageFingerprint,
		TrustClass: skills.PackageTrustOperatorInstalledUntrusted, ToolDependencyCount: len(m.ToolDependencies),
	}}, nil
}

// ValidateSkillSelection rechecks every pinned field against current lifecycle
// state. A durable user selection cannot renew a revoked or changed generation.
func ValidateSkillSelection(value Installation, item skills.ExternalSelectionItem,
	mode domain.RunModeSnapshot, role domain.AgentRole,
) (skills.Manifest, error) {
	source, err := SkillSelectionSource(value)
	if err != nil {
		return skills.Manifest{}, err
	}
	expected := source.Item
	expected.SelectionID, expected.Ordinal, expected.SpecialistEligible = item.SelectionID, item.Ordinal, item.SpecialistEligible
	if !reflect.DeepEqual(expected, item) || mode.Surface != item.Surface ||
		(role != domain.AgentRoleRoot && role != domain.AgentRoleSpecialist) ||
		(role == domain.AgentRoleSpecialist && !item.SpecialistEligible) ||
		!source.Manifest.AllowsInvocation(skills.InvocationSourceUser, true) ||
		!source.Manifest.SupportsContext(skills.ExecutionContext{Surface: mode.Surface, Phase: mode.Phase, Profile: mode.Profile, Role: role}) {
		return skills.Manifest{}, errors.New("Plugin Skill selection no longer matches its component, generation, mode or role")
	}
	return source.Manifest, nil
}
