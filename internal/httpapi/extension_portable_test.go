package httpapi

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/plugins"
)

func TestExtensionPortableSnapshotViewKeepsOptionalMetadata(t *testing.T) {
	for _, version := range []string{"", "1.0.0"} {
		value := plugins.Installation{ProtocolVersion: plugins.PortableInstallationProtocol, ID: "installation",
			Snapshot: &plugins.PortableSnapshot{PackageID: "host-package", Name: "native-name", AuthorVersion: version, Revision: strings.Repeat("a", 64), Format: "agent-skills", Skills: []plugins.SnapshotSkill{{Name: "native-name"}}},
			Source:   plugins.InstallSource{Surface: "code"}, State: plugins.StateDisabled, Generation: 4}
		view := ProjectPluginInstallation(value)
		if view.Manifest.ID != "host-package" || view.Manifest.Name != "native-name" || view.Manifest.Version != version || view.Manifest.Publisher != "" || view.Snapshot == nil || view.Snapshot.Revision != value.Revision() || view.Snapshot.Surface != "code" || len(view.Manifest.Capabilities) != 1 || view.Manifest.Capabilities[0] != "skills" || view.State != "disabled" || view.Generation != 4 {
			t.Fatalf("portable review view lost identity or invented metadata: %+v", view)
		}
	}
}
