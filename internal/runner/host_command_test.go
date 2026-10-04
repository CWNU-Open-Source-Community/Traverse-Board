package runner

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

const hostCommandTestDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestHostCommandSpecSealsExactTransportWithoutEnvironmentValues(t *testing.T) {
	request := hostCommandSpecTestRequest(t)
	spec, err := NewHostCommandSpec(request)
	if err != nil {
		t.Fatal(err)
	}
	if spec.ExecutablePath != request.ExecutablePath ||
		!reflect.DeepEqual(spec.Argv, request.Argv) ||
		!reflect.DeepEqual(spec.EnvironmentKeys, []string{"HOME", "PATH"}) ||
		spec.EnvironmentSHA256 == "" || spec.Fingerprint == "" {
		t.Fatalf("unexpected host command spec: %+v", spec)
	}
	for _, value := range request.Environment {
		encoded, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), value) {
			t.Fatalf("environment value escaped into durable command spec")
		}
	}

	reordered := request
	reordered.Argv = []string{"./...", "test"}
	reorderedSpec, err := NewHostCommandSpec(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if reorderedSpec.Fingerprint == spec.Fingerprint {
		t.Fatal("argument ordering did not affect the command fingerprint")
	}

	changedEnvironment := request
	changedEnvironment.Environment = []string{
		"HOME=" + hostCommandAbsolutePath(t, "other-home"),
		"PATH=" + hostCommandAbsolutePath(t, "other-bin"),
	}
	changedSpec, err := NewHostCommandSpec(changedEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if changedSpec.EnvironmentSHA256 == spec.EnvironmentSHA256 ||
		changedSpec.Fingerprint == spec.Fingerprint {
		t.Fatal("environment value changes were not sealed by the digest")
	}
}

func TestHostCommandSpecRejectsSecretsAndAmbiguousTransport(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HostCommandSpecRequest)
	}{
		{
			name: "relative executable",
			mutate: func(request *HostCommandSpecRequest) {
				request.ExecutablePath = "go"
			},
		},
		{
			name: "nul argument",
			mutate: func(request *HostCommandSpecRequest) {
				request.Argv = []string{"test\x00./..."}
			},
		},
		{
			name: "secret argument",
			mutate: func(request *HostCommandSpecRequest) {
				request.Argv = []string{
					"--token=sk-abcdefghijklmnopqrstuvwxyz123456",
				}
			},
		},
		{
			name: "secret environment name",
			mutate: func(request *HostCommandSpecRequest) {
				request.Environment = []string{
					"API_TOKEN=temporary-value",
					"PATH=" + hostCommandAbsolutePath(t, "bin"),
				}
			},
		},
		{
			name: "secret environment value",
			mutate: func(request *HostCommandSpecRequest) {
				request.Environment = []string{
					"HOME=sk-abcdefghijklmnopqrstuvwxyz123456",
					"PATH=" + hostCommandAbsolutePath(t, "bin"),
				}
			},
		},
		{
			name: "duplicate environment key",
			mutate: func(request *HostCommandSpecRequest) {
				request.Environment = []string{"PATH=one", "path=two"}
			},
		},
		{
			name: "invalid network intent",
			mutate: func(request *HostCommandSpecRequest) {
				request.NetworkIntent = "sandboxed"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := hostCommandSpecTestRequest(t)
			test.mutate(&request)
			if _, err := NewHostCommandSpec(request); err == nil {
				t.Fatal("invalid host command unexpectedly validated")
			}
		})
	}
}

