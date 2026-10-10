package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"cyberagent-workbench/internal/sbxmcp"
)

func TestSBXReadinessCachesOnlyHashBoundCLIVersion(t *testing.T) {
	b, fake, _ := sbxFixture(t)
	for range 2 {
		proof, err := b.Readiness(t.Context())
		if err != nil || !proof.Ready {
			t.Fatalf("healthy observations refused: %+v %v", proof, err)
		}
	}
	if fake.count("version") != 1 || fake.count("ls") != 2 {
		t.Fatal("mutable inventory was cached or unchanged CLI was restarted")
	}
	fake.ssh = "true"
	if proof, _ := b.Readiness(t.Context()); proof.Ready || proof.ReasonCode != "ssh_forwarding_not_disabled" {
		t.Fatal("cached CLI version masked mutable daemon settings")
	}
	fake.ssh = "false"
	if err := os.WriteFile(b.config.ExecutablePath, []byte("replaced CLI bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	if proof, _ := b.Readiness(t.Context()); proof.Ready || proof.ReasonCode != "cli_unavailable" {
		t.Fatal("changed CLI retained a cached compatibility proof")
	}
}

func TestSBXHostCommandsDisableBackgroundNetworkWithoutTemplateOverrides(t *testing.T) {
	t.Setenv("SBX_NO_TELEMETRY", "0")
	t.Setenv("DOCKER_CI", "false")
	t.Setenv("CI", "caller-controlled")
	t.Setenv("DEFAULT_DOCKER_SANDBOXES_TEMPLATE_IMAGE", "untrusted:latest")
	environment := sbxHostEnvironment()
	if !slices.Contains(environment, "SBX_NO_TELEMETRY=1") || !slices.Contains(environment, "DOCKER_CI=true") ||
		slices.Contains(environment, "SBX_NO_TELEMETRY=0") || slices.Contains(environment, "DOCKER_CI=false") ||
		slices.Contains(environment, "CI=caller-controlled") || slices.Contains(environment, "DEFAULT_DOCKER_SANDBOXES_TEMPLATE_IMAGE=untrusted:latest") {
		t.Fatal("local adapter background-network controls or template boundary changed")
	}
}

func TestSBXMCPPreparationReusesExactRegistrationWithoutOverwrite(t *testing.T) {
	b, fake, _ := sbxFixture(t)
	if err := b.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.calls {
		if call.Executable != b.config.HelperExecutable || !slices.Equal(call.Arguments, []string{sbxmcp.Arg}) {
			t.Fatal("existing exact registration started an SBX CLI")
		}
	}
	if len(fake.calls) != 1 || !b.helperPrepared.Load() {
		t.Fatal("existing registration did not retain its packaged-helper probe")
	}
	proof, err := b.Readiness(t.Context())
	if err != nil || !proof.Ready || !proof.MCPIsolationProven || !proof.CredentialIsolationProven {
		t.Fatalf("bound helper was not admitted: %+v %v", proof, err)
	}
}

type sbxMCPVerifyTestTransport struct {
	SBXDaemonTransport
	verify func(context.Context, string, string, string) error
}

func (transport sbxMCPVerifyTestTransport) VerifyHelper(ctx context.Context, name, executable, arg string) error {
	return transport.verify(ctx, name, executable, arg)
}

func TestSBXMCPPreparationCreatesOnlyAMissingRegistrationAndVerifiesItAgain(t *testing.T) {
	b, fake, _ := sbxFixture(t)
	fake.helperInstalled = false
	verifications := 0
	b.daemon = sbxMCPVerifyTestTransport{SBXDaemonTransport: fake, verify: func(ctx context.Context, name, executable, arg string) error {
		verifications++
		return fake.VerifyHelper(ctx, name, executable, arg)
	}}
	if err := b.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !b.helperPrepared.Load() || !fake.helperInstalled || verifications != 2 || len(fake.calls) != 2 {
		t.Fatal("missing registration was not bound and independently verified")
	}
	if !slices.Equal(fake.calls[0].Arguments, []string{sbxmcp.Arg}) || fake.calls[0].Executable != b.config.HelperExecutable {
		t.Fatal("startup skipped the packaged-helper probe")
	}
	want := []string{"--app-name", SBXAppName, "mcp", "add", b.helperName,
		"--command", b.config.HelperExecutable, "--args=" + sbxmcp.Arg, "--dir", filepath.Dir(b.config.HelperExecutable)}
	if fake.calls[1].Executable != b.config.ExecutablePath || !slices.Equal(fake.calls[1].Arguments, want) {
		t.Fatal("missing registration dispatched more than the fixed add command")
	}
}

func TestSBXMCPPreparationNeverTreatsOtherVerificationFailuresAsMissing(t *testing.T) {
	for _, failure := range []error{ErrSBXBoundary, os.ErrPermission, context.Canceled, ErrSBXUnavailable} {
		t.Run(failure.Error(), func(t *testing.T) {
			b, fake, _ := sbxFixture(t)
			b.daemon = sbxMCPVerifyTestTransport{SBXDaemonTransport: fake, verify: func(context.Context, string, string, string) error {
				return failure
			}}
			if err := b.Prepare(t.Context()); !errors.Is(err, failure) || b.helperPrepared.Load() {
				t.Fatal("verification failure was converted into a prepared registration")
			}
			for _, call := range fake.calls {
				if call.Executable != b.config.HelperExecutable || !slices.Equal(call.Arguments, []string{sbxmcp.Arg}) {
					t.Fatal("unverified registration dispatched a mutation or another SBX CLI")
				}
			}
		})
	}
}

func TestSBXMCPRegistrationMismatchStopsBeforeVMCreation(t *testing.T) {
	for _, mismatch := range []string{"command", "arguments", "resolved", "remote", "oauth", "name", "url"} {
		t.Run(mismatch, func(t *testing.T) {
			b, fake, request := sbxFixture(t)
			metadata := sbxTestMCPMetadata(b.config.HelperExecutable)
			metadata["request"]["Name"], metadata["spec"]["Name"] = b.helperName, b.helperName
			registration := metadata["spec"]
			switch mismatch {
			case "command":
				registration["Command"] = []string{"other.exe", sbxmcp.Arg}
			case "arguments":
				registration["Command"] = []string{b.config.HelperExecutable, sbxmcp.Arg, "--plugin"}
			case "resolved":
				registration["ResolvedCommand"] = "other.exe"
			case "remote":
				registration["Type"] = "remote"
			case "oauth":
				registration["RequiresOAuth"] = true
			case "name":
				registration["Name"] = "other-service"
			case "url":
				registration["URL"] = "https://unexpected.invalid/mcp"
			}
			fake.helperOverride, _ = json.Marshal(metadata)
			unchanged := string(fake.helperOverride)
			if err := b.Prepare(t.Context()); !errors.Is(err, ErrSBXBoundary) {
				t.Fatalf("mismatched registration accepted: %v", err)
			}
			if string(fake.helperOverride) != unchanged || !fake.helperInstalled || b.helperPrepared.Load() {
				t.Fatal("mismatched stored registration was replaced or admitted")
			}
			for _, call := range fake.calls {
				if call.Executable != b.config.HelperExecutable || !slices.Equal(call.Arguments, []string{sbxmcp.Arg}) {
					t.Fatal("mismatched registration started an SBX CLI")
				}
			}
			proof, err := b.Readiness(t.Context())
			if err != nil || proof.Ready || proof.ReasonCode != "mcp_helper_unavailable" {
				t.Fatalf("mismatch remained ready: %+v %v", proof, err)
			}
			_, err = b.Run(t.Context(), request, nil)
			if !errors.Is(err, ErrSBXUnavailable) || fake.count("create") != 0 || fake.count("exec") != 0 {
				t.Fatalf("mismatch dispatched: %v", err)
			}
		})
	}
}

func TestSBXHelperChangeAndGatewayPolicyInvalidateReadiness(t *testing.T) {
	b, fake, request := sbxFixture(t)
	fake.localGateway = "false"
	proof, _ := b.Readiness(t.Context())
	if proof.Ready || proof.ReasonCode != "mcp_local_gateway_required" {
		t.Fatalf("hosted gateway was admitted: %+v", proof)
	}
	fake.localGateway = "true"
	if err := os.WriteFile(b.config.HelperExecutable, []byte("changed executable"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := b.Run(t.Context(), request, nil)
	if !errors.Is(err, ErrSBXUnavailable) || fake.count("create") != 0 {
		t.Fatalf("changed helper dispatched: %v", err)
	}
}

func TestSBXReadinessIsReadOnlyAfterHelperRemoval(t *testing.T) {
	b, fake, _ := sbxFixture(t)
	fake.helperInstalled = false
	for range 2 {
		proof, _ := b.Readiness(t.Context())
		if proof.Ready {
			t.Fatal("removed helper remained ready")
		}
	}
	for _, call := range fake.calls {
		if slices.Contains(call.Arguments, "add") || slices.Contains(call.Arguments, "rm") {
			t.Fatal("readiness mutated MCP registrations")
		}
	}
	if err := b.Prepare(context.Background()); err != nil || !fake.helperInstalled {
		t.Fatalf("explicit startup did not restore helper: %v", err)
	}
}
