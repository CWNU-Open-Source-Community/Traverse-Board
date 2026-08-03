package analyzer

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

const (
	AnalyzerReleaseManifestProtocolVersion   = "analyzer_release_manifest.v1"
	AnalyzerReleaseAllowlistProtocolVersion  = "analyzer_release_allowlist.v1"
	AnalyzerReleaseCandidateProtocolVersion  = "analyzer_release_candidate.v1"
	MaxAnalyzerReleaseManifestEnvelopeBytes  = 4 * 1024
	MaxAnalyzerReleaseAllowlistEnvelopeBytes = 32 * 1024
	MaxAnalyzerReleaseCandidateEnvelopeBytes = 5 * 1024
	MaxAnalyzerReleaseAllowlistEntries       = 64
	maxAnalyzerReleaseTokenBytes             = 128
)

// AnalyzerReleaseManifest is a digest-only supply-chain declaration. It does
// not carry signature bytes and does not claim that cryptography was verified.
type AnalyzerReleaseManifest struct {
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
	ProvenanceStatementSHA256      string `json:"provenance_statement_sha256"`
	SignatureScheme                string `json:"signature_scheme"`
	SignerIdentitySHA256           string `json:"signer_identity_sha256"`
	SignatureEnvelopeSHA256        string `json:"signature_envelope_sha256"`
	DigestOnly                     bool   `json:"digest_only"`
	SignatureBytesIncluded         bool   `json:"signature_bytes_included"`
	NetworkLoaded                  bool   `json:"network_loaded"`
	PathIncluded                   bool   `json:"path_included"`
}

// AnalyzerReleaseAllowlistEntry is one exact operator-reviewed declaration.
// A match is a policy fact, not proof that the declared signature is valid.
type AnalyzerReleaseAllowlistEntry struct {
	ManifestSHA256            string `json:"manifest_sha256"`
	Analyzer                  string `json:"analyzer"`
	ReleaseVersion            string `json:"release_version"`
	TargetGOOS                string `json:"target_goos"`
	TargetGOARCH              string `json:"target_goarch"`
	ExecutableSHA256          string `json:"executable_sha256"`
	ProvenanceStatementSHA256 string `json:"provenance_statement_sha256"`
	SignatureScheme           string `json:"signature_scheme"`
	SignerIdentitySHA256      string `json:"signer_identity_sha256"`
	SignatureEnvelopeSHA256   string `json:"signature_envelope_sha256"`
}

type AnalyzerReleaseAllowlist struct {
	ProtocolVersion   string                          `json:"protocol_version"`
	Entries           []AnalyzerReleaseAllowlistEntry `json:"entries"`
	OperatorManaged   bool                            `json:"operator_managed"`
	NetworkLoaded     bool                            `json:"network_loaded"`
	EnvironmentLoaded bool                            `json:"environment_loaded"`
}

// AnalyzerReleaseCandidate binds one manifest and one operator allowlist to
// the exact format evidence. Every execution and release authority remains
// closed until separate cryptographic and platform controls exist.
type AnalyzerReleaseCandidate struct {
	ProtocolVersion                string `json:"protocol_version"`
	ExecutableFormatEvidenceSHA256 string `json:"executable_format_evidence_sha256"`
	ManifestSHA256                 string `json:"manifest_sha256"`
	AllowlistSHA256                string `json:"allowlist_sha256"`
	Analyzer                       string `json:"analyzer"`
	ReleaseVersion                 string `json:"release_version"`
	TargetGOOS                     string `json:"target_goos"`
	TargetGOARCH                   string `json:"target_goarch"`
	ExecutableSHA256               string `json:"executable_sha256"`
	AllowlistEntryCount            int    `json:"allowlist_entry_count"`
	ManifestDigestPinned           bool   `json:"manifest_digest_pinned"`
	ExecutableDigestPinned         bool   `json:"executable_digest_pinned"`
	FormatEvidenceDigestPinned     bool   `json:"format_evidence_digest_pinned"`
	ProvenanceDigestPinned         bool   `json:"provenance_digest_pinned"`
	SignatureEnvelopeDigestPinned  bool   `json:"signature_envelope_digest_pinned"`
	OperatorAllowlistMatched       bool   `json:"operator_allowlist_matched"`
	ProvenanceStatementVerified    bool   `json:"provenance_statement_verified"`
	CryptographicSignatureVerified bool   `json:"cryptographic_signature_verified"`
	PlatformSignatureVerified      bool   `json:"platform_signature_verified"`
	ImmutableHandleVerified        bool   `json:"immutable_handle_verified"`
	OperatorReviewRequired         bool   `json:"operator_review_required"`
	ReleaseApproved                bool   `json:"release_approved"`
	ProcessStartEnabled            bool   `json:"process_start_enabled"`
	ProductInvocationEnabled       bool   `json:"product_invocation_enabled"`
	NetworkAuthorized              bool   `json:"network_authorized"`
	HostFilesystemAuthorized       bool   `json:"host_filesystem_authorized"`
}

