// Package releasegate validates and aggregates the candidate-bound Standard
// Code bootstrap, product, and security evidence used by the Beta release gate.
package releasegate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/packagede2e"
	"cyberagent-workbench/internal/producte2e"
)

const (
	ProtocolVersion = "standard_code_release_gate.v1"
	IssueNumber     = 140
	StatusPassed    = "passed"

	maximumEvidenceBytes  int64 = 4 << 20
	maximumCandidateBytes int64 = 2 << 30
	maximumReportBytes          = 2 << 20
)

var gitObjectPattern = regexp.MustCompile(`^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)

// InputPaths identifies the exact candidate and immutable producer reports.
// Every path must name a regular, non-symlink file. The aggregate never stores
// any of these paths.
type InputPaths struct {
	BinaryPath       string
	ArchivePath      string
	PortableManifest string
	ReleaseMetadata  string
	BootstrapReport  string
	ProductReport    string
	SecurityReport   string
	ExpectedRevision string
}

type Report struct {
	ProtocolVersion string            `json:"protocol_version"`
	Issue           int               `json:"issue"`
	Status          string            `json:"status"`
	EvaluatedAt     time.Time         `json:"evaluated_at"`
	Candidate       CandidateEvidence `json:"candidate"`
	Components      ComponentEvidence `json:"components"`
	Coverage        CoverageEvidence  `json:"coverage"`
	Safeguards      SafeguardEvidence `json:"safeguards"`
	Gate            GateEvidence      `json:"gate"`
	ReportSHA256    string            `json:"report_sha256"`
}

type CandidateEvidence struct {
	Version                string `json:"version"`
	Revision               string `json:"revision"`
	BinarySHA256           string `json:"binary_sha256"`
	BinarySizeBytes        int64  `json:"binary_size_bytes"`
	ArchiveSHA256          string `json:"archive_sha256"`
	ArchiveSizeBytes       int64  `json:"archive_size_bytes"`
	PortableManifestSHA256 string `json:"portable_manifest_sha256"`
	ReleaseMetadataSHA256  string `json:"release_metadata_sha256"`
	FixtureManifestSHA256  string `json:"fixture_manifest_sha256"`
	AttackMatrixSHA256     string `json:"attack_matrix_sha256"`
}

type ComponentEvidence struct {
	Bootstrap ProducerEvidence `json:"bootstrap"`
	Product   ProducerEvidence `json:"product"`
	Security  ProducerEvidence `json:"security"`
}

type ProducerEvidence struct {
	ProtocolVersion string `json:"protocol_version"`
	Issue           int    `json:"issue"`
	Status          string `json:"status"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	ProducerSHA256  string `json:"producer_sha256,omitempty"`
	ChainSHA256     string `json:"chain_sha256,omitempty"`
}

type CoverageEvidence struct {
	FixtureRepositories       int      `json:"fixture_repositories"`
	Languages                 []string `json:"languages"`
	Backends                  []string `json:"backends"`
	DeliverySurfaces          []string `json:"delivery_surfaces"`
	EdgeCases                 []string `json:"edge_cases"`
	OperatingSystems          []string `json:"operating_systems"`
	DPIPercents               []int    `json:"dpi_percents"`
	ProductScenarios          int      `json:"product_scenarios"`
	ProductRealFailureRetries int      `json:"product_real_failure_retries"`
	ProductRealProcessJobs    int      `json:"product_real_process_jobs"`
	ProductContinuityCases    int      `json:"product_continuity_cases"`
	ProductPlatformRows       int      `json:"product_platform_rows"`
	SecurityRequiredCases     int      `json:"security_required_cases"`
	SecurityRequiredRuns      int      `json:"security_required_backend_runs"`
	SecurityPassedRuns        int      `json:"security_passed_backend_runs"`
}

type SafeguardEvidence struct {
	NetworkDisabled         bool `json:"network_disabled"`
	CredentialsAbsent       bool `json:"credentials_absent"`
	DangerFullAccessEnabled bool `json:"danger_full_access_enabled"`
	DebugMaximumEnabled     bool `json:"debug_maximum_access_enabled"`
	FullCDPDebugEnabled     bool `json:"full_cdp_debug_enabled"`
	SourceOverwrite         bool `json:"source_overwrite"`
	FakeRunnerAccepted      bool `json:"fake_runner_accepted"`
	OrphanProcesses         int  `json:"orphan_processes"`
	ForeignProcessesKilled  int  `json:"foreign_processes_killed"`
	SkipAccepted            bool `json:"skip_accepted"`
	WaiverAccepted          bool `json:"waiver_accepted"`
}

