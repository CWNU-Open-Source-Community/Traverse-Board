package browserruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const (
	BrowserAcceptanceProtocolVersion = "browser_executable_acceptance.v1"
	BrowserPublisherPolicyVersion    = "browser_publisher_policy.v1"

	AuthenticodeSourceUnavailable = "unavailable"
	AuthenticodeSourceWindows     = "windows_authenticode_cached_only"
)

type BrowserAcceptanceDecision string

const (
	BrowserAcceptanceAccepted BrowserAcceptanceDecision = "accepted_for_review"
	BrowserAcceptanceRefused  BrowserAcceptanceDecision = "refused"
)

type BrowserAcceptanceReason string

const (
	BrowserAcceptanceReasonPublisherVerified     BrowserAcceptanceReason = "publisher_verified"
	BrowserAcceptanceReasonSignatureUnverified   BrowserAcceptanceReason = "signature_unverified"
	BrowserAcceptanceReasonPublisherMismatch     BrowserAcceptanceReason = "publisher_mismatch"
	BrowserAcceptanceReasonPublisherUnsupported  BrowserAcceptanceReason = "publisher_policy_unsupported"
	BrowserAcceptanceReasonProvenanceUnavailable BrowserAcceptanceReason = "provenance_unavailable"
)

// AuthenticodeEvidence contains only bounded certificate metadata. It never
// retains certificate bytes, a trust handle, or a process-start capability.
type AuthenticodeEvidence struct {
	Source                    string `json:"source"`
	Publisher                 string `json:"publisher,omitempty"`
	CertificateSHA256         string `json:"certificate_sha256,omitempty"`
	SignatureVerified         bool   `json:"signature_verified"`
	SameOpenHandleVerified    bool   `json:"same_open_handle_verified"`
	CacheOnlyVerification     bool   `json:"cache_only_verification"`
	RevocationChecked         bool   `json:"revocation_checked"`
	TimestampVerified         bool   `json:"timestamp_verified"`
	RawCertificateIncluded    bool   `json:"raw_certificate_included"`
	NetworkUsed               bool   `json:"network_used"`
	PublisherPolicyMatched    bool   `json:"publisher_policy_matched"`
	PublisherPolicyVersion    string `json:"publisher_policy_version"`
	PublisherEvidenceComplete bool   `json:"publisher_evidence_complete"`
}

// BrowserAcceptanceCandidate is the immutable result of reopening the exact
// discovered executable and checking bytes, PE identity, Authenticode, and the
// fixed product publisher policy through one read-only handle. Acceptance only
// makes the file eligible for a separate operator review.
type BrowserAcceptanceCandidate struct {
	ProtocolVersion               string                    `json:"protocol_version"`
	ExecutableIdentityFingerprint string                    `json:"executable_identity_fingerprint"`
	Product                       BrowserProduct            `json:"product"`
	Channel                       BrowserChannel            `json:"channel"`
	RootID                        DiscoveryRootID           `json:"root_id"`
	ExecutableSHA256              string                    `json:"executable_sha256"`
	ExecutableBytes               int64                     `json:"executable_bytes"`
	TargetGOARCH                  string                    `json:"target_goarch"`
	Decision                      BrowserAcceptanceDecision `json:"decision"`
	ReasonCode                    BrowserAcceptanceReason   `json:"reason_code"`
	Evidence                      AuthenticodeEvidence      `json:"evidence"`
	SameHandleBytesRevalidated    bool                      `json:"same_handle_bytes_revalidated"`
	SameFilePathRevalidated       bool                      `json:"same_file_path_revalidated"`
	PERevalidated                 bool                      `json:"pe_revalidated"`
	ReviewEligible                bool                      `json:"review_eligible"`
	StartBlocked                  bool                      `json:"start_blocked"`
	LaunchTrustComplete           bool                      `json:"launch_trust_complete"`
	MetadataOnly                  bool                      `json:"metadata_only"`
	ProcessStartEnabled           bool                      `json:"process_start_enabled"`
	ProductLaunchEnabled          bool                      `json:"product_launch_enabled"`
	Authority                     RuntimeAuthority          `json:"authority"`
	Fingerprint                   string                    `json:"fingerprint"`
}

type authenticodeProbe func(*os.File, string) (AuthenticodeEvidence, error)

func BuildBrowserAcceptanceCandidate(
	identity BrowserExecutableIdentity,
) (BrowserAcceptanceCandidate, error) {
	return buildBrowserAcceptanceCandidate(identity, browserAuthenticodeEvidence)
}

