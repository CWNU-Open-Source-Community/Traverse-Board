package releasegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectEXESigningHandoffIsFailClosed(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	content, err := os.ReadFile(filepath.Join(root, "scripts", "stage-direct-exe.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{
		"direct_exe_signing_request.v1",
		"direct_exe_signing_handoff.v1",
		"direct_exe_signing.v1",
		"pe_authenticode_normalized.v1",
		"byte_identical.v1",
		"direct-exe-signing-handoff.json",
		"Get-AuthenticodeSignature",
		"Get-AuthenticodeCryptographicProfile",
		"Assert-AuthenticodeContentProfile",
		"Get-RFC3161MessageImprint",
		"2.16.840.1.101.3.4.2.1",
		"1.3.6.1.4.1.311.3.3.1",
		"1.2.840.113549.1.9.6",
		"RFC 3161 timestamp does not bind the verified Authenticode signature",
		"Authenticode file digest algorithm is not SHA-256",
		"signtool verify /pa /all /v",
		"ExpectedSignerSubject",
		"ExpectedSignerThumbprint",
		"Stable verification requires the expected signer Subject and thumbprint",
		"PrepareSigningRequest",
		"signing_request_sha256",
		"signing_handoff_sha256",
		"signature_digest_algorithm",
		"timestamp_digest_algorithm",
		"signed_at_utc",
		"payload_sha256",
		"artifact_sha256",
		"trusted_release = $false",
		"Direct EXE channel differs from the SemVer prerelease component",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("direct EXE signing handoff is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"CertificatePassword",
		"ConvertTo-SecureString",
		"Get-PfxData",
		"signtool sign",
		"ExpectedSignerSubject = [string]$evidence",
		"ExpectedSignerThumbprint = [string]$evidence",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("direct EXE verifier unexpectedly accepts signing authority: %q", forbidden)
		}
	}
}

func TestWindowsCompletionConsumesExternalFactsWithoutRewritingCandidate(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	content, err := os.ReadFile(filepath.Join(root, "scripts", "finalize-windows-release.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{
		"windows_release_completion.v1",
		"windows_store_readback.v1",
		"windows_store_lifecycle.v1",
		"windows_store_lifecycle_row.v1",
		"windows_store_listing_readback.v1",
		"windows_artifact_attestations.v1",
		"lifecycle_validation_status -cne \"not_run\"",
		"stage-direct-exe.ps1",
		"ExpectedDirectSignerSubject",
		"ExpectedDirectSignerThumbprint",
		"direct-exe-signing-handoff.json",
		"run_full_trust_approved",
		"listing_zh_cn_sha256",
		"listing_en_us_sha256",
		"privacy_policy_sha256",
		"github_release_readback_sha256",
		"windows-two-deliverable-contract\\.v1",
		"age_rating",
		"Get-AppxPackage",
		"Get-AppxPackageManifest",
		"SignatureKind",
		"installed_payload_sha256",
		"windows_10|100|zh-CN",
		"windows_11|200|zh-CN",
		"previous_store_package_version",
		"sentinel_before_sha256",
		"reinstall_at_utc",
		"Assert-JSONEquivalent",
		"attestation verify",
		"--signer-workflow",
		"--source-digest",
		"--source-ref",
		"--deny-self-hosted-runners",
		"https://cyclonedx.org/bom",
		"standard-code-product-e2e.json",
		"release view",
		"commits/$([string]$manifest.marketing_version)",
		"Assert-NoRepositoryReparsePoint",
		"[System.IO.FileMode]::CreateNew",
		"[System.IO.File]::Move",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Windows completion finalizer is missing %q", required)
		}
	}
	if strings.Contains(text, "Set-Content -LiteralPath $manifestFile") ||
		strings.Contains(text, "WriteAllText($manifestFile") {
		t.Fatal("Windows completion finalizer rewrites immutable MSIX packaging evidence")
	}
	for _, forbidden := range []string{
		"CertifiedMsixPath",
		"Microsoft-certified MSIX",
		"Subject -match 'Microsoft'",
		"WriteAllText($outputFile",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Windows completion finalizer contains obsolete or overwrite-prone construct %q", forbidden)
		}
	}
}
