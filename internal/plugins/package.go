package plugins

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
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/skills"
)

func ParsePackage(raw []byte) (Package, error) {
	if len(raw) < 1 || len(raw) > MaxArchiveBytes {
		return Package{}, fmt.Errorf("plugin archive must contain between 1 and %d bytes", MaxArchiveBytes)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return Package{}, fmt.Errorf("open plugin ZIP: %w", err)
	}
	if len(reader.File) < 1 || len(reader.File) > MaxEntries+2 {
		return Package{}, errors.New("plugin ZIP entry count is invalid")
	}
	contents := make(map[string][]byte, len(reader.File))
	total := 0
	for _, file := range reader.File {
		if !validPackagePath(file.Name) || file.FileInfo().IsDir() ||
			file.Mode()&os.ModeSymlink != 0 || file.Mode().Perm()&0o111 != 0 ||
			file.UncompressedSize64 > MaxUncompressedBytes {
			return Package{}, fmt.Errorf("plugin ZIP entry %q is unsafe", file.Name)
		}
		if _, found := contents[file.Name]; found {
			return Package{}, fmt.Errorf("plugin ZIP repeats %q", file.Name)
		}
		limit := int64(MaxUncompressedBytes - total + 1)
		entry, err := readZipFile(file, limit)
		if err != nil {
			return Package{}, err
		}
		total += len(entry)
		if total > MaxUncompressedBytes {
			return Package{}, errors.New("plugin uncompressed content exceeds its limit")
		}
		contents[file.Name] = entry
	}
	manifestRaw, found := contents[ManifestPath]
	if !found || len(manifestRaw) < 2 || len(manifestRaw) > MaxManifestBytes {
		return Package{}, errors.New("plugin manifest is missing or oversized")
	}
	manifest, err := decodeManifest(manifestRaw)
	if err != nil {
		return Package{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Package{}, err
	}
	expectedEntries := len(manifest.Files) + 1
	_, signaturePresent := contents[SignaturePath]
	if signaturePresent {
		expectedEntries++
	}
	if len(contents) != expectedEntries {
		return Package{}, errors.New("plugin ZIP contains files absent from its signed manifest")
	}
	for _, file := range manifest.Files {
		value, found := contents[file.Path]
		if !found || len(value) != file.Bytes || digest(value) != file.SHA256 {
			return Package{}, fmt.Errorf("plugin file %q does not match its manifest", file.Path)
		}
	}
	if err := validateContributionContents(manifest, contents); err != nil {
		return Package{}, err
	}
	packageFingerprint := packageFingerprint(manifestRaw, manifest.Files)
	pkg := Package{Manifest: manifest, ArchiveSHA256: digest(raw),
		PackageFingerprint: packageFingerprint, ArchiveBytes: len(raw),
		UncompressedBytes: total, SignaturePresent: signaturePresent, raw: slices.Clone(raw)}
	if signaturePresent {
		signatureRaw := contents[SignaturePath]
		if len(signatureRaw) < 2 || len(signatureRaw) > MaxSignatureBytes {
			return Package{}, errors.New("plugin signature is oversized")
		}
		signature, err := decodeSignature(signatureRaw)
		if err != nil {
			return Package{}, err
		}
		if signature.Publisher != manifest.Publisher {
			return Package{}, errors.New("plugin signature publisher does not match the manifest")
		}
		publicKey, err := base64.StdEncoding.DecodeString(signature.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return Package{}, errors.New("plugin signature public key is invalid")
		}
		signatureBytes, err := base64.StdEncoding.DecodeString(signature.Signature)
		if err != nil || len(signatureBytes) != ed25519.SignatureSize {
			return Package{}, errors.New("plugin signature bytes are invalid")
		}
		if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(packageFingerprint), signatureBytes) {
			return Package{}, errors.New("plugin signature verification failed")
		}
		pkg.SignatureValid = true
		pkg.PublisherFingerprint = digest(publicKey)
		pkg.PublisherPublicKey = signature.PublicKey
	}
	return pkg, nil
}

