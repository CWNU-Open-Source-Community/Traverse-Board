package packagede2e

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type recordingSecurityDriver struct {
	config SecurityDriverConfig
	cases  []SecurityDriverCase
	closed int
	failAt int
}

func (d *recordingSecurityDriver) Open(_ context.Context,
	config SecurityDriverConfig,
) ([]SecurityBackendEvidence, error) {
	d.config = config
	return []SecurityBackendEvidence{
		{Backend: "local", Availability: SecurityBackendReady,
			IdentitySHA256: strings.Repeat("1", 64), GenerationSHA256: strings.Repeat("2", 64),
			Network: "disabled", Credentials: "none"},
		{Backend: "docker", Availability: SecurityBackendReady,
			IdentitySHA256: strings.Repeat("3", 64), GenerationSHA256: strings.Repeat("4", 64),
			Network: "disabled", Credentials: "none"},
	}, nil
}

func (d *recordingSecurityDriver) Execute(_ context.Context,
	current SecurityDriverCase,
) (SecurityCaseBackendEvidence, error) {
	d.cases = append(d.cases, current)
	result := SecurityCaseBackendEvidence{Status: SecurityEvidencePassed,
		ActualOutcome:  current.Attack.ExpectedOutcome,
		ActualSignal:   current.Attack.ExpectedSignal,
		OperatorCode:   "standard_code.attack." + current.Attack.ExpectedSignal,
		DiagnosticCode: "matrix." + current.Attack.ID, ActualExecution: true,
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}
	sources := map[string]string{"operator_ui": "desktop.projection",
		"immutable_event": "run.event", "workspace_digest": "drydock.observation",
		"process_receipt":     "command_runtime.receipt",
		"network_observation": "sandbox.network", "artifact_digest": "artifact.store",
		"thread_transcript": "thread.projection", "checkpoint": "workspace.checkpoint"}
	for _, kind := range current.Attack.RequiredEvidence {
		result.Evidence = append(result.Evidence, SecurityEvidenceRef{Kind: kind,
			Source: sources[kind], SHA256: strings.Repeat("a", 64)})
	}
	if d.failAt == current.Ordinal {
		result.Status = SecurityEvidenceFailed
		result.ActualExecution = false
		result.Evidence = nil
	}
	return result, nil
}

func (d *recordingSecurityDriver) Close(context.Context) (SecurityCleanupEvidence, error) {
	d.closed++
	return SecurityCleanupEvidence{OwnedRootSHA256: strings.Repeat("b", 64),
		OwnedProcessesStarted: len(d.cases), OwnedProcessesReaped: len(d.cases),
		OwnedDirectoriesOnly: true}, nil
}

func TestSecurityExecutorRunsExactFrozenMatrixAndWritesImmutableEvidence(t *testing.T) {
	archive := packagedSecurityTestArchive(t)
	root := filepath.Join(t.TempDir(), "standard-code-attack-exact")
	driver := &recordingSecurityDriver{}
	report, err := RunStandardCodeSecurityMatrix(t.Context(), SecurityMatrixOptions{
		HarnessRoot: root, CandidateArchive: archive}, driver)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	expectedRuns := 0
	for _, attack := range definition.AttackMatrix.Cases {
		expectedRuns += len(attack.Backends)
	}
	if report.Status != SecurityEvidencePassed || len(driver.cases) != expectedRuns ||
		report.Summary.RequiredCaseCount != 40 ||
		report.Summary.RequiredBackendRuns != expectedRuns ||
		report.Summary.PassedBackendRuns != expectedRuns || driver.closed != 1 {
		t.Fatalf("matrix report=%+v calls=%d close=%d", report.Summary,
			len(driver.cases), driver.closed)
	}
	for index, current := range driver.cases {
		if current.Ordinal != index+1 || current.Attack.ID == "" ||
			(current.Backend != "local" && current.Backend != "docker") ||
			current.FixtureID == "" {
			t.Fatalf("matrix call %d is not exact: %+v", index, current)
		}
	}
	if driver.config.Definition.MatrixSHA256 != definition.MatrixSHA256 ||
		!pathInsideTest(driver.config.OwnedRuntimeRoot, driver.config.FixtureRoot) {
		t.Fatalf("driver config is not frozen/owned: %+v", driver.config)
	}
	evidencePath := filepath.Join(root, "standard-code-security-evidence.json")
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var stored StandardCodeSecurityEvidence
	if err := json.Unmarshal(raw, &stored); err != nil ||
		ValidateStandardCodeSecurityEvidence(stored) != nil ||
		!reflect.DeepEqual(report, stored) {
		t.Fatalf("stored evidence did not round trip: %v", err)
	}
	if err := WriteStandardCodeSecurityEvidence(evidencePath, report); err == nil {
		t.Fatal("immutable evidence file was overwritten")
	}
}

