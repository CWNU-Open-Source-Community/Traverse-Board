package analyzer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	AnalyzerProvenanceStatementProtocolVersion     = "analyzer_provenance_statement.v1"
	AnalyzerProvenanceVerificationProtocolVersion  = "analyzer_provenance_verification.v1"
	AnalyzerProvenanceSignatureScheme              = "ed25519.v1"
	MaxAnalyzerProvenanceStatementEnvelopeBytes    = 8 * 1024
	MaxAnalyzerProvenanceVerificationEnvelopeBytes = 6 * 1024
	analyzerProvenanceSigningDomain                = "cyberagent-workbench/analyzer-provenance/v1\x00"
)

// AnalyzerProvenanceStatement is the canonical caller-owned byte payload that
// a release signer attests. It contains digests and release metadata only.
type AnalyzerProvenanceStatement struct {
	ProtocolVersion                string `json:"protocol_version"`
	Analyzer                       string `json:"analyzer"`
	ReleaseVersion                 string `json:"release_version"`
	ReleaseChannel                 string `json:"release_channel"`
	TargetGOOS                     string `json:"target_goos"`
	TargetGOARCH                   string `json:"target_goarch"`
	ExecutableFormat               string `json:"executable_format"`
	ExecutableBytes                int    `json:"executable_bytes"`
	ExecutableSHA256               string `json:"executable_sha256"`
	ExecutableFormatEvidenceSHA256 string `json:"executable_format_evidence_sha256"`
	ProvenanceType                 string `json:"provenance_type"`
	SignatureScheme                string `json:"signature_scheme"`
	SignerIdentitySHA256           string `json:"signer_identity_sha256"`
	SourceRepositorySHA256         string `json:"source_repository_sha256"`
	SourceRevisionSHA256           string `json:"source_revision_sha256"`
	BuildRecipeSHA256              string `json:"build_recipe_sha256"`
	CallerBytesOnly                bool   `json:"caller_bytes_only"`
	NetworkLoaded                  bool   `json:"network_loaded"`
	PathIncluded                   bool   `json:"path_included"`
}

// AnalyzerProvenanceVerification proves that one exact canonical statement
// was signed by the allowlisted Ed25519 identity. It deliberately grants no
// platform, release, process, product, filesystem, network, or persistence
// authority.
type AnalyzerProvenanceVerification struct {
	ProtocolVersion                 string `json:"protocol_version"`
	ReleaseCandidateSHA256          string `json:"release_candidate_sha256"`
	ManifestSHA256                  string `json:"manifest_sha256"`
	ProvenanceStatementSHA256       string `json:"provenance_statement_sha256"`
	SignatureEnvelopeSHA256         string `json:"signature_envelope_sha256"`
	SignerIdentitySHA256            string `json:"signer_identity_sha256"`
	SignatureScheme                 string `json:"signature_scheme"`
	Analyzer                        string `json:"analyzer"`
	ReleaseVersion                  string `json:"release_version"`
	TargetGOOS                      string `json:"target_goos"`
	TargetGOARCH                    string `json:"target_goarch"`
	ExecutableSHA256                string `json:"executable_sha256"`
	StatementCanonical              bool   `json:"statement_canonical"`
	StatementDigestMatched          bool   `json:"statement_digest_matched"`
	ManifestBindingVerified         bool   `json:"manifest_binding_verified"`
	ReleaseCandidateBindingVerified bool   `json:"release_candidate_binding_verified"`
	SignerIdentityMatched           bool   `json:"signer_identity_matched"`
	DetachedSignatureVerified       bool   `json:"detached_signature_verified"`
	CallerBytesVerified             bool   `json:"caller_bytes_verified"`
	PlatformSignatureVerified       bool   `json:"platform_signature_verified"`
	ImmutableHandleVerified         bool   `json:"immutable_handle_verified"`
	ReleaseApproved                 bool   `json:"release_approved"`
	ProcessStartEnabled             bool   `json:"process_start_enabled"`
	ProductInvocationEnabled        bool   `json:"product_invocation_enabled"`
	NetworkAuthorized               bool   `json:"network_authorized"`
	HostFilesystemAuthorized        bool   `json:"host_filesystem_authorized"`
	ResultPersistenceAuthorized     bool   `json:"result_persistence_authorized"`
	ArtifactCommitAuthorized        bool   `json:"artifact_commit_authorized"`
}

