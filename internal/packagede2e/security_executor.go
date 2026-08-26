package packagede2e

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	maximumCandidateMetadataBytes = 64 << 10
	maximumCandidateBinaryBytes   = 512 << 20
)

type SecurityMatrixOptions struct {
	HarnessRoot      string
	CandidateArchive string
	OutputName       string
}

type SecurityMatrixDriver interface {
	Open(context.Context, SecurityDriverConfig) ([]SecurityBackendEvidence, error)
	Execute(context.Context, SecurityDriverCase) (SecurityCaseBackendEvidence, error)
	Close(context.Context) (SecurityCleanupEvidence, error)
}

type SecurityDriverConfig struct {
	OwnedRuntimeRoot string
	FixtureRoot      string
	Definition       Definition
	Candidate        SecurityCandidateEvidence
}

type SecurityDriverCase struct {
	Attack    AttackCase
	Backend   string
	FixtureID string
	Ordinal   int
}

// RunStandardCodeSecurityMatrix executes every frozen case/backend pair in
// matrix order. A failed or unavailable backend is recorded as a failed,
// unexecuted result; it is never rewritten to pass or skip.
func RunStandardCodeSecurityMatrix(ctx context.Context, options SecurityMatrixOptions,
	driver SecurityMatrixDriver,
) (report StandardCodeSecurityEvidence, resultErr error) {
	if ctx == nil || driver == nil {
		return report, errors.New("security matrix context and product driver are required")
	}
	root, err := validateNewSecurityHarnessRoot(options.HarnessRoot)
	if err != nil {
		return report, err
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return report, fmt.Errorf("create security matrix root: %w", err)
	}
	rootOwned := true
	defer func() {
		if resultErr != nil && rootOwned && report.ReportSHA256 == "" {
			_ = os.RemoveAll(root)
		}
	}()

	executable, err := os.Executable()
	if err != nil {
		return report, fmt.Errorf("resolve packaged executable: %w", err)
	}
	candidate, err := VerifyStandardCodeSecurityCandidate(executable,
		options.CandidateArchive)
	if err != nil {
		return report, err
	}
	definition, err := LoadDefinition()
	if err != nil {
		return report, err
	}
	candidate.FixtureSHA256 = definition.ManifestSHA256
	candidate.AttackMatrixSHA256 = definition.MatrixSHA256

	runtimeRoot := filepath.Join(root, "runtime")
	fixtureRoot := filepath.Join(runtimeRoot, "fixtures")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		return report, fmt.Errorf("create security runtime root: %w", err)
	}
	if _, err := Prepare(ctx, PrepareOptions{OutputRoot: fixtureRoot}); err != nil {
		return report, fmt.Errorf("materialize security fixtures: %w", err)
	}

	report = StandardCodeSecurityEvidence{Candidate: candidate,
		StartedAt: time.Now().UTC()}
	backends, openErr := driver.Open(ctx, SecurityDriverConfig{OwnedRuntimeRoot: runtimeRoot,
		FixtureRoot: fixtureRoot, Definition: definition, Candidate: candidate})
	if openErr != nil {
		return report, fmt.Errorf("open packaged security driver: %w", openErr)
	}
	driverOpened := true
	defer func() {
		if driverOpened {
			closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = driver.Close(closeCtx)
			cancel()
		}
	}()
	report.Backends = append([]SecurityBackendEvidence(nil), backends...)
	backendByName := make(map[string]SecurityBackendEvidence, len(backends))
	for _, backend := range backends {
		backendByName[backend.Backend] = backend
	}

	ordinal := 0
	for _, attack := range definition.AttackMatrix.Cases {
		caseEvidence := SecurityAttackCaseEvidence{ID: attack.ID,
			Category: attack.Category, Phase: attack.Phase,
			ExpectedOutcome: attack.ExpectedOutcome, ExpectedSignal: attack.ExpectedSignal}
		for backendIndex, backend := range attack.Backends {
			ordinal++
			fixtureID := attack.FixtureIDs[backendIndex%len(attack.FixtureIDs)]
			started := time.Now().UTC()
			backendEvidence, available := backendByName[backend]
			if !available || backendEvidence.Availability != SecurityBackendReady {
				caseEvidence.Backends = append(caseEvidence.Backends,
					SecurityCaseBackendEvidence{Backend: backend, FixtureID: fixtureID,
						Status: SecurityEvidenceFailed, ActualOutcome: "propose",
						ActualSignal: "approval_required", ActualExecution: false,
						OperatorCode:   "standard_code.attack.approval_required",
						DiagnosticCode: "backend.unavailable", StartedAt: started,
						CompletedAt: time.Now().UTC()})
				continue
			}
			result, executeErr := driver.Execute(ctx, SecurityDriverCase{Attack: attack,
				Backend: backend, FixtureID: fixtureID, Ordinal: ordinal})
			result.Backend, result.FixtureID = backend, fixtureID
			if result.StartedAt.IsZero() {
				result.StartedAt = started
			}
			if result.CompletedAt.IsZero() {
				result.CompletedAt = time.Now().UTC()
			}
			if executeErr != nil {
				result.Status = SecurityEvidenceFailed
				if result.ActualOutcome == "" {
					result.ActualOutcome = "deny"
				}
				if result.ActualSignal == "" {
					result.ActualSignal = "failed_precondition"
				}
				if result.OperatorCode == "" {
					result.OperatorCode = "standard_code.attack.failed_precondition"
				}
				if result.DiagnosticCode == "" {
					result.DiagnosticCode = "product.execution_failed"
				}
			}
			if result.Status == "" {
				result.Status = SecurityEvidencePassed
			}
			caseEvidence.Backends = append(caseEvidence.Backends, result)
		}
		report.Cases = append(report.Cases, caseEvidence)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cleanup, closeErr := driver.Close(closeCtx)
	cancel()
	driverOpened = false
	report.Cleanup = cleanup
	report.CompletedAt = time.Now().UTC()
	if closeErr != nil {
		return report, fmt.Errorf("close packaged security driver: %w", closeErr)
	}
	if err := os.RemoveAll(runtimeRoot); err != nil {
		return report, fmt.Errorf("remove owned security runtime root: %w", err)
	}
	if _, err := os.Lstat(runtimeRoot); !errors.Is(err, os.ErrNotExist) {
		return report, errors.New("owned security runtime root cleanup was not confirmed")
	}
	if err := FinalizeStandardCodeSecurityEvidence(&report); err != nil {
		return report, err
	}
	outputName := strings.TrimSpace(options.OutputName)
	if outputName == "" {
		outputName = "standard-code-security-evidence.json"
	}
	if filepath.Base(outputName) != outputName || strings.ToLower(filepath.Ext(outputName)) != ".json" {
		return report, errors.New("security evidence output name is invalid")
	}
	if err := WriteStandardCodeSecurityEvidence(filepath.Join(root, outputName), report); err != nil {
		return report, err
	}
	rootOwned = false
	if report.Status != SecurityEvidencePassed {
		return report, errors.New("packaged Standard Code security matrix failed closed")
	}
	return report, nil
}

