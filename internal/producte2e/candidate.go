package producte2e

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/packagede2e"
)

const maximumCandidateFileBytes int64 = 2 << 30
const maximumCandidateMetadataBytes int64 = 4 << 20

type CandidateOptions struct {
	BinaryPath           string
	ZipPath              string
	PortableManifestPath string
	ReleaseMetadataPath  string
	FixtureReportPath    string
	ExpectedRevision     string
}

type portableManifest struct {
	ProtocolVersion           string          `json:"protocol_version"`
	ZipName                   string          `json:"zip_name"`
	ZipSHA256                 string          `json:"zip_sha256"`
	BinarySHA256              string          `json:"binary_sha256"`
	SBOMSHA256                string          `json:"sbom_sha256"`
	NoticeSHA256              string          `json:"notice_sha256"`
	Version                   string          `json:"version"`
	Revision                  string          `json:"revision"`
	ZipReproducibilityChecked bool            `json:"zip_reproducibility_checked"`
	ZipTimestampsReproducible bool            `json:"zip_timestamps_reproducible"`
	Contents                  []string        `json:"contents"`
	Entries                   []portableEntry `json:"entries"`
}

type portableEntry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type releaseMetadata struct {
	ProtocolVersion               string `json:"protocol_version"`
	AppVersion                    string `json:"app_version"`
	Revision                      string `json:"revision"`
	SourceDateEpoch               int64  `json:"source_date_epoch"`
	Modified                      bool   `json:"modified"`
	GoVersion                     string `json:"go_version"`
	NodeVersion                   string `json:"node_version"`
	NPMVersion                    string `json:"npm_version"`
	RustVersion                   string `json:"rust_version"`
	GoSumSHA256                   string `json:"go_sum_sha256"`
	NodeLockSHA256                string `json:"node_lock_sha256"`
	CargoLockSHA256               string `json:"cargo_lock_sha256"`
	EmbeddedAnalyzerSHA256        string `json:"embedded_analyzer_sha256"`
	TargetOS                      string `json:"target_os"`
	TargetArch                    string `json:"target_arch"`
	CGOEnabled                    string `json:"cgo_enabled"`
	Trimpath                      bool   `json:"trimpath"`
	BinaryName                    string `json:"binary_name"`
	SHA256                        string `json:"sha256"`
	OperatorPreviewIncluded       bool   `json:"operator_preview_included"`
	OperatorPreviewLauncherName   string `json:"operator_preview_launcher_name"`
	OperatorPreviewLauncherSHA256 string `json:"operator_preview_launcher_sha256"`
	LocalTestGuideName            string `json:"local_test_guide_name"`
	LocalTestGuideSHA256          string `json:"local_test_guide_sha256"`
	DefaultUILanguage             string `json:"default_ui_language"`
	ReproducibilityChecked        bool   `json:"reproducibility_checked"`
	Reproducible                  bool   `json:"reproducible"`
	InstallerIncluded             bool   `json:"installer_included"`
	RegistryWrites                bool   `json:"registry_writes"`
	StartupTask                   bool   `json:"startup_task"`
	AutoUpdateEnabled             bool   `json:"auto_update_enabled"`
	ManualWindows10MatrixRequired bool   `json:"manual_windows_10_matrix_required"`
}

