// Package uievidence defines the durable, source-bound UI verification
// protocol. Browser content is untrusted evidence: none of these records grant
// process, network, credential, browser-profile, or verification authority.
package uievidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runner"
)

const (
	ProtocolVersion         = "ui-evidence.v1"
	AttemptProtocolVersion  = "ui-evidence-attempt.v1"
	StepProtocolVersion     = "ui-evidence-step.v1"
	ArtifactProtocolVersion = "ui-evidence-artifact.v1"
	DriverProtocolVersion   = "restricted-cdp-ui-evidence.v1"

	MaxSteps                = 128
	MaxMasks                = 32
	MaxSelectorBytes        = 2 * 1024
	MaxPageStateBytes       = 8 * 1024
	MaxArtifactBytes        = 32 * 1024 * 1024
	MaxAttemptArtifactBytes = 128 * 1024 * 1024
	MaxArtifactStoreBytes   = 2 * 1024 * 1024 * 1024
	MaxScreenshotWidth      = 7680
	MaxScreenshotHeight     = 4320
)

type Status string

const (
	StatusNotRun      Status = "not_run"
	StatusRunning     Status = "running"
	StatusPassed      Status = "passed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusTimedOut    Status = "timed_out"
	StatusInterrupted Status = "interrupted"
)

func (s Status) Valid() bool {
	switch s {
	case StatusNotRun, StatusRunning, StatusPassed, StatusFailed,
		StatusCancelled, StatusTimedOut, StatusInterrupted:
		return true
	default:
		return false
	}
}

func (s Status) Terminal() bool {
	return s == StatusPassed || s == StatusFailed || s == StatusCancelled ||
		s == StatusTimedOut || s == StatusInterrupted
}

// Passed is deliberately narrower than "not failed". In particular not_run
// never becomes green verification evidence.
func (s Status) Passed() bool { return s == StatusPassed }

type FailureStage string

const (
	FailureNone       FailureStage = "none"
	FailureBuild      FailureStage = "build"
	FailureLaunch     FailureStage = "launch"
	FailureReadiness  FailureStage = "readiness"
	FailureNavigation FailureStage = "navigation"
	FailureSelector   FailureStage = "selector"
	FailureAssertion  FailureStage = "assertion"
	FailureConsole    FailureStage = "console"
	FailureNetwork    FailureStage = "network"
	FailureCapture    FailureStage = "capture"
	FailureCleanup    FailureStage = "cleanup"
)

func (s FailureStage) Valid() bool {
	switch s {
	case FailureNone, FailureBuild, FailureLaunch, FailureReadiness,
		FailureNavigation, FailureSelector, FailureAssertion, FailureConsole,
		FailureNetwork, FailureCapture, FailureCleanup:
		return true
	default:
		return false
	}
}

type StepKind string

const (
	StepNavigate      StepKind = "navigate"
	StepClick         StepKind = "click"
	StepType          StepKind = "type"
	StepAssertPresent StepKind = "assert_present"
	StepAssertAbsent  StepKind = "assert_absent"
	StepCapture       StepKind = "capture"
)

func (k StepKind) Valid() bool {
	switch k {
	case StepNavigate, StepClick, StepType, StepAssertPresent,
		StepAssertAbsent, StepCapture:
		return true
	default:
		return false
	}
}

type ArtifactKind string

const (
	ArtifactScreenshot    ArtifactKind = "screenshot"
	ArtifactDOM           ArtifactKind = "dom"
	ArtifactAccessibility ArtifactKind = "accessibility"
	ArtifactConsole       ArtifactKind = "console"
	ArtifactNetwork       ArtifactKind = "network"
	ArtifactPerformance   ArtifactKind = "performance"
	ArtifactVideo         ArtifactKind = "video"
)

func (k ArtifactKind) Valid() bool {
	switch k {
	case ArtifactScreenshot, ArtifactDOM, ArtifactAccessibility,
		ArtifactConsole, ArtifactNetwork, ArtifactPerformance, ArtifactVideo:
		return true
	default:
		return false
	}
}

type ArtifactRetentionPolicy string

const ArtifactRetentionRunHistory ArtifactRetentionPolicy = "run_history"

func (p ArtifactRetentionPolicy) Valid() bool {
	return p == ArtifactRetentionRunHistory
}

type Theme string

const (
	ThemeLight Theme = "light"
	ThemeDark  Theme = "dark"
)

func (t Theme) Valid() bool { return t == ThemeLight || t == ThemeDark }

