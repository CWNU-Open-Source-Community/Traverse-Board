package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func attachmentTestSpec(t *testing.T) CommandRuntimeResolvedSpec {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NormalizeCommandRuntimeSpec(CommandRuntimeSpec{
		Version: CommandRuntimeProtocolVersion, Profile: CommandRuntimeProcess,
		Executable: executable, Arguments: []string{}, WorkingDirectory: ".", Environment: []CommandRuntimeEnvironment{},
		StdinPolicy: CommandRuntimeStdinClosed, CloseInitialStdin: true, TimeoutMilliseconds: 1000,
		Output:  CommandRuntimeOutputPolicy{InlineBytes: MinCommandRuntimeInlineBytes, ArtifactBytes: MinCommandRuntimeInlineBytes},
		Network: CommandRuntimeNetworkDisabled, Credentials: CommandRuntimeCredentialsNone, Purpose: "read sent attachment",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestCommandRuntimeAttachmentBindingKeepsPrivatePathAndPinsManifest(t *testing.T) {
	base := attachmentTestSpec(t)
	before := CommandRuntimeSpecFingerprint(base)
	input := CommandRuntimeAttachmentInput{Root: t.TempDir(), ManifestSHA256: strings.Repeat("a", 64)}
	bound, err := BindCommandRuntimeAttachmentInput(base, input)
	if err != nil {
		t.Fatal(err)
	}
	if CommandRuntimeSpecFingerprint(base) != before || base.AttachmentInput != nil || bound.EnvironmentSHA256 == base.EnvironmentSHA256 || CommandRuntimeSpecFingerprint(bound) == before {
		t.Fatal("attachment binding mutated the original request or omitted its identity")
	}
	if err := validateCommandRuntimeLaunchDirectory(bound); err != nil {
		t.Fatal(err)
	}
	var intent map[string]any
	if err := json.Unmarshal([]byte(commandRuntimeIntentJSON(bound)), &intent); err != nil {
		t.Fatal(err)
	}
	if intent["attachment_manifest_sha256"] != input.ManifestSHA256 || strings.Contains(commandRuntimeIntentJSON(bound), input.Root) {
		t.Fatal("durable command intent lacks manifest identity or leaks native attachment path")
	}
	if strings.Contains(commandRuntimeIntentJSON(base), "attachment_manifest_sha256") {
		t.Fatal("old no-attachment intent shape changed")
	}
	other, err := BindCommandRuntimeAttachmentInput(base, CommandRuntimeAttachmentInput{Root: input.Root, ManifestSHA256: strings.Repeat("b", 64)})
	if err != nil || CommandRuntimeSpecFingerprint(other) == CommandRuntimeSpecFingerprint(bound) {
		t.Fatal("changed manifest reused the command identity")
	}
	if err := os.Remove(input.Root); err != nil {
		t.Fatal(err)
	}
	if validateCommandRuntimeLaunchDirectory(bound) == nil {
		t.Fatal("removed input root passed launch revalidation")
	}
}

func TestCommandRuntimeAttachmentBindingRejectsUserEnvironmentAndWorkspaceOverlap(t *testing.T) {
	base := attachmentTestSpec(t)
	for _, name := range []string{CommandRuntimeAttachmentEnvironment, strings.ToLower(CommandRuntimeAttachmentEnvironment)} {
		intent := base.Spec
		intent.Environment = []CommandRuntimeEnvironment{{Name: name, Value: "C:/unrelated"}}
		if _, err := NormalizeCommandRuntimeIntent(intent); err == nil {
			t.Fatal("model can override fixed attachment environment")
		}
	}
	for _, root := range []string{base.WorkspaceRoot, filepath.Join(base.WorkspaceRoot, "inputs"), "relative"} {
		if _, err := BindCommandRuntimeAttachmentInput(base, CommandRuntimeAttachmentInput{Root: root, ManifestSHA256: strings.Repeat("a", 64)}); err == nil {
			t.Fatalf("unsafe attachment root accepted: %q", root)
		}
	}
	bound, err := BindCommandRuntimeAttachmentInput(base, CommandRuntimeAttachmentInput{Root: t.TempDir(), ManifestSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	bound.Environment = replaceCommandRuntimeEnvironment(bound.Environment, CommandRuntimeAttachmentEnvironment, "C:/different")
	if validateCommandRuntimeAttachmentInput(bound) == nil {
		t.Fatal("changed fixed directory passed launch validation")
	}
}
