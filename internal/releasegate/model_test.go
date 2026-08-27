package releasegate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/packagede2e"
	"cyberagent-workbench/internal/producte2e"
)

func TestAggregateFilesRequiresExactCompleteCandidateBoundEvidence(t *testing.T) {
	paths := writeValidInputs(t)
	report, err := AggregateFiles(paths)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusPassed || !report.Gate.ReleaseAuthorized ||
		report.Coverage.SecurityPassedRuns != 75 || report.Coverage.ProductScenarios != 4 ||
		len(report.Coverage.EdgeCases) != 8 || report.Components.Product.ChainSHA256 == "" {
		t.Fatalf("unexpected aggregate: %+v", report)
	}
	output := filepath.Join(t.TempDir(), "standard-code-release-gate.json")
	if err := WriteReport(output, report); err != nil {
		t.Fatal(err)
	}
	if err := VerifyReport(output, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteReport(output, report); err == nil {
		t.Fatal("aggregate report overwrite was accepted")
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`C:\\Users\\operator`, "/home/operator", `"skip_accepted": true`} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("aggregate leaked forbidden content %q", forbidden)
		}
	}
}

func TestAggregateFilesRejectsCrossCandidateReplayAndUnknownFields(t *testing.T) {
	paths := writeValidInputs(t)
	product := readProductReport(t, paths.ProductReport)
	product.Candidate.Revision = strings.Repeat("9", 40)
	product, err := product.Seal()
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, paths.ProductReport, product)
	if _, err := AggregateFiles(paths); err == nil ||
		!strings.Contains(err.Error(), "candidate bindings do not match") {
		t.Fatalf("cross-candidate product report was accepted: %v", err)
	}

	paths = writeValidInputs(t)
	content, err := os.ReadFile(paths.ProductReport)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(content, &object); err != nil {
		t.Fatal(err)
	}
	object["waiver_reason"] = "not allowed"
	writeJSON(t, paths.ProductReport, object)
	if _, err := AggregateFiles(paths); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown product report field was accepted: %v", err)
	}
}

func TestAggregateFilesRejectsTamperedSecurityAndBootstrapEvidence(t *testing.T) {
	paths := writeValidInputs(t)
	security := readSecurityReport(t, paths.SecurityReport)
	security.Cases[0].Backends[0].Evidence = security.Cases[0].Backends[0].Evidence[1:]
	writeJSON(t, paths.SecurityReport, security)
	if _, err := AggregateFiles(paths); err == nil ||
		!strings.Contains(err.Error(), "security evidence is invalid") {
		t.Fatalf("tampered security evidence was accepted: %v", err)
	}

	paths = writeValidInputs(t)
	bootstrap := readBootstrapReport(t, paths.BootstrapReport)
	bootstrap.AttackMatrix.EvidencedCaseCount = 40
	bootstrap.AttackMatrix.RemainingRequiredCaseCount = 0
	bootstrap.AttackMatrix.Status = "passed"
	writeJSON(t, paths.BootstrapReport, bootstrap)
	if _, err := AggregateFiles(paths); err == nil ||
		!strings.Contains(err.Error(), "bootstrap attack matrix state is invalid") {
		t.Fatalf("bootstrap verdict inflation was accepted: %v", err)
	}
}

func writeValidInputs(t *testing.T) InputPaths {
	t.Helper()
	root := t.TempDir()
	revision := strings.Repeat("b", 40)
	binary := filepath.Join(root, "TraverseBoard.exe")
	archive := filepath.Join(root, "Prayu-portable-v0.1.0-test-windows-amd64.zip")
	manifest := filepath.Join(root, "portable-zip-manifest.json")
	metadata := filepath.Join(root, "release-metadata.json")
	bootstrapPath := filepath.Join(root, "standard-code-packaged-e2e.json")
	productPath := filepath.Join(root, "standard-code-product-e2e.json")
	securityPath := filepath.Join(root, "standard-code-security-evidence.json")
	writeBytes(t, binary, []byte("MZ\x00release-gate-test"))
	writeBytes(t, archive, []byte("PK\x03\x04release-gate-test"))
	writeBytes(t, manifest, []byte("{\"protocol_version\":\"portable_zip_manifest.v1\"}\n"))
	writeBytes(t, metadata, []byte("{\"protocol_version\":\"portable_release_metadata.v1\"}\n"))
	binarySHA, binarySize, err := digestRegularFile(binary, maximumCandidateBytes)
	if err != nil {
		t.Fatal(err)
	}
	archiveSHA, archiveSize, err := digestRegularFile(archive, maximumCandidateBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, manifestSHA, err := readEvidenceFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, metadataSHA, err := readEvidenceFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := packagede2e.LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 27, 1, 0, 0, 0, time.UTC)
	bootstrap := validBootstrap(now, revision, binarySHA, archiveSHA,
		definition.ManifestSHA256, definition.MatrixSHA256)
	product := validProductReport(t, now.Add(time.Minute), revision, binarySHA, binarySize,
		archiveSHA, archiveSize, manifestSHA, metadataSHA,
		definition.ManifestSHA256, definition.MatrixSHA256)
	security := validSecurityReport(t, now.Add(2*time.Minute), revision, binarySHA, archiveSHA,
		filepath.Base(archive))
	writeJSON(t, bootstrapPath, bootstrap)
	writeJSON(t, productPath, product)
	writeJSON(t, securityPath, security)
	return InputPaths{BinaryPath: binary, ArchivePath: archive,
		PortableManifest: manifest, ReleaseMetadata: metadata,
		BootstrapReport: bootstrapPath, ProductReport: productPath,
		SecurityReport: securityPath, ExpectedRevision: revision}
}