type SourceBinding struct {
	RepositoryKind  string `json:"repository_kind"`
	Commit          string `json:"commit"`
	Branch          string `json:"branch,omitempty"`
	Dirty           bool   `json:"dirty"`
	DirtyDigest     string `json:"dirty_digest"`
	RootFingerprint string `json:"root_fingerprint"`
	IndexSHA256     string `json:"index_sha256"`
	ManifestSHA256  string `json:"manifest_sha256"`
}

func (s SourceBinding) Validate() error {
	if s.RepositoryKind != "git" && s.RepositoryKind != "non_git" {
		return errors.New("UI evidence repository kind is invalid")
	}
	if s.RepositoryKind == "git" {
		if s.Commit == "unborn" || s.Commit == "non-git" || !validCommit(s.Commit) {
			return errors.New("UI evidence source commit is invalid")
		}
	} else if s.Commit != "non-git" {
		return errors.New("non-Git UI evidence source is inconsistent")
	}
	if len(s.Branch) > 255 || strings.ContainsRune(s.Branch, 0) ||
		!validDigest(s.DirtyDigest) || !validDigest(s.RootFingerprint) ||
		!validDigest(s.IndexSHA256) || !validDigest(s.ManifestSHA256) {
		return errors.New("UI evidence source binding is invalid")
	}
	return nil
}

// CommandRecipe is the persisted, secret-free projection of one exact
// command-runtime.v2 resolved spec. Host paths and environment values are
// represented only by hashes; canonical argv remains reviewable.
type CommandRecipe struct {
	ProtocolVersion      string   `json:"protocol_version"`
	Profile              string   `json:"profile"`
	ExecutableName       string   `json:"executable_name"`
	ExecutablePathSHA256 string   `json:"executable_path_sha256"`
	ExecutableSHA256     string   `json:"executable_sha256"`
	CanonicalArgv        []string `json:"canonical_argv"`
	WorkingDirectory     string   `json:"working_directory"`
	EnvironmentNames     []string `json:"environment_names"`
	EnvironmentSHA256    string   `json:"environment_sha256"`
	TimeoutMilliseconds  int64    `json:"timeout_milliseconds"`
	Network              string   `json:"network"`
	Credentials          string   `json:"credentials"`
	Purpose              string   `json:"purpose"`
	Fingerprint          string   `json:"fingerprint"`
}