func TestHostCommandProposalAcceptsOnlyCanonicalReviewedShellEnvelopes(t *testing.T) {
	for _, shell := range []struct {
		name       string
		executable string
		command    string
	}{
		{name: "powershell", executable: "pwsh.exe", command: "git status --short"},
		{name: "bash", executable: "bash", command: "git status --short"},
	} {
		t.Run(shell.name, func(t *testing.T) {
			request := hostCommandSpecTestRequest(t)
			request.ExecutablePath = hostCommandAbsolutePath(t, "bin", shell.executable)
			argv, err := CanonicalHostShellArguments(shell.name, shell.command)
			if err != nil {
				t.Fatal(err)
			}
			request.Argv = argv
			spec, err := NewHostCommandSpec(request)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateHostCommandProposalTransport(spec); err != nil {
				t.Fatalf("canonical %s shell was rejected: %v", shell.name, err)
			}
			if err := ValidateHostCommandProcessProposalTransport(spec); err == nil {
				t.Fatalf("canonical %s shell escaped through process transport", shell.name)
			}
			if dialect, ok := HostCommandShellDialect(spec); !ok || dialect != shell.name {
				t.Fatalf("shell classification=%q ok=%t", dialect, ok)
			}

			tampered := request
			tampered.Argv = append([]string(nil), argv...)
			tampered.Argv[0] = "-Interactive"
			tamperedSpec, err := NewHostCommandSpec(tampered)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateHostCommandProposalTransport(tamperedSpec); err == nil {
				t.Fatal("non-canonical shell argv unexpectedly passed approval mode")
			}
			proposal := hostCommandProposalFixture(t)
			proposal.Spec = tamperedSpec
			proposal.Fingerprint = HostCommandProposalFingerprint(proposal)
			if proposal.Validate() == nil {
				t.Fatal("non-canonical shell argv unexpectedly validated in a historical proposal")
			}
		})
	}
	if _, err := CanonicalHostShellArguments("bash", "first\nsecond"); err == nil {
		t.Fatal("multiline shell text unexpectedly produced a canonical argv")
	}
}

func TestHistoricalHostCommandProposalAndReviewRejectAlteredAuthority(t *testing.T) {
	proposal := hostCommandProposalFixture(t)
	if proposal.Validate() != nil {
		t.Fatal("historical proposal did not validate")
	}
	review := hostCommandReviewFixture(proposal)
	if review.Validate() != nil {
		t.Fatal("historical review did not validate")
	}
	tampered := review
	tampered.ProposalFingerprint = hostCommandTestDigest
	if err := tampered.Validate(); err == nil {
		t.Fatal("review detached from its proposal unexpectedly validated")
	}
	for _, reviewer := range []string{"agent", "model", "repository", "skill", "supervisor", "run_supervisor"} {
		tampered := review
		tampered.ReviewedBy = reviewer
		tampered.RequestFingerprint = HostCommandReviewRequestFingerprint(tampered)
		tampered.Fingerprint = HostCommandReviewFingerprint(tampered)
		if err := tampered.Validate(); err == nil {
			t.Fatalf("reserved reviewer %q unexpectedly validated", reviewer)
		}
	}
}

func TestHostPowerShellUTF8EnvelopeRetainsReviewedLegacyIdentity(t *testing.T) {
	const command = "Write-Output '中文'; exit 7"
	request := hostCommandSpecTestRequest(t)
	request.ExecutablePath = hostCommandAbsolutePath(t, "bin", "pwsh.exe")
	request.Argv = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}
	legacy, err := NewHostCommandSpec(request)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(legacy)
	if err := ValidateHostCommandProposalTransport(legacy); err != nil {
		t.Fatalf("stored exact legacy envelope was rejected: %v", err)
	}
	if err := ValidateHostCommandProcessProposalTransport(legacy); err == nil {
		t.Fatal("legacy shell became available through process transport")
	}
	after, _ := json.Marshal(legacy)
	if string(before) != string(after) {
		t.Fatal("legacy argv/fingerprint changed during recognition")
	}
	request.Argv, err = CanonicalHostShellArguments("powershell", command)
	if err != nil {
		t.Fatal(err)
	}
	current, err := NewHostCommandSpec(request)
	if err != nil {
		t.Fatal(err)
	}
	if current.Argv[len(current.Argv)-1] != command || current.Fingerprint == legacy.Fingerprint {
		t.Fatal("UTF-8 initialization was not independently sealed with the exact command")
	}
	request.Argv[len(request.Argv)-2] += " Write-Output unreviewed;"
	tampered, err := NewHostCommandSpec(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHostCommandProposalTransport(tampered); err == nil {
		t.Fatal("noncanonical bootstrap was accepted as a reviewed shell envelope")
	}
}

