package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/skills"
)

const skillCandidateSelect = `SELECT id, protocol_version, operation_key_digest,
	request_fingerprint, invocation_id, run_id, root_agent_id, session_id, workspace_id, surface,
	manifest_json, content, archive_sha256, package_fingerprint, archive_bytes,
	candidate_fingerprint, requested_by, created_at FROM skill_candidates`

type skillCandidateQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *SQLiteStore) CreateSkillCandidate(ctx context.Context,
	candidate skills.SkillCandidate,
) (skills.SkillCandidate, bool, error) {
	candidate = skills.CloneSkillCandidate(candidate)
	if err := candidate.Validate(); err != nil {
		return skills.SkillCandidate{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "Skill candidate is invalid", err)
	}
	if candidate.CreatedAt.After(time.Now().UTC()) {
		return skills.SkillCandidate{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Skill candidate timestamp is in the future")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return skills.SkillCandidate{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, lookupErr := getSkillCandidateByOperation(ctx, tx,
		candidate.OperationKeyDigest); lookupErr != nil {
		return skills.SkillCandidate{}, false, lookupErr
	} else if found {
		if !sameSkillCandidateRequest(existing, candidate) {
			return skills.SkillCandidate{}, false, apperror.New(apperror.CodeConflict,
				"Skill candidate operation key was already used for different content")
		}
		if err := tx.Commit(); err != nil {
			return skills.SkillCandidate{}, false, err
		}
		return existing, true, nil
	}
	manifestJSON, err := json.Marshal(candidate.Manifest)
	if err != nil {
		return skills.SkillCandidate{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_candidates
		(id, protocol_version, operation_key_digest, request_fingerprint, invocation_id, run_id,
		root_agent_id, session_id, workspace_id, surface, manifest_json, content,
		archive_sha256, package_fingerprint, archive_bytes, candidate_fingerprint,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		candidate.ID, candidate.ProtocolVersion, candidate.OperationKeyDigest,
		candidate.RequestFingerprint, candidate.InvocationID, candidate.RunID, candidate.RootAgentID,
		candidate.SessionID, candidate.WorkspaceID, candidate.Surface, string(manifestJSON),
		candidate.Content, candidate.ArchiveSHA256, candidate.PackageFingerprint,
		candidate.ArchiveBytes, candidate.CandidateFingerprint, candidate.RequestedBy,
		ts(candidate.CreatedAt)); err != nil {
		return skills.SkillCandidate{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return skills.SkillCandidate{}, false, err
	}
	return skills.CloneSkillCandidate(candidate), false, nil
}

func (s *SQLiteStore) GetSkillCandidate(ctx context.Context,
	id string,
) (skills.SkillCandidateRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 || strings.ContainsRune(id, 0) {
		return skills.SkillCandidateRecord{}, apperror.New(
			apperror.CodeInvalidArgument, "Skill candidate id is invalid")
	}
	return getSkillCandidateRecord(ctx, s.db, id)
}

func (s *SQLiteStore) ListSkillCandidates(ctx context.Context,
	runID string,
) ([]skills.SkillCandidateRecord, error) {
	runID = strings.TrimSpace(runID)
	query := `SELECT id FROM skill_candidates`
	var args []any
	if runID != "" {
		if len(runID) > 256 || strings.ContainsRune(runID, 0) {
			return nil, apperror.New(apperror.CodeInvalidArgument,
				"Skill candidate Run id is invalid")
		}
		query += ` WHERE run_id = ?`
		args = append(args, runID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > skills.MaxSkillCandidates {
		return nil, apperror.New(apperror.CodeInternal,
			"Skill candidate Registry exceeds its bound")
	}
	values := make([]skills.SkillCandidateRecord, 0, len(ids))
	for _, id := range ids {
		value, err := getSkillCandidateRecord(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (s *SQLiteStore) CreateSkillCandidateReview(ctx context.Context,
	review skills.SkillCandidateReview,
) (skills.SkillCandidateRecord, bool, error) {
	if err := review.Validate(); err != nil {
		return skills.SkillCandidateRecord{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "Skill candidate review is invalid", err)
	}
	if review.CreatedAt.After(time.Now().UTC()) {
		return skills.SkillCandidateRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Skill candidate review timestamp is in the future")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, lookupErr := getSkillCandidateReviewByOperation(ctx, tx,
		review.OperationKeyDigest); lookupErr != nil {
		return skills.SkillCandidateRecord{}, false, lookupErr
	} else if found {
		if !sameSkillCandidateReviewRequest(existing, review) {
			return skills.SkillCandidateRecord{}, false, apperror.New(apperror.CodeConflict,
				"Skill candidate review operation key was already used for another decision")
		}
		record, err := getSkillCandidateRecord(ctx, tx, existing.CandidateID)
		if err != nil {
			return skills.SkillCandidateRecord{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return skills.SkillCandidateRecord{}, false, err
		}
		return record, true, nil
	}
	record, err := getSkillCandidateRecord(ctx, tx, review.CandidateID)
	if err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	if record.Review != nil {
		return skills.SkillCandidateRecord{}, false, apperror.New(apperror.CodeConflict,
			"Skill candidate already has an immutable human review")
	}
	if review.CandidateFingerprint != record.Candidate.CandidateFingerprint ||
		review.CreatedAt.Before(record.Candidate.CreatedAt) {
		return skills.SkillCandidateRecord{}, false, apperror.New(apperror.CodeConflict,
			"Skill candidate review does not bind the exact candidate")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_candidate_reviews
		(id, protocol_version, operation_key_digest, request_fingerprint, candidate_id,
		candidate_fingerprint, decision, reason, reviewer, review_fingerprint, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, review.ID, review.ProtocolVersion,
		review.OperationKeyDigest, review.RequestFingerprint, review.CandidateID,
		review.CandidateFingerprint, review.Decision, review.Reason, review.Reviewer,
		review.ReviewFingerprint, ts(review.CreatedAt)); err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	record.Review = &review
	return skills.CloneSkillCandidateRecord(record), false, record.Validate()
}

func (s *SQLiteStore) CreateSkillCandidateImport(ctx context.Context,
	value skills.SkillCandidateImport,
) (skills.SkillCandidateRecord, bool, error) {
	if err := value.Validate(); err != nil {
		return skills.SkillCandidateRecord{}, false, apperror.Wrap(
			apperror.CodeInvalidArgument, "Skill candidate import is invalid", err)
	}
	if value.CreatedAt.After(time.Now().UTC()) {
		return skills.SkillCandidateRecord{}, false, apperror.New(
			apperror.CodeInvalidArgument, "Skill candidate import timestamp is in the future")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, lookupErr := getSkillCandidateImportByOperation(ctx, tx,
		value.OperationKeyDigest); lookupErr != nil {
		return skills.SkillCandidateRecord{}, false, lookupErr
	} else if found {
		if !sameSkillCandidateImportRequest(existing, value) {
			return skills.SkillCandidateRecord{}, false, apperror.New(apperror.CodeConflict,
				"Skill candidate import operation key was already used for another candidate")
		}
		record, err := getSkillCandidateRecord(ctx, tx, existing.CandidateID)
		if err != nil {
			return skills.SkillCandidateRecord{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return skills.SkillCandidateRecord{}, false, err
		}
		return record, true, nil
	}
	record, err := getSkillCandidateRecord(ctx, tx, value.CandidateID)
	if err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	if record.Import != nil {
		return skills.SkillCandidateRecord{}, false, apperror.New(apperror.CodeConflict,
			"Skill candidate already has an immutable import receipt")
	}
	if record.Review == nil || record.Review.Decision != skills.SkillCandidateReviewApprove ||
		value.CandidateFingerprint != record.Candidate.CandidateFingerprint ||
		value.ReviewFingerprint != record.Review.ReviewFingerprint ||
		value.CreatedAt.Before(record.Review.CreatedAt) {
		return skills.SkillCandidateRecord{}, false, apperror.New(apperror.CodeFailedPrecondition,
			"Skill candidate import requires an exact approved human review")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_candidate_imports
		(id, protocol_version, operation_key_digest, request_fingerprint, candidate_id,
		candidate_fingerprint, review_fingerprint, installation_id,
		installation_fingerprint, imported_by, import_fingerprint, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, value.ID, value.ProtocolVersion,
		value.OperationKeyDigest, value.RequestFingerprint, value.CandidateID,
		value.CandidateFingerprint, value.ReviewFingerprint, value.InstallationID,
		value.InstallationFingerprint, value.ImportedBy, value.ImportFingerprint,
		ts(value.CreatedAt)); err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return skills.SkillCandidateRecord{}, false, err
	}
	record.Import = &value
	return skills.CloneSkillCandidateRecord(record), false, record.Validate()
}

func getSkillCandidateRecord(ctx context.Context, queryer skillCandidateQueryer,
	id string,
) (skills.SkillCandidateRecord, error) {
	candidate, err := scanSkillCandidate(queryer.QueryRowContext(ctx,
		skillCandidateSelect+` WHERE id = ?`, id))
	if err != nil {
		return skills.SkillCandidateRecord{}, err
	}
	review, reviewed, err := getSkillCandidateReview(ctx, queryer, id)
	if err != nil {
		return skills.SkillCandidateRecord{}, err
	}
	imported, foundImport, err := getSkillCandidateImport(ctx, queryer, id)
	if err != nil {
		return skills.SkillCandidateRecord{}, err
	}
	record := skills.SkillCandidateRecord{Candidate: candidate}
	if reviewed {
		record.Review = &review
	}
	if foundImport {
		record.Import = &imported
	}
	if err := record.Validate(); err != nil {
		return skills.SkillCandidateRecord{}, apperror.Wrap(
			apperror.CodeInternal, "stored Skill candidate record is invalid", err)
	}
	return skills.CloneSkillCandidateRecord(record), nil
}

func getSkillCandidateByOperation(ctx context.Context, queryer skillCandidateQueryer,
	digest string,
) (skills.SkillCandidate, bool, error) {
	value, err := scanSkillCandidate(queryer.QueryRowContext(ctx,
		skillCandidateSelect+` WHERE operation_key_digest = ?`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return skills.SkillCandidate{}, false, nil
	}
	return value, err == nil, err
}

func scanSkillCandidate(row packageRowScanner) (skills.SkillCandidate, error) {
	var value skills.SkillCandidate
	var manifestJSON, created string
	if err := row.Scan(&value.ID, &value.ProtocolVersion, &value.OperationKeyDigest,
		&value.RequestFingerprint, &value.InvocationID, &value.RunID, &value.RootAgentID, &value.SessionID,
		&value.WorkspaceID, &value.Surface, &manifestJSON, &value.Content,
		&value.ArchiveSHA256, &value.PackageFingerprint, &value.ArchiveBytes,
		&value.CandidateFingerprint, &value.RequestedBy, &created); err != nil {
		return skills.SkillCandidate{}, err
	}
	manifest, err := skills.DecodeManifestMetadata([]byte(manifestJSON))
	if err != nil {
		return skills.SkillCandidate{}, apperror.Wrap(
			apperror.CodeInternal, "stored Skill candidate manifest is invalid", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || string(canonical) != manifestJSON {
		return skills.SkillCandidate{}, apperror.New(
			apperror.CodeInternal, "stored Skill candidate manifest is not canonical")
	}
	value.Manifest = manifest
	value.CreatedAt = parseTS(created)
	if err := value.Validate(); err != nil {
		return skills.SkillCandidate{}, apperror.Wrap(
			apperror.CodeInternal, "stored Skill candidate is invalid", err)
	}
	return skills.CloneSkillCandidate(value), nil
}

func getSkillCandidateReview(ctx context.Context, queryer skillCandidateQueryer,
	candidateID string,
) (skills.SkillCandidateReview, bool, error) {
	return scanSkillCandidateReview(queryer.QueryRowContext(ctx,
		`SELECT id, protocol_version, operation_key_digest, request_fingerprint,
		candidate_id, candidate_fingerprint, decision, reason, reviewer,
		review_fingerprint, created_at FROM skill_candidate_reviews WHERE candidate_id = ?`,
		candidateID))
}

func getSkillCandidateReviewByOperation(ctx context.Context, queryer skillCandidateQueryer,
	digest string,
) (skills.SkillCandidateReview, bool, error) {
	return scanSkillCandidateReview(queryer.QueryRowContext(ctx,
		`SELECT id, protocol_version, operation_key_digest, request_fingerprint,
		candidate_id, candidate_fingerprint, decision, reason, reviewer,
		review_fingerprint, created_at FROM skill_candidate_reviews WHERE operation_key_digest = ?`,
		digest))
}

func scanSkillCandidateReview(row packageRowScanner) (skills.SkillCandidateReview, bool, error) {
	var value skills.SkillCandidateReview
	var created string
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.OperationKeyDigest,
		&value.RequestFingerprint, &value.CandidateID, &value.CandidateFingerprint,
		&value.Decision, &value.Reason, &value.Reviewer, &value.ReviewFingerprint, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return skills.SkillCandidateReview{}, false, nil
	}
	if err != nil {
		return skills.SkillCandidateReview{}, false, err
	}
	value.CreatedAt = parseTS(created)
	if err := value.Validate(); err != nil {
		return skills.SkillCandidateReview{}, false, apperror.Wrap(
			apperror.CodeInternal, "stored Skill candidate review is invalid", err)
	}
	return value, true, nil
}

func getSkillCandidateImport(ctx context.Context, queryer skillCandidateQueryer,
	candidateID string,
) (skills.SkillCandidateImport, bool, error) {
	return scanSkillCandidateImport(queryer.QueryRowContext(ctx,
		`SELECT id, protocol_version, operation_key_digest, request_fingerprint,
		candidate_id, candidate_fingerprint, review_fingerprint, installation_id,
		installation_fingerprint, imported_by, import_fingerprint, created_at
		FROM skill_candidate_imports WHERE candidate_id = ?`, candidateID))
}

func getSkillCandidateImportByOperation(ctx context.Context, queryer skillCandidateQueryer,
	digest string,
) (skills.SkillCandidateImport, bool, error) {
	return scanSkillCandidateImport(queryer.QueryRowContext(ctx,
		`SELECT id, protocol_version, operation_key_digest, request_fingerprint,
		candidate_id, candidate_fingerprint, review_fingerprint, installation_id,
		installation_fingerprint, imported_by, import_fingerprint, created_at
		FROM skill_candidate_imports WHERE operation_key_digest = ?`, digest))
}

func scanSkillCandidateImport(row packageRowScanner) (skills.SkillCandidateImport, bool, error) {
	var value skills.SkillCandidateImport
	var created string
	err := row.Scan(&value.ID, &value.ProtocolVersion, &value.OperationKeyDigest,
		&value.RequestFingerprint, &value.CandidateID, &value.CandidateFingerprint,
		&value.ReviewFingerprint, &value.InstallationID, &value.InstallationFingerprint,
		&value.ImportedBy, &value.ImportFingerprint, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return skills.SkillCandidateImport{}, false, nil
	}
	if err != nil {
		return skills.SkillCandidateImport{}, false, err
	}
	value.CreatedAt = parseTS(created)
	if err := value.Validate(); err != nil {
		return skills.SkillCandidateImport{}, false, apperror.Wrap(
			apperror.CodeInternal, "stored Skill candidate import is invalid", err)
	}
	return value, true, nil
}

func sameSkillCandidateRequest(left, right skills.SkillCandidate) bool {
	return left.OperationKeyDigest == right.OperationKeyDigest &&
		left.RequestFingerprint == right.RequestFingerprint &&
		left.InvocationID == right.InvocationID && left.RunID == right.RunID &&
		left.RootAgentID == right.RootAgentID && left.SessionID == right.SessionID &&
		left.WorkspaceID == right.WorkspaceID && left.RequestedBy == right.RequestedBy
}

func sameSkillCandidateReviewRequest(left, right skills.SkillCandidateReview) bool {
	return left.OperationKeyDigest == right.OperationKeyDigest &&
		left.RequestFingerprint == right.RequestFingerprint &&
		left.CandidateID == right.CandidateID &&
		left.CandidateFingerprint == right.CandidateFingerprint &&
		left.Decision == right.Decision && left.Reason == right.Reason &&
		left.Reviewer == right.Reviewer
}

func sameSkillCandidateImportRequest(left, right skills.SkillCandidateImport) bool {
	return left.OperationKeyDigest == right.OperationKeyDigest &&
		left.RequestFingerprint == right.RequestFingerprint &&
		left.CandidateID == right.CandidateID &&
		left.CandidateFingerprint == right.CandidateFingerprint &&
		left.ReviewFingerprint == right.ReviewFingerprint &&
		left.ImportedBy == right.ImportedBy
}
