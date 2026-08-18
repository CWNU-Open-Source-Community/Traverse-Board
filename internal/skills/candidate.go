package skills

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
)

const (
	SkillCandidateProtocolVersion       = "skill_candidate.v1"
	SkillCandidateReviewProtocolVersion = "skill_candidate_review.v1"
	SkillCandidateImportProtocolVersion = "skill_candidate_import.v1"
	MaxSkillCandidates                  = 64
	MaxSkillCandidatesPerRun            = 4
	MaxSkillCandidateReviewReasonRunes  = 2048
)

type SkillCandidateStatus string

const (
	SkillCandidateProposed SkillCandidateStatus = "proposed"
	SkillCandidateApproved SkillCandidateStatus = "approved"
	SkillCandidateRejected SkillCandidateStatus = "rejected"
	SkillCandidateImported SkillCandidateStatus = "imported"
)

type SkillCandidateReviewDecision string

const (
	SkillCandidateReviewApprove SkillCandidateReviewDecision = "approve"
	SkillCandidateReviewReject  SkillCandidateReviewDecision = "reject"
)

func (d SkillCandidateReviewDecision) Valid() bool {
	return d == SkillCandidateReviewApprove || d == SkillCandidateReviewReject
}

// SkillCandidate is immutable untrusted data proposed by the root model. Its
// presence never selects, activates, or installs the contained instructions.
type SkillCandidate struct {
	ID                   string
	ProtocolVersion      string
	OperationKeyDigest   string
	RequestFingerprint   string
	InvocationID         string
	RunID                string
	RootAgentID          string
	SessionID            string
	WorkspaceID          string
	Surface              domain.ExecutionSurface
	Manifest             Manifest
	Content              string
	ArchiveSHA256        string
	PackageFingerprint   string
	ArchiveBytes         int
	CandidateFingerprint string
	RequestedBy          string
	CreatedAt            time.Time
}

type SkillCandidateReview struct {
	ID                   string
	ProtocolVersion      string
	OperationKeyDigest   string
	RequestFingerprint   string
	CandidateID          string
	CandidateFingerprint string
	Decision             SkillCandidateReviewDecision
	Reason               string
	Reviewer             string
	ReviewFingerprint    string
	CreatedAt            time.Time
}

type SkillCandidateImport struct {
	ID                      string
	ProtocolVersion         string
	OperationKeyDigest      string
	RequestFingerprint      string
	CandidateID             string
	CandidateFingerprint    string
	ReviewFingerprint       string
	InstallationID          string
	InstallationFingerprint string
	ImportedBy              string
	ImportFingerprint       string
	CreatedAt               time.Time
}

type SkillCandidateRecord struct {
	Candidate SkillCandidate
	Review    *SkillCandidateReview
	Import    *SkillCandidateImport
}

func (c SkillCandidate) Validate() error {
	if !validPackageIdentity(c.ID) || c.ProtocolVersion != SkillCandidateProtocolVersion ||
		!validPackageIdentity(c.InvocationID) ||
		!validPackageIdentity(c.RunID) || !domain.ValidAgentID(c.RootAgentID) ||
		!validPackageIdentity(c.SessionID) || !validPackageIdentity(c.WorkspaceID) ||
		c.Surface != domain.ExecutionSurfaceCode || !validPackageActor(c.RequestedBy) ||
		!validUTC(c.CreatedAt) {
		return errors.New("Skill candidate scope is invalid")
	}
	if c.Manifest.Publisher != "" || !c.Manifest.HasModeMetadata() ||
		!slices.Equal(c.Manifest.Surfaces, []domain.ExecutionSurface{domain.ExecutionSurfaceCode}) {
		return errors.New("Skill candidate must be an unsigned mode-aware Code Skill")
	}
	content := []byte(c.Content)
	if err := c.Manifest.Validate(content); err != nil {
		return fmt.Errorf("Skill candidate manifest is invalid: %w", err)
	}
	raw, err := BuildUnsignedPackage(c.Manifest, content)
	if err != nil {
		return fmt.Errorf("Skill candidate package is invalid: %w", err)
	}
	parsed, err := ParsePackage(raw)
	if err != nil {
		return err
	}
	preview := parsed.Preview()
	if c.ArchiveSHA256 != preview.ArchiveSHA256 ||
		c.PackageFingerprint != preview.PackageFingerprint || c.ArchiveBytes != len(raw) {
		return errors.New("Skill candidate package descriptor is invalid")
	}
	if !validSHA256(c.OperationKeyDigest) || !validSHA256(c.RequestFingerprint) ||
		c.RequestFingerprint != SkillCandidateRequestFingerprint(c) ||
		!validSHA256(c.CandidateFingerprint) ||
		c.CandidateFingerprint != SkillCandidateFingerprint(c) {
		return errors.New("Skill candidate fingerprint is invalid")
	}
	return nil
}

func SkillCandidateRequestFingerprint(c SkillCandidate) string {
	return runmutation.Fingerprint("skill_candidate_request.v1", c.InvocationID,
		c.RunID, c.RootAgentID,
		c.SessionID, c.WorkspaceID, string(c.Surface), c.PackageFingerprint,
		c.ArchiveSHA256, strconv.Itoa(c.ArchiveBytes), c.RequestedBy)
}

func SkillCandidateFingerprint(c SkillCandidate) string {
	return runmutation.Fingerprint(SkillCandidateProtocolVersion, c.ID,
		c.OperationKeyDigest, c.RequestFingerprint, c.CreatedAt.UTC().Format(time.RFC3339Nano))
}

