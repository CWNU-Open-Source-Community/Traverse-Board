package packagede2e

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStandardCodeSecurityEvidenceRequiresRealExactMatrixCoverage(t *testing.T) {
	report := validSecurityEvidenceReport(t)
	if err := ValidateStandardCodeSecurityEvidence(report); err != nil {
		t.Fatal(err)
	}
	if report.Status != SecurityEvidencePassed || report.Summary.RequiredCaseCount != 40 ||
		report.Summary.RequiredBackendRuns <= report.Summary.RequiredCaseCount ||
		report.Summary.PassedBackendRuns != report.Summary.RequiredBackendRuns ||
		report.Summary.FailedBackendRuns != 0 || report.Summary.UnexecutedBackendRuns != 0 {
		t.Fatalf("unexpected security summary: %+v", report.Summary)
	}

	missing := report
	missing.Cases = append([]SecurityAttackCaseEvidence(nil), report.Cases[:39]...)
	if err := FinalizeStandardCodeSecurityEvidence(&missing); err == nil {
		t.Fatal("incomplete frozen matrix was accepted")
	}

	synthetic := validSecurityEvidenceReportBeforeFinalize(t)
	synthetic.Cases[0].Backends[0].ActualExecution = false
	if err := FinalizeStandardCodeSecurityEvidence(&synthetic); err == nil {
		t.Fatal("an unexecuted backend result was accepted as pass")
	}

	mismatch := validSecurityEvidenceReportBeforeFinalize(t)
	mismatch.Cases[0].Backends[0].ActualSignal = "permission_denied"
	if err := FinalizeStandardCodeSecurityEvidence(&mismatch); err == nil {
		t.Fatal("a mismatched expected signal was accepted as pass")
	}
}

func TestStandardCodeSecurityEvidenceFailsClosedWhenDockerIsUnavailable(t *testing.T) {
	report := validSecurityEvidenceReportBeforeFinalize(t)
	report.Backends[1].Availability = SecurityBackendUnavailable
	report.Backends[1].UnavailableSignal = "approval_required"
	report.Backends[1].ApprovalFallback = true
	for caseIndex := range report.Cases {
		for backendIndex := range report.Cases[caseIndex].Backends {
			result := &report.Cases[caseIndex].Backends[backendIndex]
			if result.Backend == "docker" {
				result.Status = SecurityEvidenceFailed
				result.ActualExecution = false
				result.ActualOutcome = "propose"
				result.ActualSignal = "approval_required"
				result.DiagnosticCode = "backend.unavailable"
			}
		}
	}
	if err := FinalizeStandardCodeSecurityEvidence(&report); err != nil {
		t.Fatal(err)
	}
	if report.Status != SecurityEvidenceFailed || report.Summary.FailedBackendRuns == 0 ||
		report.Summary.UnexecutedBackendRuns == 0 || report.Backends[1].FullAccessEnabled {
		t.Fatalf("unavailable Docker did not fail closed: %+v", report)
	}
}

func TestStandardCodeSecurityEvidenceRejectsEvidenceAndChainTampering(t *testing.T) {
	report := validSecurityEvidenceReport(t)
	report.Cases[0].Backends[0].Evidence = report.Cases[0].Backends[0].Evidence[1:]
	if err := ValidateStandardCodeSecurityEvidence(report); err == nil {
		t.Fatal("missing required evidence was accepted")
	}

	report = validSecurityEvidenceReport(t)
	report.Cases[0].Backends[0].RecordSHA256 = strings.Repeat("f", 64)
	if err := ValidateStandardCodeSecurityEvidence(report); err == nil {
		t.Fatal("tampered append-only case chain was accepted")
	}

	report = validSecurityEvidenceReportBeforeFinalize(t)
	report.Cases[0].Backends[0].Evidence[0].Source = "harness_assertion"
	if err := FinalizeStandardCodeSecurityEvidence(&report); err == nil {
		t.Fatal("synthetic harness assertion was accepted as product evidence")
	}
}