func BuildAnalyzerReleaseManifest(evidence ExecutableFormatEvidence, releaseVersion,
	releaseChannel, provenanceType, provenanceStatementSHA256, signatureScheme,
	signerIdentitySHA256, signatureEnvelopeSHA256 string,
) (AnalyzerReleaseManifest, ErrorCode) {
	evidenceDigest, ok := canonicalSHA256(evidence)
	if !ok || evidence.ProtocolVersion != ExecutableFormatEvidenceProtocolVersion ||
		!evidence.ExecutableFormatVerified || !evidence.TargetArchitectureVerified ||
		evidence.ProcessStartEnabled || evidence.ProductInvocationEnabled ||
		!validAnalyzerReleaseToken(releaseVersion) ||
		!validAnalyzerReleaseToken(releaseChannel) ||
		!validAnalyzerReleaseToken(provenanceType) ||
		!validAnalyzerReleaseToken(signatureScheme) ||
		!validDigest(provenanceStatementSHA256) || !validDigest(signerIdentitySHA256) ||
		!validDigest(signatureEnvelopeSHA256) {
		return AnalyzerReleaseManifest{}, CodeInvalidContent
	}
	manifest := AnalyzerReleaseManifest{
		ProtocolVersion: AnalyzerReleaseManifestProtocolVersion, Analyzer: evidence.Analyzer,
		ReleaseVersion: releaseVersion, ReleaseChannel: releaseChannel,
		TargetGOOS: evidence.TargetGOOS, TargetGOARCH: evidence.TargetGOARCH,
		ExecutableFormat: evidence.ExecutableFormat, ExecutableBytes: evidence.ExecutableBytes,
		ExecutableSHA256:               evidence.ExecutableSHA256,
		ExecutableFormatEvidenceSHA256: evidenceDigest, ProvenanceType: provenanceType,
		ProvenanceStatementSHA256: provenanceStatementSHA256, SignatureScheme: signatureScheme,
		SignerIdentitySHA256:    signerIdentitySHA256,
		SignatureEnvelopeSHA256: signatureEnvelopeSHA256, DigestOnly: true,
	}
	if !validateAnalyzerReleaseManifestStructure(manifest, evidence) {
		return AnalyzerReleaseManifest{}, CodeInternal
	}
	return manifest, ""
}

func ValidateAnalyzerReleaseManifest(manifest AnalyzerReleaseManifest,
	evidence ExecutableFormatEvidence,
) ErrorCode {
	expected, code := BuildAnalyzerReleaseManifest(evidence, manifest.ReleaseVersion,
		manifest.ReleaseChannel, manifest.ProvenanceType, manifest.ProvenanceStatementSHA256,
		manifest.SignatureScheme, manifest.SignerIdentitySHA256,
		manifest.SignatureEnvelopeSHA256)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(manifest, expected) {
		return CodeInvalidResult
	}
	return ""
}