type GateEvidence struct {
	CandidateBound           bool `json:"candidate_bound"`
	BootstrapPassed          bool `json:"bootstrap_passed"`
	ProductMatrixComplete    bool `json:"product_matrix_complete"`
	SecurityMatrixComplete   bool `json:"security_matrix_complete"`
	WindowsMatrixComplete    bool `json:"windows_matrix_complete"`
	ProducerSelfHashesValid  bool `json:"producer_self_hashes_valid"`
	MissingEvidenceIsFailure bool `json:"missing_evidence_is_failure"`
	UnexecutedIsNeverPass    bool `json:"unexecuted_is_never_pass"`
	ReleaseAuthorized        bool `json:"release_authorized"`
}

type bootstrapReport struct {
	ProtocolVersion   string                `json:"protocol_version"`
	GeneratedAt       time.Time             `json:"generated_at"`
	Issue             int                   `json:"issue"`
	BootstrapStatus   string                `json:"bootstrap_status"`
	ReleaseGateStatus string                `json:"release_gate_status"`
	Candidate         bootstrapCandidate    `json:"candidate"`
	FixtureSet        bootstrapFixtureSet   `json:"fixture_set"`
	Results           []bootstrapResult     `json:"results"`
	AttackMatrix      bootstrapAttackMatrix `json:"attack_matrix"`
}

type bootstrapCandidate struct {
	Version         string `json:"version"`
	Revision        string `json:"revision"`
	ArchiveSHA256   string `json:"zip_sha256"`
	BinarySHA256    string `json:"binary_sha256"`
	SourceDateEpoch int64  `json:"source_date_epoch"`
}

type bootstrapFixtureSet struct {
	ProtocolVersion     string `json:"protocol_version"`
	ManifestSHA256      string `json:"manifest_sha256"`
	AttackMatrixSHA256  string `json:"attack_matrix_sha256"`
	RepositoryCount     int    `json:"repository_count"`
	AttackCaseCount     int    `json:"attack_case_count"`
	OracleVerified      bool   `json:"oracle_verified"`
	AllAttackCasesBound bool   `json:"all_attack_cases_bound"`
}

type bootstrapResult struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Facts  json.RawMessage `json:"facts"`
}

type bootstrapAttackMatrix struct {
	RequiredCaseCount               int    `json:"required_case_count"`
	PreparedCaseCount               int    `json:"prepared_case_count"`
	EvidencedCaseCount              int    `json:"evidenced_case_count"`
	RemainingRequiredCaseCount      int    `json:"remaining_required_case_count"`
	Status                          string `json:"status"`
	FailurePolicy                   string `json:"failure_policy"`
	UnexecutedCasesAreNotPassOrSkip bool   `json:"unexecuted_cases_are_not_pass_or_skip"`
}

