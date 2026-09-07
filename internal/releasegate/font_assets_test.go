package releasegate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const approvedHarmonyOSSansArchiveSHA256 = "ba7ddf71fc4dee33a7170869564ad76d421a2ed5c58e5aac9a573c39945ef654"
const approvedHarmonyOSSansLicenseSHA256 = "16dec5061d77a322351a226d201edc8aa9edd058697c6c3def0b20388e09bbd5"

func TestApprovedHarmonyOSSansAssetsStayByteIdentical(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", ".."))
	fontDirectory := filepath.Join(root, "web", "src", "assets", "fonts")
	approved := map[string]struct {
		size   int64
		sha256 string
	}{
		"HarmonyOSSansSC-Bold.ttf":     {size: 8379892, sha256: "c215d8ab1cb6709fec2e063f8213e9af86d7587d345b56325e36b67d6b947d98"},
		"HarmonyOSSansSC-Medium.ttf":   {size: 8449140, sha256: "7aa97804da2fc3802d116011b73ee25791303598718cc58dc49fedc9d63e5d2a"},
		"HarmonyOSSansSC-Regular.ttf":  {size: 8483132, sha256: "984cf609545acee8ef060780fb70fc3099b058c0553416331b6e863fdf7c26fa"},
		"HarmonyOSSansSC-Semibold.ttf": {size: 8409696, sha256: "794eaca447316607a98d46b9d3269271c285a19dfd26cad608e3e52368eb855e"},
	}

	entries, err := os.ReadDir(fontDirectory)
	if err != nil {
		t.Fatal(err)
	}
	gotNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		gotNames = append(gotNames, entry.Name())
	}
	sort.Strings(gotNames)
	wantNames := []string{
		"HarmonyOSSansSC-Bold.ttf",
		"HarmonyOSSansSC-Medium.ttf",
		"HarmonyOSSansSC-Regular.ttf",
		"HarmonyOSSansSC-Semibold.ttf",
		"PROVENANCE.md",
	}
	if strings.Join(gotNames, "\x00") != strings.Join(wantNames, "\x00") {
		t.Fatalf("font asset set = %q, want exactly %q", gotNames, wantNames)
	}

	provenance := string(readTestFile(t, filepath.Join(fontDirectory, "PROVENANCE.md")))
	if !strings.Contains(provenance, approvedHarmonyOSSansArchiveSHA256) {
		t.Fatal("font provenance does not pin the approved vendor archive")
	}
	for name, expected := range approved {
		path := filepath.Join(fontDirectory, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Size() != expected.size {
			t.Fatalf("%s size/type = %d/%s, want %d/regular", name, info.Size(), info.Mode(), expected.size)
		}
		content := readTestFile(t, path)
		digest := sha256.Sum256(content)
		if got := hex.EncodeToString(digest[:]); got != expected.sha256 {
			t.Fatalf("%s SHA-256 = %s, want %s", name, got, expected.sha256)
		}
		if !strings.Contains(provenance, name) || !strings.Contains(provenance, expected.sha256) {
			t.Fatalf("font provenance does not bind %s to its approved digest", name)
		}
	}

	license := readTestFile(t, filepath.Join(root, "web", "public", "licenses", "HarmonyOS-Sans.txt"))
	licenseDigest := sha256.Sum256(license)
	if got := hex.EncodeToString(licenseDigest[:]); got != approvedHarmonyOSSansLicenseSHA256 {
		t.Fatalf("HarmonyOS Sans license SHA-256 = %s, want %s", got, approvedHarmonyOSSansLicenseSHA256)
	}
	notices := string(readTestFile(t, filepath.Join(root, "web", "public", "THIRD-PARTY-NOTICES.txt")))
	if !strings.Contains(notices, "HarmonyOS Sans") || !strings.Contains(notices, "/licenses/HarmonyOS-Sans.txt") {
		t.Fatal("third-party notices do not expose the HarmonyOS Sans license route")
	}
	attributes := string(readTestFile(t, filepath.Join(root, ".gitattributes")))
	if !strings.Contains(attributes, "web/src/assets/fonts/*.ttf -text") {
		t.Fatal("font binaries are not explicitly protected from Git text conversion")
	}
	workflow := string(readTestFile(t, filepath.Join(root, ".github", "workflows", "release-desktop.yml")))
	if !strings.Contains(workflow, "web/src/assets/fonts/**") || !strings.Contains(workflow, "web/public/**") {
		t.Fatal("Desktop release workflow does not watch fonts and their public notices")
	}
	readme := string(readTestFile(t, filepath.Join(root, "README.md")))
	settings := string(readTestFile(t, filepath.Join(root, "web", "src", "v2", "components", "settings.tsx")))
	if !strings.Contains(readme, "第三方字体声明") ||
		!strings.Contains(settings, "中文界面使用 HarmonyOS Sans Fonts；完整许可文本随软件发布。") ||
		!strings.Contains(settings, "FontLicenseControl") {
		t.Fatal("product surfaces do not prominently identify HarmonyOS Sans and expose its license")
	}
}
