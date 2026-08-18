package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolgateway"
)

const runSkillGeneratorName = "run-skill-generator"

type SkillCandidateMutationStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetSkillSelectionByRun(context.Context, string) (skills.Selection, bool, error)
	CreateSkillCandidate(context.Context, skills.SkillCandidate) (
		skills.SkillCandidate, bool, error)
}

type SkillCandidateReviewStore interface {
	GetSkillCandidate(context.Context, string) (skills.SkillCandidateRecord, error)
	ListSkillCandidates(context.Context, string) ([]skills.SkillCandidateRecord, error)
	CreateSkillCandidateReview(context.Context, skills.SkillCandidateReview) (
		skills.SkillCandidateRecord, bool, error)
	CreateSkillCandidateImport(context.Context, skills.SkillCandidateImport) (
		skills.SkillCandidateRecord, bool, error)
}

type SkillCandidateToolExecutor struct {
	store SkillCandidateMutationStore
}

func NewSkillCandidateToolExecutor(store SkillCandidateMutationStore) *SkillCandidateToolExecutor {
	return &SkillCandidateToolExecutor{store: store}
}

func (e *SkillCandidateToolExecutor) ProposeSkillCandidate(ctx context.Context,
	scope toolgateway.SkillCandidateContext, spec toolgateway.SkillCandidateSpec,
) (toolgateway.SkillCandidateResult, error) {
	if e == nil || e.store == nil {
		return toolgateway.SkillCandidateResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Skill candidate mutation store is required")
	}
	if err := scope.Validate(); err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Skill candidate scope is invalid", err)
	}
	run, err := e.store.GetRun(ctx, scope.RunID)
	if err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Normalize(err)
	}
	if run.SessionID != scope.SessionID || run.Status != domain.RunRunning {
		return toolgateway.SkillCandidateResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Skill candidates require the active Run Session")
	}
	mode, err := e.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Normalize(err)
	}
	if mode.Surface != domain.ExecutionSurfaceCode || mode.Phase != domain.ExecutionPhaseDeliver {
		return toolgateway.SkillCandidateResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Skill candidates are available only in Code Deliver mode")
	}
	selection, found, err := e.store.GetSkillSelectionByRun(ctx, run.ID)
	if err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Normalize(err)
	}
	if !found || !slices.ContainsFunc(selection.Items, func(item skills.SelectionItem) bool {
		return item.Name == runSkillGeneratorName
	}) {
		return toolgateway.SkillCandidateResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"run-skill-generator must be explicitly selected before proposing a candidate")
	}
	registry, err := skills.BuiltinRegistry()
	if err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Normalize(err)
	}
	if _, reserved := registry.Get(spec.Name); reserved {
		return toolgateway.SkillCandidateResult{}, apperror.New(apperror.CodeConflict,
			"Skill candidate cannot replace a built-in Skill")
	}
	manifest := skills.BindManifestContent(skills.Manifest{
		Protocol: skills.ProtocolVersion, Name: spec.Name, Version: spec.SkillVersion,
		Description: spec.Description, Profiles: slices.Clone(spec.Profiles),
		Surfaces: slices.Clone(spec.Surfaces), Phases: slices.Clone(spec.Phases),
		Roles: slices.Clone(spec.Roles), UserInvocable: spec.UserInvocable,
		ModelInvocable: spec.ModelInvocable, ExplicitOnly: spec.ExplicitOnly,
		ToolDependencies: slices.Clone(spec.ToolDependencies),
	}, []byte(spec.Content))
	raw, err := skills.BuildUnsignedPackage(manifest, []byte(spec.Content))
	if err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "generated Skill candidate failed validation", err)
	}
	parsed, err := skills.ParsePackage(raw)
	if err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Wrap(
			apperror.CodeInternal, "generated Skill candidate package is invalid", err)
	}
	preview := parsed.Preview()
	now := time.Now().UTC()
	candidate := skills.SkillCandidate{
		ID: idgen.New("skill-candidate"), ProtocolVersion: skills.SkillCandidateProtocolVersion,
		OperationKeyDigest: runmutation.OperationKeyDigest(
			string(toolgateway.SkillCandidateProposeTool), scope.RunID, scope.OperationKey),
		InvocationID: scope.InvocationID,
		RunID:        scope.RunID, RootAgentID: scope.RootAgentID, SessionID: scope.SessionID,
		WorkspaceID: scope.WorkspaceID, Surface: domain.ExecutionSurfaceCode,
		Manifest: preview.Manifest, Content: spec.Content,
		ArchiveSHA256: preview.ArchiveSHA256, PackageFingerprint: preview.PackageFingerprint,
		ArchiveBytes: preview.ArchiveBytes, RequestedBy: scope.RequestedBy, CreatedAt: now,
	}
	candidate.RequestFingerprint = skills.SkillCandidateRequestFingerprint(candidate)
	candidate.CandidateFingerprint = skills.SkillCandidateFingerprint(candidate)
	stored, replayed, err := e.store.CreateSkillCandidate(ctx, candidate)
	if err != nil {
		return toolgateway.SkillCandidateResult{}, apperror.Normalize(err)
	}
	return toolgateway.SkillCandidateResult{
		CandidateID: stored.ID, CandidateFingerprint: stored.CandidateFingerprint,
		Name: stored.Manifest.Name, Version: stored.Manifest.Version, Status: "proposed",
		PackageFingerprint: stored.PackageFingerprint,
		ContentSHA256:      stored.Manifest.ContentSHA256,
		ContentBytes:       stored.Manifest.ContentBytes, Replayed: replayed,
	}, nil
}

