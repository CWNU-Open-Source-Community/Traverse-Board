package analyzer

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAnalyzerProvenanceVerificationAuthenticatesCallerBytesWithoutAuthority(t *testing.T) {
	chain := mustAnalyzerSignedReleaseChain(t)
	verification := chain.verification
	if verification.ProtocolVersion != AnalyzerProvenanceVerificationProtocolVersion ||
		!verification.StatementCanonical || !verification.StatementDigestMatched ||
		!verification.ManifestBindingVerified || !verification.ReleaseCandidateBindingVerified ||
		!verification.SignerIdentityMatched || !verification.DetachedSignatureVerified ||
		!verification.CallerBytesVerified || verification.PlatformSignatureVerified ||
		verification.ImmutableHandleVerified || verification.ReleaseApproved ||
		verification.ProcessStartEnabled || verification.ProductInvocationEnabled ||
		verification.NetworkAuthorized || verification.HostFilesystemAuthorized ||
		verification.ResultPersistenceAuthorized || verification.ArtifactCommitAuthorized {
		t.Fatalf("unsafe or incomplete provenance verification: %#v", verification)
	}

	encodedStatement, code := EncodeAnalyzerProvenanceStatement(chain.statement, chain.evidence)
	if code != "" || !bytes.Equal(encodedStatement, chain.rawStatement) {
		t.Fatalf("statement encode failed: code=%s", code)
	}
	decodedStatement, code := DecodeAnalyzerProvenanceStatement(encodedStatement, chain.evidence)
	if code != "" || !reflect.DeepEqual(decodedStatement, chain.statement) {
		t.Fatalf("statement round trip failed: code=%s value=%#v", code, decodedStatement)
	}
	encoded, code := EncodeAnalyzerProvenanceVerification(verification, chain.candidate,
		chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
		chain.manifest, chain.allowlist, chain.release, chain.rawStatement, chain.publicKey,
		chain.signature)
	if code != "" {
		t.Fatal(code)
	}
	decoded, code := DecodeAnalyzerProvenanceVerification(encoded, chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
		chain.allowlist, chain.release, chain.rawStatement, chain.publicKey, chain.signature)
	if code != "" || !reflect.DeepEqual(decoded, verification) {
		t.Fatalf("verification round trip failed: code=%s value=%#v", code, decoded)
	}
	for name, secret := range map[string]string{
		"public key": base64.StdEncoding.EncodeToString(chain.publicKey),
		"signature":  base64.StdEncoding.EncodeToString(chain.signature),
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("%s bytes leaked into verification envelope", name)
		}
	}
	assertExactObjectKeys(t, encoded, []string{"analyzer", "artifact_commit_authorized",
		"caller_bytes_verified", "detached_signature_verified", "executable_sha256",
		"host_filesystem_authorized", "immutable_handle_verified",
		"manifest_binding_verified", "manifest_sha256", "network_authorized",
		"platform_signature_verified", "process_start_enabled", "product_invocation_enabled",
		"protocol_version", "provenance_statement_sha256", "release_approved",
		"release_candidate_binding_verified", "release_candidate_sha256", "release_version",
		"result_persistence_authorized", "signature_envelope_sha256", "signature_scheme",
		"signer_identity_matched", "signer_identity_sha256", "statement_canonical",
		"statement_digest_matched", "target_goarch", "target_goos"})
}

func TestAnalyzerProvenanceVerificationRejectsByteAndSignatureDrift(t *testing.T) {
	chain := mustAnalyzerSignedReleaseChain(t)
	mutatedStatement := chain.statement
	mutatedStatement.SourceRevisionSHA256 = strings.Repeat("9", 64)
	mutatedRaw, _ := json.Marshal(mutatedStatement)
	badSignature := append([]byte(nil), chain.signature...)
	badSignature[0] ^= 0x01
	otherPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x61}, ed25519.SeedSize))
	otherPublic := otherPrivate.Public().(ed25519.PublicKey)
	domainless := ed25519.Sign(chain.privateKey, chain.rawStatement)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, chain.rawStatement, "", "  "); err != nil {
		t.Fatal(err)
	}

	for name, testCase := range map[string]struct {
		raw, publicKey, signature []byte
	}{
		"statement drift": {mutatedRaw, chain.publicKey, chain.signature},
		"signature drift": {chain.rawStatement, chain.publicKey, badSignature},
		"signer drift":    {chain.rawStatement, otherPublic, chain.signature},
		"domainless":      {chain.rawStatement, chain.publicKey, domainless},
		"noncanonical":    {pretty.Bytes(), chain.publicKey, chain.signature},
	} {
		t.Run(name, func(t *testing.T) {
			if _, code := BuildAnalyzerProvenanceVerification(chain.candidate, chain.raw,
				chain.executable, chain.identity, chain.preflight, chain.evidence, chain.manifest,
				chain.allowlist, chain.release, testCase.raw, testCase.publicKey,
				testCase.signature); code == "" {
				t.Fatal("drift was accepted")
			}
		})
	}
}

