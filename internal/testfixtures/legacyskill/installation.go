// Package legacyskill seeds historical installation rows for compatibility tests.
// It is imported only by tests; production has no legacy first-install writer.
package legacyskill

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/skills"
)

// Seed opens an already migrated test database and inserts one pending intent.
// It neither changes schema nor disables constraints, and has no replay logic.
func Seed(t testing.TB, path string, value skills.PackageInstallation) {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := Insert(t.Context(), db, value); err != nil {
		t.Fatalf("seed historical Skill intent: %v", err)
	}
}

// Insert writes exactly the operation and installation rows supported by the
// historical schema. The error is exposed for tests of its original constraints.
func Insert(ctx context.Context, db *sql.DB, value skills.PackageInstallation) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := value.CreatedAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_package_install_operations
		(key_digest, request_fingerprint, installation_id, name, version, surface,
		installed_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, value.OperationKeyDigest,
		value.RequestFingerprint, value.ID, value.Name, value.Version, value.Surface,
		value.InstalledBy, stamp); err != nil {
		return err
	}
	m := value.Manifest
	columns := `id, protocol_version, operation_key_digest, request_fingerprint, name, version,
		surface, manifest_protocol, description, profiles_json, tool_dependencies_json,
		content_path, content_sha256, content_bytes, content_token_upper_bound,
		archive_sha256, package_fingerprint, archive_bytes, uncompressed_bytes,
		entry_count, trust_class, risk_codes_json, executable_asset_count,
		install_hook_count, import_command_execution, import_network_access,
		import_provider_calls, tool_capability_grant, run_selection_authorized,
		context_injection_authorized, operator_confirmed, installation_fingerprint,
		installed_by, created_at`
	args := []any{value.ID, value.ProtocolVersion, value.OperationKeyDigest, value.RequestFingerprint,
		value.Name, value.Version, value.Surface, m.Protocol, m.Description, arrayJSON(m.Profiles),
		arrayJSON(m.ToolDependencies), m.ContentPath, m.ContentSHA256, m.ContentBytes, m.ContentTokenUpperBound,
		value.ArchiveSHA256, value.PackageFingerprint, value.ArchiveBytes, value.UncompressedBytes,
		value.EntryCount, value.TrustClass, arrayJSON(value.RiskCodes), value.ExecutableAssetCount,
		value.InstallHookCount, value.ImportCommandExecution, value.ImportNetworkAccess,
		value.ImportProviderCalls, value.ToolCapabilityGrant, value.RunSelectionAuthorized,
		value.ContextInjectionAuthorized, value.OperatorConfirmed, value.InstallationFingerprint,
		value.InstalledBy, stamp}
	var modes int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('skill_package_installations')
		WHERE name = 'surfaces_json'`).Scan(&modes); err != nil {
		return err
	}
	if modes != 0 {
		columns += `, surfaces_json, phases_json, roles_json, user_invocable, model_invocable, explicit_only`
		args = append(args, arrayJSON(m.Surfaces), arrayJSON(m.Phases), arrayJSON(m.Roles),
			m.UserInvocable, m.ModelInvocable, m.ExplicitOnly)
	} else if m.HasModeMetadata() {
		return fmt.Errorf("pre-v111 fixture cannot store mode metadata")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	if _, err := tx.ExecContext(ctx, "INSERT INTO skill_package_installations ("+columns+
		") VALUES ("+placeholders+")", args...); err != nil {
		return err
	}
	return tx.Commit()
}

func arrayJSON[T ~string](values []T) string {
	if values == nil {
		return "[]"
	}
	raw, _ := json.Marshal(values) // String slices cannot fail JSON encoding.
	return string(raw)
}