func ValidateCandidate(options CandidateOptions) (CandidateEvidence, FixtureEvidence, error) {
	paths := []string{options.BinaryPath, options.ZipPath, options.PortableManifestPath,
		options.ReleaseMetadataPath, options.FixtureReportPath}
	resolved := make([]string, len(paths))
	for index, value := range paths {
		path, err := regularFile(value)
		if err != nil {
			return CandidateEvidence{}, FixtureEvidence{}, err
		}
		resolved[index] = path
	}
	if parent := filepath.Dir(resolved[0]); filepath.Dir(resolved[1]) != parent ||
		filepath.Dir(resolved[2]) != parent || filepath.Dir(resolved[3]) != parent {
		return CandidateEvidence{}, FixtureEvidence{},
			errors.New("candidate binary, ZIP, manifest, and metadata must share one directory")
	}
	manifestBytes, err := readBoundedFile(resolved[2], maximumCandidateMetadataBytes)
	if err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	metadataBytes, err := readBoundedFile(resolved[3], maximumCandidateMetadataBytes)
	if err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	fixtureBytes, err := readBoundedFile(resolved[4], maximumCandidateMetadataBytes)
	if err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	var manifest portableManifest
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, fmt.Errorf("portable manifest: %w", err)
	}
	var metadata releaseMetadata
	if err := decodeStrictJSON(metadataBytes, &metadata); err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, fmt.Errorf("release metadata: %w", err)
	}
	var fixture packagede2e.FixtureSetReport
	if err := decodeStrictJSON(fixtureBytes, &fixture); err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, fmt.Errorf("fixture report: %w", err)
	}
	if err := packagede2e.ValidateFixtureSetReport(fixture); err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, fmt.Errorf("fixture report: %w", err)
	}
	definition, err := packagede2e.LoadDefinition()
	if err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	if !fixture.OracleVerified || fixture.ManifestSHA256 != definition.ManifestSHA256 ||
		fixture.AttackMatrixSHA256 != definition.MatrixSHA256 {
		return CandidateEvidence{}, FixtureEvidence{},
			errors.New("fixture report is not the current real four-language oracle")
	}
	reports := make(map[string]packagede2e.FixtureRepositoryReport,
		len(fixture.Repositories))
	for _, report := range fixture.Repositories {
		reports[report.ID] = report
	}
	for _, repository := range definition.Manifest.Repositories {
		report, found := reports[repository.ID]
		if !found || report.Language != repository.Language ||
			report.Head != repository.ExpectedHead || report.Tree != repository.ExpectedTree ||
			report.FileCount != len(repository.Files) {
			return CandidateEvidence{}, FixtureEvidence{},
				fmt.Errorf("fixture report repository %q does not match the frozen oracle", repository.ID)
		}
	}
	binaryHash, binarySize, err := fileDigest(resolved[0])
	if err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	zipHash, zipSize, err := fileDigest(resolved[1])
	if err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	expectedRevision := strings.TrimSpace(options.ExpectedRevision)
	if expectedRevision != "" && (!validGitObject(expectedRevision) ||
		manifest.Revision != expectedRevision) {
		return CandidateEvidence{}, FixtureEvidence{}, errors.New("candidate revision does not match")
	}
	if err := validateCandidateMetadata(manifest, metadata, binaryHash, zipHash,
		filepath.Base(resolved[0]), filepath.Base(resolved[1]), metadataBytes); err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	if err := validatePortableArchive(resolved[1], manifest, metadataBytes, binaryHash); err != nil {
		return CandidateEvidence{}, FixtureEvidence{}, err
	}
	candidate := CandidateEvidence{Version: manifest.Version, Revision: manifest.Revision,
		BinarySHA256: binaryHash, BinarySizeBytes: binarySize,
		ZipSHA256: zipHash, ZipSizeBytes: zipSize,
		ManifestSHA256:        digestBytes(manifestBytes),
		ReleaseMetadataSHA256: digestBytes(metadataBytes)}
	fixtureEvidence := FixtureEvidence{ProtocolVersion: fixture.ProtocolVersion,
		ReportSHA256:       digestBytes(fixtureBytes),
		ManifestSHA256:     fixture.ManifestSHA256,
		AttackMatrixSHA256: fixture.AttackMatrixSHA256,
		RepositoryCount:    fixture.RepositoryCount, OracleVerified: fixture.OracleVerified}
	return candidate, fixtureEvidence, nil
}

func validatePortableArchive(path string, manifest portableManifest, metadataBytes []byte,
	binaryHash string,
) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open portable ZIP: %w", err)
	}
	defer archive.Close()
	if len(archive.File) != len(manifest.Entries) {
		return errors.New("portable ZIP entry count does not match its manifest")
	}
	expected := make(map[string]portableEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		expected[entry.Name] = entry
	}
	seen := make(map[string]bool, len(archive.File))
	var totalSize uint64
	for _, entry := range archive.File {
		name := entry.Name
		manifestEntry, found := expected[name]
		if !found || seen[name] || filepath.Base(name) != name ||
			strings.ContainsAny(name, `\:`) || entry.FileInfo().IsDir() ||
			entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > uint64(maximumCandidateFileBytes) ||
			entry.UncompressedSize64 != uint64(manifestEntry.Size) {
			return fmt.Errorf("portable ZIP entry %q is unsafe or does not match its manifest", name)
		}
		totalSize += entry.UncompressedSize64
		if totalSize > uint64(maximumCandidateFileBytes) {
			return errors.New("portable ZIP total uncompressed size exceeds its bound")
		}
		stream, openErr := entry.Open()
		if openErr != nil {
			return fmt.Errorf("open portable ZIP entry %q: %w", name, openErr)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, io.LimitReader(stream, maximumCandidateFileBytes+1))
		closeErr := stream.Close()
		if copyErr != nil || closeErr != nil || size != manifestEntry.Size ||
			hex.EncodeToString(hash.Sum(nil)) != manifestEntry.SHA256 {
			return fmt.Errorf("portable ZIP entry %q digest or size differs", name)
		}
		seen[name] = true
	}
	if len(seen) != len(expected) || expected["TraverseBoard.exe"].SHA256 != binaryHash ||
		expected["release-metadata.json"].SHA256 != digestBytes(metadataBytes) {
		return errors.New("portable ZIP is not bound to the extracted executable and metadata")
	}
	return nil
}

