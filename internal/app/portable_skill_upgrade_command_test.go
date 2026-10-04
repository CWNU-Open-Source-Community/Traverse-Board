package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cyberagent-workbench/internal/plugins"
)

func TestPortableSkillCLIUpgradeDisableThenEnableAndReplay(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	directory := filepath.Join(t.TempDir(), "cli-upgrade")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	oldBody := []byte("---\nname: cli-upgrade\ndescription: Exercise explicit CLI revision switching.\n---\nRevision one.\n")
	newBody := []byte("---\nname: cli-upgrade\ndescription: Exercise explicit CLI revision switching.\n---\nRevision two.\n")
	writeBody := func(raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	importRevision := func(key string) (string, string, int) {
		t.Helper()
		return executeTestCommand(t, "skill", "import-dir", directory, "--surface", "code", "--operation-key", key, "--confirm-untrusted-skill")
	}
	list := func() []plugins.Installation {
		t.Helper()
		output, stderr, code := executeTestCommand(t, "plugin", "list")
		var values []plugins.Installation
		if err := json.Unmarshal([]byte(output), &values); code != 0 || stderr != "" || err != nil {
			t.Fatalf("plugin list code=%d stderr=%q output=%q error=%v", code, stderr, output, err)
		}
		return values
	}
	show := func(want plugins.Installation) {
		t.Helper()
		output, stderr, code := executeTestCommand(t, "plugin", "show", want.ID)
		var got plugins.Installation
		if err := json.Unmarshal([]byte(output), &got); code != 0 || stderr != "" || err != nil ||
			got.State != want.State || got.Generation != want.Generation || got.Revision() != want.Revision() ||
			!slices.Equal(got.EnabledCapabilities, want.EnabledCapabilities) {
			t.Fatalf("persisted CLI state changed: code=%d stderr=%q got=%+v want=%+v error=%v", code, stderr, got, want, err)
		}
	}
	review := func(value plugins.Installation, action plugins.ReviewAction) plugins.Installation {
		t.Helper()
		args := []string{"plugin", "review", value.ID, "--action", string(action), "--fingerprint", value.PackageFingerprint,
			"--generation", fmt.Sprint(value.Generation), "--by", "operator"}
		if action == plugins.ReviewEnable {
			args = append(args, "--capabilities", "skills", "--confirm-untrusted")
		}
		output, stderr, code := executeTestCommand(t, args...)
		var result plugins.Installation
		if err := json.Unmarshal([]byte(output), &result); code != 0 || stderr != "" || err != nil {
			t.Fatalf("plugin review %s code=%d stderr=%q output=%q error=%v", action, code, stderr, output, err)
		}
		if result.ID != value.ID || result.Generation != value.Generation+1 {
			t.Fatalf("review %s did not update exactly one generation: %+v", action, result)
		}
		t.Logf("plugin review --action %s --generation %d: state=%s generation=%d", action, value.Generation, result.State, result.Generation)
		return result
	}
	writeBody(oldBody)
	if output, stderr, code := importRevision("cli-upgrade-v1"); code != 0 || stderr != "" || !strings.Contains(output, "state: enabled") {
		t.Fatalf("initial import code=%d stderr=%q output=%q", code, stderr, output)
	}
	values := list()
	if len(values) != 1 || values[0].State != plugins.StateEnabled {
		t.Fatalf("initial installation: %+v", values)
	}
	old := values[0]
	writeBody(newBody)
	output, stderr, code := importRevision("cli-upgrade-v2")
	t.Logf("same-source revision import: exit=%d stderr=%q output=%q", code, stderr, output)
	if code == 0 || !strings.Contains(stderr, "another plugin version is enabled") {
		t.Fatalf("CLI did not expose the enabled predecessor conflict: exit=%d stderr=%q", code, stderr)
	}
	values = list()
	if len(values) != 2 {
		t.Fatalf("new approved revision is not discoverable through plugin list: %+v", values)
	}
	var next plugins.Installation
	for _, value := range values {
		if value.ID != old.ID {
			next = value
		}
	}
	if next.State != plugins.StateApproved || next.Generation != 2 || len(next.EnabledCapabilities) != 0 ||
		next.PackageID() != old.PackageID() || next.Revision() == old.Revision() || next.Source.URI != old.Source.URI {
		t.Fatalf("upgrade conflict state: %+v", next)
	}
	show(old)
	t.Logf("after import conflict: old=%s generation=%d new=%s generation=%d", old.State, old.Generation, next.State, next.Generation)
	old = review(old, plugins.ReviewDisable)
	if old.State != plugins.StateDisabled {
		t.Fatalf("disable left predecessor active: %+v", old)
	}
	show(old)
	show(next)
	next = review(next, plugins.ReviewEnable)
	if next.State != plugins.StateEnabled || len(next.EnabledCapabilities) != 1 || next.EnabledCapabilities[0] != plugins.CapabilitySkills {
		t.Fatalf("enable did not activate new Skill revision: %+v", next)
	}
	show(old)
	show(next)
	assertReplay := func(key string, want plugins.Installation) {
		t.Helper()
		output, stderr, code := importRevision(key)
		if code != 0 || stderr != "" || !strings.Contains(output, "state: "+string(want.State)) {
			t.Fatalf("import retry code=%d stderr=%q output=%q", code, stderr, output)
		}
		show(want)
		t.Logf("skill import-dir operation %s retry: state=%s generation=%d unchanged", key, want.State, want.Generation)
	}
	assertReplay("cli-upgrade-v2", next)
	if _, stderr, code := importRevision("cli-upgrade-v1"); code == 0 || !strings.Contains(stderr, "operation key is already bound to different input") {
		t.Fatalf("old operation accepted new source bytes: exit=%d stderr=%q", code, stderr)
	}
	writeBody(oldBody)
	assertReplay("cli-upgrade-v1", old)
	show(next)
	writeBody(newBody)
	next = review(next, plugins.ReviewDisable)
	assertReplay("cli-upgrade-v2", next)
	show(old)
	if len(list()) != 2 {
		t.Fatal("retries created extra installation records")
	}
}