func TestStandardCodeSecurityEvidenceIsCreateExclusiveAndPathFree(t *testing.T) {
	report := validSecurityEvidenceReport(t)
	output := filepath.Join(t.TempDir(), "security-evidence.json")
	if err := WriteStandardCodeSecurityEvidence(output, report); err != nil {
		t.Fatal(err)
	}
	if err := WriteStandardCodeSecurityEvidence(output, report); err == nil {
		t.Fatal("security evidence writer overwrote an existing report")
	}

	leaked := validSecurityEvidenceReportBeforeFinalize(t)
	if runtime.GOOS == "windows" {
		leaked.Candidate.OSVersion = `C:\Users\operator\private`
	} else {
		leaked.Candidate.OSVersion = "/home/operator/private"
	}
	if err := FinalizeStandardCodeSecurityEvidence(&leaked); err == nil {
		t.Fatal("private absolute path was accepted in evidence")
	}
}

func validSecurityEvidenceReport(t *testing.T) StandardCodeSecurityEvidence {
	t.Helper()
	report := validSecurityEvidenceReportBeforeFinalize(t)
	if err := FinalizeStandardCodeSecurityEvidence(&report); err != nil {
		t.Fatal(err)
	}
	return report
}

func validSecurityEvidenceReportBeforeFinalize(t *testing.T) StandardCodeSecurityEvidence {
	t.Helper()
	definition, err := LoadDefinition()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)
	report := StandardCodeSecurityEvidence{
		Candidate: SecurityCandidateEvidence{
			SourceCommit: strings.Repeat("a", 40), ExecutableSHA256: strings.Repeat("b", 64),
			ArchiveSHA256: strings.Repeat("c", 64), ArchiveName: "Prayu-portable-v0.1.0-windows-amd64.zip",
			FixtureSHA256: definition.ManifestSHA256, AttackMatrixSHA256: definition.MatrixSHA256,
			OperatingSystem: runtime.GOOS, OSVersion: "test-os-build", Architecture: runtime.GOARCH,
		},
		Backends: []SecurityBackendEvidence{
			{Backend: "local", Availability: SecurityBackendReady,
				IdentitySHA256: strings.Repeat("d", 64), GenerationSHA256: strings.Repeat("e", 64),
				Network: "disabled", Credentials: "none"},
			{Backend: "docker", Availability: SecurityBackendReady,
				IdentitySHA256: strings.Repeat("f", 64), GenerationSHA256: strings.Repeat("1", 64),
				Network: "disabled", Credentials: "none"},
		},
		Cleanup: SecurityCleanupEvidence{OwnedRootSHA256: strings.Repeat("2", 64),
			OwnedProcessesStarted: 4, OwnedProcessesReaped: 4, OwnedDirectoriesOnly: true},
		StartedAt: now, CompletedAt: now.Add(time.Minute),
	}
	for caseIndex, attack := range definition.AttackMatrix.Cases {
		current := SecurityAttackCaseEvidence{ID: attack.ID, Category: attack.Category,
			Phase: attack.Phase, ExpectedOutcome: attack.ExpectedOutcome,
			ExpectedSignal: attack.ExpectedSignal}
		for backendIndex, backend := range attack.Backends {
			result := SecurityCaseBackendEvidence{Backend: backend,
				FixtureID: attack.FixtureIDs[(caseIndex+backendIndex)%len(attack.FixtureIDs)],
				Status:    SecurityEvidencePassed, ActualOutcome: attack.ExpectedOutcome,
				ActualSignal: attack.ExpectedSignal, ActualExecution: true,
				OperatorCode:   "standard_code.attack." + attack.ExpectedSignal,
				DiagnosticCode: "product.observed", StartedAt: now,
				CompletedAt: now.Add(time.Second)}
			for _, kind := range attack.RequiredEvidence {
				digest, digestErr := securityEvidenceDigest([]string{attack.ID, backend, kind})
				if digestErr != nil {
					t.Fatal(digestErr)
				}
				result.Evidence = append(result.Evidence, SecurityEvidenceRef{
					Kind: kind, Source: securityEvidenceSource(kind), SHA256: digest})
			}
			current.Backends = append(current.Backends, result)
		}
		report.Cases = append(report.Cases, current)
	}
	return report
}

func securityEvidenceSource(kind string) string {
	switch kind {
	case "operator_ui":
		return "desktop.projection"
	case "immutable_event":
		return "run.event"
	case "workspace_digest":
		return "drydock.observation"
	case "process_receipt":
		return "command_runtime.receipt"
	case "network_observation":
		return "sandbox.network"
	case "artifact_digest":
		return "artifact.store"
	case "thread_transcript":
		return "thread.projection"
	case "checkpoint":
		return "workspace.checkpoint"
	default:
		return "product.evidence"
	}
}
