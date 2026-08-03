package analyzer

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestAnalyzerReleaseCandidatePinsManifestAndOperatorAllowlistWithoutAuthority(t *testing.T) {
	chain := mustAnalyzerExecutableEvidenceChain(t)
	manifest := mustAnalyzerReleaseManifest(t, chain.evidence)
	entry, code := AnalyzerReleaseAllowlistEntryForManifest(manifest, chain.evidence)
	if code != "" {
		t.Fatal(code)
	}
	allowlist, code := BuildAnalyzerReleaseAllowlist([]AnalyzerReleaseAllowlistEntry{entry})
	if code != "" {
		t.Fatal(code)
	}
	release, code := BuildAnalyzerReleaseCandidate(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, manifest, allowlist)
	if code != "" {
		t.Fatal(code)
	}
	if release.ProtocolVersion != AnalyzerReleaseCandidateProtocolVersion ||
		release.AllowlistEntryCount != 1 || !release.ManifestDigestPinned ||
		!release.ExecutableDigestPinned || !release.FormatEvidenceDigestPinned ||
		!release.ProvenanceDigestPinned || !release.SignatureEnvelopeDigestPinned ||
		!release.OperatorAllowlistMatched || release.ProvenanceStatementVerified ||
		release.CryptographicSignatureVerified || release.PlatformSignatureVerified ||
		release.ImmutableHandleVerified || !release.OperatorReviewRequired ||
		release.ReleaseApproved || release.ProcessStartEnabled ||
		release.ProductInvocationEnabled || release.NetworkAuthorized ||
		release.HostFilesystemAuthorized {
		t.Fatalf("unsafe or incomplete release candidate: %#v", release)
	}

	encodedManifest, code := EncodeAnalyzerReleaseManifest(manifest, chain.evidence)
	if code != "" {
		t.Fatal(code)
	}
	decodedManifest, code := DecodeAnalyzerReleaseManifest(encodedManifest, chain.evidence)
	if code != "" || !reflect.DeepEqual(decodedManifest, manifest) {
		t.Fatalf("manifest round trip failed: code=%s value=%#v", code, decodedManifest)
	}
	encodedAllowlist, code := EncodeAnalyzerReleaseAllowlist(allowlist)
	if code != "" {
		t.Fatal(code)
	}
	decodedAllowlist, code := DecodeAnalyzerReleaseAllowlist(encodedAllowlist)
	if code != "" || !reflect.DeepEqual(decodedAllowlist, allowlist) {
		t.Fatalf("allowlist round trip failed: code=%s value=%#v", code, decodedAllowlist)
	}
	encodedRelease, code := EncodeAnalyzerReleaseCandidate(release, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, manifest, allowlist)
	if code != "" {
		t.Fatal(code)
	}
	decodedRelease, code := DecodeAnalyzerReleaseCandidate(encodedRelease, chain.candidate,
		chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence, manifest,
		allowlist)
	if code != "" || !reflect.DeepEqual(decodedRelease, release) {
		t.Fatalf("release round trip failed: code=%s value=%#v", code, decodedRelease)
	}
	assertExactObjectKeys(t, encodedRelease, []string{"allowlist_entry_count",
		"allowlist_sha256", "analyzer", "cryptographic_signature_verified",
		"executable_digest_pinned", "executable_format_evidence_sha256",
		"executable_sha256", "format_evidence_digest_pinned", "host_filesystem_authorized",
		"immutable_handle_verified", "manifest_digest_pinned", "manifest_sha256",
		"network_authorized", "operator_allowlist_matched", "operator_review_required",
		"platform_signature_verified", "process_start_enabled", "product_invocation_enabled",
		"protocol_version", "provenance_digest_pinned", "provenance_statement_verified",
		"release_approved", "release_version", "signature_envelope_digest_pinned",
		"target_goarch", "target_goos"})
}

func TestAnalyzerReleaseAllowlistIsSortedUniqueAndClosedToAmbientSources(t *testing.T) {
	chain := mustAnalyzerExecutableEvidenceChain(t)
	firstManifest := mustAnalyzerReleaseManifest(t, chain.evidence)
	first, code := AnalyzerReleaseAllowlistEntryForManifest(firstManifest, chain.evidence)
	if code != "" {
		t.Fatal(code)
	}
	second := first
	second.ReleaseVersion = "v9.9.9"
	second.ManifestSHA256 = strings.Repeat("d", 64)
	allowlist, code := BuildAnalyzerReleaseAllowlist([]AnalyzerReleaseAllowlistEntry{second, first})
	if code != "" {
		t.Fatal(code)
	}
	if len(allowlist.Entries) != 2 || allowlist.Entries[0].ReleaseVersion != "v0.1.0" ||
		!allowlist.OperatorManaged || allowlist.NetworkLoaded || allowlist.EnvironmentLoaded {
		t.Fatalf("unexpected allowlist: %#v", allowlist)
	}
	if _, code := BuildAnalyzerReleaseAllowlist([]AnalyzerReleaseAllowlistEntry{first, first}); code != CodeInvalidContent {
		t.Fatalf("duplicate allowlist code = %s", code)
	}
	mutated := allowlist
	mutated.NetworkLoaded = true
	if code := ValidateAnalyzerReleaseAllowlist(mutated); code != CodeInvalidResult {
		t.Fatalf("ambient allowlist source code = %s", code)
	}
}

