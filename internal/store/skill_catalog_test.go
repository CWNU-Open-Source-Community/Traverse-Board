package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/toolgateway"
)

func removeSchemaV104ForTestStatements() []string {
	return append(removeSchemaV105ForTestStatements(), []string{
		`DROP TABLE skill_catalog_audit`,
		`DROP TABLE skill_catalog_imports`,
		`DROP TABLE skill_catalog_pins`,
		`DROP TABLE skill_catalog_publishers`,
		`DELETE FROM schema_migrations WHERE version = 104`,
	}...)
}

func removeSchemaV105ForTestStatements() []string {
	return append(removeSchemaV106ForTestStatements(), []string{
		`DROP TABLE once_command_proposal_operations`,
		`DROP TABLE once_command_proposals`,
		`DELETE FROM schema_migrations WHERE version = 105`,
	}...)
}

func removeSchemaV106ForTestStatements() []string {
	return append(removeSchemaV107ForTestStatements(), []string{
		`DROP TABLE git_mutation_operations`,
		`DELETE FROM schema_migrations WHERE version = 106`,
	}...)
}

func removeSchemaV107ForTestStatements() []string {
	return append(removeSchemaV108ForTestStatements(), []string{
		`DROP TABLE git_remote_operations`,
		`DELETE FROM schema_migrations WHERE version = 107`,
	}...)
}

func removeSchemaV108ForTestStatements() []string {
	return append(removeSchemaV109ForTestStatements(), []string{
		`DROP TABLE terminal_sessions`,
		`DELETE FROM schema_migrations WHERE version = 108`,
	}...)
}

func removeSchemaV109ForTestStatements() []string {
	return append(removeSchemaV110ForTestStatements(), []string{
		`DELETE FROM schema_migrations WHERE version = 109`,
	}...)
}

func removeSchemaV110ForTestStatements() []string {
	return append(removeSchemaV111ForTestStatements(), []string{
		`DROP TRIGGER trg_root_mode_skill_context_commit_delete_immutable`,
		`DROP TRIGGER trg_root_mode_skill_context_commit_update_immutable`,
		`DROP TRIGGER trg_root_mode_skill_context_preparation_delete_immutable`,
		`DROP TRIGGER trg_root_mode_skill_context_preparation_update_immutable`,
		`DROP TRIGGER trg_root_mode_skill_context_commit_insert`,
		`DROP TRIGGER trg_root_mode_skill_context_preparation_insert`,
		`DROP TABLE root_mode_skill_context_commits`,
		`DROP INDEX idx_root_mode_skill_context_run_turn`,
		`DROP TABLE root_mode_skill_context_preparations`,
		`DELETE FROM schema_migrations WHERE version = 110`,
	}...)
}

func removeSchemaV111ForTestStatements() []string {
	return append(removeSchemaV112ForTestStatements(), []string{
		`DROP TRIGGER skill_package_installation_insert_guard`,
		`ALTER TABLE skill_package_installations DROP COLUMN explicit_only`,
		`ALTER TABLE skill_package_installations DROP COLUMN model_invocable`,
		`ALTER TABLE skill_package_installations DROP COLUMN user_invocable`,
		`ALTER TABLE skill_package_installations DROP COLUMN roles_json`,
		`ALTER TABLE skill_package_installations DROP COLUMN phases_json`,
		`ALTER TABLE skill_package_installations DROP COLUMN surfaces_json`,
		skillPackageInstallationStatements[2],
		`DELETE FROM schema_migrations WHERE version = 111`,
	}...)
}

func removeSchemaV112ForTestStatements() []string {
	return append(removeSchemaV113ForTestStatements(), []string{
		`DROP TRIGGER skill_candidate_imports_no_delete`,
		`DROP TRIGGER skill_candidate_imports_no_update`,
		`DROP TRIGGER skill_candidate_reviews_no_delete`,
		`DROP TRIGGER skill_candidate_reviews_no_update`,
		`DROP TRIGGER skill_candidates_no_delete`,
		`DROP TRIGGER skill_candidates_no_update`,
		`DROP TRIGGER skill_candidate_import_insert_guard`,
		`DROP TRIGGER skill_candidate_review_insert_guard`,
		`DROP TRIGGER skill_candidate_insert_guard`,
		`DROP TABLE skill_candidate_imports`,
		`DROP TABLE skill_candidate_reviews`,
		`DROP INDEX idx_skill_candidates_run_created`,
		`DROP TABLE skill_candidates`,
		`DELETE FROM schema_migrations WHERE version = 112`,
	}...)
}

