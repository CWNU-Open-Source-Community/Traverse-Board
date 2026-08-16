package skills

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	SignedPackageProtocolVersion   = "skill_package.v2"
	SignatureProtocolVersion       = "skill_package_signature.v1"
	PackageSignaturePath           = "SIGNATURE.json"
	SignatureAlgorithm             = "ed25519"
	MaxSignatureBytes              = 4 * 1024
	SignedPackageEntryCount        = 3
	SignedPackageUncompressedBytes = MaxManifestBytes + MaxContentBytes + MaxSignatureBytes
)

// PackageSignature is the detached-adjacent signature block of a v2 package.
// It covers the exact manifest and content bytes inside the deterministic ZIP
// container: signature = Ed25519(sk, sha256(manifest.json) || sha256(SKILL.md)).
// SignedAt is informational only and is excluded from package fingerprints.
type PackageSignature struct {
	Protocol  string `json:"protocol"`
	Publisher string `json:"publisher"`
	PublicKey string `json:"public_key"`
	Algorithm string `json:"algorithm"`
	SignedAt  string `json:"signed_at"`
	Signature string `json:"signature"`
}

// SignedPackage is an immutable, validated in-memory v2 package. Verification
// is cryptographic only: a valid signature never grants trust. Trust is a
// separate user/admin catalog decision.
type SignedPackage struct {
	Package              SkillPackage
	Signature            PackageSignature
	SignatureValid       bool
	PublisherFingerprint string
}

// ParseSignedPackage validates the complete deterministic ZIP container before
// decoding the manifest, content, and signature. It never writes files or
// executes code. A v2 package without a valid signature is rejected outright;
// trust (and therefore selectability) is still a catalog decision.
func ParseSignedPackage(raw []byte) (*SignedPackage, error) {
	if len(raw) == 0 || len(raw) > MaxPackageArchiveBytes {
		return nil, fmt.Errorf("invalid signed skill package: archive must contain between 1 and %d bytes", MaxPackageArchiveBytes)
	}
	records, uncompressedBytes, err := inspectPackageContainer(raw,
		[]string{PackageManifestPath, PackageContentPath, PackageSignaturePath},
		[]uint32{uint32(MaxManifestBytes), uint32(MaxContentBytes), uint32(MaxSignatureBytes)},
		uint64(SignedPackageUncompressedBytes))
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: %w", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: open ZIP: %w", err)
	}
	if len(reader.File) != SignedPackageEntryCount {
		return nil, fmt.Errorf("invalid signed skill package: ZIP contains %d entries, want %d", len(reader.File), SignedPackageEntryCount)
	}
	manifestRaw, err := readPackageEntry(reader.File[0], records[0], MaxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: %w", err)
	}
	content, err := readPackageEntry(reader.File[1], records[1], MaxContentBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: %w", err)
	}
	signatureRaw, err := readPackageEntry(reader.File[2], records[2], MaxSignatureBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: %w", err)
	}
	manifest, err := decodeManifest(manifestRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: manifest: %w", err)
	}
	if manifest.ContentPath != PackageContentPath {
		return nil, fmt.Errorf("invalid signed skill package: manifest content_path must be %q", PackageContentPath)
	}
	if err := manifest.Validate(content); err != nil {
		return nil, fmt.Errorf("invalid signed skill package: manifest: %w", err)
	}
	signature, err := decodePackageSignature(signatureRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: signature: %w", err)
	}
	if err := validatePublisher(manifest.Publisher); err != nil {
		return nil, fmt.Errorf("invalid signed skill package: manifest: %w", err)
	}
	if signature.Publisher != manifest.Publisher {
		return nil, errors.New("invalid signed skill package: signature publisher does not match manifest publisher")
	}
	signatureValid, fingerprint, err := verifyPackageSignature(manifestRaw, content, signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: %w", err)
	}
	if !signatureValid {
		return nil, errors.New("invalid signed skill package: signature verification failed")
	}
	packageFingerprint, err := signedPackageFingerprint(manifest, content, signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signed skill package: canonicalize: %w", err)
	}
	archiveDigest := sha256.Sum256(raw)
	return &SignedPackage{
		Package: SkillPackage{
			preview: PackagePreview{
				ProtocolVersion:    SignedPackageProtocolVersion,
				Manifest:           cloneManifest(manifest),
				ArchiveSHA256:      hex.EncodeToString(archiveDigest[:]),
				PackageFingerprint: packageFingerprint,
				ArchiveBytes:       len(raw),
				UncompressedBytes:  uncompressedBytes,
				EntryCount:         SignedPackageEntryCount,
				TrustClass:         PackageTrustSignedUntrusted,
				RiskCodes: []PackageRiskCode{
					PackageRiskUntrustedInstructions,
					PackageRiskDeclaredToolsOnly,
				},
			},
			content: bytes.Clone(content),
		},
		Signature:            signature,
		SignatureValid:       true,
		PublisherFingerprint: fingerprint,
	}, nil
}

