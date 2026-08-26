// Package producte2e validates the packaged Standard Code product slice.
//
// It deliberately consumes durable product facts rather than Agent prose. A
// passing report requires a clean release candidate, the fixed four-language
// oracle, real persisted Command Runtime Jobs and delivery receipts, plus
// candidate-bound surface and Windows UX evidence.
package producte2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/packagede2e"
)

const (
	RunbookProtocol = "standard_code_product_runbook.v1"
	ReportProtocol  = "standard_code_product_e2e.v1"
	IssueNumber     = 182

	maxRunbookBytes = 2 * 1024 * 1024
	maxReportBytes  = 2 * 1024 * 1024
)

var requiredLanguages = []string{"go", "node", "python", "rust"}
var requiredBackends = []string{"local", "docker"}
var requiredSurfaces = []string{"desktop", "cli", "http", "handoff", "final"}
var requiredContinuityCases = []string{"completed", "failed", "approval_wait", "restart"}
var requiredEdgeCases = []string{
	"chinese_path", "space_path", "long_path", "crlf", "dirty_tracked",
	"untracked", "binary", "concurrent_edit",
}

type Runbook struct {
	ProtocolVersion       string                `json:"protocol_version"`
	Issue                 int                   `json:"issue"`
	CandidateSHA256       string                `json:"candidate_sha256"`
	FixtureManifestSHA256 string                `json:"fixture_manifest_sha256"`
	DefaultLaunch         DefaultLaunchEvidence `json:"default_launch"`
	Backends              []BackendEvidence     `json:"backends"`
	Edges                 []EdgeEvidence        `json:"edges"`
	Continuity            []ContinuityEvidence  `json:"continuity"`
	Platforms             []PlatformEvidence    `json:"platforms"`
}

type DefaultLaunchEvidence struct {
	CandidateSHA256           string   `json:"candidate_sha256"`
	Arguments                 []string `json:"arguments"`
	ProviderConfigured        bool     `json:"provider_configured"`
	WorkspaceTrustVisible     bool     `json:"workspace_trust_visible"`
	BackendReadinessVisible   bool     `json:"backend_readiness_visible"`
	StandardCodeStartVisible  bool     `json:"standard_code_start_visible"`
	DangerFullAccessEnabled   bool     `json:"danger_full_access_enabled"`
	DebugMaximumAccessEnabled bool     `json:"debug_maximum_access_enabled"`
	FullCDPDebugEnabled       bool     `json:"full_cdp_debug_enabled"`
	EvidencePath              string   `json:"evidence_path"`
	EvidenceSHA256            string   `json:"evidence_sha256"`
}

type BackendEvidence struct {
	Backend  string            `json:"backend"`
	State    string            `json:"state"`
	Runs     []RunEvidence     `json:"runs"`
	Fallback *FallbackEvidence `json:"fallback,omitempty"`
}

type RunEvidence struct {
	ID          string              `json:"id"`
	Language    string              `json:"language"`
	RunID       string              `json:"run_id"`
	Projections []SurfaceProjection `json:"projections"`
}

type SurfaceProjection struct {
	Surface         string `json:"surface"`
	CandidateSHA256 string `json:"candidate_sha256"`
	RunID           string `json:"run_id"`
	Status          string `json:"status"`
	ReceiptSHA256   string `json:"receipt_sha256"`
	DiffSHA256      string `json:"diff_sha256"`
	CheckpointID    string `json:"checkpoint_id"`
	EvidencePath    string `json:"evidence_path"`
	EvidenceSHA256  string `json:"evidence_sha256"`
}

type FallbackEvidence struct {
	CandidateSHA256       string `json:"candidate_sha256"`
	RunID                 string `json:"run_id"`
	ApprovalID            string `json:"approval_id"`
	ReasonCode            string `json:"reason_code"`
	ReadinessEvidencePath string `json:"readiness_evidence_path"`
	ReadinessEvidenceSHA  string `json:"readiness_evidence_sha256"`
	UIEvidencePath        string `json:"ui_evidence_path"`
	UIEvidenceSHA256      string `json:"ui_evidence_sha256"`
}