func BuildAnalyzerReleaseAllowlist(entries []AnalyzerReleaseAllowlistEntry,
) (AnalyzerReleaseAllowlist, ErrorCode) {
	if len(entries) == 0 || len(entries) > MaxAnalyzerReleaseAllowlistEntries {
		return AnalyzerReleaseAllowlist{}, CodeInvalidContent
	}
	cloned := append([]AnalyzerReleaseAllowlistEntry(nil), entries...)
	for _, entry := range cloned {
		if !validAnalyzerReleaseAllowlistEntry(entry) {
			return AnalyzerReleaseAllowlist{}, CodeInvalidContent
		}
	}
	sort.Slice(cloned, func(left, right int) bool {
		return analyzerReleaseAllowlistEntryKey(cloned[left]) <
			analyzerReleaseAllowlistEntryKey(cloned[right])
	})
	for index := 1; index < len(cloned); index++ {
		if analyzerReleaseAllowlistEntryKey(cloned[index-1]) ==
			analyzerReleaseAllowlistEntryKey(cloned[index]) {
			return AnalyzerReleaseAllowlist{}, CodeInvalidContent
		}
	}
	return AnalyzerReleaseAllowlist{
		ProtocolVersion: AnalyzerReleaseAllowlistProtocolVersion, Entries: cloned,
		OperatorManaged: true,
	}, ""
}

func ValidateAnalyzerReleaseAllowlist(allowlist AnalyzerReleaseAllowlist) ErrorCode {
	expected, code := BuildAnalyzerReleaseAllowlist(allowlist.Entries)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(allowlist, expected) {
		return CodeInvalidResult
	}
	return ""
}

func AnalyzerReleaseAllowlistEntryForManifest(manifest AnalyzerReleaseManifest,
	evidence ExecutableFormatEvidence,
) (AnalyzerReleaseAllowlistEntry, ErrorCode) {
	if code := ValidateAnalyzerReleaseManifest(manifest, evidence); code != "" {
		return AnalyzerReleaseAllowlistEntry{}, code
	}
	manifestDigest, ok := canonicalSHA256(manifest)
	if !ok {
		return AnalyzerReleaseAllowlistEntry{}, CodeInternal
	}
	return AnalyzerReleaseAllowlistEntry{
		ManifestSHA256: manifestDigest, Analyzer: manifest.Analyzer,
		ReleaseVersion: manifest.ReleaseVersion, TargetGOOS: manifest.TargetGOOS,
		TargetGOARCH: manifest.TargetGOARCH, ExecutableSHA256: manifest.ExecutableSHA256,
		ProvenanceStatementSHA256: manifest.ProvenanceStatementSHA256,
		SignatureScheme:           manifest.SignatureScheme,
		SignerIdentitySHA256:      manifest.SignerIdentitySHA256,
		SignatureEnvelopeSHA256:   manifest.SignatureEnvelopeSHA256,
	}, ""
}