func validBootstrap(now time.Time, revision, binarySHA, archiveSHA, fixtureSHA, matrixSHA string) bootstrapReport {
	resultIDs := []string{"candidate_provenance", "fixed_repository_oracle",
		"exact_package_extraction", "packaged_default_start",
		"packaged_operator_preview_kill_reopen", "fixed_repositories_immutable",
		"credential_sentinel_non_persistence", "candidate_process_cleanup",
		"owned_harness_cleanup"}
	results := make([]bootstrapResult, 0, len(resultIDs))
	for _, id := range resultIDs {
		results = append(results, bootstrapResult{ID: id, Status: "pass", Facts: json.RawMessage(`{}`)})
	}
	return bootstrapReport{ProtocolVersion: packagede2e.PackagedE2EProtocol,
		GeneratedAt: now, Issue: IssueNumber, BootstrapStatus: "pass",
		ReleaseGateStatus: "needs_full_matrix",
		Candidate: bootstrapCandidate{Version: "v0.1.0-test", Revision: revision,
			ArchiveSHA256: archiveSHA, BinarySHA256: binarySHA, SourceDateEpoch: 1_700_000_000},
		FixtureSet: bootstrapFixtureSet{ProtocolVersion: packagede2e.FixtureSetProtocol,
			ManifestSHA256: fixtureSHA, AttackMatrixSHA256: matrixSHA,
			RepositoryCount: 4, AttackCaseCount: 40, OracleVerified: true,
			AllAttackCasesBound: true},
		Results: results,
		AttackMatrix: bootstrapAttackMatrix{RequiredCaseCount: 40, PreparedCaseCount: 40,
			EvidencedCaseCount: 0, RemainingRequiredCaseCount: 40,
			Status: "needs_full_matrix", FailurePolicy: "fail_closed_no_waiver",
			UnexecutedCasesAreNotPassOrSkip: true}}
}