type portableReleaseMetadata struct {
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

func VerifyStandardCodeSecurityCandidate(executablePath, archivePath string) (
	SecurityCandidateEvidence, error,
) {
	executablePath, err := filepath.Abs(strings.TrimSpace(executablePath))
	if err != nil || strings.TrimSpace(archivePath) == "" {
		return SecurityCandidateEvidence{}, errors.New("packaged candidate paths are required")
	}
	archivePath, err = filepath.Abs(strings.TrimSpace(archivePath))
	if err != nil || filepath.Ext(archivePath) == "" {
		return SecurityCandidateEvidence{}, errors.New("packaged candidate archive path is invalid")
	}
	executableSHA, _, err := hashRegularFile(executablePath, maximumCandidateBinaryBytes)
	if err != nil {
		return SecurityCandidateEvidence{}, fmt.Errorf("hash packaged executable: %w", err)
	}
	archiveSHA, _, err := hashRegularFile(archivePath, maximumCandidateBinaryBytes)
	if err != nil {
		return SecurityCandidateEvidence{}, fmt.Errorf("hash candidate archive: %w", err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return SecurityCandidateEvidence{}, fmt.Errorf("open candidate archive: %w", err)
	}
	defer reader.Close()
	var metadata portableReleaseMetadata
	binaryMatches, metadataFound := false, false
	for _, entry := range reader.File {
		if entry.Name != filepath.ToSlash(entry.Name) || strings.Contains(entry.Name, "../") ||
			strings.HasPrefix(entry.Name, "/") || strings.ContainsRune(entry.Name, 0) {
			return SecurityCandidateEvidence{}, errors.New("candidate archive entry is unsafe")
		}
		switch entry.Name {
		case "TraverseBoard.exe":
			if binaryMatches {
				return SecurityCandidateEvidence{}, errors.New("candidate archive has duplicate executable")
			}
			digest, err := hashZipEntry(entry, maximumCandidateBinaryBytes)
			if err != nil || digest != executableSHA {
				return SecurityCandidateEvidence{}, errors.New("running executable does not match candidate archive")
			}
			binaryMatches = true
		case "release-metadata.json":
			if metadataFound {
				return SecurityCandidateEvidence{}, errors.New("candidate archive has duplicate release metadata")
			}
			raw, err := readZipEntry(entry, maximumCandidateMetadataBytes)
			if err != nil || strictJSON(raw, &metadata) != nil {
				return SecurityCandidateEvidence{}, errors.New("candidate release metadata is invalid")
			}
			metadataFound = true
		}
	}
	if !binaryMatches || !metadataFound || metadata.ProtocolVersion != "portable_release_metadata.v1" ||
		!lowercaseObjectPattern.MatchString(metadata.Revision) || metadata.SourceDateEpoch <= 0 ||
		metadata.Modified || !metadata.ReproducibilityChecked || !metadata.Reproducible ||
		!safeSecurityText(metadata.AppVersion, 128) || !safeSecurityText(metadata.GoVersion, 128) ||
		!safeSecurityText(metadata.NodeVersion, 128) || !safeSecurityText(metadata.NPMVersion, 128) ||
		!safeSecurityText(metadata.RustVersion, 128) ||
		!lowercaseDigestPattern.MatchString(metadata.GoSumSHA256) ||
		!lowercaseDigestPattern.MatchString(metadata.NodeLockSHA256) ||
		!lowercaseDigestPattern.MatchString(metadata.CargoLockSHA256) ||
		!lowercaseDigestPattern.MatchString(metadata.EmbeddedAnalyzerSHA256) ||
		metadata.TargetOS != runtime.GOOS || metadata.TargetArch != runtime.GOARCH ||
		(metadata.CGOEnabled != "0" && metadata.CGOEnabled != "1") || !metadata.Trimpath ||
		metadata.BinaryName != "TraverseBoard.exe" || metadata.SHA256 != executableSHA ||
		!metadata.OperatorPreviewIncluded ||
		metadata.OperatorPreviewLauncherName != "Start-Prayu-Operator-Preview.cmd" ||
		!lowercaseDigestPattern.MatchString(metadata.OperatorPreviewLauncherSHA256) ||
		metadata.LocalTestGuideName != "LOCAL-TEST-GUIDE.txt" ||
		!lowercaseDigestPattern.MatchString(metadata.LocalTestGuideSHA256) ||
		metadata.DefaultUILanguage != "zh-CN" || metadata.InstallerIncluded ||
		metadata.RegistryWrites || metadata.StartupTask || metadata.AutoUpdateEnabled ||
		!metadata.ManualWindows10MatrixRequired {
		return SecurityCandidateEvidence{}, errors.New("candidate archive provenance is incomplete")
	}
	return SecurityCandidateEvidence{SourceCommit: metadata.Revision,
		ExecutableSHA256: executableSHA, ArchiveSHA256: archiveSHA,
		ArchiveName: filepath.Base(archivePath), OperatingSystem: runtime.GOOS,
		OSVersion: securityOSVersion(), Architecture: runtime.GOARCH}, nil
}

func hashRegularFile(path string, maximum int64) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maximum {
		return "", 0, errors.New("file is unavailable, indirect, empty, or oversized")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || written != info.Size() || written > maximum {
		return "", written, errors.New("file changed or exceeded its bound while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func hashZipEntry(entry *zip.File, maximum int64) (string, error) {
	if entry == nil || entry.FileInfo().Mode()&os.ModeSymlink != 0 ||
		entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > uint64(maximum) {
		return "", errors.New("candidate archive entry is invalid")
	}
	file, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || written != int64(entry.UncompressedSize64) || written > maximum {
		return "", errors.New("candidate archive entry size changed")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readZipEntry(entry *zip.File, maximum int64) ([]byte, error) {
	if entry == nil || entry.FileInfo().Mode()&os.ModeSymlink != 0 ||
		entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > uint64(maximum) {
		return nil, errors.New("candidate metadata entry is invalid")
	}
	file, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum || uint64(len(raw)) != entry.UncompressedSize64 {
		return nil, errors.New("candidate metadata entry exceeded its bound")
	}
	return raw, nil
}

func strictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON has trailing data")
	}
	return nil
}

func validateNewSecurityHarnessRoot(value string) (string, error) {
	value = strings.TrimSpace(value)
	root, err := filepath.Abs(value)
	if err != nil || value == "" || filepath.Clean(root) != root ||
		root == filepath.VolumeName(root)+string(filepath.Separator) ||
		!strings.HasPrefix(strings.ToLower(filepath.Base(root)), "standard-code-attack-") {
		return "", errors.New("security matrix root must be a new standard-code-attack-* directory")
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("security matrix root already exists or cannot be inspected")
	}
	parent := filepath.Dir(root)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("security matrix root parent is unavailable or indirect")
	}
	return root, nil
}