func BuildAnalyzerReleaseCandidate(candidate InvocationCandidate, rawRequest, executable []byte,
	identity ExecutableIdentity, preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
) (AnalyzerReleaseCandidate, ErrorCode) {
	if code := ValidateExecutableFormatEvidence(evidence, candidate, rawRequest, executable,
		identity, preflight); code != "" {
		return AnalyzerReleaseCandidate{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerReleaseManifest(manifest, evidence); code != "" {
		return AnalyzerReleaseCandidate{}, CodeInvalidResult
	}
	if code := ValidateAnalyzerReleaseAllowlist(allowlist); code != "" {
		return AnalyzerReleaseCandidate{}, CodeInvalidResult
	}
	expectedEntry, code := AnalyzerReleaseAllowlistEntryForManifest(manifest, evidence)
	if code != "" {
		return AnalyzerReleaseCandidate{}, CodeInvalidResult
	}
	matchCount := 0
	for _, entry := range allowlist.Entries {
		if reflect.DeepEqual(entry, expectedEntry) {
			matchCount++
		}
	}
	if matchCount != 1 {
		return AnalyzerReleaseCandidate{}, CodeInvalidContent
	}
	evidenceDigest, evidenceOK := canonicalSHA256(evidence)
	manifestDigest, manifestOK := canonicalSHA256(manifest)
	allowlistDigest, allowlistOK := canonicalSHA256(allowlist)
	if !evidenceOK || !manifestOK || !allowlistOK {
		return AnalyzerReleaseCandidate{}, CodeInternal
	}
	release := AnalyzerReleaseCandidate{
		ProtocolVersion:                AnalyzerReleaseCandidateProtocolVersion,
		ExecutableFormatEvidenceSHA256: evidenceDigest, ManifestSHA256: manifestDigest,
		AllowlistSHA256: allowlistDigest, Analyzer: manifest.Analyzer,
		ReleaseVersion: manifest.ReleaseVersion, TargetGOOS: manifest.TargetGOOS,
		TargetGOARCH: manifest.TargetGOARCH, ExecutableSHA256: manifest.ExecutableSHA256,
		AllowlistEntryCount: len(allowlist.Entries), ManifestDigestPinned: true,
		ExecutableDigestPinned: true, FormatEvidenceDigestPinned: true,
		ProvenanceDigestPinned: true, SignatureEnvelopeDigestPinned: true,
		OperatorAllowlistMatched: true, OperatorReviewRequired: true,
	}
	if !validateAnalyzerReleaseCandidateStructure(release, evidence, manifest, allowlist) {
		return AnalyzerReleaseCandidate{}, CodeInternal
	}
	return release, ""
}

func ValidateAnalyzerReleaseCandidate(release AnalyzerReleaseCandidate,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
) ErrorCode {
	expected, code := BuildAnalyzerReleaseCandidate(candidate, rawRequest, executable, identity,
		preflight, evidence, manifest, allowlist)
	if code != "" {
		return code
	}
	if !reflect.DeepEqual(release, expected) {
		return CodeInvalidResult
	}
	return ""
}

func EncodeAnalyzerReleaseManifest(manifest AnalyzerReleaseManifest,
	evidence ExecutableFormatEvidence,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerReleaseManifest(manifest, evidence); code != "" {
		return nil, code
	}
	return encodeAnalyzerReleaseValue(manifest, MaxAnalyzerReleaseManifestEnvelopeBytes)
}

func DecodeAnalyzerReleaseManifest(raw []byte, evidence ExecutableFormatEvidence,
) (AnalyzerReleaseManifest, ErrorCode) {
	var wire analyzerReleaseManifestWire
	if !strictDecode(raw, MaxAnalyzerReleaseManifestEnvelopeBytes, &wire) || !wire.complete() {
		return AnalyzerReleaseManifest{}, CodeInvalidResult
	}
	manifest := wire.value()
	if code := ValidateAnalyzerReleaseManifest(manifest, evidence); code != "" {
		return AnalyzerReleaseManifest{}, CodeInvalidResult
	}
	return manifest, ""
}

func EncodeAnalyzerReleaseAllowlist(allowlist AnalyzerReleaseAllowlist) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerReleaseAllowlist(allowlist); code != "" {
		return nil, code
	}
	return encodeAnalyzerReleaseValue(allowlist, MaxAnalyzerReleaseAllowlistEnvelopeBytes)
}

func DecodeAnalyzerReleaseAllowlist(raw []byte) (AnalyzerReleaseAllowlist, ErrorCode) {
	var wire analyzerReleaseAllowlistWire
	if !strictDecode(raw, MaxAnalyzerReleaseAllowlistEnvelopeBytes, &wire) || !wire.complete() {
		return AnalyzerReleaseAllowlist{}, CodeInvalidResult
	}
	allowlist := wire.value()
	if code := ValidateAnalyzerReleaseAllowlist(allowlist); code != "" {
		return AnalyzerReleaseAllowlist{}, CodeInvalidResult
	}
	return allowlist, ""
}

func EncodeAnalyzerReleaseCandidate(release AnalyzerReleaseCandidate,
	candidate InvocationCandidate, rawRequest, executable []byte, identity ExecutableIdentity,
	preflight InvocationPreflight, evidence ExecutableFormatEvidence,
	manifest AnalyzerReleaseManifest, allowlist AnalyzerReleaseAllowlist,
) ([]byte, ErrorCode) {
	if code := ValidateAnalyzerReleaseCandidate(release, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist); code != "" {
		return nil, code
	}
	return encodeAnalyzerReleaseValue(release, MaxAnalyzerReleaseCandidateEnvelopeBytes)
}

func DecodeAnalyzerReleaseCandidate(raw []byte, candidate InvocationCandidate,
	rawRequest, executable []byte, identity ExecutableIdentity, preflight InvocationPreflight,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist,
) (AnalyzerReleaseCandidate, ErrorCode) {
	var wire analyzerReleaseCandidateWire
	if !strictDecode(raw, MaxAnalyzerReleaseCandidateEnvelopeBytes, &wire) || !wire.complete() {
		return AnalyzerReleaseCandidate{}, CodeInvalidResult
	}
	release := wire.value()
	if code := ValidateAnalyzerReleaseCandidate(release, candidate, rawRequest, executable,
		identity, preflight, evidence, manifest, allowlist); code != "" {
		return AnalyzerReleaseCandidate{}, CodeInvalidResult
	}
	return release, ""
}

func encodeAnalyzerReleaseValue(value any, maximum int) ([]byte, ErrorCode) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maximum {
		return nil, CodeInternal
	}
	return encoded, ""
}

