package store

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/toolcontract"
)

// Historical fixtures must not retain the widened v2 table or erase native
// installations. Rebuild the exact v121 definition before removing its ledger.
func removeSchemaV177ForTestStatements() []string {
	statements := []string{
		`CREATE TEMP TABLE legacy_fixture_no_portable_installations(n INTEGER CHECK(n=0));`,
		`INSERT INTO legacy_fixture_no_portable_installations SELECT count(*) FROM plugin_installations WHERE protocol_version!='plugin-installation.v1';`,
		`DROP TABLE legacy_fixture_no_portable_installations;`,
		`PRAGMA foreign_keys=OFF;`,
	}
	var recreate []string
	for _, s := range pluginRuntimeStatements {
		if strings.HasPrefix(s, "CREATE TABLE plugin_installations (") {
			statements = append(statements, strings.Replace(s, "CREATE TABLE plugin_installations (", "CREATE TABLE plugin_installations_pre177 (", 1))
		}
		if (strings.HasPrefix(s, "CREATE INDEX") || strings.HasPrefix(s, "CREATE UNIQUE INDEX") || strings.HasPrefix(s, "CREATE TRIGGER")) && strings.Contains(s, "ON plugin_installations") {
			recreate = append(recreate, s)
		}
	}
	statements = append(statements, `INSERT INTO plugin_installations_pre177 SELECT * FROM plugin_installations;`,
		`DROP TABLE plugin_installations;`, `ALTER TABLE plugin_installations_pre177 RENAME TO plugin_installations;`)
	statements = append(statements, recreate...)
	return append(statements, `DELETE FROM schema_migrations WHERE version=177;`, `PRAGMA foreign_keys=ON;`)
}

