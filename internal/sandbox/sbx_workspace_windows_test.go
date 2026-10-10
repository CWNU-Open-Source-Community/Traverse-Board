//go:build windows

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSBXWorkspaceRejectsWindowsJunctionBeforeVMCreation(t *testing.T) {
	for _, atRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested reparse point", true: "root alias"}[atRoot], func(t *testing.T) {
			b, fake, r := sbxFixture(t)
			outside := filepath.Join(filepath.Dir(r.DrydockRoot), "outside-directory")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(outside, "sentinel.txt")
			if err := os.WriteFile(marker, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(r.DrydockRoot, "escape")
			if atRoot {
				link = filepath.Join(filepath.Dir(r.DrydockRoot), "root-alias")
				outside = r.DrydockRoot
			}
			// Creating a directory junction needs neither Developer Mode nor
			// symlink privilege, so this regression does not skip on Windows CI.
			if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
				t.Fatalf("creating owned junction fixture: %v output=%s", err, output)
			}
			// Remove only the junction itself before t.TempDir cleanup.
			t.Cleanup(func() { _ = os.Remove(link) })
			if atRoot {
				r.DrydockRoot = link
			}
			sbxAssertWorkspaceRejectionBeforeDispatch(t, b, fake, r)
			if contents, err := os.ReadFile(marker); err != nil || string(contents) != "unchanged" {
				t.Fatalf("junction target changed: %q error=%v", contents, err)
			}
			if err := sbxValidateWorkspace(context.Background(), link); !errors.Is(err, ErrSBXBoundary) {
				t.Fatalf("direct preflight accepted junction root: %v", err)
			}
		})
	}
}
