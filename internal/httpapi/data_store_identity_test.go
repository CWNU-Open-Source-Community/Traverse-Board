package httpapi

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/store"
)

// Embedding the public Store interface deliberately hides optional capabilities.
type legacyHealthStore struct{ Store }

func TestHealthDataStoreIdentityIsOptionalNonSecretAndReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-fixture-name.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
		if _, err := raw.Exec(`CREATE TRIGGER health_no_setting_` + operation + ` BEFORE ` + operation +
			` ON provider_setting BEGIN SELECT RAISE(ABORT, 'health must not write'); END`); err != nil {
			t.Fatal(err)
		}
	}
	api, err := New(st, Config{AccessToken: testAccessToken})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response := performRequest(t, api, http.MethodGet, "/api/v1/health", testAccessToken,
			"127.0.0.1", "127.0.0.1:12345", nil)
		var view HealthView
		decodeData(t, response, &view)
		if view.DataStoreID != st.DataStoreID() || view.DataStoreID == "" ||
			strings.Contains(response.Body.String(), "private-fixture-name") ||
			strings.Contains(response.Body.String(), testAccessToken) ||
			strings.Contains(response.Body.String(), "data_store_seed") {
			t.Fatal("health omitted its exact scope or exposed private initialization data")
		}
	}
	legacy, err := New(legacyHealthStore{Store: st}, Config{AccessToken: testAccessToken})
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(t, legacy, http.MethodGet, "/api/v1/health", testAccessToken,
		"127.0.0.1", "127.0.0.1:12345", nil)
	var view HealthView
	decodeData(t, response, &view)
	if view.DataStoreID != "" || strings.Contains(response.Body.String(), "data_store_id") {
		t.Fatal("legacy store fabricated a durable recovery namespace")
	}
}

func TestHealthDataStoreIdentityOpenAPIKeepsLegacyFieldOptional(t *testing.T) {
	registry := newOpenAPISchemaRegistry()
	registry.ref(reflect.TypeOf(HealthView{}))
	health := registry.schemas["HealthView"]
	properties := health["properties"].(map[string]any)
	identity := properties["data_store_id"].(map[string]any)
	if identity["pattern"] != "^ds1_[0-9a-f]{64}$" || identity["maxLength"] != 68 {
		t.Fatalf("recovery namespace schema is not bounded: %#v", identity)
	}
	for _, required := range health["required"].([]string) {
		if required == "data_store_id" {
			t.Fatal("recovery namespace became mandatory for old services")
		}
	}
}
