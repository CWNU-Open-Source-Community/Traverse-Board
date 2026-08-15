package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/pricing"
)

func TestPriceSnapshotImportAndList(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.api.priceSnapshotController = fixture.store
	fixture.api.modelControlEnabled = true

	listResponse := fixture.get(t, PriceSnapshotsPath)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("price list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var empty PriceSnapshotListView
	decodeDataStatus(t, listResponse, http.StatusOK, &empty)
	if empty.ProtocolVersion != PriceSnapshotListProtocol || len(empty.Items) != 0 {
		t.Fatalf("empty price list is invalid: %#v", empty)
	}

	document := openAPIPriceSnapshotDocument(t)
	importRaw, err := json.Marshal(PriceSnapshotImportRequestView{
		Version: pricing.ProtocolVersion, Document: document,
	})
	if err != nil {
		t.Fatal(err)
	}
	importBody := string(importRaw)
	first := performControlPathRequest(t, fixture.api, PriceSnapshotsPath,
		"price-snapshot-import-0001", strings.NewReader(importBody))
	var imported PriceSnapshotImportView
	decodeDataStatus(t, first, http.StatusOK, &imported)
	if imported.ProtocolVersion != pricing.ProtocolVersion || imported.Currency != pricing.CurrencyUSD ||
		imported.Source != pricing.SourceOperatorImport || imported.EntryCount != 1 ||
		imported.Fingerprint == "" || imported.Replayed {
		t.Fatalf("price import result is invalid: %#v", imported)
	}

	replayed := performControlPathRequest(t, fixture.api, PriceSnapshotsPath,
		"price-snapshot-import-0002", strings.NewReader(importBody))
	decodeDataStatus(t, replayed, http.StatusOK, &imported)
	if !imported.Replayed || imported.Fingerprint == "" {
		t.Fatalf("same-content import did not replay idempotently: %#v", imported)
	}

	listResponse = fixture.get(t, PriceSnapshotsPath)
	var listed PriceSnapshotListView
	decodeDataStatus(t, listResponse, http.StatusOK, &listed)
	if len(listed.Items) != 1 || listed.Items[0].EntryCount != 1 ||
		listed.Items[0].Entries[0].Provider != "mock" ||
		listed.Items[0].Entries[0].InputPerMillionMicros != 1000000 {
		t.Fatalf("price list did not reflect the import: %#v", listed)
	}
}

func TestPriceSnapshotImportRejectsTamperedAndGated(t *testing.T) {
	fixture := newAPIFixture(t)
	fixture.api.priceSnapshotController = fixture.store
	fixture.api.modelControlEnabled = true

	tampered := openAPIPriceSnapshotDocument(t)
	var wire pricing.Wire
	if err := json.Unmarshal([]byte(tampered), &wire); err != nil {
		t.Fatal(err)
	}
	wire.Entries[0].InputPerMillionMicros = 999999
	tamperedRaw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	tampered = string(tamperedRaw)
	tamperedRawView, err := json.Marshal(PriceSnapshotImportRequestView{
		Version: pricing.ProtocolVersion, Document: tampered,
	})
	if err != nil {
		t.Fatal(err)
	}
	tamperedBody := string(tamperedRawView)
	rejected := performControlPathRequest(t, fixture.api, PriceSnapshotsPath,
		"price-snapshot-tampered-0001", strings.NewReader(tamperedBody))
	if rejected.Code != http.StatusOK {
		t.Fatalf("self-consistent replacement price table status=%d want=200 body=%s",
			rejected.Code, rejected.Body.String())
	}

	invalidWire := openAPIPriceSnapshotDocument(t)
	var invalidWireDoc pricing.Wire
	if err := json.Unmarshal([]byte(invalidWire), &invalidWireDoc); err != nil {
		t.Fatal(err)
	}
	invalidWireDoc.Entries[0].InputPerMillionMicros = -1
	invalidRaw, err := json.Marshal(invalidWireDoc)
	if err != nil {
		t.Fatal(err)
	}
	invalidRawView, err := json.Marshal(PriceSnapshotImportRequestView{
		Version: pricing.ProtocolVersion, Document: string(invalidRaw),
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidBody := string(invalidRawView)
	invalid := performControlPathRequest(t, fixture.api, PriceSnapshotsPath,
		"price-snapshot-invalid-0001", strings.NewReader(invalidBody))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("negative price import status=%d want=400 body=%s", invalid.Code,
			invalid.Body.String())
	}

	malformed := performControlPathRequest(t, fixture.api, PriceSnapshotsPath,
		"price-snapshot-malformed-0001", strings.NewReader(`{"version":"price_snapshot.v1"}`))
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed import status=%d want=400 body=%s", malformed.Code,
			malformed.Body.String())
	}

	document := openAPIPriceSnapshotDocument(t)
	wrappedRaw, err := json.Marshal(PriceSnapshotImportRequestView{
		Version: pricing.ProtocolVersion, Document: document,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrappedDocument := string(wrappedRaw)
	fixture.api.modelControlEnabled = false
	gated := performControlPathRequest(t, fixture.api, PriceSnapshotsPath,
		"price-snapshot-gated-0001", strings.NewReader(wrappedDocument))
	if gated.Code != http.StatusNotFound {
		t.Fatalf("gated import status=%d want=404 body=%s", gated.Code, gated.Body.String())
	}

	fixture.api.modelControlEnabled = true
	wrappedRaw, err = json.Marshal(PriceSnapshotImportRequestView{
		Version: pricing.ProtocolVersion, Document: openAPIPriceSnapshotDocument(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	wrappedDocument = string(wrappedRaw)
	fixture.api.priceSnapshotController = nil
	missing := performControlPathRequest(t, fixture.api, PriceSnapshotsPath,
		"price-snapshot-missing-0001", strings.NewReader(wrappedDocument))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing controller status=%d want=404 body=%s", missing.Code,
			missing.Body.String())
	}

	fixture.api.priceSnapshotController = fixture.store
	untrusted := httptest.NewRequest(http.MethodGet, PriceSnapshotsPath, nil)
	untrusted.Host = "127.0.0.1"
	untrusted.RemoteAddr = "127.0.0.1:45000"
	untrusted.Header.Set("Authorization", "Bearer "+testAccessToken)
	untrustedResponse := httptest.NewRecorder()
	fixture.api.ServeHTTP(untrustedResponse, untrusted)
	if untrustedResponse.Code != http.StatusOK {
		t.Fatalf("authenticated read access status=%d want=200 body=%s",
			untrustedResponse.Code, untrustedResponse.Body.String())
	}
}