func validateAnalyzerReleaseManifestStructure(manifest AnalyzerReleaseManifest,
	evidence ExecutableFormatEvidence,
) bool {
	evidenceDigest, ok := canonicalSHA256(evidence)
	return ok && manifest.ProtocolVersion == AnalyzerReleaseManifestProtocolVersion &&
		manifest.Analyzer == evidence.Analyzer &&
		validAnalyzerReleaseToken(manifest.ReleaseVersion) &&
		validAnalyzerReleaseToken(manifest.ReleaseChannel) &&
		manifest.TargetGOOS == evidence.TargetGOOS && manifest.TargetGOARCH == evidence.TargetGOARCH &&
		manifest.ExecutableFormat == evidence.ExecutableFormat &&
		manifest.ExecutableBytes == evidence.ExecutableBytes &&
		manifest.ExecutableSHA256 == evidence.ExecutableSHA256 &&
		manifest.ExecutableFormatEvidenceSHA256 == evidenceDigest &&
		validAnalyzerReleaseToken(manifest.ProvenanceType) &&
		validDigest(manifest.ProvenanceStatementSHA256) &&
		validAnalyzerReleaseToken(manifest.SignatureScheme) &&
		validDigest(manifest.SignerIdentitySHA256) &&
		validDigest(manifest.SignatureEnvelopeSHA256) && manifest.DigestOnly &&
		!manifest.SignatureBytesIncluded && !manifest.NetworkLoaded && !manifest.PathIncluded
}

func validateAnalyzerReleaseCandidateStructure(release AnalyzerReleaseCandidate,
	evidence ExecutableFormatEvidence, manifest AnalyzerReleaseManifest,
	allowlist AnalyzerReleaseAllowlist,
) bool {
	evidenceDigest, evidenceOK := canonicalSHA256(evidence)
	manifestDigest, manifestOK := canonicalSHA256(manifest)
	allowlistDigest, allowlistOK := canonicalSHA256(allowlist)
	return evidenceOK && manifestOK && allowlistOK &&
		release.ProtocolVersion == AnalyzerReleaseCandidateProtocolVersion &&
		release.ExecutableFormatEvidenceSHA256 == evidenceDigest &&
		release.ManifestSHA256 == manifestDigest && release.AllowlistSHA256 == allowlistDigest &&
		release.Analyzer == manifest.Analyzer && release.ReleaseVersion == manifest.ReleaseVersion &&
		release.TargetGOOS == manifest.TargetGOOS && release.TargetGOARCH == manifest.TargetGOARCH &&
		release.ExecutableSHA256 == manifest.ExecutableSHA256 &&
		release.AllowlistEntryCount == len(allowlist.Entries) && release.ManifestDigestPinned &&
		release.ExecutableDigestPinned && release.FormatEvidenceDigestPinned &&
		release.ProvenanceDigestPinned && release.SignatureEnvelopeDigestPinned &&
		release.OperatorAllowlistMatched && !release.ProvenanceStatementVerified &&
		!release.CryptographicSignatureVerified && !release.PlatformSignatureVerified &&
		!release.ImmutableHandleVerified && release.OperatorReviewRequired &&
		!release.ReleaseApproved && !release.ProcessStartEnabled &&
		!release.ProductInvocationEnabled && !release.NetworkAuthorized &&
		!release.HostFilesystemAuthorized
}

func validAnalyzerReleaseAllowlistEntry(entry AnalyzerReleaseAllowlistEntry) bool {
	return validDigest(entry.ManifestSHA256) && validAnalyzerReleaseToken(entry.Analyzer) &&
		validAnalyzerReleaseToken(entry.ReleaseVersion) &&
		validAnalyzerReleaseToken(entry.TargetGOOS) &&
		validAnalyzerReleaseToken(entry.TargetGOARCH) && validDigest(entry.ExecutableSHA256) &&
		validDigest(entry.ProvenanceStatementSHA256) &&
		validAnalyzerReleaseToken(entry.SignatureScheme) &&
		validDigest(entry.SignerIdentitySHA256) && validDigest(entry.SignatureEnvelopeSHA256)
}

