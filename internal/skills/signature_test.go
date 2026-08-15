package skills

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

func mustSignFixture(t *testing.T, manifest Manifest, content []byte) ([]byte, ed25519.PrivateKey) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := SignPackage(manifest, content, privateKey, time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return raw, privateKey
}

func TestParseSignedPackageRoundTrip(t *testing.T) {
	content := []byte("# Signed review helper\n\nEvidence, not authority.\n")
	manifest := fixtureManifest(content)
	manifest.Name = "signed-review"
	manifest.Publisher = "ctf-blue-team"
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := SignPackage(manifest, content, privateKey, time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSignedPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.SignatureValid {
		t.Fatal("valid signature was not accepted")
	}
	if parsed.PublisherFingerprint != PublisherFingerprint(publicKey) {
		t.Fatalf("publisher fingerprint = %q, want key fingerprint", parsed.PublisherFingerprint)
	}
	preview := parsed.Package.Preview()
	if preview.ProtocolVersion != SignedPackageProtocolVersion || preview.EntryCount != SignedPackageEntryCount ||
		preview.TrustClass != PackageTrustSignedUntrusted || preview.Manifest.Publisher != manifest.Publisher {
		t.Fatalf("unexpected signed preview: %#v", preview)
	}
	if len(preview.PackageFingerprint) != 64 || len(preview.ArchiveSHA256) != 64 {
		t.Fatal("signed package digests are missing")
	}
	if !bytes.Equal(parsed.Package.contentBytes(), content) {
		t.Fatal("signed content was not retained exactly")
	}
}

func TestParseSignedPackageRejectsTamperingAndStaleSignatures(t *testing.T) {
	content := []byte("# Tamper target\n")
	manifest := fixtureManifest(content)
	manifest.Name = "tamper-target"
	manifest.Publisher = "ctf-blue-team"
	raw, _ := mustSignFixture(t, manifest, content)
	parsed, err := ParseSignedPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	// One flipped byte anywhere must break either the container or the signature.
	flipped := bytes.Clone(raw)
	flipped[len(flipped)/2] ^= 0xff
	if _, err := ParseSignedPackage(flipped); err == nil {
		t.Fatal("byte-flipped archive was accepted")
	}
	// Altered content carrying the original signature block must fail verification.
	altered := []byte("# Altered content\n")
	alteredManifest := fixtureManifest(altered)
	alteredManifest.Name = "tamper-target"
	alteredManifest.Publisher = "ctf-blue-team"
	manifestRaw, err := json.Marshal(alteredManifest)
	if err != nil {
		t.Fatal(err)
	}
	signatureRaw, err := json.Marshal(parsed.Signature)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := buildDeterministicPackage([]deterministicZipEntry{
		{name: PackageManifestPath, data: manifestRaw},
		{name: PackageContentPath, data: altered},
		{name: PackageSignaturePath, data: signatureRaw},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSignedPackage(stale); err == nil {
		t.Fatal("package with a stale signature was accepted")
	}
}

func TestParseSignedPackageRejectsMismatchedPublisherAndMalformedSignature(t *testing.T) {
	content := []byte("# Publisher mismatch\n")
	manifest := fixtureManifest(content)
	manifest.Name = "publisher-mismatch"
	manifest.Publisher = "ctf-blue-team"
	raw, _ := mustSignFixture(t, manifest, content)
	parsed, err := ParseSignedPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// Publisher in the signature block does not match the manifest publisher.
	mismatched := parsed.Signature
	mismatched.Publisher = "someone-else"
	signatureRaw, err := json.Marshal(mismatched)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := buildDeterministicPackage([]deterministicZipEntry{
		{name: PackageManifestPath, data: manifestRaw},
		{name: PackageContentPath, data: content},
		{name: PackageSignaturePath, data: signatureRaw},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSignedPackage(pkg); err == nil {
		t.Fatal("publisher mismatch was accepted")
	}
	// Unknown signature field must be rejected by the strict decoder.
	if _, err := decodePackageSignature([]byte("{\"protocol\":\"skill_package_signature.v1\",\"publisher\":\"p\",\"public_key\":\"k\",\"algorithm\":\"ed25519\",\"signed_at\":\"\",\"signature\":\"s\",\"extra\":true}")); err == nil {
		t.Fatal("unknown signature field was accepted")
	}
}

func TestSignedPackageFingerprintIgnoresSignedAt(t *testing.T) {
	content := []byte("# Stable fingerprint\n")
	manifest := fixtureManifest(content)
	manifest.Name = "stable-fingerprint"
	manifest.Publisher = "ctf-blue-team"
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	first, err := SignPackage(manifest, content, privateKey, time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	second, err := SignPackage(manifest, content, privateKey, time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	left, err := ParseSignedPackage(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := ParseSignedPackage(second)
	if err != nil {
		t.Fatal(err)
	}
	if left.Package.Preview().PackageFingerprint != right.Package.Preview().PackageFingerprint {
		t.Fatal("package fingerprint drifted with signed_at")
	}
}

func TestSignPackageRejectsInvalidInputs(t *testing.T) {
	content := []byte("# Invalid\n")
	manifest := fixtureManifest(content)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignPackage(manifest, content, privateKey, time.Now().UTC()); err == nil {
		t.Fatal("unsigned manifest (no publisher) was signed")
	}
	manifest.Publisher = "publisher with spaces"
	if _, err := SignPackage(manifest, content, privateKey, time.Now().UTC()); err == nil {
		t.Fatal("invalid publisher was signed")
	}
}