type EdgeEvidence struct {
	Kind           string `json:"kind"`
	RunID          string `json:"run_id"`
	Scope          string `json:"scope"`
	Path           string `json:"path"`
	BaselineSHA256 string `json:"baseline_sha256,omitempty"`
	ExpectedSHA256 string `json:"expected_sha256"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

type ContinuityEvidence struct {
	Case                string `json:"case"`
	CandidateSHA256     string `json:"candidate_sha256"`
	ThreadID            string `json:"thread_id"`
	RunID               string `json:"run_id"`
	SuccessorRunID      string `json:"successor_run_id,omitempty"`
	QueuedMessageSHA256 string `json:"queued_message_sha256,omitempty"`
	ComposerEnabled     bool   `json:"composer_enabled"`
	ProcessRestarted    bool   `json:"process_restarted"`
	EvidencePath        string `json:"evidence_path"`
	EvidenceSHA256      string `json:"evidence_sha256"`
}

type PlatformEvidence struct {
	ID                       string `json:"id"`
	CandidateSHA256          string `json:"candidate_sha256"`
	OS                       string `json:"os"`
	Build                    string `json:"build"`
	DPIPercent               int    `json:"dpi_percent"`
	Locale                   string `json:"locale"`
	DefaultLaunchPassed      bool   `json:"default_launch_passed"`
	ChineseIMEPassed         bool   `json:"chinese_ime_passed"`
	KeyboardNavigationPassed bool   `json:"keyboard_navigation_passed"`
	FocusVisiblePassed       bool   `json:"focus_visible_passed"`
	AccessibleNamesPassed    bool   `json:"accessible_names_passed"`
	NoCriticalA11yViolations bool   `json:"no_critical_a11y_violations"`
	EvidencePath             string `json:"evidence_path"`
	EvidenceSHA256           string `json:"evidence_sha256"`
}

type CandidateEvidence struct {
	Version               string `json:"version"`
	Revision              string `json:"revision"`
	BinarySHA256          string `json:"binary_sha256"`
	BinarySizeBytes       int64  `json:"binary_size_bytes"`
	ZipSHA256             string `json:"zip_sha256"`
	ZipSizeBytes          int64  `json:"zip_size_bytes"`
	ManifestSHA256        string `json:"portable_manifest_sha256"`
	ReleaseMetadataSHA256 string `json:"release_metadata_sha256"`
}

type FixtureEvidence struct {
	ProtocolVersion    string `json:"protocol_version"`
	ReportSHA256       string `json:"report_sha256"`
	ManifestSHA256     string `json:"manifest_sha256"`
	AttackMatrixSHA256 string `json:"attack_matrix_sha256"`
	RepositoryCount    int    `json:"repository_count"`
	OracleVerified     bool   `json:"oracle_verified"`
}

type BackendSummary struct {
	Backend        string `json:"backend"`
	State          string `json:"state"`
	PassedRuns     int    `json:"passed_runs"`
	ApprovalID     string `json:"approval_id,omitempty"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	EvidenceSHA256 string `json:"evidence_sha256,omitempty"`
}

type ScenarioSummary struct {
	ID                  string `json:"id"`
	Language            string `json:"language"`
	Backend             string `json:"backend"`
	RunID               string `json:"run_id"`
	ThreadID            string `json:"thread_id"`
	SessionID           string `json:"session_id"`
	FixtureHead         string `json:"fixture_head"`
	ReadRounds          int    `json:"read_rounds"`
	AppliedEdits        int    `json:"applied_edits"`
	FailedJobs          int    `json:"failed_jobs"`
	PassedJobs          int    `json:"passed_jobs"`
	FixRounds           int    `json:"fix_rounds"`
	ArtifactCount       int    `json:"artifact_count"`
	ProjectionCount     int    `json:"projection_count"`
	ReceiptSHA256       string `json:"receipt_sha256"`
	DiffSHA256          string `json:"diff_sha256"`
	CheckpointID        string `json:"checkpoint_id"`
	WorkspaceRevision   string `json:"workspace_revision_sha256"`
	SourceWorkPreserved bool   `json:"source_work_preserved"`
}