func validateCandidateMetadata(manifest portableManifest, metadata releaseMetadata,
	binaryHash, zipHash, binaryName, zipName string, metadataBytes []byte,
) error {
	if manifest.ProtocolVersion != "portable_zip_manifest.v1" ||
		metadata.ProtocolVersion != "portable_release_metadata.v1" ||
		manifest.ZipName != zipName || binaryName != "TraverseBoard.exe" ||
		manifest.ZipSHA256 != zipHash || manifest.BinarySHA256 != binaryHash ||
		metadata.SHA256 != binaryHash || manifest.Version != metadata.AppVersion ||
		manifest.Revision != metadata.Revision || !validGitObject(manifest.Revision) ||
		!validText(manifest.Version, 128) || metadata.SourceDateEpoch <= 0 ||
		metadata.Modified || !manifest.ZipReproducibilityChecked ||
		!manifest.ZipTimestampsReproducible || !metadata.ReproducibilityChecked ||
		!metadata.Reproducible || !metadata.Trimpath || metadata.TargetOS != "windows" ||
		metadata.TargetArch != "amd64" || metadata.BinaryName != "TraverseBoard.exe" ||
		metadata.OperatorPreviewIncluded || metadata.OperatorPreviewLauncherName != "" ||
		metadata.OperatorPreviewLauncherSHA256 != "" ||
		metadata.DefaultUILanguage != "zh-CN" ||
		metadata.LocalTestGuideName != "LOCAL-TEST-GUIDE.txt" ||
		metadata.InstallerIncluded || metadata.RegistryWrites || metadata.StartupTask ||
		metadata.AutoUpdateEnabled || !metadata.ManualWindows10MatrixRequired {
		return errors.New("candidate provenance or safe portable metadata is invalid")
	}
	for _, value := range []string{manifest.SBOMSHA256, manifest.NoticeSHA256,
		metadata.GoSumSHA256, metadata.NodeLockSHA256, metadata.CargoLockSHA256,
		metadata.EmbeddedAnalyzerSHA256, metadata.LocalTestGuideSHA256} {
		if !validDigest(value) {
			return errors.New("candidate dependency or companion digest is invalid")
		}
	}
	for _, value := range []string{metadata.GoVersion, metadata.NodeVersion,
		metadata.NPMVersion, metadata.RustVersion, metadata.CGOEnabled,
		metadata.LocalTestGuideName} {
		if !validText(value, 256) {
			return errors.New("candidate toolchain or companion identity is invalid")
		}
	}
	expectedContents := []string{"TraverseBoard.exe", "LOCAL-TEST-GUIDE.txt",
		"LICENSE", "README.md", "NOTICE", "sbom.json",
		"release-metadata.json"}
	if !sameOrderedStrings(manifest.Contents, expectedContents) ||
		len(manifest.Entries) != len(expectedContents) {
		return errors.New("portable ZIP content set is invalid")
	}
	entries := map[string]portableEntry{}
	for _, entry := range manifest.Entries {
		if !contains(expectedContents, entry.Name) || entries[entry.Name].Name != "" ||
			entry.Size <= 0 || !validDigest(entry.SHA256) {
			return errors.New("portable ZIP entry is invalid")
		}
		entries[entry.Name] = entry
	}
	if entries["TraverseBoard.exe"].SHA256 != binaryHash ||
		entries["release-metadata.json"].SHA256 != digestBytes(metadataBytes) ||
		entries["sbom.json"].SHA256 != manifest.SBOMSHA256 ||
		entries["NOTICE"].SHA256 != manifest.NoticeSHA256 ||
		entries["LOCAL-TEST-GUIDE.txt"].SHA256 != metadata.LocalTestGuideSHA256 {
		return errors.New("portable ZIP entry hashes are not bound to candidate metadata")
	}
	return nil
}

func sameOrderedStrings(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func regularFile(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("candidate evidence path is required")
	}
	path, err := filepath.Abs(trimmed)
	if err != nil || filepath.Clean(path) != path {
		return "", errors.New("candidate evidence path is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maximumCandidateFileBytes {
		return "", fmt.Errorf("candidate evidence file %q is unavailable or invalid", filepath.Base(path))
	}
	return path, nil
}

func fileDigest(path string) (string, int64, error) {
	return fileDigestLimit(path, maximumCandidateFileBytes)
}

func fileDigestLimit(path string, limit int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil || size <= 0 || size > limit {
		return "", 0, errors.New("candidate evidence file size is invalid")
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > limit {
		return nil, fmt.Errorf("evidence file %q exceeds its bound", filepath.Base(path))
	}
	return os.ReadFile(path)
}

func decodeStrictJSON(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value is not allowed")
		}
		return err
	}
	return nil
}
