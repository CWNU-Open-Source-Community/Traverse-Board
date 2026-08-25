package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"
)

//go:generate go run -tags clean_install_baseline_generate ./cmd/generate-clean-install-baseline -sql clean_install_baseline.sql -metadata clean_install_baseline_generated.go

// cleanInstallBaselineSQL is generated from a database built by the complete
// historical migration plan. It contains schema only; the immutable migration
// ledger is inserted from migrationPlan in the same transaction at runtime.
//
//go:embed clean_install_baseline.sql
var cleanInstallBaselineSQL string

const cleanInstallBaselineStatementSeparator = "\n-- traverse-board-clean-install-object-boundary --\n"

type cleanInstallBaselineArtifact struct {
	SchemaVersion       int
	SQL                 string
	SQLSHA256           string
	SchemaSHA256        string
	MigrationPlanSHA256 string
}

type sqliteQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *SQLiteStore) tryCleanInstallBaseline(ctx context.Context, plan []migration) (bool, error) {
	return s.tryCleanInstallBaselineArtifact(ctx, plan, cleanInstallBaselineArtifact{
		SchemaVersion:       cleanInstallBaselineSchemaVersion,
		SQL:                 cleanInstallBaselineSQL,
		SQLSHA256:           cleanInstallBaselineSQLSHA256,
		SchemaSHA256:        cleanInstallBaselineSchemaSHA256,
		MigrationPlanSHA256: cleanInstallBaselineMigrationPlanSHA256,
	})
}

func (s *SQLiteStore) tryCleanInstallBaselineArtifact(ctx context.Context, plan []migration,
	artifact cleanInstallBaselineArtifact,
) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("sqlite store is not open")
	}
	if err := validateMigrationPlan(plan, nil); err != nil {
		return false, err
	}
	if artifact.SchemaVersion != LatestSchemaVersion || len(plan) != artifact.SchemaVersion {
		// A stale or missing generated artifact is never trusted. The historical
		// path remains the safe creation path until the artifact is regenerated.
		return false, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("reserve clean-install baseline connection: %w", err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin clean-install baseline: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	empty, err := sqliteMainSchemaEmpty(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("prove clean-install database is empty: %w", err)
	}
	if !empty {
		return false, nil
	}
	if !cleanInstallBaselineMatchesPlan(plan, artifact) {
		return false, nil
	}
	statements := strings.Split(artifact.SQL, cleanInstallBaselineStatementSeparator)
	for index, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			return false, fmt.Errorf("clean-install schema baseline statement %d is empty", index+1)
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return false, fmt.Errorf("apply clean-install schema baseline statement %d: %w", index+1, err)
		}
	}

	appliedAt := ts(time.Now().UTC())
	for _, item := range plan {
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations
			(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
			item.Version, item.Name, migrationChecksum(item), appliedAt); err != nil {
			return false, fmt.Errorf("record clean-install migration %d: %w", item.Version, err)
		}
	}
	applied, err := loadAppliedMigrationsFrom(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("verify clean-install migration ledger: %w", err)
	}
	if err := validateMigrationPlan(plan, applied); err != nil {
		return false, fmt.Errorf("verify clean-install migration plan: %w", err)
	}
	digest, err := sqliteSchemaDigest(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("fingerprint clean-install schema: %w", err)
	}
	if digest != artifact.SchemaSHA256 {
		return false, fmt.Errorf("clean-install schema fingerprint mismatch: got %s want %s",
			digest, artifact.SchemaSHA256)
	}
	if err := verifySQLiteForeignKeys(ctx, tx); err != nil {
		return false, fmt.Errorf("verify clean-install foreign keys: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit clean-install baseline: %w", err)
	}
	return true, nil
}

func cleanInstallBaselineMatchesPlan(plan []migration, artifact cleanInstallBaselineArtifact) bool {
	if migrationPlanDigest(plan) != artifact.MigrationPlanSHA256 {
		return false
	}
	sum := sha256.Sum256([]byte(artifact.SQL))
	return hex.EncodeToString(sum[:]) == artifact.SQLSHA256
}

func sqliteMainSchemaEmpty(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (bool, error) {
	var count int
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM main.sqlite_schema
		WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\'`).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func migrationPlanDigest(plan []migration) string {
	digest := sha256.New()
	for _, item := range plan {
		writeDigestField(digest, fmt.Sprintf("%d", item.Version))
		writeDigestField(digest, item.Name)
		writeDigestField(digest, migrationChecksum(item))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func sqliteSchemaDigest(ctx context.Context, queryer sqliteQueryer) (string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT type, name, COALESCE(sql, '')
		FROM main.sqlite_schema WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\'
		ORDER BY type, name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	digest := sha256.New()
	for rows.Next() {
		var objectType, name, statement string
		if err := rows.Scan(&objectType, &name, &statement); err != nil {
			return "", err
		}
		writeDigestField(digest, objectType)
		writeDigestField(digest, name)
		writeDigestField(digest, strings.TrimSpace(statement))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeDigestField(digest hash.Hash, value string) {
	_, _ = fmt.Fprintf(digest, "%d:", len(value))
	_, _ = digest.Write([]byte(value))
	_, _ = digest.Write([]byte{0})
}

func verifySQLiteForeignKeys(ctx context.Context, queryer sqliteQueryer) error {
	rows, err := queryer.QueryContext(ctx, `PRAGMA foreign_key_check;`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return err
		}
		return fmt.Errorf("foreign-key violation table=%q row=%v parent=%q key=%d",
			table, rowID, parent, foreignKeyID)
	}
	return rows.Err()
}