type ContinuitySummary struct {
	Case           string `json:"case"`
	ThreadID       string `json:"thread_id"`
	RunID          string `json:"run_id"`
	SuccessorRunID string `json:"successor_run_id,omitempty"`
	Verified       bool   `json:"verified"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

type PlatformSummary struct {
	ID             string `json:"id"`
	OS             string `json:"os"`
	Build          string `json:"build"`
	DPIPercent     int    `json:"dpi_percent"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

type Coverage struct {
	Languages          []string `json:"languages"`
	Backends           []string `json:"backends"`
	Surfaces           []string `json:"surfaces"`
	EdgeCases          []string `json:"edge_cases"`
	ContinuityCases    []string `json:"continuity_cases"`
	OperatingSystems   []string `json:"operating_systems"`
	DPIPercents        []int    `json:"dpi_percents"`
	RealFailureRetries int      `json:"real_failure_retries"`
	RealProcessJobs    int      `json:"real_process_jobs"`
}

type Safeguards struct {
	NetworkDisabled    bool `json:"network_disabled"`
	CredentialsAbsent  bool `json:"credentials_absent"`
	DangerFullAccess   bool `json:"danger_full_access"`
	DebugMaximumAccess bool `json:"debug_maximum_access"`
	FullCDPDebug       bool `json:"full_cdp_debug"`
	SourceOverwrite    bool `json:"source_overwrite"`
	FakeRunnerAccepted bool `json:"fake_runner_accepted"`
	SkipAccepted       bool `json:"skip_accepted"`
	WaiverAccepted     bool `json:"waiver_accepted"`
}

type Report struct {
	ProtocolVersion string              `json:"protocol_version"`
	Issue           int                 `json:"issue"`
	Status          string              `json:"status"`
	GeneratedAt     time.Time           `json:"generated_at"`
	Candidate       CandidateEvidence   `json:"candidate"`
	Fixture         FixtureEvidence     `json:"fixture"`
	Backends        []BackendSummary    `json:"backends"`
	Scenarios       []ScenarioSummary   `json:"scenarios"`
	Continuity      []ContinuitySummary `json:"continuity"`
	Platforms       []PlatformSummary   `json:"platforms"`
	Coverage        Coverage            `json:"coverage"`
	Safeguards      Safeguards          `json:"safeguards"`
	RunbookSHA256   string              `json:"runbook_sha256"`
	EvidenceSHA256  string              `json:"evidence_sha256"`
}

func DecodeRunbook(content []byte) (Runbook, error) {
	if len(content) == 0 || len(content) > maxRunbookBytes {
		return Runbook{}, errors.New("product E2E runbook size is invalid")
	}
	var value Runbook
	if err := decodeStrictJSON(content, &value); err != nil {
		return Runbook{}, fmt.Errorf("decode product E2E runbook: %w", err)
	}
	if err := value.Validate(); err != nil {
		return Runbook{}, err
	}
	return value, nil
}

func (r Runbook) Validate() error {
	if r.ProtocolVersion != RunbookProtocol || r.Issue != IssueNumber ||
		!validDigest(r.CandidateSHA256) || !validDigest(r.FixtureManifestSHA256) {
		return errors.New("product E2E runbook identity is invalid")
	}
	if err := r.DefaultLaunch.validate(r.CandidateSHA256); err != nil {
		return err
	}
	if len(r.Backends) != len(requiredBackends) {
		return errors.New("product E2E runbook must cover Local and Docker backends")
	}
	seenBackends := map[string]bool{}
	seenRunIDs := map[string]bool{}
	languagePass := map[string]bool{}
	for _, backend := range r.Backends {
		if seenBackends[backend.Backend] || !contains(requiredBackends, backend.Backend) {
			return fmt.Errorf("product E2E backend %q is invalid or duplicated", backend.Backend)
		}
		seenBackends[backend.Backend] = true
		if backend.State != "ready" && backend.State != "approval_required" {
			return fmt.Errorf("product E2E backend %q state is invalid", backend.Backend)
		}
		if backend.State == "ready" {
			if backend.Fallback != nil || len(backend.Runs) != len(requiredLanguages) {
				return fmt.Errorf("ready backend %q must contain exactly four runs", backend.Backend)
			}
			seenLanguages := map[string]bool{}
			for _, run := range backend.Runs {
				if seenLanguages[run.Language] || !contains(requiredLanguages, run.Language) ||
					!validIdentity(run.ID) || !validIdentity(run.RunID) || seenRunIDs[run.RunID] {
					return fmt.Errorf("backend %q run %q is invalid", backend.Backend, run.ID)
				}
				seenLanguages[run.Language] = true
				seenRunIDs[run.RunID] = true
				languagePass[run.Language] = true
				if err := validateProjections(run.Projections, r.CandidateSHA256, run.RunID); err != nil {
					return fmt.Errorf("run %q projections: %w", run.ID, err)
				}
			}
		} else {
			if len(backend.Runs) != 0 || backend.Fallback == nil {
				return fmt.Errorf("unavailable backend %q requires one Approval fallback", backend.Backend)
			}
			if err := backend.Fallback.validate(r.CandidateSHA256, backend.Backend); err != nil {
				return err
			}
		}
	}
	for _, language := range requiredLanguages {
		if !languagePass[language] {
			return fmt.Errorf("language %q has no real packaged workflow", language)
		}
	}
	if err := validateEdges(r.Edges, seenRunIDs); err != nil {
		return err
	}
	if err := validateContinuity(r.Continuity, r.CandidateSHA256); err != nil {
		return err
	}
	if err := validatePlatforms(r.Platforms, r.CandidateSHA256); err != nil {
		return err
	}
	return nil
}

func (e DefaultLaunchEvidence) validate(candidate string) error {
	if e.CandidateSHA256 != candidate || len(e.Arguments) != 0 ||
		!e.ProviderConfigured || !e.WorkspaceTrustVisible ||
		!e.BackendReadinessVisible || !e.StandardCodeStartVisible ||
		e.DangerFullAccessEnabled || e.DebugMaximumAccessEnabled ||
		e.FullCDPDebugEnabled || !safeRelativePath(e.EvidencePath) ||
		!validDigest(e.EvidenceSHA256) {
		return errors.New("zero-argument packaged launch evidence is incomplete or unsafe")
	}
	return nil
}

func validateProjections(values []SurfaceProjection, candidate, runID string) error {
	if len(values) != len(requiredSurfaces) {
		return errors.New("all five delivery surfaces are required")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value.Surface] || !contains(requiredSurfaces, value.Surface) ||
			value.CandidateSHA256 != candidate || value.RunID != runID ||
			value.Status != "passed" || !validDigest(value.ReceiptSHA256) ||
			!validDigest(value.DiffSHA256) || !validIdentity(value.CheckpointID) ||
			!safeRelativePath(value.EvidencePath) || !validDigest(value.EvidenceSHA256) {
			return fmt.Errorf("surface %q evidence is invalid", value.Surface)
		}
		seen[value.Surface] = true
	}
	return nil
}