func buildBrowserAcceptanceCandidate(identity BrowserExecutableIdentity,
	probe authenticodeProbe,
) (BrowserAcceptanceCandidate, error) {
	if err := ValidateBrowserExecutableIdentity(identity); err != nil {
		return BrowserAcceptanceCandidate{}, err
	}
	if err := validateAcceptancePath(identity); err != nil {
		return BrowserAcceptanceCandidate{}, err
	}
	if probe == nil {
		return BrowserAcceptanceCandidate{}, errors.New("browser Authenticode probe is required")
	}
	pathInfo, err := os.Lstat(identity.CanonicalPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return BrowserAcceptanceCandidate{}, errors.New("browser acceptance path is not a non-link regular file")
	}
	file, err := os.Open(identity.CanonicalPath)
	if err != nil {
		return BrowserAcceptanceCandidate{}, errors.New("browser acceptance candidate cannot be opened read-only")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || !os.SameFile(pathInfo, before) ||
		before.Size() != identity.ExecutableBytes {
		return BrowserAcceptanceCandidate{}, errors.New("browser acceptance handle does not match the discovered file")
	}
	firstDigest, targetArch, err := revalidateOpenBrowserHandle(file, before.Size())
	if err != nil {
		return BrowserAcceptanceCandidate{}, err
	}
	if firstDigest != identity.ExecutableSHA256 || targetArch != identity.TargetGOARCH {
		return BrowserAcceptanceCandidate{}, errors.New("browser acceptance bytes or PE architecture drifted")
	}
	evidence, err := probe(file, identity.CanonicalPath)
	if err != nil {
		return BrowserAcceptanceCandidate{}, fmt.Errorf("inspect browser Authenticode evidence: %w", err)
	}
	secondDigest, secondArch, err := revalidateOpenBrowserHandle(file, before.Size())
	if err != nil || secondDigest != firstDigest || secondArch != targetArch {
		return BrowserAcceptanceCandidate{}, errors.New("browser acceptance bytes changed during publisher verification")
	}
	after, err := file.Stat()
	pathAfter, pathErr := os.Lstat(identity.CanonicalPath)
	if err != nil || pathErr != nil || !after.Mode().IsRegular() ||
		pathAfter.Mode()&os.ModeSymlink != 0 || !pathAfter.Mode().IsRegular() ||
		!os.SameFile(before, after) || !os.SameFile(before, pathAfter) ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return BrowserAcceptanceCandidate{}, errors.New("browser acceptance path or handle changed during verification")
	}

	decision, reason := classifyBrowserPublisherEvidence(identity, &evidence)
	candidate := BrowserAcceptanceCandidate{
		ProtocolVersion:               BrowserAcceptanceProtocolVersion,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		Product:                       identity.Product, Channel: identity.Channel, RootID: identity.RootID,
		ExecutableSHA256: identity.ExecutableSHA256, ExecutableBytes: identity.ExecutableBytes,
		TargetGOARCH: identity.TargetGOARCH, Decision: decision, ReasonCode: reason,
		Evidence: evidence, SameHandleBytesRevalidated: true, SameFilePathRevalidated: true,
		PERevalidated: true, ReviewEligible: decision == BrowserAcceptanceAccepted,
		StartBlocked: true, MetadataOnly: true,
	}
	candidate.Fingerprint, err = browserAcceptanceFingerprint(candidate)
	if err != nil {
		return BrowserAcceptanceCandidate{}, err
	}
	if err := ValidateBrowserAcceptanceCandidate(candidate, identity); err != nil {
		return BrowserAcceptanceCandidate{}, err
	}
	return candidate, nil
}