func CommandRecipeFromResolved(spec runner.CommandRuntimeResolvedSpec) (CommandRecipe, error) {
	resolved, err := runner.NormalizeCommandRuntimeSpec(spec.Spec, spec.WorkspaceRoot)
	if err != nil {
		return CommandRecipe{}, err
	}
	if runner.CommandRuntimeSpecFingerprint(resolved) !=
		runner.CommandRuntimeSpecFingerprint(spec) {
		return CommandRecipe{}, errors.New("UI evidence command recipe lost its resolved identity")
	}
	spec = resolved
	names := make([]string, 0, len(spec.Spec.Environment))
	for _, entry := range spec.Spec.Environment {
		names = append(names, entry.Name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	pathDigest := sha256.Sum256([]byte(filepath.Clean(spec.ExecutablePath)))
	recipe := CommandRecipe{
		ProtocolVersion: runner.CommandRuntimeProtocolVersion,
		Profile:         string(spec.Spec.Profile), ExecutableName: filepath.Base(spec.ExecutablePath),
		ExecutablePathSHA256: hex.EncodeToString(pathDigest[:]),
		ExecutableSHA256:     spec.ExecutableSHA256,
		CanonicalArgv:        append([]string(nil), spec.CanonicalArgv...),
		WorkingDirectory:     spec.Spec.WorkingDirectory,
		EnvironmentNames:     names, EnvironmentSHA256: spec.EnvironmentSHA256,
		TimeoutMilliseconds: spec.Spec.TimeoutMilliseconds,
		Network:             string(spec.Spec.Network), Credentials: string(spec.Spec.Credentials),
		Purpose: spec.Spec.Purpose,
	}
	recipe.Fingerprint = fingerprint(recipe)
	if err := recipe.Validate(); err != nil {
		return CommandRecipe{}, err
	}
	return recipe, nil
}

func (r CommandRecipe) Validate() error {
	if r.ProtocolVersion != runner.CommandRuntimeProtocolVersion ||
		(r.Profile != string(runner.CommandRuntimePowerShell) &&
			r.Profile != string(runner.CommandRuntimeBash) &&
			r.Profile != string(runner.CommandRuntimeProcess)) ||
		!validText(r.ExecutableName, 512, false) ||
		strings.ContainsAny(r.ExecutableName, `/\\`) ||
		!validDigest(r.ExecutablePathSHA256) || !validDigest(r.ExecutableSHA256) ||
		!validDigest(r.EnvironmentSHA256) || r.TimeoutMilliseconds < 1 ||
		r.TimeoutMilliseconds > int64((30*time.Minute)/time.Millisecond) ||
		r.Network != string(runner.CommandRuntimeNetworkDisabled) ||
		r.Credentials != string(runner.CommandRuntimeCredentialsNone) ||
		!validText(r.WorkingDirectory, 4096, false) ||
		!validText(r.Purpose, 1200, false) || redact.String(r.Purpose) != r.Purpose {
		return errors.New("UI evidence command recipe is invalid")
	}
	if len(r.CanonicalArgv) == 0 || len(r.CanonicalArgv) > 128 ||
		len(r.EnvironmentNames) > 32 {
		return errors.New("UI evidence command recipe bounds are invalid")
	}
	for _, value := range r.CanonicalArgv {
		if !validText(value, 65536, true) || redact.String(value) != value {
			return errors.New("UI evidence command recipe argv is invalid")
		}
	}
	previous := ""
	for _, value := range r.EnvironmentNames {
		key := strings.ToLower(value)
		if !validText(value, 128, false) || key <= previous {
			return errors.New("UI evidence environment names are invalid")
		}
		previous = key
	}
	if r.Fingerprint != fingerprint(r) {
		return errors.New("UI evidence command recipe fingerprint is invalid")
	}
	return nil
}

func SealCommandRecipe(recipe CommandRecipe) (CommandRecipe, error) {
	recipe.Fingerprint = fingerprint(recipe)
	return recipe, recipe.Validate()
}

type Readiness struct {
	URL                  string `json:"url"`
	Method               string `json:"method"`
	ExpectedStatus       []int  `json:"expected_status"`
	TimeoutMilliseconds  int64  `json:"timeout_milliseconds"`
	IntervalMilliseconds int64  `json:"interval_milliseconds"`
}

func (r Readiness) Validate() error {
	if !validLoopbackHTTPURL(r.URL) || r.Method != "GET" ||
		r.TimeoutMilliseconds < 100 || r.TimeoutMilliseconds > 120000 ||
		r.IntervalMilliseconds < 10 || r.IntervalMilliseconds > 5000 ||
		len(r.ExpectedStatus) == 0 || len(r.ExpectedStatus) > 16 {
		return errors.New("UI evidence readiness contract is invalid")
	}
	previous := 0
	for _, status := range r.ExpectedStatus {
		if status < 100 || status > 599 || status <= previous {
			return errors.New("UI evidence readiness statuses are invalid")
		}
		previous = status
	}
	return nil
}

type Viewport struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	DPR    float64 `json:"dpr"`
}

func (v Viewport) Validate() error {
	if v.Width < 320 || v.Width > MaxScreenshotWidth ||
		v.Height < 240 || v.Height > MaxScreenshotHeight ||
		math.IsNaN(v.DPR) || math.IsInf(v.DPR, 0) || v.DPR < 0.5 || v.DPR > 4 {
		return errors.New("UI evidence viewport is invalid")
	}
	if float64(v.Width)*v.DPR > MaxScreenshotWidth ||
		float64(v.Height)*v.DPR > MaxScreenshotHeight {
		return errors.New("UI evidence viewport pixel surface exceeds the screenshot limit")
	}
	return nil
}

type Environment struct {
	Viewport      Viewport `json:"viewport"`
	Locale        string   `json:"locale"`
	Theme         Theme    `json:"theme"`
	ReducedMotion bool     `json:"reduced_motion"`
}

func (e Environment) Validate() error {
	if err := e.Viewport.Validate(); err != nil {
		return err
	}
	if !validLocale(e.Locale) || !e.Theme.Valid() {
		return errors.New("UI evidence presentation environment is invalid")
	}
	return nil
}

type Fixture struct {
	Name          string `json:"name"`
	Seed          string `json:"seed"`
	PageState     string `json:"page_state"`
	DataSHA256    string `json:"data_sha256"`
	Deterministic bool   `json:"deterministic"`
	Synthetic     bool   `json:"synthetic"`
}

func (f Fixture) Validate() error {
	if !validText(f.Name, 256, false) || !validText(f.Seed, 256, false) ||
		!validText(f.PageState, MaxPageStateBytes, true) || !validDigest(f.DataSHA256) ||
		!f.Deterministic || redact.String(f.Name+f.Seed+f.PageState) != f.Name+f.Seed+f.PageState {
		return errors.New("UI evidence fixture is invalid")
	}
	return nil
}

type Step struct {
	ID           string   `json:"id"`
	Kind         StepKind `json:"kind"`
	Selector     string   `json:"selector,omitempty"`
	InputSHA256  string   `json:"input_sha256,omitempty"`
	CaptureAfter bool     `json:"capture_after"`
}

func (s Step) Validate() error {
	if !validIdentity(s.ID) || !s.Kind.Valid() {
		return errors.New("UI evidence step identity is invalid")
	}
	requiresSelector := s.Kind == StepClick || s.Kind == StepType ||
		s.Kind == StepAssertPresent || s.Kind == StepAssertAbsent
	if requiresSelector != (s.Selector != "") ||
		(s.Selector != "" && (!validText(s.Selector, MaxSelectorBytes, false) ||
			redact.String(s.Selector) != s.Selector)) {
		return errors.New("UI evidence step selector is invalid")
	}
	if s.Kind == StepType {
		if !validDigest(s.InputSHA256) {
			return errors.New("UI evidence typed input digest is invalid")
		}
	} else if s.InputSHA256 != "" {
		return errors.New("UI evidence non-input step contains an input digest")
	}
	return nil
}

type CapturePolicy struct {
	Screenshot    bool     `json:"screenshot"`
	DOM           bool     `json:"dom"`
	Accessibility bool     `json:"accessibility"`
	Console       bool     `json:"console"`
	Network       bool     `json:"network"`
	Performance   bool     `json:"performance"`
	Video         bool     `json:"video"`
	MaskSelectors []string `json:"mask_selectors"`
}

func (p CapturePolicy) Validate() error {
	if !p.Screenshot && !p.DOM && !p.Accessibility && !p.Console &&
		!p.Network && !p.Performance && !p.Video {
		return errors.New("UI evidence capture policy is empty")
	}
	if len(p.MaskSelectors) > MaxMasks {
		return errors.New("UI evidence mask selector limit exceeded")
	}
	seen := map[string]struct{}{}
	for _, selector := range p.MaskSelectors {
		if !validText(selector, MaxSelectorBytes, false) ||
			redact.String(selector) != selector {
			return errors.New("UI evidence mask selector is invalid")
		}
		if _, exists := seen[selector]; exists {
			return errors.New("UI evidence mask selector is duplicated")
		}
		seen[selector] = struct{}{}
	}
	return nil
}

type FailurePolicy struct {
	FailOnConsoleError bool `json:"fail_on_console_error"`
	FailOnPageError    bool `json:"fail_on_page_error"`
	FailOnRequestError bool `json:"fail_on_request_error"`
	FailOnHTTPStatus   bool `json:"fail_on_http_status"`
}

func (p FailurePolicy) Validate() error {
	if !p.FailOnConsoleError || !p.FailOnPageError || !p.FailOnRequestError ||
		!p.FailOnHTTPStatus {
		return errors.New("UI evidence failure policy must fail closed")
	}
	return nil
}

type BrowserIdentity struct {
	Product          string `json:"product"`
	Version          string `json:"version"`
	ExecutableSHA256 string `json:"executable_sha256"`
	DriverProtocol   string `json:"driver_protocol"`
	Headless         bool   `json:"headless"`
	TemporaryProfile bool   `json:"temporary_profile"`
}

func (b BrowserIdentity) Validate() error {
	if !validText(b.Product, 128, false) || !validText(b.Version, 128, false) ||
		!validDigest(b.ExecutableSHA256) || b.DriverProtocol != DriverProtocolVersion ||
		!b.TemporaryProfile {
		return errors.New("UI evidence browser identity is invalid")
	}
	return nil
}

type EvidenceAuthority struct {
	ProcessStart     bool `json:"process_start"`
	NetworkAccess    bool `json:"network_access"`
	CredentialAccess bool `json:"credential_access"`
	PersonalProfile  bool `json:"personal_profile"`
	RequestMutation  bool `json:"request_mutation"`
	VerificationPass bool `json:"verification_pass"`
}

func (a EvidenceAuthority) Validate() error {
	if a.ProcessStart || a.NetworkAccess || a.CredentialAccess ||
		a.PersonalProfile || a.RequestMutation || a.VerificationPass {
		return errors.New("UI evidence cannot carry authority")
	}
	return nil
}

type Manifest struct {
	ProtocolVersion string            `json:"protocol_version"`
	AttemptID       string            `json:"attempt_id"`
	RunID           string            `json:"run_id"`
	MissionID       string            `json:"mission_id"`
	SessionID       string            `json:"session_id"`
	WorkspaceID     string            `json:"workspace_id"`
	Source          SourceBinding     `json:"source"`
	Build           *CommandRecipe    `json:"build,omitempty"`
	Start           CommandRecipe     `json:"start"`
	Readiness       Readiness         `json:"readiness"`
	Browser         BrowserIdentity   `json:"browser"`
	URL             string            `json:"url"`
	Route           string            `json:"route"`
	Environment     Environment       `json:"environment"`
	Fixture         Fixture           `json:"fixture"`
	Steps           []Step            `json:"steps"`
	Capture         CapturePolicy     `json:"capture"`
	FailurePolicy   FailurePolicy     `json:"failure_policy"`
	Authority       EvidenceAuthority `json:"authority"`
	CreatedAt       time.Time         `json:"created_at"`
	Fingerprint     string            `json:"fingerprint"`
}

func (m Manifest) Validate() error {
	if m.ProtocolVersion != ProtocolVersion || !validIdentity(m.AttemptID) ||
		!validIdentity(m.RunID) || !validIdentity(m.MissionID) ||
		!validIdentity(m.SessionID) || !validIdentity(m.WorkspaceID) ||
		m.CreatedAt.IsZero() || m.Fingerprint != fingerprint(m) {
		return errors.New("UI evidence manifest identity is invalid")
	}
	if err := m.Source.Validate(); err != nil {
		return err
	}
	if m.Build != nil {
		if err := m.Build.Validate(); err != nil {
			return err
		}
	}
	if err := m.Start.Validate(); err != nil {
		return err
	}
	if err := m.Readiness.Validate(); err != nil {
		return err
	}
	if err := m.Browser.Validate(); err != nil {
		return err
	}
	if !validLoopbackHTTPURL(m.URL) || !sameLoopbackOrigin(m.URL, m.Readiness.URL) ||
		!validRoute(m.Route) || routeForURL(m.URL) != m.Route {
		return errors.New("UI evidence target is invalid")
	}
	if err := m.Environment.Validate(); err != nil {
		return err
	}
	if err := m.Fixture.Validate(); err != nil {
		return err
	}
	if err := m.Capture.Validate(); err != nil {
		return err
	}
	if !m.Capture.Screenshot || !m.Capture.DOM || !m.Capture.Accessibility ||
		!m.Capture.Console || !m.Capture.Network || !m.Capture.Performance ||
		m.Capture.Video {
		return errors.New("UI evidence v1 requires every core capture and rejects video")
	}
	if err := m.FailurePolicy.Validate(); err != nil {
		return err
	}
	if err := m.Authority.Validate(); err != nil {
		return err
	}
	if len(m.Steps) == 0 || len(m.Steps) > MaxSteps || m.Steps[0].Kind != StepNavigate {
		return errors.New("UI evidence steps must begin with navigation")
	}
	seen := map[string]struct{}{}
	for _, step := range m.Steps {
		if err := step.Validate(); err != nil {
			return err
		}
		if _, exists := seen[step.ID]; exists {
			return errors.New("UI evidence step identity is duplicated")
		}
		seen[step.ID] = struct{}{}
	}
	return nil
}

func SealManifest(manifest Manifest) (Manifest, error) {
	manifest.ProtocolVersion = ProtocolVersion
	manifest.CreatedAt = manifest.CreatedAt.UTC()
	manifest.Fingerprint = fingerprint(manifest)
	return manifest, manifest.Validate()
}

type DiagnosticsSummary struct {
	ConsoleWarnings int `json:"console_warnings"`
	ConsoleErrors   int `json:"console_errors"`
	PageErrors      int `json:"page_errors"`
	FailedRequests  int `json:"failed_requests"`
	HTTPFailures    int `json:"http_failures"`
	AllowedRequests int `json:"allowed_requests"`
	BlockedRequests int `json:"blocked_requests"`
}

func (d DiagnosticsSummary) Validate() error {
	for _, value := range []int{d.ConsoleWarnings, d.ConsoleErrors, d.PageErrors,
		d.FailedRequests, d.HTTPFailures, d.AllowedRequests, d.BlockedRequests} {
		if value < 0 || value > 1_000_000 {
			return errors.New("UI evidence diagnostic count is invalid")
		}
	}
	return nil
}

type CleanupReceipt struct {
	BrowserTreeReaped     bool `json:"browser_tree_reaped"`
	ApplicationTreeReaped bool `json:"application_tree_reaped"`
	ProfileRemoved        bool `json:"profile_removed"`
	NetworkReleased       bool `json:"network_released"`
	PortReleased          bool `json:"port_released"`
}

func (c CleanupReceipt) Complete() bool {
	return c.BrowserTreeReaped && c.ApplicationTreeReaped && c.ProfileRemoved &&
		c.NetworkReleased && c.PortReleased
}

type Attempt struct {
	ProtocolVersion    string             `json:"protocol_version"`
	Manifest           Manifest           `json:"manifest"`
	OperationDigest    string             `json:"operation_digest"`
	RequestFingerprint string             `json:"request_fingerprint"`
	Status             Status             `json:"status"`
	FailureStage       FailureStage       `json:"failure_stage"`
	FailureCode        string             `json:"failure_code,omitempty"`
	FailureMessage     string             `json:"failure_message,omitempty"`
	Diagnostics        DiagnosticsSummary `json:"diagnostics"`
	Cleanup            CleanupReceipt     `json:"cleanup"`
	ArtifactCount      int                `json:"artifact_count"`
	ArtifactBytes      int64              `json:"artifact_bytes"`
	Version            int64              `json:"version"`
	CreatedAt          time.Time          `json:"created_at"`
	StartedAt          *time.Time         `json:"started_at,omitempty"`
	CompletedAt        *time.Time         `json:"completed_at,omitempty"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

func (a Attempt) Validate() error {
	if a.ProtocolVersion != AttemptProtocolVersion || a.Manifest.Validate() != nil ||
		!validDigest(a.OperationDigest) || !validDigest(a.RequestFingerprint) ||
		a.RequestFingerprint != a.Manifest.Fingerprint ||
		!a.Status.Valid() || !a.FailureStage.Valid() || a.Version < 1 ||
		a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() || a.UpdatedAt.Before(a.CreatedAt) ||
		a.ArtifactCount < 0 || a.ArtifactCount > 10000 || a.ArtifactBytes < 0 ||
		a.ArtifactBytes > MaxAttemptArtifactBytes ||
		(a.ArtifactCount == 0) != (a.ArtifactBytes == 0) ||
		!validOptionalText(a.FailureCode, 128, false) ||
		!validOptionalText(a.FailureMessage, 2048, true) ||
		redact.String(a.FailureMessage) != a.FailureMessage || a.Diagnostics.Validate() != nil {
		return errors.New("UI evidence attempt is invalid")
	}
	if a.Status == StatusNotRun {
		if a.Version != 1 || a.StartedAt != nil || a.CompletedAt != nil ||
			a.FailureStage != FailureNone ||
			a.FailureCode != "" || a.FailureMessage != "" ||
			a.Diagnostics != (DiagnosticsSummary{}) || a.Cleanup != (CleanupReceipt{}) ||
			a.ArtifactCount != 0 || a.ArtifactBytes != 0 {
			return errors.New("not-run UI evidence contains execution state")
		}
		return nil
	}
	if a.StartedAt == nil || a.StartedAt.IsZero() || a.StartedAt.Before(a.CreatedAt) {
		return errors.New("executed UI evidence is missing its start time")
	}
	if a.Status == StatusRunning {
		if a.Version != 2 || a.CompletedAt != nil || a.FailureStage != FailureNone ||
			a.FailureCode != "" || a.FailureMessage != "" ||
			a.Diagnostics != (DiagnosticsSummary{}) || a.Cleanup != (CleanupReceipt{}) ||
			a.ArtifactCount != 0 || a.ArtifactBytes != 0 {
			return errors.New("running UI evidence contains terminal state")
		}
		return nil
	}
	if !a.Status.Terminal() || a.Version != 3 || a.CompletedAt == nil || a.CompletedAt.IsZero() ||
		a.CompletedAt.Before(*a.StartedAt) || a.UpdatedAt.Before(*a.CompletedAt) {
		return errors.New("terminal UI evidence timing is invalid")
	}
	if a.Status == StatusPassed {
		if a.FailureStage != FailureNone || a.FailureCode != "" ||
			a.FailureMessage != "" || !a.Cleanup.Complete() ||
			a.ArtifactCount < 1 || a.ArtifactBytes < 1 ||
			a.Diagnostics.ConsoleErrors != 0 || a.Diagnostics.PageErrors != 0 ||
			a.Diagnostics.FailedRequests != 0 || a.Diagnostics.HTTPFailures != 0 ||
			a.Diagnostics.BlockedRequests != 0 {
			return errors.New("passed UI evidence is not clean and fail-closed")
		}
	} else if a.FailureStage == FailureNone || a.FailureCode == "" {
		return errors.New("unsuccessful UI evidence lacks a failure classification")
	}
	return nil
}

type StepReceipt struct {
	ProtocolVersion string       `json:"protocol_version"`
	AttemptID       string       `json:"attempt_id"`
	StepID          string       `json:"step_id"`
	Sequence        int          `json:"sequence"`
	Kind            StepKind     `json:"kind"`
	Status          Status       `json:"status"`
	FailureStage    FailureStage `json:"failure_stage"`
	Message         string       `json:"message,omitempty"`
	StartedAt       time.Time    `json:"started_at"`
	CompletedAt     time.Time    `json:"completed_at"`
	Fingerprint     string       `json:"fingerprint"`
}

func (r StepReceipt) Validate() error {
	if r.ProtocolVersion != StepProtocolVersion || !validIdentity(r.AttemptID) ||
		!validIdentity(r.StepID) || r.Sequence < 1 || r.Sequence > MaxSteps ||
		!r.Kind.Valid() || (r.Status != StatusPassed && r.Status != StatusFailed &&
		r.Status != StatusCancelled && r.Status != StatusTimedOut) ||
		!r.FailureStage.Valid() || !validOptionalText(r.Message, 2048, true) ||
		redact.String(r.Message) != r.Message || r.StartedAt.IsZero() ||
		r.CompletedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) ||
		r.Fingerprint != fingerprint(r) {
		return errors.New("UI evidence step receipt is invalid")
	}
	if (r.Status == StatusPassed) != (r.FailureStage == FailureNone) {
		return errors.New("UI evidence step outcome is inconsistent")
	}
	return nil
}

func SealStepReceipt(receipt StepReceipt) (StepReceipt, error) {
	receipt.ProtocolVersion = StepProtocolVersion
	receipt.StartedAt = receipt.StartedAt.UTC()
	receipt.CompletedAt = receipt.CompletedAt.UTC()
	receipt.Fingerprint = fingerprint(receipt)
	return receipt, receipt.Validate()
}

type ArtifactMetadata struct {
	ProtocolVersion string                  `json:"protocol_version"`
	ID              string                  `json:"id"`
	AttemptID       string                  `json:"attempt_id"`
	RunID           string                  `json:"run_id"`
	StepID          string                  `json:"step_id"`
	Kind            ArtifactKind            `json:"kind"`
	MIME            string                  `json:"mime"`
	SHA256          string                  `json:"sha256"`
	Bytes           int64                   `json:"bytes"`
	Width           int                     `json:"width,omitempty"`
	Height          int                     `json:"height,omitempty"`
	Viewport        Viewport                `json:"viewport"`
	SourceCommit    string                  `json:"source_commit"`
	RetentionPolicy ArtifactRetentionPolicy `json:"retention_policy"`
	Redacted        bool                    `json:"redacted"`
	Untrusted       bool                    `json:"untrusted"`
	CreatedAt       time.Time               `json:"created_at"`
	Fingerprint     string                  `json:"fingerprint"`
}

func (m ArtifactMetadata) Validate() error {
	if m.ProtocolVersion != ArtifactProtocolVersion || !validIdentity(m.ID) ||
		!validIdentity(m.AttemptID) || !validIdentity(m.RunID) ||
		!validIdentity(m.StepID) || !m.Kind.Valid() || m.Kind == ArtifactVideo ||
		!validText(m.MIME, 128, false) || !validDigest(m.SHA256) ||
		m.Bytes < 1 || m.Bytes > MaxArtifactBytes || m.CreatedAt.IsZero() ||
		!m.Untrusted || m.Fingerprint != fingerprint(m) || m.Viewport.Validate() != nil ||
		!validCommit(m.SourceCommit) || !m.RetentionPolicy.Valid() {
		return errors.New("UI evidence artifact metadata is invalid")
	}
	if m.Kind == ArtifactScreenshot {
		if m.MIME != "image/png" || m.Width < 1 || m.Height < 1 ||
			m.Width > MaxScreenshotWidth || m.Height > MaxScreenshotHeight ||
			math.Abs(float64(m.Width)-float64(m.Viewport.Width)*m.Viewport.DPR) > 1 ||
			math.Abs(float64(m.Height)-float64(m.Viewport.Height)*m.Viewport.DPR) > 1 {
			return errors.New("UI evidence screenshot metadata is invalid")
		}
	} else if m.Width != 0 || m.Height != 0 {
		return errors.New("non-image UI evidence artifact has dimensions")
	}
	return nil
}

type Artifact struct {
	Metadata ArtifactMetadata
	Content  []byte
}

type ListFilter struct {
	RunID  string
	Status Status
	Limit  int
}

func (f ListFilter) Validate() error {
	if f.Limit < 0 || f.Limit > 500 ||
		(f.RunID != "" && !validIdentity(f.RunID)) ||
		(f.Status != "" && !f.Status.Valid()) {
		return errors.New("UI evidence list filter is invalid")
	}
	return nil
}

func (a Artifact) Validate() error {
	if err := a.Metadata.Validate(); err != nil {
		return err
	}
	if int64(len(a.Content)) != a.Metadata.Bytes {
		return errors.New("UI evidence artifact byte count is inconsistent")
	}
	digest := sha256.Sum256(a.Content)
	if hex.EncodeToString(digest[:]) != a.Metadata.SHA256 {
		return errors.New("UI evidence artifact integrity check failed")
	}
	return nil
}

func SealArtifact(metadata ArtifactMetadata, content []byte) (Artifact, error) {
	digest := sha256.Sum256(content)
	metadata.ProtocolVersion = ArtifactProtocolVersion
	metadata.CreatedAt = metadata.CreatedAt.UTC()
	metadata.Bytes = int64(len(content))
	metadata.SHA256 = hex.EncodeToString(digest[:])
	metadata.Fingerprint = fingerprint(metadata)
	artifact := Artifact{Metadata: metadata, Content: append([]byte(nil), content...)}
	return artifact, artifact.Validate()
}

func fingerprint(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var canonical any
	if json.Unmarshal(raw, &canonical) != nil {
		return ""
	}
	if object, ok := canonical.(map[string]any); ok {
		delete(object, "fingerprint")
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validCommit(value string) bool {
	if value == "unborn" || value == "non-git" {
		return true
	}
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func validIdentity(value string) bool {
	return value == strings.TrimSpace(value) && domain.ValidAgentID(value)
}

func validOptionalText(value string, maxBytes int, lines bool) bool {
	return value == "" || validText(value, maxBytes, lines)
}

func validText(value string, maxBytes int, lines bool) bool {
	if value == "" || len([]byte(value)) > maxBytes || !utf8.ValidString(value) ||
		strings.ContainsRune(value, 0) || value != strings.TrimSpace(value) {
		return false
	}
	if !lines && strings.ContainsAny(value, "\r\n") {
		return false
	}
	return true
}

func validLocale(value string) bool {
	if !validText(value, 64, false) {
		return false
	}
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 3 || len(parts[0]) < 2 || len(parts[0]) > 8 {
		return false
	}
	for _, part := range parts {
		for _, current := range part {
			if current < 'A' || current > 'Z' {
				if current < 'a' || current > 'z' {
					if current < '0' || current > '9' {
						return false
					}
				}
			}
		}
	}
	return true
}

func validRoute(value string) bool {
	return validText(value, 4096, false) && strings.HasPrefix(value, "/") &&
		!strings.HasPrefix(value, "//")
}

func validLoopbackHTTPURL(value string) bool {
	if len(value) > 4096 || strings.ContainsAny(value, "\r\n\t") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Hostname() == "" || parsed.Port() == "" || parsed.Path == "" {
		return false
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	if err != nil || !address.Unmap().IsLoopback() {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	return err == nil && port >= 1 && port <= 65535
}

func sameLoopbackOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	return leftErr == nil && rightErr == nil && leftURL.Scheme == rightURL.Scheme &&
		strings.EqualFold(leftURL.Host, rightURL.Host)
}

func routeForURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.EscapedPath() == "" {
		return ""
	}
	return parsed.EscapedPath()
}

func InputSHA256(value string) (string, error) {
	if !validText(value, 64*1024, true) || redact.String(value) != value {
		return "", fmt.Errorf("UI evidence input is invalid or contains secret-like material")
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:]), nil
}