func (e FallbackEvidence) validate(candidate, backend string) error {
	allowedReason := map[string]bool{
		"local_unavailable": true, "docker_unavailable": true,
		"startup_gate_closed": true, "backend_not_ready": true,
	}
	if e.CandidateSHA256 != candidate || !validIdentity(e.RunID) ||
		!validIdentity(e.ApprovalID) || !allowedReason[e.ReasonCode] ||
		!safeRelativePath(e.ReadinessEvidencePath) ||
		!validDigest(e.ReadinessEvidenceSHA) || !safeRelativePath(e.UIEvidencePath) ||
		!validDigest(e.UIEvidenceSHA256) {
		return fmt.Errorf("backend %q Approval fallback evidence is invalid", backend)
	}
	if backend == "local" && e.ReasonCode == "docker_unavailable" ||
		backend == "docker" && e.ReasonCode == "local_unavailable" {
		return fmt.Errorf("backend %q fallback reason is inconsistent", backend)
	}
	return nil
}

func validateEdges(values []EdgeEvidence, runIDs map[string]bool) error {
	if len(values) < len(requiredEdgeCases) || len(values) > 64 {
		return errors.New("product E2E edge evidence count is invalid")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !contains(requiredEdgeCases, value.Kind) || seen[value.Kind] ||
			!runIDs[value.RunID] || (value.Scope != "source" && value.Scope != "drydock") ||
			!safeRelativePath(value.Path) || !validDigest(value.ExpectedSHA256) ||
			!validDigest(value.EvidenceSHA256) || value.EvidenceSHA256 != value.ExpectedSHA256 {
			return fmt.Errorf("edge evidence %q is invalid", value.Kind)
		}
		if value.BaselineSHA256 != "" && !validDigest(value.BaselineSHA256) {
			return fmt.Errorf("edge evidence %q baseline digest is invalid", value.Kind)
		}
		if value.Kind == "concurrent_edit" &&
			(value.Scope != "source" || value.BaselineSHA256 == "" ||
				value.BaselineSHA256 == value.ExpectedSHA256) {
			return errors.New("concurrent edit evidence must preserve a changed source file")
		}
		seen[value.Kind] = true
	}
	for _, kind := range requiredEdgeCases {
		if !seen[kind] {
			return fmt.Errorf("edge case %q is missing", kind)
		}
	}
	return nil
}

