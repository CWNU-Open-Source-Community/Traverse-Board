//go:build darwin && desktop

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinRiskRestartRejectsAParentOtherThanItsCreator(t *testing.T) {
	if _, err := prepareRiskRestartParent(os.Getpid()); err == nil ||
		!strings.Contains(err.Error(), "parent identity is invalid") {
		t.Fatalf("unrelated restart parent error = %v", err)
	}
}

func TestDarwinRiskRestartExecutableIdentityUsesTheSameFile(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processExecutable, err := darwinProcessExecutable(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !sameDarwinExecutable(executable, processExecutable) {
		t.Fatalf("proc_pidpath executable %q does not match %q", processExecutable, executable)
	}
	if !sameDarwinExecutable(executable, filepath.Clean(executable)) {
		t.Fatal("the same macOS executable was not recognized")
	}
	if sameDarwinExecutable(executable, filepath.Join(filepath.Dir(executable), "other")) {
		t.Fatal("a different macOS executable was accepted as the restart parent")
	}
}