func TestSecurityExecutorCannotTurnUnexecutedCaseIntoPass(t *testing.T) {
	root := filepath.Join(t.TempDir(), "standard-code-attack-fail-closed")
	driver := &recordingSecurityDriver{failAt: 7}
	report, err := RunStandardCodeSecurityMatrix(t.Context(), SecurityMatrixOptions{
		HarnessRoot: root, CandidateArchive: packagedSecurityTestArchive(t)}, driver)
	if err == nil || report.Status != SecurityEvidenceFailed ||
		report.Summary.UnexecutedBackendRuns != 1 ||
		report.Summary.FailedBackendRuns != 1 || driver.closed != 1 {
		t.Fatalf("unexecuted case was not fail closed: status=%s summary=%+v err=%v",
			report.Status, report.Summary, err)
	}
	if _, statErr := os.Stat(filepath.Join(root,
		"standard-code-security-evidence.json")); statErr != nil {
		t.Fatalf("failed immutable evidence was not retained: %v", statErr)
	}
}

func TestSecurityCandidateRejectsScriptLauncherAndPreviewMetadata(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyStandardCodeSecurityCandidate(executable,
		packagedSecurityTestArchive(t)); err != nil {
		t.Fatal(err)
	}
	t.Run("script launcher", func(t *testing.T) {
		archive := packagedSecurityTestArchiveWithOptions(t, packagedSecurityArchiveOptions{
			ExtraFiles: map[string][]byte{"Start-Traverse-Board.cmd": []byte("@echo off\r\n")},
		})
		if _, err := VerifyStandardCodeSecurityCandidate(executable, archive); err == nil {
			t.Fatal("portable archive with a script launcher was accepted")
		}
	})
	t.Run("preview metadata", func(t *testing.T) {
		archive := packagedSecurityTestArchiveWithOptions(t, packagedSecurityArchiveOptions{
			MutateMetadata: func(metadata *portableReleaseMetadata) {
				metadata.OperatorPreviewIncluded = true
				metadata.OperatorPreviewLauncherName = "legacy-preview.cmd"
				metadata.OperatorPreviewLauncherSHA256 = strings.Repeat("d", 64)
			},
		})
		if _, err := VerifyStandardCodeSecurityCandidate(executable, archive); err == nil {
			t.Fatal("operator-preview launcher metadata was accepted")
		}
	})
}

type packagedSecurityArchiveOptions struct {
	ExtraFiles     map[string][]byte
	MutateMetadata func(*portableReleaseMetadata)
}

func packagedSecurityTestArchive(t *testing.T) string {
	return packagedSecurityTestArchiveWithOptions(t, packagedSecurityArchiveOptions{})
}

func packagedSecurityTestArchiveWithOptions(t *testing.T,
	options packagedSecurityArchiveOptions,
) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executableSHA, _, err := hashRegularFile(executable, maximumCandidateBinaryBytes)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "TraverseBoard-test.zip")
	file, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	guide := []byte("Packaged test guide\r\n")
	guideDigest := sha256.Sum256(guide)
	metadataValue := portableReleaseMetadata{
		ProtocolVersion: "portable_release_metadata.v1", AppVersion: "security-test",
		Revision: strings.Repeat("c", 40), SourceDateEpoch: 1,
		GoVersion: "go1.26.5", NodeVersion: "v24.14.0", NPMVersion: "11.9.0",
		RustVersion: "1.97.1", GoSumSHA256: strings.Repeat("d", 64),
		NodeLockSHA256: strings.Repeat("d", 64), CargoLockSHA256: strings.Repeat("d", 64),
		EmbeddedAnalyzerSHA256: strings.Repeat("d", 64), TargetOS: runtime.GOOS,
		TargetArch: runtime.GOARCH, CGOEnabled: "0", Trimpath: true,
		BinaryName: "TraverseBoard.exe", SHA256: executableSHA, OperatorPreviewIncluded: false,
		OperatorPreviewLauncherName: "", OperatorPreviewLauncherSHA256: "",
		LocalTestGuideName:   "LOCAL-TEST-GUIDE.txt",
		LocalTestGuideSHA256: hex.EncodeToString(guideDigest[:]),
		DefaultUILanguage:    "zh-CN", ReproducibilityChecked: true, Reproducible: true,
		ManualWindows10MatrixRequired: true}
	if options.MutateMetadata != nil {
		options.MutateMetadata(&metadataValue)
	}
	metadata, err := json.Marshal(metadataValue)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"TraverseBoard.exe":     binary,
		"LOCAL-TEST-GUIDE.txt":  guide,
		"LICENSE":               []byte("test license\n"),
		"README.md":             []byte("test readme\n"),
		"NOTICE":                []byte("test notice\n"),
		"sbom.json":             []byte(`{"bomFormat":"CycloneDX"}`),
		"release-metadata.json": metadata,
	}
	names := append([]string(nil), securityCandidateArchiveEntries[:]...)
	for name, content := range options.ExtraFiles {
		files[name] = content
		names = append(names, name)
	}
	writer := zip.NewWriter(file)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func pathInsideTest(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
