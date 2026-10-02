package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/toolcontract"
)

func (i Installation) PackageID() string {
	if i.Snapshot != nil {
		return i.Snapshot.PackageID
	}
	return i.Manifest.ID
}
func (i Installation) Revision() string {
	if i.Snapshot != nil {
		return i.Snapshot.Revision
	}
	return i.Manifest.Version
}
func (i Installation) DisplayName() string {
	if i.Snapshot != nil {
		return i.Snapshot.Name
	}
	return i.Manifest.Name
}
func (i Installation) Capabilities() []Capability {
	if i.Snapshot != nil {
		// Portable runtime contributions are enabled only when their real host
		// wiring exists. Reading source instructions never enables MCP or scripts.
		if len(i.Snapshot.Skills) != 0 {
			return []Capability{CapabilitySkills}
		}
		return nil
	}
	return slices.Clone(i.Manifest.Capabilities)
}

func snapshotFingerprint(snapshot PortableSnapshot) string {
	raw, _ := json.Marshal(snapshot)
	return digest(append([]byte(PortableInstallationProtocol+"\x00"), raw...))
}

func (i Installation) validateDescription() error {
	switch i.ProtocolVersion {
	case InstallationProtocol:
		if i.Snapshot != nil || i.Source.Surface != "" || i.Source.OperationKeyDigest != "" || i.Source.Kind == "local_directory" {
			return errors.New("legacy installation cannot carry portable scope")
		}
		return i.Manifest.Validate()
	case PortableInstallationProtocol:
		if i.Snapshot == nil || i.Snapshot.Validate() != nil || !reflect.DeepEqual(i.Manifest, Manifest{}) ||
			i.ArchiveSHA256 != i.Snapshot.Revision || i.PackageFingerprint != snapshotFingerprint(*i.Snapshot) ||
			i.Source.Surface == "" || !validDigest(i.Source.OperationKeyDigest) || i.ID != "plugin-import-"+i.Source.OperationKeyDigest || i.SignaturePresent || i.SignatureValid || i.PublisherFingerprint != "" || i.PublisherPublicKey != "" ||
			i.Snapshot.Source.URI != i.Source.URI || i.Snapshot.Source.Revision != i.Source.Commit {
			return errors.New("portable installation description is invalid")
		}
		return nil
	default:
		return errors.New("plugin installation version is unsupported")
	}
}

// StageDirectory uses the existing installation, review and object retention
// lifecycle. Acquisition grants no capability; callers explicitly review and
// enable the selected contribution through Service.Review.
func (s *Service) StageDirectory(ctx context.Context, directory, packageID string,
	source InstallSource, supersedes, actor, scratchRoot string,
) (Installation, bool, error) {
	if !validText(actor, 256, false) || !validDigest(source.OperationKeyDigest) || (source.Surface != "code" && source.Surface != "cyber") {
		return Installation{}, false, apperror.New(apperror.CodeInvalidArgument, "portable staging needs an actor and selected surface")
	}
	pkg, err := CapturePortableDirectory(ctx, directory, packageID,
		toolcontract.SourceRef{URI: source.URI, Revision: source.Commit}, scratchRoot)
	if err != nil {
		return Installation{}, false, err
	}
	source.SHA256 = pkg.Snapshot.Revision
	value := Installation{ProtocolVersion: PortableInstallationProtocol, ID: "plugin-import-" + source.OperationKeyDigest,
		Snapshot: &pkg.Snapshot, Source: source, ArchiveSHA256: source.SHA256,
		PackageFingerprint: snapshotFingerprint(pkg.Snapshot), ArchiveBytes: len(pkg.raw),
		State: StateStaged, EnabledCapabilities: []Capability{}, Generation: 1,
		SupersedesInstallationID: strings.TrimSpace(supersedes), StagedBy: actor,
		CreatedAt: s.now().UTC()}
	value.UpdatedAt = value.CreatedAt
	if err := value.Validate(); err != nil {
		return Installation{}, false, err
	}
	return s.store.CreatePluginInstallation(ctx, value, pkg.raw)
}