func removeSchemaV113ForTestStatements() []string {
	return []string{
		`DELETE FROM schema_migrations WHERE version = 113`,
	}
}
func TestSkillCatalogMigrationAndRemovalChain(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "skill-catalog-migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	version, err := st.SchemaVersion(ctx)
	if err != nil || version != LatestSchemaVersion {
		t.Fatalf("version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	for _, statement := range removeSchemaV105ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("remove v103 %q: %v", statement, err)
		}
	}
	version, err = st.SchemaVersion(ctx)
	if err != nil || version != 104 {
		t.Fatalf("version after v105 removal=%d want=104 err=%v", version, err)
	}
}

func TestSkillCatalogPublisherTrustAndRevoke(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "skill-catalog-trust.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	publicKey := strings.Repeat("a", 44) // base64 of a 32-byte key placeholder
	fingerprint := strings.Repeat("0", 64)
	publisher := SkillCatalogPublisher{Fingerprint: fingerprint, Name: "ctf-blue-team",
		Team: "blue", PublicKey: publicKey}
	if err := st.UpsertTrustedPublisher(ctx, publisher, "admin"); err != nil {
		t.Fatal(err)
	}
	got, found, err := st.GetSkillCatalogPublisher(ctx, fingerprint)
	if err != nil || !found || got.TrustClass != "trusted" || got.TrustedBy != "admin" || got.Team != "blue" {
		t.Fatalf("unexpected trusted publisher: %#v found=%t err=%v", got, found, err)
	}
	// Trust is idempotent per fingerprint.
	if err := st.UpsertTrustedPublisher(ctx, publisher, "admin"); err != nil {
		t.Fatalf("re-trust failed: %v", err)
	}
	if err := st.RevokeSkillCatalogPublisher(ctx, fingerprint, "admin"); err != nil {
		t.Fatal(err)
	}
	got, found, err = st.GetSkillCatalogPublisher(ctx, fingerprint)
	if err != nil || !found || got.TrustClass != "revoked" || got.RevokedBy != "admin" || got.RevokedAt == nil {
		t.Fatalf("unexpected revoked publisher: %#v found=%t err=%v", got, found, err)
	}
	if err := st.RevokeSkillCatalogPublisher(ctx, strings.Repeat("f", 64), "admin"); err == nil {
		t.Fatal("revoking an unknown publisher succeeded")
	}
	audit, err := st.ListSkillCatalogAudit(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var trusted, revoked int
	for _, event := range audit {
		switch event.EventType {
		case SkillCatalogTrustedEventType:
			trusted++
			if event.Subject != fingerprint {
				t.Fatalf("trust audit subject = %q", event.Subject)
			}
		case SkillCatalogRevokedEventType:
			revoked++
		}
	}
	if trusted != 2 || revoked != 1 {
		t.Fatalf("audit counts trusted=%d revoked=%d", trusted, revoked)
	}
}

func TestSkillCatalogPinGateRequiresInstallationAndTrust(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "skill-catalog-pin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	// Unsigned local package: pin without any publisher gate.
	insertSkillInstallationFixture(t, st, "scan-aid", "1.0.0", domain.ExecutionSurfaceCode,
		"operator_installed_untrusted")
	insertSkillInstallationFixture(t, st, "scan-aid", "1.1.0", domain.ExecutionSurfaceCode,
		"operator_installed_untrusted")
	// Package imported from a signed publisher that is not yet trusted.
	signedFingerprint := strings.Repeat("1", 64)
	insertSkillInstallationFixture(t, st, "signed-aid", "1.0.0", domain.ExecutionSurfaceCode,
		"operator_installed_untrusted")
	if err := st.PinSkillCatalogVersion(ctx, SkillCatalogPin{SkillName: "missing-skill",
		Version: "1.0.0", Surface: domain.ExecutionSurfaceCode}, "admin", ""); err == nil {
		t.Fatal("pinned a skill that is not installed")
	}
	if err := st.PinSkillCatalogVersion(ctx, SkillCatalogPin{SkillName: "scan-aid",
		Version: "1.0.0", Surface: domain.ExecutionSurfaceCode}, "admin", ""); err != nil {
		t.Fatalf("pin unsigned package: %v", err)
	}
	if err := st.PinSkillCatalogVersion(ctx, SkillCatalogPin{SkillName: "signed-aid",
		Version: "1.0.0", Surface: domain.ExecutionSurfaceCode}, "admin", signedFingerprint); err == nil {
		t.Fatal("pinned a signed package with an untrusted publisher")
	}
	publicKey := strings.Repeat("b", 44)
	if err := st.UpsertTrustedPublisher(ctx, SkillCatalogPublisher{
		Fingerprint: signedFingerprint, Name: "signed-vendor", PublicKey: publicKey}, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.PinSkillCatalogVersion(ctx, SkillCatalogPin{SkillName: "signed-aid",
		Version: "1.0.0", Surface: domain.ExecutionSurfaceCode}, "admin", signedFingerprint); err != nil {
		t.Fatalf("pin trusted signed package: %v", err)
	}
	// Upgrade: repin scan-aid to 1.1.0 and check the transition audit.
	if err := st.PinSkillCatalogVersion(ctx, SkillCatalogPin{SkillName: "scan-aid",
		Version: "1.1.0", Surface: domain.ExecutionSurfaceCode}, "admin", ""); err != nil {
		t.Fatal(err)
	}
	active, found, err := st.ActiveSkillCatalogPin(ctx, "scan-aid", domain.ExecutionSurfaceCode)
	if err != nil || !found || active.Version != "1.1.0" || !active.Enabled {
		t.Fatalf("unexpected active pin: %#v found=%t err=%v", active, found, err)
	}
	// Disable and enable with audit.
	if err := st.SetSkillCatalogPinEnabled(ctx, "scan-aid", domain.ExecutionSurfaceCode, false, "admin"); err != nil {
		t.Fatal(err)
	}
	active, _, _ = st.ActiveSkillCatalogPin(ctx, "scan-aid", domain.ExecutionSurfaceCode)
	if active.Enabled {
		t.Fatal("disabled pin still enabled")
	}
	if err := st.SetSkillCatalogPinEnabled(ctx, "scan-aid", domain.ExecutionSurfaceCode, true, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSkillCatalogPinEnabled(ctx, "never-pinned", domain.ExecutionSurfaceCode, false, "admin"); err == nil {
		t.Fatal("disabled a skill without a pin")
	}
	audit, err := st.ListSkillCatalogAudit(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var pinned, disabled, enabled int
	for _, event := range audit {
		switch event.EventType {
		case SkillCatalogPinnedEventType:
			pinned++
			if event.Subject == "scan-aid@1.1.0" && !strings.Contains(event.PayloadJSON, "upgraded_or_rolled_back") {
				t.Fatalf("upgrade transition missing: %s", event.PayloadJSON)
			}
		case SkillCatalogDisabledEventType:
			disabled++
		case SkillCatalogEnabledEventType:
			enabled++
		}
	}
	if pinned != 3 || disabled != 1 || enabled != 1 {
		t.Fatalf("audit counts pinned=%d disabled=%d enabled=%d", pinned, disabled, enabled)
	}
}

func TestSkillCatalogImportLedger(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "skill-catalog-import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	imp := SkillCatalogImport{ID: "imp-1", SourceKind: "url",
		Source: "https://publisher.example/skills/scan-aid.zip", Pin: strings.Repeat("a", 64),
		ArchiveSHA256: strings.Repeat("b", 64), PackageFingerprint: strings.Repeat("c", 64),
		PublisherFingerprint: strings.Repeat("d", 64)}
	if err := st.RecordSkillCatalogImport(ctx, imp, "admin"); err != nil {
		t.Fatal(err)
	}
	imports, err := st.ListSkillCatalogImports(ctx, 10)
	if err != nil || len(imports) != 1 || imports[0].SourceKind != "url" ||
		imports[0].Pin != imp.Pin || imports[0].ArchiveSHA256 != imp.ArchiveSHA256 {
		t.Fatalf("unexpected import ledger: %#v err=%v", imports, err)
	}
	bad := imp
	bad.ID = "imp-2"
	bad.SourceKind = "ftp"
	if err := st.RecordSkillCatalogImport(ctx, bad, "admin"); err == nil {
		t.Fatal("invalid source kind was accepted")
	}
	audit, err := st.ListSkillCatalogAudit(ctx, 10)
	if err != nil || len(audit) != 1 || audit[0].EventType != SkillImportCompletedEventType {
		t.Fatalf("unexpected import audit: %#v err=%v", audit, err)
	}
}

func insertSkillInstallationFixture(t *testing.T, st *SQLiteStore, name, version string,
	surface domain.ExecutionSurface, trustClass string,
) {
	t.Helper()
	ctx := context.Background()
	unique := fmt.Sprintf("%s-%s-%s", name, version, surface)
	archiveSHA256 := fmt.Sprintf("%064x", hashFixture(unique+"-archive"))
	packageFingerprint := fmt.Sprintf("%064x", hashFixture(unique+"-package"))
	installationID := "inst-" + unique
	now := time.Now().UTC()
	profiles := []domain.Profile{domain.ProfileCode}
	if surface == domain.ExecutionSurfaceCyber {
		profiles = []domain.Profile{domain.ProfileScript}
	}
	installation := skills.PackageInstallation{
		ID:              installationID,
		ProtocolVersion: skills.PackageInstallationProtocolVersion,
		Name:            name,
		Version:         version,
		Surface:         surface,
		Manifest: skills.Manifest{
			Protocol:               skills.ProtocolVersion,
			Name:                   name,
			Version:                version,
			Description:            "fixture skill",
			Profiles:               profiles,
			ToolDependencies:       []toolgateway.ToolName{},
			ContentPath:            "SKILL.md",
			ContentSHA256:          strings.Repeat("a", 64),
			ContentBytes:           4,
			ContentTokenUpperBound: 4,
		},
		ArchiveSHA256:      archiveSHA256,
		PackageFingerprint: packageFingerprint,
		ArchiveBytes:       64,
		UncompressedBytes:  64,
		EntryCount:         2,
		TrustClass:         skills.PackageTrustOperatorInstalledUntrusted,
		RiskCodes:          []skills.PackageRiskCode{skills.PackageRiskUntrustedInstructions, skills.PackageRiskDeclaredToolsOnly},
		OperatorConfirmed:  true,
		InstalledBy:        "fixture",
		CreatedAt:          now,
	}
	installation.OperationKeyDigest = fmt.Sprintf("%064x", hashFixture(unique+"-key"))
	installation.RequestFingerprint = skills.PackageInstallationIntentFingerprint(installation)
	installation.InstallationFingerprint = skills.PackageInstallationFingerprint(installation)
	nowText := ts(now)
	profilesJSON, err := json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin fixture tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_package_install_operations
		(key_digest, request_fingerprint, installation_id, name, version, surface, installed_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'fixture', ?)`,
		installation.OperationKeyDigest, installation.RequestFingerprint, installationID, name, version, surface, nowText); err != nil {
		t.Fatalf("insert operation fixture: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_package_installations
		(id, protocol_version, operation_key_digest, request_fingerprint, name, version, surface,
		manifest_protocol, description, profiles_json, tool_dependencies_json, content_path,
		content_sha256, content_bytes, content_token_upper_bound, archive_sha256, package_fingerprint,
		archive_bytes, uncompressed_bytes, entry_count, trust_class, risk_codes_json,
		executable_asset_count, install_hook_count, import_command_execution, import_network_access,
		import_provider_calls, tool_capability_grant, run_selection_authorized, context_injection_authorized,
		operator_confirmed, installation_fingerprint, installed_by, created_at)
		VALUES (?, 'skill_package_installation.v1', ?, ?, ?, ?, ?, 'skill.v1', 'fixture skill', ?,
		'[]', 'SKILL.md', ?, 4, 4, ?, ?, 64, 64, 2, 'operator_installed_untrusted', ?,
		0, 0, 0, 0, 0, 0, 0, 0, 1, ?, 'fixture', ?)`,
		installationID, installation.OperationKeyDigest, installation.RequestFingerprint, name, version, surface,
		string(profilesJSON), strings.Repeat("a", 64), archiveSHA256, packageFingerprint,
		`["untrusted_instructions","declared_tools_not_capabilities"]`,
		installation.InstallationFingerprint, nowText); err != nil {
		t.Fatalf("insert installation fixture: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit fixture tx: %v", err)
	}
}

func hashFixture(value string) uint64 {
	var hash uint64
	for _, current := range []byte(value) {
		hash = hash*131 + uint64(current)
	}
	return hash
}
