package releasegate

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func expectedWindowsVisualAssetMatrix() map[string]image.Point {
	expected := map[string]image.Point{
		"StoreLogo.png":                   image.Pt(50, 50),
		"Square150x150Logo.png":           image.Pt(150, 150),
		"Square44x44Logo.png":             image.Pt(44, 44),
		"Square44x44Logo.scale-200.png":   image.Pt(88, 88),
		"Square44x44Logo.scale-400.png":   image.Pt(176, 176),
		"Square150x150Logo.scale-200.png": image.Pt(300, 300),
		"Square150x150Logo.scale-400.png": image.Pt(600, 600),
		"StoreLogo.scale-125.png":         image.Pt(63, 63),
		"StoreLogo.scale-150.png":         image.Pt(75, 75),
		"StoreLogo.scale-200.png":         image.Pt(100, 100),
		"StoreLogo.scale-400.png":         image.Pt(200, 200),
	}
	for _, size := range []int{16, 20, 24, 30, 32, 36, 40, 48, 60, 64, 72, 80, 96, 256} {
		for _, suffix := range []string{"", "_altform-unplated", "_altform-lightunplated"} {
			name := fmt.Sprintf("Square44x44Logo.targetsize-%d%s.png", size, suffix)
			expected[name] = image.Pt(size, size)
		}
	}
	return expected
}

func TestWindowsMSIXVisualAssetMatrixIsExact(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	expected := expectedWindowsVisualAssetMatrix()
	if len(expected) != 53 {
		t.Fatalf("test matrix contains %d assets; want 53", len(expected))
	}

	assetRoot := filepath.Join(root, "packaging", "windows", "Assets")
	entries, err := os.ReadDir(assetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(expected) {
		t.Fatalf("Assets contains %d children; want exactly %d", len(entries), len(expected))
	}
	for _, entry := range entries {
		want, ok := expected[entry.Name()]
		if entry.IsDir() || !ok {
			t.Fatalf("Assets contains an unexpected child or incorrect-case name: %q", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(assetRoot, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) >= 204800 {
			t.Fatalf("%s is %d bytes; Windows App Certification requires less than 204800", entry.Name(), len(data))
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode %s: %v", entry.Name(), err)
		}
		got := image.Pt(decoded.Bounds().Dx(), decoded.Bounds().Dy())
		if got != want {
			t.Fatalf("%s dimensions are %v; want %v", entry.Name(), got, want)
		}
		corner := color.NRGBAModel.Convert(decoded.At(decoded.Bounds().Min.X, decoded.Bounds().Min.Y)).(color.NRGBA)
		center := color.NRGBAModel.Convert(decoded.At(
			decoded.Bounds().Min.X+decoded.Bounds().Dx()/2,
			decoded.Bounds().Min.Y+decoded.Bounds().Dy()/2,
		)).(color.NRGBA)
		if corner.A > 32 || center.A != 255 {
			t.Fatalf("%s rounded alpha is corner=%d center=%d; want <=32/255", entry.Name(), corner.A, center.A)
		}
	}

	helperPath := filepath.Join(root, "scripts", "windows-visual-assets.ps1")
	helper, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatal(err)
	}
	specPattern := regexp.MustCompile(`(?m)^\s*New-WindowsVisualAssetSpec -Name '([^']+)' -Width ([0-9]+) -Height ([0-9]+)\s*$`)
	matches := specPattern.FindAllStringSubmatch(string(helper), -1)
	if len(matches) != len(expected) {
		t.Fatalf("shared PowerShell contract contains %d literal specs; want %d", len(matches), len(expected))
	}
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		name := match[1]
		want, ok := expected[name]
		if !ok || seen[name] {
			t.Fatalf("shared PowerShell contract has unexpected or duplicate spec %q", name)
		}
		seen[name] = true
		width, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatal(err)
		}
		height, err := strconv.Atoi(match[3])
		if err != nil {
			t.Fatal(err)
		}
		if image.Pt(width, height) != want {
			t.Fatalf("shared PowerShell dimensions for %s are %dx%d; want %v", name, width, height, want)
		}
	}
}

func TestMSIXPackagingAndVerifierBindVisualAssetsAndPRI(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	read := func(path ...string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(append([]string{root}, path...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	packageScript := read("scripts", "package-msix.ps1")
	verifyScript := read("scripts", "verify-msix.ps1")
	for label, script := range map[string]string{
		"package-msix.ps1": packageScript,
		"verify-msix.ps1":  verifyScript,
	} {
		for _, required := range []string{
			"scripts/windows-visual-assets.ps1",
			". $visualAssetHelperPath",
			"Get-WindowsVisualAssetDirectoryInventory",
			"Assert-WindowsVisualAssetInventoriesEqual",
			"Assert-WindowsVisualAssetsPRI",
			"resources.pri",
		} {
			if !strings.Contains(script, required) {
				t.Fatalf("%s does not bind %q", label, required)
			}
		}
	}

	writeManifest := strings.Index(packageScript, "Write-ManifestXML -Document")
	copyAssets := strings.Index(packageScript, "foreach ($asset in $sourceVisualAssetInventory)")
	makePRI := strings.Index(packageScript, "& $makepri new")
	checkPRI := strings.Index(packageScript, "$stagedPRIHash = Assert-WindowsVisualAssetsPRI")
	makeAppx := strings.Index(packageScript, "& $makeappx pack")
	if writeManifest < 0 || copyAssets <= writeManifest || makePRI <= copyAssets ||
		checkPRI <= makePRI || makeAppx <= checkPRI {
		t.Fatal("MSIX packaging order must be manifest -> exact assets -> MakePri -> PRI audit -> MakeAppx")
	}
	for _, required := range []string{
		"/pr $staging", "/cf $priConfigPath", "/mn $stagedManifestPath",
		"/of $stagedPRIPath /o", "Get-WindowsVisualAssetDirectoryInventory -AssetRoot (Join-Path $unpackRoot \"Assets\")",
	} {
		if !strings.Contains(packageScript, required) {
			t.Fatalf("package-msix.ps1 is missing %q", required)
		}
	}
	for _, required := range []string{
		"Get-WindowsVisualAssetArchiveInventory -Archive $archive",
		"$priEntries.Count -ne 1",
		"[string]$priEntries[0].FullName -cne \"resources.pri\"",
		"Find-WindowsVisualAssetSDKTool -Name \"makepri\"",
	} {
		if !strings.Contains(verifyScript, required) {
			t.Fatalf("verify-msix.ps1 is missing %q", required)
		}
	}

	priConfig := read("packaging", "windows", "priconfig.xml")
	var parsed struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal([]byte(priConfig), &parsed); err != nil {
		t.Fatalf("priconfig.xml is not well-formed XML: %v", err)
	}
	if parsed.XMLName.Local != "resources" {
		t.Fatalf("priconfig.xml root is %q; want resources", parsed.XMLName.Local)
	}
	for _, forbidden := range []string{"autoResourcePackage", "AutoMerge", "ReverseMap"} {
		if strings.Contains(priConfig, forbidden) {
			t.Fatalf("priconfig.xml enables split or reverse-map output via %q", forbidden)
		}
	}
	for label, script := range map[string]string{
		"package-msix.ps1": packageScript,
		"verify-msix.ps1":  verifyScript,
	} {
		forbiddenOption := regexp.MustCompile(`(?i)(^|\s)/(rm|reversemap|am|automerge)(\s|$)`)
		if match := forbiddenOption.FindString(script); match != "" {
			t.Fatalf("%s enables forbidden MakePri option %q", label, strings.TrimSpace(match))
		}
	}
}
