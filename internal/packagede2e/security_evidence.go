package packagede2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	StandardCodeSecurityEvidenceProtocol = "standard_code_packaged_security_evidence.v1"
	SecurityEvidenceIssue                = 181
	SecurityEvidenceParentIssue          = 140

	SecurityEvidencePassed = "passed"
	SecurityEvidenceFailed = "failed"

	SecurityBackendReady       = "ready"
	SecurityBackendUnavailable = "unavailable"
)

var securityCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,127}$`)

// StandardCodeSecurityEvidence is the independent Issue #181 evidence
// producer. The release owner may aggregate this document, but this protocol
// cannot declare the parent Issue #140 release gate passed.
type StandardCodeSecurityEvidence struct {
	ProtocolVersion string                       `json:"protocol_version"`
	Issue           int                          `json:"issue"`
	ParentIssue     int                          `json:"parent_issue"`
	Status          string                       `json:"status"`
	Candidate       SecurityCandidateEvidence    `json:"candidate"`
	Backends        []SecurityBackendEvidence    `json:"backends"`
	Cases           []SecurityAttackCaseEvidence `json:"cases"`
	Summary         SecurityEvidenceSummary      `json:"summary"`
	Cleanup         SecurityCleanupEvidence      `json:"cleanup"`
	ChainSHA256     string                       `json:"chain_sha256"`
	ReportSHA256    string                       `json:"report_sha256"`
	StartedAt       time.Time                    `json:"started_at"`
	CompletedAt     time.Time                    `json:"completed_at"`
}

type SecurityCandidateEvidence struct {
	SourceCommit       string `json:"source_commit"`
	ExecutableSHA256   string `json:"executable_sha256"`
	ArchiveSHA256      string `json:"archive_sha256"`
	ArchiveName        string `json:"archive_name"`
	FixtureSHA256      string `json:"fixture_manifest_sha256"`
	AttackMatrixSHA256 string `json:"attack_matrix_sha256"`
	OperatingSystem    string `json:"operating_system"`
	OSVersion          string `json:"os_version"`
	Architecture       string `json:"architecture"`
}

type SecurityBackendEvidence struct {
	Backend           string `json:"backend"`
	Availability      string `json:"availability"`
	IdentitySHA256    string `json:"identity_sha256"`
	GenerationSHA256  string `json:"generation_sha256"`
	Network           string `json:"network"`
	Credentials       string `json:"credentials"`
	UnavailableSignal string `json:"unavailable_signal,omitempty"`
	ApprovalFallback  bool   `json:"approval_fallback"`
	FullAccessEnabled bool   `json:"full_access_enabled"`
}

type SecurityAttackCaseEvidence struct {
	ID              string                        `json:"id"`
	Category        string                        `json:"category"`
	Phase           string                        `json:"phase"`
	ExpectedOutcome string                        `json:"expected_outcome"`
	ExpectedSignal  string                        `json:"expected_signal"`
	Backends        []SecurityCaseBackendEvidence `json:"backends"`
}

type SecurityCaseBackendEvidence struct {
	Backend         string                `json:"backend"`
	FixtureID       string                `json:"fixture_id"`
	Status          string                `json:"status"`
	ActualOutcome   string                `json:"actual_outcome"`
	ActualSignal    string                `json:"actual_signal"`
	OperatorCode    string                `json:"operator_code"`
	DiagnosticCode  string                `json:"diagnostic_code"`
	ActualExecution bool                  `json:"actual_execution"`
	Evidence        []SecurityEvidenceRef `json:"evidence"`
	PreviousSHA256  string                `json:"previous_sha256"`
	RecordSHA256    string                `json:"record_sha256"`
	StartedAt       time.Time             `json:"started_at"`
	CompletedAt     time.Time             `json:"completed_at"`
}

type SecurityEvidenceRef struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

type SecurityEvidenceSummary struct {
	RequiredCaseCount     int `json:"required_case_count"`
	RequiredBackendRuns   int `json:"required_backend_runs"`
	PassedBackendRuns     int `json:"passed_backend_runs"`
	FailedBackendRuns     int `json:"failed_backend_runs"`
	UnexecutedBackendRuns int `json:"unexecuted_backend_runs"`
}

type SecurityCleanupEvidence struct {
	OwnedRootSHA256        string `json:"owned_root_sha256"`
	OwnedProcessesStarted  int    `json:"owned_processes_started"`
	OwnedProcessesReaped   int    `json:"owned_processes_reaped"`
	OrphanProcesses        int    `json:"orphan_processes"`
	ForeignProcessesKilled int    `json:"foreign_processes_killed"`
	OwnedDirectoriesOnly   bool   `json:"owned_directories_only"`
}

// FinalizeStandardCodeSecurityEvidence derives the append-only case chain,
// summary, final status, and self hash. Callers cannot supply those verdicts.
func FinalizeStandardCodeSecurityEvidence(report *StandardCodeSecurityEvidence) error {
	if report == nil {
		return errors.New("security evidence report is required")
	}
	report.ProtocolVersion = StandardCodeSecurityEvidenceProtocol
	report.Issue = SecurityEvidenceIssue
	report.ParentIssue = SecurityEvidenceParentIssue
	report.Summary = SecurityEvidenceSummary{}
	report.ChainSHA256 = strings.Repeat("0", 64)
	for caseIndex := range report.Cases {
		for backendIndex := range report.Cases[caseIndex].Backends {
			current := &report.Cases[caseIndex].Backends[backendIndex]
			current.PreviousSHA256 = report.ChainSHA256
			current.RecordSHA256 = ""
			digest, err := securityEvidenceDigest(*current)
			if err != nil {
				return err
			}
			current.RecordSHA256 = digest
			report.ChainSHA256 = digest
			report.Summary.RequiredBackendRuns++
			switch current.Status {
			case SecurityEvidencePassed:
				report.Summary.PassedBackendRuns++
			case SecurityEvidenceFailed:
				report.Summary.FailedBackendRuns++
			}
			if !current.ActualExecution {
				report.Summary.UnexecutedBackendRuns++
			}
		}
	}
	report.Summary.RequiredCaseCount = len(report.Cases)
	report.Status = SecurityEvidencePassed
	if report.Summary.FailedBackendRuns != 0 || report.Summary.UnexecutedBackendRuns != 0 ||
		report.Summary.PassedBackendRuns != report.Summary.RequiredBackendRuns {
		report.Status = SecurityEvidenceFailed
	}
	report.ReportSHA256 = ""
	digest, err := securityEvidenceDigest(*report)
	if err != nil {
		return err
	}
	report.ReportSHA256 = digest
	return ValidateStandardCodeSecurityEvidence(*report)
}

func ValidateStandardCodeSecurityEvidence(report StandardCodeSecurityEvidence) error {
	definition, err := LoadDefinition()
	if err != nil {
		return err
	}
	if report.ProtocolVersion != StandardCodeSecurityEvidenceProtocol ||
		report.Issue != SecurityEvidenceIssue || report.ParentIssue != SecurityEvidenceParentIssue ||
		(report.Status != SecurityEvidencePassed && report.Status != SecurityEvidenceFailed) ||
		!lowercaseDigestPattern.MatchString(report.ChainSHA256) ||
		!lowercaseDigestPattern.MatchString(report.ReportSHA256) ||
		report.StartedAt.IsZero() || report.CompletedAt.Before(report.StartedAt) {
		return errors.New("security evidence header is invalid")
	}
	if err := validateSecurityCandidate(report.Candidate, definition); err != nil {
		return err
	}
	backendState, err := validateSecurityBackends(report.Backends)
	if err != nil {
		return err
	}
	expectedSummary := SecurityEvidenceSummary{RequiredCaseCount: len(definition.AttackMatrix.Cases)}
	previous := strings.Repeat("0", 64)
	if len(report.Cases) != len(definition.AttackMatrix.Cases) {
		return errors.New("security evidence does not cover the frozen matrix")
	}
	for index, attack := range definition.AttackMatrix.Cases {
		current := report.Cases[index]
		if current.ID != attack.ID || current.Category != attack.Category ||
			current.Phase != attack.Phase || current.ExpectedOutcome != attack.ExpectedOutcome ||
			current.ExpectedSignal != attack.ExpectedSignal ||
			len(current.Backends) != len(attack.Backends) {
			return fmt.Errorf("security evidence case %q does not match the frozen matrix", attack.ID)
		}
		for backendIndex, expectedBackend := range attack.Backends {
			result := current.Backends[backendIndex]
			if result.Backend != expectedBackend || !containsString(attack.FixtureIDs, result.FixtureID) ||
				(result.Status != SecurityEvidencePassed && result.Status != SecurityEvidenceFailed) ||
				!securityCodePattern.MatchString(result.OperatorCode) ||
				!securityCodePattern.MatchString(result.DiagnosticCode) ||
				result.PreviousSHA256 != previous || result.StartedAt.IsZero() ||
				result.CompletedAt.Before(result.StartedAt) {
				return fmt.Errorf("security evidence result %q/%q is invalid", attack.ID, expectedBackend)
			}
			recorded := result.RecordSHA256
			result.RecordSHA256 = ""
			digest, digestErr := securityEvidenceDigest(result)
			if digestErr != nil || recorded != digest {
				return fmt.Errorf("security evidence result %q/%q chain is invalid", attack.ID, expectedBackend)
			}
			previous = recorded
			if err := validateSecurityEvidenceRefs(result.Evidence, attack.RequiredEvidence,
				result.Status == SecurityEvidencePassed); err != nil {
				return fmt.Errorf("security evidence result %q/%q: %w", attack.ID, expectedBackend, err)
			}
			if result.Status == SecurityEvidencePassed &&
				(!result.ActualExecution || result.ActualOutcome != attack.ExpectedOutcome ||
					result.ActualSignal != attack.ExpectedSignal ||
					backendState[expectedBackend] != SecurityBackendReady) {
				return fmt.Errorf("security evidence result %q/%q is a synthetic or mismatched pass", attack.ID, expectedBackend)
			}
			expectedSummary.RequiredBackendRuns++
			if result.Status == SecurityEvidencePassed {
				expectedSummary.PassedBackendRuns++
			} else {
				expectedSummary.FailedBackendRuns++
			}
			if !result.ActualExecution {
				expectedSummary.UnexecutedBackendRuns++
			}
		}
	}
	if report.ChainSHA256 != previous || report.Summary != expectedSummary {
		return errors.New("security evidence chain or summary is invalid")
	}
	expectedStatus := SecurityEvidencePassed
	if expectedSummary.FailedBackendRuns != 0 || expectedSummary.UnexecutedBackendRuns != 0 ||
		expectedSummary.PassedBackendRuns != expectedSummary.RequiredBackendRuns {
		expectedStatus = SecurityEvidenceFailed
	}
	if report.Status != expectedStatus {
		return errors.New("security evidence status does not match its results")
	}
	if err := validateSecurityCleanup(report.Cleanup); err != nil {
		return err
	}
	providedDigest := report.ReportSHA256
	report.ReportSHA256 = ""
	digest, err := securityEvidenceDigest(report)
	if err != nil || providedDigest != digest {
		return errors.New("security evidence report digest is invalid")
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if unsafeSecurityEvidenceText(raw) {
		return errors.New("security evidence contains a secret, control sequence, or private path")
	}
	return nil
}

func validateSecurityCandidate(value SecurityCandidateEvidence, definition Definition) error {
	if !lowercaseObjectPattern.MatchString(value.SourceCommit) ||
		!lowercaseDigestPattern.MatchString(value.ExecutableSHA256) ||
		!lowercaseDigestPattern.MatchString(value.ArchiveSHA256) ||
		!lowercaseDigestPattern.MatchString(value.FixtureSHA256) ||
		!lowercaseDigestPattern.MatchString(value.AttackMatrixSHA256) ||
		value.FixtureSHA256 != definition.ManifestSHA256 ||
		value.AttackMatrixSHA256 != definition.MatrixSHA256 ||
		filepath.Base(value.ArchiveName) != value.ArchiveName ||
		!strings.HasSuffix(strings.ToLower(value.ArchiveName), ".zip") ||
		value.OperatingSystem != runtime.GOOS || value.Architecture != runtime.GOARCH ||
		!safeSecurityText(value.OSVersion, 128) {
		return errors.New("security evidence candidate provenance is invalid")
	}
	return nil
}

func validateSecurityBackends(backends []SecurityBackendEvidence) (map[string]string, error) {
	if len(backends) != 2 {
		return nil, errors.New("security evidence requires exact Local and Docker backends")
	}
	state := make(map[string]string, len(backends))
	for index, backend := range backends {
		expected := []string{"local", "docker"}[index]
		if backend.Backend != expected || state[backend.Backend] != "" ||
			(backend.Availability != SecurityBackendReady &&
				backend.Availability != SecurityBackendUnavailable) ||
			!lowercaseDigestPattern.MatchString(backend.IdentitySHA256) ||
			!lowercaseDigestPattern.MatchString(backend.GenerationSHA256) ||
			backend.Network != "disabled" || backend.Credentials != "none" ||
			backend.FullAccessEnabled {
			return nil, fmt.Errorf("security backend %q is invalid", backend.Backend)
		}
		if backend.Availability == SecurityBackendUnavailable {
			if backend.UnavailableSignal != "approval_required" || !backend.ApprovalFallback {
				return nil, fmt.Errorf("security backend %q did not fail closed", backend.Backend)
			}
		} else if backend.UnavailableSignal != "" || backend.ApprovalFallback {
			return nil, fmt.Errorf("ready security backend %q carries fallback authority", backend.Backend)
		}
		state[backend.Backend] = backend.Availability
	}
	return state, nil
}

func validateSecurityEvidenceRefs(refs []SecurityEvidenceRef, required []string,
	requireComplete bool,
) error {
	if len(refs) > 16 || (requireComplete && len(refs) < len(required)) {
		return errors.New("evidence reference count is invalid")
	}
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if seen[ref.Kind] || !containsString(required, ref.Kind) ||
			!securityCodePattern.MatchString(ref.Source) ||
			ref.Source == "harness_assertion" ||
			!lowercaseDigestPattern.MatchString(ref.SHA256) {
			return errors.New("evidence reference is invalid or synthetic")
		}
		seen[ref.Kind] = true
	}
	for _, kind := range required {
		if requireComplete && !seen[kind] {
			return fmt.Errorf("required evidence %q is missing", kind)
		}
	}
	return nil
}

func validateSecurityCleanup(value SecurityCleanupEvidence) error {
	if !lowercaseDigestPattern.MatchString(value.OwnedRootSHA256) ||
		value.OwnedProcessesStarted < 0 || value.OwnedProcessesReaped < 0 ||
		value.OwnedProcessesStarted != value.OwnedProcessesReaped ||
		value.OrphanProcesses != 0 || value.ForeignProcessesKilled != 0 ||
		!value.OwnedDirectoriesOnly {
		return errors.New("security evidence cleanup is incomplete or unsafe")
	}
	return nil
}

func WriteStandardCodeSecurityEvidence(path string, report StandardCodeSecurityEvidence) error {
	if err := ValidateStandardCodeSecurityEvidence(report); err != nil {
		return err
	}
	target, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" ||
		target == filepath.VolumeName(target)+string(filepath.Separator) {
		return errors.New("security evidence output path is invalid")
	}
	parent := filepath.Dir(target)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("security evidence output parent is unavailable or indirect")
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create security evidence: %w", err)
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func securityEvidenceDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func safeSecurityText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		len([]byte(value)) <= maximum && !strings.ContainsAny(value, "\x00\r\n\x1b")
}

func unsafeSecurityEvidenceText(raw []byte) bool {
	if !utf8.Valid(raw) || bytes.Contains(raw, []byte{'\x1b'}) || bytes.ContainsRune(raw, 0) {
		return true
	}
	lower := strings.ToLower(string(raw))
	for _, marker := range []string{
		"-----begin private key-----", "aws_secret_access_key", "authorization: bearer",
		"github_token", "openai_api_key", "ssh_auth_sock", "http_proxy=", "https_proxy=",
		"c:\\\\users\\\\", "/users/", "/home/", "appdata\\\\", "\\\\\\\\.\\\\pipe\\\\",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	index := sort.SearchStrings(appendSortedCopy(values), expected)
	ordered := appendSortedCopy(values)
	return index < len(ordered) && ordered[index] == expected
}

func appendSortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