// AggregateFiles strictly validates every producer report, recomputes all file
// digests, cross-binds them to one candidate, and returns a sealed verdict.
func AggregateFiles(options InputPaths) (Report, error) {
	if !gitObjectPattern.MatchString(options.ExpectedRevision) {
		return Report{}, errors.New("release gate expected revision is invalid")
	}
	binaryHash, binarySize, err := digestRegularFile(options.BinaryPath, maximumCandidateBytes)
	if err != nil {
		return Report{}, fmt.Errorf("release gate binary: %w", err)
	}
	archiveHash, archiveSize, err := digestRegularFile(options.ArchivePath, maximumCandidateBytes)
	if err != nil {
		return Report{}, fmt.Errorf("release gate archive: %w", err)
	}
	_, manifestHash, err := readEvidenceFile(options.PortableManifest)
	if err != nil {
		return Report{}, fmt.Errorf("release gate portable manifest: %w", err)
	}
	_, metadataHash, err := readEvidenceFile(options.ReleaseMetadata)
	if err != nil {
		return Report{}, fmt.Errorf("release gate release metadata: %w", err)
	}
	bootstrapBytes, bootstrapHash, err := readEvidenceFile(options.BootstrapReport)
	if err != nil {
		return Report{}, fmt.Errorf("release gate bootstrap evidence: %w", err)
	}
	productBytes, productHash, err := readEvidenceFile(options.ProductReport)
	if err != nil {
		return Report{}, fmt.Errorf("release gate product evidence: %w", err)
	}
	securityBytes, securityHash, err := readEvidenceFile(options.SecurityReport)
	if err != nil {
		return Report{}, fmt.Errorf("release gate security evidence: %w", err)
	}

	var bootstrap bootstrapReport
	if err := decodeStrictJSON(bootstrapBytes, &bootstrap); err != nil {
		return Report{}, fmt.Errorf("release gate bootstrap evidence is invalid: %w", err)
	}
	if err := validateBootstrap(bootstrap); err != nil {
		return Report{}, err
	}
	var product producte2e.Report
	if err := decodeStrictJSON(productBytes, &product); err != nil {
		return Report{}, fmt.Errorf("release gate product evidence is invalid: %w", err)
	}
	if err := product.Validate(); err != nil {
		return Report{}, fmt.Errorf("release gate product evidence is invalid: %w", err)
	}
	var security packagede2e.StandardCodeSecurityEvidence
	if err := decodeStrictJSON(securityBytes, &security); err != nil {
		return Report{}, fmt.Errorf("release gate security evidence is invalid: %w", err)
	}
	if err := packagede2e.ValidateStandardCodeSecurityEvidence(security); err != nil {
		return Report{}, fmt.Errorf("release gate security evidence is invalid: %w", err)
	}

	report, err := aggregate(aggregateInputs{
		bootstrap: bootstrap, bootstrapArtifactSHA: bootstrapHash,
		product: product, productArtifactSHA: productHash,
		security: security, securityArtifactSHA: securityHash,
		binarySHA: binaryHash, binarySize: binarySize,
		archiveSHA: archiveHash, archiveSize: archiveSize,
		manifestSHA: manifestHash, metadataSHA: metadataHash,
		expectedRevision: options.ExpectedRevision,
	})
	if err != nil {
		return Report{}, err
	}
	return report.Seal()
}

type aggregateInputs struct {
	bootstrap            bootstrapReport
	bootstrapArtifactSHA string
	product              producte2e.Report
	productArtifactSHA   string
	security             packagede2e.StandardCodeSecurityEvidence
	securityArtifactSHA  string
	binarySHA            string
	binarySize           int64
	archiveSHA           string
	archiveSize          int64
	manifestSHA          string
	metadataSHA          string
	expectedRevision     string
}