type SkillCandidateService struct {
	store    SkillCandidateReviewStore
	registry *SkillPackageRegistryService
}

func NewSkillCandidateService(store SkillCandidateReviewStore,
	registry *SkillPackageRegistryService,
) *SkillCandidateService {
	return &SkillCandidateService{store: store, registry: registry}
}

type ReviewSkillCandidateRequest struct {
	CandidateID          string
	CandidateFingerprint string
	Decision             skills.SkillCandidateReviewDecision
	Reason               string
	OperationKey         string
	Reviewer             string
}

type ReviewSkillCandidateResult struct {
	Record   skills.SkillCandidateRecord
	Replayed bool
}

type ImportSkillCandidateRequest struct {
	CandidateID          string
	CandidateFingerprint string
	OperationKey         string
	ImportedBy           string
	ConfirmUntrusted     bool
}

type ImportSkillCandidateResult struct {
	Record           skills.SkillCandidateRecord
	InstalledPackage skills.InstalledPackage
	Replayed         bool
	RecoveredPending bool
}

func (s *SkillCandidateService) Get(ctx context.Context,
	id string,
) (skills.SkillCandidateRecord, error) {
	if s == nil || s.store == nil {
		return skills.SkillCandidateRecord{}, apperror.New(
			apperror.CodeFailedPrecondition, "Skill candidate store is required")
	}
	value, err := s.store.GetSkillCandidate(ctx, strings.TrimSpace(id))
	return value, apperror.Normalize(err)
}

func (s *SkillCandidateService) List(ctx context.Context,
	runID string,
) ([]skills.SkillCandidateRecord, error) {
	if s == nil || s.store == nil {
		return nil, apperror.New(apperror.CodeFailedPrecondition,
			"Skill candidate store is required")
	}
	values, err := s.store.ListSkillCandidates(ctx, strings.TrimSpace(runID))
	return values, apperror.Normalize(err)
}