func ValidateBrowserAcceptanceCandidate(candidate BrowserAcceptanceCandidate,
	identity BrowserExecutableIdentity,
) error {
	if err := ValidateBrowserExecutableIdentity(identity); err != nil {
		return err
	}
	if candidate.ProtocolVersion != BrowserAcceptanceProtocolVersion ||
		candidate.ExecutableIdentityFingerprint != identity.Fingerprint ||
		candidate.Product != identity.Product || candidate.Channel != identity.Channel ||
		candidate.RootID != identity.RootID ||
		candidate.ExecutableSHA256 != identity.ExecutableSHA256 ||
		candidate.ExecutableBytes != identity.ExecutableBytes ||
		candidate.TargetGOARCH != identity.TargetGOARCH ||
		!candidate.SameHandleBytesRevalidated || !candidate.SameFilePathRevalidated ||
		!candidate.PERevalidated || !candidate.StartBlocked || candidate.LaunchTrustComplete ||
		!candidate.MetadataOnly || candidate.ProcessStartEnabled || candidate.ProductLaunchEnabled ||
		candidate.Authority != (RuntimeAuthority{}) {
		return errors.New("browser acceptance candidate lost an immutable non-starting boundary")
	}
	expectedEvidence := candidate.Evidence
	decision, reason := classifyBrowserPublisherEvidence(identity, &expectedEvidence)
	if !reflect.DeepEqual(expectedEvidence, candidate.Evidence) ||
		candidate.Decision != decision || candidate.ReasonCode != reason ||
		candidate.ReviewEligible != (decision == BrowserAcceptanceAccepted) {
		return errors.New("browser acceptance publisher decision is inconsistent")
	}
	expected, err := browserAcceptanceFingerprint(candidate)
	if err != nil || candidate.Fingerprint != expected {
		return errors.New("browser acceptance candidate fingerprint mismatch")
	}
	return nil
}

func classifyBrowserPublisherEvidence(identity BrowserExecutableIdentity,
	evidence *AuthenticodeEvidence,
) (BrowserAcceptanceDecision, BrowserAcceptanceReason) {
	evidence.Publisher = strings.TrimSpace(evidence.Publisher)
	evidence.PublisherPolicyVersion = BrowserPublisherPolicyVersion
	evidence.PublisherPolicyMatched = browserPublisherAllowed(identity.Product, evidence.Publisher)
	evidence.PublisherEvidenceComplete = evidence.SignatureVerified &&
		evidence.SameOpenHandleVerified && evidence.CacheOnlyVerification &&
		evidence.Source == AuthenticodeSourceWindows && evidence.Publisher != "" &&
		validSHA256(evidence.CertificateSHA256)
	if evidence.RawCertificateIncluded || evidence.NetworkUsed || evidence.RevocationChecked ||
		evidence.TimestampVerified {
		evidence.PublisherEvidenceComplete = false
	}
	if identity.Product == BrowserProductChromium {
		return BrowserAcceptanceRefused, BrowserAcceptanceReasonPublisherUnsupported
	}
	if evidence.Source == AuthenticodeSourceUnavailable {
		return BrowserAcceptanceRefused, BrowserAcceptanceReasonProvenanceUnavailable
	}
	if !evidence.SignatureVerified || !evidence.SameOpenHandleVerified ||
		!evidence.PublisherEvidenceComplete {
		return BrowserAcceptanceRefused, BrowserAcceptanceReasonSignatureUnverified
	}
	if !evidence.PublisherPolicyMatched {
		return BrowserAcceptanceRefused, BrowserAcceptanceReasonPublisherMismatch
	}
	return BrowserAcceptanceAccepted, BrowserAcceptanceReasonPublisherVerified
}

func browserPublisherAllowed(product BrowserProduct, publisher string) bool {
	switch product {
	case BrowserProductChrome:
		return publisher == "Google LLC" || publisher == "Google Inc"
	case BrowserProductEdge:
		return publisher == "Microsoft Corporation"
	default:
		return false
	}
}

func revalidateOpenBrowserHandle(file *os.File, size int64) (string, string, error) {
	if file == nil || size < MinBrowserExecutableBytes || size > MaxBrowserExecutableBytes {
		return "", "", errors.New("browser acceptance handle or size is invalid")
	}
	arch, err := inspectPEArchitecture(file, size)
	if err != nil {
		return "", "", err
	}
	hasher := sha256.New()
	read, err := io.Copy(hasher, io.NewSectionReader(file, 0, size))
	if err != nil || read != size {
		return "", "", errors.New("browser acceptance handle could not be hashed exactly")
	}
	return hex.EncodeToString(hasher.Sum(nil)), arch, nil
}

func browserAcceptanceFingerprint(value BrowserAcceptanceCandidate) (string, error) {
	copyValue := value
	copyValue.Fingerprint = ""
	return fingerprintJSON(copyValue, "browser executable acceptance")
}

func validateAcceptancePath(identity BrowserExecutableIdentity) error {
	root, ok := executableIdentityRoot(identity)
	if !ok || !filepath.IsAbs(identity.CanonicalPath) ||
		!pathWithinRoot(root, identity.CanonicalPath) {
		return errors.New("browser acceptance path escaped its discovery root")
	}
	return nil
}