func validateContributionContents(manifest Manifest, contents map[string][]byte) error {
	for _, contribution := range manifest.Skills {
		manifestRaw, content := contents[contribution.ManifestPath], contents[contribution.ContentPath]
		if len(manifestRaw) == 0 || len(manifestRaw) > skills.MaxManifestBytes ||
			len(content) == 0 || len(content) > skills.MaxContentBytes {
			return fmt.Errorf("plugin Skill %q content is missing or oversized", contribution.Name)
		}
		decoder := json.NewDecoder(bytes.NewReader(manifestRaw))
		decoder.DisallowUnknownFields()
		var skillManifest skills.Manifest
		if err := decoder.Decode(&skillManifest); err != nil {
			return fmt.Errorf("plugin Skill %q manifest is invalid: %w", contribution.Name, err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return fmt.Errorf("plugin Skill %q manifest contains trailing JSON", contribution.Name)
		}
		if skillManifest.Name != contribution.Name ||
			skillManifest.ContentPath != path.Base(contribution.ContentPath) ||
			(skillManifest.Publisher != "" && skillManifest.Publisher != manifest.Publisher) {
			return fmt.Errorf("plugin Skill %q identity does not match its contribution", contribution.Name)
		}
		if err := skillManifest.Validate(content); err != nil {
			return fmt.Errorf("plugin Skill %q failed validation: %w", contribution.Name, err)
		}
	}
	for name, value := range contents {
		switch {
		case strings.HasPrefix(name, "docs/") && strings.HasSuffix(name, ".md"):
			if !inertUTF8(value) {
				return fmt.Errorf("plugin documentation %q is not inert UTF-8", name)
			}
		case strings.HasPrefix(name, "ui/") && strings.HasSuffix(name, ".json"):
			if len(value) == 0 || !utf8.Valid(value) || !json.Valid(value) {
				return fmt.Errorf("plugin UI metadata %q is invalid JSON", name)
			}
		case strings.HasPrefix(name, "ui/") && strings.HasSuffix(name, ".png"):
			if len(value) < 8 || !bytes.Equal(value[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
				return fmt.Errorf("plugin UI asset %q is not a PNG", name)
			}
		case strings.HasPrefix(name, "ui/") && strings.HasSuffix(name, ".webp"):
			if len(value) < 12 || string(value[:4]) != "RIFF" || string(value[8:12]) != "WEBP" {
				return fmt.Errorf("plugin UI asset %q is not a WebP image", name)
			}
		}
	}
	return nil
}

func inertUTF8(value []byte) bool {
	if len(value) == 0 || !utf8.Valid(value) {
		return false
	}
	for _, current := range string(value) {
		if current == 0 || (unicode.IsControl(current) && current != '\n' &&
			current != '\r' && current != '\t') {
			return false
		}
	}
	return true
}

func decodeManifest(raw []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value Manifest
	if err := decoder.Decode(&value); err != nil {
		return Manifest{}, fmt.Errorf("decode strict plugin manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("plugin manifest contains trailing JSON")
	}
	return value, nil
}

func decodeSignature(raw []byte) (Signature, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value Signature
	if err := decoder.Decode(&value); err != nil {
		return Signature{}, fmt.Errorf("decode strict plugin signature: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Signature{}, errors.New("plugin signature contains trailing JSON")
	}
	if value.ProtocolVersion != SignatureProtocolVersion || value.Algorithm != "ed25519" ||
		!validText(value.Publisher, 256, false) {
		return Signature{}, errors.New("plugin signature metadata is invalid")
	}
	if value.SignedAt != "" {
		if _, err := time.Parse(time.RFC3339, value.SignedAt); err != nil {
			return Signature{}, errors.New("plugin signature timestamp is invalid")
		}
	}
	return value, nil
}

func SignPackage(manifest Manifest, files map[string][]byte, privateKey ed25519.PrivateKey,
	signedAt time.Time,
) ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("plugin signing key is invalid")
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	fingerprint := packageFingerprint(manifestRaw, manifest.Files)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	signatureRaw, err := json.Marshal(Signature{ProtocolVersion: SignatureProtocolVersion,
		Publisher: manifest.Publisher, Algorithm: "ed25519",
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(fingerprint))),
		SignedAt:  signedAt.UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	entries := map[string][]byte{ManifestPath: manifestRaw, SignaturePath: signatureRaw}
	for _, file := range manifest.Files {
		value, found := files[file.Path]
		if !found || len(value) != file.Bytes || digest(value) != file.SHA256 {
			return nil, fmt.Errorf("plugin signing file %q does not match the manifest", file.Path)
		}
		entries[file.Path] = value
	}
	return buildZip(entries)
}

func BuildUnsignedPackage(manifest Manifest, files map[string][]byte) ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	entries := map[string][]byte{ManifestPath: manifestRaw}
	for _, file := range manifest.Files {
		value, found := files[file.Path]
		if !found || len(value) != file.Bytes || digest(value) != file.SHA256 {
			return nil, fmt.Errorf("plugin file %q does not match the manifest", file.Path)
		}
		entries[file.Path] = value
	}
	return buildZip(entries)
}

func buildZip(entries map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		header.SetModTime(time.Unix(0, 0).UTC())
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(entries[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > MaxArchiveBytes {
		return nil, errors.New("plugin archive exceeds its limit")
	}
	return buffer.Bytes(), nil
}

func readZipFile(file *zip.File, limit int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	value, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) >= limit {
		return nil, errors.New("plugin ZIP entry exceeds the uncompressed limit")
	}
	return value, nil
}

func packageFingerprint(manifestRaw []byte, files []FileEntry) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(ProtocolVersion))
	_, _ = hash.Write([]byte{0})
	manifestDigest := sha256.Sum256(manifestRaw)
	_, _ = hash.Write(manifestDigest[:])
	for _, file := range files {
		_, _ = io.WriteString(hash, file.Path)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, file.SHA256)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, fmt.Sprint(file.Bytes))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func validPackagePath(value string) bool {
	return value != "" && len(value) <= 512 && value == strings.TrimSpace(value) &&
		!strings.Contains(value, "\\") && !strings.ContainsRune(value, 0) &&
		!strings.HasPrefix(value, "/") && path.Clean(value) == value &&
		value != "." && !strings.HasPrefix(value, "../") &&
		!strings.Contains(value, "/../")
}

func allowedPackagePath(value string) bool {
	if strings.HasPrefix(value, "skills/") {
		return strings.HasSuffix(value, "/manifest.json") || strings.HasSuffix(value, "/SKILL.md")
	}
	if strings.HasPrefix(value, "ui/") {
		return strings.HasSuffix(value, ".png") || strings.HasSuffix(value, ".webp") ||
			strings.HasSuffix(value, ".json")
	}
	return strings.HasPrefix(value, "docs/") && strings.HasSuffix(value, ".md")
}