func TestHostPowerShellRejectsOpeningDeclarationsWithoutInspectingCodeValues(t *testing.T) {
	for _, command := range []string{
		"param([string]$Value='must-not-execute'); Write-Output $Value",
		"[CmdletBinding()] param([string]$Value='must-not-execute'); Write-Output $Value",
		" <# before <# nested #> declaration #> PARAM ([string]$Value); Write-Output $Value",
		"using namespace System.Text; Write-Output must-not-execute",
		"<# before #> using module ./example.psm1; Write-Output must-not-execute",
		"using assembly 'example.dll'; Write-Output must-not-execute",
	} {
		if argv, err := CanonicalHostShellArguments("powershell", command); err == nil || argv != nil || !strings.Contains(err.Error(), ".ps1") {
			t.Fatalf("inline declaration was not rejected with the file alternative: argv=%q error=%v", argv, err)
		}
		legacy := HostCommandSpec{ExecutablePath: hostCommandAbsolutePath(t, "bin", "pwsh.exe"),
			Argv: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}}
		if dialect, ok := HostCommandShellDialect(legacy); !ok || dialect != "powershell" {
			t.Fatal("new construction restriction was retroactively applied to the old envelope")
		}
	}
	for _, command := range []string{
		"Write-Output 'param($Value)'", "'using namespace System.Text'", "# param($Value)",
		"function Example { param($Value) Write-Output $Value }; Example '中文'",
		"& ./declared.ps1", "Write-Output 'using assembly example.dll'",
	} {
		if _, err := CanonicalHostShellArguments("powershell", command); err != nil {
			t.Fatalf("ordinary command was mistaken for a declaration: %q: %v", command, err)
		}
	}
}

func TestHistoricalHostCommandProposalRejectsNonApprovalPermission(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionConservative, domain.RunExecutionPermissionFullAccess} {
		proposal := hostCommandProposalFixture(t)
		proposal.PermissionMode = mode
		proposal.Fingerprint = HostCommandProposalFingerprint(proposal)
		if proposal.Validate() == nil {
			t.Fatalf("permission %q unexpectedly validated in the retired approval protocol", mode)
		}
	}
}

func hostCommandProposalFixture(t *testing.T) HostCommandProposal {
	t.Helper()
	spec, err := NewHostCommandSpec(hostCommandSpecTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	proposal := HostCommandProposal{
		ID: "host-command-proposal", ProtocolVersion: HostCommandProposalProtocolVersion, PolicyVersion: HostCommandPolicyVersion,
		RunID: "run-host-proposal", MissionID: "mission-host-proposal", SessionID: "session-host-command", WorkspaceID: "workspace-host-command",
		RootAgentID: "agent-root-host-command", InteractionSnapshotID: "interaction-host-command", InteractionRevision: 1, ExecutionProfileRevision: 1,
		PermissionSnapshotID: "permission-host-approval", PermissionRevision: 2, PermissionMode: domain.RunExecutionPermissionApproval,
		Spec: spec, RequestedBy: "run_supervisor", CreatedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}
	proposal.Fingerprint = HostCommandProposalFingerprint(proposal)
	return proposal
}

func hostCommandReviewFixture(proposal HostCommandProposal) HostCommandReview {
	review := HostCommandReview{
		ID: "host-command-review", ProtocolVersion: HostCommandReviewProtocolVersion, PolicyVersion: HostCommandPolicyVersion,
		ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint, RunID: proposal.RunID, Decision: HostCommandReviewApprove,
		ReviewedBy: "cli_operator", Reason: "approved after exact command review", OperationKeyDigest: hostCommandTestDigest,
		SingleUseExecutionAuthorized: true, CreatedAt: proposal.CreatedAt,
	}
	review.RequestFingerprint = HostCommandReviewRequestFingerprint(review)
	review.Fingerprint = HostCommandReviewFingerprint(review)
	return review
}

func hostCommandSpecTestRequest(t *testing.T) HostCommandSpecRequest {
	t.Helper()
	return HostCommandSpecRequest{
		ExecutablePath:   hostCommandAbsolutePath(t, "bin", "go"),
		ExecutableSHA256: hostCommandTestDigest,
		Argv:             []string{"test", "./..."},
		WorkingDirectory: hostCommandAbsolutePath(t, "workspace"),
		Environment: []string{
			"PATH=" + hostCommandAbsolutePath(t, "bin"),
			"HOME=" + hostCommandAbsolutePath(t, "home"),
		},
		NetworkIntent:       HostNetworkIntentHost,
		TimeoutMilliseconds: (30 * time.Second).Milliseconds(),
		Purpose:             "run the repository test suite",
	}
}

func hostCommandAbsolutePath(t *testing.T, elements ...string) string {
	t.Helper()
	return filepath.Join(append([]string{t.TempDir()}, elements...)...)
}
