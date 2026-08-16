package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectLicense(t *testing.T) {
	mitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mitDir, "LICENSE"),
		[]byte("MIT License\n\nPermission is hereby granted, free of charge"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lic := detectLicense(mitDir); !lic.Found || lic.SPDXID != "MIT" {
		t.Fatalf("MIT license = %#v", lic)
	}

	apacheDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(apacheDir, "LICENSE.txt"),
		[]byte("Apache License\nVersion 2.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lic := detectLicense(apacheDir); !lic.Found || lic.SPDXID != "Apache-2.0" {
		t.Fatalf("Apache license = %#v", lic)
	}

	if lic := detectLicense(t.TempDir()); lic.Found {
		t.Fatalf("absent license unexpectedly found: %#v", lic)
	}
}

func TestValidateLicensesFailsClosed(t *testing.T) {
	mitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mitDir, "LICENSE"), []byte("MIT License"), 0o644); err != nil {
		t.Fatal(err)
	}
	unknownDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(unknownDir, "LICENSE"), []byte("custom terms"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateLicenses([]module{{Path: "example.com/ok", Version: "v1.0.0", Dir: mitDir}}); err != nil {
		t.Fatalf("known license rejected: %v", err)
	}
	err := validateLicenses([]module{
		{Path: "example.com/missing", Version: "v1.0.0", Dir: t.TempDir()},
		{Path: "example.com/unknown", Version: "v2.0.0", Dir: unknownDir},
	})
	if err == nil || !strings.Contains(err.Error(), "example.com/missing@v1.0.0 (missing)") ||
		!strings.Contains(err.Error(), "example.com/unknown@v2.0.0 (unrecognized)") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestBuildSBOMIsDeterministicCycloneDX(t *testing.T) {
	modules := []module{
		{Path: "github.com/example/b", Version: "v1.0.0", Dir: t.TempDir()},
		{Path: "github.com/example/a", Version: "v2.0.0", Dir: t.TempDir()},
	}
	first, err := buildSBOM(modules, "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildSBOM(modules, "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("SBOM is not deterministic")
	}
	var document struct {
		BomFormat   string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Components  []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Purl    string `json:"purl"`
		} `json:"components"`
	}
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatal(err)
	}
	if document.BomFormat != "CycloneDX" || document.SpecVersion != "1.4" {
		t.Fatalf("SBOM metadata = %#v", document)
	}
	if len(document.Components) != 2 ||
		document.Components[0].Name != "github.com/example/a" ||
		document.Components[1].Name != "github.com/example/b" {
		t.Fatalf("SBOM components not sorted: %#v", document.Components)
	}
}

func TestBuildNoticeListsModulesAndLicenseText(t *testing.T) {
	mitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mitDir, "LICENSE"),
		[]byte("MIT License"), 0o644); err != nil {
		t.Fatal(err)
	}
	modules := []module{{Path: "github.com/example/mit", Version: "v1.0.0", Dir: mitDir}}
	notice := buildNotice(modules)
	if !strings.Contains(string(notice), "github.com/example/mit@v1.0.0 (MIT)") ||
		!strings.Contains(string(notice), "MIT License") {
		t.Fatalf("NOTICE is incomplete: %s", notice)
	}
}

func TestCreateDeterministicZipIsByteStable(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(t.TempDir(), "first.zip")
	second := filepath.Join(t.TempDir(), "second.zip")
	if err := createDeterministicZip(source, first); err != nil {
		t.Fatal(err)
	}
	if err := createDeterministicZip(source, second); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("deterministic ZIP differs across runs")
	}
}