func aggregate(input aggregateInputs) (Report, error) {
	candidate := input.product.Candidate
	security := input.security
	bootstrap := input.bootstrap
	if candidate.Revision != input.expectedRevision || bootstrap.Candidate.Revision != input.expectedRevision ||
		security.Candidate.SourceCommit != input.expectedRevision ||
		candidate.Version != bootstrap.Candidate.Version ||
		candidate.BinarySHA256 != input.binarySHA || bootstrap.Candidate.BinarySHA256 != input.binarySHA ||
		security.Candidate.ExecutableSHA256 != input.binarySHA || candidate.BinarySizeBytes != input.binarySize ||
		candidate.ZipSHA256 != input.archiveSHA || bootstrap.Candidate.ArchiveSHA256 != input.archiveSHA ||
		security.Candidate.ArchiveSHA256 != input.archiveSHA || candidate.ZipSizeBytes != input.archiveSize ||
		candidate.ManifestSHA256 != input.manifestSHA || candidate.ReleaseMetadataSHA256 != input.metadataSHA {
		return Report{}, errors.New("release gate candidate bindings do not match")
	}
	if input.product.Fixture.ManifestSHA256 != bootstrap.FixtureSet.ManifestSHA256 ||
		security.Candidate.FixtureSHA256 != bootstrap.FixtureSet.ManifestSHA256 ||
		input.product.Fixture.AttackMatrixSHA256 != bootstrap.FixtureSet.AttackMatrixSHA256 ||
		security.Candidate.AttackMatrixSHA256 != bootstrap.FixtureSet.AttackMatrixSHA256 {
		return Report{}, errors.New("release gate fixture or attack matrix bindings do not match")
	}
	if security.Status != packagede2e.SecurityEvidencePassed ||
		security.Summary.RequiredCaseCount != 40 || security.Summary.RequiredBackendRuns != 75 ||
		security.Summary.PassedBackendRuns != 75 || security.Summary.FailedBackendRuns != 0 ||
		security.Summary.UnexecutedBackendRuns != 0 ||
		security.Cleanup.OwnedProcessesStarted != security.Cleanup.OwnedProcessesReaped ||
		security.Cleanup.OrphanProcesses != 0 || security.Cleanup.ForeignProcessesKilled != 0 ||
		!security.Cleanup.OwnedDirectoriesOnly {
		return Report{}, errors.New("release gate security matrix is incomplete")
	}

	evaluatedAt := bootstrap.GeneratedAt
	if input.product.GeneratedAt.After(evaluatedAt) {
		evaluatedAt = input.product.GeneratedAt
	}
	if security.CompletedAt.After(evaluatedAt) {
		evaluatedAt = security.CompletedAt
	}
	report := Report{ProtocolVersion: ProtocolVersion, Issue: IssueNumber,
		Status: StatusPassed, EvaluatedAt: evaluatedAt.UTC(),
		Candidate: CandidateEvidence{Version: candidate.Version, Revision: candidate.Revision,
			BinarySHA256: input.binarySHA, BinarySizeBytes: input.binarySize,
			ArchiveSHA256: input.archiveSHA, ArchiveSizeBytes: input.archiveSize,
			PortableManifestSHA256: input.manifestSHA,
			ReleaseMetadataSHA256:  input.metadataSHA,
			FixtureManifestSHA256:  bootstrap.FixtureSet.ManifestSHA256,
			AttackMatrixSHA256:     bootstrap.FixtureSet.AttackMatrixSHA256},
		Components: ComponentEvidence{
			Bootstrap: ProducerEvidence{ProtocolVersion: bootstrap.ProtocolVersion,
				Issue: bootstrap.Issue, Status: bootstrap.BootstrapStatus,
				ArtifactSHA256: input.bootstrapArtifactSHA},
			Product: ProducerEvidence{ProtocolVersion: input.product.ProtocolVersion,
				Issue: input.product.Issue, Status: input.product.Status,
				ArtifactSHA256: input.productArtifactSHA,
				ProducerSHA256: input.product.EvidenceSHA256,
				ChainSHA256:    input.product.RunbookSHA256},
			Security: ProducerEvidence{ProtocolVersion: security.ProtocolVersion,
				Issue: security.Issue, Status: security.Status,
				ArtifactSHA256: input.securityArtifactSHA,
				ProducerSHA256: security.ReportSHA256, ChainSHA256: security.ChainSHA256}},
		Coverage: CoverageEvidence{FixtureRepositories: bootstrap.FixtureSet.RepositoryCount,
			Languages:                 append([]string(nil), input.product.Coverage.Languages...),
			Backends:                  append([]string(nil), input.product.Coverage.Backends...),
			DeliverySurfaces:          append([]string(nil), input.product.Coverage.Surfaces...),
			EdgeCases:                 append([]string(nil), input.product.Coverage.EdgeCases...),
			OperatingSystems:          append([]string(nil), input.product.Coverage.OperatingSystems...),
			DPIPercents:               append([]int(nil), input.product.Coverage.DPIPercents...),
			ProductScenarios:          len(input.product.Scenarios),
			ProductRealFailureRetries: input.product.Coverage.RealFailureRetries,
			ProductRealProcessJobs:    input.product.Coverage.RealProcessJobs,
			ProductContinuityCases:    len(input.product.Continuity),
			ProductPlatformRows:       len(input.product.Platforms),
			SecurityRequiredCases:     security.Summary.RequiredCaseCount,
			SecurityRequiredRuns:      security.Summary.RequiredBackendRuns,
			SecurityPassedRuns:        security.Summary.PassedBackendRuns},
		Safeguards: SafeguardEvidence{NetworkDisabled: input.product.Safeguards.NetworkDisabled,
			CredentialsAbsent:       input.product.Safeguards.CredentialsAbsent,
			DangerFullAccessEnabled: input.product.Safeguards.DangerFullAccess,
			DebugMaximumEnabled:     input.product.Safeguards.DebugMaximumAccess,
			FullCDPDebugEnabled:     input.product.Safeguards.FullCDPDebug,
			SourceOverwrite:         input.product.Safeguards.SourceOverwrite,
			FakeRunnerAccepted:      input.product.Safeguards.FakeRunnerAccepted,
			OrphanProcesses:         security.Cleanup.OrphanProcesses,
			ForeignProcessesKilled:  security.Cleanup.ForeignProcessesKilled,
			SkipAccepted:            input.product.Safeguards.SkipAccepted,
			WaiverAccepted:          input.product.Safeguards.WaiverAccepted},
		Gate: GateEvidence{CandidateBound: true, BootstrapPassed: true,
			ProductMatrixComplete: true, SecurityMatrixComplete: true,
			WindowsMatrixComplete: true, ProducerSelfHashesValid: true,
			MissingEvidenceIsFailure: true, UnexecutedIsNeverPass: true,
			ReleaseAuthorized: true}}
	return report, nil
}

