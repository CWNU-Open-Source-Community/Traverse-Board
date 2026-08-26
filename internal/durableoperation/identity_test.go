package durableoperation_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/durableoperation"
)

type fingerprintGoldenFile struct {
	Vectors []fingerprintGoldenVector `json:"vectors"`
}

type fingerprintGoldenVector struct {
	Name   string   `json:"name"`
	Domain string   `json:"domain"`
	Fields []string `json:"fields"`
	SHA256 string   `json:"sha256"`
}

func TestFingerprintGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "identity_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var golden fingerprintGoldenFile
	if err := decoder.Decode(&golden); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("golden vector file has trailing JSON: %v", err)
	}
	if len(golden.Vectors) != 8 {
		t.Fatalf("golden vector count=%d want=8", len(golden.Vectors))
	}
	results := make(map[string]string, len(golden.Vectors))
	for _, vector := range golden.Vectors {
		if vector.Name == "" || results[vector.Name] != "" {
			t.Fatalf("golden vector name is missing or duplicated: %q", vector.Name)
		}
		got, err := durableoperation.Fingerprint(vector.Domain, vector.Fields...)
		if err != nil {
			t.Fatalf("%s: %v", vector.Name, err)
		}
		if got != vector.SHA256 {
			t.Fatalf("%s digest=%s want=%s", vector.Name, got, vector.SHA256)
		}
		results[vector.Name] = got
	}
	for _, pair := range [][2]string{
		{"boundary_left", "boundary_right"},
		{"order_forward", "order_reverse"},
	} {
		if results[pair[0]] == results[pair[1]] {
			t.Fatalf("golden vectors %s and %s collapsed", pair[0], pair[1])
		}
	}
}

func TestIdentityDecisionAndFailClosedValidation(t *testing.T) {
	key, err := durableoperation.Fingerprint("run_creation_operation.v1", "key")
	if err != nil {
		t.Fatal(err)
	}
	request, err := durableoperation.Fingerprint("run_creation_request.v1", "request")
	if err != nil {
		t.Fatal(err)
	}
	changedRequest, err := durableoperation.Fingerprint("run_creation_request.v1", "changed")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := durableoperation.NewIdentity("run_creation_operation.v1", key, request)
	if err != nil {
		t.Fatal(err)
	}
	same, err := durableoperation.NewIdentity("run_creation_operation.v1", key, request)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := durableoperation.Decide(stored, same)
	if err != nil || decision != durableoperation.DecisionReplay || decision.String() != "replay" {
		t.Fatalf("same identity decision=%s err=%v", decision, err)
	}
	changed, err := durableoperation.NewIdentity("run_creation_operation.v1", key, changedRequest)
	if err != nil {
		t.Fatal(err)
	}
	decision, err = durableoperation.Decide(stored, changed)
	if err != nil || decision != durableoperation.DecisionConflict || decision.String() != "conflict" {
		t.Fatalf("changed identity decision=%s err=%v", decision, err)
	}

	malformed := []struct {
		domain  string
		key     string
		request string
	}{
		{domain: "", key: key, request: request},
		{domain: "run_creation_operation.v1 ", key: key, request: request},
		{domain: "run_creation_operation.v1", key: "", request: request},
		{domain: "run_creation_operation.v1", key: strings.ToUpper(key), request: request},
		{domain: "run_creation_operation.v1", key: key, request: "short"},
	}
	for index, value := range malformed {
		if _, err := durableoperation.NewIdentity(value.domain, value.key, value.request); err == nil {
			t.Fatalf("malformed identity %d was accepted", index)
		}
	}
	if _, err := durableoperation.Decide(durableoperation.Identity{}, same); err == nil {
		t.Fatal("zero stored identity was accepted")
	}

	otherKey, err := durableoperation.Fingerprint("run_creation_operation.v1", "other-key")
	if err != nil {
		t.Fatal(err)
	}
	differentKey, err := durableoperation.NewIdentity(
		"run_creation_operation.v1", otherKey, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := durableoperation.Decide(stored, differentKey); err == nil {
		t.Fatal("different operation keys were treated as comparable retries")
	}
	differentDomain, err := durableoperation.NewIdentity(
		"scheduled_job_operation.v1", key, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := durableoperation.Decide(stored, differentDomain); err == nil {
		t.Fatal("different operation domains were treated as comparable retries")
	}
	if stored.DomainSeparator() != "run_creation_operation.v1" ||
		stored.OperationKeyDigest() != key || stored.RequestFingerprint() != request {
		t.Fatalf("identity accessors changed normalized values: %#v", stored)
	}
}

func TestFingerprintRejectsUnversionedDomainAndInvalidUTF8(t *testing.T) {
	if _, err := durableoperation.Fingerprint("run_creation_operation", "key"); err == nil {
		t.Fatal("unversioned domain separator was accepted")
	}
	if _, err := durableoperation.Fingerprint("run_creation_operation.v1",
		string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 field was accepted")
	}
}