func TestSchemaV177PreservesV1RowsObjectsSignaturesAndTransitions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v176-plugins.db")
	st := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(t.Context(), st, migrationPlan(), 176); err != nil {
		t.Fatal(err)
	}
	svc, _ := plugins.NewService(st)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	legacy := stageHookPluginFixture(t, t.Context(), svc, "legacy-signed-plugin", "1.2.3", "", key)
	legacy = reviewHookPluginFixture(t, t.Context(), svc, legacy, plugins.ReviewApprove, true)
	legacy = reviewHookPluginFixture(t, t.Context(), svc, legacy, plugins.ReviewEnable, true)
	archive, err := st.LoadPluginObject(t.Context(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := plugins.ParsePackage(archive)
	if err != nil || !parsed.SignatureValid {
		t.Fatalf("legacy signature before upgrade: %v", err)
	}
	var manifest, source, planBefore string
	if err := st.db.QueryRow(`SELECT manifest_json,source_json FROM plugin_installations WHERE id=?`, legacy.ID).Scan(&manifest, &source); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT group_concat(version || ':' || checksum, ',') FROM schema_migrations`).Scan(&planBefore); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	actual, err := st.GetPluginInstallation(t.Context(), legacy.ID)
	if err != nil || !reflect.DeepEqual(actual, legacy) {
		t.Fatalf("v1 record changed: %+v %v", actual, err)
	}
	got, err := st.LoadPluginObject(t.Context(), legacy.ID)
	if err != nil || !bytes.Equal(got, archive) {
		t.Fatal("v1 object changed", err)
	}
	var afterManifest, afterSource, planAfter string
	if err := st.db.QueryRow(`SELECT manifest_json,source_json FROM plugin_installations WHERE id=?`, legacy.ID).Scan(&afterManifest, &afterSource); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT group_concat(version || ':' || checksum, ',') FROM schema_migrations WHERE version<=176`).Scan(&planAfter); err != nil {
		t.Fatal(err)
	}
	if afterManifest != manifest || afterSource != source || planAfter != planBefore {
		t.Fatal("migration rewrote historical JSON or checksums")
	}
	var transitions int
	if err := st.db.QueryRow(`SELECT count(*) FROM plugin_installation_transitions WHERE installation_id=?`, legacy.ID).Scan(&transitions); err != nil || transitions != 2 {
		t.Fatal("transition history lost", transitions, err)
	}
	rows, err := st.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("migration damaged foreign keys")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE plugin_installations SET source_json='{}',generation=generation+1 WHERE id=?`, legacy.ID); err == nil {
		t.Fatal("identity trigger lost")
	}
	if _, err := st.db.Exec(`DELETE FROM plugin_objects WHERE package_fingerprint=?`, legacy.PackageFingerprint); err == nil {
		t.Fatal("object retention trigger lost")
	}
	if _, err := st.db.Exec(`DELETE FROM plugin_installations WHERE id=?`, legacy.ID); err == nil {
		t.Fatal("installation retention trigger lost")
	}
	svc, _ = plugins.NewService(st)
	disabled := reviewHookPluginFixture(t, t.Context(), svc, actual, plugins.ReviewDisable, false)
	if disabled.Generation != legacy.Generation+1 || disabled.State != plugins.StateDisabled {
		t.Fatal("v1 review cannot advance after upgrade")
	}
	assertLatestMigrationLedger(t, st, migrationPlan())
}

func TestSchemaV177RejectsMalformedV2Descriptor(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "v2-constraints.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, _ := plugins.NewService(st)
	directory, err := filepath.Abs(filepath.Join("..", "agentpackages", "testdata", "upstream", "anthropic-skill-creator", "skills", "skill-creator"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := plugins.CapturePortableDirectory(t.Context(), directory, "portable-constraint-fixture", toolcontract.SourceRef{URI: directory}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := svc.StageSnapshot(t.Context(), pkg.Snapshot, pkg.Archive(), plugins.InstallSource{Kind: "local_directory", URI: directory, Surface: "code", OperationKeyDigest: strings.Repeat("a", 64)}, "", "operator", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if value.Snapshot.AuthorVersion != "" || value.Manifest.Publisher != "" {
		t.Fatal("invented author/version")
	}
	before, err := sqliteSchemaDigest(t.Context(), st.db)
	if err != nil {
		t.Fatal(err)
	}
	rejected := false
	for _, statement := range removeSchemaV177ForTestStatements() {
		if _, err := st.db.Exec(statement); err != nil {
			rejected = true
			break
		}
	}
	after, err := sqliteSchemaDigest(t.Context(), st.db)
	if err != nil || !rejected || before != after {
		t.Fatal("fixture downgrade mutated native schema before rejecting installed data", err)
	}
	if version, err := st.SchemaVersion(t.Context()); err != nil || version != LatestSchemaVersion {
		t.Fatal("fixture downgrade discarded native migration ledger", err)
	}
	// Bypass only the identity trigger in this disposable database to exercise
	// the persistent CHECK constraints independently of Go validation.
	if _, err := st.db.Exec(`DROP TRIGGER trg_plugin_installation_immutable_identity`); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{
		`publisher='invented.publisher'`,
		`plugin_version='1.0.0'`,
		`source_json=json_remove(source_json,'$.surface')`,
		`source_json=json_remove(source_json,'$.operation_key_digest')`,
		`manifest_json=json_set(manifest_json,'$.revision','wrong')`,
		`signature_present=1`,
	} {
		if _, err := st.db.Exec(`UPDATE plugin_installations SET `+set+`,generation=generation+1 WHERE id=?`, value.ID); err == nil {
			t.Fatal("accepted malformed v2", set)
		}
	}
	loaded, err := st.GetPluginInstallation(t.Context(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(loaded.Snapshot)
	if !bytes.Contains(raw, []byte(`"format":"agent-skills"`)) {
		t.Fatal("lost native format")
	}
	archive, err := st.LoadPluginObject(t.Context(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	archive[len(archive)/2] ^= 1
	if _, err := st.db.Exec(`DROP TRIGGER trg_plugin_objects_update_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE plugin_objects SET archive=? WHERE package_fingerprint=?`, archive, value.PackageFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadPluginObject(t.Context(), value.ID); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatal("object reader accepted corrupted retained bytes", err)
	}
}
