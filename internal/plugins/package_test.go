package plugins

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolgateway"
)

func TestPluginPackageVerifiesSignedInertSkillContents(t *testing.T) {
	manifest, files := pluginSkillFixture(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := SignPackage(manifest, files, privateKey,
		time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := ParsePackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !pkg.SignaturePresent || !pkg.SignatureValid ||
		len(pkg.PublisherFingerprint) != 64 || pkg.Manifest.ID != manifest.ID ||
		pkg.ArchiveSHA256 == "" || pkg.PackageFingerprint == "" {
		t.Fatalf("signed plugin metadata is incomplete: %#v", pkg)
	}
	copyArchive := pkg.Archive()
	copyArchive[0] ^= 0xff
	if pkg.Archive()[0] == copyArchive[0] {
		t.Fatal("plugin archive escaped through a mutable alias")
	}
}

func TestPluginPackageRejectsInvalidSignatureExtraFilesAndExecutableAssets(t *testing.T) {
	manifest, files := pluginSkillFixture(t)
	manifestRaw, _ := json.Marshal(manifest)
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badSignature, _ := json.Marshal(Signature{ProtocolVersion: SignatureProtocolVersion,
		Publisher: manifest.Publisher, Algorithm: "ed25519",
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Signature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))})
	entries := map[string][]byte{ManifestPath: manifestRaw, SignaturePath: badSignature}
	for name, value := range files {
		entries[name] = value
	}
	raw, err := buildZip(entries)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePackage(raw); err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("invalid signature was accepted: %v", err)
	}
	entries["scripts/install.ps1"] = []byte("Write-Host unsafe")
	raw, _ = buildZip(entries)
	if _, err := ParsePackage(raw); err == nil {
		t.Fatal("plugin package accepted an executable file absent from the manifest")
	}
	traversal, _ := buildZip(map[string][]byte{"../plugin.json": manifestRaw})
	if _, err := ParsePackage(traversal); err == nil {
		t.Fatal("plugin package accepted a path traversal entry")
	}
	var executable bytes.Buffer
	writer := zip.NewWriter(&executable)
	header := &zip.FileHeader{Name: ManifestPath, Method: zip.Deflate}
	header.SetMode(0o755)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(manifestRaw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePackage(executable.Bytes()); err == nil ||
		!strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("plugin package accepted an executable-mode entry: %v", err)
	}
}

func TestPluginPackageRejectsMalformedContributedSkill(t *testing.T) {
	manifest, files := pluginSkillFixture(t)
	files["skills/helper/manifest.json"] = []byte(`{"protocol":"skill.v1","name":"different"}`)
	for index := range manifest.Files {
		if manifest.Files[index].Path == "skills/helper/manifest.json" {
			manifest.Files[index].Bytes = len(files[manifest.Files[index].Path])
			manifest.Files[index].SHA256 = packageTestDigest(files[manifest.Files[index].Path])
		}
	}
	raw, err := BuildUnsignedPackage(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePackage(raw); err == nil || !strings.Contains(err.Error(), "Skill") {
		t.Fatalf("malformed contributed Skill was accepted: %v", err)
	}
}

func pluginSkillFixture(t *testing.T) (Manifest, map[string][]byte) {
	t.Helper()
	content := []byte("# Helper\n\nInspect bounded workspace evidence.\n")
	skillManifest := skills.Manifest{Protocol: skills.ProtocolVersion, Name: "helper",
		Version: "1.0.0", Publisher: "fixture.publisher", Description: "Inspect evidence.",
		Profiles: []domain.Profile{domain.ProfileCode},
		Surfaces: []domain.ExecutionSurface{domain.ExecutionSurfaceCode},
		Phases:   []domain.ExecutionPhase{domain.ExecutionPhaseDeliver},
		Roles:    []domain.AgentRole{domain.AgentRoleRoot}, UserInvocable: true,
		ExplicitOnly: true, ToolDependencies: []toolgateway.ToolName{toolgateway.ReadFileTool},
		ContentPath: "SKILL.md", ContentSHA256: packageTestDigest(content),
		ContentBytes: len(content), ContentTokenUpperBound: skills.ContentTokenUpperBound(content)}
	manifestRaw, err := json.Marshal(skillManifest)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"skills/helper/manifest.json": manifestRaw,
		"skills/helper/SKILL.md": content}
	manifest := Manifest{ProtocolVersion: ProtocolVersion, ID: "fixture-plugin",
		Name: "Fixture Plugin", Version: "1.0.0", Publisher: "fixture.publisher",
		Description: "A bounded inert plugin fixture.", Capabilities: []Capability{CapabilitySkills},
		Files: []FileEntry{
			{Path: "skills/helper/manifest.json", SHA256: packageTestDigest(manifestRaw), Bytes: len(manifestRaw)},
			{Path: "skills/helper/SKILL.md", SHA256: packageTestDigest(content), Bytes: len(content)},
		}, Skills: []SkillContribution{{Name: "helper",
			ManifestPath: "skills/helper/manifest.json", ContentPath: "skills/helper/SKILL.md"}}}
	return manifest, files
}

func packageTestDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
