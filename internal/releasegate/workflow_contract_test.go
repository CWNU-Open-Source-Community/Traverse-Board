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
		"scripts/finalize-windows-release.ps1",
		"packaging/windows/RELEASE-NOTES.md",
		"Validate Draft Release evidence intake",
		"Require successful central CI for the exact release revision",
		"The exact release revision has no successful central CI push run",
		"standard-code-product-e2e.json",
		"standard-code-security-evidence.json",
		"standard-code-release-gate.json",
		"-RequireReleaseGate",
		"--verify-report build/desktop/standard-code-release-gate.json",
		"The candidate evidence Draft Release is no longer available",
		"The candidate evidence Draft Release differs from the exact channel-bound intake allowlist",
		"Published release asset inventory differs from the exact public allowlist",
		"A stable release requires the exact Partner Center identity and an explicit Store package version",
		"A stable release requires the exact direct EXE signer Subject and thumbprint",
		"Direct EXE signer identity configuration is partial",
		"Prepare protected direct-EXE signing request",
		"Upload isolated direct-EXE signing request",
		"default: finalize",
		`$phase = "finalize"`,
		`$phase = "validate"`,
		`$phase -ceq "prepare" -and $isPrerelease`,
		"The prepare phase is only valid for a stable signing request",
		"-PrepareSigningRequest",
		"signing_prepare",
		"name: ${{ steps.release.outputs.artifact_name }}-direct-exe-signing-request",
		"direct-exe-signing-request.json",
		"-SigningRequestPath build/signing/direct-exe-signing-request.json",
		"TraverseBoard-signed.exe",
		"direct-exe-signing-handoff.json",
		"Stage prerelease direct executable",
		"Stage trusted stable direct executable",
		"Reverify staged direct executable",
		"TraverseBoard.direct.exe",
		"direct-exe-signing.json",
		"VerifyOnly = $true",
		"Package Partner Center-bound Store upload",
		"Exercise synthetic Partner Center contract on pull requests",
		`(?<prerelease>-`,
		"IS_PRERELEASE",
		"-notin @(\"true\", \"false\")",
		"Upload isolated development MSIX validation artifact",
		"Upload isolated Partner Center submission artifact",
		"attest-windows-artifacts:",
		"Attest Windows release artifacts",
		"actions: read",
		"artifact-metadata: write",
		"attestations: write",
		"contents: read",
		"id-token: write",
		"windows_artifact_attestations.v1",
		"direct_exe_provenance",
		"direct_exe_sbom",
		"store_msixupload_provenance",
		"predicate_type",
		"https://slsa.dev/provenance/v1",
		"https://cyclonedx.org/bom",
		"artifact-attestations.json",
		"gh attestation verify",
		"--signer-workflow",
		"--source-digest",
		"--predicate-type",
		"needs.portable-zip.outputs.signing_prepare != 'true'",
		"needs.portable-zip.outputs.phase != 'prepare'",
		"needs.portable-zip.outputs.phase == 'finalize'",
		"windows-two-deliverable-contract.v1",
		"Windows 下载 / Windows downloads",
		"本版本面向 Windows 用户只定义两个成品：",
		"This version defines exactly two Windows user products:",
		"直接 EXE 信任状态 / Direct-EXE trust state:",
		"{{VERSION}}",
		"{{TRUST_STATE}}",
		"--notes-file",
		"Published release metadata or canonical bilingual body differs after readback",
		`"--draft=false"`,
		`"--prerelease=${{`,
	}
	for _, value := range required {
		if !strings.Contains(text, value) {
			t.Fatalf("release workflow is missing %q", value)
		}
	}
	for _, forbidden := range []string{"continue-on-error: true", "|| true", "waiver: true",
		`"--latest"`, "MSIX_CERTIFICATE_BASE64", "MSIX_CERTIFICATE_PASSWORD",
		"A stable direct EXE release requires a valid Authenticode signature",
		"Get-AuthenticodeSignature -LiteralPath build/desktop/TraverseBoard.exe"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("release workflow contains fail-open construct %q", forbidden)
		}
	}
	const attestAction = "uses: actions/attest@1e69f48acb82d1966a394da916b4c1698aa569d6 # v4"
	if count := strings.Count(text, attestAction); count != 3 {
		t.Fatalf("release workflow must use exactly three pinned attestation steps; got %d", count)
	}
	portableStart := strings.Index(text, "  portable-zip:")
	attestStart := strings.Index(text, "  attest-windows-artifacts:")
	publishStart := strings.Index(text, "  publish:")
	if portableStart < 0 || attestStart <= portableStart || publishStart <= attestStart {
		t.Fatal("release workflow job boundaries are invalid")
	}
	portableJob := text[portableStart:attestStart]
	for _, forbiddenPermission := range []string{
		"id-token: write", "attestations: write", "artifact-metadata: write",
	} {
		if strings.Contains(portableJob, forbiddenPermission) {
			t.Fatalf("pull-request-capable build job received attestation authority %q", forbiddenPermission)
		}
	}
	attestJob := text[attestStart:publishStart]
	for _, value := range []string{
		"github.event_name != 'pull_request'",
		"needs.portable-zip.outputs.phase != 'prepare'",
		"needs.portable-zip.outputs.signing_prepare != 'true'",
		"actions: read", "artifact-metadata: write", "attestations: write",
		"contents: read", "id-token: write",
	} {
		if !strings.Contains(attestJob, value) {
			t.Fatalf("attestation job is missing scoped contract %q", value)
		}
	}
	if strings.Contains(attestJob, "contents: write") {
		t.Fatal("attestation job unexpectedly has repository write authority")
	}
	publishJob := text[publishStart:]
	for name, job := range map[string]string{
		"candidate build": portableJob,
		"attestation":     attestJob,
		"publication":     publishJob,
	} {
		for _, signerParameter := range []string{
			"ExpectedDirectSignerSubject",
			"ExpectedDirectSignerThumbprint",
		} {
			if count := strings.Count(job, signerParameter); count < 2 {
				t.Fatalf("%s job does not pass the protected signer through every stable verifier via %q; got %d uses", name, signerParameter, count)
			}
		}
	}
	prepareStart := strings.Index(portableJob, "Prepare protected direct-EXE signing request")
	prepareEnd := strings.Index(portableJob, "Package internal verification ZIP with evidence")
	if prepareStart < 0 || prepareEnd <= prepareStart {
		t.Fatal("stable signing-request phase is not isolated before downstream packaging")
	}
	prepareBlock := portableJob[prepareStart:prepareEnd]
	for _, value := range []string{
		"steps.release.outputs.signing_prepare == 'true'",
		"-PrepareSigningRequest",
		"build/desktop/TraverseBoard.exe",
		"build/desktop/release-metadata.json",
		"build/desktop/direct-exe-signing-request.json",
	} {
		if !strings.Contains(prepareBlock, value) {
			t.Fatalf("stable signing-request phase is missing %q", value)
		}
	}
	for _, forbiddenSigningResult := range []string{
		"TraverseBoard-signed.exe", "direct-exe-signing-handoff.json",
		"standard-code-product-e2e.json", "standard-code-security-evidence.json",
		"sbom.json", "NOTICE",
	} {
		if strings.Contains(prepareBlock, forbiddenSigningResult) {
			t.Fatalf("stable signing-request artifact leaks finalize-only input %q", forbiddenSigningResult)
		}
	}
	if count := strings.Count(portableJob, "steps.release.outputs.signing_prepare != 'true'"); count < 12 {
		t.Fatalf("stable signing prepare does not cleanly skip downstream work; only %d guards", count)
	}
	for _, artifactContract := range []struct {
		name  string
		start string
		end   string
	}{
		{
			name:  "exact Windows candidate",
			start: "Upload exact Desktop verification and direct-EXE candidate",
			end:   "Upload isolated development MSIX validation artifact",
		},
		{
			name:  "Partner Center submission",
			start: "Upload isolated Partner Center submission artifact",
			end:   "  attest-windows-artifacts:",
		},
	} {
		start := strings.Index(text, artifactContract.start)
		end := strings.Index(text, artifactContract.end)
		if start < 0 || end <= start {
			t.Fatalf("%s artifact block is missing or out of order", artifactContract.name)
		}
		block := text[start:end]
		for _, retained := range []string{
			"build/desktop/direct-exe-signing-request.json",
			"build/desktop/direct-exe-signing-handoff.json",
			"build/desktop/direct-exe-signing.json",
			"build/desktop/TraverseBoard.direct.exe",
		} {
			if !strings.Contains(block, retained) {
				t.Fatalf("%s artifact does not retain %q", artifactContract.name, retained)
			}
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
	for _, publicAsset := range []string{
		`"build/public/TraverseBoard.exe"`,
		`"build/desktop/direct-exe-signing.json"`,
		`"build/desktop/direct-exe-signing-request.json"`,
		`"build/desktop/direct-exe-signing-handoff.json"`,
		`"build/attestations/artifact-attestations.json"`,
	} {
		if !strings.Contains(publicAssets, publicAsset) {
			t.Fatalf("public release asset construction is missing %q", publicAsset)
		}
	}
	for _, internalOnly := range []string{"Prayu-portable-", "portable-zip-manifest.json",
		"msix-manifest.json", ".msixupload", ".msix\"", "build/desktop/TraverseBoard.exe",
		"TraverseBoard.direct.exe", "TraverseBoard-signed.exe"} {
		if strings.Contains(publicAssets, internalOnly) {
			t.Fatalf("internal Windows candidate leaked into public release assets: %q", internalOnly)
		}
	}
	stableAssetsStart := strings.Index(publicAssets, "if ($stable) {")
	attestationIndexStart := strings.Index(publicAssets, "$attestationIndex =")
	if stableAssetsStart < 0 || attestationIndexStart <= stableAssetsStart {
		t.Fatal("public release asset construction has no channel-bound stable evidence block")
	}
	baseAssets := publicAssets[:stableAssetsStart]
	stableAssets := publicAssets[stableAssetsStart:attestationIndexStart]
	if strings.Contains(baseAssets, "direct-exe-signing-handoff.json") {
		t.Fatal("prerelease public asset base unexpectedly requires a stable signing handoff")
	}
	for _, stableOnly := range []string{
		"direct-exe-signing-request.json",
		"direct-exe-signing-handoff.json",
	} {
		if !strings.Contains(stableAssets, stableOnly) {
			t.Fatalf("stable public asset block is missing %q", stableOnly)
		}
	}
	if !strings.Contains(publishJob,
		"foreach ($intakeOnly in @('TraverseBoard-signed.exe'))") {
		t.Fatal("stable publication does not clean up exactly the renamed signed-EXE intake asset")
	}
	cleanupStart := strings.Index(publishJob, "$release =")
	cleanupEnd := strings.Index(publishJob, "$upload =")
	if cleanupStart < 0 || cleanupEnd <= cleanupStart {
		t.Fatal("stable publication cleanup block is missing or out of order")
	}
	if strings.Contains(publishJob[cleanupStart:cleanupEnd],
		"direct-exe-signing-handoff.json") {
		t.Fatal("stable publication deletes the handoff that its public checksums retain")
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
