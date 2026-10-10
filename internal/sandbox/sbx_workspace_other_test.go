//go:build !windows

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSBXWorkspaceRejectsSymlinkBeforeVMCreation(t *testing.T) {
	for _, target := range []string{"nested/source-link.txt", "nested/directory-link", "root-alias"} {
		t.Run(target, func(t *testing.T) {
			b, fake, r := sbxFixture(t)
			outsideDir := filepath.Join(filepath.Dir(r.DrydockRoot), "outside-directory")
			if err := os.Mkdir(outsideDir, 0700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(outsideDir, "sentinel.txt")
			if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(r.DrydockRoot, filepath.FromSlash(target))
			linkedTarget := outside
			if target == "nested/directory-link" {
				linkedTarget = outsideDir
			} else if target == "root-alias" {
				link = filepath.Join(filepath.Dir(r.DrydockRoot), "root-alias")
				linkedTarget = r.DrydockRoot
			}
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(linkedTarget, link); err != nil {
				t.Fatal(err)
			}
			if target == "root-alias" {
				r.DrydockRoot = link
			}
			sbxAssertWorkspaceRejectionBeforeDispatch(t, b, fake, r)
			if contents, err := os.ReadFile(outside); err != nil || string(contents) != "unchanged" {
				t.Fatalf("symlink target changed: %q error=%v", contents, err)
			}
		})
	}
}
