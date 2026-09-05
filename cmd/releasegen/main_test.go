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
	notice := buildNotice(modules, nil)
	if !strings.Contains(string(notice), "github.com/example/mit@v1.0.0 (MIT)") ||
		!strings.Contains(string(notice), "MIT License") {
		t.Fatalf("NOTICE is incomplete: %s", notice)
	}
}

func TestBuildNoticeIncludesBundledAssetNoticeAndCompleteLicense(t *testing.T) {
	root := t.TempDir()
	writeBundledNoticeFixture(t, root,
		"Traverse Board uses HarmonyOS Sans Fonts.\r\nCopyright 2021 Huawei Device Co., Ltd.\r\n",
		"License Notice\r\nCopyright 2021 Huawei Device Co., Ltd.\r\n"+
			"HarmonyOS Sans Fonts License Agreement\r\ncomplete terms\r\n")

	bundledNotices, err := loadBundledAssetNotices(root)
	if err != nil {
		t.Fatal(err)
	}
	notice := string(buildNotice(nil, bundledNotices))
	for _, marker := range []string{
		"--- Bundled UI asset notices ---",
		"Traverse Board uses HarmonyOS Sans Fonts.",
		"--- Bundled UI asset license texts ---",
		"Copyright 2021 Huawei Device Co., Ltd.",
		"HarmonyOS Sans Fonts License Agreement\ncomplete terms",
	} {
		if !strings.Contains(notice, marker) {
			t.Fatalf("NOTICE is missing %q:\n%s", marker, notice)
		}
	}
	if strings.Contains(notice, "\r") {
		t.Fatalf("NOTICE did not normalize line endings:\n%q", notice)
	}
}

func TestLoadBundledAssetNoticesFailsClosed(t *testing.T) {
	t.Run("missing notice", func(t *testing.T) {
		root := t.TempDir()
		licensePath := filepath.Join(root, "web", "public", "licenses", "HarmonyOS-Sans.txt")
		if err := os.MkdirAll(filepath.Dir(licensePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(licensePath, []byte("Copyright 2021 Huawei Device Co., Ltd.\nHarmonyOS Sans Fonts License Agreement\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadBundledAssetNotices(root); err == nil || !strings.Contains(err.Error(), "notice") {
			t.Fatalf("missing bundled notice error = %v", err)
		}
	})

	t.Run("empty license", func(t *testing.T) {
		root := t.TempDir()
		writeBundledNoticeFixture(t, root, "HarmonyOS Sans Fonts\n", "\r\n")
		if _, err := loadBundledAssetNotices(root); err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("empty bundled license error = %v", err)
		}
	})
}

func writeBundledNoticeFixture(t *testing.T, root, notice, license string) {
	t.Helper()
	noticePath := filepath.Join(root, "web", "public", "THIRD-PARTY-NOTICES.txt")
	licensePath := filepath.Join(root, "web", "public", "licenses", "HarmonyOS-Sans.txt")
	for _, path := range []string{noticePath, licensePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(noticePath, []byte(notice), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(licensePath, []byte(license), 0o644); err != nil {
		t.Fatal(err)
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
