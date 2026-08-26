package producte2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/packagede2e"
)

func TestRunbookRequiresClosedProductMatrix(t *testing.T) {
	runbook := validRunbook()
	if err := runbook.Validate(); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(runbook)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRunbook(content); err != nil {
		t.Fatal(err)
	}
	unknown := append(content[:len(content)-1], []byte(`,"unexpected":true}`)...)
	if _, err := DecodeRunbook(unknown); err == nil {
		t.Fatal("unknown runbook field was accepted")
	}
	trailing := append(append([]byte(nil), content...), []byte(` {}`)...)
	if _, err := DecodeRunbook(trailing); err == nil {
		t.Fatal("trailing runbook value was accepted")
	}

	changed := validRunbook()
	changed.Backends[0].Runs[0].Projections[0].Status = "stale"
	if err := changed.Validate(); err == nil {
		t.Fatal("stale Desktop projection was accepted")
	}
	changed = validRunbook()
	changed.DefaultLaunch.DangerFullAccessEnabled = true
	if err := changed.Validate(); err == nil {
		t.Fatal("danger-full-access default launch was accepted")
	}
	changed = validRunbook()
	changed.Platforms = changed.Platforms[:3]
	if err := changed.Validate(); err == nil {
		t.Fatal("incomplete Windows/DPI matrix was accepted")
	}
	changed = validRunbook()
	changed.Edges = changed.Edges[:len(changed.Edges)-1]
	if err := changed.Validate(); err == nil {
		t.Fatal("missing edge evidence was accepted")
	}
	changed = validRunbook()
	changed.Backends[1].Fallback = nil
	if err := changed.Validate(); err == nil {
		t.Fatal("silent unavailable backend was accepted")
	}
}

func TestEvidenceFilesBindLaunchAndEveryManualProjection(t *testing.T) {
	root := t.TempDir()
	runbook := validRunbook()
	launch := launchRecord{ProtocolVersion: "standard_code_product_launch.v1",
		CandidateSHA256:  runbook.CandidateSHA256,
		ExecutableSHA256: runbook.CandidateSHA256, Arguments: []string{},
		ProcessID: 4242, StartedAt: time.Now().UTC()}
	launchBytes, err := json.Marshal(launch)
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence := func(path string, content []byte) string {
		t.Helper()
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(path)), content)
		return digestBytes(content)
	}
	runbook.DefaultLaunch.EvidenceSHA256 = writeEvidence(
		runbook.DefaultLaunch.EvidencePath, launchBytes)
	for backendIndex := range runbook.Backends {
		backend := &runbook.Backends[backendIndex]
		if backend.Fallback != nil {
			backend.Fallback.ReadinessEvidenceSHA = writeEvidence(
				backend.Fallback.ReadinessEvidencePath, []byte("docker readiness unavailable"))
			backend.Fallback.UIEvidenceSHA256 = writeEvidence(
				backend.Fallback.UIEvidencePath, []byte("explicit Approval UI"))
		}
		for runIndex := range backend.Runs {
			for projectionIndex := range backend.Runs[runIndex].Projections {
				projection := &backend.Runs[runIndex].Projections[projectionIndex]
				projection.EvidenceSHA256 = writeEvidence(projection.EvidencePath,
					[]byte(projection.Surface+" durable projection"))
			}
		}
	}
	for index := range runbook.Continuity {
		evidence := &runbook.Continuity[index]
		evidence.EvidenceSHA256 = writeEvidence(evidence.EvidencePath,
			[]byte(evidence.Case+" composer continuity"))
	}
	for index := range runbook.Platforms {
		evidence := &runbook.Platforms[index]
		evidence.EvidenceSHA256 = writeEvidence(evidence.EvidencePath,
			[]byte(evidence.ID+" IME accessibility"))
	}
	if err := runbook.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := validateEvidenceFiles(root, runbook); err != nil {
		t.Fatal(err)
	}

	projection := &runbook.Backends[0].Runs[0].Projections[0]
	writeTestFile(t, filepath.Join(root, filepath.FromSlash(projection.EvidencePath)),
		[]byte("tampered projection"))
	if err := validateEvidenceFiles(root, runbook); err == nil {
		t.Fatal("tampered projection capture was accepted")
	}
	projection.EvidenceSHA256 = writeEvidence(projection.EvidencePath,
		[]byte(projection.Surface+" durable projection"))
	runbook.Platforms[0].EvidencePath = runbook.Continuity[0].EvidencePath
	runbook.Platforms[0].EvidenceSHA256 = runbook.Continuity[0].EvidenceSHA256
	if err := validateEvidenceFiles(root, runbook); err == nil {
		t.Fatal("reused evidence path was accepted for two product claims")
	}
	runbook.Platforms[0].EvidencePath = "windows_10/repaired.png"
	runbook.Platforms[0].EvidenceSHA256 = writeEvidence(runbook.Platforms[0].EvidencePath,
		[]byte("repaired platform evidence"))
	launch.Arguments = []string{"--operator-preview"}
	launchBytes, err = json.Marshal(launch)
	if err != nil {
		t.Fatal(err)
	}
	runbook.DefaultLaunch.EvidenceSHA256 = writeEvidence(
		runbook.DefaultLaunch.EvidencePath, launchBytes)
	if err := validateEvidenceFiles(root, runbook); err == nil {
		t.Fatal("non-zero-argument launch record was accepted")
	}
}

