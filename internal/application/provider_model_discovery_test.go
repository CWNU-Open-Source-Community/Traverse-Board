package application

import (
	"context"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/modelregistry"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type discoveryMutationCredential struct {
	credential.Store
	onGet func()
}

func (s discoveryMutationCredential) Get(ctx context.Context, id string) (string, bool, error) {
	s.onGet()
	return s.Store.Get(ctx, id)
}

func TestProviderModelDiscoveryDraftKeyNotPersistedAndStoredKeyBound(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer ordinary-private-key" {
			t.Error("wrong frozen credential")
		}
		fmt.Fprint(w, `{"data":[{"id":"fresh-model"}]}`)
	}))
	defer srv.Close()
	store := newProviderDefinitionMemoryStore()
	registry := modelregistry.New(nil)
	svc, err := NewProviderDefinitionService(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	creds := credential.NewMemoryStore()
	svc.WithModelDiscoveryCredentials(creds)
	r := ProviderModelDiscoveryRequest{Version: modelregistry.ModelDiscoveryVersion, ProviderID: "custom-acme", EndpointURL: srv.URL + "/v1/chat/completions", Transport: modelregistry.ProviderTransportOpenAIChatCompletions, Secret: "ordinary-private-key", ConfirmDiscovery: true}
	result, err := svc.DiscoverModels(t.Context(), r)
	if err != nil || len(result.Models) != 1 {
		t.Fatalf("empty draft catalog %v %v", result, err)
	}
	if configured, _ := creds.Configured(t.Context(), r.ProviderID); configured {
		t.Fatal("transient key was persisted")
	}
	listed, _ := svc.List(t.Context())
	if listed.Revision != 0 || len(listed.Providers) != 0 {
		t.Fatal("discovery saved a Provider")
	}
	d := customProviderDefinition(r.ProviderID)
	d.EndpointURL = r.EndpointURL
	d.Transport = r.Transport
	created, err := svc.Upsert(t.Context(), ProviderDefinitionUpsertRequest{Version: ProviderDefinitionControlProtocolVersion, Definition: d, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = creds.Put(t.Context(), r.ProviderID, r.Secret)
	r.Secret = ""
	r.ExpectedDefinitionRevision = created.Definition.Revision
	if _, err = svc.DiscoverModels(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	r.EndpointURL = srv.URL + "/other/responses"
	if _, err = svc.DiscoverModels(t.Context(), r); err == nil || calls.Load() != 2 {
		t.Fatalf("changed endpoint sent stored key: %v calls%d", err, calls.Load())
	}
	r.EndpointURL = d.EndpointURL
	svc.WithModelDiscoveryCredentials(discoveryMutationCredential{Store: creds, onGet: func() {
		updated := created.Definition
		updated.EndpointURL = srv.URL + "/changed/responses"
		_, e := svc.Upsert(t.Context(), ProviderDefinitionUpsertRequest{Version: ProviderDefinitionControlProtocolVersion, Definition: updated, ExpectedCollectionRevision: created.Collection.Revision, Confirm: true})
		if e != nil {
			t.Fatal(e)
		}
	}})
	if _, err = svc.DiscoverModels(t.Context(), r); err == nil || calls.Load() != 2 {
		t.Fatalf("endpoint/key interleaving dispatched HTTP: %v calls%d", err, calls.Load())
	}
}