func validateContinuity(values []ContinuityEvidence, candidate string) error {
	if len(values) != len(requiredContinuityCases) {
		return errors.New("all four Thread continuity cases are required")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value.Case] || !contains(requiredContinuityCases, value.Case) ||
			value.CandidateSHA256 != candidate || !validIdentity(value.ThreadID) ||
			!validIdentity(value.RunID) || !value.ComposerEnabled ||
			!safeRelativePath(value.EvidencePath) || !validDigest(value.EvidenceSHA256) {
			return fmt.Errorf("Thread continuity case %q is invalid", value.Case)
		}
		if (value.Case == "completed" || value.Case == "failed") &&
			(!validIdentity(value.SuccessorRunID) || value.SuccessorRunID == value.RunID) {
			return fmt.Errorf("terminal continuity case %q requires a successor Run", value.Case)
		}
		if value.Case == "approval_wait" && !validDigest(value.QueuedMessageSHA256) {
			return errors.New("approval-wait continuity requires a queued message digest")
		}
		if value.Case == "restart" && !value.ProcessRestarted {
			return errors.New("restart continuity requires a real process restart")
		}
		seen[value.Case] = true
	}
	return nil
}

func validatePlatforms(values []PlatformEvidence, candidate string) error {
	if len(values) != 4 {
		return errors.New("Windows 10/11 at 100%/200% DPI require four evidence rows")
	}
	seenIDs := map[string]bool{}
	seenMatrix := map[string]bool{}
	for _, value := range values {
		key := fmt.Sprintf("%s/%d", value.OS, value.DPIPercent)
		if !validIdentity(value.ID) || seenIDs[value.ID] || value.CandidateSHA256 != candidate ||
			(value.OS != "windows_10" && value.OS != "windows_11") ||
			(value.DPIPercent != 100 && value.DPIPercent != 200) || seenMatrix[key] ||
			!validText(value.Build, 128) || value.Locale != "zh-CN" ||
			!value.DefaultLaunchPassed || !value.ChineseIMEPassed ||
			!value.KeyboardNavigationPassed || !value.FocusVisiblePassed ||
			!value.AccessibleNamesPassed || !value.NoCriticalA11yViolations ||
			!safeRelativePath(value.EvidencePath) || !validDigest(value.EvidenceSHA256) {
			return fmt.Errorf("platform evidence %q is invalid", value.ID)
		}
		seenIDs[value.ID] = true
		seenMatrix[key] = true
	}
	return nil
}

