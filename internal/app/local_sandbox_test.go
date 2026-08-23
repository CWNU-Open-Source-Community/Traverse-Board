package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/sandbox"
)

func TestLocalSandboxReadinessCLIIsStrictAndNonAuthorizing(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	code := Execute([]string{"sandbox", "local-readiness", "--json"}, &out, &errOut)
	if code != 0 || errOut.Len() != 0 {
		t.Fatalf("local readiness code=%d stdout=%s stderr=%s",
			code, out.String(), errOut.String())
	}
	var readiness sandbox.LocalReadiness
	if err := json.Unmarshal(out.Bytes(), &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.Validate() != nil || readiness.Status != sandbox.LocalReadinessDisabled ||
		readiness.Ready || readiness.CapabilityGrant {
		t.Fatalf("disabled readiness widened authority: %#v", readiness)
	}
	lower := strings.ToLower(out.String())
	for _, forbidden := range []string{"owner_root", "drydock_root", "profile_name",
		"process_id", "credential" + "_value"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("readiness exposed private field %q: %s", forbidden, out.String())
		}
	}
}
