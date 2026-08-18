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
			permission := hostCommandApprovalPermission(t)
			proposalRequest := hostCommandProposalTestRequest(tamperedSpec, permission,
				time.Now().UTC())
			if _, err := NewHostCommandProposal(proposalRequest); err == nil {
				t.Fatal("non-canonical shell argv entered the durable proposal domain")
			}
		})
	}
	if _, err := CanonicalHostShellArguments("bash", "first\nsecond"); err == nil {
		t.Fatal("multiline shell text unexpectedly produced a canonical argv")
	}
}

func TestHostCommandProposalRequiresApprovalSnapshotAndExactReview(t *testing.T) {
	proposal := hostCommandProposalFixture(t)
	if proposal.ExecutionAuthorized || proposal.InstructionAuthorized ||
		proposal.CapabilityGrant || proposal.PermissionMode !=
		domain.RunExecutionPermissionApproval {
		t.Fatalf("proposal unexpectedly carries authority: %+v", proposal)
	}

	review, err := NewHostCommandReview(
		"host-command-review", proposal, HostCommandReviewApprove,
		"cli_operator", "approved after exact command review",
		hostCommandTestDigest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !review.SingleUseExecutionAuthorized || review.CapabilityGrant {
		t.Fatalf("unexpected review authority: %+v", review)
	}
	tampered := review
	tampered.ProposalFingerprint = hostCommandTestDigest
	if err := tampered.Validate(); err == nil {
		t.Fatal("review detached from its proposal unexpectedly validated")
	}

	for _, reviewer := range []string{
		"agent", "model", "repository", "skill", "supervisor", "run_supervisor",
	} {
		if _, err := NewHostCommandReview(
			"review-"+reviewer, proposal, HostCommandReviewApprove,
			reviewer, "must be rejected", hostCommandTestDigest,
			time.Now().UTC()); err == nil {
			t.Fatalf("reserved reviewer %q unexpectedly validated", reviewer)
		}
	}
}

func TestHostCommandProposalRejectsNonApprovalPermission(t *testing.T) {
	now := time.Now().UTC()
	mission := domain.Mission{ID: "mission-host-command", CreatedAt: now}
	run := domain.Run{
		ID: "run-host-command", MissionID: mission.ID,
		Status: domain.RunCreated, CreatedAt: now,
	}
	permission, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"permission-conservative", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NewHostCommandSpec(hostCommandSpecTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	request := hostCommandProposalTestRequest(spec, permission, now)
	if _, err := NewHostCommandProposal(request); err == nil {
		t.Fatal("conservative permission unexpectedly produced a host command proposal")
	}
	full, err := permission.Next(
		"permission-full", domain.RunExecutionPermissionFullAccess, true,
		"test_operator", "test transition", now)
	if err != nil {
		t.Fatal(err)
	}
	request.Permission = full
	if _, err := NewHostCommandProposal(request); err == nil {
		t.Fatal("full-access permission unexpectedly used the approval protocol")
	}
}

func hostCommandProposalFixture(t *testing.T) HostCommandProposal {
	t.Helper()
	now := time.Now().UTC()
	permission := hostCommandApprovalPermission(t)
	spec, err := NewHostCommandSpec(hostCommandSpecTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := NewHostCommandProposal(
		hostCommandProposalTestRequest(spec, permission, now))
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func hostCommandApprovalPermission(
	t *testing.T,
) domain.RunExecutionPermissionSnapshot {
	t.Helper()
	now := time.Now().UTC()
	mission := domain.Mission{ID: "mission-host-proposal", CreatedAt: now}
	run := domain.Run{
		ID: "run-host-proposal", MissionID: mission.ID,
		Status: domain.RunCreated, CreatedAt: now,
	}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"permission-host-conservative", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := initial.Next(
		"permission-host-approval", domain.RunExecutionPermissionApproval, true,
		"test_operator", "approve each exact host command", now)
	if err != nil {
		t.Fatal(err)
	}
	return permission
}

func hostCommandProposalTestRequest(spec HostCommandSpec,
	permission domain.RunExecutionPermissionSnapshot, now time.Time,
) HostCommandProposalRequest {
	return HostCommandProposalRequest{
		ID:    "host-command-proposal",
		RunID: permission.RunID, MissionID: permission.MissionID,
		SessionID: "session-host-command", WorkspaceID: "workspace-host-command",
		RootAgentID:           "agent-root-host-command",
		InteractionSnapshotID: "interaction-host-command",
		InteractionRevision:   1, ExecutionProfileRevision: 1,
		Permission: permission, Spec: spec,
		RequestedBy: "run_supervisor", CreatedAt: now,
	}
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