func BuildAnalyzerProvenanceStatement(evidence ExecutableFormatEvidence, releaseVersion,
	releaseChannel, provenanceType, signatureScheme, signerIdentitySHA256,
	sourceRepositorySHA256, sourceRevisionSHA256, buildRecipeSHA256 string,
) (AnalyzerProvenanceStatement, ErrorCode) {
	evidenceDigest, ok := canonicalSHA256(evidence)
	if !ok || evidence.ProtocolVersion != ExecutableFormatEvidenceProtocolVersion ||
		!evidence.ExecutableFormatVerified || !evidence.TargetArchitectureVerified ||
		evidence.ProcessStartEnabled || evidence.ProductInvocationEnabled ||
		!validAnalyzerReleaseToken(releaseVersion) ||
		!validAnalyzerReleaseToken(releaseChannel) ||
		!validAnalyzerReleaseToken(provenanceType) ||
		signatureScheme != AnalyzerProvenanceSignatureScheme ||
		!validDigest(signerIdentitySHA256) || !validDigest(sourceRepositorySHA256) ||
		!validDigest(sourceRevisionSHA256) || !validDigest(buildRecipeSHA256) {
		return AnalyzerProvenanceStatement{}, CodeInvalidContent
	}
	statement := AnalyzerProvenanceStatement{
		ProtocolVersion: AnalyzerProvenanceStatementProtocolVersion,
		Analyzer:        evidence.Analyzer, ReleaseVersion: releaseVersion,
		ReleaseChannel: releaseChannel, TargetGOOS: evidence.TargetGOOS,
		TargetGOARCH: evidence.TargetGOARCH, ExecutableFormat: evidence.ExecutableFormat,
		ExecutableBytes: evidence.ExecutableBytes, ExecutableSHA256: evidence.ExecutableSHA256,
		ExecutableFormatEvidenceSHA256: evidenceDigest, ProvenanceType: provenanceType,
		SignatureScheme: signatureScheme, SignerIdentitySHA256: signerIdentitySHA256,
		SourceRepositorySHA256: sourceRepositorySHA256,
		SourceRevisionSHA256:   sourceRevisionSHA256, BuildRecipeSHA256: buildRecipeSHA256,
		CallerBytesOnly: true,
	}
	if !validateAnalyzerProvenanceStatementStructure(statement, evidence) {
		return AnalyzerProvenanceStatement{}, CodeInternal
	}
	return statement, ""
}