func validateBootstrap(report bootstrapReport) error {
	if report.ProtocolVersion != packagede2e.PackagedE2EProtocol || report.Issue != IssueNumber ||
		report.BootstrapStatus != "pass" || report.ReleaseGateStatus != "needs_full_matrix" ||
		report.GeneratedAt.IsZero() || !validText(report.Candidate.Version, 128) ||
		!gitObjectPattern.MatchString(report.Candidate.Revision) ||
		!validDigest(report.Candidate.BinarySHA256) || !validDigest(report.Candidate.ArchiveSHA256) ||
		report.Candidate.SourceDateEpoch <= 0 ||
		report.FixtureSet.ProtocolVersion != packagede2e.FixtureSetProtocol ||
		!validDigest(report.FixtureSet.ManifestSHA256) ||
		!validDigest(report.FixtureSet.AttackMatrixSHA256) ||
		report.FixtureSet.RepositoryCount != 4 || report.FixtureSet.AttackCaseCount != 40 ||
		!report.FixtureSet.OracleVerified || !report.FixtureSet.AllAttackCasesBound {
		return errors.New("release gate bootstrap evidence is incomplete")
	}
	expectedResults := []string{"candidate_provenance", "fixed_repository_oracle",
		"exact_package_extraction", "packaged_default_start",
		"packaged_operator_preview_kill_reopen", "fixed_repositories_immutable",
		"credential_sentinel_non_persistence", "candidate_process_cleanup",
		"owned_harness_cleanup"}
	if len(report.Results) != len(expectedResults) {
		return errors.New("release gate bootstrap result set is incomplete")
	}
	seen := map[string]bool{}
	for _, result := range report.Results {
		if result.Status != "pass" || seen[result.ID] || !contains(expectedResults, result.ID) ||
			len(result.Facts) == 0 || !json.Valid(result.Facts) {
			return errors.New("release gate bootstrap result set is invalid")
		}
		seen[result.ID] = true
	}
	matrix := report.AttackMatrix
	if matrix.RequiredCaseCount != 40 || matrix.PreparedCaseCount != 40 ||
		matrix.EvidencedCaseCount != 0 || matrix.RemainingRequiredCaseCount != 40 ||
		matrix.Status != "needs_full_matrix" || matrix.FailurePolicy != "fail_closed_no_waiver" ||
		!matrix.UnexecutedCasesAreNotPassOrSkip {
		return errors.New("release gate bootstrap attack matrix state is invalid")
	}
	return nil
}