func decodePackageSignature(raw []byte) (PackageSignature, error) {
	if len(raw) == 0 || len(raw) > MaxSignatureBytes {
		return PackageSignature{}, fmt.Errorf("signature must contain between 1 and %d bytes", MaxSignatureBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var signature PackageSignature
	if err := decoder.Decode(&signature); err != nil {
		return PackageSignature{}, fmt.Errorf("decode strict JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PackageSignature{}, errors.New("signature contains trailing JSON data")
	}
	if signature.Protocol != SignatureProtocolVersion {
		return PackageSignature{}, fmt.Errorf("unsupported signature protocol %q", signature.Protocol)
	}
	if signature.Algorithm != SignatureAlgorithm {
		return PackageSignature{}, fmt.Errorf("unsupported signature algorithm %q", signature.Algorithm)
	}
	if err := validatePublisher(signature.Publisher); err != nil {
		return PackageSignature{}, err
	}
	if _, err := decodeEd25519Key(signature.PublicKey); err != nil {
		return PackageSignature{}, fmt.Errorf("public_key: %w", err)
	}
	if _, err := decodeEd25519Signature(signature.Signature); err != nil {
		return PackageSignature{}, fmt.Errorf("signature: %w", err)
	}
	if signature.SignedAt != "" {
		if _, err := time.Parse(time.RFC3339, signature.SignedAt); err != nil {
			return PackageSignature{}, errors.New("signed_at must be an RFC3339 timestamp when present")
		}
	}
	return signature, nil
}

func decodeEd25519Key(value string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("must be standard base64 of a 32-byte Ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

func decodeEd25519Signature(value string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, errors.New("must be standard base64 of a 64-byte Ed25519 signature")
	}
	return raw, nil
}

// signedMessage is the exact byte string every package signature covers.
func signedMessage(manifestRaw, content []byte) []byte {
	manifestDigest := sha256.Sum256(manifestRaw)
	contentDigest := sha256.Sum256(content)
	message := make([]byte, 0, sha256.Size*2)
	message = append(message, manifestDigest[:]...)
	message = append(message, contentDigest[:]...)
	return message
}

func verifyPackageSignature(manifestRaw, content []byte, signature PackageSignature) (bool, string, error) {
	publicKey, err := decodeEd25519Key(signature.PublicKey)
	if err != nil {
		return false, "", err
	}
	signatureBytes, err := decodeEd25519Signature(signature.Signature)
	if err != nil {
		return false, "", err
	}
	fingerprint := PublisherFingerprint(publicKey)
	return ed25519.Verify(publicKey, signedMessage(manifestRaw, content), signatureBytes), fingerprint, nil
}

// PublisherFingerprint is the stable catalog identity for a publisher: the
// SHA-256 of the Ed25519 public key, hex-encoded.
func PublisherFingerprint(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}

// signatureCanonical is the fingerprint-stable subset of the signature block.
// SignedAt is deliberately excluded so re-signing the same bytes at another
// time cannot change the package fingerprint.
type signatureCanonical struct {
	Protocol  string `json:"protocol"`
	Publisher string `json:"publisher"`
	PublicKey string `json:"public_key"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

func signedPackageFingerprint(manifest Manifest, content []byte, signature PackageSignature) (string, error) {
	canonicalManifest, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	canonicalSignature, err := json.Marshal(signatureCanonical{
		Protocol: signature.Protocol, Publisher: signature.Publisher,
		PublicKey: signature.PublicKey, Algorithm: signature.Algorithm,
		Signature: signature.Signature,
	})
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(SignedPackageProtocolVersion))
	_, _ = hash.Write([]byte{0})
	writePackageFrame(hash, canonicalManifest)
	writePackageFrame(hash, content)
	writePackageFrame(hash, canonicalSignature)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// SignPackage creates a v2 package from a validated manifest, content, and an
// Ed25519 private key. It exists for publisher tooling and tests; the running
// server never signs packages and never has access to a publisher key.
func SignPackage(manifest Manifest, content []byte, privateKey ed25519.PrivateKey, signedAt time.Time) ([]byte, error) {
	if err := manifest.Validate(content); err != nil {
		return nil, fmt.Errorf("sign package: %w", err)
	}
	if manifest.ContentPath != PackageContentPath {
		return nil, errors.New("sign package: manifest content_path must be SKILL.md")
	}
	if err := validatePublisher(manifest.Publisher); err != nil {
		return nil, fmt.Errorf("sign package: %w", err)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("sign package: private key must be a 64-byte Ed25519 seed")
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("sign package: marshal manifest: %w", err)
	}
	signatureBytes := ed25519.Sign(privateKey, signedMessage(manifestRaw, content))
	signature := PackageSignature{
		Protocol:  SignatureProtocolVersion,
		Publisher: manifest.Publisher,
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Algorithm: SignatureAlgorithm,
		SignedAt:  signedAt.UTC().Format(time.RFC3339),
		Signature: base64.StdEncoding.EncodeToString(signatureBytes),
	}
	signatureRaw, err := json.Marshal(signature)
	if err != nil {
		return nil, fmt.Errorf("sign package: marshal signature: %w", err)
	}
	return buildDeterministicPackage([]deterministicZipEntry{
		{name: PackageManifestPath, data: manifestRaw},
		{name: PackageContentPath, data: content},
		{name: PackageSignaturePath, data: signatureRaw},
	})
}
