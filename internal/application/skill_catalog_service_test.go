package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

func newCatalogFixture(t *testing.T) (*SkillCatalogService, *skills.Registry) {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	builtins, err := skills.BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	objects, err := skills.NewLocalPackageObjectStore(home)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewSkillPackageRegistryService(st, objects, builtins)
	return NewSkillCatalogService(st, registry), builtins
}

func TestSkillCatalogServiceTrustRevokeAndPin(t *testing.T) {
	ctx := context.Background()
	service, _ := newCatalogFixture(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyB64 := base64.StdEncoding.EncodeToString(publicKey)
	if err := service.PinVersion(ctx, PinSkillVersionRequest{SkillName: "scan-aid",
		Version: "1.0.0", Surface: domain.ExecutionSurfaceCode, Actor: "admin"}); err == nil {
		t.Fatal("pinned a skill before installation")
	}
	content := []byte("# Signed catalog skill\n")
	manifest := buildCatalogManifest(content, "scan-aid", "1.0.0", "ctf-blue-team")
	signedRaw, err := skills.SignPackage(manifest, content, privateKey, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := skills.ParseSignedPackage(signedRaw)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestRaw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	signatureRaw, _ := json.Marshal(parsed.Signature)
	if err := os.WriteFile(filepath.Join(dir, "SIGNATURE.json"), signatureRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	imported, err := service.ImportFromDirectory(ctx, ImportSkillFromDirectoryRequest{
		Directory: dir, Surface: domain.ExecutionSurfaceCode, OperationKey: "op-1-signed-dir-import",
		InstalledBy: "admin", ConfirmUntrusted: true,
	})
	if err != nil {
		t.Fatalf("import signed directory: %v", err)
	}
	if !imported.Signed || imported.Import.PublisherFingerprint == "" || imported.Import.SourceKind != "local" {
		t.Fatalf("unexpected import result: %#v", imported)
	}
	if err := service.PinVersion(ctx, PinSkillVersionRequest{SkillName: "scan-aid",
		Version: "1.0.0", Surface: domain.ExecutionSurfaceCode, Actor: "admin"}); err == nil {
		t.Fatal("pinned a signed skill with an untrusted publisher")
	}
	trusted, err := service.TrustPublisher(ctx, TrustSkillPublisherRequest{
		Name: "ctf-blue-team", PublicKey: publicKeyB64, Actor: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if trusted.Fingerprint != imported.Import.PublisherFingerprint || trusted.TrustClass != "trusted" {
		t.Fatalf("unexpected trusted publisher: %#v", trusted)
	}
	if err := service.PinVersion(ctx, PinSkillVersionRequest{SkillName: "scan-aid",
		Version: "1.0.0", Surface: domain.ExecutionSurfaceCode, Actor: "admin"}); err != nil {
		t.Fatalf("pin trusted skill: %v", err)
	}
	if err := service.SetEnabled(ctx, SetSkillEnabledRequest{SkillName: "scan-aid",
		Surface: domain.ExecutionSurfaceCode, Enabled: false, Actor: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetEnabled(ctx, SetSkillEnabledRequest{SkillName: "scan-aid",
		Surface: domain.ExecutionSurfaceCode, Enabled: true, Actor: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokePublisher(ctx, RevokeSkillPublisherRequest{
		Fingerprint: trusted.Fingerprint, Actor: "admin"}); err != nil {
		t.Fatal(err)
	}
	publishers, err := service.ListPublishers(ctx)
	if err != nil || len(publishers) != 1 || publishers[0].TrustClass != "revoked" {
		t.Fatalf("unexpected publishers after revoke: %#v err=%v", publishers, err)
	}
	audit, err := service.ListAudit(ctx, 50)
	if err != nil || len(audit) < 5 {
		t.Fatalf("catalog audit trail too short: %d err=%v", len(audit), err)
	}
}

func TestSkillCatalogServiceImportFromURLPinsBytes(t *testing.T) {
	ctx := context.Background()
	service, _ := newCatalogFixture(t)
	content := []byte("# URL skill\n")
	manifest := buildCatalogManifest(content, "url-aid", "1.0.0", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestRaw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := skills.BuildPackageFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	pin := hex.EncodeToString(digest[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(raw)
	}))
	defer server.Close()
	service.HTTPClient = server.Client()
	imported, err := service.ImportFromURL(ctx, ImportSkillFromURLRequest{
		URL: server.URL + "/pkg.zip", SHA256: pin, Surface: domain.ExecutionSurfaceCode,
		OperationKey: "op-url-import-0001", InstalledBy: "admin", ConfirmUntrusted: true,
	})
	if err != nil {
		t.Fatalf("import from URL: %v", err)
	}
	if imported.Signed || imported.Import.SourceKind != "url" || imported.Import.Pin != pin {
		t.Fatalf("unexpected URL import: %#v", imported)
	}
	if _, err := service.ImportFromURL(ctx, ImportSkillFromURLRequest{
		URL: server.URL + "/pkg.zip", SHA256: pin[:len(pin)-1] + "0", Surface: domain.ExecutionSurfaceCode,
		OperationKey: "op-url-import-0002", InstalledBy: "admin", ConfirmUntrusted: true,
	}); err == nil {
		t.Fatal("imported URL bytes that drift from the pin")
	}
}

func buildCatalogManifest(content []byte, name, version, publisher string) skills.Manifest {
	digest := sha256.Sum256(content)
	return skills.Manifest{
		Protocol:               skills.ProtocolVersion,
		Name:                   name,
		Version:                version,
		Publisher:              publisher,
		Description:            "Catalog fixture skill.",
		Profiles:               []domain.Profile{domain.ProfileCode},
		ToolDependencies:       []toolgateway.ToolName{},
		ContentPath:            "SKILL.md",
		ContentSHA256:          hex.EncodeToString(digest[:]),
		ContentBytes:           len(content),
		ContentTokenUpperBound: skills.ContentTokenUpperBound(content),
	}
}
