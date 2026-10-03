package plugins

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/toolgateway"
)

func TestLegacySkillSnapshotRetainsSignedBytesAndRejectsDescriptionDrift(t *testing.T) {
	body := []byte("# Original instructions\r\n\r\nKeep these bytes.\r\n")
	manifest := skills.BindManifestContent(skills.Manifest{Protocol: skills.ProtocolVersion,
		Name: "legacy-original", Version: "1.2.3", Description: "Original legacy instructions",
		Profiles: []domain.Profile{domain.ProfileCode}, ToolDependencies: []toolgateway.ToolName{toolgateway.ReadFileTool}}, body)
	manifest.Publisher = "example-publisher"
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := skills.SignPackage(manifest, body, private, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := CaptureLegacySkill(t.Context(), raw, "legacy-original", toolcontract.SourceRef{URI: "fixture", SHA256: digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pkg.Archive(), raw) || pkg.Snapshot.Legacy == nil || len(pkg.Snapshot.Inventory) != 3 {
		t.Fatal("legacy signature or archive was rewritten")
	}
	reader, err := OpenPortableSnapshot(t.Context(), pkg.Snapshot, raw, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := pkg.Snapshot.Skills[0].Instructions
	got, actual, err := reader.Read(t.Context(), ref.Component, "", len(body))
	if err != nil || actual != ref || !bytes.Equal(got, body) {
		t.Fatalf("original read=%q ref=%+v err=%v", got, actual, err)
	}
	got[0] = 'x'
	got, _, err = reader.Read(t.Context(), ref.Component, "", len(body))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("caller changed retained bytes")
	}
	for _, resource := range []string{"SKILL.md", "../manifest.json", "SIGNATURE.json"} {
		if _, _, err := reader.Read(t.Context(), ref.Component, resource, len(body)); err == nil {
			t.Fatal("legacy archive exposed an undeclared resource")
		}
	}
	if _, _, err := reader.Read(t.Context(), ref.Component, "", len(body)-1); err == nil {
		t.Fatal("read exceeded limit")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := reader.Read(cancelled, ref.Component, "", len(body)); err == nil {
		t.Fatal("cancelled read succeeded")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Read(t.Context(), ref.Component, "", len(body)); err == nil {
		t.Fatal("closed reader returned instructions")
	}
	changed := pkg.Snapshot
	changed.Legacy = new(skills.PackagePreview)
	*changed.Legacy = *pkg.Snapshot.Legacy
	changed.Legacy.Manifest.Description = "Altered description"
	if _, err := OpenPortableSnapshot(t.Context(), changed, raw, t.TempDir()); err == nil {
		t.Fatal("accepted metadata drift")
	}
	raw[len(raw)/2] ^= 1
	if _, err := OpenPortableSnapshot(t.Context(), pkg.Snapshot, raw, t.TempDir()); err == nil {
		t.Fatal("accepted changed signed bytes")
	}
}