func (s *SkillCandidateService) Review(ctx context.Context,
	request ReviewSkillCandidateRequest,
) (ReviewSkillCandidateResult, error) {
	if s == nil || s.store == nil {
		return ReviewSkillCandidateResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Skill candidate store is required")
	}
	normalized, err := normalizeReviewSkillCandidateRequest(request)
	if err != nil {
		return ReviewSkillCandidateResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Skill candidate review request is invalid", err)
	}
	record, err := s.store.GetSkillCandidate(ctx, normalized.CandidateID)
	if err != nil {
		return ReviewSkillCandidateResult{}, apperror.Normalize(err)
	}
	if record.Candidate.CandidateFingerprint != normalized.CandidateFingerprint {
		return ReviewSkillCandidateResult{}, apperror.New(apperror.CodeConflict,
			"candidate fingerprint does not match the content shown for review")
	}
	now := time.Now().UTC()
	review := skills.SkillCandidateReview{
		ID: idgen.New("skill-review"), ProtocolVersion: skills.SkillCandidateReviewProtocolVersion,
		OperationKeyDigest: runmutation.Fingerprint("skill_candidate_review_operation.v1",
			normalized.OperationKey),
		CandidateID:          record.Candidate.ID,
		CandidateFingerprint: record.Candidate.CandidateFingerprint,
		Decision:             normalized.Decision, Reason: normalized.Reason,
		Reviewer: normalized.Reviewer, CreatedAt: now,
	}
	review.RequestFingerprint = skills.SkillCandidateReviewRequestFingerprint(review)
	review.ReviewFingerprint = skills.SkillCandidateReviewFingerprint(review)
	stored, replayed, err := s.store.CreateSkillCandidateReview(ctx, review)
	if err != nil {
		return ReviewSkillCandidateResult{}, apperror.Normalize(err)
	}
	return ReviewSkillCandidateResult{Record: stored, Replayed: replayed}, nil
}

func (s *SkillCandidateService) Import(ctx context.Context,
	request ImportSkillCandidateRequest,
) (ImportSkillCandidateResult, error) {
	if s == nil || s.store == nil || s.registry == nil {
		return ImportSkillCandidateResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Skill candidate store and package Registry are required")
	}
	normalized, err := normalizeImportSkillCandidateRequest(request)
	if err != nil {
		return ImportSkillCandidateResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Skill candidate import request is invalid", err)
	}
	if !normalized.ConfirmUntrusted {
		return ImportSkillCandidateResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"candidate import requires explicit untrusted Skill confirmation")
	}
	record, err := s.store.GetSkillCandidate(ctx, normalized.CandidateID)
	if err != nil {
		return ImportSkillCandidateResult{}, apperror.Normalize(err)
	}
	if record.Candidate.CandidateFingerprint != normalized.CandidateFingerprint {
		return ImportSkillCandidateResult{}, apperror.New(apperror.CodeConflict,
			"candidate fingerprint does not match the approved content")
	}
	operationDigest := runmutation.Fingerprint("skill_candidate_import_operation.v1",
		normalized.OperationKey)
	intent := skills.SkillCandidateImport{
		CandidateID:          record.Candidate.ID,
		CandidateFingerprint: record.Candidate.CandidateFingerprint,
		ImportedBy:           normalized.ImportedBy,
	}
	if record.Import != nil {
		intent.ReviewFingerprint = record.Import.ReviewFingerprint
		intent.RequestFingerprint = skills.SkillCandidateImportRequestFingerprint(intent)
		if record.Import.OperationKeyDigest != operationDigest ||
			record.Import.RequestFingerprint != intent.RequestFingerprint {
			return ImportSkillCandidateResult{}, apperror.New(apperror.CodeConflict,
				"Skill candidate already has a different immutable import receipt")
		}
		installed, err := s.registry.Get(ctx, record.Candidate.Manifest.Name,
			record.Candidate.Manifest.Version)
		if err != nil {
			return ImportSkillCandidateResult{}, err
		}
		return ImportSkillCandidateResult{Record: record, InstalledPackage: installed,
			Replayed: true}, nil
	}
	if record.Review == nil || record.Review.Decision != skills.SkillCandidateReviewApprove {
		return ImportSkillCandidateResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"Skill candidate requires an approved exact-fingerprint human review")
	}
	raw, err := skills.BuildUnsignedPackage(record.Candidate.Manifest,
		[]byte(record.Candidate.Content))
	if err != nil {
		return ImportSkillCandidateResult{}, apperror.Wrap(
			apperror.CodeInternal, "approved Skill candidate failed package reconstruction", err)
	}
	installed, err := s.registry.Import(ctx, ImportSkillPackageRequest{
		Raw: raw, Surface: domain.ExecutionSurfaceCode,
		OperationKey: "candidate-" + operationDigest[:48], InstalledBy: normalized.ImportedBy,
		ConfirmUntrusted: true,
	})
	if err != nil {
		return ImportSkillCandidateResult{}, err
	}
	now := time.Now().UTC()
	receipt := skills.SkillCandidateImport{
		ID: idgen.New("skill-import"), ProtocolVersion: skills.SkillCandidateImportProtocolVersion,
		OperationKeyDigest: operationDigest, CandidateID: record.Candidate.ID,
		CandidateFingerprint:    record.Candidate.CandidateFingerprint,
		ReviewFingerprint:       record.Review.ReviewFingerprint,
		InstallationID:          installed.Package.Installation.ID,
		InstallationFingerprint: installed.Package.Installation.InstallationFingerprint,
		ImportedBy:              normalized.ImportedBy, CreatedAt: now,
	}
	receipt.RequestFingerprint = skills.SkillCandidateImportRequestFingerprint(receipt)
	receipt.ImportFingerprint = skills.SkillCandidateImportFingerprint(receipt)
	stored, replayed, err := s.store.CreateSkillCandidateImport(ctx, receipt)
	if err != nil {
		return ImportSkillCandidateResult{}, apperror.Normalize(err)
	}
	return ImportSkillCandidateResult{Record: stored, InstalledPackage: installed.Package,
		Replayed:         replayed || installed.Replayed,
		RecoveredPending: installed.RecoveredPending}, nil
}

