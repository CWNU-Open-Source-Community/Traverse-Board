package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

	"cyberagent-workbench/internal/agentpackages"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
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
		var capabilities []Capability
		if len(i.Snapshot.Skills) != 0 {
			capabilities = append(capabilities, CapabilitySkills)
		}
		// This is a source contribution candidate, not executable authority.
		// StageMCPServers loads the exact object and admits only declarations
		// accepted by the native codec; instruction import enables Skills only.
		if i.Snapshot.Format == agentpackages.FormatAgentPlugin && slices.ContainsFunc(i.Snapshot.Inventory, func(e SnapshotEntry) bool {
			return e.Path == "mcp.json" && e.Kind == "file"
		}) {
			capabilities = append(capabilities, CapabilityMCP)
		}
		return capabilities
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
		if i.Snapshot.Legacy != nil {
			return i.Snapshot.Legacy.Manifest.ValidateInstallationSurface(domain.ExecutionSurface(i.Source.Surface))
		}
		return nil
	default:
		return errors.New("plugin installation version is unsupported")
	}
}

// StageSnapshot admits an acquired package through the Plugin installation
// lifecycle. All descriptions are re-derived from its bytes;
// the caller's snapshot is neither a capability nor trusted parsing evidence.
func (s *Service) StageSnapshot(ctx context.Context, snapshot PortableSnapshot, raw []byte,
	source InstallSource, supersedes, actor, scratchRoot string,
) (Installation, bool, error) {
	if !validText(actor, 256, false) || !validDigest(source.OperationKeyDigest) || (source.Surface != "code" && source.Surface != "cyber") {
		return Installation{}, false, apperror.New(apperror.CodeInvalidArgument, "portable staging needs an actor and selected surface")
	}
	raw = slices.Clone(raw)
	reader, err := OpenPortableSnapshot(ctx, snapshot, raw, scratchRoot)
	if err != nil {
		return Installation{}, false, apperror.Wrap(apperror.CodeInvalidArgument, "acquired Plugin snapshot failed validation", err)
	}
	pkg := PortablePackage{Snapshot: reader.snapshot, raw: raw}
	if err := reader.Close(); err != nil {
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
		return Installation{}, false, apperror.Wrap(apperror.CodeInvalidArgument, "Plugin installation failed validation", err)
	}
	return s.store.CreatePluginInstallation(ctx, value, pkg.raw)
}