func ValidateAnalyzerProvenanceStatement(statement AnalyzerProvenanceStatement,
	evidence ExecutableFormatEvidence,
) ErrorCode {
	expected, code := BuildAnalyzerProvenanceStatement(evidence, statement.ReleaseVersion,
		statement.ReleaseChannel, statement.ProvenanceType, statement.SignatureScheme,
		statement.SignerIdentitySHA256, statement.SourceRepositorySHA256,
		statement.SourceRevisionSHA256, statement.BuildRecipeSHA256)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(statement, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerProvenanceStatement(statement AnalyzerProvenanceStatement,
	evidence ExecutableFormatEvidence,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerProvenanceStatement(statement, evidence); code != "" {
		return nil, code
	}
	return encodeAnalyzerProvenanceValue(statement, MaxAnalyzerProvenanceStatementEnvelopeBytes)
}

func DecodeAnalyzerProvenanceStatement(raw []byte, evidence ExecutableFormatEvidence,
) (AnalyzerProvenanceStatement, ErrorCode) {
	statement, code := decodeCanonicalAnalyzerProvenanceStatement(raw)
	if code != "" || ValidateAnalyzerProvenanceStatement(statement, evidence) != "" {
		return AnalyzerProvenanceStatement{}, CodeInvalidResult
	}
	return statement, ""
}

// AnalyzerProvenanceSigningPayload returns a domain-separated copy of one
// canonical statement. The input is parsed strictly before it can be signed.
func AnalyzerProvenanceSigningPayload(rawStatement []byte) ([]byte, ErrorCode) {
	if _, code := decodeCanonicalAnalyzerProvenanceStatement(rawStatement); code != "" {
		return nil, code
	}
	payload := make([]byte, 0, len(analyzerProvenanceSigningDomain)+len(rawStatement))
	payload = append(payload, analyzerProvenanceSigningDomain...)
	payload = append(payload, rawStatement...)
	return payload, ""
}

func BuildAnalyzerProvenanceVerification(candidate InvocationCandidate, rawRequest,
	executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
	rawStatement, publicKey, detachedSignature []byte,
) (AnalyzerProvenanceVerification, ErrorCode) {
	if code := ValidateAnalyzerReleaseCandidate(release, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist); code != "" {
		return AnalyzerProvenanceVerification{}, CodeInvalidResult
	}
	statement, code := DecodeAnalyzerProvenanceStatement(rawStatement, evidence)
	if code != "" || !provenanceStatementMatchesManifest(statement, manifest) {
		return AnalyzerProvenanceVerification{}, CodeInvalidResult
	}
	if len(publicKey) != ed25519.PublicKeySize || len(detachedSignature) != ed25519.SignatureSize {
		return AnalyzerProvenanceVerification{}, CodeInvalidContent
	}
	statementDigest := analyzerProvenanceBytesSHA256(rawStatement)
	publicKeyDigest := analyzerProvenanceBytesSHA256(publicKey)
	signatureDigest := analyzerProvenanceBytesSHA256(detachedSignature)
	if statementDigest != manifest.ProvenanceStatementSHA256 ||
		publicKeyDigest != manifest.SignerIdentitySHA256 ||
		signatureDigest != manifest.SignatureEnvelopeSHA256 {
		return AnalyzerProvenanceVerification{}, CodeInvalidResult
	}
	payload, code := AnalyzerProvenanceSigningPayload(rawStatement)
	if code != "" || !ed25519.Verify(ed25519.PublicKey(publicKey), payload, detachedSignature) {
		return AnalyzerProvenanceVerification{}, CodeInvalidResult
	}
	releaseDigest, releaseOK := canonicalSHA256(release)
	manifestDigest, manifestOK := canonicalSHA256(manifest)
	if !releaseOK || !manifestOK {
		return AnalyzerProvenanceVerification{}, CodeInternal
	}
	verification := AnalyzerProvenanceVerification{
		ProtocolVersion:        AnalyzerProvenanceVerificationProtocolVersion,
		ReleaseCandidateSHA256: releaseDigest, ManifestSHA256: manifestDigest,
		ProvenanceStatementSHA256: statementDigest,
		SignatureEnvelopeSHA256:   signatureDigest, SignerIdentitySHA256: publicKeyDigest,
		SignatureScheme: manifest.SignatureScheme, Analyzer: manifest.Analyzer,
		ReleaseVersion: manifest.ReleaseVersion, TargetGOOS: manifest.TargetGOOS,
		TargetGOARCH: manifest.TargetGOARCH, ExecutableSHA256: manifest.ExecutableSHA256,
		StatementCanonical: true, StatementDigestMatched: true,
		ManifestBindingVerified: true, ReleaseCandidateBindingVerified: true,
		SignerIdentityMatched: true, DetachedSignatureVerified: true, CallerBytesVerified: true,
	}
	if !validateAnalyzerProvenanceVerificationStructure(verification, release, manifest) {
		return AnalyzerProvenanceVerification{}, CodeInternal
	}
	return verification, ""
}

func ValidateAnalyzerProvenanceVerification(verification AnalyzerProvenanceVerification,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate, rawStatement, publicKey, detachedSignature []byte,
) ErrorCode {
	expected, code := BuildAnalyzerProvenanceVerification(candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist, release, rawStatement, publicKey,
		detachedSignature)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(verification, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerProvenanceVerification(verification AnalyzerProvenanceVerification,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
	release AnalyzerReleaseCandidate, rawStatement, publicKey, detachedSignature []byte,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerProvenanceVerification(verification, candidate, rawRequest,
		executable, identity, preflight, evidence, manifest, allowlist, release, rawStatement,
		publicKey, detachedSignature); code != "" {
		return nil, code
	}
	return encodeAnalyzerProvenanceValue(verification,
		MaxAnalyzerProvenanceVerificationEnvelopeBytes)
}

func DecodeAnalyzerProvenanceVerification(raw []byte, candidate InvocationCandidate,
	rawRequest, executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist, release AnalyzerReleaseCandidate,
	rawStatement, publicKey, detachedSignature []byte,
) (AnalyzerProvenanceVerification, ErrorCode) {
	var wire analyzerProvenanceVerificationWire
	if !strictDecode(raw, MaxAnalyzerProvenanceVerificationEnvelopeBytes, &wire) ||
		!wire.complete() {
		return AnalyzerProvenanceVerification{}, CodeInvalidResult
	}
	verification := wire.value()
	if code := ValidateAnalyzerProvenanceVerification(verification, candidate, rawRequest,
		executable, identity, preflight, evidence, manifest, allowlist, release, rawStatement,
		publicKey, detachedSignature); code != "" {
		return AnalyzerProvenanceVerification{}, CodeInvalidResult
	}
	return verification, ""
}

func decodeCanonicalAnalyzerProvenanceStatement(raw []byte,
) (AnalyzerProvenanceStatement, ErrorCode) {
	var wire analyzerProvenanceStatementWire
	if !strictDecode(raw, MaxAnalyzerProvenanceStatementEnvelopeBytes, &wire) || !wire.complete() {
		return AnalyzerProvenanceStatement{}, CodeInvalidResult
	}
	statement := wire.value()
	canonical, err := json.Marshal(statement)
	if err != nil || !bytes.Equal(raw, canonical) || !validAnalyzerProvenanceStatementShape(statement) {
		return AnalyzerProvenanceStatement{}, CodeInvalidResult
	}
	return statement, ""
}

func encodeAnalyzerProvenanceValue(value any, maximum int) ([]byte, ErrorCode) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximum {
		return nil, CodeInternal
	}
	return encoded, ""
}

func validateAnalyzerProvenanceStatementStructure(statement AnalyzerProvenanceStatement,
	evidence ExecutableFormatEvidence,
) bool {
	evidenceDigest, ok := canonicalSHA256(evidence)
	return ok && validAnalyzerProvenanceStatementShape(statement) &&
		statement.Analyzer == evidence.Analyzer && statement.TargetGOOS == evidence.TargetGOOS &&
		statement.TargetGOARCH == evidence.TargetGOARCH &&
		statement.ExecutableFormat == evidence.ExecutableFormat &&
		statement.ExecutableBytes == evidence.ExecutableBytes &&
		statement.ExecutableSHA256 == evidence.ExecutableSHA256 &&
		statement.ExecutableFormatEvidenceSHA256 == evidenceDigest
}

func validAnalyzerProvenanceStatementShape(statement AnalyzerProvenanceStatement) bool {
	return statement.ProtocolVersion == AnalyzerProvenanceStatementProtocolVersion &&
		validAnalyzerReleaseToken(statement.Analyzer) &&
		validAnalyzerReleaseToken(statement.ReleaseVersion) &&
		validAnalyzerReleaseToken(statement.ReleaseChannel) &&
		validAnalyzerReleaseToken(statement.TargetGOOS) &&
		validAnalyzerReleaseToken(statement.TargetGOARCH) &&
		validAnalyzerReleaseToken(statement.ExecutableFormat) && statement.ExecutableBytes > 0 &&
		validDigest(statement.ExecutableSHA256) &&
		validDigest(statement.ExecutableFormatEvidenceSHA256) &&
		validAnalyzerReleaseToken(statement.ProvenanceType) &&
		statement.SignatureScheme == AnalyzerProvenanceSignatureScheme &&
		validDigest(statement.SignerIdentitySHA256) &&
		validDigest(statement.SourceRepositorySHA256) &&
		validDigest(statement.SourceRevisionSHA256) && validDigest(statement.BuildRecipeSHA256) &&
		statement.CallerBytesOnly && !statement.NetworkLoaded && !statement.PathIncluded
}

func provenanceStatementMatchesManifest(statement AnalyzerProvenanceStatement,
	manifest AnalyzerReleaseManifest,
) bool {
	return manifest.Analyzer == statement.Analyzer &&
		manifest.ReleaseVersion == statement.ReleaseVersion &&
		manifest.ReleaseChannel == statement.ReleaseChannel &&
		manifest.TargetGOOS == statement.TargetGOOS &&
		manifest.TargetGOARCH == statement.TargetGOARCH &&
		manifest.ExecutableFormat == statement.ExecutableFormat &&
		manifest.ExecutableBytes == statement.ExecutableBytes &&
		manifest.ExecutableSHA256 == statement.ExecutableSHA256 &&
		manifest.ExecutableFormatEvidenceSHA256 == statement.ExecutableFormatEvidenceSHA256 &&
		manifest.ProvenanceType == statement.ProvenanceType &&
		manifest.SignatureScheme == statement.SignatureScheme &&
		manifest.SignerIdentitySHA256 == statement.SignerIdentitySHA256
}

func validateAnalyzerProvenanceVerificationStructure(
	verification AnalyzerProvenanceVerification, release AnalyzerReleaseCandidate,
	manifest AnalyzerReleaseManifest,
) bool {
	releaseDigest, releaseOK := canonicalSHA256(release)
	manifestDigest, manifestOK := canonicalSHA256(manifest)
	return releaseOK && manifestOK &&
		verification.ProtocolVersion == AnalyzerProvenanceVerificationProtocolVersion &&
		verification.ReleaseCandidateSHA256 == releaseDigest &&
		verification.ManifestSHA256 == manifestDigest &&
		verification.ProvenanceStatementSHA256 == manifest.ProvenanceStatementSHA256 &&
		verification.SignatureEnvelopeSHA256 == manifest.SignatureEnvelopeSHA256 &&
		verification.SignerIdentitySHA256 == manifest.SignerIdentitySHA256 &&
		verification.SignatureScheme == AnalyzerProvenanceSignatureScheme &&
		verification.Analyzer == manifest.Analyzer &&
		verification.ReleaseVersion == manifest.ReleaseVersion &&
		verification.TargetGOOS == manifest.TargetGOOS &&
		verification.TargetGOARCH == manifest.TargetGOARCH &&
		verification.ExecutableSHA256 == manifest.ExecutableSHA256 &&
		verification.StatementCanonical && verification.StatementDigestMatched &&
		verification.ManifestBindingVerified && verification.ReleaseCandidateBindingVerified &&
		verification.SignerIdentityMatched && verification.DetachedSignatureVerified &&
		verification.CallerBytesVerified && !verification.PlatformSignatureVerified &&
		!verification.ImmutableHandleVerified && !verification.ReleaseApproved &&
		!verification.ProcessStartEnabled && !verification.ProductInvocationEnabled &&
		!verification.NetworkAuthorized && !verification.HostFilesystemAuthorized &&
		!verification.ResultPersistenceAuthorized && !verification.ArtifactCommitAuthorized
}

func analyzerProvenanceBytesSHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

type analyzerProvenanceStatementWire struct {
	ProtocolVersion                *string `json:"protocol_version"`
	Analyzer                       *string `json:"analyzer"`
	ReleaseVersion                 *string `json:"release_version"`
	ReleaseChannel                 *string `json:"release_channel"`
	TargetGOOS                     *string `json:"target_goos"`
	TargetGOARCH                   *string `json:"target_goarch"`
	ExecutableFormat               *string `json:"executable_format"`
	ExecutableBytes                *int    `json:"executable_bytes"`
	ExecutableSHA256               *string `json:"executable_sha256"`
	ExecutableFormatEvidenceSHA256 *string `json:"executable_format_evidence_sha256"`
	ProvenanceType                 *string `json:"provenance_type"`
	SignatureScheme                *string `json:"signature_scheme"`
	SignerIdentitySHA256           *string `json:"signer_identity_sha256"`
	SourceRepositorySHA256         *string `json:"source_repository_sha256"`
	SourceRevisionSHA256           *string `json:"source_revision_sha256"`
	BuildRecipeSHA256              *string `json:"build_recipe_sha256"`
	CallerBytesOnly                *bool   `json:"caller_bytes_only"`
	NetworkLoaded                  *bool   `json:"network_loaded"`
	PathIncluded                   *bool   `json:"path_included"`
}

func (wire analyzerProvenanceStatementWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.Analyzer != nil && wire.ReleaseVersion != nil &&
		wire.ReleaseChannel != nil && wire.TargetGOOS != nil && wire.TargetGOARCH != nil &&
		wire.ExecutableFormat != nil && wire.ExecutableBytes != nil &&
		wire.ExecutableSHA256 != nil && wire.ExecutableFormatEvidenceSHA256 != nil &&
		wire.ProvenanceType != nil && wire.SignatureScheme != nil &&
		wire.SignerIdentitySHA256 != nil && wire.SourceRepositorySHA256 != nil &&
		wire.SourceRevisionSHA256 != nil && wire.BuildRecipeSHA256 != nil &&
		wire.CallerBytesOnly != nil && wire.NetworkLoaded != nil && wire.PathIncluded != nil
}

func (wire analyzerProvenanceStatementWire) value() AnalyzerProvenanceStatement {
	return AnalyzerProvenanceStatement{
		ProtocolVersion: *wire.ProtocolVersion, Analyzer: *wire.Analyzer,
		ReleaseVersion: *wire.ReleaseVersion, ReleaseChannel: *wire.ReleaseChannel,
		TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableFormat: *wire.ExecutableFormat, ExecutableBytes: *wire.ExecutableBytes,
		ExecutableSHA256:               *wire.ExecutableSHA256,
		ExecutableFormatEvidenceSHA256: *wire.ExecutableFormatEvidenceSHA256,
		ProvenanceType:                 *wire.ProvenanceType, SignatureScheme: *wire.SignatureScheme,
		SignerIdentitySHA256:   *wire.SignerIdentitySHA256,
		SourceRepositorySHA256: *wire.SourceRepositorySHA256,
		SourceRevisionSHA256:   *wire.SourceRevisionSHA256,
		BuildRecipeSHA256:      *wire.BuildRecipeSHA256, CallerBytesOnly: *wire.CallerBytesOnly,
		NetworkLoaded: *wire.NetworkLoaded, PathIncluded: *wire.PathIncluded,
	}
}

type analyzerProvenanceVerificationWire struct {
	ProtocolVersion                 *string `json:"protocol_version"`
	ReleaseCandidateSHA256          *string `json:"release_candidate_sha256"`
	ManifestSHA256                  *string `json:"manifest_sha256"`
	ProvenanceStatementSHA256       *string `json:"provenance_statement_sha256"`
	SignatureEnvelopeSHA256         *string `json:"signature_envelope_sha256"`
	SignerIdentitySHA256            *string `json:"signer_identity_sha256"`
	SignatureScheme                 *string `json:"signature_scheme"`
	Analyzer                        *string `json:"analyzer"`
	ReleaseVersion                  *string `json:"release_version"`
	TargetGOOS                      *string `json:"target_goos"`
	TargetGOARCH                    *string `json:"target_goarch"`
	ExecutableSHA256                *string `json:"executable_sha256"`
	StatementCanonical              *bool   `json:"statement_canonical"`
	StatementDigestMatched          *bool   `json:"statement_digest_matched"`
	ManifestBindingVerified         *bool   `json:"manifest_binding_verified"`
	ReleaseCandidateBindingVerified *bool   `json:"release_candidate_binding_verified"`
	SignerIdentityMatched           *bool   `json:"signer_identity_matched"`
	DetachedSignatureVerified       *bool   `json:"detached_signature_verified"`
	CallerBytesVerified             *bool   `json:"caller_bytes_verified"`
	PlatformSignatureVerified       *bool   `json:"platform_signature_verified"`
	ImmutableHandleVerified         *bool   `json:"immutable_handle_verified"`
	ReleaseApproved                 *bool   `json:"release_approved"`
	ProcessStartEnabled             *bool   `json:"process_start_enabled"`
	ProductInvocationEnabled        *bool   `json:"product_invocation_enabled"`
	NetworkAuthorized               *bool   `json:"network_authorized"`
	HostFilesystemAuthorized        *bool   `json:"host_filesystem_authorized"`
	ResultPersistenceAuthorized     *bool   `json:"result_persistence_authorized"`
	ArtifactCommitAuthorized        *bool   `json:"artifact_commit_authorized"`
}

func (wire analyzerProvenanceVerificationWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.ReleaseCandidateSHA256 != nil &&
		wire.ManifestSHA256 != nil && wire.ProvenanceStatementSHA256 != nil &&
		wire.SignatureEnvelopeSHA256 != nil && wire.SignerIdentitySHA256 != nil &&
		wire.SignatureScheme != nil && wire.Analyzer != nil && wire.ReleaseVersion != nil &&
		wire.TargetGOOS != nil && wire.TargetGOARCH != nil && wire.ExecutableSHA256 != nil &&
		wire.StatementCanonical != nil && wire.StatementDigestMatched != nil &&
		wire.ManifestBindingVerified != nil && wire.ReleaseCandidateBindingVerified != nil &&
		wire.SignerIdentityMatched != nil && wire.DetachedSignatureVerified != nil &&
		wire.CallerBytesVerified != nil && wire.PlatformSignatureVerified != nil &&
		wire.ImmutableHandleVerified != nil && wire.ReleaseApproved != nil &&
		wire.ProcessStartEnabled != nil && wire.ProductInvocationEnabled != nil &&
		wire.NetworkAuthorized != nil && wire.HostFilesystemAuthorized != nil &&
		wire.ResultPersistenceAuthorized != nil && wire.ArtifactCommitAuthorized != nil
}

func (wire analyzerProvenanceVerificationWire) value() AnalyzerProvenanceVerification {
	return AnalyzerProvenanceVerification{
		ProtocolVersion:           *wire.ProtocolVersion,
		ReleaseCandidateSHA256:    *wire.ReleaseCandidateSHA256,
		ManifestSHA256:            *wire.ManifestSHA256,
		ProvenanceStatementSHA256: *wire.ProvenanceStatementSHA256,
		SignatureEnvelopeSHA256:   *wire.SignatureEnvelopeSHA256,
		SignerIdentitySHA256:      *wire.SignerIdentitySHA256,
		SignatureScheme:           *wire.SignatureScheme, Analyzer: *wire.Analyzer,
		ReleaseVersion: *wire.ReleaseVersion, TargetGOOS: *wire.TargetGOOS,
		TargetGOARCH: *wire.TargetGOARCH, ExecutableSHA256: *wire.ExecutableSHA256,
		StatementCanonical:              *wire.StatementCanonical,
		StatementDigestMatched:          *wire.StatementDigestMatched,
		ManifestBindingVerified:         *wire.ManifestBindingVerified,
		ReleaseCandidateBindingVerified: *wire.ReleaseCandidateBindingVerified,
		SignerIdentityMatched:           *wire.SignerIdentityMatched,
		DetachedSignatureVerified:       *wire.DetachedSignatureVerified,
		CallerBytesVerified:             *wire.CallerBytesVerified,
		PlatformSignatureVerified:       *wire.PlatformSignatureVerified,
		ImmutableHandleVerified:         *wire.ImmutableHandleVerified,
		ReleaseApproved:                 *wire.ReleaseApproved, ProcessStartEnabled: *wire.ProcessStartEnabled,
		ProductInvocationEnabled:    *wire.ProductInvocationEnabled,
		NetworkAuthorized:           *wire.NetworkAuthorized,
		HostFilesystemAuthorized:    *wire.HostFilesystemAuthorized,
		ResultPersistenceAuthorized: *wire.ResultPersistenceAuthorized,
		ArtifactCommitAuthorized:    *wire.ArtifactCommitAuthorized,
	}
}
