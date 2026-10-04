package toolcontract

import (
	"bytes"
	"testing"
)

func TestHTTPConfiguredHeadersCoexistWithHostCredentialBinding(t *testing.T) {
	launch := httpLaunch()
	launch.HTTP.Headers["authorization"] = "Bearer configured-fixture"
	launch.HTTP.Headers["mcp-session-id"] = "configured-session-fixture"
	launch.HTTP.Headers["mcp-protocol-version"] = "configured-version-fixture"
	if err := launch.Validate(); err != nil {
		t.Fatal(err)
	}
	declaration := LaunchDeclaration{Component: launch.Component, Format: launch.Format,
		FormatVersion: launch.FormatVersion, Transport: launch.Transport, HTTP: launch.HTTP}
	if err := declaration.Validate(); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("k"), 32)
	before, err := FingerprintLaunch(key, launch)
	if err != nil {
		t.Fatal(err)
	}
	// Even a shadowed configured value remains bound to the acquired launch.
	// This contract does not send or choose header values: the transport must
	// prove generated-header precedence on the actual HTTP request separately.
	launch.HTTP.Headers["authorization"] = "Bearer changed-fixture"
	after, err := FingerprintLaunch(key, launch)
	if err != nil || before == after {
		t.Fatal("configured header drift was not fingerprinted")
	}
	launch.HTTP.Credential.Revision = "revision-2"
	current, err := FingerprintLaunch(key, launch)
	if err != nil || current == after {
		t.Fatal("host credential revision drift was not fingerprinted")
	}
	if launch.HTTP.Headers["authorization"] != "Bearer changed-fixture" {
		t.Fatal("contract mutated configured headers")
	}
	launch.HTTP.Headers["Authorization"] = "duplicate-fixture"
	if err := launch.Validate(); err == nil {
		t.Fatal("case-equivalent configured headers must still be rejected")
	}
}