func validAnalyzerReleaseToken(value string) bool {
	if len(value) == 0 || len(value) > maxAnalyzerReleaseTokenBytes {
		return false
	}
	for _, char := range []byte(value) {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:+@/-", rune(char))) {
			return false
		}
	}
	return true
}

func analyzerReleaseAllowlistEntryKey(entry AnalyzerReleaseAllowlistEntry) string {
	return strings.Join([]string{entry.Analyzer, entry.ReleaseVersion, entry.TargetGOOS,
		entry.TargetGOARCH, entry.ManifestSHA256, entry.ExecutableSHA256,
		entry.ProvenanceStatementSHA256, entry.SignatureScheme, entry.SignerIdentitySHA256,
		entry.SignatureEnvelopeSHA256}, "\x00")
}

type analyzerReleaseManifestWire struct {
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
	ProvenanceStatementSHA256      *string `json:"provenance_statement_sha256"`
	SignatureScheme                *string `json:"signature_scheme"`
	SignerIdentitySHA256           *string `json:"signer_identity_sha256"`
	SignatureEnvelopeSHA256        *string `json:"signature_envelope_sha256"`
	DigestOnly                     *bool   `json:"digest_only"`
	SignatureBytesIncluded         *bool   `json:"signature_bytes_included"`
	NetworkLoaded                  *bool   `json:"network_loaded"`
	PathIncluded                   *bool   `json:"path_included"`
}

func (wire analyzerReleaseManifestWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.Analyzer != nil && wire.ReleaseVersion != nil &&
		wire.ReleaseChannel != nil && wire.TargetGOOS != nil && wire.TargetGOARCH != nil &&
		wire.ExecutableFormat != nil && wire.ExecutableBytes != nil &&
		wire.ExecutableSHA256 != nil && wire.ExecutableFormatEvidenceSHA256 != nil &&
		wire.ProvenanceType != nil && wire.ProvenanceStatementSHA256 != nil &&
		wire.SignatureScheme != nil && wire.SignerIdentitySHA256 != nil &&
		wire.SignatureEnvelopeSHA256 != nil && wire.DigestOnly != nil &&
		wire.SignatureBytesIncluded != nil && wire.NetworkLoaded != nil && wire.PathIncluded != nil
}

func (wire analyzerReleaseManifestWire) value() AnalyzerReleaseManifest {
	return AnalyzerReleaseManifest{
		ProtocolVersion: *wire.ProtocolVersion, Analyzer: *wire.Analyzer,
		ReleaseVersion: *wire.ReleaseVersion, ReleaseChannel: *wire.ReleaseChannel,
		TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableFormat: *wire.ExecutableFormat, ExecutableBytes: *wire.ExecutableBytes,
		ExecutableSHA256:               *wire.ExecutableSHA256,
		ExecutableFormatEvidenceSHA256: *wire.ExecutableFormatEvidenceSHA256,
		ProvenanceType:                 *wire.ProvenanceType,
		ProvenanceStatementSHA256:      *wire.ProvenanceStatementSHA256,
		SignatureScheme:                *wire.SignatureScheme, SignerIdentitySHA256: *wire.SignerIdentitySHA256,
		SignatureEnvelopeSHA256: *wire.SignatureEnvelopeSHA256, DigestOnly: *wire.DigestOnly,
		SignatureBytesIncluded: *wire.SignatureBytesIncluded, NetworkLoaded: *wire.NetworkLoaded,
		PathIncluded: *wire.PathIncluded,
	}
}

type analyzerReleaseAllowlistEntryWire struct {
	ManifestSHA256            *string `json:"manifest_sha256"`
	Analyzer                  *string `json:"analyzer"`
	ReleaseVersion            *string `json:"release_version"`
	TargetGOOS                *string `json:"target_goos"`
	TargetGOARCH              *string `json:"target_goarch"`
	ExecutableSHA256          *string `json:"executable_sha256"`
	ProvenanceStatementSHA256 *string `json:"provenance_statement_sha256"`
	SignatureScheme           *string `json:"signature_scheme"`
	SignerIdentitySHA256      *string `json:"signer_identity_sha256"`
	SignatureEnvelopeSHA256   *string `json:"signature_envelope_sha256"`
}