func TestAnalyzerProvenanceVerificationRejectsSchemaWidening(t *testing.T) {
	chain := mustAnalyzerSignedReleaseChain(t)
	encoded, code := EncodeAnalyzerProvenanceVerification(chain.verification, chain.candidate,
		chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
		chain.manifest, chain.allowlist, chain.release, chain.rawStatement, chain.publicKey,
		chain.signature)
	if code != "" {
		t.Fatal(code)
	}
	text := string(encoded)
	for name, malformed := range map[string]string{
		"future": strings.Replace(text, AnalyzerProvenanceVerificationProtocolVersion,
			"analyzer_provenance_verification.v2", 1),
		"unknown": strings.Replace(text, `"release_approved":false`,
			`"release_approved":false,"executable_path":"tool"`, 1),
		"duplicate": strings.Replace(text, `"process_start_enabled":false`,
			`"process_start_enabled":false,"process_start_enabled":false`, 1),
		"missing false": strings.Replace(text, `,"network_authorized":false`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, got := DecodeAnalyzerProvenanceVerification([]byte(malformed), chain.candidate,
				chain.raw, chain.executable, chain.identity, chain.preflight, chain.evidence,
				chain.manifest, chain.allowlist, chain.release, chain.rawStatement,
				chain.publicKey, chain.signature); got != CodeInvalidResult {
				t.Fatalf("schema drift code = %s", got)
			}
		})
	}
}

type analyzerSignedReleaseChain struct {
	analyzerExecutableEvidenceChain
	privateKey   ed25519.PrivateKey
	publicKey    ed25519.PublicKey
	statement    AnalyzerProvenanceStatement
	rawStatement []byte
	signature    []byte
	manifest     AnalyzerReleaseManifest
	allowlist    AnalyzerReleaseAllowlist
	release      AnalyzerReleaseCandidate
	verification AnalyzerProvenanceVerification
}

func mustAnalyzerSignedReleaseChain(t *testing.T) analyzerSignedReleaseChain {
	t.Helper()
	chain := mustAnalyzerExecutableEvidenceChain(t)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	publicKey := append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
	statement, code := BuildAnalyzerProvenanceStatement(chain.evidence, "v0.1.0", "stable",
		"slsa_provenance.v1", AnalyzerProvenanceSignatureScheme,
		analyzerProvenanceBytesSHA256(publicKey), strings.Repeat("d", 64),
		strings.Repeat("e", 64), strings.Repeat("f", 64))
	if code != "" {
		t.Fatal(code)
	}
	rawStatement, code := EncodeAnalyzerProvenanceStatement(statement, chain.evidence)
	if code != "" {
		t.Fatal(code)
	}
	payload, code := AnalyzerProvenanceSigningPayload(rawStatement)
	if code != "" {
		t.Fatal(code)
	}
	signature := ed25519.Sign(privateKey, payload)
	manifest, code := BuildAnalyzerReleaseManifest(chain.evidence, statement.ReleaseVersion,
		statement.ReleaseChannel, statement.ProvenanceType,
		analyzerProvenanceBytesSHA256(rawStatement), statement.SignatureScheme,
		analyzerProvenanceBytesSHA256(publicKey), analyzerProvenanceBytesSHA256(signature))
	if code != "" {
		t.Fatal(code)
	}
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
	verification, code := BuildAnalyzerProvenanceVerification(chain.candidate, chain.raw,
		chain.executable, chain.identity, chain.preflight, chain.evidence, manifest, allowlist,
		release, rawStatement, publicKey, signature)
	if code != "" {
		t.Fatal(code)
	}
	return analyzerSignedReleaseChain{analyzerExecutableEvidenceChain: chain,
		privateKey: privateKey, publicKey: publicKey, statement: statement,
		rawStatement: rawStatement, signature: signature, manifest: manifest,
		allowlist: allowlist, release: release, verification: verification}
}