func (report Report) Seal() (Report, error) {
	report.ReportSHA256 = ""
	digest, err := reportDigest(report)
	if err != nil {
		return Report{}, err
	}
	report.ReportSHA256 = digest
	if err := report.Validate(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (report Report) Validate() error {
	if report.ProtocolVersion != ProtocolVersion || report.Issue != IssueNumber ||
		report.Status != StatusPassed || report.EvaluatedAt.IsZero() ||
		!validDigest(report.ReportSHA256) || !gitObjectPattern.MatchString(report.Candidate.Revision) ||
		!validText(report.Candidate.Version, 128) || report.Candidate.BinarySizeBytes <= 0 ||
		report.Candidate.ArchiveSizeBytes <= 0 {
		return errors.New("release gate report envelope is invalid")
	}
	for _, digest := range []string{report.Candidate.BinarySHA256, report.Candidate.ArchiveSHA256,
		report.Candidate.PortableManifestSHA256, report.Candidate.ReleaseMetadataSHA256,
		report.Candidate.FixtureManifestSHA256, report.Candidate.AttackMatrixSHA256,
		report.Components.Bootstrap.ArtifactSHA256, report.Components.Product.ArtifactSHA256,
		report.Components.Product.ProducerSHA256, report.Components.Product.ChainSHA256,
		report.Components.Security.ArtifactSHA256,
		report.Components.Security.ProducerSHA256, report.Components.Security.ChainSHA256} {
		if !validDigest(digest) {
			return errors.New("release gate report digest is invalid")
		}
	}
	if report.Components.Bootstrap.ProtocolVersion != packagede2e.PackagedE2EProtocol ||
		report.Components.Bootstrap.Issue != IssueNumber || report.Components.Bootstrap.Status != "pass" ||
		report.Components.Product.ProtocolVersion != producte2e.ReportProtocol ||
		report.Components.Product.Issue != producte2e.IssueNumber ||
		report.Components.Product.Status != "pass" ||
		report.Components.Security.ProtocolVersion != packagede2e.StandardCodeSecurityEvidenceProtocol ||
		report.Components.Security.Issue != packagede2e.SecurityEvidenceIssue ||
		report.Components.Security.Status != packagede2e.SecurityEvidencePassed {
		return errors.New("release gate component identity is invalid")
	}
	if !sameStrings(report.Coverage.Languages, []string{"go", "node", "python", "rust"}) ||
		!sameStrings(report.Coverage.Backends, []string{"local", "docker"}) ||
		!sameStrings(report.Coverage.DeliverySurfaces, []string{"desktop", "cli", "http", "handoff", "final"}) ||
		!sameStrings(report.Coverage.EdgeCases, []string{"chinese_path", "space_path", "long_path",
			"crlf", "dirty_tracked", "untracked", "binary", "concurrent_edit"}) ||
		!sameStrings(report.Coverage.OperatingSystems, []string{"windows_10", "windows_11"}) ||
		len(report.Coverage.DPIPercents) != 2 || report.Coverage.DPIPercents[0] != 100 ||
		report.Coverage.DPIPercents[1] != 200 ||
		report.Coverage.FixtureRepositories != 4 || report.Coverage.ProductScenarios < 4 ||
		report.Coverage.ProductScenarios > 8 || report.Coverage.ProductRealFailureRetries < 4 ||
		report.Coverage.ProductRealProcessJobs < 8 || report.Coverage.ProductContinuityCases != 4 ||
		report.Coverage.ProductPlatformRows != 4 || report.Coverage.SecurityRequiredCases != 40 ||
		report.Coverage.SecurityRequiredRuns != 75 || report.Coverage.SecurityPassedRuns != 75 {
		return errors.New("release gate coverage is incomplete")
	}
	if !report.Safeguards.NetworkDisabled || !report.Safeguards.CredentialsAbsent ||
		report.Safeguards.DangerFullAccessEnabled || report.Safeguards.DebugMaximumEnabled ||
		report.Safeguards.FullCDPDebugEnabled || report.Safeguards.SourceOverwrite ||
		report.Safeguards.FakeRunnerAccepted ||
		report.Safeguards.OrphanProcesses != 0 || report.Safeguards.ForeignProcessesKilled != 0 ||
		report.Safeguards.SkipAccepted || report.Safeguards.WaiverAccepted {
		return errors.New("release gate safeguards are incomplete")
	}
	if !report.Gate.CandidateBound || !report.Gate.BootstrapPassed ||
		!report.Gate.ProductMatrixComplete || !report.Gate.SecurityMatrixComplete ||
		!report.Gate.WindowsMatrixComplete || !report.Gate.ProducerSelfHashesValid ||
		!report.Gate.MissingEvidenceIsFailure || !report.Gate.UnexecutedIsNeverPass ||
		!report.Gate.ReleaseAuthorized {
		return errors.New("release gate verdict is incomplete")
	}
	probe := report
	probe.ReportSHA256 = ""
	digest, err := reportDigest(probe)
	if err != nil || digest != report.ReportSHA256 {
		return errors.New("release gate report digest does not match")
	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > maximumReportBytes || !utf8.Valid(encoded) {
		return errors.New("release gate report exceeds its bound")
	}
	return nil
}

func WriteReport(path string, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	resolved, err := newOutputPath(path)
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	file, err := os.OpenFile(resolved, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create release gate report: %w", err)
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

// VerifyReport recomputes the exact deterministic aggregate and requires the
// existing report to be byte-semantically identical.
func VerifyReport(path string, expected Report) error {
	content, _, err := readEvidenceFile(path)
	if err != nil {
		return err
	}
	var actual Report
	if err := decodeStrictJSON(content, &actual); err != nil {
		return err
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("release gate report does not match recomputed evidence")
	}
	return nil
}

func readEvidenceFile(path string) ([]byte, string, error) {
	resolved, info, err := regularFile(path)
	if err != nil {
		return nil, "", err
	}
	if info.Size() <= 0 || info.Size() > maximumEvidenceBytes {
		return nil, "", errors.New("evidence file size is invalid")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, "", err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, "", errors.New("evidence file identity changed before read")
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maximumEvidenceBytes+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	pathAfter, pathErr := os.Lstat(resolved)
	if readErr != nil {
		return nil, "", readErr
	}
	if statErr != nil || pathErr != nil || closeErr != nil ||
		!stableRegularFile(opened, after, pathAfter) || int64(len(content)) != opened.Size() {
		return nil, "", errors.New("evidence file changed while being read")
	}
	if !utf8.Valid(content) {
		return nil, "", errors.New("evidence file is not UTF-8")
	}
	return content, digestBytes(content), nil
}

func digestRegularFile(path string, maximum int64) (string, int64, error) {
	resolved, info, err := regularFile(path)
	if err != nil {
		return "", 0, err
	}
	if info.Size() <= 0 || info.Size() > maximum {
		return "", 0, errors.New("candidate file size is invalid")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", 0, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return "", 0, errors.New("candidate file identity changed before read")
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, maximum+1))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	pathAfter, pathErr := os.Lstat(resolved)
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	if afterErr != nil || pathErr != nil || written != opened.Size() ||
		!stableRegularFile(opened, after, pathAfter) {
		return "", 0, errors.New("candidate file changed while being read")
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func stableRegularFile(before, after, pathAfter os.FileInfo) bool {
	return before != nil && after != nil && pathAfter != nil &&
		before.Mode().IsRegular() && after.Mode().IsRegular() && pathAfter.Mode().IsRegular() &&
		before.Mode()&os.ModeSymlink == 0 && after.Mode()&os.ModeSymlink == 0 &&
		pathAfter.Mode()&os.ModeSymlink == 0 && os.SameFile(before, after) &&
		os.SameFile(after, pathAfter) && before.Size() == after.Size() &&
		before.Size() == pathAfter.Size() && before.ModTime().Equal(after.ModTime()) &&
		before.ModTime().Equal(pathAfter.ModTime())
}

func regularFile(path string) (string, os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil, errors.New("file path is required")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, errors.New("file must be regular and not a symlink")
	}
	return resolved, info, nil
}

func newOutputPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("release gate report path is required")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(resolved); !os.IsNotExist(err) {
		if err == nil {
			return "", errors.New("release gate report already exists")
		}
		return "", err
	}
	parent, err := os.Lstat(filepath.Dir(resolved))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("release gate report parent is invalid")
	}
	return resolved, nil
}

func decodeStrictJSON(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func reportDigest(report Report) (string, error) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func digestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validText(value string, maximum int) bool {
	return value == strings.TrimSpace(value) && value != "" && utf8.ValidString(value) &&
		len(value) <= maximum && !strings.ContainsAny(value, "\r\n\x00")
}

func contains(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func sameStrings(actual, expected []string) bool {
	left, right := append([]string(nil), actual...), append([]string(nil), expected...)
	sort.Strings(left)
	sort.Strings(right)
	return reflect.DeepEqual(left, right)
}
