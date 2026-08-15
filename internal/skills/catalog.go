package skills

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
)

// ParsedPackage is the result of accepting either package generation.
type ParsedPackage struct {
	V1 *SkillPackage
	V2 *SignedPackage
}

func (p ParsedPackage) Preview() PackagePreview {
	if p.V2 != nil {
		return p.V2.Package.Preview()
	}
	if p.V1 != nil {
		return p.V1.Preview()
	}
	return PackagePreview{}
}

// ParsePackageAny accepts a v2 signed package or a v1 unsigned package and
// rejects everything else.
func ParsePackageAny(raw []byte) (ParsedPackage, error) {
	if signed, err := ParseSignedPackage(raw); err == nil {
		return ParsedPackage{V2: signed}, nil
	}
	if pkg, err := ParsePackage(raw); err == nil {
		return ParsedPackage{V1: pkg}, nil
	}
	return ParsedPackage{}, errors.New("package is neither a valid v1 nor a valid signed v2 skill package")
}

// Package returns the validated SkillPackage for either generation.
func (p ParsedPackage) Package() *SkillPackage {
	if p.V2 != nil {
		return &p.V2.Package
	}
	return p.V1
}

// UnsignedForm verifies and strips the signature envelope: a signed v2 package
// returns the equivalent deterministic v1 two-entry archive, and a v1 package
// returns its input bytes. The signed archive digest and publisher identity are
// preserved by the catalog import ledger, not by the installation record.
func UnsignedForm(raw []byte) ([]byte, error) {
	signed, err := ParseSignedPackage(raw)
	if err == nil {
		manifestRaw, err := json.Marshal(signed.Package.Manifest())
		if err != nil {
			return nil, err
		}
		return buildDeterministicPackage([]deterministicZipEntry{
			{name: PackageManifestPath, data: manifestRaw},
			{name: PackageContentPath, data: signed.Package.contentBytes()},
		})
	}
	if _, err := ParsePackage(raw); err != nil {
		return nil, errors.New("package is neither a valid v1 nor a valid signed v2 skill package")
	}
	return bytes.Clone(raw), nil
}

// PublisherFingerprintFromBase64 derives the catalog identity of a publisher
// from its base64-encoded Ed25519 public key.
func PublisherFingerprintFromBase64(publicKey string) (string, error) {
	key, err := decodeEd25519Key(publicKey)
	if err != nil {
		return "", err
	}
	return PublisherFingerprint(ed25519.PublicKey(key)), nil
}
