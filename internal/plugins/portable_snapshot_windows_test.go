//go:build windows

package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"cyberagent-workbench/internal/toolcontract"
)

func TestPortableSnapshotRejectsRealWindowsJunctionAtAcquisitionAndRead(t *testing.T) {
	pkg, source := portableSnapshotFixture(t)
	outside := t.TempDir()
	// Equal bytes prove the containment check, not just a differing digest.
	if err := os.WriteFile(filepath.Join(outside, "data.bin"), []byte{0, 0xff, 0x82, 0x0d, 0x0a}, 0o600); err != nil {
		t.Fatal(err)
	}
	link := func(name string) {
		t.Helper()
		command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", name, outside)
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("controlled fixture junction: %v %s", err, output)
		}
	}
	link(filepath.Join(source, "external"))
	if _, err := CapturePortableDirectory(context.Background(), source, "package", toolcontract.SourceRef{URI: "test:junction"}, t.TempDir()); err == nil {
		t.Fatal("junction silently materialized during acquisition")
	}
	reader, err := OpenPortableSnapshot(context.Background(), pkg.Snapshot, pkg.Archive(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := os.Rename(filepath.Join(reader.directory, "references"), filepath.Join(reader.directory, "original-references")); err != nil {
		t.Fatal(err)
	}
	link(filepath.Join(reader.directory, "references"))
	if _, _, err := reader.Read(context.Background(), pkg.Snapshot.Skills[0].Instructions.Component, "references/data.bin", 10); err == nil {
		t.Fatal("equal-byte external replacement escaped the held root")
	}
}
