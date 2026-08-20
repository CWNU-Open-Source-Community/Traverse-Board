//go:build windows

package browserruntime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRestrictedBrowserEnvironmentUsesKnownFoldersWithoutProcessSecrets(
	t *testing.T,
) {
	profilePath := filepath.Join(`C:\evidence`, "profile")
	systemRoot := `C:\Windows`
	values, err := restrictedBrowserEnvironmentValues(profilePath, systemRoot,
		[]string{
			`USERPROFILE=C:\Users\fixture`,
			`LOCALAPPDATA=C:\Users\fixture\AppData\Local`,
			`APPDATA=C:\Users\fixture\AppData\Roaming`,
			`USERNAME=fixture`,
			`USERDOMAIN=WORKSTATION`,
			`HOMEDRIVE=C:`,
			`HOMEPATH=\Users\fixture`,
			`ProgramData=C:\ProgramData`,
			`Path=C:\untrusted-bin`,
			`PATHEXT=.UNTRUSTED`,
			`SystemRoot=C:\tampered`,
			`SECRET_TOKEN=must-not-reach-browser`,
		})
	if err != nil {
		t.Fatal(err)
	}
	environment := make(map[string]string, len(values))
	for index, entry := range values {
		if index > 0 && strings.ToLower(values[index-1]) > strings.ToLower(entry) {
			t.Fatal("browser environment block is not sorted case-insensitively")
		}
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			t.Fatalf("invalid browser environment entry %q", entry)
		}
		environment[entry[:separator]] = entry[separator+1:]
	}
	if _, present := environment["SECRET_TOKEN"]; present {
		t.Fatal("process secret escaped into the browser environment")
	}
	for name, want := range map[string]string{
		"APPDATA":      `C:\Users\fixture\AppData\Roaming`,
		"LOCALAPPDATA": `C:\Users\fixture\AppData\Local`,
		"USERPROFILE":  `C:\Users\fixture`,
		"HOME":         profilePath,
		"TEMP":         filepath.Join(profilePath, "Temp"),
		"TMP":          filepath.Join(profilePath, "Temp"),
		"SYSTEMROOT":   systemRoot,
		"WINDIR":       systemRoot,
		"PATH":         filepath.Join(systemRoot, "System32") + ";" + systemRoot,
		"PATHEXT":      ".COM;.EXE;.BAT;.CMD",
	} {
		if got := environment[name]; got != want {
			t.Fatalf("browser environment %s=%q, want %q", name, got, want)
		}
	}
}

func TestRestrictedBrowserEnvironmentRequiresTokenKnownFolders(t *testing.T) {
	_, err := restrictedBrowserEnvironmentValues(`C:\evidence\profile`,
		`C:\Windows`, []string{
			`USERPROFILE=C:\Users\fixture`,
			`APPDATA=C:\Users\fixture\AppData\Roaming`,
		})
	if err == nil {
		t.Fatal("browser environment accepted a missing LOCALAPPDATA known folder")
	}
}

func TestRestrictedBrowserEnvironmentRejectsDuplicateStructuralVariable(t *testing.T) {
	_, err := restrictedBrowserEnvironmentValues(`C:\evidence\profile`,
		`C:\Windows`, []string{
			`USERPROFILE=C:\Users\fixture`,
			`LOCALAPPDATA=C:\Users\fixture\AppData\Local`,
			`APPDATA=C:\Users\fixture\AppData\Roaming`,
			`appdata=C:\duplicate`,
		})
	if err == nil {
		t.Fatal("browser environment accepted a duplicate structural variable")
	}
}