func validProductReport(t *testing.T, now time.Time, revision, binarySHA string, binarySize int64,
	archiveSHA string, archiveSize int64, manifestSHA, metadataSHA, fixtureSHA, matrixSHA string,
) producte2e.Report {
	t.Helper()
	digest := strings.Repeat("a", 64)
	languages := []string{"go", "node", "python", "rust"}
	report := producte2e.Report{ProtocolVersion: producte2e.ReportProtocol,
		Issue: producte2e.IssueNumber, Status: "pass", GeneratedAt: now,
		Candidate: producte2e.CandidateEvidence{Version: "v0.1.0-test", Revision: revision,
			BinarySHA256: binarySHA, BinarySizeBytes: binarySize,
			ZipSHA256: archiveSHA, ZipSizeBytes: archiveSize,
			ManifestSHA256: manifestSHA, ReleaseMetadataSHA256: metadataSHA},
		Fixture: producte2e.FixtureEvidence{ProtocolVersion: packagede2e.FixtureSetProtocol,
			ReportSHA256: digest, ManifestSHA256: fixtureSHA,
			AttackMatrixSHA256: matrixSHA, RepositoryCount: 4, OracleVerified: true},
		Backends: []producte2e.BackendSummary{{Backend: "local", State: "ready", PassedRuns: 4},
			{Backend: "docker", State: "approval_required", ApprovalID: "approval-docker",
				FallbackReason: "docker_unavailable", EvidenceSHA256: digest}},
		Coverage: producte2e.Coverage{Languages: languages,
			Backends: []string{"local", "docker"},
			Surfaces: []string{"desktop", "cli", "http", "handoff", "final"},
			EdgeCases: []string{"chinese_path", "space_path", "long_path", "crlf",
				"dirty_tracked", "untracked", "binary", "concurrent_edit"},
			ContinuityCases:  []string{"completed", "failed", "approval_wait", "restart"},
			OperatingSystems: []string{"windows_10", "windows_11"},
			DPIPercents:      []int{100, 200}, RealFailureRetries: 4, RealProcessJobs: 8},
		Safeguards:    producte2e.Safeguards{NetworkDisabled: true, CredentialsAbsent: true},
		RunbookSHA256: digest}
	for _, language := range languages {
		report.Scenarios = append(report.Scenarios, producte2e.ScenarioSummary{
			ID: language + "-local", Language: language, Backend: "local",
			RunID: "run-" + language, ThreadID: "thread-" + language,
			SessionID: "session-" + language, FixtureHead: strings.Repeat("c", 40),
			ReadRounds: 2, AppliedEdits: 2, FailedJobs: 1, PassedJobs: 1,
			FixRounds: 1, ArtifactCount: 2, ProjectionCount: 5,
			ReceiptSHA256: digest, DiffSHA256: digest,
			CheckpointID: "checkpoint-" + language, WorkspaceRevision: digest,
			SourceWorkPreserved: true})
	}
	for _, current := range []string{"completed", "failed", "approval_wait", "restart"} {
		summary := producte2e.ContinuitySummary{Case: current, ThreadID: "thread-" + current,
			RunID: "run-" + current, Verified: true, EvidenceSHA256: digest}
		if current == "completed" || current == "failed" {
			summary.SuccessorRunID = "successor-" + current
		}
		report.Continuity = append(report.Continuity, summary)
	}
	for _, osName := range []string{"windows_10", "windows_11"} {
		for _, dpi := range []int{100, 200} {
			report.Platforms = append(report.Platforms, producte2e.PlatformSummary{
				ID: osName + "-" + strconv.Itoa(dpi), OS: osName,
				Build: "build-19045", DPIPercent: dpi, EvidenceSHA256: digest})
		}
	}
	sealed, err := report.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func validSecurityReport(t *testing.T, now time.Time, revision, binarySHA, archiveSHA, archiveName string) packagede2e.StandardCodeSecurityEvidence {
	t.Helper()
	definition, err := packagede2e.LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("d", 64)
	report := packagede2e.StandardCodeSecurityEvidence{
		Candidate: packagede2e.SecurityCandidateEvidence{SourceCommit: revision,
			ExecutableSHA256: binarySHA, ArchiveSHA256: archiveSHA, ArchiveName: archiveName,
			FixtureSHA256:      definition.ManifestSHA256,
			AttackMatrixSHA256: definition.MatrixSHA256,
			OperatingSystem:    runtime.GOOS, OSVersion: "test-os-build", Architecture: runtime.GOARCH},
		Backends: []packagede2e.SecurityBackendEvidence{
			{Backend: "local", Availability: packagede2e.SecurityBackendReady,
				IdentitySHA256: digest, GenerationSHA256: strings.Repeat("e", 64),
				Network: "disabled", Credentials: "none"},
			{Backend: "docker", Availability: packagede2e.SecurityBackendReady,
				IdentitySHA256: strings.Repeat("f", 64), GenerationSHA256: strings.Repeat("1", 64),
				Network: "disabled", Credentials: "none"}},
		Cleanup: packagede2e.SecurityCleanupEvidence{OwnedRootSHA256: strings.Repeat("2", 64),
			OwnedProcessesStarted: 8, OwnedProcessesReaped: 8, OwnedDirectoriesOnly: true},
		StartedAt: now, CompletedAt: now.Add(time.Minute)}
	for caseIndex, attack := range definition.AttackMatrix.Cases {
		current := packagede2e.SecurityAttackCaseEvidence{ID: attack.ID,
			Category: attack.Category, Phase: attack.Phase,
			ExpectedOutcome: attack.ExpectedOutcome, ExpectedSignal: attack.ExpectedSignal}
		for backendIndex, backend := range attack.Backends {
			result := packagede2e.SecurityCaseBackendEvidence{Backend: backend,
				FixtureID:     attack.FixtureIDs[(caseIndex+backendIndex)%len(attack.FixtureIDs)],
				Status:        packagede2e.SecurityEvidencePassed,
				ActualOutcome: attack.ExpectedOutcome, ActualSignal: attack.ExpectedSignal,
				OperatorCode:   "standard_code.attack." + attack.ExpectedSignal,
				DiagnosticCode: "product.observed", ActualExecution: true,
				StartedAt: now, CompletedAt: now.Add(time.Second)}
			for _, kind := range attack.RequiredEvidence {
				result.Evidence = append(result.Evidence, packagede2e.SecurityEvidenceRef{
					Kind: kind, Source: securityEvidenceSource(kind), SHA256: digest})
			}
			current.Backends = append(current.Backends, result)
		}
		report.Cases = append(report.Cases, current)
	}
	if err := packagede2e.FinalizeStandardCodeSecurityEvidence(&report); err != nil {
		t.Fatal(err)
	}
	return report
}

func securityEvidenceSource(kind string) string {
	values := map[string]string{"operator_ui": "desktop.projection",
		"immutable_event": "run.event", "workspace_digest": "drydock.observation",
		"process_receipt": "command_runtime.receipt", "network_observation": "sandbox.network",
		"artifact_digest": "artifact.store", "thread_transcript": "thread.projection",
		"checkpoint": "workspace.checkpoint"}
	return values[kind]
}

func readProductReport(t *testing.T, path string) producte2e.Report {
	t.Helper()
	var report producte2e.Report
	readJSON(t, path, &report)
	return report
}

func readSecurityReport(t *testing.T, path string) packagede2e.StandardCodeSecurityEvidence {
	t.Helper()
	var report packagede2e.StandardCodeSecurityEvidence
	readJSON(t, path, &report)
	return report
}

func readBootstrapReport(t *testing.T, path string) bootstrapReport {
	t.Helper()
	var report bootstrapReport
	readJSON(t, path, &report)
	return report
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeBytes(t, path, append(content, '\n'))
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