func (r Report) Seal() (Report, error) {
	r.EvidenceSHA256 = ""
	digest, err := reportDigest(r)
	if err != nil {
		return Report{}, err
	}
	r.EvidenceSHA256 = digest
	if err := r.Validate(); err != nil {
		return Report{}, err
	}
	return r, nil
}

func (r Report) Validate() error {
	if r.ProtocolVersion != ReportProtocol || r.Issue != IssueNumber ||
		r.Status != "pass" || r.GeneratedAt.IsZero() || !validDigest(r.RunbookSHA256) ||
		!validDigest(r.EvidenceSHA256) || len(r.Backends) != 2 ||
		len(r.Scenarios) < 4 || len(r.Scenarios) > 8 || len(r.Continuity) != 4 ||
		len(r.Platforms) != 4 {
		return errors.New("product E2E report envelope is invalid")
	}
	if !validDigest(r.Candidate.BinarySHA256) || !validDigest(r.Candidate.ZipSHA256) ||
		!validDigest(r.Candidate.ManifestSHA256) ||
		!validDigest(r.Candidate.ReleaseMetadataSHA256) ||
		r.Candidate.BinarySizeBytes <= 0 || r.Candidate.ZipSizeBytes <= 0 ||
		!validGitObject(r.Candidate.Revision) || !validText(r.Candidate.Version, 128) {
		return errors.New("product E2E candidate evidence is invalid")
	}
	if r.Fixture.ProtocolVersion != packagede2e.FixtureSetProtocol ||
		!validDigest(r.Fixture.ReportSHA256) ||
		!validDigest(r.Fixture.ManifestSHA256) ||
		!validDigest(r.Fixture.AttackMatrixSHA256) || r.Fixture.RepositoryCount != 4 ||
		!r.Fixture.OracleVerified {
		return errors.New("product E2E fixture oracle evidence is invalid")
	}
	seenBackends := map[string]bool{}
	for _, backend := range r.Backends {
		if seenBackends[backend.Backend] || !contains(requiredBackends, backend.Backend) ||
			(backend.State != "ready" && backend.State != "approval_required") ||
			backend.PassedRuns < 0 || backend.PassedRuns > 4 {
			return errors.New("product E2E backend summary is invalid")
		}
		if backend.State == "ready" && (backend.PassedRuns != 4 ||
			backend.ApprovalID != "" || backend.FallbackReason != "" ||
			backend.EvidenceSHA256 != "") {
			return errors.New("ready product E2E backend summary is inconsistent")
		}
		if backend.State == "approval_required" && (backend.PassedRuns != 0 ||
			!validIdentity(backend.ApprovalID) || backend.FallbackReason == "" ||
			!validDigest(backend.EvidenceSHA256)) {
			return errors.New("Approval product E2E backend summary is inconsistent")
		}
		seenBackends[backend.Backend] = true
	}
	seenScenarios := map[string]bool{}
	seenRunIDs := map[string]bool{}
	languageCoverage := map[string]bool{}
	for _, scenario := range r.Scenarios {
		if !validIdentity(scenario.ID) || seenScenarios[scenario.ID] ||
			!contains(requiredLanguages, scenario.Language) ||
			!contains(requiredBackends, scenario.Backend) ||
			!validIdentity(scenario.RunID) || seenRunIDs[scenario.RunID] ||
			!validIdentity(scenario.ThreadID) || !validIdentity(scenario.SessionID) ||
			!validGitObject(scenario.FixtureHead) ||
			scenario.ReadRounds < 2 || scenario.AppliedEdits < 2 ||
			scenario.FailedJobs < 1 || scenario.PassedJobs < 1 ||
			scenario.FixRounds < 1 || scenario.ArtifactCount < 1 ||
			scenario.ProjectionCount != len(requiredSurfaces) ||
			!validDigest(scenario.ReceiptSHA256) || !validDigest(scenario.DiffSHA256) ||
			!validIdentity(scenario.CheckpointID) || !validDigest(scenario.WorkspaceRevision) ||
			!scenario.SourceWorkPreserved {
			return errors.New("product E2E scenario summary is invalid")
		}
		seenScenarios[scenario.ID] = true
		seenRunIDs[scenario.RunID] = true
		languageCoverage[scenario.Language] = true
	}
	for _, language := range requiredLanguages {
		if !languageCoverage[language] {
			return errors.New("product E2E scenario language coverage is incomplete")
		}
	}
	seenContinuity := map[string]bool{}
	for _, value := range r.Continuity {
		if seenContinuity[value.Case] || !contains(requiredContinuityCases, value.Case) ||
			!validIdentity(value.ThreadID) || !validIdentity(value.RunID) ||
			!value.Verified || !validDigest(value.EvidenceSHA256) ||
			((value.Case == "completed" || value.Case == "failed") &&
				!validIdentity(value.SuccessorRunID)) {
			return errors.New("product E2E continuity summary is invalid")
		}
		seenContinuity[value.Case] = true
	}
	seenPlatforms := map[string]bool{}
	seenPlatformMatrix := map[string]bool{}
	for _, value := range r.Platforms {
		key := fmt.Sprintf("%s/%d", value.OS, value.DPIPercent)
		if !validIdentity(value.ID) || seenPlatforms[value.ID] ||
			(value.OS != "windows_10" && value.OS != "windows_11") ||
			(value.DPIPercent != 100 && value.DPIPercent != 200) ||
			seenPlatformMatrix[key] || !validText(value.Build, 128) ||
			!validDigest(value.EvidenceSHA256) {
			return errors.New("product E2E platform summary is invalid")
		}
		seenPlatforms[value.ID] = true
		seenPlatformMatrix[key] = true
	}
	if !sameStrings(r.Coverage.Languages, requiredLanguages) ||
		!sameStrings(r.Coverage.Backends, requiredBackends) ||
		!sameStrings(r.Coverage.Surfaces, requiredSurfaces) ||
		!sameStrings(r.Coverage.EdgeCases, requiredEdgeCases) ||
		!sameStrings(r.Coverage.ContinuityCases, requiredContinuityCases) ||
		!sameStrings(r.Coverage.OperatingSystems, []string{"windows_10", "windows_11"}) ||
		len(r.Coverage.DPIPercents) != 2 || r.Coverage.DPIPercents[0] != 100 ||
		r.Coverage.DPIPercents[1] != 200 || r.Coverage.RealFailureRetries < 4 ||
		r.Coverage.RealProcessJobs < 8 {
		return errors.New("product E2E coverage is incomplete")
	}
	if !r.Safeguards.NetworkDisabled || !r.Safeguards.CredentialsAbsent ||
		r.Safeguards.DangerFullAccess || r.Safeguards.DebugMaximumAccess ||
		r.Safeguards.FullCDPDebug || r.Safeguards.SourceOverwrite ||
		r.Safeguards.FakeRunnerAccepted || r.Safeguards.SkipAccepted ||
		r.Safeguards.WaiverAccepted {
		return errors.New("product E2E safeguards are incomplete")
	}
	probe := r
	probe.EvidenceSHA256 = ""
	digest, err := reportDigest(probe)
	if err != nil || digest != r.EvidenceSHA256 {
		return errors.New("product E2E report digest does not match")
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > maxReportBytes {
		return errors.New("product E2E report exceeds its bound")
	}
	return nil
}

func reportDigest(value Report) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func RunbookDigest(content []byte) string { return digestBytes(content) }

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

func validGitObject(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value) &&
		(len(decoded) == 20 || len(decoded) == 32)
}

func validIdentity(value string) bool {
	return validText(value, 256) && !strings.ContainsAny(value, "/\\")
}

func validText(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		!strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= limit
}

func safeRelativePath(value string) bool {
	if !validText(value, 1024) || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "/") || value == "." || value == ".." {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." ||
			strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
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
	if len(actual) != len(expected) {
		return false
	}
	seen := make(map[string]bool, len(actual))
	for _, value := range actual {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	for _, value := range expected {
		if !seen[value] {
			return false
		}
	}
	return true
}