func (wire analyzerReleaseAllowlistEntryWire) complete() bool {
	return wire.ManifestSHA256 != nil && wire.Analyzer != nil && wire.ReleaseVersion != nil &&
		wire.TargetGOOS != nil && wire.TargetGOARCH != nil && wire.ExecutableSHA256 != nil &&
		wire.ProvenanceStatementSHA256 != nil && wire.SignatureScheme != nil &&
		wire.SignerIdentitySHA256 != nil && wire.SignatureEnvelopeSHA256 != nil
}

func (wire analyzerReleaseAllowlistEntryWire) value() AnalyzerReleaseAllowlistEntry {
	return AnalyzerReleaseAllowlistEntry{
		ManifestSHA256: *wire.ManifestSHA256, Analyzer: *wire.Analyzer,
		ReleaseVersion: *wire.ReleaseVersion, TargetGOOS: *wire.TargetGOOS,
		TargetGOARCH: *wire.TargetGOARCH, ExecutableSHA256: *wire.ExecutableSHA256,
		ProvenanceStatementSHA256: *wire.ProvenanceStatementSHA256,
		SignatureScheme:           *wire.SignatureScheme, SignerIdentitySHA256: *wire.SignerIdentitySHA256,
		SignatureEnvelopeSHA256: *wire.SignatureEnvelopeSHA256,
	}
}

type analyzerReleaseAllowlistWire struct {
	ProtocolVersion   *string                              `json:"protocol_version"`
	Entries           *[]analyzerReleaseAllowlistEntryWire `json:"entries"`
	OperatorManaged   *bool                                `json:"operator_managed"`
	NetworkLoaded     *bool                                `json:"network_loaded"`
	EnvironmentLoaded *bool                                `json:"environment_loaded"`
}

func (wire analyzerReleaseAllowlistWire) complete() bool {
	if wire.ProtocolVersion == nil || wire.Entries == nil || wire.OperatorManaged == nil ||
		wire.NetworkLoaded == nil || wire.EnvironmentLoaded == nil {
		return false
	}
	for _, entry := range *wire.Entries {
		if !entry.complete() {
			return false
		}
	}
	return true
}

func (wire analyzerReleaseAllowlistWire) value() AnalyzerReleaseAllowlist {
	entries := make([]AnalyzerReleaseAllowlistEntry, len(*wire.Entries))
	for index, entry := range *wire.Entries {
		entries[index] = entry.value()
	}
	return AnalyzerReleaseAllowlist{ProtocolVersion: *wire.ProtocolVersion, Entries: entries,
		OperatorManaged: *wire.OperatorManaged, NetworkLoaded: *wire.NetworkLoaded,
		EnvironmentLoaded: *wire.EnvironmentLoaded}
}

type analyzerReleaseCandidateWire struct {
	ProtocolVersion                *string `json:"protocol_version"`
	ExecutableFormatEvidenceSHA256 *string `json:"executable_format_evidence_sha256"`
	ManifestSHA256                 *string `json:"manifest_sha256"`
	AllowlistSHA256                *string `json:"allowlist_sha256"`
	Analyzer                       *string `json:"analyzer"`
	ReleaseVersion                 *string `json:"release_version"`
	TargetGOOS                     *string `json:"target_goos"`
	TargetGOARCH                   *string `json:"target_goarch"`
	ExecutableSHA256               *string `json:"executable_sha256"`
	AllowlistEntryCount            *int    `json:"allowlist_entry_count"`
	ManifestDigestPinned           *bool   `json:"manifest_digest_pinned"`
	ExecutableDigestPinned         *bool   `json:"executable_digest_pinned"`
	FormatEvidenceDigestPinned     *bool   `json:"format_evidence_digest_pinned"`
	ProvenanceDigestPinned         *bool   `json:"provenance_digest_pinned"`
	SignatureEnvelopeDigestPinned  *bool   `json:"signature_envelope_digest_pinned"`
	OperatorAllowlistMatched       *bool   `json:"operator_allowlist_matched"`
	ProvenanceStatementVerified    *bool   `json:"provenance_statement_verified"`
	CryptographicSignatureVerified *bool   `json:"cryptographic_signature_verified"`
	PlatformSignatureVerified      *bool   `json:"platform_signature_verified"`
	ImmutableHandleVerified        *bool   `json:"immutable_handle_verified"`
	OperatorReviewRequired         *bool   `json:"operator_review_required"`
	ReleaseApproved                *bool   `json:"release_approved"`
	ProcessStartEnabled            *bool   `json:"process_start_enabled"`
	ProductInvocationEnabled       *bool   `json:"product_invocation_enabled"`
	NetworkAuthorized              *bool   `json:"network_authorized"`
	HostFilesystemAuthorized       *bool   `json:"host_filesystem_authorized"`
}

