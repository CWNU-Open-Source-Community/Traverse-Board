package application

import (
	"context"
	"reflect"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolgateway"
)

// Explicit delivery uses the existing persisted Run selection, never an
// invocation flag supplied by a tool call or the installation's declarations.
func selectedPortableSkill(ctx context.Context, source any, value plugins.Installation, pin toolgateway.SkillReadRequest,
	mode domain.RunModeSnapshot, role domain.AgentRole,
) (skills.ExternalSelectionItem, skills.Manifest, error) {
	deny := func() (skills.ExternalSelectionItem, skills.Manifest, error) {
		return skills.ExternalSelectionItem{}, skills.Manifest{}, apperror.New(apperror.CodePolicyDenied, "Plugin Skill is not explicitly selected for this Run and role")
	}
	store, ok := source.(interface {
		GetExternalSkillSelectionByRun(context.Context, string) (skills.ExternalSelection, bool, error)
		GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	})
	if !ok || mode.RunID == "" {
		return deny()
	}
	current, err := store.GetRunMode(ctx, mode.RunID)
	if err != nil {
		return skills.ExternalSelectionItem{}, skills.Manifest{}, err
	}
	if current.ID != mode.ID || current.Revision != mode.Revision || !reflect.DeepEqual(current, mode) {
		return deny()
	}
	selection, found, err := store.GetExternalSkillSelectionByRun(ctx, mode.RunID)
	if err != nil {
		return skills.ExternalSelectionItem{}, skills.Manifest{}, err
	}
	if !found || selection.Validate() != nil || selection.RunID != mode.RunID || selection.MissionID != mode.MissionID || selection.Surface != mode.Surface || selection.Profile != mode.Profile {
		return deny()
	}
	for _, item := range selection.Items {
		if item.Plugin == nil || item.InstallationID != pin.InstallationID || item.Plugin.PackageID != pin.PackageID ||
			item.Plugin.ComponentID != pin.ComponentID || item.Plugin.Revision != pin.Revision || item.Plugin.Generation != pin.InstallationGeneration {
			continue
		}
		manifest, err := plugins.ValidateSkillSelection(value, item, mode, role)
		if err != nil {
			return deny()
		}
		return item, manifest, nil
	}
	return deny()
}

// Both Root and Specialist keep the legacy loader and context assembly. Only
// the source-specific verified read is adapted to the common Plugin snapshot.
type selectedSkillLoader struct {
	skills.PackageObjectLoader
	source any
	mode   domain.RunModeSnapshot
	role   domain.AgentRole
}

func (l selectedSkillLoader) LoadSelectedSkill(ctx context.Context, item skills.ExternalSelectionItem) (skills.Manifest, []byte, error) {
	store, ok := l.source.(portableSkillReadStore)
	if !ok || item.Plugin == nil {
		return skills.Manifest{}, nil, apperror.New(apperror.CodeFailedPrecondition, "selected Plugin reader is unavailable")
	}
	pin := toolgateway.SkillReadRequest{InstallationID: item.InstallationID, PackageID: item.Plugin.PackageID,
		ComponentID: item.Plugin.ComponentID, Revision: item.Plugin.Revision, InstallationGeneration: item.Plugin.Generation}
	var installed plugins.Installation
	var manifest skills.Manifest
	recheck := func() error {
		value, err := store.GetPluginInstallation(ctx, item.InstallationID)
		if err != nil {
			return err
		}
		selected, currentManifest, err := selectedPortableSkill(ctx, l.source, value, pin, l.mode, l.role)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(selected, item) {
			return apperror.New(apperror.CodeConflict, "selected Plugin component binding changed")
		}
		installed, manifest = value, currentManifest
		return nil
	}
	if err := recheck(); err != nil {
		return skills.Manifest{}, nil, err
	}
	body, _, err := readPortableSkillContent(ctx, store, installed, pin, recheck)
	return manifest, body, err
}