func (r SkillCandidateReview) Validate() error {
	if !validPackageIdentity(r.ID) || r.ProtocolVersion != SkillCandidateReviewProtocolVersion ||
		!validSHA256(r.OperationKeyDigest) || !validSHA256(r.RequestFingerprint) ||
		!validPackageIdentity(r.CandidateID) || !validSHA256(r.CandidateFingerprint) ||
		!r.Decision.Valid() || !validCandidateReviewReason(r.Reason, r.Decision) ||
		!validHumanSkillCandidateActor(r.Reviewer) || !validUTC(r.CreatedAt) ||
		r.RequestFingerprint != SkillCandidateReviewRequestFingerprint(r) ||
		!validSHA256(r.ReviewFingerprint) ||
		r.ReviewFingerprint != SkillCandidateReviewFingerprint(r) {
		return errors.New("Skill candidate review is invalid")
	}
	return nil
}

func SkillCandidateReviewRequestFingerprint(r SkillCandidateReview) string {
	return runmutation.Fingerprint("skill_candidate_review_request.v1", r.CandidateID,
		r.CandidateFingerprint, string(r.Decision), r.Reason, r.Reviewer)
}

func SkillCandidateReviewFingerprint(r SkillCandidateReview) string {
	return runmutation.Fingerprint(SkillCandidateReviewProtocolVersion, r.ID,
		r.OperationKeyDigest, r.RequestFingerprint, r.CreatedAt.UTC().Format(time.RFC3339Nano))
}

func (i SkillCandidateImport) Validate() error {
	if !validPackageIdentity(i.ID) || i.ProtocolVersion != SkillCandidateImportProtocolVersion ||
		!validSHA256(i.OperationKeyDigest) || !validSHA256(i.RequestFingerprint) ||
		!validPackageIdentity(i.CandidateID) || !validSHA256(i.CandidateFingerprint) ||
		!validSHA256(i.ReviewFingerprint) || !validPackageIdentity(i.InstallationID) ||
		!validSHA256(i.InstallationFingerprint) ||
		!validHumanSkillCandidateActor(i.ImportedBy) ||
		!validUTC(i.CreatedAt) || i.RequestFingerprint != SkillCandidateImportRequestFingerprint(i) ||
		!validSHA256(i.ImportFingerprint) ||
		i.ImportFingerprint != SkillCandidateImportFingerprint(i) {
		return errors.New("Skill candidate import is invalid")
	}
	return nil
}

func SkillCandidateImportRequestFingerprint(i SkillCandidateImport) string {
	return runmutation.Fingerprint("skill_candidate_import_request.v1", i.CandidateID,
		i.CandidateFingerprint, i.ReviewFingerprint, i.ImportedBy)
}

func SkillCandidateImportFingerprint(i SkillCandidateImport) string {
	return runmutation.Fingerprint(SkillCandidateImportProtocolVersion, i.ID,
		i.OperationKeyDigest, i.RequestFingerprint, i.InstallationID,
		i.InstallationFingerprint, i.CreatedAt.UTC().Format(time.RFC3339Nano))
}

func (r SkillCandidateRecord) Status() SkillCandidateStatus {
	if r.Import != nil {
		return SkillCandidateImported
	}
	if r.Review == nil {
		return SkillCandidateProposed
	}
	if r.Review.Decision == SkillCandidateReviewApprove {
		return SkillCandidateApproved
	}
	return SkillCandidateRejected
}

func (r SkillCandidateRecord) Validate() error {
	if err := r.Candidate.Validate(); err != nil {
		return err
	}
	if r.Review != nil {
		if err := r.Review.Validate(); err != nil {
			return err
		}
		if r.Review.CandidateID != r.Candidate.ID ||
			r.Review.CandidateFingerprint != r.Candidate.CandidateFingerprint ||
			r.Review.CreatedAt.Before(r.Candidate.CreatedAt) {
			return errors.New("Skill candidate review binding is invalid")
		}
	}
	if r.Import != nil {
		if err := r.Import.Validate(); err != nil {
			return err
		}
		if r.Review == nil || r.Review.Decision != SkillCandidateReviewApprove ||
			r.Import.CandidateID != r.Candidate.ID ||
			r.Import.CandidateFingerprint != r.Candidate.CandidateFingerprint ||
			r.Import.ReviewFingerprint != r.Review.ReviewFingerprint ||
			r.Import.CreatedAt.Before(r.Review.CreatedAt) {
			return errors.New("Skill candidate import binding is invalid")
		}
	}
	return nil
}

func CloneSkillCandidate(value SkillCandidate) SkillCandidate {
	value.Manifest = cloneManifest(value.Manifest)
	return value
}

func CloneSkillCandidateRecord(value SkillCandidateRecord) SkillCandidateRecord {
	value.Candidate = CloneSkillCandidate(value.Candidate)
	if value.Review != nil {
		copy := *value.Review
		value.Review = &copy
	}
	if value.Import != nil {
		copy := *value.Import
		value.Import = &copy
	}
	return value
}

func validCandidateReviewReason(value string, decision SkillCandidateReviewDecision) bool {
	if strings.TrimSpace(value) != value || !utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > MaxSkillCandidateReviewReasonRunes {
		return false
	}
	if decision == SkillCandidateReviewReject && value == "" {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func validHumanSkillCandidateActor(value string) bool {
	if !validPackageActor(value) {
		return false
	}
	switch strings.ToLower(value) {
	case "agent", "llm", "model", "repository", "repo", "skill", "supervisor",
		"run_supervisor":
		return false
	default:
		return true
	}
}