func normalizeReviewSkillCandidateRequest(request ReviewSkillCandidateRequest) (
	ReviewSkillCandidateRequest, error,
) {
	request.CandidateID = strings.TrimSpace(request.CandidateID)
	request.CandidateFingerprint = strings.TrimSpace(request.CandidateFingerprint)
	request.Reason = strings.TrimSpace(redact.String(request.Reason))
	request.Reviewer = strings.TrimSpace(redact.String(request.Reviewer))
	if request.Reviewer == "" {
		request.Reviewer = "cli_operator"
	}
	if request.CandidateID == "" || len(request.CandidateID) > 256 ||
		len(request.CandidateFingerprint) != 64 || !request.Decision.Valid() ||
		reservedSkillCandidateHuman(request.Reviewer) {
		return ReviewSkillCandidateRequest{}, errors.New("candidate identity and decision are required")
	}
	if request.Decision == skills.SkillCandidateReviewReject && request.Reason == "" {
		return ReviewSkillCandidateRequest{}, errors.New("rejected candidate requires a reason")
	}
	if err := validateCandidateOperationKey(request.OperationKey); err != nil {
		return ReviewSkillCandidateRequest{}, err
	}
	return request, nil
}

func normalizeImportSkillCandidateRequest(request ImportSkillCandidateRequest) (
	ImportSkillCandidateRequest, error,
) {
	request.CandidateID = strings.TrimSpace(request.CandidateID)
	request.CandidateFingerprint = strings.TrimSpace(request.CandidateFingerprint)
	request.ImportedBy = strings.TrimSpace(redact.String(request.ImportedBy))
	if request.ImportedBy == "" {
		request.ImportedBy = "cli_operator"
	}
	if request.CandidateID == "" || len(request.CandidateID) > 256 ||
		len(request.CandidateFingerprint) != 64 || reservedSkillCandidateHuman(request.ImportedBy) {
		return ImportSkillCandidateRequest{}, errors.New("candidate identity is required")
	}
	if err := validateCandidateOperationKey(request.OperationKey); err != nil {
		return ImportSkillCandidateRequest{}, err
	}
	return request, nil
}

func reservedSkillCandidateHuman(value string) bool {
	switch strings.ToLower(value) {
	case "agent", "llm", "model", "repository", "repo", "skill", "supervisor",
		"run_supervisor":
		return true
	default:
		return false
	}
}

func validateCandidateOperationKey(value string) error {
	if strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return errors.New("operation key must be normalized UTF-8")
	}
	if _, err := domain.NormalizeAgentOperationKey(value); err != nil {
		return err
	}
	for _, current := range value {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return errors.New("operation key cannot contain whitespace or control characters")
		}
	}
	return nil
}