func TestReportSealRejectsTamperingAndIncompleteTruth(t *testing.T) {
	report := validReport()
	sealed, err := report.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Status != "pass" || !validDigest(sealed.EvidenceSHA256) {
		t.Fatalf("unexpected sealed report: %#v", sealed)
	}
	tampered := sealed
	tampered.Scenarios[0].FailedJobs = 0
	if err := tampered.Validate(); err == nil {
		t.Fatal("report without a real failed Job was accepted")
	}
	sealed.Scenarios[0].FailedJobs = 1
	tampered = sealed
	tampered.Candidate.BinarySHA256 = strings.Repeat("c", 64)
	if err := tampered.Validate(); err == nil {
		t.Fatal("candidate hash tampering was accepted")
	}
	tampered = sealed
	tampered.Safeguards.SkipAccepted = true
	if err := tampered.Validate(); err == nil {
		t.Fatal("skip was accepted as product evidence")
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := WriteReport(path, sealed); err != nil {
		t.Fatal(err)
	}
	if err := WriteReport(path, sealed); err == nil {
		t.Fatal("product report overwrite was accepted")
	}
}

func TestValidateCandidateBindsExactPortableFilesAndOracle(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "TraverseBoard.exe")
	zipPath := filepath.Join(root, "Prayu-portable-v0.1.0-test-windows-amd64.zip")
	metadataPath := filepath.Join(root, "release-metadata.json")
	manifestPath := filepath.Join(root, "portable-zip-manifest.json")
	fixturePath := filepath.Join(root, "fixture-set.json")
	binaryBytes := []byte("real-candidate-placeholder")
	writeTestFile(t, binaryPath, binaryBytes)
	binaryHash, _, err := fileDigest(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	revision := strings.Repeat("b", 40)
	launcher := []byte("@echo off\r\n")
	guide := []byte("Packaged test guide\r\n")
	metadata := releaseMetadata{ProtocolVersion: "portable_release_metadata.v1",
		AppVersion: "v0.1.0-test", Revision: revision, SourceDateEpoch: 946684800,
		GoVersion: "go1.25.12", NodeVersion: "v24.16.0", NPMVersion: "11.0.0",
		RustVersion: "rustc 1.97.1", GoSumSHA256: digest, NodeLockSHA256: digest,
		CargoLockSHA256: digest, EmbeddedAnalyzerSHA256: digest, TargetOS: "windows",
		TargetArch: "amd64", CGOEnabled: "1", Trimpath: true,
		BinaryName: "TraverseBoard.exe", SHA256: binaryHash,
		OperatorPreviewIncluded:       true,
		OperatorPreviewLauncherName:   "Start-Prayu-Operator-Preview.cmd",
		OperatorPreviewLauncherSHA256: digestBytes(launcher),
		LocalTestGuideName:            "LOCAL-TEST-GUIDE.txt", LocalTestGuideSHA256: digestBytes(guide),
		DefaultUILanguage: "zh-CN", ReproducibilityChecked: true, Reproducible: true,
		ManualWindows10MatrixRequired: true}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, metadataPath, metadataBytes)
	contents := []string{"TraverseBoard.exe", "Start-Prayu-Operator-Preview.cmd",
		"LOCAL-TEST-GUIDE.txt", "LICENSE", "README.md", "NOTICE", "sbom.json",
		"release-metadata.json"}
	archiveFiles := map[string][]byte{
		"TraverseBoard.exe":                binaryBytes,
		"Start-Prayu-Operator-Preview.cmd": launcher,
		"LOCAL-TEST-GUIDE.txt":             guide,
		"LICENSE":                          []byte("test license\n"),
		"README.md":                        []byte("test readme\n"),
		"NOTICE":                           []byte("test notice\n"),
		"sbom.json":                        []byte(`{"bomFormat":"CycloneDX"}`),
		"release-metadata.json":            metadataBytes,
	}
	entries := make([]portableEntry, 0, len(contents))
	for _, name := range contents {
		content := archiveFiles[name]
		entries = append(entries, portableEntry{Name: name, Size: int64(len(content)),
			SHA256: digestBytes(content)})
	}
	writeTestZip(t, zipPath, contents, archiveFiles)
	zipHash, _, err := fileDigest(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := portableManifest{ProtocolVersion: "portable_zip_manifest.v1",
		ZipName: filepath.Base(zipPath), ZipSHA256: zipHash, BinarySHA256: binaryHash,
		SBOMSHA256:   digestBytes(archiveFiles["sbom.json"]),
		NoticeSHA256: digestBytes(archiveFiles["NOTICE"]), Version: metadata.AppVersion,
		Revision: revision, ZipReproducibilityChecked: true,
		ZipTimestampsReproducible: true, Contents: contents, Entries: entries}
	writeJSON(t, manifestPath, manifest)
	definition, err := packagede2e.LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	fixture := packagede2e.FixtureSetReport{ProtocolVersion: packagede2e.FixtureSetProtocol,
		ManifestSHA256:     definition.ManifestSHA256,
		AttackMatrixSHA256: definition.MatrixSHA256,
		RepositoryCount:    len(definition.Manifest.Repositories),
		AttackCaseCount:    len(definition.AttackMatrix.Cases),
		RequiredCategories: append([]string(nil), definition.AttackMatrix.RequiredCategories...),
		OracleVerified:     true, AllAttackCasesBound: true}
	for _, repository := range definition.Manifest.Repositories {
		fixture.Repositories = append(fixture.Repositories,
			packagede2e.FixtureRepositoryReport{ID: repository.ID,
				Language: repository.Language, Head: repository.ExpectedHead,
				Tree: repository.ExpectedTree, ContentSHA256: digest,
				FileCount: len(repository.Files), Clean: true,
				BaselineFailureObserved: true, RepairPassVerified: true})
	}
	writeJSON(t, fixturePath, fixture)
	candidate, oracle, err := ValidateCandidate(CandidateOptions{BinaryPath: binaryPath,
		ZipPath: zipPath, PortableManifestPath: manifestPath,
		ReleaseMetadataPath: metadataPath, FixtureReportPath: fixturePath,
		ExpectedRevision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.BinarySHA256 != binaryHash || candidate.ZipSHA256 != zipHash ||
		candidate.Revision != revision || !oracle.OracleVerified ||
		oracle.ManifestSHA256 != definition.ManifestSHA256 {
		t.Fatalf("unexpected candidate/oracle: %#v %#v", candidate, oracle)
	}
	writeTestFile(t, binaryPath, []byte("tampered-candidate"))
	if _, _, err := ValidateCandidate(CandidateOptions{BinaryPath: binaryPath,
		ZipPath: zipPath, PortableManifestPath: manifestPath,
		ReleaseMetadataPath: metadataPath, FixtureReportPath: fixturePath,
		ExpectedRevision: revision}); err == nil {
		t.Fatal("tampered packaged executable was accepted")
	}
	writeTestFile(t, binaryPath, binaryBytes)
	archiveFiles["README.md"] = []byte("tampered archive readme\n")
	writeTestZip(t, zipPath, contents, archiveFiles)
	manifest.ZipSHA256, _, err = fileDigest(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, manifestPath, manifest)
	if _, _, err := ValidateCandidate(CandidateOptions{BinaryPath: binaryPath,
		ZipPath: zipPath, PortableManifestPath: manifestPath,
		ReleaseMetadataPath: metadataPath, FixtureReportPath: fixturePath,
		ExpectedRevision: revision}); err == nil {
		t.Fatal("ZIP entry drift hidden behind a refreshed top-level hash was accepted")
	}
}

func validRunbook() Runbook {
	digest := strings.Repeat("a", 64)
	runbook := Runbook{ProtocolVersion: RunbookProtocol, Issue: IssueNumber,
		CandidateSHA256: digest, FixtureManifestSHA256: strings.Repeat("b", 64),
		DefaultLaunch: DefaultLaunchEvidence{CandidateSHA256: digest,
			Arguments: []string{}, ProviderConfigured: true, WorkspaceTrustVisible: true,
			BackendReadinessVisible: true, StandardCodeStartVisible: true,
			EvidencePath: "launch/default.json", EvidenceSHA256: digest}}
	local := BackendEvidence{Backend: "local", State: "ready"}
	for _, language := range requiredLanguages {
		runID := "run-" + language + "-local"
		run := RunEvidence{ID: language + "-local", Language: language, RunID: runID}
		for _, surface := range requiredSurfaces {
			run.Projections = append(run.Projections, SurfaceProjection{Surface: surface,
				CandidateSHA256: digest, RunID: runID, Status: "passed",
				ReceiptSHA256: digest, DiffSHA256: digest,
				CheckpointID:   "checkpoint-" + language,
				EvidencePath:   "surfaces/" + runID + "/" + surface + ".json",
				EvidenceSHA256: digest})
		}
		local.Runs = append(local.Runs, run)
	}
	runbook.Backends = []BackendEvidence{local, {Backend: "docker",
		State: "approval_required", Fallback: &FallbackEvidence{
			CandidateSHA256: digest, RunID: "run-docker-fallback",
			ApprovalID: "approval-docker", ReasonCode: "docker_unavailable",
			ReadinessEvidencePath: "fallback/docker-readiness.json",
			ReadinessEvidenceSHA:  digest, UIEvidencePath: "fallback/docker.png",
			UIEvidenceSHA256: digest}}}
	for index, kind := range requiredEdgeCases {
		edge := EdgeEvidence{Kind: kind, RunID: "run-go-local", Scope: "drydock",
			Path: fmtTestPath(index), ExpectedSHA256: digest, EvidenceSHA256: digest}
		if kind == "dirty_tracked" || kind == "untracked" || kind == "concurrent_edit" {
			edge.Scope = "source"
		}
		if kind == "concurrent_edit" {
			edge.BaselineSHA256 = strings.Repeat("c", 64)
		}
		runbook.Edges = append(runbook.Edges, edge)
	}
	for _, current := range requiredContinuityCases {
		evidence := ContinuityEvidence{Case: current, CandidateSHA256: digest,
			ThreadID: "thread-" + current, RunID: "run-" + current,
			ComposerEnabled: true, EvidencePath: "continuity/" + current + ".png",
			EvidenceSHA256: digest}
		if current == "completed" || current == "failed" {
			evidence.SuccessorRunID = "successor-" + current
		}
		if current == "approval_wait" {
			evidence.QueuedMessageSHA256 = digest
		}
		if current == "restart" {
			evidence.ProcessRestarted = true
		}
		runbook.Continuity = append(runbook.Continuity, evidence)
	}
	for _, osName := range []string{"windows_10", "windows_11"} {
		for _, dpi := range []int{100, 200} {
			runbook.Platforms = append(runbook.Platforms, PlatformEvidence{
				ID: osName + "-" + strconv.Itoa(dpi), CandidateSHA256: digest,
				OS: osName, Build: "build-19045", DPIPercent: dpi, Locale: "zh-CN",
				DefaultLaunchPassed: true, ChineseIMEPassed: true,
				KeyboardNavigationPassed: true, FocusVisiblePassed: true,
				AccessibleNamesPassed: true, NoCriticalA11yViolations: true,
				EvidencePath:   osName + "/dpi-" + strconv.Itoa(dpi) + ".png",
				EvidenceSHA256: digest})
		}
	}
	return runbook
}

func validReport() Report {
	digest := strings.Repeat("a", 64)
	report := Report{ProtocolVersion: ReportProtocol, Issue: IssueNumber, Status: "pass",
		GeneratedAt: time.Now().UTC(), Candidate: CandidateEvidence{Version: "v0.1.0-test",
			Revision: strings.Repeat("b", 40), BinarySHA256: digest, BinarySizeBytes: 10,
			ZipSHA256: digest, ZipSizeBytes: 20, ManifestSHA256: digest,
			ReleaseMetadataSHA256: digest},
		Fixture: FixtureEvidence{ProtocolVersion: packagede2e.FixtureSetProtocol,
			ReportSHA256: digest, ManifestSHA256: digest, AttackMatrixSHA256: digest,
			RepositoryCount: 4, OracleVerified: true},
		Backends: []BackendSummary{{Backend: "local", State: "ready", PassedRuns: 4},
			{Backend: "docker", State: "approval_required", ApprovalID: "approval-docker",
				FallbackReason: "docker_unavailable", EvidenceSHA256: digest}},
		Coverage: Coverage{Languages: append([]string(nil), requiredLanguages...),
			Backends:         append([]string(nil), requiredBackends...),
			Surfaces:         append([]string(nil), requiredSurfaces...),
			EdgeCases:        append([]string(nil), requiredEdgeCases...),
			ContinuityCases:  append([]string(nil), requiredContinuityCases...),
			OperatingSystems: []string{"windows_10", "windows_11"},
			DPIPercents:      []int{100, 200}, RealFailureRetries: 4, RealProcessJobs: 8},
		Safeguards:    Safeguards{NetworkDisabled: true, CredentialsAbsent: true},
		RunbookSHA256: digest}
	for _, language := range requiredLanguages {
		report.Scenarios = append(report.Scenarios, ScenarioSummary{ID: language + "-local",
			Language: language, Backend: "local", RunID: "run-" + language,
			ThreadID: "thread-" + language, SessionID: "session-" + language,
			FixtureHead: strings.Repeat("c", 40), ReadRounds: 2, AppliedEdits: 2,
			FailedJobs: 1, PassedJobs: 1, FixRounds: 1, ArtifactCount: 2,
			ProjectionCount: 5, ReceiptSHA256: digest, DiffSHA256: digest,
			CheckpointID: "checkpoint-" + language, WorkspaceRevision: digest,
			SourceWorkPreserved: true})
	}
	for _, current := range requiredContinuityCases {
		summary := ContinuitySummary{Case: current, ThreadID: "thread-" + current,
			RunID: "run-" + current, Verified: true, EvidenceSHA256: digest}
		if current == "completed" || current == "failed" {
			summary.SuccessorRunID = "successor-" + current
		}
		report.Continuity = append(report.Continuity, summary)
	}
	for _, osName := range []string{"windows_10", "windows_11"} {
		for _, dpi := range []int{100, 200} {
			report.Platforms = append(report.Platforms, PlatformSummary{
				ID: osName + "-" + strconv.Itoa(dpi), OS: osName, Build: "build-19045",
				DPIPercent: dpi, EvidenceSHA256: digest})
		}
	}
	return report
}

func fmtTestPath(index int) string { return "edge/evidence-" + string(rune('a'+index)) + ".txt" }

func writeTestZip(t *testing.T, path string, order []string, files map[string][]byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range order {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			_ = writer.Close()
			_ = file.Close()
			t.Fatal(createErr)
		}
		if _, writeErr := io.Copy(entry, bytes.NewReader(files[name])); writeErr != nil {
			_ = writer.Close()
			_ = file.Close()
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, content)
}

func writeTestFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