func (wire analyzerReleaseCandidateWire) complete() bool {
	return wire.ProtocolVersion != nil && wire.ExecutableFormatEvidenceSHA256 != nil &&
		wire.ManifestSHA256 != nil && wire.AllowlistSHA256 != nil && wire.Analyzer != nil &&
		wire.ReleaseVersion != nil && wire.TargetGOOS != nil && wire.TargetGOARCH != nil &&
		wire.ExecutableSHA256 != nil && wire.AllowlistEntryCount != nil &&
		wire.ManifestDigestPinned != nil && wire.ExecutableDigestPinned != nil &&
		wire.FormatEvidenceDigestPinned != nil && wire.ProvenanceDigestPinned != nil &&
		wire.SignatureEnvelopeDigestPinned != nil && wire.OperatorAllowlistMatched != nil &&
		wire.ProvenanceStatementVerified != nil && wire.CryptographicSignatureVerified != nil &&
		wire.PlatformSignatureVerified != nil && wire.ImmutableHandleVerified != nil &&
		wire.OperatorReviewRequired != nil && wire.ReleaseApproved != nil &&
		wire.ProcessStartEnabled != nil && wire.ProductInvocationEnabled != nil &&
		wire.NetworkAuthorized != nil && wire.HostFilesystemAuthorized != nil
}

func (wire analyzerReleaseCandidateWire) value() AnalyzerReleaseCandidate {
	return AnalyzerReleaseCandidate{
		ProtocolVersion:                *wire.ProtocolVersion,
		ExecutableFormatEvidenceSHA256: *wire.ExecutableFormatEvidenceSHA256,
		ManifestSHA256:                 *wire.ManifestSHA256, AllowlistSHA256: *wire.AllowlistSHA256,
		Analyzer: *wire.Analyzer, ReleaseVersion: *wire.ReleaseVersion,
		TargetGOOS: *wire.TargetGOOS, TargetGOARCH: *wire.TargetGOARCH,
		ExecutableSHA256:               *wire.ExecutableSHA256,
		AllowlistEntryCount:            *wire.AllowlistEntryCount,
		ManifestDigestPinned:           *wire.ManifestDigestPinned,
		ExecutableDigestPinned:         *wire.ExecutableDigestPinned,
		FormatEvidenceDigestPinned:     *wire.FormatEvidenceDigestPinned,
		ProvenanceDigestPinned:         *wire.ProvenanceDigestPinned,
		SignatureEnvelopeDigestPinned:  *wire.SignatureEnvelopeDigestPinned,
		OperatorAllowlistMatched:       *wire.OperatorAllowlistMatched,
		ProvenanceStatementVerified:    *wire.ProvenanceStatementVerified,
		CryptographicSignatureVerified: *wire.CryptographicSignatureVerified,
		PlatformSignatureVerified:      *wire.PlatformSignatureVerified,
		ImmutableHandleVerified:        *wire.ImmutableHandleVerified,
		OperatorReviewRequired:         *wire.OperatorReviewRequired, ReleaseApproved: *wire.ReleaseApproved,
		ProcessStartEnabled:      *wire.ProcessStartEnabled,
		ProductInvocationEnabled: *wire.ProductInvocationEnabled,
		NetworkAuthorized:        *wire.NetworkAuthorized,
		HostFilesystemAuthorized: *wire.HostFilesystemAuthorized,
	}
}