func TestAnalyzerReleaseCandidateRejectsPolicyDriftAndSchemaWidening(t *testing.T) {
	chain := mustAnalyzerExecutableEvidenceChain(t)
	manifest := mustAnalyzerReleaseManifest(t, chain.evidence)
	entry, _ := AnalyzerReleaseAllowlistEntryForManifest(manifest, chain.evidence)
	allowlist, _ := BuildAnalyzerReleaseAllowlist([]AnalyzerReleaseAllowlistEntry{entry})
	release, code := BuildAnalyzerReleaseCandidate(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, manifest, allowlist)
	if code != "" {
		t.Fatal(code)
	}

	badManifest := manifest
	badManifest.SignatureBytesIncluded = true
	if code := ValidateAnalyzerReleaseManifest(badManifest, chain.evidence); code != CodeInvalidResult {
		t.Fatalf("signature bytes code = %s", code)
	}
	missing := entry
	missing.ReleaseVersion = "v0.1.1"
	missing.ManifestSHA256 = strings.Repeat("e", 64)
	missingAllowlist, _ := BuildAnalyzerReleaseAllowlist([]AnalyzerReleaseAllowlistEntry{missing})
	if _, code := BuildAnalyzerReleaseCandidate(chain.candidate, chain.raw, chain.executable,
		chain.identity, chain.preflight, chain.evidence, manifest, missingAllowlist); code != CodeInvalidContent {
		t.Fatalf("missing manifest allowlist code = %s", code)
	}
	mutated := release
	mutated.CryptographicSignatureVerified = true
	if code := ValidateAnalyzerReleaseCandidate(mutated, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, manifest,
		allowlist); code != CodeInvalidResult {
		t.Fatalf("signature authority drift code = %s", code)
	}

	encoded, code := EncodeAnalyzerReleaseCandidate(release, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, manifest, allowlist)
	if code != "" {
		t.Fatal(code)
	}
	text := string(encoded)
	for name, malformed := range map[string]string{
		"future": strings.Replace(text, AnalyzerReleaseCandidateProtocolVersion,
			"analyzer_release_candidate.v2", 1),
		"unknown": strings.Replace(text, `"release_approved":false`,
			`"release_approved":false,"executable_path":"tool"`, 1),
		"duplicate": strings.Replace(text, `"process_start_enabled":false`,
			`"process_start_enabled":false,"process_start_enabled":false`, 1),
		"missing false": strings.Replace(text, `,"network_authorized":false`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeAnalyzerReleaseCandidate([]byte(malformed), chain.candidate,
				chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
				manifest, allowlist); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}
}

type analyzerExecutableEvidenceChain struct {
	raw        []byte
	candidate  InvocationCandidate
	executable []byte
	identity   ExecutableIdentity
	preflight  InvocationPreflight
	evidence   ExecutableFormatEvidence
}

func mustAnalyzerExecutableEvidenceChain(t *testing.T) analyzerExecutableEvidenceChain {
	t.Helper()
	if executableFormatForGOOS(runtime.GOOS) == "" {
		t.Skipf("runtime GOOS %q is not a PE/ELF target", runtime.GOOS)
	}
	raw := testRequestJSON(t)
	candidate := mustInvocationCandidate(t, raw)
	executable := testExecutableForTarget(t, runtime.GOOS, runtime.GOARCH)
	identity, code := BuildExecutableIdentity(candidate, raw, executable)
	if code != "" {
		t.Fatal(code)
	}
	preflight, code := BuildInvocationPreflight(candidate, raw, executable, identity)
	if code != "" {
		t.Fatal(code)
	}
	evidence, code := BuildExecutableFormatEvidence(candidate, raw, executable, identity,
		preflight)
	if code != "" {
		t.Fatal(code)
	}
	return analyzerExecutableEvidenceChain{raw: raw, candidate: candidate,
		executable: executable, identity: identity, preflight: preflight, evidence: evidence}
}

func mustAnalyzerReleaseManifest(t *testing.T,
	evidence ExecutableFormatEvidence,
) AnalyzerReleaseManifest {
	t.Helper()
	manifest, code := BuildAnalyzerReleaseManifest(evidence, "v0.1.0", "stable",
		"slsa_provenance.v1", strings.Repeat("a", 64), "detached_signature.v1",
		strings.Repeat("b", 64), strings.Repeat("c", 64))
	if code != "" {
		t.Fatal(code)
	}
	return manifest
}
