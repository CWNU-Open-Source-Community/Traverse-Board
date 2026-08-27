package releasegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDesktopReleaseWorkflowWiresEvidenceAndReverifiesBeforePublish(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, ".github", "workflows", "release-desktop.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("release workflow YAML is invalid: %v", err)
	}
	text := string(content)
	required := []string{
		"cmd/releasegate/**",
		"internal/releasegate/**",
		"Validate Draft Release evidence intake",
		"Require successful central CI for the exact release revision",
		"The exact release revision has no successful central CI push run",
		"standard-code-product-e2e.json",
		"standard-code-security-evidence.json",
		"standard-code-release-gate.json",
		"-RequireReleaseGate",
		"--verify-report build/desktop/standard-code-release-gate.json",
		"The candidate evidence Draft Release is no longer available",
		"The candidate evidence Draft Release must contain exactly the two required evidence assets",
		"Published release asset inventory differs from the exact public allowlist",
		"A stable release requires the exact Partner Center identity and an explicit Store package version",
		"A stable direct EXE release requires a valid Authenticode signature",
		"Package Partner Center-bound Store upload",
		"Exercise synthetic Partner Center contract on pull requests",
		`(?<prerelease>-`,
		"IS_PRERELEASE",
		"-notin @(\"true\", \"false\")",
		"Upload isolated development MSIX validation artifact",
		"Upload isolated Partner Center submission artifact",
		`"--draft=false"`,
		`"--prerelease=${{`,
	}
	for _, value := range required {
		if !strings.Contains(text, value) {
			t.Fatalf("release workflow is missing %q", value)
		}
	}
	for _, forbidden := range []string{"continue-on-error: true", "|| true", "waiver: true",
		`"--latest"`, "MSIX_CERTIFICATE_BASE64", "MSIX_CERTIFICATE_PASSWORD"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("release workflow contains fail-open construct %q", forbidden)
		}
	}
	assetStart := strings.Index(text, "Create verified release asset set")
	if assetStart < 0 {
		t.Fatal("release workflow has no public asset construction step")
	}
	assetEndOffset := strings.Index(text[assetStart:], "$release =")
	if assetEndOffset < 0 {
		t.Fatal("release workflow public asset step has no draft-release check")
	}
	publicAssets := text[assetStart : assetStart+assetEndOffset]
	if !strings.Contains(publicAssets, `"build/desktop/TraverseBoard.exe"`) {
		t.Fatal("direct TraverseBoard.exe is missing from public release assets")
	}
	for _, internalOnly := range []string{"Prayu-portable-", "portable-zip-manifest.json",
		"msix-manifest.json", ".msixupload", ".msix\""} {
		if strings.Contains(publicAssets, internalOnly) {
			t.Fatalf("internal Windows candidate leaked into public release assets: %q", internalOnly)
		}
	}
}

func TestPackagedHarnessRequiresBothReportsForAggregate(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "scripts", "standard-code-packaged-e2e.ps1"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, value := range []string{"$productReportProvided -ne $securityReportProvided",
		"$RequireReleaseGate -and -not $productReportProvided",
		"go run ./cmd/releasegate", "standard_code_release_gate: passed"} {
		if !strings.Contains(text, value) {
			t.Fatalf("packaged harness is missing %q", value)
		}
	}
}
