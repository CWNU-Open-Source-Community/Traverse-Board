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
		"Standard Code Beta releases must use a prerelease version",
		`"--draft=false"`,
		`"--prerelease"`,
	}
	for _, value := range required {
		if !strings.Contains(text, value) {
			t.Fatalf("release workflow is missing %q", value)
		}
	}
	for _, forbidden := range []string{"continue-on-error: true", "|| true", "waiver: true", `"--latest"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("release workflow contains fail-open construct %q", forbidden)
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
